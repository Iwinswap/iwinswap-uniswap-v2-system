package uniswapv2

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock Dependencies ---

// mockIDProvider simulates the upstream service that manages permanent IDs for tokens and pools.
type mockIDProvider struct {
	tokenCounter uint64
	poolCounter  uint64
	tokens       map[common.Address]uint64
	pools        map[common.Address]uint64
}

func newMockIDProvider() *mockIDProvider {
	return &mockIDProvider{
		tokenCounter: 1, // Start at 1 for clarity
		poolCounter:  1,
		tokens:       make(map[common.Address]uint64),
		pools:        make(map[common.Address]uint64),
	}
}

func (p *mockIDProvider) RegisterToken(addr common.Address) uint64 {
	if id, ok := p.tokens[addr]; ok {
		return id
	}
	id := p.tokenCounter
	p.tokens[addr] = id
	p.tokenCounter++
	return id
}

func (p *mockIDProvider) RegisterPool(addr common.Address) uint64 {
	if id, ok := p.pools[addr]; ok {
		return id
	}
	id := p.poolCounter
	p.pools[addr] = id
	p.poolCounter++
	return id
}

func (p *mockIDProvider) TokenAddressToID(addr common.Address) (uint64, error) {
	if id, ok := p.tokens[addr]; ok {
		return id, nil
	}
	return 0, errors.New("token not registered")
}

func (p *mockIDProvider) PoolAddressToID(addr common.Address) (uint64, error) {
	if id, ok := p.pools[addr]; ok {
		return id, nil
	}
	return 0, errors.New("pool not registered")
}

// testFindViewByID is a helper to find a specific pool in a view slice.
func testFindViewByID(view []PoolView, id uint64) *PoolView {
	for i := range view {
		if view[i].ID == id {
			return &view[i]
		}
	}
	return nil
}

// --- Test Suite ---

