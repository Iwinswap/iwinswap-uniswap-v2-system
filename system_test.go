package uniswapv2

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ethclients "github.com/Iwinswap/iwinswap-ethclients"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock Infrastructure ---

// mockPersistence simulates the upstream database or service that stores permanent ID mappings.
type mockPersistence struct {
	mu             sync.Mutex
	tokenCounter   uint64
	poolCounter    uint64
	tokens         map[common.Address]uint64
	pools          map[common.Address]uint64
	idToPool       map[uint64]common.Address
	failOnRegister bool
}

func newMockPersistence() *mockPersistence {
	return &mockPersistence{
		tokenCounter: 100,
		poolCounter:  1000,
		tokens:       make(map[common.Address]uint64),
		idToPool:     make(map[uint64]common.Address),
		pools:        make(map[common.Address]uint64),
	}
}

func (p *mockPersistence) TokenAddressToID(addr common.Address) (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id, ok := p.tokens[addr]; ok {
		return id, nil
	}
	// Simulate adding a new token if it doesn't exist, as the initializer would need it.
	p.tokenCounter++
	p.tokens[addr] = p.tokenCounter
	return p.tokenCounter, nil
}

func (p *mockPersistence) PoolAddressToID(addr common.Address) (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id, ok := p.pools[addr]; ok {
		return id, nil
	}
	return 0, errors.New("mock: pool not found")
}

func (p *mockPersistence) PoolIDToAddress(id uint64) (common.Address, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if addr, ok := p.idToPool[id]; ok {
		return addr, nil
	}
	return common.Address{}, errors.New("mock: pool ID not found")
}

func (p *mockPersistence) RegisterPool(t0, t1, poolAddr common.Address) (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failOnRegister {
		return 0, errors.New("mock: forced registration failure")
	}
	if _, ok := p.pools[poolAddr]; ok {
		return p.pools[poolAddr], nil
	}
	p.poolCounter++
	id := p.poolCounter
	p.pools[poolAddr] = id
	p.idToPool[id] = poolAddr
	return id, nil
}

// --- Test Setup Helper ---

type systemTestConfig struct {
	inBlockedList   func(poolAddr common.Address) bool
	poolInitializer PoolInitializerFunc
	discoverPools   DiscoverPoolsFunc
	updatedInBlock  UpdatedInBlockFunc
	getReserves     GetReservesFunc // For reconciler
	testBloom       TestBloomFunc
	pruneFrequency  time.Duration
	initFrequency   time.Duration
	resyncFrequency time.Duration // For reconciler
}

type testSystem struct {
	System       *UniswapV2System
	Persistence  *mockPersistence
	TestClient   *ethclients.TestETHClient
	BlockEventer chan *types.Block
	cancel       context.CancelFunc

	// errorMu protects capturedErrors
	errorMu        sync.Mutex
	capturedErrors []error
}

// AddError safely adds an error to the capturedErrors slice.
func (ts *testSystem) AddError(err error) {
	ts.errorMu.Lock()
	defer ts.errorMu.Unlock()
	ts.capturedErrors = append(ts.capturedErrors, err)
}

// GetErrors safely returns a copy of the captured errors.
func (ts *testSystem) GetErrors() []error {
	ts.errorMu.Lock()
	defer ts.errorMu.Unlock()
	// Return a copy to prevent race conditions on the slice itself
	// if the caller modifies it or holds it while other goroutines add errors.
	errsCopy := make([]error, len(ts.capturedErrors))
	copy(errsCopy, ts.capturedErrors)
	return errsCopy
}

