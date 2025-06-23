package uniswapv2

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// SystemError is a base type for errors originating from the UniswapV2System.
type SystemError struct {
	BlockNumber uint64
	Err         error
}

func (e *SystemError) Error() string {
	return fmt.Sprintf("block %d: %v", e.BlockNumber, e.Err)
}

func (e *SystemError) Unwrap() error {
	return e.Err
}

// InitializationError indicates a failure during the initialization of a new pool.
type InitializationError struct {
	SystemError
	PoolAddress common.Address
}

func (e *InitializationError) Error() string {
	return fmt.Sprintf("block %d: failed to initialize pool %s: %v", e.BlockNumber, e.PoolAddress.Hex(), e.Err)
}

// RegistrationError is a critical error when the system fails to register
// a new, valid pool with the upstream persistence layer.
type RegistrationError struct {
	InitializationError
	Token0Address common.Address
	Token1Address common.Address
}

func (e *RegistrationError) Error() string {
	return fmt.Sprintf("CRITICAL block %d: failed to register new pool %s (tokens %s, %s): %v", e.BlockNumber, e.PoolAddress.Hex(), e.Token0Address.Hex(), e.Token1Address.Hex(), e.Err)
}

// DataConsistencyError indicates a critical internal state mismatch,
// for example, failing to find a pool ID immediately after it was registered.
type DataConsistencyError struct {
	SystemError
	PoolAddress common.Address
	Details     string
}

func (e *DataConsistencyError) Error() string {
	return fmt.Sprintf("CRITICAL block %d: data consistency error for pool %s: %s: %v", e.BlockNumber, e.PoolAddress.Hex(), e.Details, e.Err)
}

// UpdateError indicates a failure to update the reserves of a known, existing pool.
type UpdateError struct {
	SystemError
	PoolAddress common.Address
	PoolID      uint64
	Reserve0    *big.Int
	Reserve1    *big.Int
}

func (e *UpdateError) Error() string {
	return fmt.Sprintf("block %d: failed to update reserves for pool %s (id %d): %v", e.BlockNumber, e.PoolAddress.Hex(), e.PoolID, e.Err)
}

// PrunerError indicates a failure during the periodic pruning process.
type PrunerError struct {
	Err    error
	PoolID uint64
}

func (e *PrunerError) Error() string {
	return fmt.Sprintf("pruner: failed to process pool ID %d: %v", e.PoolID, e.Err)
}

func (e *PrunerError) Unwrap() error {
	return e.Err
}
