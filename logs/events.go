package logs

import "github.com/Iwinswap/iwinswap-uniswap-v2-system/abi"

var (
	UniswapV2SwapEvent = abi.UniswapV2ABI.Events["Swap"].ID
	UniswapV2SyncEvent = abi.UniswapV2ABI.Events["Sync"].ID
)
