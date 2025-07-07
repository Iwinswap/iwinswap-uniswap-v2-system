package reserves

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"

	ethclients "github.com/Iwinswap/iwinswap-ethclients"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create mock return data for getReserves.
func reservesToBytes(r0, r1 *big.Int) []byte {
	data := make([]byte, 96) // reserves are uint112, but padded to 32 bytes each + a 32 byte timestamp
	copy(data[32-len(r0.Bytes()):32], r0.Bytes())
	copy(data[64-len(r1.Bytes()):64], r1.Bytes())
	return data
}

// Helper function to safely create a new big.Int from a string. Panics on failure, for test setup only.
func newBigIntFromString(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic(fmt.Sprintf("failed to parse big int string for test setup: %s", s))
	}
	return n
}

// --- Test Suite ---

func TestGetReserves(t *testing.T) {
	// --- Shared Test Setup ---
	poolAddr1 := common.HexToAddress("0x1")
	poolAddr2 := common.HexToAddress("0x2")
	failingPoolAddr := common.HexToAddress("0xDEAD")

	reserves1 := []*big.Int{newBigIntFromString("1000000000000000000000"), newBigIntFromString("2000000000000000000000")}
	reserves2 := []*big.Int{newBigIntFromString("3000"), big.NewInt(4000)}

	// --- Test Cases Table ---
	testCases := []struct {
		name         string
		poolAddrs    []common.Address
		setupHandler func(t *testing.T, client *ethclients.TestETHClient)
		validate     func(t *testing.T, r0s, r1s []*big.Int, errs []error)
	}{
		{
			name:      "Happy Path - Single pool",
			poolAddrs: []common.Address{poolAddr1},
			setupHandler: func(t *testing.T, client *ethclients.TestETHClient) {
				client.SetCallContractHandler(func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
					require.Equal(t, poolAddr1, *msg.To)
					require.Equal(t, getReservesSig, msg.Data)
					return reservesToBytes(reserves1[0], reserves1[1]), nil
				})
			},
			validate: func(t *testing.T, r0s, r1s []*big.Int, errs []error) {
				require.Len(t, r0s, 1)
				require.NoError(t, errs[0])
				assert.Equal(t, 0, reserves1[0].Cmp(r0s[0]))
				assert.Equal(t, 0, reserves1[1].Cmp(r1s[0]))
			},
		},
		{
			name:      "Happy Path - Multiple pools concurrently",
			poolAddrs: []common.Address{poolAddr1, poolAddr2},
			setupHandler: func(t *testing.T, client *ethclients.TestETHClient) {
				client.SetCallContractHandler(func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
					if *msg.To == poolAddr1 {
						return reservesToBytes(reserves1[0], reserves1[1]), nil
					}
					if *msg.To == poolAddr2 {
						return reservesToBytes(reserves2[0], reserves2[1]), nil
					}
					return nil, fmt.Errorf("unexpected call to address %s", msg.To.Hex())
				})
			},
			validate: func(t *testing.T, r0s, r1s []*big.Int, errs []error) {
				require.Len(t, r0s, 2)
				assert.NoError(t, errs[0])
				assert.NoError(t, errs[1])
				// Order is not guaranteed due to concurrency, so check both possibilities.
				if r0s[0].Cmp(reserves1[0]) == 0 { // First result is for pool1
					assert.Equal(t, 0, reserves1[1].Cmp(r1s[0]))
					assert.Equal(t, 0, reserves2[0].Cmp(r0s[1]))
					assert.Equal(t, 0, reserves2[1].Cmp(r1s[1]))
				} else { // First result is for pool2
					assert.Equal(t, 0, reserves2[1].Cmp(r1s[0]))
					assert.Equal(t, 0, reserves1[0].Cmp(r0s[1]))
					assert.Equal(t, 0, reserves1[1].Cmp(r1s[1]))
				}
			},
		},
		{
			name:      "Mixed Case - One pool succeeds, one fails",
			poolAddrs: []common.Address{poolAddr1, failingPoolAddr},
			setupHandler: func(t *testing.T, client *ethclients.TestETHClient) {
				client.SetCallContractHandler(func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
					if *msg.To == poolAddr1 {
						return reservesToBytes(reserves1[0], reserves1[1]), nil
					}
					if *msg.To == failingPoolAddr {
						return nil, errors.New("rpc error")
					}
					return nil, fmt.Errorf("unexpected call to address %s", msg.To.Hex())
				})
			},
			validate: func(t *testing.T, r0s, r1s []*big.Int, errs []error) {
				require.Len(t, errs, 2)
				// Find which index corresponds to the failing pool.
				failingIdx := 0
				successIdx := 1
				if errs[0] == nil {
					failingIdx = 1
					successIdx = 0
				}
				assert.NoError(t, errs[successIdx])
				assert.Equal(t, 0, reserves1[0].Cmp(r0s[successIdx]))
				assert.Error(t, errs[failingIdx])
				assert.Contains(t, errs[failingIdx].Error(), "rpc error")
				assert.Nil(t, r0s[failingIdx])
			},
		},
		{
			name:      "Error Case - Malformed response data",
			poolAddrs: []common.Address{poolAddr1},
			setupHandler: func(t *testing.T, client *ethclients.TestETHClient) {
				client.SetCallContractHandler(func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
					return []byte{0x01, 0x02, 0x03}, nil // Invalid length
				})
			},
			validate: func(t *testing.T, r0s, r1s []*big.Int, errs []error) {
				require.Len(t, errs, 1)
				assert.Error(t, errs[0])
				assert.Contains(t, errs[0].Error(), "invalid response length")
			},
		},
		{
			name:      "Boundary Case - Empty input slice",
			poolAddrs: []common.Address{},
			validate: func(t *testing.T, r0s, r1s []*big.Int, errs []error) {
				assert.Empty(t, r0s)
				assert.Empty(t, r1s)
				assert.Empty(t, errs)
			},
		},
		{
			name:      "Boundary Case - Nil input slice",
			poolAddrs: nil,
			validate: func(t *testing.T, r0s, r1s []*big.Int, errs []error) {
				assert.Nil(t, r0s)
				assert.Nil(t, r1s)
				assert.Nil(t, errs)
			},
		},
		{
			name:      "Context Cancellation",
			poolAddrs: []common.Address{poolAddr1},
			validate: func(t *testing.T, r0s, r1s []*big.Int, errs []error) {
				require.Len(t, errs, 1)
				assert.Error(t, errs[0])
				assert.ErrorIs(t, errs[0], context.Canceled)
			},
		},
	}

	// --- Run Tests ---
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := ethclients.NewTestETHClient()
			if tc.setupHandler != nil {
				tc.setupHandler(t, client)
			}

			ctx := context.Background()
			if tc.name == "Context Cancellation" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(context.Background())
				cancel() // Cancel context immediately
			}

			r0s, r1s, errs := NewGetReserves(10)(ctx, tc.poolAddrs, client)

			if tc.validate != nil {
				tc.validate(t, r0s, r1s, errs)
			}
		})
	}
}
