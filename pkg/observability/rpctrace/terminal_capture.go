package rpctrace

import (
	"context"
	"strings"
	"sync"
)

// TerminalTraceState distinguishes missing terminal evidence from a wire join.
type TerminalTraceState uint8

const (
	TerminalTraceUnknown TerminalTraceState = iota
	TerminalTraceBound
	TerminalTraceUnbound
	TerminalTraceRecorderDisabled
	TerminalTraceRecorderExhausted
)

// TerminalIdentity retains only one completed client RPC's bounded identity.
type TerminalIdentity struct {
	// State preserves why a join is missing without retaining arbitrary error text.
	State TerminalTraceState
	// TraceID is one bounded recorder-generated identity, never caller payload.
	TraceID string
	// Unbound is true only for a known local-only identity, never missing evidence.
	Unbound bool
}

// String is a closed diagnostic value; missing observations never mean bound.
func (s TerminalTraceState) String() string {
	switch s {
	case TerminalTraceBound:
		return "bound"
	case TerminalTraceUnbound:
		return "unbound"
	case TerminalTraceRecorderDisabled:
		return "recorder_disabled"
	case TerminalTraceRecorderExhausted:
		return "recorder_exhausted"
	default:
		return "unknown"
	}
}

// Valid rejects identities that cannot be emitted by the bounded recorder.
func (i TerminalIdentity) Valid() bool {
	switch i.State {
	case TerminalTraceUnknown, TerminalTraceRecorderDisabled, TerminalTraceRecorderExhausted:
		return i.TraceID == "" && !i.Unbound
	case TerminalTraceBound, TerminalTraceUnbound:
		if i.Unbound != (i.State == TerminalTraceUnbound) {
			return false
		}
		id := i.TraceID
		if i.Unbound {
			if !strings.HasPrefix(id, "local/") {
				return false
			}
			id = strings.TrimPrefix(id, "local/")
		}
		token, request, found := strings.Cut(id, "/")
		return found && len(token) == 32 && len(request) > 0 && len(request) <= 16 && terminalHex(token) && terminalHex(request)
	default:
		return false
	}
}

func terminalHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// TerminalCapture is one exact Call's observation, never a sample registry.
// It is single-use: the first terminal publication wins, including missing evidence.
type TerminalCapture struct {
	mu       sync.Mutex
	identity TerminalIdentity
}

type terminalCaptureKey struct{}

// WithTerminalCapture requests fixed terminal metadata without creating a recorder.
func WithTerminalCapture(ctx context.Context) (context.Context, *TerminalCapture) {
	if ctx == nil {
		ctx = context.Background()
	}
	c := &TerminalCapture{}
	return context.WithValue(ctx, terminalCaptureKey{}, c), c
}

// TerminalCaptureFrom returns the exact caller-owned terminal observation.
func TerminalCaptureFrom(ctx context.Context) *TerminalCapture {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(terminalCaptureKey{}).(*TerminalCapture)
	return c
}

// Snapshot returns a detached fixed-value observation. A zero value is unknown.
func (c *TerminalCapture) Snapshot() TerminalIdentity {
	if c == nil {
		return TerminalIdentity{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.identity
}

// IdentityFor validates the endpoint and service before exposing fixed identity.
// A finished trace retains its identity; it never retains or revives live work.
func (t *Trace) IdentityFor(side Side, serviceID uint16) TerminalIdentity {
	if t == nil {
		return TerminalIdentity{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.side != side || t.serviceID != serviceID {
		return TerminalIdentity{}
	}
	state := TerminalTraceBound
	if t.unbound {
		state = TerminalTraceUnbound
	}
	identity := TerminalIdentity{State: state, TraceID: t.id, Unbound: t.unbound}
	if !identity.Valid() {
		return TerminalIdentity{}
	}
	return identity
}

// RecorderDisabled records explicit missing evidence without enabling tracing.
func (c *TerminalCapture) RecorderDisabled() {
	c.record(TerminalIdentity{State: TerminalTraceRecorderDisabled})
}

func (c *TerminalCapture) record(identity TerminalIdentity) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.identity.State == TerminalTraceUnknown {
		c.identity = identity
	}
}
