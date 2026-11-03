package sendtrace

import (
	"context"
	"time"
)

// PermissionAttempts retains at most 64 original value tokens for a shared read.
// It never retains member contexts, a parent Trace, policy facts or cancellation.
// Omitted tokens remain unknown rather than acquiring a representative identity.
type PermissionAttempts struct {
	tokens [64]AttemptToken
	count  uint8
}

type permissionAttemptObserver interface {
	RecordAttemptPermissionRPC(AttemptToken, RPCReference)
}

func activePermissionAttemptObserver() permissionAttemptObserver {
	observer := activeAttemptObserver()
	if observer == nil {
		return nil
	}
	permission, _ := observer.(permissionAttemptObserver)
	return permission
}

// AddContext copies only valid, unique observation tokens while capability is on.
func (s *PermissionAttempts) AddContext(ctx context.Context) {
	if activePermissionAttemptObserver() == nil || ctx == nil {
		return
	}
	if inherited, ok := ctx.Value(permissionAttemptsKey{}).(PermissionAttempts); ok {
		for i := uint8(0); i < inherited.count; i++ {
			s.add(inherited.tokens[i])
		}
	}
	s.add(GatewayAttemptFromContext(ctx))
}
func (s *PermissionAttempts) add(token AttemptToken) {
	if !token.Valid() || int(s.count) == len(s.tokens) {
		return
	}
	for i := uint8(0); i < s.count; i++ {
		if s.tokens[i] == token {
			return
		}
	}
	s.tokens[s.count] = token
	s.count++
}

// Enabled reports retained originals and the independent optional sink capability.
func (s PermissionAttempts) Enabled() bool {
	return s.count != 0 && activePermissionAttemptObserver() != nil
}

type permissionAttemptsKey struct{}

// WithContext publishes an immutable token copy on the chosen execution owner.
// Its deadline, cancellation and other values continue to belong to that owner.
func (s PermissionAttempts) WithContext(ctx context.Context) context.Context {
	if !s.Enabled() {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, permissionAttemptsKey{}, s)
}

// RecordRPC copies one actual physical call's terminal fact to each original.
// Missing/invalid evidence is absent, never a fabricated local or remote request.
func (s PermissionAttempts) RecordRPC(ref RPCReference) {
	if !ref.Valid() {
		return
	}
	if observer := activePermissionAttemptObserver(); observer != nil {
		for i := uint8(0); i < s.count; i++ {
			observer.RecordAttemptPermissionRPC(s.tokens[i], ref)
		}
	}
}

// RecordRPCEnvelope copies one actual remote SEND-permission service91 wrapper
// interval to its original tokens. It borrows the existing metric clock; missing
// metric clocks stay unknown. This is not server, network or exclusive CPU time.
func (s PermissionAttempts) RecordRPCEnvelope(startedAt time.Time, duration time.Duration, result Result) {
	if startedAt.IsZero() || duration < 0 {
		return
	}
	if observer := activeAttemptObserver(); observer != nil {
		phase := AttemptPhase{Stage: StageMessagePermissionRPC, StartedAt: startedAt, Duration: duration, Result: result, Boundary: "longest_origin_rpc91", EndDerived: true}
		for i := uint8(0); i < s.count; i++ {
			observer.RecordAttemptPhase(s.tokens[i], phase)
		}
	}
}
