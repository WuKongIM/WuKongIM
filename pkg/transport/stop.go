package transport

import (
	"errors"
	"fmt"
)

// ErrStopIncomplete means shutdown still owns work. Dependencies must remain
// alive and a later StopContext must join the same shutdown before disposal.
var ErrStopIncomplete = errors.New("transport: stop incomplete")

// StopIncompleteError is a bounded ownership snapshot at caller-budget expiry.
// Zero requests may still mean connection, executor, or observer cleanup remains.
type StopIncompleteError struct {
	cause         error
	requests      int
	retainedBytes int64
	connections   int
}

func (e *StopIncompleteError) Error() string {
	return fmt.Sprintf("%v: requests=%d retained_bytes=%d connections=%d: %v", ErrStopIncomplete, e.requests, e.retainedBytes, e.connections, e.cause)
}

// Unwrap preserves the caller's cancellation or deadline cause.
func (e *StopIncompleteError) Unwrap() error { return e.cause }

// Is lets composition retain dependencies without parsing diagnostic text.
func (e *StopIncompleteError) Is(target error) bool { return target == ErrStopIncomplete }

// StopIncomplete lets lifecycle composition retain dependencies without
// depending on the concrete transport error type.
func (e *StopIncompleteError) StopIncomplete() bool { return true }

// UnfinishedRequests includes queued/executing server requests or unfinished
// client calls, sends and peer-close operations.
func (e *StopIncompleteError) UnfinishedRequests() int { return e.requests }

// RetainedBytes counts initial request backing bytes until the owner completes,
// excluding connection queues and handler results; it is not process memory.
func (e *StopIncompleteError) RetainedBytes() int64 { return e.retainedBytes }
