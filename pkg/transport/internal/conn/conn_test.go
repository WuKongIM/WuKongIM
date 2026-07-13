package conn

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transport/internal/sched"
	"github.com/WuKongIM/WuKongIM/pkg/transport/wire"
)

type recordingObserver struct {
	mu     sync.Mutex
	events []core.Event
}

type writeBatchSnapshot struct {
	serviceIDs []uint16
	bodies     []string
	maxBody    int
}

func (o *recordingObserver) ObserveTransport(event core.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *recordingObserver) snapshot() []core.Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]core.Event(nil), o.events...)
}

func TestConnSendWritesFrame(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()

	c := New(raw, Config{Limits: testLimits()}, nil)
	c.Start()
	defer c.Close(nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Send(context.Background(), Outbound{
			Priority:  core.PriorityRPC,
			ServiceID: 7,
			Payload:   core.CopyOwnedBuffer([]byte("hello")),
		})
	}()

	frameCh := make(chan wire.Frame, 1)
	go func() {
		frame, err := wire.ReadFrame(peer, testLimits().MaxFrameBodyBytes)
		if err != nil {
			t.Errorf("ReadFrame() error = %v", err)
			return
		}
		frameCh <- frame
	}()

	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	frame := waitFrame(t, frameCh)
	defer frame.Body.Release()

	if frame.Header.Kind != core.FrameKindData {
		t.Fatalf("kind = %v, want %v", frame.Header.Kind, core.FrameKindData)
	}
	if frame.Header.Priority != core.PriorityRPC {
		t.Fatalf("priority = %v, want %v", frame.Header.Priority, core.PriorityRPC)
	}
	if frame.Header.ServiceID != 7 {
		t.Fatalf("service id = %d, want 7", frame.Header.ServiceID)
	}
	if got := string(frame.Body.Bytes()); got != "hello" {
		t.Fatalf("body = %q, want hello", got)
	}
}

