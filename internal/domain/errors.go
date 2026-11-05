// Package domain holds the pure business rules of the warehouse service:
// entities, state machines, unit conversion, stock allocation (FEFO/FIFO)
// and FIFO costing. It has no knowledge of the database or HTTP.
package domain

import "fmt"

// Kind classifies a business error so that outer layers can map it to a
// transport-level status (for example an HTTP status code).
type Kind uint8

const (
	// KindInvalid means the request breaks a validation rule.
	KindInvalid Kind = iota + 1
	// KindNotFound means a referenced entity does not exist.
	KindNotFound
	// KindConflict means the request is valid but conflicts with the
	// current state (wrong status, not enough stock, duplicate key).
	KindConflict
)

// Error is a business error with a stable machine-readable code.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Details map[string]any
}

// Error implements the error interface.
func (e *Error) Error() string { return e.Code + ": " + e.Message }

// With returns the error with one more detail field set.
func (e *Error) With(key string, value any) *Error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

// Invalid creates a validation error.
func Invalid(code, format string, args ...any) *Error {
	return &Error{Kind: KindInvalid, Code: code, Message: fmt.Sprintf(format, args...)}
}

// NotFound creates a not-found error for the named entity.
func NotFound(entity string, id any) *Error {
	return &Error{
		Kind:    KindNotFound,
		Code:    entity + "_not_found",
		Message: fmt.Sprintf("%s %v not found", entity, id),
	}
}

// Conflict creates a state conflict error.
func Conflict(code, format string, args ...any) *Error {
	return &Error{Kind: KindConflict, Code: code, Message: fmt.Sprintf(format, args...)}
}

// Common error codes used across the service.
const (
	CodeValidation            = "validation_failed"
	CodeInvalidTransition     = "invalid_transition"
	CodeInsufficientStock     = "insufficient_stock"
	CodeInsufficientCost      = "insufficient_cost_layers"
	CodeLocationCounting      = "location_count_open"
	CodeCapacityExceeded      = "capacity_exceeded"
	CodeOverReceipt           = "over_receipt"
	CodeUnderReceipt          = "under_receipt"
	CodeDuplicate             = "duplicate"
	CodeUnknownUnit           = "unknown_unit"
	CodeLotRequired           = "lot_required"
	CodeLotNotAllowed         = "lot_not_allowed"
	CodeExpiryRequired        = "expiry_required"
	CodeLotExpiryMismatch     = "lot_expiry_mismatch"
	CodeWrongLocationType     = "wrong_location_type"
	CodeWrongWarehouse        = "wrong_warehouse"
	CodeQuantityOverflow      = "quantity_overflow"
	CodeCountAlreadyOpen      = "count_already_open"
	CodeUncountedLines        = "uncounted_lines"
	CodeTransferOverReceipt   = "transfer_over_receipt"
	CodeIdempotencyMismatch   = "idempotency_key_reused"
	CodeIdempotencyInProgress = "idempotency_in_progress"
)
