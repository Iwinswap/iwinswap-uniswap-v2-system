package uniswapv2

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	ethclients "github.com/Iwinswap/iwinswap-ethclients"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/prometheus/client_golang/prometheus"
)

// Logger defines a standard interface for structured, leveled logging,
// compatible with the standard library's slog.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// --- Function Type Definitions for Dependencies ---

// These named types create a clear, maintainable contract for the system's dependencies.

type GetClientFunc func() (ethclients.ETHClient, error)
type InBlockedListFunc func(poolAddr common.Address) bool
type PoolInitializerFunc func(ctx context.Context, poolAddr []common.Address, client ethclients.ETHClient) (token0, token1 []common.Address, poolType []uint8, feeBps []uint16, reserve0, reserve1 []*big.Int, errs []error)
type DiscoverPoolsFunc func([]types.Log) ([]common.Address, error)
type UpdatedInBlockFunc func([]types.Log) (pools []common.Address, reserve0, reserve1 []*big.Int, err error)
type GetReservesFunc func(ctx context.Context, poolAddrs []common.Address, client ethclients.ETHClient) (reserve0, reserve1 []*big.Int, errs []error)
type AddressToIDFunc func(common.Address) (uint64, error)
type IDToAddressFunc func(uint64) (common.Address, error)
type RegisterPoolFunc func(token0, token1, poolAddr common.Address) (poolID uint64, err error)
type ErrorHandlerFunc func(err error)
type TestBloomFunc func(types.Bloom) bool

// Config holds all the dependencies and settings for the UniswapV2System.
// Using a configuration struct makes initialization cleaner and more extensible.
type Config struct {
	SystemName       string
	PrometheusReg    prometheus.Registerer
	NewBlockEventer  chan *types.Block
	GetClient        GetClientFunc
	InBlockedList    InBlockedListFunc
	PoolInitializer  PoolInitializerFunc
	DiscoverPools    DiscoverPoolsFunc
	UpdatedInBlock   UpdatedInBlockFunc
	GetReserves      GetReservesFunc
	TokenAddressToID AddressToIDFunc
	PoolAddressToID  AddressToIDFunc
	PoolIDToAddress  IDToAddressFunc
	RegisterPool     RegisterPoolFunc
	ErrorHandler     ErrorHandlerFunc
	TestBloom        TestBloomFunc
	PruneFrequency   time.Duration
	InitFrequency    time.Duration
	ResyncFrequency  time.Duration
	Logger           Logger
}

// validate checks that all essential fields in the Config are provided.
func (c *Config) validate() error {
	if c.SystemName == "" {
		return errors.New("system name is required")
	}
	if c.NewBlockEventer == nil {
		return errors.New("new block eventer channel is required")
	}
	if c.GetClient == nil {
		return errors.New("get client function is required")
	}
	if c.InBlockedList == nil {
		return errors.New("in blocked list function is required")
	}
	if c.PoolInitializer == nil {
		return errors.New("pool initializer function is required")
	}
	if c.DiscoverPools == nil {
		return errors.New("discover pools function is required")
	}
	if c.UpdatedInBlock == nil {
		return errors.New("updated in block function is required")
	}
	if c.GetReserves == nil {
		return errors.New("get reserves function is required")
	}
	if c.TokenAddressToID == nil {
		return errors.New("token address to id function is required")
	}
	if c.PoolAddressToID == nil {
		return errors.New("pool address to id function is required")
	}
	if c.PoolIDToAddress == nil {
		return errors.New("pool id to address function is required")
	}
	if c.RegisterPool == nil {
		return errors.New("register pool function is required")
	}
	if c.ErrorHandler == nil {
		return errors.New("error handler function is required")
	}
	if c.TestBloom == nil {
		return errors.New("test bloom function is required")
	}
	return nil
}

