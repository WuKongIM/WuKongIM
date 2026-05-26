package gnet

import (
	"bytes"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
)

func TestConnStateRejectsDataOverPendingByteLimit(t *testing.T) {
	state := &connState{
		maxPendingBytes: 4,
	}

	if !state.enqueueData([]byte("abcd")) {
		t.Fatal("enqueueData rejected payload within pending byte limit")
	}
	if got, want := state.pendingBytes, 4; got != want {
		t.Fatalf("pending bytes = %d, want %d", got, want)
	}
	if state.enqueueData([]byte("e")) {
		t.Fatal("enqueueData accepted payload over pending byte limit")
	}
	if got, want := state.pendingBytes, 4; got != want {
		t.Fatalf("pending bytes after rejection = %d, want %d", got, want)
	}

	event, ok := state.nextEvent()
	if !ok {
		t.Fatal("nextEvent returned no event")
	}
	state.releaseEvent(event)
	if got := state.pendingBytes; got != 0 {
		t.Fatalf("pending bytes after release = %d, want 0", got)
	}
	if !state.enqueueData([]byte("efgh")) {
		t.Fatal("enqueueData rejected payload after pending bytes were released")
	}
}

func TestConnStateRejectsWSRawInboundOverPendingByteLimit(t *testing.T) {
	state := &connState{maxPendingBytes: 4}

	if state.appendWSInbound([]byte("1234567890123456789")) {
		t.Fatal("appendWSInbound accepted raw websocket bytes over pending byte limit")
	}
	if got := len(state.wsInbound); got != 0 {
		t.Fatalf("ws inbound bytes after rejection = %d, want 0", got)
	}
}

func TestConnStateRejectsOversizedWSFrameBeforeQueueingPayload(t *testing.T) {
	encoded := encodeMaskedTestWSFrame(t, true, wsOpcodeBinary, [4]byte{1, 2, 3, 4}, []byte("12345"))
	state := &connState{
		mode:            connModeWSFrames,
		maxPendingBytes: 4,
		wsInbound:       append([]byte(nil), encoded...),
	}

	result, ok := state.nextWSResult()
	if !ok {
		t.Fatal("nextWSResult did not return close result for oversized websocket frame")
	}
	if !result.closeNow {
		t.Fatal("oversized websocket frame did not request close")
	}
	if !errors.Is(result.closeErr, ErrPendingBytesExceeded) {
		t.Fatalf("close error = %v, want %v", result.closeErr, ErrPendingBytesExceeded)
	}
	if len(result.payload) != 0 {
		t.Fatalf("oversized websocket frame returned payload bytes = %d, want 0", len(result.payload))
	}
}

func TestConnStateRejectsOversizedWSFragmentedMessage(t *testing.T) {
	first := encodeMaskedTestWSFrame(t, false, wsOpcodeBinary, [4]byte{1, 2, 3, 4}, []byte("123"))
	second := encodeMaskedTestWSFrame(t, true, wsOpcodeContinuation, [4]byte{5, 6, 7, 8}, []byte("45"))
	state := &connState{
		mode:            connModeWSFrames,
		maxPendingBytes: 4,
	}
	state.wsInbound = append(state.wsInbound[:0], first...)
	if result, ok := state.nextWSResult(); ok {
		t.Fatalf("first fragment returned result = %+v, want incomplete", result)
	}

	state.wsInbound = append(state.wsInbound[:0], second...)
	result, ok := state.nextWSResult()
	if !ok {
		t.Fatal("nextWSResult did not return close result for oversized fragmented websocket message")
	}
	if !result.closeNow {
		t.Fatal("oversized fragmented websocket message did not request close")
	}
	if !errors.Is(result.closeErr, ErrPendingBytesExceeded) {
		t.Fatalf("close error = %v, want %v", result.closeErr, ErrPendingBytesExceeded)
	}
	if len(result.payload) != 0 {
		t.Fatalf("oversized fragmented websocket message returned payload bytes = %d, want 0", len(result.payload))
	}
}

