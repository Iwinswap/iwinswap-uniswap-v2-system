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
	token0Sig      = abi.UniswapV2ABI.Methods["token0"].ID
	token1Sig      = abi.UniswapV2ABI.Methods["token1"].ID
	getReservesSig = abi.UniswapV2ABI.Methods["getReserves"].ID
	factorySig     = abi.UniswapV2ABI.Methods["factory"].ID
)

const (
	// defaultRPCTimeout defines the default timeout for individual RPC calls.
	defaultRPCTimeout = 10 * time.Second
)

// KnownFactory holds configuration for a recognized Uniswap V2-style protocol.
type KnownFactory struct {
	Address      common.Address
	ProtocolName string
	FeeBps       uint16
}

// PoolInitializer is a struct that holds the configuration needed to initialize pools
// from a curated list of supported DEX protocols. It manages a global concurrency
// limit for all its operations.
type PoolInitializer struct {
	factoryMap map[common.Address]KnownFactory
	semaphore  chan struct{}
}

// NewPoolInitializer creates and configures a new PoolInitializer instance.
// It takes a list of known factories and a concurrency limit, returning an error
// if the concurrency limit is not a positive number.
func NewPoolInitializer(knownFactories []KnownFactory, maxConcurrentCalls int) (*PoolInitializer, error) {
	if maxConcurrentCalls <= 0 {
		return nil, fmt.Errorf("maxConcurrentCalls must be a positive number, but got %d", maxConcurrentCalls)
	}
	factoryMap := make(map[common.Address]KnownFactory, len(knownFactories))
	for _, f := range knownFactories {
		factoryMap[f.Address] = f
	}
	return &PoolInitializer{
		factoryMap: factoryMap,
		// The semaphore is created once and shared by all calls to Initialize on this instance.
		semaphore: make(chan struct{}, maxConcurrentCalls),
	}, nil
}

// Initialize takes a batch of potential pool addresses and attempts to initialize them.
// It identifies the protocol and fee by matching the pool's factory against its configured list.
// The entire batch operation is governed by the provided context for cancellation, and the
// number of concurrent RPC calls is limited by the initializer's shared semaphore.
func (p *PoolInitializer) Initialize(
	ctx context.Context,
	poolAddrs []common.Address,
	client ethclients.ETHClient,
) (token0s, token1s []common.Address, poolTypes []uint8, feeBps []uint16, reserve0s, reserve1s []*big.Int, errs []error) {
	numPools := len(poolAddrs)
	if numPools == 0 {
		return nil, nil, nil, nil, nil, nil, nil
	}

	// Pre-allocate result slices to the exact size needed.
	token0s = make([]common.Address, numPools)
	token1s = make([]common.Address, numPools)
	poolTypes = make([]uint8, numPools)
	feeBps = make([]uint16, numPools)
	reserve0s = make([]*big.Int, numPools)
	reserve1s = make([]*big.Int, numPools)
	errs = make([]error, numPools)

	var wg sync.WaitGroup

	for i, addr := range poolAddrs {
		wg.Add(1)
		// This will block until a slot in the shared semaphore is free.
		p.semaphore <- struct{}{}

		go func(index int, poolAddr common.Address) {
			defer wg.Done()
			// Release the semaphore slot when the goroutine finishes.
			defer func() { <-p.semaphore }()

			if ctx.Err() != nil {
				errs[index] = ctx.Err()
				return
			}

			// 1. Identify the pool's factory.
			factoryAddr, err := getFactory(ctx, poolAddr, client)
			if err != nil {
				errs[index] = fmt.Errorf("could not get factory for pool %s: %w", poolAddr.Hex(), err)
				return
			}

			// 2. Validate against the configured list.
			factoryConfig, ok := p.factoryMap[factoryAddr]
			if !ok {
				errs[index] = fmt.Errorf("pool %s has an unknown factory %s", poolAddr.Hex(), factoryAddr.Hex())
				return
			}

			// 3. Get token addresses.
			t0, t1, err := getTokens(ctx, poolAddr, client)
			if err != nil {
				errs[index] = fmt.Errorf("failed to get tokens: %w", err)
				return
			}

			// 4. Get reserves.
			r0, r1, err := getReserves(ctx, poolAddr, client)
			if err != nil {
				errs[index] = fmt.Errorf("failed to get reserves: %w", err)
				return
			}

			// 5. Success! Populate results.
			token0s[index] = t0
			token1s[index] = t1
			reserve0s[index] = r0
			reserve1s[index] = r1
			poolTypes[index] = 0 // Standard Uniswap V2 pool type
			feeBps[index] = factoryConfig.FeeBps

		}(i, addr)
	}

	wg.Wait()

	return token0s, token1s, poolTypes, feeBps, reserve0s, reserve1s, errs
}

// getFactory fetches the factory address for a single pool by making an RPC call.
func getFactory(parentCtx context.Context, poolAddr common.Address, client ethclients.ETHClient) (common.Address, error) {
	ctx, cancel := context.WithTimeout(parentCtx, defaultRPCTimeout)
	defer cancel()

	factoryCallData, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &poolAddr,
		Data: factorySig,
	}, nil)
	if err != nil {
		return common.Address{}, fmt.Errorf("eth_call for factory failed: %w", err)
	}
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
	if len(reservesCallData) != 96 {
		return nil, nil, fmt.Errorf("invalid response length for getReserves: got %d bytes", len(reservesCallData))
	}

	reserve0 := new(big.Int).SetBytes(reservesCallData[0:32])
	reserve1 := new(big.Int).SetBytes(reservesCallData[32:64])

	return reserve0, reserve1, nil
}
