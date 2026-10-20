package domain

import (
	"errors"
	"fmt"
)

// Domain errors. Mapped to HTTP status in one place (internal/api).
var (
	ErrNotFound   = errors.New("not found")
	ErrInvalid    = errors.New("invalid input")
	ErrConflict   = errors.New("conflict")
	ErrActiveRun  = errors.New("task already has an active run")
	ErrNotAllowed = errors.New("not allowed")

	// ErrBudgetExceeded: the persona or the company spent its monthly budget; no run is opened.
	ErrBudgetExceeded = fmt.Errorf("monthly budget exceeded: %w", ErrConflict)
)

// ErrSubscriptionGuard: the weekly guard of the persona's subscription tripped; no run is opened until the next week.
var ErrSubscriptionGuard = fmt.Errorf("weekly subscription guard: %w", ErrConflict)

// ErrPersonaUnavailable: the persona cannot take a chat message now (paused, in error, off...); the reason
// travels in the message.
var ErrPersonaUnavailable = fmt.Errorf("persona unavailable: %w", ErrConflict)

// ErrPersonaNotReady: the last readiness test of the persona failed (binary, auth or model); no run is opened until a
// test passes (ADR-0055). The reason travels in the message.
var ErrPersonaNotReady = fmt.Errorf("persona not ready: %w", ErrConflict)

// ValidationError carries a field-level reason and unwraps to ErrInvalid.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Reason }
func (e *ValidationError) Unwrap() error { return ErrInvalid }

func Invalid(field, reason string) error { return &ValidationError{Field: field, Reason: reason} }

// ErrUnauthenticated: credencial ausente, inválida, expirada ou revogada (sessão, API key).
var ErrUnauthenticated = errors.New("unauthenticated")
