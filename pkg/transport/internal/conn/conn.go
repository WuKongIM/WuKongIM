// Package conn owns one transport peer connection's read, write, and RPC response loops.
package conn

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/buffer"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/sched"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

var (
	writeFramesInto   = wire.WriteFramesInto
	waitForWriteBatch = waitForBatch
)

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
	// ReceiveTracing enables bounded timing only for negotiated sampled frames.
	// False avoids additional receive clocks, identity locks and timing objects.
	ReceiveTracing bool
}

// Outbound is one caller-owned frame queued for connection writes.
type Outbound struct {
	// Trace carries a selected request's bounded observation through the write owner.
	Trace *rpctrace.Trace
	// FinishTrace transfers terminal server sample ownership to this response write.
	FinishTrace bool
	// TraceResult preserves the handler outcome when the response write succeeds.
	TraceResult         rpctrace.Result
	traceQueue          rpctrace.Span
	traceWrite          rpctrace.Span
	traceObserver       core.Observer
	traceNodeID         core.NodeID
	responseObservation *responseObservation
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
	// Trace is attached by the server only after negotiated identity validation.
	Trace *rpctrace.Trace
	// ReceiveTiming is a verified frame's fixed pre-dispatch observation, if any.
	ReceiveTiming *rpc.ReceiveTiming
	// BudgetMillis is the remaining receiver budget of a negotiated request.
	BudgetMillis uint32
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
	// capability serializes one version-1 negotiation per connection generation.
	capability      atomic.Uint32
	capabilityMu    sync.Mutex
	capabilityReady chan struct{}
	// inboundMu owns cancellation handles until each admitted request finishes.
	inboundMu       sync.Mutex
	inboundRequests map[uint64]context.CancelFunc
	// pendingObservationMu orders absolute pending-RPC snapshots after concurrent
	// pending-table mutations so downstream revision-aware gauges cannot regress.
	pendingObservationMu sync.Mutex

	// traceMu owns the independent once-per-connection trace capability and token.
	traceMu       sync.Mutex
	traceState    uint8
	traceToken    [16]byte
	traceErr      error
	traceReady    chan struct{}
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
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskTransportConnRead, c.readLoop)
		goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskTransportConnWrite, c.writeLoop)
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
	if outbound.Trace != nil {
		stage := rpctrace.StageClientQueue
		if outbound.Kind == core.FrameKindRPCResponse {
			stage = rpctrace.StageResponseQueue
		}
		outbound.traceQueue = outbound.Trace.Begin(stage)
		outbound.traceObserver = c.cfg.Observer
		outbound.traceNodeID = c.cfg.NodeID
	}
	if outbound.Payload.Len() > c.cfg.Limits.MaxFrameBodyBytes {
		outbound.Payload.Release()
		outbound.finishTraceWrite(core.ErrMsgTooLarge)
		return core.ErrMsgTooLarge
	}

	// Admission charges backing storage while batch limits continue to count wire body bytes.
	err := c.scheduler.Enqueue(ctx, sched.Item{
		Priority:      outbound.Priority,
		Bytes:         outbound.Payload.Len(),
		RetainedBytes: outbound.Payload.RetainedBytes(),
		Value:         outbound,
	})
	if err != nil {
		outbound.Payload.Release()
		outbound.finishTraceWrite(err)
		return err
	}
	return nil
}

