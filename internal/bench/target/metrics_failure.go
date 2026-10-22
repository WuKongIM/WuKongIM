package target

import (
	"context"
	"errors"
)

// MetricsFailureKind is a closed diagnostic class, never a verdict or raw error.
type MetricsFailureKind string

type metricsFailureClass uint8

const (
	metricsFailureNone metricsFailureClass = iota
	metricsFailureConfiguration
	metricsFailureContextCanceled
	metricsFailureContextDeadline
	metricsFailureTransport
	metricsFailureAuth
	metricsFailureHTTPStatus
	metricsFailureResponseLimit
	metricsFailureBodyRead
	metricsFailureParse
	metricsFailureMixed
	metricsFailureUnknown
)

var metricsFailureKinds = [...]MetricsFailureKind{
	"", "configuration", "context_canceled", "context_deadline", "transport",
	"auth", "http_status", "response_limit", "body_read", "parse", "mixed", "unknown",
}

// Bounded discards arbitrary text supplied by an external diagnostic caller.
func (k MetricsFailureKind) Bounded() MetricsFailureKind {
	for _, known := range metricsFailureKinds[1:] {
		if k == known {
			return known
		}
	}
	return metricsFailureKinds[metricsFailureUnknown]
}

func (c metricsFailureClass) kind() MetricsFailureKind {
	if c <= metricsFailureNone || c > metricsFailureUnknown {
		return metricsFailureKinds[metricsFailureUnknown]
	}
	return metricsFailureKinds[c]
}

// metricsError exposes only its closed class; the original causal chain stays private.
type metricsError struct {
	// class identifies the failed boundary without retaining endpoint text.
	class metricsFailureClass
	// cause preserves exactly the error chain returned by the original client.
	cause error
}

func (e *metricsError) Error() string {
	class := metricsFailureUnknown
	if e != nil {
		class = e.class
	}
	return "observation metrics " + string(class.kind())
}
func (e *metricsError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// MetricsFailure identifies a metrics-port failure without formatting its cause.
// A typed acquisition class wins over nested client-timeout context errors.
func MetricsFailure(err error) MetricsFailureKind {
	if err == nil {
		return ""
	}
	var failure *metricsError
	if errors.As(err, &failure) && failure != nil {
		return failure.class.kind()
	}
	canceled, deadline := errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded)
	switch {
	case canceled && deadline:
		return metricsFailureMixed.kind()
	case canceled:
		return metricsFailureContextCanceled.kind()
	case deadline:
		return metricsFailureContextDeadline.kind()
	default:
		return metricsFailureUnknown.kind()
	}
}

func metricsContextFailure(err error) metricsFailureClass {
	switch {
	case errors.Is(err, context.Canceled):
		return metricsFailureContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return metricsFailureContextDeadline
	default:
		return metricsFailureUnknown
	}
}

// metricsFailureSet retains only a fixed bitset across existing address fallback.
type metricsFailureSet uint16

func (s *metricsFailureSet) add(class metricsFailureClass) {
	if class <= metricsFailureNone || class > metricsFailureUnknown {
		class = metricsFailureUnknown
	}
	*s |= 1 << class
}
func (s metricsFailureSet) class() metricsFailureClass {
	found := metricsFailureNone
	for class := metricsFailureConfiguration; class <= metricsFailureUnknown; class++ {
		if s&(1<<class) == 0 {
			continue
		}
		if found != metricsFailureNone {
			return metricsFailureMixed
		}
		found = class
	}
	if found == metricsFailureNone {
		return metricsFailureUnknown
	}
	return found
}
