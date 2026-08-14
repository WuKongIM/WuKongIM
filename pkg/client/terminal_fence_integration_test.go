//go:build integration

package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	accessgateway "github.com/WuKongIM/WuKongIM/internal/access/gateway"
	"github.com/WuKongIM/WuKongIM/internal/usecase/benchterminal"
	pkgateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const terminalFenceIntegrationTimeout = 3 * time.Second

func TestTerminalFenceRealTCPOrdersPriorRecvBeforeMatchingAck(t *testing.T) {
	fixture := newTerminalFenceTCPFixture(t, terminalFenceReplyMatchingACK)

	first := terminalFenceRecv(1, "before-fence-1")
	if err := fixture.opened.WriteFrame(first); err != nil {
		t.Fatalf("write first RECV: %v", err)
	}
	waitForTerminalInboundSnapshot(t, fixture.client, func(snapshot InboundQueueSnapshot) bool {
		return snapshot.Depth == 1 && snapshot.Handoffs == 0
	}, "first RECV queued")

	fixture.handler.setPriorRecv(terminalFenceRecv(2, "before-fence-2"))
	sealDone := make(chan error, 1)
	sealCtx, cancelSeal := context.WithTimeout(context.Background(), terminalFenceIntegrationTimeout)
	defer cancelSeal()
	go func() {
		sealDone <- fixture.client.SealIngressWithFence(sealCtx, fixture.grant)
	}()

	waitForTerminalRequest(t, fixture.handler)
	waitForTerminalInboundSnapshot(t, fixture.client, func(snapshot InboundQueueSnapshot) bool {
		return snapshot.Depth == 1 && snapshot.Handoffs == 1
	}, "reader blocked publishing the second pre-fence RECV")
	select {
	case err := <-sealDone:
		t.Fatalf("terminal fence completed while its reader was blocked before the ACK: %v", err)
	default:
	}

	assertTerminalRecv(t, fixture.client, 1, "before-fence-1")
	select {
	case err := <-sealDone:
		if err != nil {
			t.Fatalf("SealIngressWithFence() error: %v", err)
		}
	case <-time.After(terminalFenceIntegrationTimeout):
		t.Fatal("timed out waiting for matching terminal ACK")
	}

	snapshot := fixture.client.InboundQueueSnapshot()
	if snapshot.Depth != 1 || snapshot.Handoffs != 0 {
		t.Fatalf("snapshot after matching ACK = %+v, want second pre-fence RECV decoded and queued", snapshot)
	}
	assertTerminalRecv(t, fixture.client, 2, "before-fence-2")

	fixture.closeAndAssertNoOrphans(t)
}

func TestTerminalFenceRealTCPFailsClosedOnMalformedAck(t *testing.T) {
	fixture := newTerminalFenceTCPFixture(t, terminalFenceReplyMalformedACK)

	ctx, cancel := context.WithTimeout(context.Background(), terminalFenceIntegrationTimeout)
	defer cancel()
	err := fixture.client.SealIngressWithFence(ctx, fixture.grant)
	if !errors.Is(err, ErrTerminalFenceProtocol) {
		t.Fatalf("SealIngressWithFence() error = %v, want %v", err, ErrTerminalFenceProtocol)
	}
	waitForTerminalRequest(t, fixture.handler)

	fixture.closeAndAssertNoOrphans(t)
}

func TestTerminalFenceRealTCPFailsClosedOnTruncatedAckEOF(t *testing.T) {
	fixture := newTerminalFenceTCPFixture(t, terminalFenceReplyTruncatedACK)

	ctx, cancel := context.WithTimeout(context.Background(), terminalFenceIntegrationTimeout)
	defer cancel()
	err := fixture.client.SealIngressWithFence(ctx, fixture.grant)
	if !errors.Is(err, ErrTerminalFenceProtocol) ||
		!strings.Contains(err.Error(), "truncated WKProto frame") ||
		!strings.Contains(err.Error(), io.ErrUnexpectedEOF.Error()) {
		t.Fatalf("SealIngressWithFence() error = %v, want fail-closed truncated-frame EOF evidence", err)
	}
	waitForTerminalRequest(t, fixture.handler)
	if fixture.proxy == nil {
		t.Fatal("truncated ACK fixture has no TCP proxy")
	}
	waitForTerminalDone(t, fixture.proxy.truncated, "TCP terminal ACK truncation")

	fixture.closeAndAssertNoOrphans(t)
}

