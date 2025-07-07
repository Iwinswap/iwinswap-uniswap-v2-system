package reserves

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
	// getReservesSig is the method signature for the getReserves() contract call.
	// Loading it from the shared ABI package ensures consistency and maintainability.
	getReservesSig = abi.UniswapV2ABI.Methods["getReserves"].ID
)

const (
	// defaultRPCTimeout defines the default timeout for individual RPC calls.
	defaultRPCTimeout = 10 * time.Second
)

// NewGetReserves returns a new function for fetching reserves that limits the
// number of concurrent RPC calls to the provided `maxConcurrentCalls`.
// it  returns a function that matches the uniswapv2.GetReservesFunc type and can be injected as a dependency
func NewGetReserves(
	maxConcurrentCalls int,
) func(
	ctx context.Context,
	poolAddrs []common.Address,
	client ethclients.ETHClient,
) (reserve0s, reserve1s []*big.Int, errs []error) {

	// The returned function closes over the semaphore channel.
	semaphore := make(chan struct{}, maxConcurrentCalls)

	return func(
		ctx context.Context,
		poolAddrs []common.Address,
		client ethclients.ETHClient,
	) (reserve0s, reserve1s []*big.Int, errs []error) {
		numPools := len(poolAddrs)
		if numPools == 0 {
			return nil, nil, nil
		}

		// Pre-allocate result slices to the exact size needed. This is crucial for
		// safely writing results from concurrent goroutines into the correct index.
		reserve0s = make([]*big.Int, numPools)
		reserve1s = make([]*big.Int, numPools)
		errs = make([]error, numPools)

		var wg sync.WaitGroup
		wg.Add(numPools)

		for i, addr := range poolAddrs {
			// This will block until a spot is available in the semaphore channel,
			// effectively limiting the number of concurrent goroutines.
			semaphore <- struct{}{}

			go func(index int, poolAddr common.Address) {
				defer func() {
					// Release the spot in the semaphore channel once the goroutine is done.
					<-semaphore
					wg.Done()
				}()

				if ctx.Err() != nil {
					errs[index] = ctx.Err()
					return
				}

				r0, r1, err := getReservesForPool(ctx, poolAddr, client)
				if err != nil {
					errs[index] = err
					return
				}

				reserve0s[index] = r0
				reserve1s[index] = r1
			}(i, addr)
		}

		wg.Wait()

		return reserve0s, reserve1s, errs
	}
}

// getReservesForPool performs the actual RPC call for a single pool.
func getReservesForPool(parentCtx context.Context, poolAddr common.Address, client ethclients.ETHClient) (*big.Int, *big.Int, error) {
	ctx, cancel := context.WithTimeout(parentCtx, defaultRPCTimeout)
	defer cancel()

	// Perform the eth_call to the contract's getReserves() method.
	reservesCallData, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &poolAddr,
		Data: getReservesSig,
	}, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("eth_call for getReserves failed for pool %s: %w", poolAddr.Hex(), err)
	}

	// The getReserves() function returns (uint112 reserve0, uint112 reserve1, uint32 blockTimestampLast).
	// The return data is packed into three 32-byte slots, for a total of 96 bytes.
	if len(reservesCallData) != 96 {
		return nil, nil, fmt.Errorf("invalid response length for getReserves on pool %s: got %d bytes", poolAddr.Hex(), len(reservesCallData))
	}

	// Unpack the reserve values from the first two 32-byte slots.
	// The final slot (blockTimestampLast) is ignored.
	reserve0 := new(big.Int).SetBytes(reservesCallData[0:32])
	reserve1 := new(big.Int).SetBytes(reservesCallData[32:64])

	return reserve0, reserve1, nil
}
