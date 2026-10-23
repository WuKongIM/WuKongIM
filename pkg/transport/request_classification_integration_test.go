//go:build integration

package transport_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// A mixed service retains one registry/admission owner while trusted request
// classification selects whether already executing work follows its caller.
func TestMixedServiceRequestCancellationOverTCP(t *testing.T) {
	for _, reason := range []string{"deadline", "cancel", "disconnect"} {
		t.Run(reason, func(t *testing.T) {
			entered := []chan struct{}{make(chan struct{}), make(chan struct{})}
			release := make(chan struct{}, 1)
			observed := make(chan error, 2)
			var calls, classified atomic.Int32
			opts := budgetOpts()
			opts.Timeout = 2 * time.Second
			opts.CancelRunningFor = func(p []byte) bool {
				classified.Add(1)
				return len(p) == 1 && p[0] == 0
			}
			client, _ := budgetPair(t, opts, func(ctx context.Context, p []byte) ([]byte, error) {
				calls.Add(1)
				close(entered[p[0]])
				if p[0] == 0 {
					<-ctx.Done()
				} else {
					select {
					case <-release:
					case <-ctx.Done():
					}
				}
				observed <- ctx.Err()
				return []byte("completed"), nil
			}, nil)
			defer close(release)
			for _, operation := range []byte{0, 1} {
				ctx, cancel := context.WithCancel(context.Background())
				if reason == "deadline" {
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
				}
				caller := make(chan error, 1)
				go func() { caller <- budgetCall(ctx, client, []byte{operation}) }()
				budgetWait(t, entered[operation])
				if reason == "cancel" {
					cancel()
				} else if reason == "disconnect" {
					client.ClosePeer(2)
				}
				select {
				case err := <-caller:
					if err == nil {
						t.Fatal("abandoned caller unexpectedly received success")
					}
				case <-time.After(time.Second):
					cancel()
					t.Fatal("caller did not return")
				}
				cancel()
				if operation == 1 {
					// The failed caller cannot infer a mutation outcome. Let the
					// original invocation complete without replaying it.
					release <- struct{}{}
				}
				select {
				case err := <-observed:
					if operation == 0 && err == nil {
						t.Fatal("read did not inherit request cancellation")
					}
					if operation == 1 && err != nil {
						t.Fatalf("running mutation was canceled by caller: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("handler did not finish")
				}
			}
			if calls.Load() != 2 || classified.Load() != 2 {
				t.Fatalf("handler/classifier calls=%d/%d, want once per request", calls.Load(), classified.Load())
			}
		})
	}
}

func TestRequestClassifierRejectsConflictingRegistration(t *testing.T) {
	server, err := transport.NewServer(transport.ServerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	opts := budgetOpts()
	opts.CancelRunning = true
	opts.CancelRunningFor = func([]byte) bool { return true }
	handler := func(context.Context, []byte) ([]byte, error) { return nil, nil }
	if err := server.Handle(1, handler, opts); !errors.Is(err, transport.ErrInvalidConfig) {
		t.Fatalf("conflicting policy accepted: %v", err)
	}
	opts.CancelRunning = false
	if err := server.Handle(1, handler, opts); err != nil {
		t.Fatalf("rejected registration consumed registry capacity: %v", err)
	}
}

func TestRequestClassifierUnknownAndPanicDoNotBecomeReadsOverTCP(t *testing.T) {
	var calls, classified atomic.Int32
	opts := budgetOpts()
	opts.CancelRunningFor = func(p []byte) bool {
		classified.Add(1)
		if len(p) == 1 && p[0] == 2 {
			panic("classifier failed")
		}
		return len(p) == 1 && p[0] == 0
	}
	client, _ := budgetPair(t, opts, func(ctx context.Context, p []byte) ([]byte, error) {
		calls.Add(1)
		if len(p) != 1 {
			if _, ok := ctx.Deadline(); !ok {
				return nil, errors.New("service timeout missing")
			}
			return nil, transport.ErrInvalidFrame
		}
		return p, nil
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, p := range [][]byte{nil, {2}} {
		err := budgetCall(ctx, client, p)
		var remote transport.RemoteError
		if !errors.As(err, &remote) {
			t.Fatalf("invalid payload/classifier panic did not return a remote error: %v", err)
		}
	}
	if err := budgetCall(ctx, client, []byte{0}); err != nil {
		t.Fatalf("classifier failure leaked the service owner: %v", err)
	}
	if calls.Load() != 2 || classified.Load() != 3 {
		t.Fatalf("handler/classifier calls=%d/%d", calls.Load(), classified.Load())
	}
}

func TestRequestClassifiedReadStopsWithServerOverTCP(t *testing.T) {
	entered, finished := make(chan struct{}), make(chan struct{})
	opts := budgetOpts()
	opts.Timeout = 0
	opts.CancelRunningFor = func([]byte) bool { return true }
	client, server := budgetPair(t, opts, func(ctx context.Context, _ []byte) ([]byte, error) {
		close(entered)
		<-ctx.Done()
		close(finished)
		return nil, ctx.Err()
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	caller := make(chan error, 1)
	go func() { caller <- budgetCall(ctx, client, nil) }()
	budgetWait(t, entered)
	server.Stop()
	budgetWait(t, finished)
	if ctx.Err() != nil {
		t.Fatal("server shutdown canceled caller's context")
	}
	select {
	case err := <-caller:
		if err == nil {
			t.Fatal("stopped read succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("caller not joined after server stop")
	}
}
