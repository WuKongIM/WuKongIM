package gnet

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
	"github.com/gorilla/websocket"
	gnetv2 "github.com/panjf2000/gnet/v2"
)

func TestTCPListenerDeliversOpenDataCloseAndWrite(t *testing.T) {
	handler := newTCPRecordingHandler(func(conn transport.Conn, data []byte) error {
		return conn.Write([]byte("ack:" + string(data)))
	})

	listener := buildTCPListener(t, "tcp-one", handler)
	defer func() { _ = listener.Stop() }()

	if err := listener.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	conn, err := net.Dial("tcp", listener.Addr())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	reply := make([]byte, len("ack:ping"))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("ReadFull: %v (opens=%d data=%d closes=%d order=%v)", err, handler.OpenCount(), handler.DataCount(), handler.CloseCount(), handler.EventOrder())
	}
	if got, want := string(reply), "ack:ping"; got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
	_ = conn.Close()

	waitUntil(t, time.Second, func() bool {
		return handler.OpenCount() == 1 && handler.DataCount() == 1 && handler.CloseCount() == 1
	})

	open := handler.OpenSnapshot(0)
	if open.id == 0 {
		t.Fatal("OnOpen received zero connection ID")
	}
	if got, want := open.localAddr, listener.Addr(); got != want {
		t.Fatalf("OnOpen LocalAddr() = %q, want %q", got, want)
	}
	if open.remoteAddr == "" {
		t.Fatal("OnOpen RemoteAddr() is empty")
	}
	if got, want := handler.DataPayload(0), "ping"; got != want {
		t.Fatalf("OnData payload = %q, want %q", got, want)
	}
	if got, want := handler.EventOrder(), []string{"open", "data", "close"}; !equalStrings(got, want) {
		t.Fatalf("event order = %v, want %v", got, want)
	}
}

func TestTCPListenersShareOneEngineAndRemainIndependentlyAddressable(t *testing.T) {
	firstHandler := newTCPRecordingHandler(nil)
	secondHandler := newTCPRecordingHandler(nil)

	first, second := buildTCPListeners(t,
		namedTCPListenerSpecWithAddress("tcp-a", firstHandler, freeTCPAddress(t)),
		namedTCPListenerSpecWithAddress("tcp-b", secondHandler, freeTCPAddress(t)),
	)
	defer func() { _ = second.Stop() }()
	defer func() { _ = first.Stop() }()

	if first.group != second.group {
		t.Fatal("expected logical listeners to share one engine group")
	}

	if err := first.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := second.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	if first.Addr() == "" || second.Addr() == "" {
		t.Fatalf("listeners must expose bound addresses, got %q and %q", first.Addr(), second.Addr())
	}
	if first.Addr() == second.Addr() {
		t.Fatalf("listeners must expose distinct addresses, both were %q", first.Addr())
	}

	sendTCPPayload(t, first.Addr(), "alpha")
	sendTCPPayload(t, second.Addr(), "beta")

	waitUntil(t, time.Second, func() bool {
		return firstHandler.DataCount() == 1 && secondHandler.DataCount() == 1
	})

	if got, want := firstHandler.DataPayload(0), "alpha"; got != want {
		t.Fatalf("first handler payload = %q, want %q", got, want)
	}
	if got, want := secondHandler.DataPayload(0), "beta"; got != want {
		t.Fatalf("second handler payload = %q, want %q", got, want)
	}
}

func TestTCPStartOneListenerDoesNotBindUnstartedSibling(t *testing.T) {
	firstHandler := newTCPRecordingHandler(nil)
	secondHandler := newTCPRecordingHandler(nil)

	first, second := buildTCPListeners(t,
		namedTCPListenerSpecWithAddress("tcp-a", firstHandler, freeTCPAddress(t)),
		namedTCPListenerSpecWithAddress("tcp-b", secondHandler, freeTCPAddress(t)),
	)
	defer func() { _ = second.Stop() }()
	defer func() { _ = first.Stop() }()

	if err := first.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}

	if !canDial(first.Addr()) {
		t.Fatalf("started listener %q is not dialable", first.Addr())
	}
	if canDial(second.Addr()) {
		t.Fatalf("unstarted sibling %q became dialable before Start", second.Addr())
	}
	if got := secondHandler.OpenCount(); got != 0 {
		t.Fatalf("unstarted sibling open count = %d, want 0", got)
	}
}

