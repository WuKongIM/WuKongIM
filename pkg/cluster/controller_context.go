package cluster

import "context"

type controllerRPCLogPolicy uint8

const (
	controllerRPCLogDefault controllerRPCLogPolicy = iota
	controllerRPCLogBestEffortRead
)

type controllerRPCLogPolicyKey struct{}

// WithBestEffortControllerRead marks controller read RPCs as non-critical
// observability probes so transient failures do not look like command failures.
func WithBestEffortControllerRead(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, controllerRPCLogPolicyKey{}, controllerRPCLogBestEffortRead)
}

func controllerRPCLogPolicyFromContext(ctx context.Context) controllerRPCLogPolicy {
	if ctx == nil {
		return controllerRPCLogDefault
	}
	policy, ok := ctx.Value(controllerRPCLogPolicyKey{}).(controllerRPCLogPolicy)
	if !ok {
		return controllerRPCLogDefault
	}
	return policy
}
