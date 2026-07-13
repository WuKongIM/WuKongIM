// Package conn owns one transport peer connection's read, write, and RPC response loops.
package conn

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/buffer"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/sched"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

var writeFramesInto = wire.WriteFramesInto

// Config configures a single connection actor.
type Config struct {
	// Limits bounds frame size, write queue depth, and write batch size.
	Limits core.Limits
	// Observer receives connection pressure events; nil disables observation callbacks.
	Observer core.Observer
	// NodeID identifies the node associated with this connection's pressure events.
	NodeID core.NodeID
	// SourceID identifies this connection for aggregate metrics; zero assigns a process-local ID.
	SourceID uint64
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
	// writeCtx cancels queued RPC writes before they leave the connection.
	writeCtx context.Context
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
//
// Dispatch must return promptly. It owns and must release inbound payloads.
// Blocking dispatch applies read-loop backpressure and can delay Close because
// the connection invokes Dispatch synchronously from the read loop.
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

var nextSourceID atomic.Uint64

// New creates a connection actor over raw.
func New(raw net.Conn, cfg Config, dispatch Dispatch) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	if cfg.SourceID == 0 {
		cfg.SourceID = nextSourceID.Add(1)
	}
	return &Conn{
		raw:      raw,
		cfg:      cfg,
		dispatch: dispatch,
		scheduler: sched.New(sched.Config{
			MaxItems:       cfg.Limits.MaxQueuedItemsPerConn,
			MaxBytes:       cfg.Limits.MaxQueuedBytesPerConn,
			MaxBatchFrames: cfg.Limits.MaxBatchFrames,
			MaxBatchBytes:  cfg.Limits.MaxBatchBytes,
			Observer:       cfg.Observer,
			SourceID:       cfg.SourceID,
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

// Done returns a channel closed when the connection actor begins shutdown.
func (c *Conn) Done() <-chan struct{} {
	return c.ctx.Done()
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
	outbound.writeCtx = ctx

	respCh := make(chan rpc.Response, 1)
	c.pending.Store(requestID, respCh)
	c.observePendingRPC("ok")
	if err := c.Send(ctx, outbound); err != nil {
		c.pending.Delete(requestID)
		c.observePendingRPC("ok")
		if errors.Is(err, context.Canceled) {
			return nil, core.ErrCanceled
		}
		return nil, err
	}

	select {
	case resp := <-respCh:
		return resp.Payload, resp.Err
	case <-ctx.Done():
		c.pending.Delete(requestID)
		c.observePendingRPC("ok")
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
		c.observeBytes("received_bytes", frame.Header.Kind, frame.Body.Len())
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
	var (
		batch     []sched.Item
		available []sched.Item
		outbounds []Outbound
		frames    []wire.Frame
		buffers   net.Buffers
	)
	for {
		var err error
		batch, err = c.scheduler.WaitBatchInto(batch)
		if err != nil {
			return
		}
		batch, available = c.collectAvailableWriteItems(batch, available)
		var werr error
		outbounds, frames, werr = c.writeOutboundBatch(batch, outbounds, frames, &buffers)
		clear(batch)
		clear(available)
		if werr != nil {
			c.shutdown(werr)
			return
		}
	}
}

func (c *Conn) collectAvailableWriteItems(batch, scratch []sched.Item) ([]sched.Item, []sched.Item) {
	if len(batch) >= c.cfg.Limits.MaxBatchFrames {
		return batch, scratch[:0]
	}
	scratch = c.scheduler.NextBatchInto(scratch)
	if len(scratch) == 0 {
		return batch, scratch
	}
	return append(batch, scratch...), scratch
}

func (c *Conn) writeOutbound(outbound Outbound) error {
	if timeout := c.cfg.Limits.WriteTimeout; timeout > 0 {
		if err := c.raw.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
			return err
		}
	}
	if err := wire.WriteFrame(c.raw, outbound.toFrame(), c.cfg.Limits.MaxFrameBodyBytes); err != nil {
		return err
	}
	c.observeBytes("sent_bytes", outbound.Kind, outbound.Payload.Len())
	return nil
}

func (c *Conn) writeOutboundBatch(items []sched.Item, outbounds []Outbound, frames []wire.Frame, buffers *net.Buffers) ([]Outbound, []wire.Frame, error) {
	outbounds = outbounds[:0]
	frames = frames[:0]
	batchBytes := 0

	flush := func() error {
		if len(outbounds) == 0 {
			return nil
		}
		if timeout := c.cfg.Limits.WriteTimeout; timeout > 0 {
			if err := c.raw.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
				releaseOutbounds(outbounds)
				return err
			}
		}
		if err := writeFramesInto(c.raw, buffers, frames, c.cfg.Limits.MaxFrameBodyBytes); err != nil {
			releaseOutbounds(outbounds)
			return err
		}
		var sentBytesByKind [core.FrameKindControl + 1]int
		for _, outbound := range outbounds {
			sentBytesByKind[outbound.Kind] += outbound.Payload.Len()
			outbound.Payload.Release()
		}
		c.observeWriteBatch(len(outbounds), batchBytes)
		for kind := core.FrameKindData; kind <= core.FrameKindControl; kind++ {
			c.observeBytes("sent_bytes", kind, sentBytesByKind[kind])
		}
		outbounds = outbounds[:0]
		frames = frames[:0]
		batchBytes = 0
		return nil
	}

	for i, item := range items {
		outbound, ok := item.Value.(Outbound)
		if !ok {
			continue
		}
		if outbound.writeCtx != nil && outbound.writeCtx.Err() != nil {
			outbound.Payload.Release()
			c.pending.Delete(outbound.RequestID)
			c.observePendingRPC("ok")
			continue
		}
		if c.outboundBatchWouldExceed(len(outbounds), batchBytes, outbound) {
			if err := flush(); err != nil {
				releaseSchedItems(items[i:])
				return outbounds, frames, err
			}
		}
		outbounds = append(outbounds, outbound)
		frames = append(frames, outbound.toFrame())
		batchBytes += outbound.Payload.Len()
	}

	return outbounds, frames, flush()
}

func (c *Conn) outboundBatchWouldExceed(frames int, bytes int, next Outbound) bool {
	if frames == 0 {
		return false
	}
	limits := c.cfg.Limits
	if frames+1 > limits.MaxBatchFrames {
		return true
	}
	return bytes+next.Payload.Len() > limits.MaxBatchBytes
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
		c.observePendingRPC("stopped")
	})
}

func (c *Conn) handleRPCResponse(frame wire.Frame) {
	defer frame.Body.Release()

	body := frame.Body.Bytes()
	if len(body) == 0 {
		c.pending.Complete(frame.Header.RequestID, rpc.Response{})
		c.observePendingRPC("ok")
		return
	}

	status := body[0]
	payload := append([]byte(nil), body[1:]...)
	if status != wire.ResponseOK {
		c.pending.Complete(frame.Header.RequestID, rpc.Response{
			Err: core.RemoteError{Code: "remote_error", Message: string(payload)},
		})
		c.observePendingRPC("ok")
		return
	}
	c.pending.Complete(frame.Header.RequestID, rpc.Response{Payload: payload})
	c.observePendingRPC("ok")
}

func (c *Conn) observePendingRPC(result string) {
	if c.cfg.Observer == nil {
		return
	}
	if result == "" {
		result = "ok"
	}
	c.cfg.Observer.ObserveTransport(core.Event{
		Name:     "pending_rpc",
		NodeID:   c.cfg.NodeID,
		SourceID: c.cfg.SourceID,
		Result:   result,
		Inflight: c.pending.Len(),
	})
}

func (c *Conn) observeBytes(name string, kind core.FrameKind, bytes int) {
	if c.cfg.Observer == nil || bytes <= 0 {
		return
	}
	c.cfg.Observer.ObserveTransport(core.Event{
		Name:     name,
		NodeID:   c.cfg.NodeID,
		SourceID: c.cfg.SourceID,
		Kind:     kind,
		Bytes:    bytes,
	})
}

func (c *Conn) observeWriteBatch(frames, bytes int) {
	if c.cfg.Observer == nil || frames <= 0 {
		return
	}
	c.cfg.Observer.ObserveTransport(core.Event{
		Name:          "write_batch",
		NodeID:        c.cfg.NodeID,
		SourceID:      c.cfg.SourceID,
		Result:        "ok",
		Items:         frames,
		Capacity:      c.cfg.Limits.MaxBatchFrames,
		Bytes:         bytes,
		BytesCapacity: int64(c.cfg.Limits.MaxBatchBytes),
	})
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

func releaseOutbounds(outbounds []Outbound) {
	for _, outbound := range outbounds {
		outbound.Payload.Release()
	}
}

// EncodeRPCResponse returns an owned RPC response body containing status then payload.
func EncodeRPCResponse(status uint8, payload []byte) core.OwnedBuffer {
	buf := buffer.DefaultSlabPool.Get(1 + len(payload))
	body := buf.Bytes()
	body[0] = status
	copy(body[1:], payload)
	return buf
}
