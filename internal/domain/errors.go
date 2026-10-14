package domain

import "errors"

// Domain errors. Mapped to HTTP status in one place (internal/api).
var (
	ErrNotFound   = errors.New("not found")
	ErrInvalid    = errors.New("invalid input")
	ErrConflict   = errors.New("conflict")
	ErrActiveRun  = errors.New("task already has an active run")
	ErrNotAllowed = errors.New("not allowed")
)

// ValidationError carries a field-level reason and unwraps to ErrInvalid.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Reason }
func (e *ValidationError) Unwrap() error { return ErrInvalid }

func Invalid(field, reason string) error { return &ValidationError{Field: field, Reason: reason} }
