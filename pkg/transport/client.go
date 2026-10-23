package transport

import (
	"context"
	"errors"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"net"
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
		traceRecorder: recorder,
		observer:      observer,
		peers: peer.NewManager(peer.Config{
			Discovery: normalized.Discovery,
			PoolSize:  normalized.PoolSize,
			Limits:    normalized.Limits,
			Observer:  normalized.Observer,
			Dial:      clientDialFunc(normalized),
		}),
	}, nil
}

// Send copies payload and sends it as a data frame.
func (c *Client) Send(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload []byte) error {
	return c.SendOwned(ctx, nodeID, shardKey, pri, serviceID, CopyOwnedBuffer(payload))
}

// SendOwned sends payload as a data frame and transfers ownership on success.
func (c *Client) SendOwned(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload OwnedBuffer) error {
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
	if c.traceRecorder != nil && (rpctrace.From(ctx) != nil || (c.traceSequence.Add(1)-1)%uint64(c.cfg.RPCTraceEvery) == 0) {
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
	c.peers.ClosePeer(nodeID)
}

// Stop releases client resources.
func (c *Client) Stop() {
	c.peers.Stop()
	c.observer.Stop()
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
