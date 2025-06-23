package logs

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// DiscoverPools iterates through a slice of Ethereum logs and identifies the addresses
// of Uniswap V2 pools by finding their unique 'Swap' events.
func DiscoverPools(logs []types.Log) ([]common.Address, error) {
	// Use a map to efficiently store unique pool addresses, preventing duplicates.
	discoveredPools := make(map[common.Address]struct{})

	for _, log := range logs {
		// A Uniswap V2 Swap event is identified by its unique topic hash.
		// It must have at least one topic to be a valid event.
		if len(log.Topics) > 0 && log.Topics[0] == UniswapV2SwapEvent {
			// For a Swap event, the address of the contract that emitted the log
			// (`log.Address`) is the address of the pool itself (the Uniswap Pair).
			discoveredPools[log.Address] = struct{}{}
		}
	}

	// Convert the map of unique addresses to a slice for the return value.
	pools := make([]common.Address, 0, len(discoveredPools))
	for poolAddr := range discoveredPools {
		pools = append(pools, poolAddr)
	}

	return pools, nil
}