func TestConnStateAvoidsCopyForCompletedWSFragmentedMessage(t *testing.T) {
	first := []byte("123")
	secondPayload := []byte("45")
	second := encodeMaskedTestWSFrame(t, true, wsOpcodeContinuation, [4]byte{5, 6, 7, 8}, secondPayload)
	wantPayload := append(append([]byte(nil), first...), secondPayload...)

	state := &connState{
		mode:            connModeWSFrames,
		maxPendingBytes: 1 << 20,
		wsOpcode:        wsOpcodeBinary,
		wsFragment:      append([]byte(nil), first...),
		wsInbound:       append([]byte(nil), second...),
	}

	result, ok := state.nextWSResult()
	if !ok {
		t.Fatal("nextWSResult did not return a completed fragmented websocket message")
	}
	if result.opcode != wsOpcodeBinary {
		t.Fatalf("opcode = %d, want binary", result.opcode)
	}
	if !bytes.Equal(result.payload, wantPayload) {
		t.Fatalf("payload = %q, want %q", result.payload, wantPayload)
	}
	if state.wsOpcode != 0 {
		t.Fatalf("ws opcode = %d, want 0 after completion", state.wsOpcode)
	}
	if state.wsFragment != nil {
		t.Fatal("ws fragment was retained after fragmented websocket message completion")
	}

	inbound := make([]byte, len(second))
	fragment := make([]byte, len(first), len(first)+len(secondPayload))
	runOnce := func() {
		copy(inbound, second)
		copy(fragment, first)
		state.wsOpcode = wsOpcodeBinary
		state.wsFragment = fragment[:len(first)]
		state.wsInbound = inbound[:len(second)]

		result, ok := state.nextWSResult()
		if !ok {
			panic("nextWSResult did not return a completed fragmented websocket message")
		}
		if result.opcode != wsOpcodeBinary {
			panic("nextWSResult returned wrong opcode")
		}
		if !bytes.Equal(result.payload, wantPayload) {
			panic("nextWSResult returned wrong payload")
		}
	}
	allocs := testing.AllocsPerRun(1000, runOnce)
	if allocs != 0 {
		t.Fatalf("allocs = %.0f, want 0", allocs)
	}
}

func TestGnetTCPOnTrafficRejectsOversizedPayloadBeforeCopy(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 128<<10)
	conn := &allocTestGnetConn{}
	runtime := &listenerRuntime{opts: transport.ListenerOptions{Network: "tcp"}}
	runtime.activate()
	state := &connState{
		raw:             conn,
		runtime:         runtime,
		mode:            connModeTCP,
		maxPendingBytes: 4,
		queue:           make([]connEvent, 0, 1),
	}
	conn.ctx = state
	group := &engineGroup{}

	allocs := testing.AllocsPerRun(1000, func() {
		conn.next = payload
		state.mu.Lock()
		state.closing = false
		state.pendingBytes = 0
		state.queue = state.queue[:0]
		state.mu.Unlock()

		group.OnTraffic(conn)
	})
	if allocs != 0 {
		t.Fatalf("allocs = %.0f, want 0", allocs)
	}
}

func TestConnStateNextEventClearsPayloadReference(t *testing.T) {
	payload := []byte("payload")
	state := &connState{
		queue: []connEvent{{kind: connEventData, data: payload}},
	}
	underlying := state.queue

	event, ok := state.nextEvent()
	if !ok {
		t.Fatal("nextEvent returned no event")
	}
	if !bytes.Equal(event.data, payload) {
		t.Fatalf("event payload = %q, want %q", event.data, payload)
	}
	if underlying[0].data != nil {
		t.Fatal("nextEvent retained popped payload in queue backing array")
	}
}

func TestConnStateFailClearsQueuedPayloadReferences(t *testing.T) {
	first := []byte("first")
	second := []byte("second")
	queue := []connEvent{
		{kind: connEventData, data: first},
		{kind: connEventData, data: second},
	}
	state := &connState{
		queue:        queue,
		pendingBytes: len(first) + len(second),
	}

	state.fail(errors.New("boom"))

	if queue[0].data != nil || queue[1].data != nil {
		t.Fatal("fail retained queued payload references in queue backing array")
	}
	if got := state.pendingBytes; got != 0 {
		t.Fatalf("pending bytes = %d, want 0", got)
	}
}