func TestTCPBadSiblingDoesNotBreakEarlierStart(t *testing.T) {
	badAddr, hold := occupyTCPAddress(t)
	defer func() { _ = hold.Close() }()

	goodHandler := newTCPRecordingHandler(func(conn transport.Conn, data []byte) error {
		return conn.Write([]byte("good:" + string(data)))
	})
	badHandler := newTCPRecordingHandler(nil)
	good, bad := buildTCPListeners(t,
		namedTCPListenerSpecWithAddress("tcp-a", goodHandler, freeTCPAddress(t)),
		namedTCPListenerSpecWithAddress("tcp-b", badHandler, badAddr),
	)
	defer func() { _ = bad.Stop() }()
	defer func() { _ = good.Stop() }()

	if err := good.Start(); err != nil {
		t.Fatalf("good Start: %v", err)
	}
	firstConn := mustDialTCP(t, good.Addr())
	writeAndReadExact(t, firstConn, []byte("good"), "good:good")
	_ = firstConn.Close()
	waitUntil(t, time.Second, func() bool { return goodHandler.DataCount() == 1 })

	if err := bad.Start(); err == nil {
		t.Fatal("bad Start succeeded, want bind failure")
	}

	secondConn := mustDialTCP(t, good.Addr())
	writeAndReadExact(t, secondConn, []byte("still-good"), "good:still-good")
	_ = secondConn.Close()
	waitUntil(t, time.Second, func() bool { return goodHandler.DataCount() == 2 })
	if got := badHandler.OpenCount(); got != 0 {
		t.Fatalf("bad sibling open count = %d, want 0", got)
	}
}

func TestTCPStopOneLogicalListenerKeepsOtherConnectionsAlive(t *testing.T) {
	firstHandler := newTCPRecordingHandler(func(conn transport.Conn, data []byte) error {
		return conn.Write([]byte("first:" + string(data)))
	})
	secondHandler := newTCPRecordingHandler(func(conn transport.Conn, data []byte) error {
		return conn.Write([]byte("second:" + string(data)))
	})

	first, second := buildTCPListeners(t,
		namedTCPListenerSpecWithAddress("tcp-a", firstHandler, freeTCPAddress(t)),
		namedTCPListenerSpecWithAddress("tcp-b", secondHandler, freeTCPAddress(t)),
	)
	defer func() { _ = second.Stop() }()
	defer func() { _ = first.Stop() }()

	if err := first.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := second.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	firstConn := mustDialTCP(t, first.Addr())
	defer func() { _ = firstConn.Close() }()
	secondConn := mustDialTCP(t, second.Addr())
	defer func() { _ = secondConn.Close() }()

	waitUntil(t, time.Second, func() bool {
		return firstHandler.OpenCount() == 1 && secondHandler.OpenCount() == 1
	})

	stopDone := make(chan struct{})
	var stopOnce sync.Once
	firstHandler.onOpen = func(conn transport.Conn) error {
		select {
		case <-stopDone:
			t.Errorf("OnOpen reached stopped listener for %s", conn.RemoteAddr())
		default:
		}
		return nil
	}
	firstHandler.onData = func(conn transport.Conn, data []byte) error {
		select {
		case <-stopDone:
			t.Errorf("OnData reached stopped listener after Stop for %s", conn.RemoteAddr())
		default:
		}
		return conn.Write([]byte("first:" + string(data)))
	}

	raceDone := make(chan struct{})
	go func() {
		defer close(raceDone)
		for {
			select {
			case <-stopDone:
				return
			default:
			}

			conn, err := net.DialTimeout("tcp", first.Addr(), 50*time.Millisecond)
			if err != nil {
				continue
			}
			_, _ = conn.Write([]byte("racing"))
			_ = conn.Close()
		}
	}()

	if err := first.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	stopOnce.Do(func() { close(stopDone) })
	<-raceDone

	openCountAfterStop := firstHandler.OpenCount()
	dataCountAfterStop := firstHandler.DataCount()

	waitUntil(t, time.Second, func() bool {
		return firstHandler.CloseCount() >= 1
	})

	writeAndReadExact(t, secondConn, []byte("still-running"), "second:still-running")
	waitUntil(t, time.Second, func() bool { return secondHandler.DataCount() == 1 })

	if got, want := secondHandler.DataPayload(0), "still-running"; got != want {
		t.Fatalf("second handler payload = %q, want %q", got, want)
	}
	if got := firstHandler.DataCount(); got != 0 {
		t.Fatalf("stopped listener received %d payloads, want 0", got)
	}

	expectReadFailure(t, firstConn)

	postStopConn, err := net.DialTimeout("tcp", first.Addr(), 200*time.Millisecond)
	if err == nil {
		defer func() { _ = postStopConn.Close() }()
		_ = postStopConn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
		_, _ = postStopConn.Write([]byte("blocked"))
		expectReadFailure(t, postStopConn)
	}

	if got := firstHandler.OpenCount(); got != 1 {
		t.Fatalf("stopped listener open count changed after Stop: got %d, want %d", got, openCountAfterStop)
	}
	if got := firstHandler.DataCount(); got != dataCountAfterStop {
		t.Fatalf("stopped listener data count changed after Stop: got %d, want %d", got, dataCountAfterStop)
	}
}