func TestTerminalFenceRealTCPCallerTimeoutCleansUpWithoutOrphans(t *testing.T) {
	fixture := newTerminalFenceTCPFixture(t, terminalFenceReplySilent)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := fixture.client.SealIngressWithFence(ctx, fixture.grant)
	elapsed := time.Since(started)
	if !errors.Is(err, ErrTerminalFenceProtocol) || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("SealIngressWithFence() error = %v, want fail-closed deadline evidence", err)
	}
	if elapsed < 50*time.Millisecond || elapsed > time.Second {
		t.Fatalf("SealIngressWithFence() elapsed = %s, want bounded caller timeout", elapsed)
	}
	waitForTerminalRequest(t, fixture.handler)

	fixture.closeAndAssertNoOrphans(t)
}

type terminalFenceReplyMode uint8

const (
	terminalFenceReplyMatchingACK terminalFenceReplyMode = iota
	terminalFenceReplyMalformedACK
	terminalFenceReplyTruncatedACK
	terminalFenceReplySilent
)

type terminalFenceTCPFixture struct {
	client  *Client
	gateway *pkgateway.Gateway
	handler *terminalFenceTransportHandler
	opened  pkgateway.Context
	grant   frame.TerminalFenceGrant
	proxy   *terminalFenceTruncatingProxy
	closed  bool
}

func newTerminalFenceTCPFixture(t *testing.T, mode terminalFenceReplyMode) *terminalFenceTCPFixture {
	t.Helper()

	inner := accessgateway.New(accessgateway.Options{})
	handler := &terminalFenceTransportHandler{
		Handler:     inner,
		mode:        mode,
		opened:      make(chan pkgateway.Context, 1),
		requestSeen: make(chan struct{}),
	}
	gw, err := pkgateway.New(pkgateway.Options{
		Handler: handler,
		Authenticator: pkgateway.NewWKProtoAuthenticator(pkgateway.WKProtoAuthOptions{
			DisableEncryption: true,
		}),
		Listeners: []pkgateway.ListenerOptions{
			binding.TCPWKProto("terminal-fence-integration", "127.0.0.1:0"),
		},
	})
	if err != nil {
		t.Fatalf("gateway.New(): %v", err)
	}
	controller := benchterminal.New(benchterminal.Options{
		Gateway:       gw,
		ChannelAppend: terminalFenceNoopStopper{},
		Delivery:      terminalFenceNoopQuiescer{},
		Reader:        bytes.NewReader(bytes.Repeat([]byte{1}, 40)),
		MaxSessions:   1,
		DrainTimeout:  time.Second,
	})
	if !inner.BindBenchTerminalFence(controller) {
		t.Fatal("BindBenchTerminalFence() = false, want first binding")
	}
	if err := gw.Start(); err != nil {
		t.Fatalf("gateway.Start(): %v", err)
	}
	clientAddr := gw.ListenerAddr("terminal-fence-integration")
	var proxy *terminalFenceTruncatingProxy
	if mode == terminalFenceReplyTruncatedACK {
		proxy = newTerminalFenceTruncatingProxy(t, clientAddr, handler.requestSeen)
		clientAddr = proxy.addr()
	}

	client, err := New(Config{
		Addr:                   clientAddr,
		OperationTimeout:       time.Second,
		ReadBufferSize:         4096,
		InboundFrameBufferSize: 1,
	})
	if err != nil {
		if proxy != nil {
			proxy.close()
		}
		_ = gw.Stop()
		t.Fatalf("client.New(): %v", err)
	}
	connectCtx, cancelConnect := context.WithTimeout(context.Background(), terminalFenceIntegrationTimeout)
	_, err = client.Connect(connectCtx, ConnectOptions{
		UID:        "terminal-fence-user",
		DeviceID:   "terminal-fence-device",
		DeviceFlag: frame.APP,
	})
	cancelConnect()
	if err != nil {
		_ = client.Close()
		if proxy != nil {
			proxy.close()
		}
		_ = gw.Stop()
		t.Fatalf("client.Connect(): %v", err)
	}

	var opened pkgateway.Context
	select {
	case opened = <-handler.opened:
	case <-time.After(terminalFenceIntegrationTimeout):
		_ = client.Close()
		if proxy != nil {
			proxy.close()
		}
		_ = gw.Stop()
		t.Fatal("timed out waiting for gateway session open")
	}
	prepareCtx, cancelPrepare := context.WithTimeout(context.Background(), terminalFenceIntegrationTimeout)
	grant, err := controller.Prepare(prepareCtx, benchterminal.PrepareRequest{
		RunID:            "terminal-fence-integration-run",
		AssignmentID:     "terminal-fence-integration-assignment",
		ExpectedSessions: 1,
	})
	cancelPrepare()
	if err != nil {
		_ = client.Close()
		if proxy != nil {
			proxy.close()
		}
		_ = gw.Stop()
		t.Fatalf("terminal controller Prepare(): %v", err)
	}

	fixture := &terminalFenceTCPFixture{
		client:  client,
		gateway: gw,
		handler: handler,
		opened:  opened,
		proxy:   proxy,
		grant: frame.TerminalFenceGrant{
			Epoch:      grant.Epoch,
			Capability: grant.Capability,
		},
	}
	t.Cleanup(func() {
		if fixture.closed {
			return
		}
		_ = fixture.client.Close()
		if fixture.proxy != nil {
			fixture.proxy.close()
		}
		_ = fixture.gateway.Stop()
	})
	return fixture
}