// UniswapV2System is the main orchestrator that connects the data registry
// to the live blockchain. It handles block events, discovers and updates pools,
// and manages state with thread-safety.
type UniswapV2System struct {
	systemName         string
	newBlockEventer    chan *types.Block
	getClient          GetClientFunc
	inBlockedList      InBlockedListFunc
	poolInitializer    PoolInitializerFunc
	discoverPools      DiscoverPoolsFunc
	updatedInBlock     UpdatedInBlockFunc
	getReserves        GetReservesFunc
	tokenAddressToID   AddressToIDFunc
	poolAddressToID    AddressToIDFunc
	registerPool       RegisterPoolFunc
	poolIDToAddress    IDToAddressFunc
	cachedView         atomic.Pointer[[]PoolView]
	lastUpdatedAtBlock uint64
	errorHandler       ErrorHandlerFunc
	testBloom          TestBloomFunc
	pruneFrequency     time.Duration
	initFrequency      time.Duration
	resyncFrequency    time.Duration
	pendingInit        map[common.Address]struct{}
	mu                 sync.RWMutex
	registry           *UniswapV2Registry
	metrics            *Metrics
	logger             Logger
}

// NewUniswapV2System constructs and returns a new, fully initialized system.
// It starts all background goroutines, making it a self-contained, "live" service upon creation.
func NewUniswapV2System(ctx context.Context, cfg *Config) (*UniswapV2System, error) {
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid uniswapv2 system configuration: %w", err)
	}

	if cfg.Logger == nil {
		// default to a silent logger.
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	metrics := NewMetrics(cfg.PrometheusReg)

	system := &UniswapV2System{
		systemName:       cfg.SystemName,
		newBlockEventer:  cfg.NewBlockEventer,
		getClient:        cfg.GetClient,
		inBlockedList:    cfg.InBlockedList,
		poolInitializer:  cfg.PoolInitializer,
		discoverPools:    cfg.DiscoverPools,
		updatedInBlock:   cfg.UpdatedInBlock,
		getReserves:      cfg.GetReserves,
		tokenAddressToID: cfg.TokenAddressToID,
		poolAddressToID:  cfg.PoolAddressToID,
		poolIDToAddress:  cfg.PoolIDToAddress,
		registerPool:     cfg.RegisterPool,
		errorHandler: func(err error) {
			errorType := determineErrorType(err)
			cfg.Logger.Error("UniswapV2System internal error", "system", cfg.SystemName, "type", errorType, "error", err)
			metrics.ErrorsTotal.WithLabelValues(cfg.SystemName, errorType).Inc()

			// 3. Call the user's external handler.
			cfg.ErrorHandler(err)
		},
		testBloom:          cfg.TestBloom,
		pruneFrequency:     cfg.PruneFrequency,
		initFrequency:      cfg.InitFrequency,
		resyncFrequency:    cfg.ResyncFrequency,
		registry:           NewUniswapV2Registry(),
		pendingInit:        make(map[common.Address]struct{}),
		lastUpdatedAtBlock: 0,
		metrics:            metrics,
		logger:             cfg.Logger,
	}

	system.cachedView.Store(&[]PoolView{})
	system.logger.Info("UniswapV2System started", "system", system.systemName)
	go system.listenBlockEventer(ctx)
	go system.startPruner(ctx)
	go system.startInitializer(ctx)
	go system.startStateReconciler(ctx)

	return system, nil
}

// View returns a copy of the latest registry view. This operation is lock-free.
func (s *UniswapV2System) View() []PoolView {
	viewPtr := s.cachedView.Load()
	if viewPtr == nil {
		return nil
	}
	view := *viewPtr
	viewCopy := make([]PoolView, len(view))
	copy(viewCopy, view)
	return viewCopy
}

// LastUpdatedAtBlock returns the block number of the last successfully processed block.
func (s *UniswapV2System) LastUpdatedAtBlock() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastUpdatedAtBlock
}

// updateCachedView generates a fresh view from the registry and atomically updates the pointer.
// This method MUST be called from within a write lock (s.mu.Lock).
func (s *UniswapV2System) updateCachedView() {
	newView := viewRegistry(s.registry)
	s.cachedView.Store(&newView)
	s.metrics.PoolsInRegistry.WithLabelValues(s.systemName).Set(float64(len(newView)))
}

// listenBlockEventer is the main event loop for the system.
func (s *UniswapV2System) listenBlockEventer(ctx context.Context) {
	for {
		select {
		case b := <-s.newBlockEventer:
			timer := prometheus.NewTimer(s.metrics.BlockProcessingDur.WithLabelValues(s.systemName))

			if !s.testBloom(b.Bloom()) {
				s.mu.Lock()
				s.lastUpdatedAtBlock = b.NumberU64()
				s.mu.Unlock()
				s.metrics.LastProcessedBlock.WithLabelValues(s.systemName).Set(float64(b.NumberU64()))
				timer.ObserveDuration()
				continue
			}
			if err := s.handleNewBlock(ctx, b); err != nil {
				s.errorHandler(err)
			}
			timer.ObserveDuration()
		case <-ctx.Done():
			s.logger.Info("UniswapV2System stopping due to context cancellation.")
			return
		}
	}
}

