package wkproto

import (
	"context"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	protocolenc "github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

func TestClientInnerConfigUsesExactCapacities(t *testing.T) {
	client, err := NewClient(ClientConfig{
		Addr:              "127.0.0.1:5100",
		SendQueueCapacity: 16,
		MaxInflight:       1,
		ReadBufferSize:    1024,
		FrameBufferSize:   4,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	cfg := client.innerConfig()

	if cfg.SendQueueCapacity != 16 || cfg.MaxInflight != 1 || cfg.ReadBufferSize != 1024 || cfg.InboundFrameBufferSize != 4 {
		t.Fatalf("inner config = %#v, want 16/1/1024/4", cfg)
	}
	session := newClientSession(&wkclient.Client{}, client.frameBufferSize)
	if got := cap(session.frameCh); got != 4 {
		t.Fatalf("adapter frame queue capacity = %d, want 4", got)
	}
}

func TestClientConnectSendsConnectPacketAndAcceptsConnack(t *testing.T) {
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
		f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode connect: %v", err)
		}
		connect, ok := f.(*frame.ConnectPacket)
		if !ok {
			t.Fatalf("first frame = %T, want *frame.ConnectPacket", f)
		}
		if connect.Version != frame.LatestVersion {
			t.Fatalf("connect.Version = %d, want %d", connect.Version, frame.LatestVersion)
		}
		if connect.UID != "u1" || connect.DeviceID != "d1" || connect.DeviceFlag != frame.APP {
			t.Fatalf("connect identity = uid %q device %q flag %d", connect.UID, connect.DeviceID, connect.DeviceFlag)
		}
		if connect.ClientTimestamp <= 0 {
			t.Fatal("connect.ClientTimestamp was not set")
		}
		if connect.ClientKey == "" {
			t.Fatal("connect.ClientKey is empty")
		}
		if connect.Token != "auth-token" {
			t.Fatalf("connect.Token = %q, want %q", connect.Token, "auth-token")
		}
		writeFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})
	})
	defer server.close()

	client, err := NewClient(ClientConfig{Addr: server.addr, Token: "auth-token", OperationTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Connect(ctx, "u1", "d1"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
}

func TestClientEncryptsSendDecryptsRecvAndWritesRecvAck(t *testing.T) {
	serverDone := make(chan struct{})
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
		defer close(serverDone)

		f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode connect: %v", err)
		}
		connect := f.(*frame.ConnectPacket)
		serverKeys, serverKey, err := protocolenc.NegotiateServerSession(connect.ClientKey)
		if err != nil {
			t.Fatalf("NegotiateServerSession() error = %v", err)
		}
		writeFrame(t, conn, &frame.ConnackPacket{
			ReasonCode:    frame.ReasonSuccess,
			ServerVersion: frame.LatestVersion,
			ServerKey:     serverKey,
			Salt:          string(serverKeys.AESIV),
		})

		f, err = codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode send: %v", err)
		}
		send, ok := f.(*frame.SendPacket)
		if !ok {
			t.Fatalf("second frame = %T, want *frame.SendPacket", f)
		}
		if got := string(send.Payload); got == "hello" {
			t.Fatalf("send payload was not encrypted: %q", got)
		}
		if send.MsgKey == "" {
			t.Fatal("send MsgKey is empty")
		}
		if err := protocolenc.ValidateSendPacket(send, serverKeys); err != nil {
			t.Fatalf("ValidateSendPacket() error = %v", err)
		}
		plain, err := protocolenc.DecryptPayload(send.Payload, serverKeys)
		if err != nil {
			t.Fatalf("DecryptPayload() error = %v", err)
		}
		if got, want := string(plain), "hello"; got != want {
			t.Fatalf("send plaintext = %q, want %q", got, want)
		}

		recv, err := protocolenc.SealRecvPacket(&frame.RecvPacket{
			MessageID:   99,
			MessageSeq:  7,
			ClientMsgNo: "m1",
			Timestamp:   123,
			FromUID:     "u2",
			ChannelID:   "u1",
			ChannelType: frame.ChannelTypePerson,
			Payload:     []byte("welcome"),
		}, serverKeys)
		if err != nil {
			t.Fatalf("SealRecvPacket() error = %v", err)
		}
		writeFrame(t, conn, recv)

		f, err = codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode recvack: %v", err)
		}
		ack, ok := f.(*frame.RecvackPacket)
		if !ok {
			t.Fatalf("third frame = %T, want *frame.RecvackPacket", f)
		}
		if ack.MessageID != 99 || ack.MessageSeq != 7 {
			t.Fatalf("recvack = (%d,%d), want (99,7)", ack.MessageID, ack.MessageSeq)
		}
	})
	defer server.close()

	client, err := NewClient(ClientConfig{Addr: server.addr, OperationTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Connect(ctx, "u1", "d1"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := client.Send(ctx, &frame.SendPacket{
		ClientSeq:   1,
		ClientMsgNo: "c1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello"),
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	f, err := client.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	recv, ok := f.(*frame.RecvPacket)
	if !ok {
		t.Fatalf("ReadFrame() = %T, want *frame.RecvPacket", f)
	}
	if got, want := string(recv.Payload), "welcome"; got != want {
		t.Fatalf("recv payload = %q, want %q", got, want)
	}
	if err := client.RecvAck(ctx, recv.MessageID, recv.MessageSeq); err != nil {
		t.Fatalf("RecvAck() error = %v", err)
	}

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("server did not observe recvack")
	}
}

func TestClientConcurrentSendAndRecvAckWriteFrames(t *testing.T) {
	framesCh := make(chan []frame.Frame, 1)
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
		f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode connect: %v", err)
		}
		if _, ok := f.(*frame.ConnectPacket); !ok {
			t.Fatalf("first frame = %T, want *frame.ConnectPacket", f)
		}
		writeFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})

		frames := make([]frame.Frame, 0, 2)
		for len(frames) < 2 {
			if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatalf("SetReadDeadline() error = %v", err)
			}
			f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
			if err != nil {
				t.Fatalf("decode client frame: %v", err)
			}
			frames = append(frames, f)
		}
		framesCh <- frames
	})
	defer server.close()

	client, err := NewClient(ClientConfig{Addr: server.addr, OperationTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Connect(ctx, "u1", "d1"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		errs <- client.Send(ctx, &frame.SendPacket{
			ClientSeq:   1,
			ClientMsgNo: "c1",
			ChannelID:   "u2",
			ChannelType: frame.ChannelTypePerson,
			Payload:     []byte("hello"),
		})
	}()
	go func() {
		defer wg.Done()
		<-start
		errs <- client.RecvAck(ctx, 99, 7)
	}()
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent write error = %v", err)
		}
	}

	frames := <-framesCh
	seen := map[frame.FrameType]bool{}
	for _, f := range frames {
		seen[f.GetFrameType()] = true
	}
	if !seen[frame.SEND] || !seen[frame.RECVACK] {
		t.Fatalf("decoded frame types = %#v, want SEND and RECVACK", seen)
	}
}

