package wkproto

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"runtime"
	"strconv"
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
	if cap(session.recvCh) != 4 || cap(session.sendackCh) != 4 || cap(session.errCh) != 4 {
		t.Fatalf("adapter queue capacities = recv:%d sendack:%d error:%d, want 4 each", cap(session.recvCh), cap(session.sendackCh), cap(session.errCh))
	}
	if cap(session.publicationPermit) != 4 {
		t.Fatalf("publication capacity = %d, want 4", cap(session.publicationPermit))
	}
}

func TestClientQueueSnapshotReportsBoundedAdapterState(t *testing.T) {
	client, err := NewClient(ClientConfig{
		Addr:            "127.0.0.1:5100",
		FrameBufferSize: 4,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	inner, err := wkclient.New(wkclient.Config{Addr: "127.0.0.1:5100", InboundFrameBufferSize: 4})
	if err != nil {
		t.Fatalf("wkclient.New() error = %v", err)
	}
	defer inner.Close()
	session := newClientSession(inner, client.frameBufferSize)
	for messageID := int64(1); messageID <= 4; messageID++ {
		session.recvCh <- &frame.RecvPacket{MessageID: messageID}
	}
	client.session = session

	snapshot := client.QueueSnapshot()

	if snapshot.InnerRecvCapacity != 4 {
		t.Fatalf("InnerRecvCapacity = %d, want 4", snapshot.InnerRecvCapacity)
	}
	if snapshot.InnerRecvDepth != 0 {
		t.Fatalf("InnerRecvDepth = %d, want 0", snapshot.InnerRecvDepth)
	}
	if snapshot.AdapterDepth != 4 || snapshot.AdapterCapacity != 12 {
		t.Fatalf("adapter queue = %d/%d, want 4/12", snapshot.AdapterDepth, snapshot.AdapterCapacity)
	}
	if snapshot.RecvDepth != 4 || snapshot.RecvCapacity != 4 {
		t.Fatalf("recv queue = %d/%d, want saturated 4/4", snapshot.RecvDepth, snapshot.RecvCapacity)
	}
	if snapshot.SendackDepth != 0 || snapshot.SendackCapacity != 4 {
		t.Fatalf("sendack queue = %d/%d, want 0/4", snapshot.SendackDepth, snapshot.SendackCapacity)
	}
	if snapshot.ErrorDepth != 0 || snapshot.ErrorCapacity != 4 {
		t.Fatalf("error queue = %d/%d, want 0/4", snapshot.ErrorDepth, snapshot.ErrorCapacity)
	}
	if snapshot.PublicationCurrent != 0 || snapshot.PublicationCapacity != 4 || snapshot.PublicationPeak != 0 || snapshot.PublicationBlocked != 0 {
		t.Fatalf("publication snapshot = current:%d capacity:%d peak:%d blocked:%d, want 0/4/0/0", snapshot.PublicationCurrent, snapshot.PublicationCapacity, snapshot.PublicationPeak, snapshot.PublicationBlocked)
	}
	typ := reflect.TypeOf(snapshot)
	for i := 0; i < typ.NumField(); i++ {
		switch typ.Field(i).Type.Kind() {
		case reflect.Chan, reflect.Map, reflect.Pointer, reflect.Slice:
			t.Fatalf("QueueSnapshot field %s exposes mutable state through %s", typ.Field(i).Name, typ.Field(i).Type)
		}
	}
}

func TestReadErrorKindDistinguishesAsyncSendFromTerminalSessionFailure(t *testing.T) {
	t.Parallel()
	session := newClientSession(nil, 2)
	nonTerminal := errors.New("async send failed")
	if !session.publishError(errorResult{err: nonTerminal, clientSeq: 17, clientMsgNo: "stable-message"}) {
		t.Fatal("publish non-terminal error = false")
	}
	if _, err := session.readFrame(context.Background()); !errors.Is(err, nonTerminal) {
		t.Fatalf("non-terminal ReadFrame error = %v", err)
	} else if info, ok := ReadErrorInfoOf(err); !ok || info.Kind != ReadErrorNonTerminal || info.ClientSeq != 17 || info.ClientMsgNo != "stable-message" {
		t.Fatalf("non-terminal ReadFrame info = %+v, %v", info, ok)
	}

	terminal := io.EOF
	if !session.publishError(errorResult{err: terminal, terminal: true}) {
		t.Fatal("publish terminal error = false")
	}
	if _, err := session.readFrame(context.Background()); !errors.Is(err, terminal) || ReadErrorKindOf(err) != ReadErrorTerminal {
		t.Fatalf("terminal ReadFrame error = %v kind=%v", err, ReadErrorKindOf(err))
	}
}

func TestClientReadFrameIgnoresShortOperationTimeoutUntilCallerCancels(t *testing.T) {
	client, err := NewClient(ClientConfig{
		Addr:             "127.0.0.1:5100",
		OperationTimeout: time.Nanosecond,
		FrameBufferSize:  1,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	client.session = newClientSession(nil, client.frameBufferSize)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, readErr := client.ReadFrame(ctx)
		result <- readErr
	}()

	select {
	case readErr := <-result:
		t.Fatalf("ReadFrame returned after operation timeout: %v", readErr)
	case <-time.After(20 * time.Millisecond):
	}

	cancel()
	select {
	case readErr := <-result:
		if !errors.Is(readErr, context.Canceled) {
			t.Fatalf("ReadFrame cancellation error = %v, want %v", readErr, context.Canceled)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadFrame did not return after caller cancellation")
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

func TestClientInterleavedReceivePressurePreservesEveryRecvInWireOrder(t *testing.T) {
	firstRecvWritten := make(chan struct{})
	releaseBurst := make(chan struct{})
	burstWritten := make(chan struct{})
	releaseServer := make(chan struct{})
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
		if _, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion); err != nil {
			t.Fatalf("decode connect: %v", err)
		}
		writeFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})

		sends := make([]*frame.SendPacket, 0, 2)
		for len(sends) < 2 {
			f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
			if err != nil {
				t.Fatalf("decode send: %v", err)
			}
			send, ok := f.(*frame.SendPacket)
			if !ok {
				t.Fatalf("client frame = %T, want *frame.SendPacket", f)
			}
			sends = append(sends, send)
		}

		writeFrame(t, conn, receivePressurePacket(1))
		close(firstRecvWritten)
		<-releaseBurst
		writeFrame(t, conn, receivePressureSendack(sends[0], 101))
		for seq := uint64(2); seq <= 5; seq++ {
			writeFrame(t, conn, receivePressurePacket(seq))
		}
		writeFrame(t, conn, receivePressureSendack(sends[1], 102))
		close(burstWritten)
		<-releaseServer
	})
	defer server.close()
	defer close(releaseServer)
	defer close(releaseBurst)

	client, err := NewClient(ClientConfig{
		Addr:             server.addr,
		OperationTimeout: time.Second,
		FrameBufferSize:  2,
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
	for seq := uint64(1); seq <= 2; seq++ {
		if err := client.Send(ctx, &frame.SendPacket{
			ClientSeq:   seq,
			ClientMsgNo: "pressure-" + strconv.FormatUint(seq, 10),
			ChannelID:   "u2",
			ChannelType: frame.ChannelTypePerson,
			Payload:     []byte("pressure"),
		}); err != nil {
			t.Fatalf("Send(%d) error = %v", seq, err)
		}
	}

	waitForSignal(t, firstRecvWritten, "first RECV write")
	waitForQueueSnapshot(t, client, func(snapshot QueueSnapshot) bool {
		return snapshot.AdapterDepth == 1
	})
	releaseBurst <- struct{}{}
	waitForSignal(t, burstWritten, "interleaved pressure burst")
	session, err := client.currentSession()
	if err != nil {
		t.Fatalf("currentSession() error = %v", err)
	}
	pendingDone := make(chan bool, 1)
	go func() {
		pendingDone <- session.waitPendingSendacks()
	}()
	select {
	case settled := <-pendingDone:
		if !settled {
			t.Fatal("pending SENDACK publishers stopped before pressure burst settled")
		}
	case <-ctx.Done():
		t.Fatalf("pending SENDACK publishers did not settle: %v", ctx.Err())
	}

	wantRecvSeqs := []uint64{1, 2, 3, 4, 5}
	gotRecvSeqs := make([]uint64, 0, len(wantRecvSeqs))
	gotAcks := make(map[uint64]bool, 2)
	for len(gotRecvSeqs)+len(gotAcks) < len(wantRecvSeqs)+2 {
		f, err := client.ReadFrame(ctx)
		if err != nil {
			t.Fatalf("ReadFrame() after RECV pressure error = %v; recv seqs = %v acks = %v", err, gotRecvSeqs, gotAcks)
		}
		switch pkt := f.(type) {
		case *frame.RecvPacket:
			gotRecvSeqs = append(gotRecvSeqs, pkt.MessageSeq)
		case *frame.SendackPacket:
			if gotAcks[pkt.ClientSeq] {
				t.Fatalf("duplicate SENDACK client seq %d", pkt.ClientSeq)
			}
			gotAcks[pkt.ClientSeq] = true
		default:
			t.Fatalf("ReadFrame() = %T, want RECV or SENDACK", f)
		}
	}
	if len(gotRecvSeqs) != len(wantRecvSeqs) {
		t.Fatalf("RECV count = %d, want %d: %v", len(gotRecvSeqs), len(wantRecvSeqs), gotRecvSeqs)
	}
	for i, want := range wantRecvSeqs {
		if gotRecvSeqs[i] != want {
			t.Fatalf("RECV seqs = %v, want strict wire order %v", gotRecvSeqs, wantRecvSeqs)
		}
	}
}

func TestClientSendackPublicationPressureBoundsAdmission(t *testing.T) {
	sendsObserved := make(chan uint64, 3)
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
		if _, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion); err != nil {
			t.Fatalf("decode connect: %v", err)
		}
		writeFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})
		for {
			f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
			if err != nil {
				return
			}
			send, ok := f.(*frame.SendPacket)
			if !ok {
				t.Fatalf("client frame = %T, want *frame.SendPacket", f)
			}
			sendsObserved <- send.ClientSeq
			writeFrame(t, conn, receivePressureSendack(send, 300+int64(send.ClientSeq)))
		}
	})
	defer server.close()

	client, err := NewClient(ClientConfig{
		Addr:             server.addr,
		OperationTimeout: time.Second,
		FrameBufferSize:  1,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()
	connectCtx, cancelConnect := context.WithTimeout(context.Background(), time.Second)
	defer cancelConnect()
	if err := client.Connect(connectCtx, "u1", "d1"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	if err := client.Send(context.Background(), publicationPressureSend(1)); err != nil {
		t.Fatalf("Send(1) error = %v", err)
	}
	waitForObservedSend(t, sendsObserved, 1)
	waitForQueueSnapshot(t, client, func(snapshot QueueSnapshot) bool {
		return snapshot.SendackDepth == 1 && snapshot.PublicationCurrent == 0
	})
	if err := client.Send(context.Background(), publicationPressureSend(2)); err != nil {
		t.Fatalf("Send(2) error = %v", err)
	}
	waitForObservedSend(t, sendsObserved, 2)
	saturated := waitForQueueSnapshot(t, client, func(snapshot QueueSnapshot) bool {
		return snapshot.SendackDepth == 1 && snapshot.PublicationCurrent == 1
	})
	if saturated.PublicationCapacity != 1 || saturated.PublicationPeak != 1 {
		t.Fatalf("publication snapshot = %+v, want capacity/peak 1", saturated)
	}
	session, err := client.currentSession()
	if err != nil {
		t.Fatalf("currentSession() error = %v", err)
	}
	session.pendingMu.Lock()
	pending := session.pendingSendacks
	session.pendingMu.Unlock()
	if pending != 1 || pending > saturated.PublicationCapacity {
		t.Fatalf("pending SENDACKs = %d, want 1 and <= publication capacity %d", pending, saturated.PublicationCapacity)
	}
	if err := client.TrySend(publicationPressureSend(3)); !errors.Is(err, wkclient.ErrSendQueueFull) {
		t.Fatalf("TrySend(3) error = %v, want %v", err, wkclient.ErrSendQueueFull)
	}
	if snapshot := client.QueueSnapshot(); snapshot.PublicationBlocked != 0 || snapshot.PublicationCurrent != 1 || snapshot.AdmissionRejected != 1 {
		t.Fatalf("TrySend(3) publication snapshot = %+v, want current/rejected 1 and no blocked caller", snapshot)
	}
	select {
	case seq := <-sendsObserved:
		t.Fatalf("server observed rejected TrySend %d", seq)
	default:
	}

	thirdCtx, cancelThird := context.WithCancel(context.Background())
	thirdStarted := make(chan struct{})
	thirdDone := make(chan error, 1)
	go func() {
		close(thirdStarted)
		thirdDone <- client.Send(thirdCtx, publicationPressureSend(3))
	}()
	waitForSignal(t, thirdStarted, "third Send admission start")
	waitForQueueSnapshot(t, client, func(snapshot QueueSnapshot) bool {
		return snapshot.PublicationBlocked == 1
	})
	select {
	case seq := <-sendsObserved:
		t.Fatalf("server observed SEND %d before publication capacity was released", seq)
	default:
	}
	cancelThird()
	select {
	case err := <-thirdDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Send(3) error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked Send(3) did not return after context cancellation")
	}
	waitForQueueSnapshot(t, client, func(snapshot QueueSnapshot) bool {
		return snapshot.PublicationBlocked == 0
	})

	readCtx, cancelRead := context.WithTimeout(context.Background(), time.Second)
	defer cancelRead()
	first, err := client.ReadFrame(readCtx)
	if err != nil {
		t.Fatalf("ReadFrame(ACK 1) error = %v", err)
	}
	if ack, ok := first.(*frame.SendackPacket); !ok || ack.ClientSeq != 1 {
		t.Fatalf("ReadFrame(ACK 1) = %#v, want client seq 1", first)
	}
	waitForQueueSnapshot(t, client, func(snapshot QueueSnapshot) bool {
		return snapshot.SendackDepth == 1 && snapshot.PublicationCurrent == 0
	})
	second, err := client.ReadFrame(readCtx)
	if err != nil {
		t.Fatalf("ReadFrame(ACK 2) error = %v", err)
	}
	if ack, ok := second.(*frame.SendackPacket); !ok || ack.ClientSeq != 2 {
		t.Fatalf("ReadFrame(ACK 2) = %#v, want client seq 2", second)
	}
}

func TestClientSendAsyncAdmissionFailureReleasesPublicationCapacity(t *testing.T) {
	inner, err := wkclient.New(wkclient.Config{Addr: "127.0.0.1:5100", InboundFrameBufferSize: 1})
	if err != nil {
		t.Fatalf("wkclient.New() error = %v", err)
	}
	defer inner.Close()
	client := &Client{
		frameBufferSize: 1,
		session:         newClientSession(inner, 1),
	}

	err = client.Send(context.Background(), publicationPressureSend(1))
	if !errors.Is(err, wkclient.ErrNotConnected) {
		t.Fatalf("Send() error = %v, want %v", err, wkclient.ErrNotConnected)
	}
	snapshot := client.QueueSnapshot()
	if snapshot.PublicationCurrent != 0 || snapshot.PublicationBlocked != 0 || snapshot.PublicationCapacity != 1 || snapshot.PublicationPeak != 1 {
		t.Fatalf("publication snapshot after SendAsync failure = %+v, want current/blocked 0 and capacity/peak 1", snapshot)
	}
	client.session.pendingMu.Lock()
	pending := client.session.pendingSendacks
	client.session.pendingMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending SENDACKs after SendAsync failure = %d, want 0", pending)
	}
}

func publicationPressureSend(seq uint64) *frame.SendPacket {
	return &frame.SendPacket{
		ClientSeq:   seq,
		ClientMsgNo: "publication-" + strconv.FormatUint(seq, 10),
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("publication-pressure"),
	}
}

func waitForObservedSend(t *testing.T, observed <-chan uint64, want uint64) {
	t.Helper()
	select {
	case got := <-observed:
		if got != want {
			t.Fatalf("observed SEND client seq = %d, want %d", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for SEND client seq %d", want)
	}
}

func receivePressurePacket(seq uint64) *frame.RecvPacket {
	return &frame.RecvPacket{
		MessageID:   int64(seq),
		MessageSeq:  seq,
		ClientMsgNo: "recv-pressure",
		FromUID:     "u2",
		ChannelID:   "u1",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("payload"),
	}
}

func receivePressureSendack(send *frame.SendPacket, messageID int64) *frame.SendackPacket {
	return &frame.SendackPacket{
		ClientSeq:   send.ClientSeq,
		ClientMsgNo: send.ClientMsgNo,
		MessageID:   messageID,
		MessageSeq:  uint64(messageID),
		ReasonCode:  frame.ReasonSuccess,
	}
}

func waitForQueueSnapshot(t *testing.T, client *Client, ready func(QueueSnapshot) bool) QueueSnapshot {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		snapshot := client.QueueSnapshot()
		if ready(snapshot) {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue snapshot did not reach expected state: %+v", snapshot)
		}
		runtime.Gosched()
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
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

func TestClientParallelAttemptsDisambiguateStableMessageIdentityByClientSeq(t *testing.T) {
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
		if _, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion); err != nil {
			t.Fatalf("decode connect: %v", err)
		}
		writeFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})

		attempts := make([]*frame.SendPacket, 2)
		for index := range attempts {
			packet, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
			if err != nil {
				t.Fatalf("decode attempt %d: %v", index, err)
			}
			attempts[index] = packet.(*frame.SendPacket)
		}
		if attempts[0].ClientMsgNo != "stable-retry" || attempts[1].ClientMsgNo != "stable-retry" {
			t.Fatalf("client_msg_no changed across attempts: %q %q", attempts[0].ClientMsgNo, attempts[1].ClientMsgNo)
		}
		if attempts[0].ClientSeq == attempts[1].ClientSeq {
			t.Fatalf("parallel attempts reused ClientSeq %d", attempts[0].ClientSeq)
		}
		writeFrame(t, conn, sendackForAttempt(attempts[1], 902))
		writeFrame(t, conn, sendackForAttempt(attempts[0], 901))
	})
	defer server.close()

	client, err := NewClient(ClientConfig{Addr: server.addr, OperationTimeout: time.Second, AckTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()
	if err := client.Connect(context.Background(), "u1", "d1"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	for _, clientSeq := range []uint64{41, 42} {
		if err := client.Send(context.Background(), &frame.SendPacket{
			ClientSeq: clientSeq, ClientMsgNo: "stable-retry", ChannelID: "u2",
			ChannelType: frame.ChannelTypePerson, Payload: []byte("attempt"),
		}); err != nil {
			t.Fatalf("Send(ClientSeq=%d) error = %v", clientSeq, err)
		}
	}

	seen := make(map[uint64]int64, 2)
	for range 2 {
		packet, readErr := client.ReadFrame(context.Background())
		if readErr != nil {
			t.Fatalf("ReadFrame() error = %v", readErr)
		}
		ack := packet.(*frame.SendackPacket)
		seen[ack.ClientSeq] = ack.MessageID
	}
	if seen[41] != 901 || seen[42] != 902 {
		t.Fatalf("attempt ACK ownership = %+v", seen)
	}
}

func TestClientTimedOutAttemptLateAckCannotStealRetry(t *testing.T) {
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
		if _, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion); err != nil {
			t.Fatalf("decode connect: %v", err)
		}
		writeFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})

		firstFrame, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode first attempt: %v", err)
		}
		secondFrame, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode retry: %v", err)
		}
		first := firstFrame.(*frame.SendPacket)
		second := secondFrame.(*frame.SendPacket)
		writeFrame(t, conn, sendackForAttempt(first, 911))
		writeFrame(t, conn, sendackForAttempt(second, 912))
	})
	defer server.close()

	client, err := NewClient(ClientConfig{Addr: server.addr, OperationTimeout: time.Second, AckTimeout: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer client.Close()
	if err := client.Connect(context.Background(), "u1", "d1"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	first := &frame.SendPacket{ClientSeq: 51, ClientMsgNo: "stable-timeout", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("first")}
	if err := client.Send(context.Background(), first); err != nil {
		t.Fatalf("first Send() error = %v", err)
	}
	if _, readErr := client.ReadFrame(context.Background()); !errors.Is(readErr, wkclient.ErrAckTimeout) {
		t.Fatalf("first result = %v, want %v", readErr, wkclient.ErrAckTimeout)
	}
	retry := *first
	retry.ClientSeq = 52
	if err := client.Send(context.Background(), &retry); err != nil {
		t.Fatalf("retry Send() error = %v", err)
	}
	packet, readErr := client.ReadFrame(context.Background())
	if readErr != nil {
		t.Fatalf("retry ReadFrame() error = %v", readErr)
	}
	ack := packet.(*frame.SendackPacket)
	if ack.ClientSeq != retry.ClientSeq || ack.ClientMsgNo != retry.ClientMsgNo || ack.MessageID != 912 {
		t.Fatalf("retry ACK = %+v", ack)
	}
}

func sendackForAttempt(send *frame.SendPacket, messageID int64) *frame.SendackPacket {
	return &frame.SendackPacket{
		ClientSeq: send.ClientSeq, ClientMsgNo: send.ClientMsgNo,
		MessageID: messageID, MessageSeq: uint64(messageID), ReasonCode: frame.ReasonSuccess,
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

func TestClientRemoteCloseReturnsOriginalTerminalErrorOnceAfterPendingSends(t *testing.T) {
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
		if _, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion); err != nil {
			t.Fatalf("decode connect: %v", err)
		}
		writeFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})
		for i := 0; i < 2; i++ {
			if _, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion); err != nil {
				t.Fatalf("decode send %d: %v", i+1, err)
			}
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
	for seq := uint64(1); seq <= 2; seq++ {
		if err := client.Send(ctx, &frame.SendPacket{
			ClientSeq:   seq,
			ClientMsgNo: "terminal-" + strconv.FormatUint(seq, 10),
			ChannelID:   "u2",
			ChannelType: frame.ChannelTypePerson,
			Payload:     []byte("terminal"),
		}); err != nil {
			t.Fatalf("Send(%d) error = %v", seq, err)
		}
	}

	if _, err := client.ReadFrame(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("first terminal ReadFrame() error = %v, want original EOF", err)
	}
	if _, err := client.ReadFrame(ctx); !errors.Is(err, errClientNotConnected) {
		t.Fatalf("second terminal ReadFrame() error = %v, want %v", err, errClientNotConnected)
	}
}

func TestClientCompletedSendackIsReturnedBeforeRemoteTerminalError(t *testing.T) {
	server := newFakeWKProtoServer(t, func(t *testing.T, conn net.Conn) {
		if _, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion); err != nil {
			t.Fatalf("decode connect: %v", err)
		}
		writeFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})
		f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			t.Fatalf("decode send: %v", err)
		}
		send := f.(*frame.SendPacket)
		writeFrame(t, conn, receivePressureSendack(send, 201))
	})
	defer server.close()

	client, err := NewClient(ClientConfig{Addr: server.addr, OperationTimeout: time.Second, FrameBufferSize: 1})
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
		ClientMsgNo: "ack-before-terminal",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("terminal"),
	}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	f, err := client.ReadFrame(ctx)
	if err != nil {
		t.Fatalf("first ReadFrame() error = %v, want SENDACK", err)
	}
	if ack, ok := f.(*frame.SendackPacket); !ok || ack.ClientSeq != 1 {
		t.Fatalf("first ReadFrame() = %#v, want SENDACK 1", f)
	}
	if _, err := client.ReadFrame(ctx); !errors.Is(err, io.EOF) {
		t.Fatalf("second ReadFrame() error = %v, want terminal EOF", err)
	}
	if _, err := client.ReadFrame(ctx); !errors.Is(err, errClientNotConnected) {
		t.Fatalf("third ReadFrame() error = %v, want %v", err, errClientNotConnected)
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

func TestClientSessionFullRecvQueueDoesNotBlockSendack(t *testing.T) {
	session := newClientSession(nil, 1)
	session.recvCh <- &frame.RecvPacket{MessageID: 1}

	if !session.publishSendack(&frame.SendackPacket{ClientSeq: 7}) {
		t.Fatal("publishSendack() = false, want independent SENDACK admission")
	}
	ack := <-session.sendackCh
	if ack.ClientSeq != 7 {
		t.Fatalf("sendack client seq = %d, want 7", ack.ClientSeq)
	}
	recv := (<-session.recvCh).(*frame.RecvPacket)
	if recv.MessageID != 1 {
		t.Fatalf("recv message id = %d, want 1", recv.MessageID)
	}
}

func TestClientSessionBlockedRecvPublishUnblocksOnClose(t *testing.T) {
	session := newClientSession(nil, 1)
	client := &Client{frameBufferSize: 1, session: session}
	session.recvCh <- &frame.RecvPacket{MessageID: 1}
	started := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		close(started)
		done <- session.publishRecv(&frame.RecvPacket{MessageID: 2})
	}()
	waitForSignal(t, started, "blocked RECV publisher start")
	waitForQueueSnapshot(t, client, func(snapshot QueueSnapshot) bool {
		return snapshot.RecvDepth == snapshot.RecvCapacity
	})
	select {
	case published := <-done:
		t.Fatalf("RECV publisher completed before close: published=%t", published)
	case <-time.After(20 * time.Millisecond):
	}

	if err := session.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}
	select {
	case published := <-done:
		if published {
			t.Fatal("blocked RECV was published after session close")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked RECV publisher did not unblock after close")
	}
}

