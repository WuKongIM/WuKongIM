//go:build integration

package transport_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/WuKongIM/WuKongIM/pkg/transport/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

type budgetObserver struct{ fn func(transport.Event) }

func (o budgetObserver) ObserveTransport(e transport.Event) { o.fn(e) }

func budgetPair(t *testing.T, opts transport.ServiceOptions, handler transport.Handler, observer transport.Observer) (*transport.Client, *transport.Server) {
	t.Helper()
	server, err := transport.NewServer(transport.ServerConfig{Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Handle(1, handler, opts); err != nil {
		t.Fatal(err)
	}
	if err = server.ListenAndServe("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	client, err := transport.NewClient(transport.ClientConfig{RequestBudgets: true, PoolSize: 1, Discovery: testkit.StaticDiscovery{2: server.Addr()}})
	if err != nil {
		server.Stop()
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Stop(); server.Stop() })
	return client, server
}

func TestNegotiatedHandlerPanicReturnsErrorBeforeRequestCleanup(t *testing.T) {
	client, _ := budgetPair(t, budgetOpts(), func(context.Context, []byte) ([]byte, error) {
		panic("handler failure")
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := budgetCall(ctx, client, []byte("request"))
	var remote transport.RemoteError
	if !errors.As(err, &remote) {
		t.Fatalf("want remote panic response, got %v", err)
	}
}

func TestInvalidCapabilityResponseNeverSendsBusinessRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	served := make(chan error, 1)
	go func() {
		peer, err := listener.Accept()
		if err != nil {
			served <- err
			return
		}
		defer peer.Close()
		_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
		probe, err := wire.ReadFrame(peer, 4096)
		if err != nil {
			served <- err
			return
		}
		probe.Body.Release()
		if probe.Header.ServiceID != wire.CapabilityServiceID {
			served <- errors.New("business request preceded negotiation")
			return
		}
		err = wire.WriteFrame(peer, wire.Frame{Header: wire.Header{Kind: transport.FrameKindRPCResponse, Priority: transport.PriorityControl, ServiceID: probe.Header.ServiceID, RequestID: probe.Header.RequestID}, Body: transport.NewOwnedBuffer(append([]byte{wire.ResponseOK}, []byte("invalid capability")...), nil)}, 4096)
		if err != nil {
			served <- err
			return
		}
		frame, err := wire.ReadFrame(peer, 4096)
		frame.Body.Release()
		if !errors.Is(err, io.EOF) {
			served <- errors.New("unexpected traffic after invalid negotiation")
			return
		}
		served <- nil
	}()
	client, err := transport.NewClient(transport.ClientConfig{RequestBudgets: true, PoolSize: 1, Discovery: testkit.StaticDiscovery{2: listener.Addr().String()}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := budgetCall(ctx, client, []byte("request")); !errors.Is(err, transport.ErrInvalidFrame) {
		t.Fatalf("negotiation=%v", err)
	}
	client.Stop()
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

func budgetOpts() transport.ServiceOptions {
	return transport.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 4096, QueueTimeout: time.Second, Timeout: time.Second}
}
func budgetCall(ctx context.Context, client *transport.Client, payload []byte) error {
	_, err := client.Call(ctx, 2, 0, transport.PriorityRPC, 1, payload)
	return err
}
func budgetWait(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("request lifecycle did not progress")
	}
}

func TestServerBudgetExpiryPreservesTypedTimeout(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "execution", true: "queue"}[queued], func(t *testing.T) {
			opts := budgetOpts()
			entered, release := make(chan struct{}), make(chan struct{})
			if queued {
				opts.QueueTimeout = 20 * time.Millisecond
			} else {
				opts.Timeout = 20 * time.Millisecond
			}
			client, _ := budgetPair(t, opts, func(ctx context.Context, _ []byte) ([]byte, error) {
				if queued {
					close(entered)
					<-release
				} else {
					<-ctx.Done()
				}
				return nil, ctx.Err()
			}, nil)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if queued {
				first := make(chan error, 1)
				go func() { first <- budgetCall(ctx, client, nil) }()
				defer func() { close(release); <-first }()
				budgetWait(t, entered)
			}
			err := budgetCall(ctx, client, nil)
			if !errors.Is(err, transport.ErrTimeout) {
				t.Fatalf("server budget lost typed timeout: %v", err)
			}
			var remote transport.RemoteError
			if !errors.As(err, &remote) {
				t.Fatalf("missing remote origin: %v", err)
			}
		})
	}
}

func TestRemoteTransportErrorsAreTypedWithoutGuessingText(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cause, want error
	}{
		{"timeout", context.DeadlineExceeded, transport.ErrTimeout},
		{"canceled", context.Canceled, transport.ErrCanceled},
		{"busy", transport.ErrBusy, transport.ErrBusy},
		{"stopped", transport.ErrStopped, transport.ErrStopped},
		{"generic", errors.New("transport: timeout"), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := budgetPair(t, budgetOpts(), func(context.Context, []byte) ([]byte, error) { return nil, tc.cause }, nil)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := budgetCall(ctx, client, nil)
			var remote transport.RemoteError
			if !errors.As(err, &remote) {
				t.Fatalf("remote error origin lost: %v", err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("%v does not retain %v", err, tc.want)
			}
			if tc.want == nil && (remote.Code != transport.RemoteErrorCodeGeneric || errors.Is(err, transport.ErrTimeout)) {
				t.Fatalf("classified arbitrary error text: %v", err)
			}
		})
	}
}

