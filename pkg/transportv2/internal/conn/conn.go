// Package conn owns one transportv2 peer connection's read, write, and RPC response loops.
package conn

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/sched"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/wire"
)

// Config configures a single connection actor.
type Config struct {
	// Limits bounds frame size, write queue depth, and write batch size.
	Limits core.Limits
}

// Outbound is one caller-owned frame queued for connection writes.
type Outbound struct {
	// Kind identifies the outbound frame behavior; zero defaults to FrameKindData.
	Kind core.FrameKind
	// Priority selects the scheduler lane.
	Priority core.Priority
	// ServiceID identifies the target service.
	ServiceID uint16
	// RequestID correlates RPC requests and responses.
	RequestID uint64
	// Payload is transferred to the connection on successful Send.
	Payload core.OwnedBuffer
}

// Inbound is one received frame transferred to Dispatch ownership.
type Inbound struct {
	// Kind identifies the inbound frame behavior.
	Kind core.FrameKind
	// Priority is the sender's priority lane.
	Priority core.Priority
	// ServiceID identifies the target service.
	ServiceID uint16
	// RequestID correlates RPC requests and responses.
	RequestID uint64
	// Payload is owned by Dispatch and must be released by it.
	Payload core.OwnedBuffer
	// Conn is the actor that received the frame.
	Conn *Conn
}

// Dispatch receives non-RPC-response inbound frames.
type Dispatch interface {
	Dispatch(context.Context, Inbound)
}

// DispatchFunc adapts a function into Dispatch.
type DispatchFunc func(context.Context, Inbound)

// Dispatch calls f(ctx, inbound).
func (f DispatchFunc) Dispatch(ctx context.Context, inbound Inbound) {
	f(ctx, inbound)
}

// Conn is a connection actor with one reader, one writer, and an RPC pending table.
type Conn struct {
	raw       net.Conn
	cfg       Config
	dispatch  Dispatch
	scheduler *sched.Scheduler
	pending   *rpc.PendingTable

	nextRequestID atomic.Uint64

	ctx    context.Context
	cancel context.CancelFunc

	startOnce sync.Once
	started   atomic.Bool
	closeOnce sync.Once

	readDone  chan struct{}
	writeDone chan struct{}
}

// New creates a connection actor over raw.
func New(raw net.Conn, cfg Config, dispatch Dispatch) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	return &Conn{
		raw:      raw,
		cfg:      cfg,
		dispatch: dispatch,
		scheduler: sched.New(sched.Config{
			MaxItems:       cfg.Limits.MaxQueuedItemsPerConn,
			MaxBytes:       cfg.Limits.MaxQueuedBytesPerConn,
			MaxBatchFrames: cfg.Limits.MaxBatchFrames,
			MaxBatchBytes:  cfg.Limits.MaxBatchBytes,
		}),
		pending:   rpc.NewPendingTable(16),
		ctx:       ctx,
		cancel:    cancel,
		readDone:  make(chan struct{}),
		writeDone: make(chan struct{}),
	}
}

// Start launches the single read and write loops. Repeated calls are ignored.
func (c *Conn) Start() {
	c.startOnce.Do(func() {
		c.started.Store(true)
		go c.readLoop()
		go c.writeLoop()
	})
}

// Send queues an outbound frame and transfers payload ownership on success.
func (c *Conn) Send(ctx context.Context, outbound Outbound) error {
	if outbound.Kind == 0 {
		outbound.Kind = core.FrameKindData
	}
	if outbound.Payload.Len() > c.cfg.Limits.MaxFrameBodyBytes {
		outbound.Payload.Release()
		return core.ErrMsgTooLarge
	}

	// Queue cost is body bytes only. Header bytes are fixed wire overhead; adding them here
	// would make a MaxFrameBodyBytes-sized frame exceed a valid MaxBatchBytes setting.
	err := c.scheduler.Enqueue(ctx, sched.Item{
		Priority: outbound.Priority,
		Bytes:    outbound.Payload.Len(),
		Value:    outbound,
	})
	if err != nil {
		outbound.Payload.Release()
		return err
	}
	return nil
}

