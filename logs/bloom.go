package logs

import (
	"github.com/ethereum/go-ethereum/core/types"
)

func SwapEventInBloom(bloom types.Bloom) bool {
	return bloom.Test(UniswapV2SwapEvent.Bytes())
}
