package gnet

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/gateway/transport"
	gnetv2 "github.com/panjf2000/gnet/v2"
)

func TestFactoryMetadataOptionsAndNetworkValidation(t *testing.T) {
	options := Options{Multicore: true, NumEventLoop: 3, ReadBufferCap: 1024}
	factory := NewFactory(options)
	if got, want := factory.Name(), Name; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
	got := factory.Options()
	if got.Multicore != options.Multicore || got.NumEventLoop != options.NumEventLoop || got.ReadBufferCap != options.ReadBufferCap {
		t.Fatalf("Options() = %+v, want %+v", got, options)
	}
	var nilFactory *Factory
	if got := nilFactory.Options(); got != (Options{}) {
		t.Fatalf("nil Options() = %+v, want zero", got)
	}
	listeners, err := factory.Build([]transport.ListenerSpec{{
		Options: transport.ListenerOptions{Network: "udp", Address: "127.0.0.1:9000"},
	}})
	if err == nil || !strings.Contains(err.Error(), "unsupported network") {
		t.Fatalf("Build(udp) error = %v, want unsupported network", err)
	}
	if listeners != nil {
		t.Fatalf("Build(udp) listeners = %v, want nil", listeners)
	}

	var nilGroup *engineGroup
	if got := nilGroup.gnetOptions(); got != nil {
		t.Fatalf("nil group options = %v, want nil", got)
	}
}

func TestListenerHandleStartStopIsIdempotentForBoundRuntime(t *testing.T) {
	spec := transport.ListenerSpec{
		Options: transport.ListenerOptions{Name: "tcp", Network: "tcp", Address: "127.0.0.1:9100"},
		Handler: noopHandler{},
	}
	group := newEngineGroup([]transport.ListenerSpec{spec})
	runtime := group.runtimes[0]
	runtime.setAddr("127.0.0.1:19100")
	group.running = true
	group.routes = map[string]*listenerRuntime{
		runtime.opts.Address: runtime,
		runtime.addr():       runtime,
	}
	stopCalls := 0
	group.stopEngineFn = func(gnetv2.Engine, *engineCycle) error {
		stopCalls++
		return nil
	}
	handle := &listenerHandle{opts: spec.Options, runtime: runtime, group: group}

	if err := handle.Start(); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	if err := handle.Start(); err != nil {
		t.Fatalf("second Start(): %v", err)
	}
	if !handle.started || !runtime.isActive() {
		t.Fatalf("after start: handle.started=%v runtime.active=%v", handle.started, runtime.isActive())
	}
	if got, want := handle.Addr(), "127.0.0.1:19100"; got != want {
		t.Fatalf("Addr() = %q, want %q", got, want)
	}

	if err := handle.Stop(); err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	if err := handle.Stop(); err != nil {
		t.Fatalf("second Stop(): %v", err)
	}
	if handle.started || runtime.isActive() || group.running {
		t.Fatalf("after stop: handle.started=%v runtime.active=%v group.running=%v", handle.started, runtime.isActive(), group.running)
	}
	if stopCalls != 1 {
		t.Fatalf("stop engine calls = %d, want 1", stopCalls)
	}
	if len(group.routes) != 0 || group.cycle != nil {
		t.Fatalf("stopped group routes=%d cycle=%v", len(group.routes), group.cycle)
	}

	unstarted := &listenerHandle{opts: spec.Options, group: group}
	if err := unstarted.Start(); err == nil {
		t.Fatal("Start() with nil runtime error = nil")
	}
	if unstarted.started {
		t.Fatal("failed Start() marked handle started")
	}
	if err := unstarted.Stop(); err != nil {
		t.Fatalf("Stop() after failed start: %v", err)
	}
	if got := unstarted.Addr(); got != spec.Options.Address {
		t.Fatalf("fallback Addr() = %q, want %q", got, spec.Options.Address)
	}
}

func TestListenerRuntimeAdmissionGenerationAndErrorReporting(t *testing.T) {
	reported := make([]error, 0, 1)
	runtime := &listenerRuntime{opts: transport.ListenerOptions{OnError: func(err error) {
		reported = append(reported, err)
	}}}
	state := &connState{}
	if runtime.admitConn(state) {
		t.Fatal("inactive runtime admitted connection")
	}

	runtime.activate()
	if !runtime.admitConn(state) {
		t.Fatal("active runtime rejected connection")
	}
	if !runtime.shouldDispatch(state) {
		t.Fatal("newly admitted connection was not dispatchable")
	}
	snapshot := runtime.deactivateAndSnapshot()
	if len(snapshot) != 1 || snapshot[0] != state {
		t.Fatalf("deactivation snapshot = %v, want admitted state", snapshot)
	}
	if runtime.shouldDispatch(state) {
		t.Fatal("connection remained dispatchable after generation changed")
	}
	runtime.untrackConn(state)
	if len(runtime.conns) != 0 {
		t.Fatalf("tracked connections after untrack = %d", len(runtime.conns))
	}
	var nilRuntime *listenerRuntime
	nilRuntime.untrackConn(state)
	runtime.untrackConn(nil)

	wantErr := errors.New("listener failed")
	runtime.reportError(nil)
	runtime.reportError(wantErr)
	nilRuntime.reportError(wantErr)
	if len(reported) != 1 || !errors.Is(reported[0], wantErr) {
		t.Fatalf("reported errors = %v, want %v", reported, wantErr)
	}
}

