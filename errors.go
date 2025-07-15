package uniswapv2

import (
	"errors"
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

// Error provides a descriptive message, distinguishing between background and block-specific errors.
func (e *InitializationError) Error() string {
	if e.BlockNumber == 0 {
		return fmt.Sprintf("initializer: failed to initialize pool %s: %v", e.PoolAddress.Hex(), e.Err)
	}
	return fmt.Sprintf("block %d: failed to initialize pool %s: %v", e.BlockNumber, e.PoolAddress.Hex(), e.Err)
}

// RegistrationError is a critical error when the system fails to register
// a new, valid pool with the upstream persistence layer.
type RegistrationError struct {
	InitializationError
	Token0Address common.Address
	Token1Address common.Address
}

// Error provides a descriptive message, distinguishing between background and block-specific errors.
func (e *RegistrationError) Error() string {
	if e.BlockNumber == 0 {
		return fmt.Sprintf("CRITICAL initializer: failed to register new pool %s (tokens %s, %s): %v", e.PoolAddress.Hex(), e.Token0Address.Hex(), e.Token1Address.Hex(), e.Err)
	}
	return fmt.Sprintf("CRITICAL block %d: failed to register new pool %s (tokens %s, %s): %v", e.BlockNumber, e.PoolAddress.Hex(), e.Token0Address.Hex(), e.Token1Address.Hex(), e.Err)
}

// DataConsistencyError indicates a critical internal state mismatch,
// for example, failing to find a pool ID immediately after it was registered.
type DataConsistencyError struct {
	SystemError
	PoolAddress common.Address
	Details     string
}

// Error provides a descriptive message, distinguishing between background and block-specific errors.
func (e *DataConsistencyError) Error() string {
	if e.BlockNumber == 0 {
		return fmt.Sprintf("CRITICAL internal: data consistency error for pool %s: %s: %v", e.PoolAddress.Hex(), e.Details, e.Err)
	}
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

// Error provides a descriptive message, distinguishing between background and block-specific errors.
func (e *UpdateError) Error() string {
	if e.SystemError.BlockNumber == 0 {
		return fmt.Sprintf("reconciler: failed to update reserves for pool with id %d: %v", e.PoolID, e.Err)
	}
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

// determineErrorType inspects an error to classify it based on its underlying type.
// This helps in categorizing errors for metrics and logging. The order of checks
// is important for wrapped errors to ensure the most specific type is matched first.
func determineErrorType(err error) string {
	var regErr *RegistrationError
	var consistencyErr *DataConsistencyError
	var initErr *InitializationError
	var updateErr *UpdateError
	var prunerErr *PrunerError

	// Check from most specific to most general.
	if errors.As(err, &regErr) {
		return "critical_registration"
	}
	if errors.As(err, &consistencyErr) {
		return "critical_consistency"
	}
	if errors.As(err, &initErr) {
		return "pool_initialization"
	}
	if errors.As(err, &updateErr) {
		return "pool_update"
	}
	if errors.As(err, &prunerErr) {
		return "pruner"
	}

	return "unknown"
}
