package pocket

import "errors"

var (
	// ErrTerminal is returned for any event applied to a terminal state.
	ErrTerminal = errors.New("pocket: state is terminal")
	// ErrIllegalTransition is returned when an event has no edge from the
	// current state.
	ErrIllegalTransition = errors.New("pocket: illegal transition for current state")
	// ErrCodeLocked is returned when the Release Code is locked, including for
	// an otherwise-correct code submitted after lockout.
	ErrCodeLocked = errors.New("pocket: release code entry is locked")
	// ErrNotYetDue is returned when a timer-driven transition fires before its
	// deadline.
	ErrNotYetDue = errors.New("pocket: timer has not elapsed")
	// ErrWindowClosed is returned when a buyer reports an issue after the
	// inspection window has closed.
	ErrWindowClosed = errors.New("pocket: inspection window has closed")
	// ErrInvalidSpec is returned by New for a spec that violates a construction
	// invariant.
	ErrInvalidSpec = errors.New("pocket: invalid pocket spec")
)
