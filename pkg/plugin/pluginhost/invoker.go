package pluginhost

import (
	"context"
	"time"
)

const stopPath = "/stop"

// RPCClient is the byte-oriented transport used by the runtime invoker.
type RPCClient interface {
	Request(ctx context.Context, uid, path string, body []byte) ([]byte, error)
	Send(uid string, msgType uint32, body []byte) error
}

// Invoker sends byte-oriented requests and messages to plugin runtimes.
type Invoker struct {
	client  RPCClient
	timeout time.Duration
}

// InvokerOption configures an Invoker.
type InvokerOption func(*Invoker)

// WithTimeout applies a runtime timeout when caller contexts have no shorter deadline.
func WithTimeout(timeout time.Duration) InvokerOption {
	return func(invoker *Invoker) {
		invoker.timeout = timeout
	}
}

// NewInvoker creates a byte-oriented plugin invoker.
func NewInvoker(client RPCClient, opts ...InvokerOption) *Invoker {
	invoker := &Invoker{client: client}
	for _, opt := range opts {
		opt(invoker)
	}
	return invoker
}

// RequestPlugin issues a byte-oriented request to a plugin path.
func (i *Invoker) RequestPlugin(ctx context.Context, no, path string, body []byte) ([]byte, error) {
	ctx, cancel := i.contextWithTimeout(ctx)
	defer cancel()

	return i.client.Request(ctx, no, path, body)
}

// SendPlugin sends a byte-oriented one-way message to a plugin.
func (i *Invoker) SendPlugin(no string, msgType uint32, body []byte) error {
	return i.client.Send(no, msgType, body)
}

// Stop asks a plugin to stop through the runtime stop path.
func (i *Invoker) Stop(ctx context.Context, no string) error {
	_, err := i.RequestPlugin(ctx, no, stopPath, nil)
	return err
}

func (i *Invoker) contextWithTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if i.timeout <= 0 {
		return ctx, func() {}
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= i.timeout {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, i.timeout)
}