func testSetupSystem(t *testing.T, cfg *systemTestConfig) *testSystem {
	ctx, cancel := context.WithCancel(context.Background())

	// Create the testSystem instance first, so its methods can be used in closures.
	ts := &testSystem{
		Persistence:  newMockPersistence(),
		TestClient:   ethclients.NewTestETHClient(),
		BlockEventer: make(chan *types.Block, 50),
		cancel:       cancel,
	}

	if cfg == nil {
		cfg = &systemTestConfig{}
	}

	// Set default implementations for all dependencies.
	inBlockedListFunc := cfg.inBlockedList
	if inBlockedListFunc == nil {
		inBlockedListFunc = func(poolAddr common.Address) bool { return false }
	}
	poolInitializerFunc := cfg.poolInitializer
	if poolInitializerFunc == nil {
		poolInitializerFunc = func(ctx context.Context, poolAddrs []common.Address, client ethclients.ETHClient) (token0s, token1s []common.Address, poolTypes []uint8, feeBps []uint16, reserve0s, reserve1s []*big.Int, errs []error) {
			token0s, token1s = make([]common.Address, len(poolAddrs)), make([]common.Address, len(poolAddrs))
			poolTypes, feeBps = make([]uint8, len(poolAddrs)), make([]uint16, len(poolAddrs))
			reserve0s, reserve1s, errs = make([]*big.Int, len(poolAddrs)), make([]*big.Int, len(poolAddrs)), make([]error, len(poolAddrs))
			for i, addr := range poolAddrs {
				var t0, t1 common.Address
				copy(t0[:], addr[:])
				t0[0] = 'a'
				copy(t1[:], addr[:])
				t1[0] = 'b'
				token0s[i], token1s[i], poolTypes[i], feeBps[i], reserve0s[i], reserve1s[i] = t0, t1, 0, 30, big.NewInt(100), big.NewInt(100)
			}
			return
		}
	}
	getReservesFunc := cfg.getReserves
	if getReservesFunc == nil {
		getReservesFunc = func(ctx context.Context, poolAddrs []common.Address, client ethclients.ETHClient) (reserve0, reserve1 []*big.Int, errs []error) {
			reserve0, reserve1, errs = make([]*big.Int, len(poolAddrs)), make([]*big.Int, len(poolAddrs)), make([]error, len(poolAddrs))
			for i := range poolAddrs {
				reserve0[i], reserve1[i] = big.NewInt(100), big.NewInt(100) // Default to initial reserves
			}
			return
		}
	}
	discoverPoolsFunc := cfg.discoverPools
	if discoverPoolsFunc == nil {
		discoverPoolsFunc = func(logs []types.Log) ([]common.Address, error) { return nil, nil }
	}
	updatedInBlockFunc := cfg.updatedInBlock
	if updatedInBlockFunc == nil {
		updatedInBlockFunc = func(logs []types.Log) (pools []common.Address, reserve0, reserve1 []*big.Int, err error) {
			return nil, nil, nil, nil
		}
	}
	testBloomFunc := cfg.testBloom
	if testBloomFunc == nil {
		testBloomFunc = func(b types.Bloom) bool { return true }
	}
	errorHandler := func(err error) {
		ts.AddError(err)
	}

	reg := prometheus.NewRegistry()

	sys, err := NewUniswapV2System(ctx, "test_system", reg, ts.BlockEventer,
		func() (ethclients.ETHClient, error) { return ts.TestClient, nil },
		inBlockedListFunc, poolInitializerFunc, discoverPoolsFunc, updatedInBlockFunc, getReservesFunc,
		ts.Persistence.TokenAddressToID, ts.Persistence.PoolAddressToID, ts.Persistence.PoolIDToAddress,
		ts.Persistence.RegisterPool, errorHandler, testBloomFunc,
		cfg.pruneFrequency, cfg.initFrequency, cfg.resyncFrequency,
	)
	require.NoError(t, err)

	ts.System = sys // Assign the system back to the struct

	return ts
}

// --- Test Helper Functions ---

func testNewBlock(number uint64) *types.Block {
	return types.NewBlock(&types.Header{Number: big.NewInt(int64(number))}, nil, nil, nil)
}

// --- Test Suite ---

