package sendtrace

import (
	"context"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// RPCReference is observation-only ownership, never a routing or durability proof.
// Multiple original SENDs may reference one physical RPC without selecting one
// of them as that shared request's parent. Empty/missing references are unknown.
type RPCReference struct {
	State   string `json:"state"`
	TraceID string `json:"trace_id"`
}

// Valid accepts only recorder identities and closed explicit missing states.
func (r RPCReference) Valid() bool {
	switch r.State {
	case "bound", "unbound":
		id := r.TraceID
		if r.State == "unbound" {
			if !strings.HasPrefix(id, "local/") {
				return false
			}
			id = strings.TrimPrefix(id, "local/")
		}
		token, request, found := strings.Cut(id, "/")
		if !found || len(token) != 32 || len(request) < 1 || len(request) > 16 {
			return false
		}
		for _, part := range []string{token, request} {
			for _, c := range part {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
					return false
				}
			}
		}
		value, err := strconv.ParseUint(request, 16, 64)
		return err == nil && value != 0
	case "recorder_disabled", "recorder_exhausted", "local_untraced", "remote_untraced":
		return r.TraceID == ""
	default:
		return false
	}
}

// CloneValid detaches a valid reference; malformed evidence remains absent.
func (r *RPCReference) CloneValid() *RPCReference {
	if r == nil || !r.Valid() {
		return nil
	}
	copy := *r
	return &copy
}

// RPCReferenceForTerminal preserves one exact Call's authoritative terminal state.
func RPCReferenceForTerminal(identity rpctrace.TerminalIdentity) *RPCReference {
	if !identity.Valid() {
		return nil
	}
	ref := &RPCReference{State: identity.State.String(), TraceID: identity.TraceID}
	return ref.CloneValid()
}

type appendRPCReferenceKey struct{}

// WithAppendRPCReference declares a trusted local/remote entry without changing
// its cancellation or deadline. Futures extract only the detached fixed value.
func WithAppendRPCReference(ctx context.Context, ref RPCReference) context.Context {
	if !ref.Valid() {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, appendRPCReferenceKey{}, ref)
}

// AppendRPCReferenceFrom reads an explicit entry declaration; nil is unknown.
func AppendRPCReferenceFrom(ctx context.Context) *RPCReference {
	if ctx == nil {
		return nil
	}
	ref, ok := ctx.Value(appendRPCReferenceKey{}).(RPCReference)
	if !ok {
		return nil
	}
	return ref.CloneValid()
}
