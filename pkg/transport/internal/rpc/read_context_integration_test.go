//go:build integration

package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
)

// TestReadHandlerCancellationSources preserves caller and service cancellation
// with and without an execution deadline, without canceling the caller on Stop.
func TestReadHandlerCancellationSources(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "without_timeout"
		if deadline {
			name = "with_timeout"
		}
		t.Run(name, func(t *testing.T) {
			for _, source := range []string{"caller", "service"} {
				t.Run(source, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					entered := make(chan struct{})
					finished := make(chan struct{})
					observed := make(chan error, 1)
					opts := core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 64, CancelRunning: true}
					if deadline {
						opts.Timeout = time.Hour
					}
					svc := NewService(1, func(hctx context.Context, _ []byte) ([]byte, error) {
						_, hasDeadline := hctx.Deadline()
						if hasDeadline != deadline {
							observed <- core.ErrInvalidConfig
							close(entered)
							return nil, nil
						}
						close(entered)
						<-hctx.Done()
						observed <- hctx.Err()
						return nil, hctx.Err()
					}, opts, nil)
					defer svc.Stop()
					if err := svc.Enqueue(Request{Context: ctx, Payload: core.NewOwnedBuffer([]byte("work"), nil), Finish: func() { close(finished) }}); err != nil {
						t.Fatal(err)
					}
					waitClosed(t, entered)
					if source == "caller" {
						cancel()
					} else {
						svc.Stop()
					}
					waitClosed(t, finished)
					if err := <-observed; err != context.Canceled {
						t.Fatalf("handler error=%v, want canceled", err)
					}
					if source == "service" && ctx.Err() != nil {
						t.Fatal("service stop canceled caller context")
					}
				})
			}
		})
	}
}