func TestClientSessionBlockedErrorPublishUnblocksOnClose(t *testing.T) {
	session := newClientSession(nil, 1)
	client := &Client{frameBufferSize: 1, session: session}
	session.errCh <- errorResult{err: errors.New("first")}
	started := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		close(started)
		done <- session.publishError(errorResult{err: errors.New("second")})
	}()
	waitForSignal(t, started, "blocked error publisher start")
	waitForQueueSnapshot(t, client, func(snapshot QueueSnapshot) bool {
		return snapshot.ErrorDepth == snapshot.ErrorCapacity
	})
	select {
	case published := <-done:
		t.Fatalf("error publisher completed before close: published=%t", published)
	case <-time.After(20 * time.Millisecond):
	}

	if err := session.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}
	select {
	case published := <-done:
		if published {
			t.Fatal("blocked error was published after session close")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked error publisher did not unblock after close")
	}
}

func TestClientErrorPublicationPressureCloseReleasesPublisherAndAdmission(t *testing.T) {
	session := newClientSession(nil, 1)
	client := &Client{frameBufferSize: 1, session: session}
	session.errCh <- errorResult{err: errors.New("queued error")}

	publisherAcquired := make(chan struct{})
	publisherDone := make(chan bool, 1)
	go func() {
		if err := session.acquirePublication(context.Background()); err != nil {
			publisherDone <- false
			return
		}
		close(publisherAcquired)
		published := session.publishError(errorResult{err: errors.New("blocked error")})
		session.releasePublication()
		publisherDone <- published
	}()
	waitForSignal(t, publisherAcquired, "error publisher publication admission")
	waitForQueueSnapshot(t, client, func(snapshot QueueSnapshot) bool {
		return snapshot.ErrorDepth == snapshot.ErrorCapacity && snapshot.PublicationCurrent == 1
	})

	waiterStarted := make(chan struct{})
	waiterDone := make(chan error, 1)
	go func() {
		close(waiterStarted)
		waiterDone <- session.acquirePublication(context.Background())
	}()
	waitForSignal(t, waiterStarted, "blocked publication waiter start")
	saturated := waitForQueueSnapshot(t, client, func(snapshot QueueSnapshot) bool {
		return snapshot.PublicationBlocked == 1
	})
	if saturated.PublicationCapacity != 1 || saturated.PublicationPeak != 1 {
		t.Fatalf("publication snapshot = %+v, want capacity/peak 1", saturated)
	}
	select {
	case published := <-publisherDone:
		t.Fatalf("error publisher completed before close: published=%t", published)
	default:
	}
	select {
	case err := <-waiterDone:
		t.Fatalf("publication waiter completed before close: %v", err)
	default:
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case published := <-publisherDone:
		if published {
			t.Fatal("blocked error published after close")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked error publisher did not return after close")
	}
	select {
	case err := <-waiterDone:
		if !errors.Is(err, errClientNotConnected) {
			t.Fatalf("publication waiter error = %v, want %v", err, errClientNotConnected)
		}
	case <-time.After(time.Second):
		t.Fatal("publication waiter did not return after close")
	}
	if snapshot := sessionPublicationSnapshot(session); snapshot.current != 0 || snapshot.blocked != 0 {
		t.Fatalf("terminal publication state = %+v, want current/blocked 0", snapshot)
	}
}

type publicationState struct {
	current int
	blocked int
}

func sessionPublicationSnapshot(session *clientSession) publicationState {
	return publicationState{
		current: int(session.publicationCurrent.Load()),
		blocked: int(session.publicationBlocked.Load()),
	}
}

func TestClientSessionBlockedSendackPublishUnblocksOnClose(t *testing.T) {
	session := newClientSession(nil, 1)
	client := &Client{frameBufferSize: 1, session: session}
	session.sendackCh <- &frame.SendackPacket{ClientSeq: 1}
	started := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		close(started)
		done <- session.publishSendack(&frame.SendackPacket{ClientSeq: 2})
	}()
	waitForSignal(t, started, "blocked SENDACK publisher start")
	waitForQueueSnapshot(t, client, func(snapshot QueueSnapshot) bool {
		return snapshot.SendackDepth == snapshot.SendackCapacity
	})
	select {
	case published := <-done:
		t.Fatalf("SENDACK publisher completed before close: published=%t", published)
	case <-time.After(20 * time.Millisecond):
	}

	if err := session.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}
	select {
	case published := <-done:
		if published {
			t.Fatal("blocked SENDACK was published after session close")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked SENDACK publisher did not unblock after close")
	}
}

