package logs

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// To make these tests self-contained and runnable, we define the constant
// that would otherwise be in another file.
// This is the Keccak-256 hash of the event signature "Sync(uint112,uint112)".
var (
	OtherEvent = common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
)

// --- Test Helper Functions ---

// newSyncLog creates a valid mock log for a Uniswap V2 Sync event.
func newSyncLog(poolAddress common.Address, reserve0, reserve1 *big.Int) types.Log {
	data := make([]byte, 64)
	copy(data[32-len(reserve0.Bytes()):32], reserve0.Bytes())
	copy(data[64-len(reserve1.Bytes()):64], reserve1.Bytes())

	return types.Log{
		Address: poolAddress,
		Topics:  []common.Hash{UniswapV2SyncEvent},
		Data:    data,
	}
}

// --- Test Suite ---

func TestUpdatedInBlock(t *testing.T) {
	// Define mock addresses and reserve values for tests.
	poolAddr1 := common.HexToAddress("0x0d4a11d5EEaaC28EC3F61d100daF4d40471f1852")
	poolAddr2 := common.HexToAddress("0xB4e16d0168e52d35CaCD2c6185b44281EC28c9Dc")

	reserves1_initial := []*big.Int{big.NewInt(1000), big.NewInt(2000)}
	reserves1_final := []*big.Int{big.NewInt(1500), big.NewInt(1500)} // Final state for pool 1
	reserves2 := []*big.Int{big.NewInt(5000), big.NewInt(8000)}

	// Define test cases table.
	testCases := []struct {
		name        string
		inputLogs   []types.Log
		expected    map[common.Address]syncData // Use a map for expected values to handle unordered output
		expectError bool
	}{
		{
			name: "Happy Path - Single sync event",
			inputLogs: []types.Log{
				newSyncLog(poolAddr1, reserves1_initial[0], reserves1_initial[1]),
			},
			expected: map[common.Address]syncData{
				poolAddr1: {reserve0: reserves1_initial[0], reserve1: reserves1_initial[1]},
			},
			expectError: false,
		},
		{
			name: "CRITICAL - Last event wins for a single pool",
			inputLogs: []types.Log{
				newSyncLog(poolAddr1, reserves1_initial[0], reserves1_initial[1]), // Initial sync
				{Address: poolAddr1, Topics: []common.Hash{OtherEvent}},           // Some other event for the same pool
				newSyncLog(poolAddr1, reserves1_final[0], reserves1_final[1]),     // Final sync
			},
			expected: map[common.Address]syncData{
				poolAddr1: {reserve0: reserves1_final[0], reserve1: reserves1_final[1]}, // Should have final state
			},
			expectError: false,
		},
		{
			name: "Happy Path - Multiple pools with mixed events",
			inputLogs: []types.Log{
				newSyncLog(poolAddr1, reserves1_initial[0], reserves1_initial[1]),
				{Address: poolAddr2, Topics: []common.Hash{OtherEvent}}, // Irrelevant event
				newSyncLog(poolAddr2, reserves2[0], reserves2[1]),
				newSyncLog(poolAddr1, reserves1_final[0], reserves1_final[1]), // Final update for pool 1
			},
			expected: map[common.Address]syncData{
				poolAddr1: {reserve0: reserves1_final[0], reserve1: reserves1_final[1]},
				poolAddr2: {reserve0: reserves2[0], reserve1: reserves2[1]},
			},
			expectError: false,
		},
		{
			name:      "Edge Case - Only non-sync events",
			inputLogs: []types.Log{{Address: poolAddr1, Topics: []common.Hash{OtherEvent}}},
			expected:  map[common.Address]syncData{},
		},
		{
			name: "Malformed Data - Sync event with wrong data length",
			inputLogs: []types.Log{
				{Address: poolAddr1, Topics: []common.Hash{UniswapV2SyncEvent}, Data: []byte{1, 2, 3}}, // Invalid data
				newSyncLog(poolAddr2, reserves2[0], reserves2[1]),                                      // Valid event should still be processed
			},
			expected: map[common.Address]syncData{
				poolAddr2: {reserve0: reserves2[0], reserve1: reserves2[1]},
			},
			expectError: false,
		},
		{
			name: "Malformed Data - Log with wrong topic count",
			inputLogs: []types.Log{
				{Address: poolAddr1, Topics: []common.Hash{UniswapV2SyncEvent, OtherEvent}}, // Invalid sync event
			},
			expected: map[common.Address]syncData{},
		},
		{
			name:        "Boundary Case - Empty log slice",
			inputLogs:   []types.Log{},
			expected:    map[common.Address]syncData{},
			expectError: false,
		},
		{
			name:        "Boundary Case - Nil log slice",
			inputLogs:   nil,
			expected:    map[common.Address]syncData{},
			expectError: false,
		},
	}

	// Run all test cases.
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Execute the function under test.
			pools, r0s, r1s, err := UpdatedInBlock(tc.inputLogs)

			// Assert error status.
			if tc.expectError {
				require.Error(t, err)
				return // No further checks if an error was expected.
			}
			require.NoError(t, err)

			// The function returns three parallel slices whose order is not guaranteed.
			// To assert correctness, we reconstruct a map from the output.
			resultMap := make(map[common.Address]syncData)
			require.Equal(t, len(pools), len(r0s), "Length of returned slices must be equal")
			require.Equal(t, len(r0s), len(r1s), "Length of returned slices must be equal")

			for i, addr := range pools {
				resultMap[addr] = syncData{
					reserve0: r0s[i],
					reserve1: r1s[i],
				}
			}

			// Assert that the reconstructed map is equal to the expected map.
			// This correctly checks for content regardless of order.
			assert.Equal(t, tc.expected, resultMap)

			// Additionally, assert that the number of discovered pools is correct.
			assert.Len(t, pools, len(tc.expected))
		})
	}
}