func TestUniswapV2System(t *testing.T) {
	addr1 := common.HexToAddress("0x1")
	addr2 := common.HexToAddress("0x2")

	// Previous tests are maintained...
	t.Run("HappyPathInitialization", func(t *testing.T) {
		cfg := &systemTestConfig{
			initFrequency: 10 * time.Millisecond,
			discoverPools: func(logs []types.Log) ([]common.Address, error) {
				if len(logs) > 0 {
					if logs[0].BlockNumber == 1 {
						return []common.Address{addr1}, nil
					}
					if logs[0].BlockNumber == 2 {
						return []common.Address{addr2}, nil
					}
				}
				return nil, nil
			},
		}
		ts := testSetupSystem(t, cfg)
		defer ts.cancel()

		ts.TestClient.SetFilterLogsHandler(func(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
			return []types.Log{{BlockNumber: q.FromBlock.Uint64()}}, nil
		})

		ts.BlockEventer <- testNewBlock(1)
		require.Eventually(t, func() bool { return len(ts.System.View()) == 1 }, time.Second, 5*time.Millisecond, "pool 1 should be initialized")

		ts.BlockEventer <- testNewBlock(2)
		require.Eventually(t, func() bool { return len(ts.System.View()) == 2 }, time.Second, 5*time.Millisecond, "pool 2 should be initialized")
		assert.Empty(t, ts.GetErrors())
	})

	t.Run("FailureInitialization", func(t *testing.T) {
		failingAddr := common.HexToAddress("0xdead")
		expectedErr := errors.New("forced initializer failure")
		cfg := &systemTestConfig{
			initFrequency: 10 * time.Millisecond,
			discoverPools: func(logs []types.Log) ([]common.Address, error) {
				return []common.Address{failingAddr}, nil
			},
			poolInitializer: func(ctx context.Context, poolAddrs []common.Address, client ethclients.ETHClient) (token0s []common.Address, token1s []common.Address, poolTypes []uint8, feeBps []uint16, reserve0s []*big.Int, reserve1s []*big.Int, errs []error) {
				errs = make([]error, len(poolAddrs))
				for i := range poolAddrs {
					errs[i] = expectedErr
				}
				return nil, nil, nil, nil, nil, nil, errs
			},
		}
		ts := testSetupSystem(t, cfg)
		defer ts.cancel()
		ts.BlockEventer <- testNewBlock(1)
		require.Eventually(t, func() bool { return len(ts.GetErrors()) > 0 }, time.Second, 5*time.Millisecond, "error should be captured")
		var initErr *InitializationError
		require.ErrorAs(t, ts.GetErrors()[0], &initErr)
		assert.ErrorIs(t, initErr.Err, expectedErr)
		assert.Len(t, ts.System.View(), 0)
	})

	t.Run("Pruner_RemovesBlockedPool", func(t *testing.T) {
		isBlocked := &atomic.Bool{}
		cfg := &systemTestConfig{
			pruneFrequency: 20 * time.Millisecond,
			initFrequency:  10 * time.Millisecond,
			discoverPools:  func(logs []types.Log) ([]common.Address, error) { return []common.Address{addr1}, nil },
			inBlockedList: func(poolAddr common.Address) bool {
				return isBlocked.Load() && poolAddr == addr1
			},
		}
		ts := testSetupSystem(t, cfg)
		defer ts.cancel()

		isBlocked.Store(false)
		ts.BlockEventer <- testNewBlock(1)
		require.Eventually(t, func() bool { return len(ts.System.View()) == 1 }, time.Second, 10*time.Millisecond, "pool should be added before pruning")

		isBlocked.Store(true)
		require.Eventually(t, func() bool { return len(ts.System.View()) == 0 }, 100*time.Millisecond, 10*time.Millisecond, "pruner should remove blocked pool")
	})

	t.Run("RaceCondition_UpdateAndDiscoverInSameBlock", func(t *testing.T) {
		cfg := &systemTestConfig{
			initFrequency: 10 * time.Millisecond,
			discoverPools: func(logs []types.Log) ([]common.Address, error) { return []common.Address{addr1}, nil },
			updatedInBlock: func(logs []types.Log) (pools []common.Address, reserve0, reserve1 []*big.Int, err error) {
				return []common.Address{addr1}, []*big.Int{big.NewInt(500)}, []*big.Int{big.NewInt(500)}, nil
			},
		}
		ts := testSetupSystem(t, cfg)
		defer ts.cancel()

		ts.BlockEventer <- testNewBlock(1)
		require.Eventually(t, func() bool { return ts.System.LastUpdatedAtBlock() == 1 }, time.Second, 5*time.Millisecond)
		require.Eventually(t, func() bool { return len(ts.System.View()) == 1 }, time.Second, 5*time.Millisecond, "pool should be initialized despite same-block update")
		assert.Empty(t, ts.GetErrors(), "No error should be captured for premature update")

		view := ts.System.View()
		assert.Equal(t, big.NewInt(100), view[0].Reserve0, "Reserves should be from the initializer, not the premature update")
	})

	t.Run("RaceCondition_InitializeVsPrune", func(t *testing.T) {
		isBlocked := &atomic.Bool{}
		initializerAttempted := &atomic.Bool{}
		defaultInitializer := func(ctx context.Context, poolAddrs []common.Address, client ethclients.ETHClient) (token0s, token1s []common.Address, poolTypes []uint8, feeBps []uint16, reserve0s, reserve1s []*big.Int, errs []error) {
			token0s, token1s = make([]common.Address, len(poolAddrs)), make([]common.Address, len(poolAddrs))
			poolTypes, feeBps = make([]uint8, len(poolAddrs)), make([]uint16, len(poolAddrs))
			reserve0s, reserve1s, errs = make([]*big.Int, len(poolAddrs)), make([]*big.Int, len(poolAddrs)), make([]error, len(poolAddrs))
			for i, addr := range poolAddrs {
				var t0, t1 common.Address
				copy(t0[:], addr[:])
				t0[0] = 'a'
				copy(t1[:], addr[:])
				t1[0] = 'b'
				token0s[i], token1s[i], poolTypes[i], feeBps[i], reserve0s[i], reserve1s[i] = t0, t1, 0, 30, big.NewInt(100), big.NewInt(100)
			}
			return
		}

		cfg := &systemTestConfig{
			pruneFrequency: 5 * time.Millisecond,
			initFrequency:  5 * time.Millisecond,
			discoverPools:  func(logs []types.Log) ([]common.Address, error) { return []common.Address{addr1}, nil },
			inBlockedList: func(poolAddr common.Address) bool {
				return isBlocked.Load() && poolAddr == addr1
			},
			poolInitializer: func(ctx context.Context, poolAddrs []common.Address, client ethclients.ETHClient) (token0s []common.Address, token1s []common.Address, poolTypes []uint8, feeBps []uint16, reserve0s []*big.Int, reserve1s []*big.Int, errs []error) {
				initializerAttempted.Store(true)
				return defaultInitializer(ctx, poolAddrs, client)
			},
		}
		ts := testSetupSystem(t, cfg)
		defer ts.cancel()

		isBlocked.Store(false)
		ts.BlockEventer <- testNewBlock(1)
		require.Eventually(t, func() bool { return ts.System.LastUpdatedAtBlock() == 1 }, time.Second, 5*time.Millisecond)
		isBlocked.Store(true)

		require.Eventually(t, func() bool {
			return initializerAttempted.Load() && len(ts.System.View()) == 0
		}, 2*time.Second, 10*time.Millisecond, "system should eventually settle with an empty view after initialization attempt")
	})

	t.Run("StateReconciliation_CorrectsDrift", func(t *testing.T) {
		initialReserve := big.NewInt(100)
		correctReserve := big.NewInt(999)
		reconcilerShouldFix := &atomic.Bool{}

		cfg := &systemTestConfig{
			initFrequency:   10 * time.Millisecond,
			resyncFrequency: 15 * time.Millisecond,
			discoverPools:   func(logs []types.Log) ([]common.Address, error) { return []common.Address{addr1}, nil },
			updatedInBlock: func(logs []types.Log) (pools []common.Address, reserve0, reserve1 []*big.Int, err error) {
				return nil, nil, nil, nil // Simulate missing Sync events.
			},
			getReserves: func(ctx context.Context, poolAddrs []common.Address, client ethclients.ETHClient) (reserve0, reserve1 []*big.Int, errs []error) {
				reserve0 = make([]*big.Int, len(poolAddrs))
				reserve1 = make([]*big.Int, len(poolAddrs))
				errs = make([]error, len(poolAddrs))
				for i := range poolAddrs {
					if reconcilerShouldFix.Load() {
						// After the flag is flipped, return the "correct" on-chain reserves.
						reserve0[i] = correctReserve
						reserve1[i] = correctReserve
					} else {
						// Initially, return the same "stale" reserves as the initializer.
						reserve0[i] = initialReserve
						reserve1[i] = initialReserve
					}
				}
				return
			},
		}
		ts := testSetupSystem(t, cfg)
		defer ts.cancel()

		// Step 1: Discover and initialize the pool.
		ts.BlockEventer <- testNewBlock(1)
		require.Eventually(t, func() bool { return len(ts.System.View()) == 1 }, time.Second, 5*time.Millisecond, "pool should be initialized")

		// Step 2: Verify its initial, possibly stale state. The reconciler should have run but found no diff.
		view := ts.System.View()
		require.Len(t, view, 1)
		assert.Equal(t, 0, initialReserve.Cmp(view[0].Reserve0), "Pool should have initial reserve of 100")

		// Step 3: Flip the flag to simulate the on-chain state changing, triggering the next reconciliation.
		reconcilerShouldFix.Store(true)

		// Step 4: Wait for the reconciler to run again and correct the state.
		require.Eventually(t, func() bool {
			latestView := ts.System.View()
			if len(latestView) == 0 {
				return false
			}
			return latestView[0].Reserve0.Cmp(correctReserve) == 0
		}, 2*time.Second, 10*time.Millisecond, "reconciler should have corrected the reserves to 999")

		assert.Empty(t, ts.GetErrors())
	})
}