func (f *terminalFenceTCPFixture) closeAndAssertNoOrphans(t *testing.T) {
	t.Helper()
	if f == nil || f.closed {
		return
	}

	f.client.mu.Lock()
	readerDone := f.client.readerDone
	writerDone := f.client.writerDone
	f.client.mu.Unlock()
	if err := f.client.Close(); err != nil && !errors.Is(err, ErrClosed) {
		t.Fatalf("client.Close(): %v", err)
	}
	waitForTerminalDone(t, readerDone, "client reader")
	waitForTerminalDone(t, writerDone, "client writer")
	if f.proxy != nil {
		f.proxy.close()
		waitForTerminalDone(t, f.proxy.done, "terminal truncating TCP proxy")
	}
	waitForTerminalGatewaySessions(t, f.gateway, 0)
	if err := f.gateway.Stop(); err != nil {
		t.Fatalf("gateway.Stop(): %v", err)
	}
	if got := f.gateway.SessionSummary().GatewaySessions; got != 0 {
		t.Fatalf("gateway sessions after Stop = %d, want 0", got)
	}
	f.closed = true
}

type terminalFenceTransportHandler struct {
	*accessgateway.Handler
	mode terminalFenceReplyMode

	opened      chan pkgateway.Context
	requestSeen chan struct{}
	requestOnce sync.Once
	priorRecvMu sync.Mutex
	priorRecv   *frame.RecvPacket
}

func (h *terminalFenceTransportHandler) OnSessionActivate(*pkgateway.Context) (*frame.ConnackPacket, error) {
	return nil, nil
}

func (h *terminalFenceTransportHandler) OnSessionOpen(ctx pkgateway.Context) error {
	select {
	case h.opened <- ctx:
	default:
	}
	return h.Handler.OnSessionOpen(ctx)
}

func (h *terminalFenceTransportHandler) OnFrame(ctx pkgateway.Context, f frame.Frame) error {
	event, ok := f.(*frame.EventPacket)
	if !ok || event == nil || event.Type != frame.TerminalFenceEventType {
		return h.Handler.OnFrame(ctx, f)
	}
	h.requestOnce.Do(func() { close(h.requestSeen) })
	switch h.mode {
	case terminalFenceReplyMatchingACK, terminalFenceReplyTruncatedACK:
		h.priorRecvMu.Lock()
		priorRecv := h.priorRecv
		h.priorRecvMu.Unlock()
		if priorRecv != nil {
			if err := ctx.WriteFrame(priorRecv); err != nil {
				return err
			}
		}
		return h.Handler.OnFrame(ctx, f)
	case terminalFenceReplyMalformedACK:
		return ctx.WriteFrame(&frame.EventPacket{
			Type: frame.TerminalFenceAckEventType,
			Data: []byte{frame.TerminalFenceVersion},
		})
	case terminalFenceReplySilent:
		return nil
	default:
		return errors.New("unknown terminal fence integration reply mode")
	}
}

func (h *terminalFenceTransportHandler) setPriorRecv(recv *frame.RecvPacket) {
	h.priorRecvMu.Lock()
	h.priorRecv = recv
	h.priorRecvMu.Unlock()
}

type terminalFenceTruncatingProxy struct {
	listener    net.Listener
	target      string
	requestSeen <-chan struct{}
	done        chan struct{}
	truncated   chan struct{}
	closeOnce   sync.Once
	mu          sync.Mutex
	clientConn  net.Conn
	targetConn  net.Conn
	closed      bool
}

func newTerminalFenceTruncatingProxy(t *testing.T, target string, requestSeen <-chan struct{}) *terminalFenceTruncatingProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for terminal truncating proxy: %v", err)
	}
	proxy := &terminalFenceTruncatingProxy{
		listener:    listener,
		target:      target,
		requestSeen: requestSeen,
		done:        make(chan struct{}),
		truncated:   make(chan struct{}),
	}
	go proxy.run()
	return proxy
}

func (p *terminalFenceTruncatingProxy) addr() string {
	if p == nil || p.listener == nil {
		return ""
	}
	return p.listener.Addr().String()
}

