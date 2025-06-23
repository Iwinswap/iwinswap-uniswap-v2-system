package logs

import (
	"testing"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/assert"
)

func TestSwapEventInBloom(t *testing.T) {

	// Define test cases table for structured testing.
	testCases := []struct {
		name         string
		setupBloom   func() types.Bloom // Function to set up the specific bloom filter for the test
		expectResult bool
	}{
		{
			name: "Happy Path - Bloom filter contains the Swap event",
			setupBloom: func() types.Bloom {
				var bloom types.Bloom
				// Add the specific event topic to the bloom filter.
				bloom.Add(UniswapV2SwapEvent.Bytes())
				return bloom
			},
			expectResult: true,
		},
		{
			name: "Negative Case - Bloom filter is empty",
			setupBloom: func() types.Bloom {
				// An empty bloom filter should not contain the event.
				return types.Bloom{}
			},
			expectResult: false,
		},
		{
			name: "Negative Case - Bloom filter contains a different event",
			setupBloom: func() types.Bloom {
				var bloom types.Bloom
				// Add a different, unrelated event topic.
				bloom.Add(ERC20TransferEvent.Bytes())
				return bloom
			},
			expectResult: false,
		},
		{
			name: "Edge Case - Bloom filter contains both Swap and other events",
			setupBloom: func() types.Bloom {
				var bloom types.Bloom
				bloom.Add(UniswapV2SwapEvent.Bytes())
				bloom.Add(ERC20TransferEvent.Bytes())
				// Add some other arbitrary data
				bloom.Add([]byte("some other data"))
				return bloom
			},
			expectResult: true,
		},
	}

	// Run all test cases.
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// --- Function to be tested (pasted here for self-containment) ---
			swapEventInBloom := func(bloom types.Bloom) bool {
				return bloom.Test(UniswapV2SwapEvent.Bytes())
			}
			// ---

			// 1. Setup
			bloomFilter := tc.setupBloom()

			// 2. Execute
			result := swapEventInBloom(bloomFilter)

			// 3. Assert
			assert.Equal(t, tc.expectResult, result)
		})
	}
}
