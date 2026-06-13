package wkproto

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	protocolenc "github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

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