func TestUniswapV2Registry(t *testing.T) {
	// Helper addresses for tests
	tokenAddr1 := common.HexToAddress("0x111")
	tokenAddr2 := common.HexToAddress("0x222")
	tokenAddr3 := common.HexToAddress("0x333")

	poolAddr12 := common.HexToAddress("0x12")
	poolAddr13 := common.HexToAddress("0x13")
	poolAddr23 := common.HexToAddress("0x23")

	t.Run("AddPool_Success", func(t *testing.T) {
		registry := NewUniswapV2Registry()
		idProvider := newMockIDProvider()

		tokenID1 := idProvider.RegisterToken(tokenAddr1)
		tokenID2 := idProvider.RegisterToken(tokenAddr2)
		poolID := idProvider.RegisterPool(poolAddr12)

		err := addPool(tokenAddr1, tokenAddr2, poolAddr12, 0, 30, idProvider.TokenAddressToID, idProvider.PoolAddressToID, registry)
		require.NoError(t, err)

		view, err := getPoolById(poolID, registry)
		require.NoError(t, err)

		assert.Equal(t, poolID, view.ID)
		assert.Equal(t, tokenID1, view.Token0)
		assert.Equal(t, tokenID2, view.Token1)
		assert.Equal(t, uint8(0), view.Type)
		assert.Equal(t, uint16(30), view.FeeBps)
		assert.Equal(t, 0, view.Reserve0.Cmp(big.NewInt(0)))
		assert.Equal(t, 0, view.Reserve1.Cmp(big.NewInt(0)))

		// HARDENING: Ensure reserves are never nil, even when zero.
		assert.NotNil(t, view.Reserve0)
		assert.NotNil(t, view.Reserve1)
	})

	t.Run("AddPool_ErrorOnDuplicate", func(t *testing.T) {
		registry := NewUniswapV2Registry()
		idProvider := newMockIDProvider()
		idProvider.RegisterToken(tokenAddr1)
		idProvider.RegisterToken(tokenAddr2)
		idProvider.RegisterPool(poolAddr12)

		err := addPool(tokenAddr1, tokenAddr2, poolAddr12, 0, 30, idProvider.TokenAddressToID, idProvider.PoolAddressToID, registry)
		require.NoError(t, err)

		err = addPool(tokenAddr1, tokenAddr2, poolAddr12, 0, 30, idProvider.TokenAddressToID, idProvider.PoolAddressToID, registry)
		require.ErrorIs(t, err, ErrPoolExists)
	})

	t.Run("UpdatePool_Immutability", func(t *testing.T) {
		registry := NewUniswapV2Registry()
		idProvider := newMockIDProvider()
		idProvider.RegisterToken(tokenAddr1)
		idProvider.RegisterToken(tokenAddr2)
		poolID := idProvider.RegisterPool(poolAddr12)
		require.NoError(t, addPool(tokenAddr1, tokenAddr2, poolAddr12, 0, 30, idProvider.TokenAddressToID, idProvider.PoolAddressToID, registry))

		// Create the values we will use for the update.
		newReserve0 := big.NewInt(500)
		newReserve1 := big.NewInt(1000)

		// Act: Update the pool.
		err := updatePool(newReserve0, newReserve1, poolID, registry)
		require.NoError(t, err)

		// Maliciously modify the original big.Int pointers *after* the update call.
		newReserve0.SetInt64(9999)
		newReserve1.SetInt64(9999)

		// Assert: Check the registry's internal state.
		view, err := getPoolById(poolID, registry)
		require.NoError(t, err)

		// The reserves in the registry should NOT have changed. They should be copies.
		assert.Equal(t, 0, view.Reserve0.Cmp(big.NewInt(500)), "Registry reserve0 should be a copy and not be mutated")
		assert.Equal(t, 0, view.Reserve1.Cmp(big.NewInt(1000)), "Registry reserve1 should be a copy and not be mutated")
	})

	t.Run("DeletePool_SwapAndPopLogic", func(t *testing.T) {
		registry := NewUniswapV2Registry()
		idProvider := newMockIDProvider()
		idProvider.RegisterToken(tokenAddr1)
		idProvider.RegisterToken(tokenAddr2)
		idProvider.RegisterToken(tokenAddr3)

		poolID1 := idProvider.RegisterPool(poolAddr12)
		require.NoError(t, addPool(tokenAddr1, tokenAddr2, poolAddr12, 1, 30, idProvider.TokenAddressToID, idProvider.PoolAddressToID, registry))
		poolID2 := idProvider.RegisterPool(poolAddr13)
		require.NoError(t, addPool(tokenAddr1, tokenAddr3, poolAddr13, 2, 5, idProvider.TokenAddressToID, idProvider.PoolAddressToID, registry))
		poolID3 := idProvider.RegisterPool(poolAddr23)
		require.NoError(t, addPool(tokenAddr2, tokenAddr3, poolAddr23, 3, 100, idProvider.TokenAddressToID, idProvider.PoolAddressToID, registry))

		require.Len(t, viewRegistry(registry), 3)

		// Delete the middle one (poolID2)
		err := deletePool(poolID2, registry)
		require.NoError(t, err)
		require.Len(t, viewRegistry(registry), 2)

		// Verify pool2 is gone and the others remain
		_, err = getPoolById(poolID2, registry)
		require.ErrorIs(t, err, ErrPoolNotFound)

		view := viewRegistry(registry)
		p1View := testFindViewByID(view, poolID1)
		p3View := testFindViewByID(view, poolID3)
		require.NotNil(t, p1View, "pool1 should still exist")
		require.NotNil(t, p3View, "pool3 should still exist")

		// HARDENING: Verify the swap was correct. The data for pool3 is correct.
		assert.Equal(t, poolID3, p3View.ID)
		assert.Equal(t, uint8(3), p3View.Type)
		assert.Equal(t, uint16(100), p3View.FeeBps)
	})

	t.Run("ErrorHandling_NotFound", func(t *testing.T) {
		registry := NewUniswapV2Registry()
		_, err := getPoolById(999, registry)
		assert.ErrorIs(t, err, ErrPoolNotFound)
		err = updatePool(big.NewInt(1), big.NewInt(1), 999, registry)
		assert.ErrorIs(t, err, ErrPoolNotFound)
		err = deletePool(999, registry)
		assert.ErrorIs(t, err, ErrPoolNotFound)
	})

	t.Run("ViewRegistry_Immutability", func(t *testing.T) {
		registry := NewUniswapV2Registry()
		idProvider := newMockIDProvider()
		idProvider.RegisterToken(tokenAddr1)
		idProvider.RegisterToken(tokenAddr2)
		poolID := idProvider.RegisterPool(poolAddr12)
		require.NoError(t, addPool(tokenAddr1, tokenAddr2, poolAddr12, 0, 30, idProvider.TokenAddressToID, idProvider.PoolAddressToID, registry))
		require.NoError(t, updatePool(big.NewInt(1000), big.NewInt(2000), poolID, registry))

		view := viewRegistry(registry)
		require.Len(t, view, 1)

		// Maliciously modify the view's data.
		view[0].Reserve0.SetInt64(555)

		originalView, err := getPoolById(poolID, registry)
		require.NoError(t, err)
		assert.Equal(t, 0, originalView.Reserve0.Cmp(big.NewInt(1000)), "registry data should not be mutated by consumers of the view")
	})

	t.Run("HasPool", func(t *testing.T) {
		registry := NewUniswapV2Registry()
		idProvider := newMockIDProvider()
		idProvider.RegisterToken(tokenAddr1)
		idProvider.RegisterToken(tokenAddr2)
		poolID := idProvider.RegisterPool(poolAddr12)
		require.NoError(t, addPool(tokenAddr1, tokenAddr2, poolAddr12, 0, 30, idProvider.TokenAddressToID, idProvider.PoolAddressToID, registry))

		// Check for an existing pool
		assert.True(t, hasPool(poolID, registry), "hasPool should return true for an existing pool")

		// Check for a non-existent pool
		assert.False(t, hasPool(999, registry), "hasPool should return false for a non-existent pool")

		// Delete the pool
		err := deletePool(poolID, registry)
		require.NoError(t, err)

		// Check for the deleted pool
		assert.False(t, hasPool(poolID, registry), "hasPool should return false for a deleted pool")
	})
}