func TestClientReadFrameBoundedSendackPreferenceDoesNotStarveRecv(t *testing.T) {
	session := newClientSession(nil, priorityResultQuota+2)
	for seq := uint64(1); seq <= priorityResultQuota+1; seq++ {
		session.sendackCh <- &frame.SendackPacket{ClientSeq: seq}
	}
	session.recvCh <- &frame.RecvPacket{MessageID: 99}

	for seq := uint64(1); seq <= priorityResultQuota; seq++ {
		f, err := session.readFrame(context.Background())
		if err != nil {
			t.Fatalf("readFrame(%d) error = %v", seq, err)
		}
		ack, ok := f.(*frame.SendackPacket)
		if !ok || ack.ClientSeq != seq {
			t.Fatalf("readFrame(%d) = %#v, want SENDACK %d", seq, f, seq)
		}
	}
	f, err := session.readFrame(context.Background())
	if err != nil {
		t.Fatalf("readFrame(RECV) error = %v", err)
	}
	recv, ok := f.(*frame.RecvPacket)
	if !ok || recv.MessageID != 99 {
		t.Fatalf("frame after %d preferred SENDACKs = %#v, want RECV 99", priorityResultQuota, f)
	}
}

func TestClientReadFrameErrorPriorityIsBoundedByQueuedRecv(t *testing.T) {
	session := newClientSession(nil, priorityResultQuota+2)
	priorityErr := errors.New("priority error")
	session.errCh <- errorResult{err: priorityErr}
	session.sendackCh <- &frame.SendackPacket{ClientSeq: 1}
	session.recvCh <- &frame.RecvPacket{MessageID: 99}

	consecutivePriority := 0
	recvCount := 0
	for i := 0; i < priorityResultQuota*3; i++ {
		f, err := session.readFrame(context.Background())
		switch {
		case errors.Is(err, priorityErr):
			consecutivePriority++
			session.errCh <- errorResult{err: priorityErr}
		case err != nil:
			t.Fatalf("readFrame(%d) error = %v, want priority error or frame", i, err)
		case f != nil:
			recv, ok := f.(*frame.RecvPacket)
			if !ok {
				t.Fatalf("readFrame(%d) = %T, want error before queued SENDACK", i, f)
			}
			if recv.MessageID != 99 {
				t.Fatalf("recv message id = %d, want 99", recv.MessageID)
			}
			recvCount++
			consecutivePriority = 0
			session.recvCh <- &frame.RecvPacket{MessageID: 99}
		}
		if consecutivePriority > priorityResultQuota {
			t.Fatalf("consecutive priority results = %d, want <= %d", consecutivePriority, priorityResultQuota)
		}
	}
	if recvCount < 2 {
		t.Fatalf("RECV count = %d, want repeated delivery under sustained priority errors", recvCount)
	}
}