// Call sends an RPC request and waits for its response or terminal cancellation.
func (c *Conn) Call(ctx context.Context, outbound Outbound) ([]byte, error) {
	requestID := c.nextRequestID.Add(1)
	outbound.Kind = core.FrameKindRPCRequest
	outbound.RequestID = requestID

	respCh := make(chan rpc.Response, 1)
	c.pending.Store(requestID, respCh)
	if err := c.Send(ctx, outbound); err != nil {
		c.pending.Delete(requestID)
		return nil, err
	}

	select {
	case resp := <-respCh:
		return resp.Payload, resp.Err
	case <-ctx.Done():
		c.pending.Delete(requestID)
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, core.ErrCanceled
		}
		return nil, ctx.Err()
	case <-c.ctx.Done():
		select {
		case resp := <-respCh:
			return resp.Payload, resp.Err
		default:
			return nil, core.ErrStopped
		}
	}
}

// Close terminates the connection actor and releases all connection-owned resources.
func (c *Conn) Close(err error) {
	c.shutdown(err)
	if c.started.Load() {
		<-c.readDone
		<-c.writeDone
	}
}

func (c *Conn) readLoop() {
	defer close(c.readDone)
	for {
		frame, err := wire.ReadFrame(c.raw, c.cfg.Limits.MaxFrameBodyBytes)
		if err != nil {
			c.shutdown(err)
			return
		}
		if frame.Header.Kind == core.FrameKindRPCResponse {
			c.handleRPCResponse(frame)
			continue
		}
		if c.dispatch == nil {
			frame.Body.Release()
			continue
		}
		c.dispatch.Dispatch(c.ctx, Inbound{
			Kind:      frame.Header.Kind,
			Priority:  frame.Header.Priority,
			ServiceID: frame.Header.ServiceID,
			RequestID: frame.Header.RequestID,
			Payload:   frame.Body,
			Conn:      c,
		})
	}
}

func (c *Conn) writeLoop() {
	defer close(c.writeDone)
	for {
		batch, err := c.scheduler.WaitBatch()
		if err != nil {
			return
		}
		for i, item := range batch {
			outbound, ok := item.Value.(Outbound)
			if !ok {
				continue
			}
			writeErr := wire.WriteFrame(c.raw, outbound.toFrame(), c.cfg.Limits.MaxFrameBodyBytes)
			outbound.Payload.Release()
			if writeErr != nil {
				releaseSchedItems(batch[i+1:])
				c.shutdown(writeErr)
				return
			}
		}
	}
}

func (c *Conn) shutdown(err error) {
	if err == nil {
		err = core.ErrStopped
	}
	c.closeOnce.Do(func() {
		c.cancel()
		_ = c.raw.Close()
		drained := c.scheduler.Stop(err)
		releaseSchedItems(drained)
		c.pending.FailAll(err)
	})
}

func (c *Conn) handleRPCResponse(frame wire.Frame) {
	defer frame.Body.Release()

	body := frame.Body.Bytes()
	if len(body) == 0 {
		c.pending.Complete(frame.Header.RequestID, rpc.Response{})
		return
	}

	status := body[0]
	payload := append([]byte(nil), body[1:]...)
	if status != wire.ResponseOK {
		c.pending.Complete(frame.Header.RequestID, rpc.Response{
			Err: core.RemoteError{Code: "remote_error", Message: string(payload)},
		})
		return
	}
	c.pending.Complete(frame.Header.RequestID, rpc.Response{Payload: payload})
}

func (o Outbound) toFrame() wire.Frame {
	return wire.Frame{
		Header: wire.Header{
			Kind:      o.Kind,
			Priority:  o.Priority,
			ServiceID: o.ServiceID,
			RequestID: o.RequestID,
		},
		Body: o.Payload,
	}
}

func releaseSchedItems(items []sched.Item) {
	for _, item := range items {
		outbound, ok := item.Value.(Outbound)
		if !ok {
			continue
		}
		outbound.Payload.Release()
	}
}

// EncodeRPCResponse returns an owned RPC response body containing status then payload.
func EncodeRPCResponse(status uint8, payload []byte) core.OwnedBuffer {
	body := make([]byte, 1+len(payload))
	body[0] = status
	copy(body[1:], payload)
	return core.NewOwnedBuffer(body, nil)
}