func TestConnWriteLoopSetsWriteDeadline(t *testing.T) {
	raw := newDeadlineConn()
	c := New(raw, Config{Limits: testLimitsWithWriteTimeout(50 * time.Millisecond)}, nil)

	if err := c.Send(context.Background(), Outbound{
		Priority:  core.PriorityRPC,
		ServiceID: 8,
		Payload:   core.CopyOwnedBuffer([]byte("deadline")),
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	go c.writeLoop()
	deadline := waitDeadline(t, raw.writeDeadlineCh)
	if time.Until(deadline) <= 0 {
		t.Fatalf("write deadline = %v, want future deadline", deadline)
	}

	c.shutdown(nil)
	waitClosed(t, c.writeDone)
}

func TestConnWriteLoopWritesSchedulerBatchOnce(t *testing.T) {
	raw := newDeadlineConn()
	limits := testLimits()
	limits.MaxBatchFrames = 3
	limits.MaxBatchBytes = 64
	c := New(raw, Config{Limits: limits}, nil)

	batchCh := make(chan writeBatchSnapshot, 1)
	oldWriteFramesInto := writeFramesInto
	writeFramesInto = func(w io.Writer, buffers *net.Buffers, frames []wire.Frame, maxBodyBytes int) error {
		snapshot := writeBatchSnapshot{maxBody: maxBodyBytes}
		for _, frame := range frames {
			snapshot.serviceIDs = append(snapshot.serviceIDs, frame.Header.ServiceID)
			snapshot.bodies = append(snapshot.bodies, string(frame.Body.Bytes()))
		}
		batchCh <- snapshot
		return nil
	}
	t.Cleanup(func() {
		writeFramesInto = oldWriteFramesInto
	})

	for i, body := range []string{"one", "two", "three"} {
		if err := c.Send(context.Background(), Outbound{
			Priority:  core.PriorityRPC,
			ServiceID: uint16(10 + i),
			Payload:   core.CopyOwnedBuffer([]byte(body)),
		}); err != nil {
			t.Fatalf("Send(%q) error = %v", body, err)
		}
	}

	go c.writeLoop()
	snapshot := waitWriteBatch(t, batchCh)
	if len(snapshot.serviceIDs) != 3 {
		t.Fatalf("write batch frame count = %d, want 3; snapshot=%+v", len(snapshot.serviceIDs), snapshot)
	}
	if snapshot.maxBody != limits.MaxFrameBodyBytes {
		t.Fatalf("max body = %d, want %d", snapshot.maxBody, limits.MaxFrameBodyBytes)
	}
	for i, want := range []uint16{10, 11, 12} {
		if got := snapshot.serviceIDs[i]; got != want {
			t.Fatalf("service id[%d] = %d, want %d", i, got, want)
		}
	}
	for i, want := range []string{"one", "two", "three"} {
		if got := snapshot.bodies[i]; got != want {
			t.Fatalf("body[%d] = %q, want %q", i, got, want)
		}
	}

	c.shutdown(nil)
	waitClosed(t, c.writeDone)
}

func TestWriteOutboundBatchAggregatesTrafficAndObservesWriteShape(t *testing.T) {
	observer := &recordingObserver{}
	limits := testLimits()
	c := New(newDeadlineConn(), Config{Limits: limits, Observer: observer, NodeID: 12, SourceID: 77}, nil)
	items := []sched.Item{
		{Bytes: 3, Value: Outbound{Kind: core.FrameKindRPCRequest, Priority: core.PriorityRPC, Payload: core.CopyOwnedBuffer([]byte("one"))}},
		{Bytes: 3, Value: Outbound{Kind: core.FrameKindRPCRequest, Priority: core.PriorityRPC, Payload: core.CopyOwnedBuffer([]byte("two"))}},
		{Bytes: 5, Value: Outbound{Kind: core.FrameKindRPCResponse, Priority: core.PriorityRPC, Payload: core.CopyOwnedBuffer([]byte("three"))}},
	}
	var buffers net.Buffers

	if _, _, err := c.writeOutboundBatch(items, nil, nil, &buffers); err != nil {
		t.Fatalf("writeOutboundBatch() error = %v", err)
	}

	events := observer.snapshot()
	var writeBatches, sentBytes []core.Event
	for _, event := range events {
		switch event.Name {
		case "write_batch":
			writeBatches = append(writeBatches, event)
		case "sent_bytes":
			sentBytes = append(sentBytes, event)
		}
	}
	if len(writeBatches) != 1 {
		t.Fatalf("write_batch event count = %d, want 1; events=%#v", len(writeBatches), events)
	}
	batch := writeBatches[0]
	if batch.NodeID != 12 || batch.SourceID != 77 || batch.Items != 3 || batch.Capacity != limits.MaxBatchFrames ||
		batch.Bytes != 11 || batch.BytesCapacity != int64(limits.MaxBatchBytes) {
		t.Fatalf("write_batch = %+v, want node/source and 3 frames/11 bytes with configured limits", batch)
	}
	if len(sentBytes) != 2 {
		t.Fatalf("sent_bytes event count = %d, want one per frame kind; events=%#v", len(sentBytes), events)
	}
	bytesByKind := make(map[core.FrameKind]int, len(sentBytes))
	for _, event := range sentBytes {
		bytesByKind[event.Kind] += event.Bytes
	}
	if bytesByKind[core.FrameKindRPCRequest] != 6 || bytesByKind[core.FrameKindRPCResponse] != 5 {
		t.Fatalf("sent bytes by kind = %#v, want request=6 response=5", bytesByKind)
	}
}

func TestCollectAvailableWriteItemsAddsImmediateSchedulerBacklog(t *testing.T) {
	limits := testLimits()
	c := New(newDeadlineConn(), Config{Limits: limits}, nil)
	batch := make([]sched.Item, 1, limits.MaxBatchFrames)
	batch[0] = sched.Item{
		Priority: core.PriorityRPC,
		Bytes:    3,
		Value: Outbound{
			Kind:     core.FrameKindRPCRequest,
			Priority: core.PriorityRPC,
			Payload:  core.CopyOwnedBuffer([]byte("one")),
		},
	}
	if err := c.scheduler.Enqueue(context.Background(), sched.Item{
		Priority: core.PriorityRPC,
		Bytes:    3,
		Value: Outbound{
			Kind:     core.FrameKindRPCRequest,
			Priority: core.PriorityRPC,
			Payload:  core.CopyOwnedBuffer([]byte("two")),
		},
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	batch, scratch := c.collectAvailableWriteItems(batch, nil)
	defer releaseSchedItems(batch)
	if len(batch) != 2 {
		t.Fatalf("collectAvailableWriteItems() batch len = %d, want 2", len(batch))
	}
	if len(scratch) != 1 {
		t.Fatalf("collectAvailableWriteItems() scratch len = %d, want 1", len(scratch))
	}
	if remaining := c.scheduler.NextBatch(); len(remaining) != 0 {
		releaseSchedItems(remaining)
		t.Fatalf("scheduler remaining items = %d, want 0", len(remaining))
	}
}

func TestWriteLoopReusesScratchAcrossBatches(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()

	limits := core.Limits{
		MaxFrameBodyBytes:     1 << 20,
		MaxQueuedBytesPerConn: 1 << 20,
		MaxQueuedItemsPerConn: 256,
		MaxBatchBytes:         64,
		MaxBatchFrames:        4,
		WriteTimeout:          time.Second,
	}
	c := New(serverSide, Config{Limits: limits}, nil)
	c.Start()
	defer c.Close(nil)

	const total = 32
	go func() {
		for i := 0; i < total; i++ {
			payload := []byte{byte(i), byte(i), byte(i)}
			_ = c.Send(context.Background(), Outbound{
				Kind:     core.FrameKindData,
				Priority: core.PriorityRaft,
				Payload:  core.NewOwnedBuffer(payload, nil),
			})
		}
	}()

	r := clientSide
	for i := 0; i < total; i++ {
		_ = r.SetReadDeadline(time.Now().Add(2 * time.Second))
		frame, err := wire.ReadFrame(r, limits.MaxFrameBodyBytes)
		if err != nil {
			t.Fatalf("ReadFrame(%d) error = %v", i, err)
		}
		body := frame.Body.Bytes()
		if len(body) != 3 || body[0] != byte(i) || body[1] != byte(i) || body[2] != byte(i) {
			frame.Body.Release()
			t.Fatalf("frame %d body = %v, want three bytes of %d", i, body, i)
		}
		frame.Body.Release()
	}
}

func TestConnDispatchesInboundFrame(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()

	inboundCh := make(chan Inbound, 1)
	c := New(raw, Config{Limits: testLimits()}, DispatchFunc(func(ctx context.Context, in Inbound) {
		inboundCh <- in
	}))
	c.Start()
	defer c.Close(nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- wire.WriteFrame(peer, wire.Frame{
			Header: wire.Header{
				Kind:      core.FrameKindNotify,
				Priority:  core.PriorityControl,
				ServiceID: 9,
				RequestID: 11,
			},
			Body: core.CopyOwnedBuffer([]byte("notify")),
		}, testLimits().MaxFrameBodyBytes)
	}()

	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	in := waitInbound(t, inboundCh)
	defer in.Payload.Release()

	if in.Conn != c {
		t.Fatalf("in.Conn = %p, want %p", in.Conn, c)
	}
	if in.Kind != core.FrameKindNotify || in.Priority != core.PriorityControl || in.ServiceID != 9 || in.RequestID != 11 {
		t.Fatalf("inbound metadata = %+v", in)
	}
	if got := string(in.Payload.Bytes()); got != "notify" {
		t.Fatalf("payload = %q, want notify", got)
	}
}

func TestConnObservesTransportBytes(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()

	observer := &recordingObserver{}
	inboundCh := make(chan Inbound, 1)
	c := New(raw, Config{Limits: testLimits(), Observer: observer, NodeID: 12, SourceID: 77}, DispatchFunc(func(ctx context.Context, in Inbound) {
		inboundCh <- in
	}))
	c.Start()
	defer c.Close(nil)

	sendErrCh := make(chan error, 1)
	go func() {
		sendErrCh <- c.Send(context.Background(), Outbound{
			Priority:  core.PriorityRPC,
			ServiceID: 7,
			Payload:   core.CopyOwnedBuffer([]byte("hello")),
		})
	}()
	frame := readPeerFrame(t, peer)
	frame.Body.Release()
	if err := waitErr(t, sendErrCh); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	waitConnEvent(t, observer, func(event core.Event) bool {
		return event.Name == "sent_bytes" &&
			event.NodeID == 12 &&
			event.SourceID == 77 &&
			event.Kind == core.FrameKindData &&
			event.Bytes == len("hello")
	})

	writePeerFrame(t, peer, wire.Frame{
		Header: wire.Header{
			Kind:      core.FrameKindNotify,
			Priority:  core.PriorityControl,
			ServiceID: 9,
		},
		Body: core.CopyOwnedBuffer([]byte("notify")),
	})
	in := waitInbound(t, inboundCh)
	in.Payload.Release()
	waitConnEvent(t, observer, func(event core.Event) bool {
		return event.Name == "received_bytes" &&
			event.NodeID == 12 &&
			event.SourceID == 77 &&
			event.Kind == core.FrameKindNotify &&
			event.Bytes == len("notify")
	})
}

func TestConnCloseFailsPendingRPC(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()

	c := New(raw, Config{Limits: testLimits()}, nil)
	c.Start()

	callCh := make(chan error, 1)
	go func() {
		_, err := c.Call(context.Background(), Outbound{
			Priority:  core.PriorityRPC,
			ServiceID: 3,
			Payload:   core.CopyOwnedBuffer([]byte("request")),
		})
		callCh <- err
	}()

	frameCh := make(chan wire.Frame, 1)
	go func() {
		frame, err := wire.ReadFrame(peer, testLimits().MaxFrameBodyBytes)
		if err != nil {
			t.Errorf("ReadFrame() error = %v", err)
			return
		}
		frameCh <- frame
	}()
	frame := waitFrame(t, frameCh)
	frame.Body.Release()

	c.Close(nil)
	if err := waitErr(t, callCh); !errors.Is(err, core.ErrStopped) {
		t.Fatalf("Call() error = %v, want %v", err, core.ErrStopped)
	}
}

func TestConnCallMapsCanceledContextBeforeSend(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()
	defer raw.Close()

	c := New(raw, Config{Limits: testLimits()}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.Call(ctx, Outbound{
		Priority:  core.PriorityRPC,
		ServiceID: 4,
		Payload:   core.CopyOwnedBuffer([]byte("request")),
	})
	if !errors.Is(err, core.ErrCanceled) {
		t.Fatalf("Call() error = %v, want %v", err, core.ErrCanceled)
	}
}

func TestConnCallSkipsQueuedRPCWhenContextCanceledBeforeWrite(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()

	c := New(raw, Config{Limits: testLimits()}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	callCh := make(chan error, 1)
	go func() {
		_, err := c.Call(ctx, Outbound{
			Priority:  core.PriorityRPC,
			ServiceID: 6,
			Payload:   core.CopyOwnedBuffer([]byte("queued")),
		})
		callCh <- err
	}()

	waitPendingLen(t, c, 1)
	cancel()
	if err := waitErr(t, callCh); !errors.Is(err, core.ErrCanceled) {
		t.Fatalf("Call() error = %v, want %v", err, core.ErrCanceled)
	}

	c.Start()
	if err := peer.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	frame, err := wire.ReadFrame(peer, testLimits().MaxFrameBodyBytes)
	if err == nil {
		frame.Body.Release()
		t.Fatalf("ReadFrame() succeeded, want timeout because queued RPC was canceled")
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("ReadFrame() error = %v, want timeout", err)
	}
	c.Close(nil)
}

func TestConnCloseBeforeStartDoesNotHang(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()

	c := New(raw, Config{Limits: testLimits()}, nil)
	done := make(chan struct{})
	go func() {
		c.Close(nil)
		close(done)
	}()
	waitClosed(t, done)
}

func TestPendingRPCObservation(t *testing.T) {
	raw, peer := net.Pipe()
	defer peer.Close()

	observer := &recordingObserver{}
	c := New(raw, Config{Limits: testLimits(), Observer: observer, NodeID: 12, SourceID: 77}, nil)
	c.Start()
	defer c.Close(nil)

	callCh := make(chan struct {
		payload []byte
		err     error
	}, 1)
	go func() {
		payload, err := c.Call(context.Background(), Outbound{
			Priority:  core.PriorityRPC,
			ServiceID: 5,
			Payload:   core.CopyOwnedBuffer([]byte("request")),
		})
		callCh <- struct {
			payload []byte
			err     error
		}{payload: payload, err: err}
	}()

	req := readPeerFrame(t, peer)
	req.Body.Release()
	waitConnEvent(t, observer, func(event core.Event) bool {
		return event.Name == "pending_rpc" && event.NodeID == 12 && event.SourceID == 77 && event.Inflight == 1
	})

	writePeerFrame(t, peer, wire.Frame{
		Header: wire.Header{
			Kind:      core.FrameKindRPCResponse,
			Priority:  core.PriorityRPC,
			ServiceID: req.Header.ServiceID,
			RequestID: req.Header.RequestID,
		},
		Body: EncodeRPCResponse(wire.ResponseOK, []byte("response")),
	})
	got := waitCall(t, callCh)
	if got.err != nil {
		t.Fatalf("Call() error = %v", got.err)
	}
	if string(got.payload) != "response" {
		t.Fatalf("payload = %q, want response", got.payload)
	}
	waitConnEvent(t, observer, func(event core.Event) bool {
		return event.Name == "pending_rpc" && event.NodeID == 12 && event.SourceID == 77 && event.Inflight == 0
	})
	c.Close(nil)
	waitConnEvent(t, observer, func(event core.Event) bool {
		return event.Name == "pending_rpc" && event.NodeID == 12 && event.SourceID == 77 &&
			event.Result == "stopped" && event.Inflight == 0
	})
}

func TestConnRPCResponseDecode(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		raw, peer := net.Pipe()
		defer peer.Close()
		c := New(raw, Config{Limits: testLimits()}, nil)
		c.Start()
		defer c.Close(nil)

		callCh := make(chan struct {
			payload []byte
			err     error
		}, 1)
		go func() {
			payload, err := c.Call(context.Background(), Outbound{
				Priority:  core.PriorityRPC,
				ServiceID: 5,
				Payload:   core.CopyOwnedBuffer([]byte("request")),
			})
			callCh <- struct {
				payload []byte
				err     error
			}{payload: payload, err: err}
		}()

		req := readPeerFrame(t, peer)
		req.Body.Release()
		writePeerFrame(t, peer, wire.Frame{
			Header: wire.Header{
				Kind:      core.FrameKindRPCResponse,
				Priority:  core.PriorityRPC,
				ServiceID: req.Header.ServiceID,
				RequestID: req.Header.RequestID,
			},
			Body: EncodeRPCResponse(wire.ResponseOK, []byte("response")),
		})

		got := waitCall(t, callCh)
		if got.err != nil {
			t.Fatalf("Call() error = %v", got.err)
		}
		if !bytes.Equal(got.payload, []byte("response")) {
			t.Fatalf("payload = %q, want response", got.payload)
		}
	})

	t.Run("remote error", func(t *testing.T) {
		raw, peer := net.Pipe()
		defer peer.Close()
		c := New(raw, Config{Limits: testLimits()}, nil)
		c.Start()
		defer c.Close(nil)

		callCh := make(chan struct {
			payload []byte
			err     error
		}, 1)
		go func() {
			payload, err := c.Call(context.Background(), Outbound{
				Priority:  core.PriorityRPC,
				ServiceID: 5,
				Payload:   core.CopyOwnedBuffer([]byte("request")),
			})
			callCh <- struct {
				payload []byte
				err     error
			}{payload: payload, err: err}
		}()

		req := readPeerFrame(t, peer)
		req.Body.Release()
		writePeerFrame(t, peer, wire.Frame{
			Header: wire.Header{
				Kind:      core.FrameKindRPCResponse,
				Priority:  core.PriorityRPC,
				ServiceID: req.Header.ServiceID,
				RequestID: req.Header.RequestID,
			},
			Body: EncodeRPCResponse(wire.ResponseErr, []byte("boom")),
		})

		got := waitCall(t, callCh)
		var remoteErr core.RemoteError
		if !errors.As(got.err, &remoteErr) {
			t.Fatalf("Call() error = %v, want RemoteError", got.err)
		}
		if remoteErr.Code != "remote_error" || remoteErr.Message != "boom" {
			t.Fatalf("remote error = %+v", remoteErr)
		}
		if got.payload != nil {
			t.Fatalf("payload = %q, want nil", got.payload)
		}
	})
}

func TestEncodeRPCResponseContentAndRelease(t *testing.T) {
	payload := []byte("response-body")
	buf := EncodeRPCResponse(wire.ResponseOK, payload)
	got := buf.Bytes()
	if len(got) != 1+len(payload) || got[0] != wire.ResponseOK || string(got[1:]) != "response-body" {
		t.Fatalf("EncodeRPCResponse bytes = %v, want status+payload", got)
	}
	buf.Release()
	buf.Release()
}

func testLimits() core.Limits {
	return core.Limits{
		MaxFrameBodyBytes:     1024,
		MaxQueuedBytesPerConn: 4096,
		MaxQueuedItemsPerConn: 16,
		MaxBatchBytes:         1024,
		MaxBatchFrames:        8,
	}
}

func testLimitsWithWriteTimeout(timeout time.Duration) core.Limits {
	limits := testLimits()
	limits.WriteTimeout = timeout
	return limits
}

func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for error")
		return nil
	}
}

func waitFrame(t *testing.T, ch <-chan wire.Frame) wire.Frame {
	t.Helper()
	select {
	case frame := <-ch:
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for frame")
		return wire.Frame{}
	}
}

func waitInbound(t *testing.T, ch <-chan Inbound) Inbound {
	t.Helper()
	select {
	case in := <-ch:
		return in
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inbound")
		return Inbound{}
	}
}

func waitCall(t *testing.T, ch <-chan struct {
	payload []byte
	err     error
}) struct {
	payload []byte
	err     error
} {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for call")
		return struct {
			payload []byte
			err     error
		}{}
	}
}

func waitDeadline(t *testing.T, ch <-chan time.Time) time.Time {
	t.Helper()
	select {
	case deadline := <-ch:
		return deadline
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for write deadline")
		return time.Time{}
	}
}

func waitWriteBatch(t *testing.T, ch <-chan writeBatchSnapshot) writeBatchSnapshot {
	t.Helper()
	select {
	case batch := <-ch:
		return batch
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for write batch")
		return writeBatchSnapshot{}
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for close")
	}
}

func waitConnEvent(t *testing.T, observer *recordingObserver, predicate func(core.Event) bool) []core.Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		events := observer.snapshot()
		for _, event := range events {
			if predicate(event) {
				return events
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for conn event; events=%#v", events)
		case <-ticker.C:
		}
	}
}

