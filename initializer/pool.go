package initializer

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	ethclients "github.com/Iwinswap/iwinswap-ethclients"
	"github.com/Iwinswap/iwinswap-uniswap-v2-system/abi"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

var (
	// Method signatures for Uniswap V2 Pair contract calls, loaded from the ABI package.
	// This approach is safer and more maintainable than using hardcoded hashes.
	token0Sig      = abi.UniswapV2ABI.Methods["token0"].ID
	token1Sig      = abi.UniswapV2ABI.Methods["token1"].ID
	getReservesSig = abi.UniswapV2ABI.Methods["getReserves"].ID
	factorySig     = abi.UniswapV2ABI.Methods["factory"].ID
)

const (
	// defaultRPCTimeout defines the default timeout for individual RPC calls made by the initializer.
	// This prevents a single slow request from blocking a goroutine indefinitely.
	defaultRPCTimeout = 10 * time.Second
)

// KnownFactory holds configuration for a recognized Uniswap V2-style protocol.
// This struct acts as a security whitelist, ensuring we only interact with trusted protocols.
type KnownFactory struct {
	// Address is the unique, canonical factory address for the protocol (e.g., Uniswap V2's factory).
	Address common.Address
	// ProtocolName is a human-readable identifier (e.g., "Uniswap-V2", "SushiSwap").
	ProtocolName string
	// FeeBps is the protocol's trading fee in basis points (e.g., 30 for 0.3%).
	FeeBps uint16
}

// PoolInitializer is a struct that holds the configuration needed to initialize pools
// from a curated list of supported DEX protocols. By explicitly defining the factory,
// we guarantee that we "know" the deployer of the pool and can be sure of its internal logic.
// Using a struct encapsulates the configuration and makes the initializer instance reusable
// and its dependencies clear.
type PoolInitializer struct {
	// factoryMap provides an efficient, O(1) lookup from a factory address to its configuration.
	factoryMap map[common.Address]KnownFactory
}

// NewPoolInitializer creates and configures a new PoolInitializer instance.
// It takes a list of known factories and builds an efficient lookup map for internal use.
func NewPoolInitializer(knownFactories []KnownFactory) *PoolInitializer {
	factoryMap := make(map[common.Address]KnownFactory, len(knownFactories))
	for _, f := range knownFactories {
		factoryMap[f.Address] = f
	}
	return &PoolInitializer{
		factoryMap: factoryMap,
	}
}

// Initialize takes a batch of potential pool addresses and attempts to initialize them.
// It identifies the protocol and fee by matching the pool's factory against its configured list.
// The entire batch operation is governed by the provided context for cancellation.
// The returned poolTypes slice will contain a '0' for each successfully initialized pool,
// signifying the standard Uniswap V2 constant product market maker type.
func (p *PoolInitializer) Initialize(
	ctx context.Context,
	poolAddrs []common.Address,
	client ethclients.ETHClient,
) (token0s, token1s []common.Address, poolTypes []uint8, feeBps []uint16, reserve0s, reserve1s []*big.Int, errs []error) {
	numPools := len(poolAddrs)
	if numPools == 0 {
		return nil, nil, nil, nil, nil, nil, nil
	}

	// Pre-allocate result slices to the exact size needed. This avoids repeated
	// reallocations as we append results, improving performance.
	token0s = make([]common.Address, numPools)
	token1s = make([]common.Address, numPools)
	poolTypes = make([]uint8, numPools)
	feeBps = make([]uint16, numPools)
	reserve0s = make([]*big.Int, numPools)
	reserve1s = make([]*big.Int, numPools)
	errs = make([]error, numPools)

	// Use a WaitGroup to concurrently process all pools and wait for them to finish.
	var wg sync.WaitGroup
	wg.Add(numPools)

	for i, addr := range poolAddrs {
		// Launch a goroutine for each pool. This allows network requests for all pools
		// to happen in parallel, dramatically speeding up the initialization process.
		go func(index int, poolAddr common.Address) {
			defer wg.Done()

			// Immediately check if the parent context has been cancelled (e.g., by a timeout or shutdown signal).
			// This provides a fast exit path and prevents starting work that will be discarded.
			if ctx.Err() != nil {
				errs[index] = ctx.Err()
				return
			}

			// 1. Identify the pool's factory by calling the contract. This is the security check.
			factoryAddr, err := getFactory(ctx, poolAddr, client)
			if err != nil {
				errs[index] = fmt.Errorf("could not get factory for pool %s: %w", poolAddr.Hex(), err)
				return
			}

			// 2. Validate against the configured list of known factories.
			factoryConfig, ok := p.factoryMap[factoryAddr]
			if !ok {
				errs[index] = fmt.Errorf("pool %s has an unknown factory %s", poolAddr.Hex(), factoryAddr.Hex())
				return
			}

			// 3. Get token0 and token1 addresses.
			t0, t1, err := getTokens(ctx, poolAddr, client)
			if err != nil {
				errs[index] = fmt.Errorf("failed to get tokens: %w", err)
				return
			}

			// 4. Get the reserves for the pool.
			r0, r1, err := getReserves(ctx, poolAddr, client)
			if err != nil {
				errs[index] = fmt.Errorf("failed to get reserves: %w", err)
				return
			}

			// 5. Success! Populate the result slices at the correct index.
			token0s[index] = t0
			token1s[index] = t1
			reserve0s[index] = r0
			reserve1s[index] = r1
			// By identifying the pool via its factory, we guarantee it follows standard
			// V2 logic. We assign it type 0 to assert this is a standard pool with
			// no weird internal swap logic (e.g., fee-on-transfer handling).
			poolTypes[index] = 0
			feeBps[index] = factoryConfig.FeeBps

		}(i, addr)
	}

	// Block here until all goroutines have called wg.Done().
	wg.Wait()

	return token0s, token1s, poolTypes, feeBps, reserve0s, reserve1s, errs
}