func TestRetainedMemoryRejectionPreservesTypedBusy(t *testing.T) {
	opts := budgetOpts()
	opts.MaxQueueBytes = 1
	client, _ := budgetPair(t, opts, func(context.Context, []byte) ([]byte, error) { t.Error("rejected request executed"); return nil, nil }, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := budgetCall(ctx, client, []byte{1}); !errors.Is(err, transport.ErrBusy) {
		t.Fatalf("admission rejection lost identity: %v", err)
	}
}

func TestNegotiatedCancelRemovesQueuedRequestAndReleasesCapacity(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	queued := make(chan struct{})
	expired := make(chan struct{})
	var onceQueued, onceExpired sync.Once
	var executed atomic.Int32
	client, _ := budgetPair(t, budgetOpts(), func(ctx context.Context, p []byte) ([]byte, error) {
		if len(p) == 1 {
			close(started)
			select {
			case <-unblock:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if len(p) == 2 {
			executed.Add(1)
		}
		return p, nil
	}, budgetObserver{func(e transport.Event) {
		if e.Name == "service_admission" && e.Result == "ok" && e.Bytes == 2 {
			onceQueued.Do(func() { close(queued) })
		}
		if e.Name == "service_queue" && e.Result == "expired" {
			onceExpired.Do(func() { close(expired) })
		}
	}})
	var onceRelease sync.Once
	release := func() { onceRelease.Do(func() { close(unblock) }) }
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	first := make(chan error, 1)
	go func() { first <- budgetCall(ctx, client, []byte{1}) }()
	budgetWait(t, started)
	requestCtx, requestCancel := context.WithCancel(ctx)
	second := make(chan error, 1)
	go func() { second <- budgetCall(requestCtx, client, []byte{2, 2}) }()
	budgetWait(t, queued)
	requestCancel()
	if err := <-second; !errors.Is(err, transport.ErrCanceled) {
		t.Fatalf("cancel=%v", err)
	}
	budgetWait(t, expired)
	third := make(chan error, 1)
	go func() { third <- budgetCall(ctx, client, []byte{3, 3, 3}) }()
	release()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-third; err != nil {
		t.Fatalf("expired FIFO slot was not reusable: %v", err)
	}
	if executed.Load() != 0 {
		t.Fatal("canceled queued request executed")
	}
}

func TestNegotiatedDeadlineStopsReadButRunningMutationFinishes(t *testing.T) {
	for _, read := range []bool{false, true} {
		t.Run(map[bool]string{false: "mutation", true: "read"}[read], func(t *testing.T) {
			entered := make(chan struct{})
			inspect := make(chan struct{})
			finished := make(chan error, 1)
			opts := budgetOpts()
			opts.CancelRunning = read
			client, _ := budgetPair(t, opts, func(ctx context.Context, _ []byte) ([]byte, error) {
				close(entered)
				if read {
					<-ctx.Done()
				} else {
					<-inspect
				}
				finished <- ctx.Err()
				return []byte("committed"), nil
			}, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			caller := make(chan error, 1)
			go func() { caller <- budgetCall(ctx, client, []byte("request")) }()
			budgetWait(t, entered)
			if err := <-caller; err == nil {
				t.Fatal("caller deadline was ignored")
			}
			close(inspect)
			select {
			case err := <-finished:
				if read && err == nil {
					t.Fatal("read ignored propagated deadline")
				}
				if !read && err != nil {
					t.Fatalf("running mutation inherited caller cancellation: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("handler did not finish")
			}
		})
	}
}

func TestNegotiatedReadCanceledOnDisconnect(t *testing.T) {
	entered := make(chan struct{})
	finished := make(chan struct{})
	opts := budgetOpts()
	opts.CancelRunning = true
	client, _ := budgetPair(t, opts, func(ctx context.Context, _ []byte) ([]byte, error) {
		close(entered)
		<-ctx.Done()
		close(finished)
		return nil, ctx.Err()
	}, nil)
	done := make(chan error, 1)
	go func() { done <- budgetCall(context.Background(), client, []byte("request")) }()
	budgetWait(t, entered)
	client.ClosePeer(2)
	budgetWait(t, finished)
	if err := <-done; err == nil {
		t.Fatal("disconnected call succeeded")
	}
}

func TestBudgetNegotiationFallsBackOnlyOnExplicitLegacyRejection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	served := make(chan error, 1)
	go func() {
		peer, err := listener.Accept()
		if err != nil {
			served <- err
			return
		}
		defer peer.Close()
		_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
		for i := 0; i < 3; i++ {
			frame, err := wire.ReadFrame(peer, 4096)
			if err != nil {
				served <- err
				return
			}
			if frame.Header.Kind != transport.FrameKindRPCRequest {
				frame.Body.Release()
				served <- errors.New("legacy peer received a new frame kind")
				return
			}
			status := wire.ResponseOK
			payload := []byte("ok")
			if i == 0 {
				if frame.Header.ServiceID != wire.CapabilityServiceID {
					served <- errors.New("missing capability probe")
					frame.Body.Release()
					return
				}
				status = wire.ResponseServiceNotFound
				payload = []byte("unsupported")
			} else if frame.Header.ServiceID != 1 {
				served <- errors.New("capability was reprobed")
				frame.Body.Release()
				return
			}
			frame.Body.Release()
			err = wire.WriteFrame(peer, wire.Frame{Header: wire.Header{Kind: transport.FrameKindRPCResponse, Priority: transport.PriorityRPC, ServiceID: frame.Header.ServiceID, RequestID: frame.Header.RequestID}, Body: transport.NewOwnedBuffer(append([]byte{status}, payload...), nil)}, 4096)
			if err != nil {
				served <- err
				return
			}
		}
		served <- nil
	}()
	client, err := transport.NewClient(transport.ClientConfig{RequestBudgets: true, PoolSize: 1, Discovery: testkit.StaticDiscovery{2: listener.Addr().String()}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		if err := budgetCall(ctx, client, []byte("request")); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}