func TestNewUniswapV2RegistryFromViews(t *testing.T) {
	t.Parallel()

	t.Run("SuccessWithValidView", func(t *testing.T) {
		// 1. Arrange: Create a valid view.
		sourceView := []PoolView{
			{ID: 10, Token0: 1, Token1: 2, Reserve0: big.NewInt(1000), Reserve1: big.NewInt(2000), Type: 1, FeeBps: 30},
			{ID: 20, Token0: 1, Token1: 3, Reserve0: big.NewInt(3000), Reserve1: big.NewInt(4000), Type: 2, FeeBps: 5},
		}

		// 2. Act: Create the registry from the view.
		registry := NewUniswapV2RegistryFromViews(sourceView)
		require.NotNil(t, registry)

		// 3. Assert: Verify the registry's state.
		rehydratedView := viewRegistry(registry)
		require.Len(t, rehydratedView, 2)
		assert.ElementsMatch(t, sourceView, rehydratedView, "Rehydrated view should match the source view")

		// Also check the internal lookup map.
		assert.Equal(t, 0, registry.idToIndex[10])
		assert.Equal(t, 1, registry.idToIndex[20])
	})

	t.Run("PerformsDeepCopyOfReserves", func(t *testing.T) {
		// 1. Arrange: Create a view with a reserve we can modify.
		originalReserve := big.NewInt(5000)
		sourceView := []PoolView{
			{ID: 10, Token0: 1, Token1: 2, Reserve0: originalReserve, Reserve1: big.NewInt(1), Type: 1, FeeBps: 30},
		}

		// 2. Act: Create the registry.
		registry := NewUniswapV2RegistryFromViews(sourceView)

		// 3. Mutate the original view's reserve *after* creating the registry.
		sourceView[0].Reserve0.SetInt64(9999)

		// 4. Assert: The reserve inside the registry should be unchanged.
		internalView, err := getPoolById(10, registry)
		require.NoError(t, err)
		assert.Equal(t, 0, internalView.Reserve0.Cmp(big.NewInt(5000)), "Registry should hold a deep copy of reserves, not a pointer to the original")
	})

	t.Run("HandlesEmptyAndNilViews", func(t *testing.T) {
		// Test with an empty slice
		registryEmpty := NewUniswapV2RegistryFromViews([]PoolView{})
		require.NotNil(t, registryEmpty)
		assert.Empty(t, viewRegistry(registryEmpty))
		assert.Empty(t, registryEmpty.idToIndex)

		// Test with a nil slice
		registryNil := NewUniswapV2RegistryFromViews(nil)
		require.NotNil(t, registryNil)
		assert.Empty(t, viewRegistry(registryNil))
		assert.Empty(t, registryNil.idToIndex)
	})
}