func TestClientConcurrentReadFrameCancellationWhileArbitrationBusy(t *testing.T) {
	session := newClientSession(nil, 1)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := session.readFrame(firstCtx)
		firstDone <- err
	}()
	waitForReadArbitrationHeld(t, session)

	secondCtx, cancelSecond := context.WithCancel(context.Background())
	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		_, err := session.readFrame(secondCtx)
		secondDone <- err
	}()
	<-secondStarted
	cancelSecond()

	select {
	case err := <-secondDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("second readFrame() error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(100 * time.Millisecond):
		cancelFirst()
		<-firstDone
		if err := session.close(); err != nil {
			t.Fatalf("close() after arbitration timeout error = %v", err)
		}
		<-secondDone
		t.Fatal("canceled reader remained blocked waiting for frame arbitration")
	}

	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first readFrame() error = %v, want %v", err, context.Canceled)
	}
}

func TestClientConcurrentReadFrameCloseUnblocksArbitrationWaiter(t *testing.T) {
	session := newClientSession(nil, 1)
	firstDone := make(chan error, 1)
	go func() {
		_, err := session.readFrame(context.Background())
		firstDone <- err
	}()
	waitForReadArbitrationHeld(t, session)

	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		_, err := session.readFrame(context.Background())
		secondDone <- err
	}()
	<-secondStarted
	if err := session.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}

	for index, done := range []<-chan error{firstDone, secondDone} {
		select {
		case err := <-done:
			if !errors.Is(err, errClientNotConnected) {
				t.Fatalf("reader %d error = %v, want %v", index+1, err, errClientNotConnected)
			}
		case <-time.After(time.Second):
			t.Fatalf("reader %d did not unblock after close", index+1)
		}
	}
}