func TestTCPAndWebSocketListenersShareOneEngineGroup(t *testing.T) {
	tcpHandler := newTCPRecordingHandler(func(conn transport.Conn, data []byte) error {
		return conn.Write([]byte("tcp:" + string(data)))
	})
	wsHandler := newTCPRecordingHandler(func(conn transport.Conn, data []byte) error {
		return conn.Write([]byte("ws:" + string(data)))
	})

	listeners, err := NewFactory().Build([]transport.ListenerSpec{
		namedTCPListenerSpecWithAddress("tcp-a", tcpHandler, freeTCPAddress(t)),
		namedWSListenerSpecWithAddress("ws-b", wsHandler, freeTCPAddress(t), "", nil),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	tcpListener := requireListenerHandle(t, listeners[0])
	wsListener := requireListenerHandle(t, listeners[1])
	defer func() { _ = wsListener.Stop() }()
	defer func() { _ = tcpListener.Stop() }()

	if tcpListener.group != wsListener.group {
		t.Fatal("expected tcp and websocket listeners to share one engine group")
	}

	if err := tcpListener.Start(); err != nil {
		t.Fatalf("tcp Start: %v", err)
	}
	if err := wsListener.Start(); err != nil {
		t.Fatalf("ws Start: %v", err)
	}

	tcpConn := mustDialTCP(t, tcpListener.Addr())
	defer func() { _ = tcpConn.Close() }()
	writeAndReadExact(t, tcpConn, []byte("ping"), "tcp:ping")

	wsConn := mustDialWS(t, wsListener.Addr(), "")
	defer func() { _ = wsConn.Close() }()
	if err := wsConn.WriteMessage(websocket.TextMessage, []byte("pong")); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	messageType, payload, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if messageType != websocket.TextMessage {
		t.Fatalf("message type = %d, want %d", messageType, websocket.TextMessage)
	}
	if got, want := string(payload), "ws:pong"; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}

	waitUntil(t, time.Second, func() bool {
		return tcpHandler.DataCount() == 1 && wsHandler.DataCount() == 1
	})
}

func TestTCPLastStopShutsSharedGroupDown(t *testing.T) {
	first, second := buildTCPListeners(t,
		namedTCPListenerSpecWithAddress("tcp-a", newTCPRecordingHandler(nil), freeTCPAddress(t)),
		namedTCPListenerSpecWithAddress("tcp-b", newTCPRecordingHandler(nil), freeTCPAddress(t)),
	)

	if err := first.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := second.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}

	firstAddr := first.Addr()
	secondAddr := second.Addr()
	if !canDial(firstAddr) {
		t.Fatalf("expected first listener %q to accept connections before shutdown", firstAddr)
	}
	if !canDial(secondAddr) {
		t.Fatalf("expected second listener %q to accept connections before shutdown", secondAddr)
	}

	if err := first.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := second.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}

	waitUntil(t, time.Second, func() bool {
		return !canDial(firstAddr) && !canDial(secondAddr)
	})
}

func TestTCPFinalStopErrorPreservesGroupState(t *testing.T) {
	spec := namedTCPListenerSpecWithAddress("tcp-a", newTCPRecordingHandler(nil), "127.0.0.1:9100")
	group := newEngineGroup([]transport.ListenerSpec{spec})
	runtime := group.runtimes[0]
	runtime.activate()
	runtime.setAddr("127.0.0.1:9100")

	group.running = true
	group.cycle = newEngineCycle()
	group.routes = map[string]*listenerRuntime{
		runtime.opts.Address: runtime,
		runtime.addr():       runtime,
	}

	stopErr := errors.New("stop failed")
	group.stopEngineFn = func(engine gnetv2.Engine, cycle *engineCycle) error {
		return stopErr
	}

	if err := group.stop(runtime); !errors.Is(err, stopErr) {
		t.Fatalf("stop error = %v, want %v", err, stopErr)
	}

	if !group.running {
		t.Fatal("group marked not running after stop error")
	}
	if group.cycle == nil {
		t.Fatal("group cycle cleared after stop error")
	}
	if len(group.routes) == 0 {
		t.Fatal("group routes cleared after stop error")
	}
	if got := runtime.isActive(); got {
		t.Fatal("runtime remained active after stop")
	}
}