// getFactory fetches the factory address for a single pool by making an RPC call.
func getFactory(parentCtx context.Context, poolAddr common.Address, client ethclients.ETHClient) (common.Address, error) {
	// Create a new context with a specific timeout for this RPC call.
	ctx, cancel := context.WithTimeout(parentCtx, defaultRPCTimeout)
	defer cancel()

	factoryCallData, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &poolAddr,
		Data: factorySig,
	}, nil)
	if err != nil {
		return common.Address{}, fmt.Errorf("eth_call for factory failed: %w", err)
	}
	// A valid address response from a view function is always 32 bytes long.
	if len(factoryCallData) != 32 {
		return common.Address{}, fmt.Errorf("invalid response length for factory: got %d bytes", len(factoryCallData))
	}

	return common.BytesToAddress(factoryCallData), nil
}

// getTokens fetches the token0 and token1 addresses for a single pool.
func getTokens(parentCtx context.Context, poolAddr common.Address, client ethclients.ETHClient) (common.Address, common.Address, error) {
	ctx, cancel := context.WithTimeout(parentCtx, defaultRPCTimeout)
	defer cancel()

	// --- Fetch token0 ---
	token0CallData, err := client.CallContract(ctx, ethereum.CallMsg{To: &poolAddr, Data: token0Sig}, nil)
	if err != nil {
		return common.Address{}, common.Address{}, fmt.Errorf("eth_call for token0 failed: %w", err)
	}
	if len(token0CallData) != 32 {
		return common.Address{}, common.Address{}, fmt.Errorf("invalid response length for token0: got %d bytes", len(token0CallData))
	}
	token0Addr := common.BytesToAddress(token0CallData)

	// --- Fetch token1 ---
	token1CallData, err := client.CallContract(ctx, ethereum.CallMsg{To: &poolAddr, Data: token1Sig}, nil)
	if err != nil {
		return common.Address{}, common.Address{}, fmt.Errorf("eth_call for token1 failed: %w", err)
	}
	if len(token1CallData) != 32 {
		return common.Address{}, common.Address{}, fmt.Errorf("invalid response length for token1: got %d bytes", len(token1CallData))
	}
	token1Addr := common.BytesToAddress(token1CallData)

	return token0Addr, token1Addr, nil
}

// getReserves fetches the reserves for a single pool.
func getReserves(parentCtx context.Context, poolAddr common.Address, client ethclients.ETHClient) (*big.Int, *big.Int, error) {
	ctx, cancel := context.WithTimeout(parentCtx, defaultRPCTimeout)
	defer cancel()

	reservesCallData, err := client.CallContract(ctx, ethereum.CallMsg{To: &poolAddr, Data: getReservesSig}, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("eth_call for getReserves failed: %w", err)
	}
	// The getReserves() function returns (uint112 reserve0, uint112 reserve1, uint32 blockTimestampLast).
	// The return data is packed into three 32-byte slots, for a total of 96 bytes.
	if len(reservesCallData) != 96 {
		return nil, nil, fmt.Errorf("invalid response length for getReserves: got %d bytes", len(reservesCallData))
	}

	// Unpack the reserve values from the first two 32-byte slots.
	// The final slot (blockTimestampLast) is ignored for this initializer's purpose.
	reserve0 := new(big.Int).SetBytes(reservesCallData[0:32])
	reserve1 := new(big.Int).SetBytes(reservesCallData[32:64])

	return reserve0, reserve1, nil
}