func waitPendingLen(t *testing.T, c *Conn, want int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if got := c.pending.Len(); got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for pending len %d, got %d", want, c.pending.Len())
		case <-ticker.C:
		}
	}
}

func readPeerFrame(t *testing.T, peer net.Conn) wire.Frame {
	t.Helper()
	frameCh := make(chan wire.Frame, 1)
	go func() {
		frame, err := wire.ReadFrame(peer, testLimits().MaxFrameBodyBytes)
		if err != nil {
			t.Errorf("ReadFrame() error = %v", err)
			return
		}
		frameCh <- frame
	}()
	return waitFrame(t, frameCh)
}

func writePeerFrame(t *testing.T, peer net.Conn, frame wire.Frame) {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		defer frame.Body.Release()
		errCh <- wire.WriteFrame(peer, frame, testLimits().MaxFrameBodyBytes)
	}()
	if err := waitErr(t, errCh); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
}

type deadlineConn struct {
	writeDeadlineCh chan time.Time
}

func newDeadlineConn() *deadlineConn {
	return &deadlineConn{
		writeDeadlineCh: make(chan time.Time, 1),
	}
}

func (c *deadlineConn) Read(p []byte) (int, error) {
	return 0, io.EOF
}

func (c *deadlineConn) Write(p []byte) (int, error) {
	return len(p), nil
}

func (c *deadlineConn) Close() error {
	return nil
}

func (c *deadlineConn) LocalAddr() net.Addr {
	return fakeAddr("local")
}

func (c *deadlineConn) RemoteAddr() net.Addr {
	return fakeAddr("remote")
}

func (c *deadlineConn) SetDeadline(t time.Time) error {
	return nil
}

func (c *deadlineConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (c *deadlineConn) SetWriteDeadline(t time.Time) error {
	select {
	case c.writeDeadlineCh <- t:
	default:
	}
	return nil
}

type fakeAddr string

func (a fakeAddr) Network() string {
	return string(a)
}

func (a fakeAddr) String() string {
	return string(a)
}
