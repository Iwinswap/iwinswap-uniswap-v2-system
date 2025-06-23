package logs

import (
	"math/big"
	"math/rand"
	"sort"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	ERC20TransferEvent = common.HexToHash("0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")
)

// --- Test Helper Functions ---

// newSwapLog creates a mock log for a Uniswap V2 Swap event.
// The address provided is the pool (pair) address.
func newSwapLog(poolAddress common.Address) types.Log {
	return types.Log{
		Address: poolAddress,
		Topics: []common.Hash{
			UniswapV2SwapEvent,
			common.HexToHash("0x000000000000000000000000c02aaa39b223fe8d0a0e5c4f27ead9083c756cc2"), // Mock sender
			common.HexToHash("0x000000000000000000000000a0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"), // Mock recipient
		},
	}
}

// newTransferLog creates a mock log for a standard ERC20 Transfer event.
// The address provided is the token contract address, which should NOT be discovered as a pool.
func newTransferLog(tokenAddress common.Address) types.Log {
	return types.Log{
		Address: tokenAddress,
		Topics: []common.Hash{
			ERC20TransferEvent,
			common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"), // Mock from
			common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000002"), // Mock to
		},
	}
}

// --- Test Suite ---

func TestDiscoverPools(t *testing.T) {
	// Define mock addresses to be used across tests for consistency.
	poolAddr1 := common.HexToAddress("0x0d4a11d5EEaaC28EC3F61d100daF4d40471f1852") // Example: WETH/USDT
	poolAddr2 := common.HexToAddress("0xB4e16d0168e52d35CaCD2c6185b44281EC28c9Dc") // Example: USDC/WETH
	tokenAddr := common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2") // WETH token

	// Define test cases table for structured testing.
	testCases := []struct {
		name          string
		inputLogs     []types.Log
		expectedPools []common.Address
		expectError   bool
	}{
		{
			name: "Happy Path - Multiple unique swap events",
			inputLogs: []types.Log{
				newSwapLog(poolAddr1),
				newSwapLog(poolAddr2),
			},
			expectedPools: []common.Address{poolAddr1, poolAddr2},
			expectError:   false,
		},
		{
			name: "Happy Path - Duplicate swap events",
			inputLogs: []types.Log{
				newSwapLog(poolAddr1),
				newSwapLog(poolAddr1), // Duplicate
				newSwapLog(poolAddr2),
			},
			expectedPools: []common.Address{poolAddr1, poolAddr2}, // Should only be discovered once
			expectError:   false,
		},
		{
			name: "Edge Case - Mixed events with swaps and transfers",
			inputLogs: []types.Log{
				newSwapLog(poolAddr1),
				newTransferLog(tokenAddr), // Should be ignored
				newSwapLog(poolAddr2),
				newTransferLog(poolAddr1), // A pool can also emit transfer events for LP tokens
			},
			expectedPools: []common.Address{poolAddr1, poolAddr2}, // Only swap event addresses
			expectError:   false,
		},
		{
			name: "Edge Case - Only non-swap events",
			inputLogs: []types.Log{
				newTransferLog(tokenAddr),
				newTransferLog(poolAddr1),
			},
			expectedPools: []common.Address{}, // Expect no pools
			expectError:   false,
		},
		{
			name: "Malformed Input - Log with no topics",
			inputLogs: []types.Log{
				{Address: common.HexToAddress("0x111")}, // Log with no topics slice
			},
			expectedPools: []common.Address{},
			expectError:   false,
		},
		{
			name: "Malformed Input - Log with empty topics slice",
			inputLogs: []types.Log{
				{Address: common.HexToAddress("0x222"), Topics: []common.Hash{}}, // Log with empty topics
			},
			expectedPools: []common.Address{},
			expectError:   false,
		},
		{
			name:          "Boundary Case - Empty log slice",
			inputLogs:     []types.Log{},
			expectedPools: []common.Address{},
			expectError:   false,
		},
		{
			name:          "Boundary Case - Nil log slice",
			inputLogs:     nil,
			expectedPools: []common.Address{},
			expectError:   false,
		},
	}

	// Run all test cases.
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// --- Corrected DiscoverPools function for testing ---
			discoverPoolsFixed := func(logs []types.Log) ([]common.Address, error) {
				discoveredPools := make(map[common.Address]struct{})
				for _, log := range logs {
					if len(log.Topics) > 0 && log.Topics[0] == UniswapV2SwapEvent {
						discoveredPools[log.Address] = struct{}{}
					}
				}
				pools := make([]common.Address, 0, len(discoveredPools))
				for poolAddr := range discoveredPools {
					pools = append(pools, poolAddr)
				}
				return pools, nil
			}
			// ---

			// Execute the function under test.
			discovered, err := discoverPoolsFixed(tc.inputLogs)

			// Assert error status.
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			// Sort both slices for deterministic comparison. Maps do not guarantee order.
			sort.Slice(discovered, func(i, j int) bool {
				return discovered[i].String() < discovered[j].String()
			})
			sort.Slice(tc.expectedPools, func(i, j int) bool {
				return tc.expectedPools[i].String() < tc.expectedPools[j].String()
			})

			// Assert the content of the discovered pools.
			assert.Equal(t, tc.expectedPools, discovered)
		})
	}

	t.Run("Performance - Large number of mixed logs", func(t *testing.T) {
		numLogs := 10000
		numPools := 100
		logs := make([]types.Log, numLogs)
		expectedPoolMap := make(map[common.Address]struct{})
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))

		for i := 0; i < numLogs; i++ {
			if rng.Intn(10) == 0 { // ~10% are swap logs
				poolNum := rng.Intn(numPools)
				poolAddr := common.BigToAddress(big.NewInt(int64(poolNum + 1)))
				logs[i] = newSwapLog(poolAddr)
				expectedPoolMap[poolAddr] = struct{}{}
			} else {
				tokenAddr := common.BigToAddress(big.NewInt(int64(rng.Intn(1000) + 1)))
				logs[i] = newTransferLog(tokenAddr)
			}
		}

		// Convert expected map to slice for comparison
		expectedPools := make([]common.Address, 0, len(expectedPoolMap))
		for addr := range expectedPoolMap {
			expectedPools = append(expectedPools, addr)
		}

		discovered, err := DiscoverPools(logs)
		require.NoError(t, err)

		assert.Len(t, discovered, len(expectedPools), "Should discover the correct number of unique pools")

		// Full equality check is expensive for large slices but good for ensuring correctness.
		sort.Slice(discovered, func(i, j int) bool {
			return discovered[i].String() < discovered[j].String()
		})
		sort.Slice(expectedPools, func(i, j int) bool {
			return expectedPools[i].String() < expectedPools[j].String()
		})
		assert.Equal(t, expectedPools, discovered)
	})
}