func TestEngineCycleAndBootReportExactlyOneOutcome(t *testing.T) {
	cycle := newEngineCycle()
	firstErr := errors.New("first")
	cycle.signalBoot(firstErr)
	cycle.signalBoot(errors.New("second"))
	if got := <-cycle.bootCh; !errors.Is(got, firstErr) {
		t.Fatalf("boot error = %v, want %v", got, firstErr)
	}
	if _, ok := <-cycle.bootCh; ok {
		t.Fatal("boot channel remained open")
	}

	successGroup := newEngineGroup(nil)
	successCycle := newEngineCycle()
	successGroup.cycle = successCycle
	if action := successGroup.OnBoot(gnetv2.Engine{}); action != gnetv2.None {
		t.Fatalf("empty OnBoot action = %v, want none", action)
	}
	if err := <-successCycle.bootCh; err != nil {
		t.Fatalf("empty OnBoot error = %v, want nil", err)
	}
	if len(successGroup.routes) != 0 {
		t.Fatalf("empty OnBoot routes = %d, want 0", len(successGroup.routes))
	}

	var reported error
	runtime := &listenerRuntime{opts: transport.ListenerOptions{
		Name:    "missing",
		Address: "127.0.0.1:9999",
		OnError: func(err error) { reported = err },
	}}
	failureGroup := newEngineGroup(nil)
	failureCycle := newEngineCycle()
	failureGroup.cycle = failureCycle
	failureGroup.bootRuntimes = []*listenerRuntime{runtime}
	if action := failureGroup.OnBoot(gnetv2.Engine{}); action != gnetv2.Shutdown {
		t.Fatalf("invalid engine OnBoot action = %v, want shutdown", action)
	}
	bootErr := <-failureCycle.bootCh
	if bootErr == nil || reported == nil || reported.Error() != bootErr.Error() {
		t.Fatalf("boot error=%v reported=%v, want matching errors", bootErr, reported)
	}
}

func TestEngineGroupTopologyHelpersDeduplicateRoutes(t *testing.T) {
	var firstErrors, secondErrors int
	group := newEngineGroup([]transport.ListenerSpec{
		{Options: transport.ListenerOptions{Name: "first", Address: "first", OnError: func(error) { firstErrors++ }}},
		{Options: transport.ListenerOptions{Name: "second", Address: "second", OnError: func(error) { secondErrors++ }}},
		{Options: transport.ListenerOptions{Name: "third", Address: "third"}},
	})
	first, second, third := group.runtimes[0], group.runtimes[1], group.runtimes[2]
	first.activate()
	third.activate()
	active := group.activeRuntimesLocked()
	if len(active) != 2 || active[0] != first || active[1] != third {
		t.Fatalf("active runtimes = %v, want first and third", active)
	}

	group.routes = map[string]*listenerRuntime{
		"first":       first,
		"first-bound": first,
		"second":      second,
	}
	if bound := group.boundRuntimesLocked(); len(bound) != 2 {
		t.Fatalf("bound runtimes = %d, want 2 unique", len(bound))
	}
	if !group.isBoundLocked(first) || group.isBoundLocked(nil) || group.isBoundLocked(third) {
		t.Fatalf("bound checks first=%v nil=%v third=%v", group.isBoundLocked(first), group.isBoundLocked(nil), group.isBoundLocked(third))
	}
	if !group.hasActiveBoundRuntimeLocked() {
		t.Fatal("group did not find active bound runtime")
	}
	first.deactivateAndSnapshot()
	if group.hasActiveBoundRuntimeLocked() {
		t.Fatal("group found active bound runtime after deactivation")
	}
	if got := group.runtimeByAddr("second"); got != second {
		t.Fatalf("runtimeByAddr(second) = %p, want %p", got, second)
	}

	group.reportGroupError(group.runtimes, nil)
	group.reportGroupError(group.runtimes, errors.New("shared failure"))
	if firstErrors != 1 || secondErrors != 1 {
		t.Fatalf("group error callbacks first=%d second=%d, want 1 each", firstErrors, secondErrors)
	}
}

