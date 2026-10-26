package transport

import (
	"context"
	"errors"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"net"
	"sync"
	"sync/atomic"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/conn"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/peer"
)

// Client owns outbound peer connection state.
type Client struct {
	// traceRecorder and traceSequence bound opt-in endpoint observations.
	traceRecorder *rpctrace.Recorder
	traceSequence atomic.Uint64
	cfg           ClientConfig
	peers         *peer.Manager
	observer      *core.ObserverDrain
	// ownershipMu fences admission before Stop begins waiting on owned calls.
	ownershipMu sync.Mutex
	stopped     bool
	// active and retainedBytes include acquire/dial and pending-call responsibility.
	active        int
	retainedBytes int64
	operations    sync.WaitGroup
	stopOnce      sync.Once
	// stopDone includes late dial cleanup and terminal observer delivery.
	stopDone chan struct{}
}

// NewClient validates config and builds a minimal client shell.
func NewClient(cfg ClientConfig) (*Client, error) {
	normalized, err := normalizeClientConfig(cfg)
	if err != nil {
		return nil, err
	}
	var recorder *rpctrace.Recorder
	if normalized.RPCTraceEvery > 0 {
		recorder, err = rpctrace.NewRecorder(128)
		if err != nil {
			return nil, err
		}
	}
	observer := core.NewObserverDrain(normalized.Observer, goruntimeregistry.TaskTransportClientObserver)
	if observer != nil {
		normalized.Observer = observer
	}
	return &Client{
		cfg:           normalized,
		stopDone:      make(chan struct{}),
		traceRecorder: recorder,
		observer:      observer,
		peers: peer.NewManager(peer.Config{
			Discovery:      normalized.Discovery,
			PoolSize:       normalized.PoolSize,
			Limits:         normalized.Limits,
			Observer:       normalized.Observer,
			ReceiveTracing: normalized.RPCTraceEvery > 0,
			Dial:           clientDialFunc(normalized),
		}),
	}, nil
}

// Send copies payload and sends it as a data frame.
func (c *Client) Send(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload []byte) error {
	return c.SendOwned(ctx, nodeID, shardKey, pri, serviceID, CopyOwnedBuffer(payload))
}

// SendOwned sends payload as a data frame and transfers ownership on success.
func (c *Client) SendOwned(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload OwnedBuffer) error {
	retained := int64(payload.RetainedBytes())
	if !c.admitOperation(retained) {
		payload.Release()
		return ErrStopped
	}
	defer c.finishOperation(retained)

	peerConn, err := c.peers.Acquire(ctx, nodeID, shardKey)
	if err != nil {
		payload.Release()
		return err
	}
	return peerConn.Send(ctx, conn.Outbound{
		Kind:      FrameKindData,
		Priority:  pri,
		ServiceID: serviceID,
		Payload:   payload,
	})
}

// Notify copies payload and sends it as a notify frame.
func (c *Client) Notify(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload []byte) error {
	return c.NotifyOwned(ctx, nodeID, shardKey, pri, serviceID, CopyOwnedBuffer(payload))
}

// NotifyOwned sends payload as a notify frame and transfers ownership on success.
func (c *Client) NotifyOwned(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload OwnedBuffer) error {
	retained := int64(payload.RetainedBytes())
	if !c.admitOperation(retained) {
		payload.Release()
		return ErrStopped
	}
	defer c.finishOperation(retained)

	peerConn, err := c.peers.Acquire(ctx, nodeID, shardKey)
	if err != nil {
		payload.Release()
		return err
	}
	return peerConn.Send(ctx, conn.Outbound{
		Kind:      FrameKindNotify,
		Priority:  pri,
		ServiceID: serviceID,
		Payload:   payload,
	})
}

// Call copies payload, sends it as an RPC request, and waits for the response.
func (c *Client) Call(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload []byte) ([]byte, error) {
	return c.CallOwned(ctx, nodeID, shardKey, pri, serviceID, CopyOwnedBuffer(payload))
}