// handleNewBlock processes a single block, performs fast synchronous updates,
// and queues slow pool initializations for asynchronous processing.
func (s *UniswapV2System) handleNewBlock(ctx context.Context, b *types.Block) error {
	blockNum := b.NumberU64()
	s.logger.Debug("Processing new block", "blockNumber", blockNum, "tx_count", len(b.Transactions()))
	client, err := s.getClient()
	if err != nil {
		return fmt.Errorf("block %d: failed to get eth client: %w", blockNum, err)
	}
	logs, err := client.FilterLogs(ctx, ethereum.FilterQuery{FromBlock: b.Number(), ToBlock: b.Number()})
	if err != nil {
		return fmt.Errorf("block %d: failed to filter logs: %w", blockNum, err)
	}

	updatedPoolAddrs, updatedReserve0s, updatedReserve1s, err := s.updatedInBlock(logs)
	if err != nil {
		s.errorHandler(&SystemError{BlockNumber: blockNum, Err: fmt.Errorf("failed to parse updated pools: %w", err)})
	}
	discoveredPoolAddrs, err := s.discoverPools(logs)
	if err != nil {
		s.errorHandler(&SystemError{BlockNumber: blockNum, Err: fmt.Errorf("failed to discover pools: %w", err)})
	}

	if len(discoveredPoolAddrs) > 0 {
		s.logger.Info(
			"Discovered new pools in block",
			"blockNumber", blockNum,
			"count", len(discoveredPoolAddrs),
		)
	}

	var capturedErrors []error
	var newPendingCount int
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		updateErrors := s.applyUpdates(blockNum, updatedPoolAddrs, updatedReserve0s, updatedReserve1s)
		capturedErrors = append(capturedErrors, updateErrors...)

		for _, poolAddr := range discoveredPoolAddrs {
			if s.inBlockedList(poolAddr) {
				continue
			}
			if _, err := s.poolAddressToID(poolAddr); err == nil {
				continue
			}
			if _, exists := s.pendingInit[poolAddr]; !exists {
				s.pendingInit[poolAddr] = struct{}{}
				newPendingCount++
			}
		}

		s.lastUpdatedAtBlock = blockNum
		s.updateCachedView()
	}()

	s.metrics.LastProcessedBlock.WithLabelValues(s.systemName).Set(float64(blockNum))
	if newPendingCount > 0 {
		s.metrics.PendingInitQueueSize.WithLabelValues(s.systemName).Add(float64(newPendingCount))
	}
	for _, e := range capturedErrors {
		s.errorHandler(e)
	}
	return nil
}

// startInitializer is a background process that periodically initializes pools from the pending queue.
func (s *UniswapV2System) startInitializer(ctx context.Context) {
	if s.initFrequency <= 0 {
		return
	}
	ticker := time.NewTicker(s.initFrequency)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.runPendingInitializations(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// runPendingInitializations drains the pending queue and processes the new pools in a batch.
func (s *UniswapV2System) runPendingInitializations(ctx context.Context) {
	timer := prometheus.NewTimer(s.metrics.PoolInitDur.WithLabelValues(s.systemName))
	defer timer.ObserveDuration()

	var poolsToInit []common.Address
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.pendingInit) > 0 {
			poolsToInit = make([]common.Address, 0, len(s.pendingInit))
			for addr := range s.pendingInit {
				poolsToInit = append(poolsToInit, addr)
			}
			s.pendingInit = make(map[common.Address]struct{})
		}
	}()

	s.metrics.PendingInitQueueSize.WithLabelValues(s.systemName).Set(0)
	if len(poolsToInit) == 0 {
		return
	}

	s.logger.Info("Running pool initializer", "count", len(poolsToInit))

	client, err := s.getClient()
	if err != nil {
		s.errorHandler(fmt.Errorf("initializer: failed to get eth client: %w", err))
		return
	}
	token0s, token1s, poolTypes, feeBps, reserve0s, reserve1s, errs := s.poolInitializer(ctx, poolsToInit, client)

	var initErrors []error
	var successfulInits int
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		initErrors = s.applyInitializations(0, poolsToInit, token0s, token1s, poolTypes, feeBps, reserve0s, reserve1s, errs)
		successfulInits = len(poolsToInit) - len(initErrors)

		if successfulInits > 0 {
			s.updateCachedView()
		}
	}()

	if successfulInits > 0 {
		s.logger.Info(
			"Successfully initialized new pools",
			"count", successfulInits,
			"failed", len(initErrors),
		)
		s.metrics.PoolsInitialized.WithLabelValues(s.systemName).Add(float64(successfulInits))
	}
	for _, e := range initErrors {
		s.errorHandler(e)
	}
}