func (p *terminalFenceTruncatingProxy) run() {
	defer close(p.done)
	clientConn, err := p.listener.Accept()
	if err != nil {
		return
	}
	targetConn, err := net.DialTimeout("tcp", p.target, terminalFenceIntegrationTimeout)
	if err != nil {
		_ = clientConn.Close()
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = clientConn.Close()
		_ = targetConn.Close()
		return
	}
	p.clientConn = clientConn
	p.targetConn = targetConn
	p.mu.Unlock()

	upstreamDone := make(chan struct{})
	go func() {
		defer close(upstreamDone)
		_, _ = io.Copy(targetConn, clientConn)
	}()

	p.forwardServerUntilTerminalACKPrefix(clientConn, targetConn)
	_ = clientConn.Close()
	_ = targetConn.Close()
	<-upstreamDone
}

func (p *terminalFenceTruncatingProxy) forwardServerUntilTerminalACKPrefix(clientConn, targetConn net.Conn) {
	buffer := make([]byte, 4096)
	for {
		count, err := targetConn.Read(buffer)
		if count > 0 {
			terminalRequestSeen := false
			select {
			case <-p.requestSeen:
				terminalRequestSeen = true
			default:
			}
			if terminalRequestSeen {
				prefixLength := count - 1
				if prefixLength > 0 {
					_ = writeAllTerminalProxy(clientConn, buffer[:prefixLength])
				}
				close(p.truncated)
				return
			}
			if writeErr := writeAllTerminalProxy(clientConn, buffer[:count]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (p *terminalFenceTruncatingProxy) close() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() {
		if p.listener != nil {
			_ = p.listener.Close()
		}
		p.mu.Lock()
		p.closed = true
		clientConn := p.clientConn
		targetConn := p.targetConn
		p.mu.Unlock()
		if clientConn != nil {
			_ = clientConn.Close()
		}
		if targetConn != nil {
			_ = targetConn.Close()
		}
	})
}

func writeAllTerminalProxy(conn net.Conn, payload []byte) error {
	for len(payload) > 0 {
		written, err := conn.Write(payload)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}

type terminalFenceNoopStopper struct{}

func (terminalFenceNoopStopper) Stop(context.Context) error { return nil }

type terminalFenceNoopQuiescer struct{}

func (terminalFenceNoopQuiescer) Quiesce(context.Context) error { return nil }

func terminalFenceRecv(sequence uint64, payload string) *frame.RecvPacket {
	return &frame.RecvPacket{
		Setting:     frame.SettingNoEncrypt,
		MessageID:   int64(sequence),
		MessageSeq:  sequence,
		ClientMsgNo: "terminal-fence-recv",
		Timestamp:   1,
		ChannelID:   "terminal-fence-channel",
		ChannelType: frame.ChannelTypeGroup,
		FromUID:     "terminal-fence-sender",
		Payload:     []byte(payload),
	}
}

func assertTerminalRecv(t *testing.T, client *Client, sequence uint64, payload string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), terminalFenceIntegrationTimeout)
	defer cancel()
	f, err := client.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame(): %v", err)
	}
	recv, ok := f.(*frame.RecvPacket)
	if !ok {
		t.Fatalf("ReadFrame() type = %T, want *frame.RecvPacket", f)
	}
	if recv.MessageSeq != sequence || string(recv.Payload) != payload {
		t.Fatalf("RECV = {seq:%d payload:%q}, want {seq:%d payload:%q}", recv.MessageSeq, recv.Payload, sequence, payload)
	}
}

func waitForTerminalRequest(t *testing.T, handler *terminalFenceTransportHandler) {
	t.Helper()
	select {
	case <-handler.requestSeen:
	case <-time.After(terminalFenceIntegrationTimeout):
		t.Fatal("timed out waiting for terminal EVENT at gateway handler")
	}
}

func waitForTerminalInboundSnapshot(t *testing.T, client *Client, ready func(InboundQueueSnapshot) bool, description string) {
	t.Helper()
	deadline := time.Now().Add(terminalFenceIntegrationTimeout)
	for time.Now().Before(deadline) {
		if snapshot := client.InboundQueueSnapshot(); ready(snapshot) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; final snapshot = %+v", description, client.InboundQueueSnapshot())
}

func waitForTerminalDone(t *testing.T, done <-chan struct{}, owner string) {
	t.Helper()
	if done == nil {
		t.Fatalf("%s completion channel is nil", owner)
	}
	select {
	case <-done:
	case <-time.After(terminalFenceIntegrationTimeout):
		t.Fatalf("timed out waiting for %s goroutine cleanup", owner)
	}
}

func waitForTerminalGatewaySessions(t *testing.T, gw *pkgateway.Gateway, want int) {
	t.Helper()
	deadline := time.Now().Add(terminalFenceIntegrationTimeout)
	for time.Now().Before(deadline) {
		if gw.SessionSummary().GatewaySessions == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("gateway sessions = %d, want %d", gw.SessionSummary().GatewaySessions, want)
}