func TestEngineGroupOnOpenEnforcesRouteActorAndRuntimeAdmission(t *testing.T) {
	raw := &contractGnetConn{}
	group := newEngineGroup(nil)
	if _, action := group.OnOpen(raw); action != gnetv2.Close {
		t.Fatalf("OnOpen without route action = %v, want close", action)
	}

	runtime := &listenerRuntime{
		opts:  transport.ListenerOptions{Network: "tcp", Address: "local"},
		conns: make(map[*connState]struct{}),
	}
	group.routes = map[string]*listenerRuntime{"local": runtime}
	if _, action := group.OnOpen(raw); action != gnetv2.Close {
		t.Fatalf("OnOpen without actors action = %v, want close", action)
	}
	group.actors.Store(&actorPool{})
	if _, action := group.OnOpen(raw); action != gnetv2.Close {
		t.Fatalf("OnOpen without actor shard action = %v, want close", action)
	}

	actors := newActorPool(1)
	group.actors.Store(actors)
	if _, action := group.OnOpen(raw); action != gnetv2.Close {
		t.Fatalf("OnOpen inactive runtime action = %v, want close", action)
	}
	runtime.activate()
	if _, action := group.OnOpen(raw); action != gnetv2.None {
		t.Fatalf("OnOpen active runtime action = %v, want none", action)
	}
	state, ok := raw.Context().(*connState)
	if !ok || state == nil {
		t.Fatalf("connection context = %T, want *connState", raw.Context())
	}
	if state.id == 0 || state.owner != actors.shards[0] || len(state.queue) != 1 || state.queue[0].kind != connEventOpen {
		t.Fatalf("admitted state id=%d owner=%p queue=%+v", state.id, state.owner, state.queue)
	}
	if len(runtime.conns) != 1 {
		t.Fatalf("tracked connections = %d, want 1", len(runtime.conns))
	}

	wsRaw := &contractGnetConn{}
	wsRuntime := &listenerRuntime{
		opts:   transport.ListenerOptions{Network: "websocket", Address: "ws"},
		active: true,
		conns:  make(map[*connState]struct{}),
	}
	group.routes["local"] = wsRuntime
	if _, action := group.OnOpen(wsRaw); action != gnetv2.None {
		t.Fatalf("websocket OnOpen action = %v, want none", action)
	}
	wsState := wsRaw.Context().(*connState)
	if wsState.mode != connModeWSHandshake || len(wsState.queue) != 0 {
		t.Fatalf("websocket state mode=%v queue=%v", wsState.mode, wsState.queue)
	}
}

func TestEngineGroupOnTrafficHandlesTCPReadFailuresAndStaleConnections(t *testing.T) {
	group := &engineGroup{}
	if action := group.OnTraffic(&contractGnetConn{}); action != gnetv2.Close {
		t.Fatalf("OnTraffic without state action = %v, want close", action)
	}

	runtime := &listenerRuntime{active: true}
	staleRaw := &contractGnetConn{}
	staleState := &connState{raw: staleRaw, runtime: runtime, generation: 1, mode: connModeTCP}
	staleRaw.SetContext(staleState)
	if action := group.OnTraffic(staleRaw); action != gnetv2.None || staleRaw.closeCalls != 1 {
		t.Fatalf("stale traffic action=%v closes=%d", action, staleRaw.closeCalls)
	}

	readErr := errors.New("read failed")
	errorRaw := &contractGnetConn{nextErr: readErr}
	errorState := &connState{raw: errorRaw, runtime: runtime, mode: connModeTCP}
	errorRaw.SetContext(errorState)
	if action := group.OnTraffic(errorRaw); action != gnetv2.None {
		t.Fatalf("read failure action = %v, want none", action)
	}
	if errorRaw.closeCalls != 1 || len(errorState.queue) != 1 || !errors.Is(errorState.queue[0].err, readErr) {
		t.Fatalf("read failure closes=%d queue=%+v", errorRaw.closeCalls, errorState.queue)
	}

	emptyRaw := &contractGnetConn{}
	emptyState := &connState{raw: emptyRaw, runtime: runtime, mode: connModeTCP}
	emptyRaw.SetContext(emptyState)
	if action := group.OnTraffic(emptyRaw); action != gnetv2.None || len(emptyState.queue) != 0 {
		t.Fatalf("empty traffic action=%v queue=%v", action, emptyState.queue)
	}
}