// startStateReconciler is a background process that periodically re-fetches pool states
// to correct for any missed events or state drift, making the system self-healing.
func (s *UniswapV2System) startStateReconciler(ctx context.Context) {
	if s.resyncFrequency <= 0 {
		return
	}
	ticker := time.NewTicker(s.resyncFrequency)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.runStateReconciliation(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// runStateReconciliation performs a single cycle of fetching on-chain reserves for all known
// pools and updating the local state if any discrepancies are found.
func (s *UniswapV2System) runStateReconciliation(ctx context.Context) {
	currentView := s.View()
	if len(currentView) == 0 {
		return
	}

	client, err := s.getClient()
	if err != nil {
		s.errorHandler(fmt.Errorf("reconciler: failed to get eth client: %w", err))
		return
	}

	currentReserves := make(map[uint64]PoolView, len(currentView))
	poolAddrs := make([]common.Address, 0, len(currentView))
	poolIDs := make([]uint64, 0, len(currentView))
	for _, view := range currentView {
		addr, err := s.poolIDToAddress(view.ID)
		if err != nil {
			s.errorHandler(&PrunerError{PoolID: view.ID, Err: fmt.Errorf("reconciler could not get address: %w", err)})
			continue
		}
		poolAddrs = append(poolAddrs, addr)
		poolIDs = append(poolIDs, view.ID)
		currentReserves[view.ID] = view
	}

	freshReserve0s, freshReserve1s, errs := s.getReserves(ctx, poolAddrs, client)

	var updatesToApply []struct {
		poolID   uint64
		reserve0 *big.Int
		reserve1 *big.Int
	}
	for i := 0; i < len(poolAddrs); i++ {
		if errs[i] != nil {
			s.errorHandler(fmt.Errorf("reconciler failed to get reserves for pool %s: %w", poolAddrs[i].Hex(), errs[i]))
			continue
		}

		poolID := poolIDs[i]
		current := currentReserves[poolID]
		freshR0 := freshReserve0s[i]
		freshR1 := freshReserve1s[i]

		if current.Reserve0.Cmp(freshR0) != 0 || current.Reserve1.Cmp(freshR1) != 0 {
			updatesToApply = append(updatesToApply, struct {
				poolID   uint64
				reserve0 *big.Int
				reserve1 *big.Int
			}{poolID: poolID, reserve0: freshR0, reserve1: freshR1})
		}
	}

	if len(updatesToApply) > 0 {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.logger.Info(
			"Reconciler found state drift, applying updates",
			"pools_to_update", len(updatesToApply),
		)
		for _, update := range updatesToApply {
			// A block number of 0 signifies a reconciliation update, not an event-driven one.
			if err := updatePool(update.reserve0, update.reserve1, update.poolID, s.registry); err != nil {
				s.errorHandler(&UpdateError{SystemError: SystemError{BlockNumber: 0, Err: err}, PoolID: update.poolID})
			}
		}
		s.updateCachedView()
	}
}

// applyUpdates handles updating reserves for existing pools. This method must be called within a write lock.
func (s *UniswapV2System) applyUpdates(blockNumber uint64, updatedPoolAddrs []common.Address, updatedReserve0s, updatedReserve1s []*big.Int) []error {
	var capturedErrors []error
	for i, poolAddr := range updatedPoolAddrs {
		poolID, err := s.poolAddressToID(poolAddr)
		if err != nil {
			continue // Pool not in registry, possibly pruned or pending initialization.
		}
		if err = updatePool(updatedReserve0s[i], updatedReserve1s[i], poolID, s.registry); err != nil {
			capturedErrors = append(capturedErrors, &UpdateError{
				SystemError: SystemError{BlockNumber: blockNumber, Err: err},
				PoolAddress: poolAddr,
				PoolID:      poolID,
				Reserve0:    updatedReserve0s[i],
				Reserve1:    updatedReserve1s[i],
			})
		}
	}
	return capturedErrors
}

// applyInitializations handles adding newly discovered pools to the registry. This method must be called within a write lock.
func (s *UniswapV2System) applyInitializations(
	blockNumber uint64,
	unknownPools, initToken0s, initToken1s []common.Address,
	initPoolTypes []uint8,
	initFeeBps []uint16,
	initReserve0s, initReserve1s []*big.Int,
	initErrs []error,
) []error {
	var capturedErrors []error
	if len(unknownPools) == 0 {
		return nil
	}

	for i, poolAddr := range unknownPools {
		if initErrs[i] != nil {
			capturedErrors = append(capturedErrors, &InitializationError{
				SystemError: SystemError{BlockNumber: blockNumber, Err: initErrs[i]},
				PoolAddress: poolAddr,
			})
			continue
		}

		_, err := s.registerPool(initToken0s[i], initToken1s[i], poolAddr)
		if err != nil {
			capturedErrors = append(capturedErrors, &RegistrationError{
				InitializationError: InitializationError{
					SystemError: SystemError{BlockNumber: blockNumber, Err: err},
					PoolAddress: poolAddr,
				},
				Token0Address: initToken0s[i],
				Token1Address: initToken1s[i],
			})
			continue
		}

		err = addPool(initToken0s[i], initToken1s[i], poolAddr, initPoolTypes[i], initFeeBps[i], s.tokenAddressToID, s.poolAddressToID, s.registry)
		if err != nil {
			capturedErrors = append(capturedErrors, &InitializationError{
				SystemError: SystemError{BlockNumber: blockNumber, Err: err},
				PoolAddress: poolAddr,
			})
			continue
		}

		poolID, err := s.poolAddressToID(poolAddr)
		if err != nil {
			capturedErrors = append(capturedErrors, &DataConsistencyError{
				SystemError: SystemError{BlockNumber: blockNumber, Err: err},
				PoolAddress: poolAddr,
				Details:     "failed to get ID for newly added pool",
			})
			continue
		}
		if err := updatePool(initReserve0s[i], initReserve1s[i], poolID, s.registry); err != nil {
			capturedErrors = append(capturedErrors, &InitializationError{
				SystemError: SystemError{
					BlockNumber: blockNumber,
					Err:         fmt.Errorf("failed to set initial reserves: %w", err),
				},
				PoolAddress: poolAddr,
			})
		}
	}
	return capturedErrors
}

// startPruner is a background process that periodically removes blocked pools from the registry.
func (s *UniswapV2System) startPruner(ctx context.Context) {
	if s.pruneFrequency <= 0 {
		return
	}
	ticker := time.NewTicker(s.pruneFrequency)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.pruneBlockedPools()
		case <-ctx.Done():
			return
		}
	}
}