func TestClientConcurrentReadFrameCancellationIsRepeatable(t *testing.T) {
	session := newClientSession(nil, 1)
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, err := session.readFrame(firstCtx)
		firstDone <- err
	}()
	waitForReadArbitrationHeld(t, session)

	const waiterCount = 32
	cancels := make([]context.CancelFunc, 0, waiterCount)
	started := make(chan struct{}, waiterCount)
	done := make(chan error, waiterCount)
	for i := 0; i < waiterCount; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		go func(ctx context.Context) {
			started <- struct{}{}
			_, err := session.readFrame(ctx)
			done <- err
		}(ctx)
	}
	for i := 0; i < waiterCount; i++ {
		<-started
	}
	for _, cancel := range cancels {
		cancel()
	}

	deadline := time.After(time.Second)
	for i := 0; i < waiterCount; i++ {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled reader error = %v, want %v", err, context.Canceled)
			}
		case <-deadline:
			cancelFirst()
			<-firstDone
			t.Fatalf("only %d/%d canceled arbitration waiters returned", i, waiterCount)
		}
	}
	cancelFirst()
	if err := <-firstDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("first readFrame() error = %v, want %v", err, context.Canceled)
	}
}

func waitForReadArbitrationHeld(t *testing.T, session *clientSession) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if len(session.readPermit) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("reader did not acquire frame arbitration")
		}
		runtime.Gosched()
	}
}

func TestClientSessionConcurrentCloseIsIdempotent(t *testing.T) {
	session := newClientSession(nil, 1)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := session.close(); err != nil {
				t.Errorf("close() error = %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestClientConcurrentCloseIsIdempotent(t *testing.T) {
	client := &Client{
		operationTimeout: time.Second,
		session:          newClientSession(nil, 1),
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := client.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestClientReadFrameCancellationUnblocks(t *testing.T) {
	client := &Client{
		operationTimeout: time.Second,
		session:          newClientSession(nil, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.ReadFrame(ctx)
		done <- err
	}()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ReadFrame() error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadFrame() did not unblock after context cancellation")
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