// Call sends an RPC request and waits for its response or terminal cancellation.
func (c *Conn) Call(ctx context.Context, outbound Outbound) (_ []byte, err error) {
	var requestID uint64
	for {
		previous := c.nextRequestID.Load()
		if previous >= rpcTraceSampleBit-1 {
			outbound.Payload.Release()
			return nil, core.ErrInvalidFrame
		}
		requestID = previous + 1
		if c.nextRequestID.CompareAndSwap(previous, requestID) {
			break
		}
	}
	if outbound.ServiceID != wire.CapabilityServiceID && outbound.ServiceID != wire.TraceCapabilityServiceID {
		if trace := rpctrace.From(ctx); trace != nil {
			if token, ok := c.TraceIdentity(requestID | rpcTraceSampleBit); ok && trace.Bind(token, requestID|rpcTraceSampleBit) {
				requestID |= rpcTraceSampleBit
				outbound.Trace = trace
				outbound.responseObservation = &responseObservation{trace: trace}
				defer func() { outbound.responseObservation.finish(core.RPCTraceResult(err)) }()
			}
		}
	}
	outbound.Kind = core.FrameKindRPCRequest
	if c.capability.Load() == 2 && outbound.ServiceID != wire.CapabilityServiceID && outbound.ServiceID != wire.TraceCapabilityServiceID {
		outbound.Kind = core.FrameKindRPCBudgetRequest
	}
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
		recordResponseReceive(&outbound, &resp)
		return resp.Payload, resp.Err
	case <-ctx.Done():
		if outbound.Kind == core.FrameKindRPCBudgetRequest {
			// Best effort: a full/failed connection cannot reliably deliver cancellation;
			// the propagated budget still bounds queued work on the receiver.
			_ = c.Send(c.ctx, Outbound{Kind: core.FrameKindRPCCancel, Priority: core.PriorityControl, ServiceID: outbound.ServiceID, RequestID: requestID})
		}
		c.pending.Delete(requestID)
		c.observePendingRPC("ok")
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, core.ErrCanceled
		}
		return nil, ctx.Err()
	case <-c.ctx.Done():
		select {
		case resp := <-respCh:
			recordResponseReceive(&outbound, &resp)
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

// WaitContext joins actual reader/writer completion without restarting shutdown.
// Done only signals shutdown admission; a retiring writer can still own buffers.
func (c *Conn) WaitContext(ctx context.Context) error {
	if !c.started.Load() {
		return nil
	}
	for _, done := range []<-chan struct{}{c.readDone, c.writeDone} {
		select {
		case <-done:
			continue
		default:
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (c *Conn) readLoop() {
	defer close(c.readDone)
	for {
		frame, err := wire.ReadFrame(c.raw, c.cfg.Limits.MaxFrameBodyBytes)
		if err != nil {
			c.shutdown(err)
			return
		}
		timing := c.startReceiveTiming(frame.Header)
		c.observeBytes("received_bytes", frame.Header.Kind, frame.Header.Priority, frame.Body.Len())
		if timing != nil {
			timing.ObserverDone = time.Now()
		}
		if frame.Header.Kind == core.FrameKindRPCResponse {
			c.handleRPCResponseWithTiming(frame, timing)
			continue
		}
		if c.dispatch == nil {
			frame.Body.Release()
			continue
		}
		c.dispatch.Dispatch(c.ctx, Inbound{
			ReceiveTiming: timing,
			BudgetMillis:  frame.Header.BudgetMillis,
			Kind:          frame.Header.Kind,
			Priority:      frame.Header.Priority,
			ServiceID:     frame.Header.ServiceID,
			RequestID:     frame.Header.RequestID,
			Payload:       frame.Body,
			Conn:          c,
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
	if len(scratch) > 0 {
		return append(batch, scratch...), scratch
	}
	if !c.shouldWaitForWriteBatch(batch) {
		return batch, scratch
	}
	waitForWriteBatch(c.ctx, c.scheduler.UrgentReady(), c.cfg.Limits.WriteBatchMaxWait)
	scratch = c.scheduler.NextBatchInto(scratch)
	return append(batch, scratch...), scratch
}

// waitForBatch never delays urgent traffic or connection shutdown for coalescing.
func waitForBatch(ctx context.Context, urgent <-chan struct{}, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-urgent:
	case <-timer.C:
	}
}

func (c *Conn) shouldWaitForWriteBatch(batch []sched.Item) bool {
	if c == nil || c.cfg.Limits.WriteBatchMaxWait <= 0 || len(batch) != 1 {
		return false
	}
	switch batch[0].Priority {
	case core.PriorityRPC, core.PriorityBulk:
		return true
	default:
		return false
	}
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
	c.observeBytes("sent_bytes", outbound.Kind, outbound.Priority, outbound.Payload.Len())
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
				finishOutbounds(outbounds, err)
				return err
			}
		}
		for i := range outbounds {
			outbounds[i].traceQueue.End(rpctrace.ResultOK)
			stage := rpctrace.StageRequestWrite
			if outbounds[i].Kind == core.FrameKindRPCResponse {
				stage = rpctrace.StageResponseWrite
			}
			outbounds[i].traceWrite = outbounds[i].Trace.Begin(stage)
		}
		if err := writeFramesInto(c.raw, buffers, frames, c.cfg.Limits.MaxFrameBodyBytes); err != nil {
			finishOutbounds(outbounds, err)
			return err
		}
		var sentBytesByKind [core.FrameKindRPCCancel + 1][core.PriorityBulk + 1]int
		for _, outbound := range outbounds {
			outbound.finishTraceWrite(nil)
			sentBytesByKind[outbound.Kind][outbound.Priority] += outbound.Payload.Len()
			outbound.Payload.Release()
		}
		c.observeWriteBatch(len(outbounds), batchBytes)
		for kind := core.FrameKindData; kind <= core.FrameKindRPCCancel; kind++ {
			for priority := core.Priority(0); priority <= core.PriorityBulk; priority++ {
				c.observeBytes("sent_bytes", kind, priority, sentBytesByKind[kind][priority])
			}
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
			outbound.finishTraceWrite(outbound.writeCtx.Err())
			c.pending.Delete(outbound.RequestID)
			c.observePendingRPC("ok")
			continue
		}
		if c.outboundBatchWouldExceed(len(outbounds), batchBytes, outbound) {
			if err := flush(); err != nil {
				finishSchedItems(items[i:], err)
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
		c.cancelInboundRequests()
		_ = c.raw.Close()
		drained := c.scheduler.Stop(err)
		finishSchedItems(drained, err)
		c.pending.FailAll(err)
		c.observePendingRPC("stopped")
	})
}

func (c *Conn) handleRPCResponse(frame wire.Frame) {
	c.handleRPCResponseWithTiming(frame, nil)
}

// handleRPCResponseWithTiming transfers an optional completed receive interval
// through the existing pending response channel. Late frames retain no owner.
func (c *Conn) handleRPCResponseWithTiming(frame wire.Frame, timing *rpc.ReceiveTiming) {
	defer frame.Body.Release()

	responseChannel, pending := c.pending.Take(frame.Header.RequestID)
	if !pending {
		return
	}
	defer c.observePendingRPC("ok")
	body := frame.Body.Bytes()
	response := rpc.Response{}
	if len(body) > 0 {
		status := body[0]
		if status != wire.ResponseOK {
			code := core.RemoteErrorCodeGeneric
			switch status {
			case wire.ResponseServiceNotFound:
				code = core.RemoteErrorCodeServiceNotFound
			case wire.ResponseTimeout:
				code = core.RemoteErrorCodeTimeout
			case wire.ResponseCanceled:
				code = core.RemoteErrorCodeCanceled
			case wire.ResponseBusy:
				code = core.RemoteErrorCodeBusy
			case wire.ResponseStopped:
				code = core.RemoteErrorCodeStopped
			}
			response.Err = core.RemoteError{Code: code, Message: string(body[1:])}
		} else {
			response.Payload = append([]byte(nil), body[1:]...)
		}
	}
	if timing != nil {
		timing.Notified = time.Now()
		response.ReceiveTiming = timing
	}
	select {
	case responseChannel <- response:
	default:
	}
}

func (c *Conn) observePendingRPC(result string) {
	if c.cfg.Observer == nil {
		return
	}
	c.pendingObservationMu.Lock()
	defer c.pendingObservationMu.Unlock()
	if result == "" {
		result = "ok"
	}
	c.cfg.Observer.ObserveTransport(core.Event{
		Name:     "pending_rpc",
		NodeID:   c.cfg.NodeID,
		SourceID: c.cfg.SourceID,
		Result:   result,
		Revision: core.NextStateRevision(),
		Inflight: c.pending.Len(),
	})
}

func (c *Conn) observeBytes(name string, kind core.FrameKind, priority core.Priority, bytes int) {
	if c.cfg.Observer == nil || bytes <= 0 {
		return
	}
	c.cfg.Observer.ObserveTransport(core.Event{
		Name:     name,
		NodeID:   c.cfg.NodeID,
		SourceID: c.cfg.SourceID,
		Kind:     kind,
		Priority: priority,
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
	var budget uint32
	if o.Kind == core.FrameKindRPCBudgetRequest && o.writeCtx != nil {
		if deadline, ok := o.writeCtx.Deadline(); ok {
			remaining := time.Until(deadline)
			millis := max(int64(1), (int64(remaining)+int64(time.Millisecond)-1)/int64(time.Millisecond))
			budget = uint32(min(millis, int64(^uint32(0))))
		}
	}
	return wire.Frame{
		Header: wire.Header{
			BudgetMillis: budget,
			Kind:         o.Kind,
			Priority:     o.Priority,
			ServiceID:    o.ServiceID,
			RequestID:    o.RequestID,
		},
		Body: o.Payload,
	}
}

func releaseSchedItems(items []sched.Item) { finishSchedItems(items, core.ErrStopped) }
func finishSchedItems(items []sched.Item, err error) {
	for _, item := range items {
		outbound, ok := item.Value.(Outbound)
		if !ok {
			continue
		}
		outbound.Payload.Release()
		outbound.finishTraceWrite(err)
	}
}

func releaseOutbounds(outbounds []Outbound) { finishOutbounds(outbounds, core.ErrStopped) }
func finishOutbounds(outbounds []Outbound, err error) {
	for _, outbound := range outbounds {
		outbound.Payload.Release()
		outbound.finishTraceWrite(err)
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
