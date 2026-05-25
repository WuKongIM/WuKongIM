package clusternet

import "context"

// Handler handles one typed clusterv2 RPC payload.
type Handler interface {
	// HandleRPC handles payload and returns a response payload.
	HandleRPC(context.Context, []byte) ([]byte, error)
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(context.Context, []byte) ([]byte, error)

// HandleRPC calls f(ctx, payload).
func (f HandlerFunc) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	return f(ctx, payload)
}
