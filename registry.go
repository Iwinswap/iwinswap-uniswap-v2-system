package uniswapv2

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

var (
	// ErrPoolExists is returned when attempting to add a pool that is already in the registry.
	ErrPoolExists = errors.New("pool already exists in registry")
	// ErrPoolNotFound is returned when attempting to access a pool that is not in the registry.
	ErrPoolNotFound = errors.New("pool not found in registry")
)

// DependencyError is returned when a function dependency required by the registry fails.
type DependencyError struct {
	// Dependency is the name of the function that failed (e.g., "poolAddressToID").
	Dependency string
	// Input is the value that was passed to the failing dependency.
	Input any
	// Err is the underlying error returned by the dependency.
	Err error
}

func (e *DependencyError) Error() string {
	return fmt.Sprintf("registry dependency '%s' failed for input '%v': %v", e.Dependency, e.Input, e.Err)
}

// Unwrap allows the error to be inspected with errors.Is and errors.As.
func (e *DependencyError) Unwrap() error {
	return e.Err
}

type PoolView struct {
	ID       uint64
	Token0   uint64
	Token1   uint64
	Reserve0 *big.Int
	Reserve1 *big.Int
	Type     uint8
	FeeBps   uint16 // i.e 30 for 0.3%
}

// UniswapV2Registry manages a large number of v2 pools using a data-oriented design.
type UniswapV2Registry struct {
	id       []uint64
	token0   []uint64
	token1   []uint64
	reserve0 []*big.Int
	reserve1 []*big.Int
	pooltype []uint8
	fee      []uint16

	// --- Mapping layers to separate logical ID from physical index ---
	idToIndex map[uint64]int // Maps a permanent ID to its current slice index
}

func NewUniswapV2Registry() *UniswapV2Registry {
	return &UniswapV2Registry{
		idToIndex: make(map[uint64]int),
	}
}

func addPool(
	token0, token1,
	pool common.Address,
	poolType uint8,
	feeBps uint16,
	tokenAddressToID func(common.Address) (uint64, error),
	poolAddressToID func(common.Address) (uint64, error),
	registry *UniswapV2Registry,
) error {
	poolID, err := poolAddressToID(pool)
	if err != nil {
		return &DependencyError{
			Dependency: "poolAddressToID",
			Input:      pool,
			Err:        err,
		}
	}

	if _, ok := registry.idToIndex[poolID]; ok {
		return ErrPoolExists
	}

	tokenID0, err := tokenAddressToID(token0)
	if err != nil {
		return &DependencyError{
			Dependency: "tokenAddressToID",
			Input:      token0,
			Err:        err,
		}
	}

	tokenID1, err := tokenAddressToID(token1)
	if err != nil {
		return &DependencyError{
			Dependency: "tokenAddressToID",
			Input:      token1,
			Err:        err,
		}
	}

	registry.id = append(registry.id, poolID)
	registry.token0 = append(registry.token0, tokenID0)
	registry.token1 = append(registry.token1, tokenID1)
	registry.reserve0 = append(registry.reserve0, big.NewInt(0))
	registry.reserve1 = append(registry.reserve1, big.NewInt(0))
	registry.pooltype = append(registry.pooltype, poolType)
	registry.fee = append(registry.fee, feeBps)

	newIndex := len(registry.id) - 1
	registry.idToIndex[poolID] = newIndex

	return nil
}

func updatePool(
	reserve0 *big.Int,
	reserve1 *big.Int,
	poolID uint64,
	registry *UniswapV2Registry,
) error {
	index, ok := registry.idToIndex[poolID]
	if !ok {
		return ErrPoolNotFound
	}

	registry.reserve0[index].Set(reserve0)
	registry.reserve1[index].Set(reserve1)

	return nil
}

func deletePool(
	poolID uint64,
	registry *UniswapV2Registry,
) error {
	indexToDelete, ok := registry.idToIndex[poolID]
	if !ok {
		return ErrPoolNotFound
	}

	lastIndex := len(registry.id) - 1
	lastPoolID := registry.id[lastIndex]

	if indexToDelete != lastIndex {
		registry.id[indexToDelete] = lastPoolID
		registry.token0[indexToDelete] = registry.token0[lastIndex]
		registry.token1[indexToDelete] = registry.token1[lastIndex]
		registry.reserve0[indexToDelete] = registry.reserve0[lastIndex]
		registry.reserve1[indexToDelete] = registry.reserve1[lastIndex]
		registry.pooltype[indexToDelete] = registry.pooltype[lastIndex]
		registry.fee[indexToDelete] = registry.fee[lastIndex]
		registry.idToIndex[lastPoolID] = indexToDelete
	}

	delete(registry.idToIndex, poolID)

	registry.id = registry.id[:lastIndex]
	registry.token0 = registry.token0[:lastIndex]
	registry.token1 = registry.token1[:lastIndex]
	registry.reserve0 = registry.reserve0[:lastIndex]
	registry.reserve1 = registry.reserve1[:lastIndex]
	registry.pooltype = registry.pooltype[:lastIndex]
	registry.fee = registry.fee[:lastIndex]

	return nil
}

func viewRegistry(
	registry *UniswapV2Registry,
) []PoolView {
	numPools := len(registry.id)
	if numPools == 0 {
		return nil
	}

	views := make([]PoolView, numPools)
	for i := 0; i < numPools; i++ {
		views[i] = PoolView{
			ID:       registry.id[i],
			Token0:   registry.token0[i],
			Token1:   registry.token1[i],
			Reserve0: new(big.Int).Set(registry.reserve0[i]),
			Reserve1: new(big.Int).Set(registry.reserve1[i]),
			Type:     registry.pooltype[i],
			FeeBps:   registry.fee[i],
		}
	}
	return views
}

// getPoolById retrieves a single pool's view by its permanent ID.
func getPoolById(
	poolID uint64,
	registry *UniswapV2Registry,
) (PoolView, error) {
	index, ok := registry.idToIndex[poolID]
	if !ok {
		return PoolView{}, ErrPoolNotFound
	}

	view := PoolView{
		ID:       registry.id[index],
		Token0:   registry.token0[index],
		Token1:   registry.token1[index],
		Reserve0: new(big.Int).Set(registry.reserve0[index]),
		Reserve1: new(big.Int).Set(registry.reserve1[index]),
		Type:     uint8(registry.pooltype[index]),
		FeeBps:   registry.fee[index],
	}

	return view, nil
}