// CallOwned sends payload as an RPC request, transfers ownership on successful
// admission, and waits for the response.
func (c *Client) CallOwned(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload OwnedBuffer) (_ []byte, err error) {
	retained := int64(payload.RetainedBytes())
	if !c.admitOperation(retained) {
		payload.Release()
		return nil, ErrStopped
	}
	// Registered first so request observers finish before ownership is returned.
	defer c.finishOperation(retained)

	if c.cfg.Observer != nil {
		started := time.Now()
		defer func() {
			result := "ok"
			if err != nil {
				result = "err"
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrTimeout) {
				result = "timeout"
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, ErrCanceled) {
				result = "canceled"
			}
			c.cfg.Observer.ObserveTransport(Event{Name: "client_rpc", NodeID: nodeID, ServiceID: serviceID, Result: result, Duration: time.Since(started)})
		}()
	}
	var trace *rpctrace.Trace
	capture := rpctrace.TerminalCaptureFrom(ctx)
	if c.traceRecorder == nil {
		capture.RecorderDisabled()
	}
	if c.traceRecorder != nil && (capture != nil || rpctrace.From(ctx) != nil || (c.traceSequence.Add(1)-1)%uint64(c.cfg.RPCTraceEvery) == 0) {
		trace = c.traceRecorder.Start(ctx, rpctrace.SideClient, serviceID)
		ctx = rpctrace.With(ctx, trace)
		defer func() { core.FinishRPCTrace(c.cfg.Observer, nodeID, trace, core.RPCTraceResult(err)) }()
	}
	acquire := trace.Begin(rpctrace.StagePeerAcquire)
	peerConn, err := c.peers.Acquire(ctx, nodeID, shardKey)
	acquire.End(core.RPCTraceResult(err))
	if err != nil {
		payload.Release()
		return nil, err
	}
	capability := trace.Begin(rpctrace.StageCapability)
	if c.cfg.RequestBudgets {
		if err := peerConn.NegotiateRequestBudgets(ctx); err != nil {
			payload.Release()
			capability.End(core.RPCTraceResult(err))
			return nil, err
		}
	}
	if trace != nil {
		if err := peerConn.NegotiateRPCTrace(ctx); err != nil {
			payload.Release()
			capability.End(core.RPCTraceResult(err))
			return nil, err
		}
	}
	capability.End(rpctrace.ResultOK)
	return peerConn.Call(ctx, conn.Outbound{
		Priority:  pri,
		ServiceID: serviceID,
		Payload:   payload,
	})
}

// ClosePeer closes all outbound connection slots for nodeID.
func (c *Client) ClosePeer(nodeID NodeID) {
	if !c.admitOperation(0) {
		return
	}
	defer c.finishOperation(0)
	c.peers.ClosePeer(nodeID)
}

// Stop begins shutdown with a bounded compatibility grace. Dependency owners
// must use StopContext to distinguish complete cleanup from retained work.
func (c *Client) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = c.StopContext(ctx)
}

// StopContext joins connection shutdown, actual request/dial completion, and
// observations. A canceled waiter cannot abandon or duplicate that ownership.
func (c *Client) StopContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.stopOnce.Do(func() {
		c.ownershipMu.Lock()
		c.stopped = true
		c.ownershipMu.Unlock()
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskTransportClientShutdown, func() {
			c.peers.Stop()
			c.operations.Wait()
			c.observer.Stop()
			close(c.stopDone)
		})
	})
	select {
	case <-c.stopDone:
		return nil
	default:
	}
	select {
	case <-c.stopDone:
		return nil
	case <-ctx.Done():
		select {
		case <-c.stopDone:
			return nil
		default:
		}
		c.ownershipMu.Lock()
		incomplete := &StopIncompleteError{cause: ctx.Err(), requests: c.active, retainedBytes: c.retainedBytes}
		c.ownershipMu.Unlock()
		return incomplete
	}
}

// admitOperation joins ownership before a concurrent Stop may wait. The caller
// retains its payload through acquire/dial and transfers it under existing rules.
func (c *Client) admitOperation(bytes int64) bool {
	c.ownershipMu.Lock()
	defer c.ownershipMu.Unlock()
	if c.stopped {
		return false
	}
	c.active++
	c.retainedBytes += bytes
	c.operations.Add(1)
	return true
}

func (c *Client) finishOperation(bytes int64) {
	c.ownershipMu.Lock()
	c.active--
	c.retainedBytes -= bytes
	c.ownershipMu.Unlock()
	c.operations.Done()
}

// Stats returns a point-in-time client stats snapshot.
func (c *Client) Stats() Stats {
	return c.peers.Stats()
}

func clientDialFunc(cfg ClientConfig) func(context.Context, string, string) (net.Conn, error) {
	if cfg.Dialer != nil {
		return func(_ context.Context, network, addr string) (net.Conn, error) {
			return cfg.Dialer(network, addr, cfg.DialTimeout)
		}
	}
	var dialer net.Dialer
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialCtx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
		defer cancel()
		return dialer.DialContext(dialCtx, network, addr)
	}
}