func TestEngineGroupWebSocketTrafficHandshakeAndControlFlow(t *testing.T) {
	validHandshake := websocketUpgradeRequest(http.MethodGet, "/ws", map[string]string{
		"Connection":            "upgrade",
		"Upgrade":               "websocket",
		"Sec-WebSocket-Key":     testWebSocketKey,
		"Sec-WebSocket-Version": "13",
	})
	tests := []struct {
		name          string
		next          []byte
		nextErr       error
		asyncWriteErr error
		autoCallback  bool
		maxPending    int
		wantMode      connMode
		wantQueueKind connEventKind
		wantClose     bool
		wantResponse  bool
	}{
		{name: "read error", nextErr: errors.New("read failed"), wantMode: connModeWSHandshake, wantQueueKind: connEventClose, wantClose: true},
		{name: "empty", wantMode: connModeWSHandshake},
		{name: "incomplete handshake", next: []byte("GET /ws HTTP/1.1\r\n"), wantMode: connModeWSHandshake},
		{name: "failed handshake", next: []byte("bad\r\n\r\n"), autoCallback: true, wantMode: connModeWSHandshake, wantClose: true, wantResponse: true},
		{name: "failed handshake write error", next: []byte("bad\r\n\r\n"), asyncWriteErr: errors.New("write failed"), wantMode: connModeWSHandshake, wantClose: true, wantResponse: true},
		{name: "successful handshake", next: validHandshake, wantMode: connModeWSFrames, wantQueueKind: connEventOpen, wantResponse: true},
		{name: "successful handshake write error", next: validHandshake, asyncWriteErr: errors.New("write failed"), wantMode: connModeWSFrames, wantClose: true, wantResponse: true},
		{name: "oversized raw input", next: bytes.Repeat([]byte{'x'}, wsMaxFrameHeaderBytes+5), maxPending: 4, wantMode: connModeWSHandshake, wantQueueKind: connEventClose, wantClose: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var reported error
			runtime := &listenerRuntime{
				opts: transport.ListenerOptions{
					Network: "websocket",
					Path:    "/ws",
					OnError: func(err error) { reported = err },
				},
				active: true,
			}
			raw := &contractGnetConn{
				nextErr:       tt.nextErr,
				asyncWriteErr: tt.asyncWriteErr,
				autoCallback:  tt.autoCallback,
			}
			raw.next = append([]byte(nil), tt.next...)
			state := &connState{
				raw:             raw,
				runtime:         runtime,
				mode:            connModeWSHandshake,
				maxPendingBytes: tt.maxPending,
			}
			raw.SetContext(state)
			group := &engineGroup{}
			if action := group.OnTraffic(raw); action != gnetv2.None {
				t.Fatalf("OnTraffic action = %v, want none", action)
			}
			if state.mode != tt.wantMode {
				t.Fatalf("mode = %v, want %v", state.mode, tt.wantMode)
			}
			if tt.wantQueueKind != 0 && (len(state.queue) == 0 || state.queue[len(state.queue)-1].kind != tt.wantQueueKind) {
				t.Fatalf("queue = %+v, want final kind %v", state.queue, tt.wantQueueKind)
			}
			if got := raw.closeCalls > 0; got != tt.wantClose {
				t.Fatalf("closed = %v, want %v", got, tt.wantClose)
			}
			if got := len(raw.lastAsyncWrite) > 0; got != tt.wantResponse {
				t.Fatalf("response written = %v, want %v", got, tt.wantResponse)
			}
			if strings.HasPrefix(tt.name, "failed handshake") && reported == nil {
				t.Fatal("handshake failure was not reported")
			}
		})
	}
}

func TestEngineGroupWebSocketTrafficWritesControlAndClosesOnWriteFailure(t *testing.T) {
	ping := encodeMaskedTestWSFrame(t, true, wsOpcodePing, [4]byte{1, 2, 3, 4}, []byte("health"))
	for _, tt := range []struct {
		name          string
		asyncWriteErr error
		wantClose     bool
		wantQueue     bool
	}{
		{name: "pong written"},
		{name: "pong write failed", asyncWriteErr: errors.New("write failed"), wantClose: true, wantQueue: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runtime := &listenerRuntime{active: true}
			raw := &contractGnetConn{asyncWriteErr: tt.asyncWriteErr}
			raw.next = append([]byte(nil), ping...)
			state := &connState{raw: raw, runtime: runtime, mode: connModeWSFrames}
			raw.SetContext(state)
			if action := (&engineGroup{}).OnTraffic(raw); action != gnetv2.None {
				t.Fatalf("OnTraffic action = %v, want none", action)
			}
			if len(raw.lastAsyncWrite) == 0 {
				t.Fatal("ping did not produce pong write")
			}
			frame, _, err := decodeWSFrame(raw.lastAsyncWrite)
			if err != nil || frame.opcode != wsOpcodePong {
				t.Fatalf("pong frame = %+v, %v", frame, err)
			}
			if got := raw.closeCalls > 0; got != tt.wantClose {
				t.Fatalf("closed = %v, want %v", got, tt.wantClose)
			}
			if got := len(state.queue) > 0; got != tt.wantQueue {
				t.Fatalf("queued close = %v, want %v", got, tt.wantQueue)
			}
		})
	}
}
