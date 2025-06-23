package initializer

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

// Helper function to create mock return data for an address.
func addressToBytes(addr common.Address) []byte {
	return common.LeftPadBytes(addr.Bytes(), 32)
}

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

func TestPoolInitializer(t *testing.T) {
	// --- Shared Test Setup ---
	knownUniFactory := KnownFactory{
		Address:      common.HexToAddress("0x5C69bEe701ef814a2B6a3EDD4B1652CB9cc5aA6f"),
		ProtocolName: "Uniswap-V2",
		FeeBps:       30,
	}
	knownSushiFactory := KnownFactory{
		Address:      common.HexToAddress("0xC0AEe478e3658e2610c5F7A4A2E1777cE9e4f2Ac"),
		ProtocolName: "SushiSwap",
		FeeBps:       30,
	}
	knownPancakeFactory := KnownFactory{
		Address:      common.HexToAddress("0xcA143Ce32fC7BF50710569a7E92404f7a232eE50"),
		ProtocolName: "PancakeSwap-V2",
		FeeBps:       25,
	}
	knownFactories := []KnownFactory{knownUniFactory, knownSushiFactory, knownPancakeFactory}

	// Mock pool and token addresses
	uniPoolAddr := common.HexToAddress("0xB4e16d0168e52d35CaCD2c6185b44281EC28c9Dc")   // USDC/WETH
	sushiPoolAddr := common.HexToAddress("0x397FF1542f962076d0BFE58e2424B25640312133") // WETH/MMY
	unknownPoolAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	failingPoolAddr := common.HexToAddress("0x2222222222222222222222222222222222222222")

	// Mock token details
	wethAddr := common.HexToAddress("0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")
	usdcAddr := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9eb0ce3606eb48")
	mmyAddr := common.HexToAddress("0x6a40a5e82b43522a76f2f28b32e2764b73134f78")

	// Mock reserves, initialized correctly from strings to handle large values.
	uniReserves := []*big.Int{
		newBigIntFromString("1000000000000"),          // 1M USDC (6 decimals)
		newBigIntFromString("1000000000000000000000"), // 1000 WETH (18 decimals)
	}
	sushiReserves := []*big.Int{
		newBigIntFromString("500000000000000000000"),    // 500 WETH (18 decimals)
		newBigIntFromString("250000000000000000000000"), // 250k MMY (18 decimals)
	}

	// --- Test Cases Table ---
	testCases := []struct {
		name         string
		poolAddrs    []common.Address
		setupHandler func(t *testing.T, client *ethclients.TestETHClient)
		validate     func(t *testing.T, inputPools []common.Address, token0s, token1s []common.Address, poolTypes []uint8, feeBps []uint16, reserve0s, reserve1s []*big.Int, errs []error)
	}{
		{
			name:      "Happy Path - Single known pool",
			poolAddrs: []common.Address{uniPoolAddr},
			setupHandler: func(t *testing.T, client *ethclients.TestETHClient) {
				client.SetCallContractHandler(func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
					switch {
					case msg.To.Hex() == uniPoolAddr.Hex() && common.Bytes2Hex(msg.Data) == common.Bytes2Hex(factorySig):
						return addressToBytes(knownUniFactory.Address), nil
					case msg.To.Hex() == uniPoolAddr.Hex() && common.Bytes2Hex(msg.Data) == common.Bytes2Hex(token0Sig):
						return addressToBytes(usdcAddr), nil
					case msg.To.Hex() == uniPoolAddr.Hex() && common.Bytes2Hex(msg.Data) == common.Bytes2Hex(token1Sig):
						return addressToBytes(wethAddr), nil
					case msg.To.Hex() == uniPoolAddr.Hex() && common.Bytes2Hex(msg.Data) == common.Bytes2Hex(getReservesSig):
						return reservesToBytes(uniReserves[0], uniReserves[1]), nil
					}
					return nil, fmt.Errorf("unexpected call in test: %s", msg.To.Hex())
				})
			},
			validate: func(t *testing.T, inputPools []common.Address, token0s, token1s []common.Address, poolTypes []uint8, feeBps []uint16, reserve0s, reserve1s []*big.Int, errs []error) {
				require.Len(t, errs, 1)
				assert.NoError(t, errs[0])
				assert.Equal(t, usdcAddr, token0s[0])
				assert.Equal(t, wethAddr, token1s[0])
				assert.Equal(t, uint16(30), feeBps[0])
				assert.Equal(t, uint8(0), poolTypes[0])
				assert.Equal(t, uniReserves[0], reserve0s[0])
			},
		},
		{
			name:      "Happy Path - Multiple different known pools",
			poolAddrs: []common.Address{uniPoolAddr, sushiPoolAddr},
			setupHandler: func(t *testing.T, client *ethclients.TestETHClient) {
				client.SetCallContractHandler(func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
					// Handle Uni Pool
					if msg.To.Hex() == uniPoolAddr.Hex() {
						switch {
						case common.Bytes2Hex(msg.Data) == common.Bytes2Hex(factorySig):
							return addressToBytes(knownUniFactory.Address), nil
						case common.Bytes2Hex(msg.Data) == common.Bytes2Hex(token0Sig):
							return addressToBytes(usdcAddr), nil
						case common.Bytes2Hex(msg.Data) == common.Bytes2Hex(token1Sig):
							return addressToBytes(wethAddr), nil
						case common.Bytes2Hex(msg.Data) == common.Bytes2Hex(getReservesSig):
							return reservesToBytes(uniReserves[0], uniReserves[1]), nil
						}
					}
					// Handle Sushi Pool
					if msg.To.Hex() == sushiPoolAddr.Hex() {
						switch {
						case common.Bytes2Hex(msg.Data) == common.Bytes2Hex(factorySig):
							return addressToBytes(knownSushiFactory.Address), nil
						case common.Bytes2Hex(msg.Data) == common.Bytes2Hex(token0Sig):
							return addressToBytes(wethAddr), nil
						case common.Bytes2Hex(msg.Data) == common.Bytes2Hex(token1Sig):
							return addressToBytes(mmyAddr), nil
						case common.Bytes2Hex(msg.Data) == common.Bytes2Hex(getReservesSig):
							return reservesToBytes(sushiReserves[0], sushiReserves[1]), nil
						}
					}
					return nil, fmt.Errorf("unexpected call in test: %s", msg.To.Hex())
				})
			},
			validate: func(t *testing.T, inputPools []common.Address, token0s, token1s []common.Address, poolTypes []uint8, feeBps []uint16, reserve0s, reserve1s []*big.Int, errs []error) {
				require.Len(t, errs, 2)
				// Use a map to validate results since concurrency means order is not guaranteed.
				results := make(map[string]map[string]interface{})
				for i, addr := range inputPools {
					results[addr.Hex()] = map[string]interface{}{
						"t0":   token0s[i],
						"t1":   token1s[i],
						"fee":  feeBps[i],
						"type": poolTypes[i],
						"r0":   reserve0s[i],
						"r1":   reserve1s[i],
						"err":  errs[i],
					}
				}

				// Validate Uniswap pool results
				uniResults, ok := results[uniPoolAddr.Hex()]
				require.True(t, ok, "Uniswap pool results not found")
				assert.Nil(t, uniResults["err"], "Uniswap pool should not have an error")
				assert.Equal(t, usdcAddr, uniResults["t0"])
				assert.Equal(t, wethAddr, uniResults["t1"])
				assert.Equal(t, uint16(30), uniResults["fee"])

				// Validate SushiSwap pool results
				sushiResults, ok := results[sushiPoolAddr.Hex()]
				require.True(t, ok, "SushiSwap pool results not found")
				assert.Nil(t, sushiResults["err"], "SushiSwap pool should not have an error")
				assert.Equal(t, wethAddr, sushiResults["t0"])
				assert.Equal(t, mmyAddr, sushiResults["t1"])
				assert.Equal(t, uint16(30), sushiResults["fee"])
			},
		},
		{
			name:      "Error Case - Unknown factory",
			poolAddrs: []common.Address{unknownPoolAddr},
			setupHandler: func(t *testing.T, client *ethclients.TestETHClient) {
				client.SetCallContractHandler(func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
					if msg.To.Hex() == unknownPoolAddr.Hex() && common.Bytes2Hex(msg.Data) == common.Bytes2Hex(factorySig) {
						return addressToBytes(common.HexToAddress("0xdeadbeef")), nil
					}
					return nil, fmt.Errorf("unexpected call in test: %s", msg.To.Hex())
				})
			},
			validate: func(t *testing.T, inputPools []common.Address, token0s, token1s []common.Address, poolTypes []uint8, feeBps []uint16, reserve0s, reserve1s []*big.Int, errs []error) {
				require.Error(t, errs[0])
				assert.Contains(t, errs[0].Error(), "unknown factory")
			},
		},
		{
			name:      "Error Case - RPC call for tokens fails",
			poolAddrs: []common.Address{failingPoolAddr},
			setupHandler: func(t *testing.T, client *ethclients.TestETHClient) {
				client.SetCallContractHandler(func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
					if msg.To.Hex() == failingPoolAddr.Hex() {
						if common.Bytes2Hex(msg.Data) == common.Bytes2Hex(factorySig) {
							return addressToBytes(knownUniFactory.Address), nil
						}
						if common.Bytes2Hex(msg.Data) == common.Bytes2Hex(token0Sig) {
							return nil, errors.New("rpc error")
						}
					}
					return nil, fmt.Errorf("unexpected call in test: %s", msg.To.Hex())
				})
			},
			validate: func(t *testing.T, inputPools []common.Address, token0s, token1s []common.Address, poolTypes []uint8, feeBps []uint16, reserve0s, reserve1s []*big.Int, errs []error) {
				require.Error(t, errs[0])
				assert.Contains(t, errs[0].Error(), "failed to get tokens")
			},
		},
		{
			name:      "Context Cancellation",
			poolAddrs: []common.Address{uniPoolAddr},
			setupHandler: func(t *testing.T, client *ethclients.TestETHClient) {
				// No handler needed, the context is cancelled before the call.
			},
			validate: func(t *testing.T, inputPools []common.Address, token0s, token1s []common.Address, poolTypes []uint8, feeBps []uint16, reserve0s, reserve1s []*big.Int, errs []error) {
				require.Error(t, errs[0])
				assert.ErrorIs(t, errs[0], context.Canceled)
			},
		},
		{
			name:         "Boundary Case - Empty input slice",
			poolAddrs:    []common.Address{},
			setupHandler: func(t *testing.T, client *ethclients.TestETHClient) {},
			validate: func(t *testing.T, inputPools []common.Address, token0s, token1s []common.Address, poolTypes []uint8, feeBps []uint16, reserve0s, reserve1s []*big.Int, errs []error) {
				assert.Empty(t, token0s)
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

			initializer := NewPoolInitializer(knownFactories)

			ctx := context.Background()
			if tc.name == "Context Cancellation" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(context.Background())
				cancel() // Cancel context immediately
			}

			token0s, token1s, poolTypes, feeBps, reserve0s, reserve1s, errs := initializer.Initialize(ctx, tc.poolAddrs, client)

			// Basic length checks
			assert.Len(t, token0s, len(tc.poolAddrs))
			assert.Len(t, errs, len(tc.poolAddrs))

			// Detailed validation
			if tc.validate != nil {
				tc.validate(t, tc.poolAddrs, token0s, token1s, poolTypes, feeBps, reserve0s, reserve1s, errs)
			}
		})
	}
}
