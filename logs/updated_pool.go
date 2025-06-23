package logs

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// syncData is an internal struct to hold the parsed reserve data for a pool.
type syncData struct {
	reserve0 *big.Int
	reserve1 *big.Int
}

// UpdatedInBlock parses a slice of logs and returns the final reserve state for each
// Uniswap V2 pool that emitted a Sync event within that block.
func UpdatedInBlock(logs []types.Log) (pools []common.Address, reserve0, reserve1 []*big.Int, err error) {
	// Use a map to store the latest sync data for each pool.
	// As we iterate through the logs in order, we can simply overwrite the entry
	// for a pool, ensuring that only the data from the final Sync event remains.
	latestSyncs := make(map[common.Address]syncData)

	for _, log := range logs {
		// A Sync event is uniquely identified by its topic hash and has no other indexed topics.
		if len(log.Topics) == 1 && log.Topics[0] == UniswapV2SyncEvent {
			// The event's Data field contains reserve0 and reserve1, each packed as 32 bytes.
			if len(log.Data) != 64 {
				// This is not a valid Sync event. Skip it to prevent panics.
				continue
			}

			// Unpack the reserves from the raw log data.
			r0 := new(big.Int).SetBytes(log.Data[:32])
			r1 := new(big.Int).SetBytes(log.Data[32:])

			// Store or overwrite the reserve data for this pool.
			latestSyncs[log.Address] = syncData{
				reserve0: r0,
				reserve1: r1,
			}
		}
	}

	// If no Sync events were found in the entire block, return early.
	if len(latestSyncs) == 0 {
		return nil, nil, nil, nil
	}

	// Convert the map to the three required parallel slices for output.
	// Pre-allocating capacity is a good practice for performance.
	pools = make([]common.Address, 0, len(latestSyncs))
	reserve0 = make([]*big.Int, 0, len(latestSyncs))
	reserve1 = make([]*big.Int, 0, len(latestSyncs))

	for poolAddr, data := range latestSyncs {
		pools = append(pools, poolAddr)
		reserve0 = append(reserve0, data.reserve0)
		reserve1 = append(reserve1, data.reserve1)
	}

	return pools, reserve0, reserve1, nil
}