func TestClientSendExposesSendackThroughReadFrame(t *testing.T) {
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
		f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode connect: %v", err)
		}
		if _, ok := f.(*frame.ConnectPacket); !ok {
			t.Fatalf("first frame = %T, want *frame.ConnectPacket", f)
		}
		writeFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})

		f, err = codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode send: %v", err)
		}
		send, ok := f.(*frame.SendPacket)
		if !ok {
			t.Fatalf("second frame = %T, want *frame.SendPacket", f)
		}
		writeFrame(t, conn, &frame.SendackPacket{
			ClientSeq:   send.ClientSeq,
			ClientMsgNo: send.ClientMsgNo,
			MessageID:   21,
			MessageSeq:  22,
			ReasonCode:  frame.ReasonSuccess,
		})
	})
	defer server.close()

	client, err := NewClient(ClientConfig{Addr: server.addr, OperationTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Connect(ctx, "u1", "d1"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := client.Send(ctx, &frame.SendPacket{
		ClientSeq:   13,
		ClientMsgNo: "client-13",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello"),
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	f, err := client.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	ack, ok := f.(*frame.SendackPacket)
	if !ok {
		t.Fatalf("ReadFrame() = %T, want *frame.SendackPacket", f)
	}
	if ack.ClientSeq != 13 || ack.ClientMsgNo != "client-13" || ack.MessageID != 21 || ack.MessageSeq != 22 {
		t.Fatalf("sendack = %#v, want client seq 13 message 21/22", ack)
	}
}

func TestClientAckTimeoutCanExceedOperationTimeout(t *testing.T) {
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
		f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode connect: %v", err)
		}
		if _, ok := f.(*frame.ConnectPacket); !ok {
			t.Fatalf("first frame = %T, want *frame.ConnectPacket", f)
		}
		writeFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})

		f, err = codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode send: %v", err)
		}
		send, ok := f.(*frame.SendPacket)
		if !ok {
			t.Fatalf("second frame = %T, want *frame.SendPacket", f)
		}
		time.Sleep(60 * time.Millisecond)
		writeFrame(t, conn, &frame.SendackPacket{
			ClientSeq:   send.ClientSeq,
			ClientMsgNo: send.ClientMsgNo,
			MessageID:   21,
			MessageSeq:  22,
			ReasonCode:  frame.ReasonSuccess,
		})
	})
	defer server.close()

	client, err := NewClient(ClientConfig{
		Addr:             server.addr,
		OperationTimeout: 20 * time.Millisecond,
		AckTimeout:       200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Connect(ctx, "u1", "d1"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := client.Send(ctx, &frame.SendPacket{
		ClientSeq:   13,
		ClientMsgNo: "client-13",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello"),
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	f, err := client.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if _, ok := f.(*frame.SendackPacket); !ok {
		t.Fatalf("ReadFrame() = %T, want *frame.SendackPacket", f)
	}
}

func TestClientReadFrameReportsRecvDecryptContext(t *testing.T) {
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
		f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode connect: %v", err)
		}
		connect := f.(*frame.ConnectPacket)
		serverKeys, serverKey, err := protocolenc.NegotiateServerSession(connect.ClientKey)
		if err != nil {
			t.Fatalf("NegotiateServerSession() error = %v", err)
		}
		writeFrame(t, conn, &frame.ConnackPacket{
			ReasonCode:    frame.ReasonSuccess,
			ServerVersion: frame.LatestVersion,
			ServerKey:     serverKey,
			Salt:          string(serverKeys.AESIV),
		})
		writeFrame(t, conn, &frame.RecvPacket{
			MessageID:   99,
			MessageSeq:  7,
			ClientMsgNo: "m1",
			FromUID:     "u2",
			ChannelID:   "g1",
			ChannelType: frame.ChannelTypeGroup,
			Payload:     []byte("plain-payload"),
		})
	})
	defer server.close()

	client, err := NewClient(ClientConfig{Addr: server.addr, OperationTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Connect(ctx, "u1", "d1"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	_, err = client.ReadFrame(ctx)
	if err == nil {
		t.Fatal("ReadFrame() error = nil, want decrypt context")
	}
	msg := err.Error()
	for _, want := range []string{
		"decrypt recv payload",
		`channel_id="g1"`,
		"channel_type=2",
		`client_msg_no="m1"`,
		"msg_key_empty=true",
		`payload_prefix="plain-payload"`,
		"illegal base64",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("ReadFrame() error %q missing %q", msg, want)
		}
	}
}

func TestClientReconnectDoesNotLetOldReaderConsumeNewSessionFrames(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	releaseFirst := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)

		first, err := ln.Accept()
		if err != nil {
			return
		}
		defer first.Close()
		if _, err := codec.New().DecodePacketWithConn(first, frame.LatestVersion); err != nil {
			t.Errorf("decode first connect: %v", err)
			return
		}
		writeFrame(t, first, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})

		second, err := ln.Accept()
		if err != nil {
			t.Errorf("accept second connection: %v", err)
			return
		}
		defer second.Close()
		if _, err := codec.New().DecodePacketWithConn(second, frame.LatestVersion); err != nil {
			t.Errorf("decode second connect: %v", err)
			return
		}
		writeFrame(t, second, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})
		writeFrame(t, second, &frame.RecvPacket{
			MessageID:   31,
			MessageSeq:  32,
			ChannelID:   "u1",
			ChannelType: frame.ChannelTypePerson,
			FromUID:     "u2",
			Payload:     []byte("second session"),
		})
		<-releaseFirst
	}()

	client, err := NewClient(ClientConfig{Addr: ln.Addr().String(), OperationTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()
	defer func() { <-serverDone }()
	defer close(releaseFirst)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Connect(ctx, "u1", "d1"); err != nil {
		t.Fatalf("first Connect() error = %v", err)
	}
	if err := client.Connect(ctx, "u1", "d1-reconnect"); err != nil {
		t.Fatalf("second Connect() error = %v", err)
	}

	f, err := client.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	recv, ok := f.(*frame.RecvPacket)
	if !ok {
		t.Fatalf("ReadFrame() = %T, want *frame.RecvPacket", f)
	}
	if got, want := string(recv.Payload), "second session"; got != want {
		t.Fatalf("recv payload = %q, want %q", got, want)
	}
}

func TestClientPublishFrameResultDoesNotBlockWhenQueueFull(t *testing.T) {
	client := &Client{}
	frameCh := make(chan frameResult, 1)
	frameCh <- frameResult{frame: &frame.RecvPacket{MessageID: 1}}
	stopCh := make(chan struct{})

	done := make(chan struct{})
	go func() {
		client.publishFrameResult(frameCh, stopCh, frameResult{err: io.EOF})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("publishFrameResult blocked behind a full queue")
	}

	select {
	case result := <-frameCh:
		if result.err != io.EOF {
			t.Fatalf("published result error = %v, want %v", result.err, io.EOF)
		}
	default:
		t.Fatal("frameCh is empty after publish")
	}
}

func TestClientPublishFrameResultKeepsQueuedSendackWhenRecvArrivesFull(t *testing.T) {
	client := &Client{}
	frameCh := make(chan frameResult, 1)
	frameCh <- frameResult{frame: &frame.SendackPacket{ClientSeq: 7}}
	stopCh := make(chan struct{})

	client.publishFrameResult(frameCh, stopCh, frameResult{frame: &frame.RecvPacket{MessageID: 1}})

	result := <-frameCh
	ack, ok := result.frame.(*frame.SendackPacket)
	if !ok {
		t.Fatalf("published frame = %T, want *frame.SendackPacket", result.frame)
	}
	if ack.ClientSeq != 7 {
		t.Fatalf("sendack client seq = %d, want 7", ack.ClientSeq)
	}
}

func TestClientPublishFrameResultEvictsRecvForSendack(t *testing.T) {
	client := &Client{}
	frameCh := make(chan frameResult, 1)
	frameCh <- frameResult{frame: &frame.RecvPacket{MessageID: 1}}
	stopCh := make(chan struct{})

	client.publishFrameResult(frameCh, stopCh, frameResult{frame: &frame.SendackPacket{ClientSeq: 7}})

	result := <-frameCh
	ack, ok := result.frame.(*frame.SendackPacket)
	if !ok {
		t.Fatalf("published frame = %T, want *frame.SendackPacket", result.frame)
	}
	if ack.ClientSeq != 7 {
		t.Fatalf("sendack client seq = %d, want 7", ack.ClientSeq)
	}
}

func TestClientSessionPublishKeepsConcurrentPriorityResults(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(oldProcs)

	const queueSize = 20000
	session := &clientSession{
		frameCh: make(chan frameResult, queueSize),
		stopCh:  make(chan struct{}),
	}
	for i := 0; i < queueSize; i++ {
		session.frameCh <- frameResult{frame: &frame.RecvPacket{MessageID: int64(i + 1)}}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		session.publishFrameResult(frameResult{frame: &frame.SendackPacket{ClientSeq: 1}})
	}()

	deadline := time.After(time.Second)
	for len(session.frameCh) == queueSize {
		select {
		case <-deadline:
			t.Fatal("first publisher did not start draining")
		default:
			runtime.Gosched()
		}
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		session.publishFrameResult(frameResult{frame: &frame.SendackPacket{ClientSeq: 2}})
	}()
	wg.Wait()

	seen := map[uint64]bool{}
	for len(session.frameCh) > 0 {
		result := <-session.frameCh
		if ack, ok := result.frame.(*frame.SendackPacket); ok {
			seen[ack.ClientSeq] = true
		}
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("queued sendacks = %#v, want client seqs 1 and 2", seen)
	}
}

func TestClientMethodsReportNotConnectedForNilClient(t *testing.T) {
	var client *Client
	if err := client.Send(context.Background(), &frame.SendPacket{}); err == nil {
		t.Fatal("Send() error = nil, want not connected")
	}
	if _, err := client.ReadFrame(context.Background()); err == nil {
		t.Fatal("ReadFrame() error = nil, want not connected")
	}
	if err := client.RecvAck(context.Background(), 1, 1); err == nil {
		t.Fatal("RecvAck() error = nil, want not connected")
	}
}

type fakeWKProtoServer struct {
	addr string
	ln   net.Listener
	done chan struct{}
}

func newFakeWKProtoServer(t *testing.T, serve func(*testing.T, net.Conn)) *fakeWKProtoServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeWKProtoServer{addr: ln.Addr().String(), ln: ln, done: make(chan struct{})}
	go func() {
		defer close(server.done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		serve(t, conn)
	}()
	return server
}

func (s *fakeWKProtoServer) close() {
	_ = s.ln.Close()
	<-s.done
}

func writeFrame(t *testing.T, conn net.Conn, f frame.Frame) {
	t.Helper()
	payload := mustEncodeFrame(t, f)
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write frame %T: %v", f, err)
	}
}

func mustEncodeFrame(t *testing.T, f frame.Frame) []byte {
	t.Helper()
	payload, err := codec.New().EncodeFrame(f, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame(%T): %v", f, err)
	}
	return payload
}