// pruneBlockedPools scans the registry for pools that should no longer be tracked and removes them.
func (s *UniswapV2System) pruneBlockedPools() {
	currentView := s.View()
	if len(currentView) == 0 {
		return
	}

	var capturedErrors []error
	var poolsToDelete []uint64
	for _, poolView := range currentView {
		poolAddr, err := s.poolIDToAddress(poolView.ID)
		if err != nil {
			capturedErrors = append(capturedErrors, &PrunerError{PoolID: poolView.ID, Err: fmt.Errorf("could not get address: %w", err)})
			continue
		}
		if s.inBlockedList(poolAddr) {
			poolsToDelete = append(poolsToDelete, poolView.ID)
		}
	}

	if len(poolsToDelete) > 0 {
		s.mu.Lock()
		s.logger.Info("Pruner removing blocked pools", "count", len(poolsToDelete))
		for _, poolID := range poolsToDelete {
			if err := deletePool(poolID, s.registry); err != nil {
				capturedErrors = append(capturedErrors, &PrunerError{PoolID: poolID, Err: fmt.Errorf("failed to delete from registry: %w", err)})
			}
		}
		s.updateCachedView()
		s.mu.Unlock()
	}

	for _, err := range capturedErrors {
		s.errorHandler(err)
	}
}