type tcpOpenSnapshot struct {
	id         uint64
	localAddr  string
	remoteAddr string
}

type tcpRecordingHandler struct {
	mu       sync.Mutex
	onOpen   func(conn transport.Conn) error
	onData   func(conn transport.Conn, data []byte) error
	opens    []tcpOpenSnapshot
	payloads [][]byte
	closes   []error
	dataErrs []error
	events   []string
}

func newTCPRecordingHandler(onData func(conn transport.Conn, data []byte) error) *tcpRecordingHandler {
	return &tcpRecordingHandler{onData: onData}
}

func (h *tcpRecordingHandler) OnOpen(conn transport.Conn) error {
	h.mu.Lock()
	h.opens = append(h.opens, tcpOpenSnapshot{
		id:         conn.ID(),
		localAddr:  conn.LocalAddr(),
		remoteAddr: conn.RemoteAddr(),
	})
	h.events = append(h.events, "open")
	onOpen := h.onOpen
	h.mu.Unlock()
	if onOpen != nil {
		return onOpen(conn)
	}
	return nil
}

func (h *tcpRecordingHandler) OnData(conn transport.Conn, data []byte) error {
	h.mu.Lock()
	h.payloads = append(h.payloads, append([]byte(nil), data...))
	h.events = append(h.events, "data")
	h.mu.Unlock()

	var err error
	if h.onData != nil {
		err = h.onData(conn, data)
	}

	h.mu.Lock()
	h.dataErrs = append(h.dataErrs, err)
	h.mu.Unlock()
	return err
}

func (h *tcpRecordingHandler) OnClose(conn transport.Conn, err error) {
	h.mu.Lock()
	h.closes = append(h.closes, err)
	h.events = append(h.events, "close")
	h.mu.Unlock()
}

func (h *tcpRecordingHandler) OpenCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.opens)
}

func (h *tcpRecordingHandler) DataCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.payloads)
}

func (h *tcpRecordingHandler) CloseCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.closes)
}

func (h *tcpRecordingHandler) OpenSnapshot(index int) tcpOpenSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.opens[index]
}

func (h *tcpRecordingHandler) DataPayload(index int) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return string(h.payloads[index])
}

func (h *tcpRecordingHandler) EventOrder() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.events...)
}

func (h *tcpRecordingHandler) DataErr(index int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.dataErrs[index]
}

func buildTCPListener(t *testing.T, name string, handler transport.ConnHandler) *listenerHandle {
	t.Helper()

	listeners, err := NewFactory().Build([]transport.ListenerSpec{namedTCPListenerSpec(name, handler)})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return requireListenerHandle(t, listeners[0])
}

func buildTCPListeners(t *testing.T, specs ...transport.ListenerSpec) (*listenerHandle, *listenerHandle) {
	t.Helper()

	listeners, err := NewFactory().Build(specs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got, want := len(listeners), len(specs); got != want {
		t.Fatalf("Build returned %d listeners, want %d", got, want)
	}
	return requireListenerHandle(t, listeners[0]), requireListenerHandle(t, listeners[1])
}

func namedTCPListenerSpec(name string, handler transport.ConnHandler) transport.ListenerSpec {
	return namedTCPListenerSpecWithAddress(name, handler, "127.0.0.1:0")
}

func namedTCPListenerSpecWithAddress(name string, handler transport.ConnHandler, address string) transport.ListenerSpec {
	return transport.ListenerSpec{
		Options: transport.ListenerOptions{
			Name:    name,
			Network: "tcp",
			Address: address,
		},
		Handler: handler,
	}
}

func sendTCPPayload(t *testing.T, addr string, payload string) {
	t.Helper()

	conn := mustDialTCP(t, addr)
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte(payload)); err != nil {
		t.Fatalf("Write(%q): %v", addr, err)
	}
}

func mustDialTCP(t *testing.T, addr string) net.Conn {
	t.Helper()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial(%q): %v", addr, err)
	}
	return conn
}

func writeAndReadExact(t *testing.T, conn net.Conn, payload []byte, want string) {
	t.Helper()

	if err := conn.SetDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("SetDeadline: %v", err)
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	reply := make([]byte, len(want))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if got := string(reply); got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
}

func expectReadFailure(t *testing.T, conn net.Conn) {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("Read succeeded, want connection to be closed")
	}
}

func canDial(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func freeTCPAddress(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return addr
}

func occupyTCPAddress(t *testing.T) (string, net.Listener) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	return ln.Addr().String(), ln
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

var _ transport.ConnHandler = (*tcpRecordingHandler)(nil)
