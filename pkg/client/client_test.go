package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

func TestClientConnectSendsConnectPacketAndStartsLoops(t *testing.T) {
	c, serverConn := newPipeClientServerOrFatal(t, Config{Token: "cfg-token"})

	serverErr := runTestServer(func() error {
		f, err := readTestFrame(serverConn)
		if err != nil {
			return err
		}
		connect, ok := f.(*frame.ConnectPacket)
		if !ok {
			return errors.New("server read non-CONNECT frame")
		}
		if connect.UID != "uid-1" {
			return errors.New("CONNECT UID mismatch")
		}
		if connect.DeviceID != "device-1" {
			return errors.New("CONNECT DeviceID mismatch")
		}
		if connect.Token != "cfg-token" {
			return errors.New("CONNECT Token mismatch")
		}
		if connect.ClientKey == "" {
			return errors.New("CONNECT ClientKey is empty")
		}
		keys, serverKey, err := wkprotoenc.NegotiateServerSession(connect.ClientKey)
		if err != nil {
			return err
		}
		if err := writeTestFrame(serverConn, &frame.ConnackPacket{
			Framer: frame.Framer{
				FrameType:        frame.CONNACK,
				HasServerVersion: true,
			},
			ServerVersion: frame.LatestVersion,
			ReasonCode:    frame.ReasonSuccess,
			ServerKey:     serverKey,
			Salt:          string(keys.AESIV),
			NodeId:        101,
		}); err != nil {
			return err
		}
		return nil
	})

	ack, err := c.Connect(context.Background(), ConnectOptions{
		UID:        "uid-1",
		DeviceID:   "device-1",
		DeviceFlag: frame.APP,
	})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if ack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("Connect() ack reason = %s, want %s", ack.ReasonCode, frame.ReasonSuccess)
	}
	if ack.NodeId != 101 {
		t.Fatalf("Connect() ack node id = %d, want 101", ack.NodeId)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestClientConnectAcceptsUnencryptedSuccessConnack(t *testing.T) {
	c, serverConn := newPipeClientServerOrFatal(t, Config{Token: "cfg-token"})

	serverErr := runTestServer(func() error {
		if _, err := readTestFrame(serverConn); err != nil {
			return err
		}
		return writeTestFrame(serverConn, &frame.ConnackPacket{
			Framer: frame.Framer{
				FrameType:        frame.CONNACK,
				HasServerVersion: true,
			},
			ServerVersion: frame.LatestVersion,
			ReasonCode:    frame.ReasonSuccess,
			NodeId:        202,
		})
	})

	ack, err := c.Connect(context.Background(), ConnectOptions{
		UID:        "uid-1",
		DeviceID:   "device-1",
		DeviceFlag: frame.APP,
	})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if ack.NodeId != 202 {
		t.Fatalf("Connect() ack node id = %d, want 202", ack.NodeId)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestClientConnectRejectsNonSuccessConnack(t *testing.T) {
	c, serverConn := newPipeClientServerOrFatal(t, Config{Token: "cfg-token"})

	serverErr := runTestServer(func() error {
		if _, err := readTestFrame(serverConn); err != nil {
			return err
		}
		return writeTestFrame(serverConn, &frame.ConnackPacket{
			Framer: frame.Framer{
				FrameType:        frame.CONNACK,
				HasServerVersion: true,
			},
			ServerVersion: frame.LatestVersion,
			ReasonCode:    frame.ReasonAuthFail,
		})
	})

	_, err := c.Connect(context.Background(), ConnectOptions{
		UID:        "uid-1",
		DeviceID:   "device-1",
		DeviceFlag: frame.APP,
	})
	if err == nil {
		t.Fatal("Connect() error = nil, want auth failure")
	}
	if !strings.Contains(err.Error(), frame.ReasonAuthFail.String()) {
		t.Fatalf("Connect() error = %v, want %s", err, frame.ReasonAuthFail)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestClientConnectHonorsCallerContext(t *testing.T) {
	c, err := New(Config{
		Addr:   "pipe",
		Token:  "cfg-token",
		Dialer: contextErrDialer{},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = c.Connect(ctx, ConnectOptions{
		UID:        "uid-1",
		DeviceID:   "device-1",
		DeviceFlag: frame.APP,
	})
	if err == nil {
		t.Fatal("Connect() error = nil, want context cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Connect() error = %v, want %v", err, context.Canceled)
	}
}

func TestClientConnectCancelsBlockedConnackReadPromptly(t *testing.T) {
	c, serverConn := newPipeClientServerOrFatal(t, Config{Token: "cfg-token"})

	connectRead := make(chan struct{})
	releaseServer := make(chan struct{})
	serverErr := runTestServer(func() error {
		if _, err := readTestFrame(serverConn); err != nil {
			return err
		}
		time.Sleep(50 * time.Millisecond)
		close(connectRead)
		<-releaseServer
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	connectErr := make(chan error, 1)
	go func() {
		_, err := c.Connect(ctx, ConnectOptions{
			UID:        "uid-1",
			DeviceID:   "device-1",
			DeviceFlag: frame.APP,
		})
		connectErr <- err
	}()

	select {
	case <-connectRead:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("server did not read CONNECT")
	}

	cancel()

	select {
	case err := <-connectErr:
		if !errors.Is(err, context.Canceled) {
			close(releaseServer)
			t.Fatalf("Connect() error = %v, want %v", err, context.Canceled)
		}
	case <-time.After(500 * time.Millisecond):
		_ = serverConn.Close()
		close(releaseServer)
		t.Fatal("Connect() did not return promptly after context cancellation")
	}

	close(releaseServer)
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestClientConnectSerializesConcurrentHandshakes(t *testing.T) {
	firstClient, firstServer := net.Pipe()
	secondClient, secondServer := net.Pipe()
	dialer := &sequenceDialer{
		conns:  []net.Conn{firstClient, secondClient},
		called: make(chan int, 2),
	}
	c, err := New(Config{
		Addr:   "pipe",
		Token:  "cfg-token",
		Dialer: dialer,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		_ = firstServer.Close()
		_ = secondServer.Close()
	})

	firstRead := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstServerErr := runTestServer(func() error {
		if _, err := readTestFrame(firstServer); err != nil {
			return err
		}
		close(firstRead)
		<-releaseFirst
		return writeTestFrame(firstServer, &frame.ConnackPacket{
			Framer: frame.Framer{
				FrameType:        frame.CONNACK,
				HasServerVersion: true,
			},
			ServerVersion: frame.LatestVersion,
			ReasonCode:    frame.ReasonSuccess,
		})
	})
	secondServerErr := runTestServer(func() error {
		if _, err := readTestFrame(secondServer); err != nil {
			return err
		}
		return writeTestFrame(secondServer, &frame.ConnackPacket{
			Framer: frame.Framer{
				FrameType:        frame.CONNACK,
				HasServerVersion: true,
			},
			ServerVersion: frame.LatestVersion,
			ReasonCode:    frame.ReasonSuccess,
		})
	})

	firstErr := make(chan error, 1)
	go func() {
		_, err := c.Connect(context.Background(), ConnectOptions{
			UID:        "uid-1",
			DeviceID:   "device-1",
			DeviceFlag: frame.APP,
		})
		firstErr <- err
	}()

	if call := <-dialer.called; call != 1 {
		t.Fatalf("first dial call = %d, want 1", call)
	}
	select {
	case <-firstRead:
	case <-time.After(time.Second):
		t.Fatal("first server did not read CONNECT")
	}

	secondErr := make(chan error, 1)
	go func() {
		_, err := c.Connect(context.Background(), ConnectOptions{
			UID:        "uid-2",
			DeviceID:   "device-2",
			DeviceFlag: frame.APP,
		})
		secondErr <- err
	}()

	select {
	case call := <-dialer.called:
		t.Fatalf("second Connect dialed before first handshake completed: call %d", call)
	case <-time.After(30 * time.Millisecond):
	}

	close(releaseFirst)
	if err := <-firstErr; err != nil {
		t.Fatalf("first Connect() error = %v", err)
	}
	if err := <-firstServerErr; err != nil {
		t.Fatalf("first server error = %v", err)
	}

	select {
	case call := <-dialer.called:
		if call != 2 {
			t.Fatalf("second dial call = %d, want 2", call)
		}
	case <-time.After(time.Second):
		t.Fatal("second Connect did not start after first completed")
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("second Connect() error = %v", err)
	}
	if err := <-secondServerErr; err != nil {
		t.Fatalf("second server error = %v", err)
	}
}

func TestClientReaderDecodesConcatenatedSendacksOutOfOrder(t *testing.T) {
	c, serverConn := newConnectedPipeClientOrFatal(t, Config{})

	first, err := c.pending.add(pendingKey{ClientSeq: 1, ClientMsgNo: "one"}, time.Second)
	if err != nil {
		t.Fatalf("pending.add(first) error = %v", err)
	}
	second, err := c.pending.add(pendingKey{ClientSeq: 2, ClientMsgNo: "two"}, time.Second)
	if err != nil {
		t.Fatalf("pending.add(second) error = %v", err)
	}

	secondAck := encodeClientTestFrameOrFatal(t, &frame.SendackPacket{
		ClientSeq:   2,
		ClientMsgNo: "two",
		MessageID:   2002,
		MessageSeq:  22,
		ReasonCode:  frame.ReasonSuccess,
	})
	firstAck := encodeClientTestFrameOrFatal(t, &frame.SendackPacket{
		ClientSeq:   1,
		ClientMsgNo: "one",
		MessageID:   1001,
		MessageSeq:  11,
		ReasonCode:  frame.ReasonSuccess,
	})
	if _, err := serverConn.Write(append(secondAck, firstAck...)); err != nil {
		t.Fatalf("server Write() error = %v", err)
	}

	secondOutcome := readPendingOutcomeOrFatal(t, second)
	if secondOutcome.err != nil {
		t.Fatalf("second pending err = %v", secondOutcome.err)
	}
	if secondOutcome.result.MessageID != 2002 {
		t.Fatalf("second MessageID = %d, want 2002", secondOutcome.result.MessageID)
	}

	firstOutcome := readPendingOutcomeOrFatal(t, first)
	if firstOutcome.err != nil {
		t.Fatalf("first pending err = %v", firstOutcome.err)
	}
	if firstOutcome.result.MessageID != 1001 {
		t.Fatalf("first MessageID = %d, want 1001", firstOutcome.result.MessageID)
	}
}

func TestClientSendRejectsOversizedPayload(t *testing.T) {
	c, _ := newConnectedPipeClientOrFatal(t, Config{})

	_, err := c.Send(context.Background(), Message{
		ChannelID:   "ch-oversized",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     make([]byte, codec.PayloadMaxSize+1),
	})
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("Send() error = %v, want %v", err, ErrPayloadTooLarge)
	}
}

func TestClientSendBeforeConnectRejectsQuickly(t *testing.T) {
	c, err := New(Config{Addr: "pipe"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = c.SendAsync(ctx, Message{
		ChannelID:   "ch-before-connect",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("payload"),
	})
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("SendAsync() error = %v, want %v", err, ErrNotConnected)
	}
}

func TestClientSendBatchValidationFailureDoesNotLeavePending(t *testing.T) {
	c, _ := newConnectedPipeClientOrFatal(t, Config{
		AckTimeout: time.Second,
	})

	_, err := c.SendBatch(context.Background(), []Message{
		{
			ClientMsgNo: "valid",
			ChannelID:   "ch-batch-validation",
			ChannelType: frame.ChannelTypeGroup,
			Payload:     []byte("valid"),
		},
		{
			ClientMsgNo: "invalid",
			ChannelID:   "ch-batch-validation",
			Payload:     []byte("missing channel type"),
		},
	})
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("SendBatch() error = %v, want %v", err, ErrInvalidMessage)
	}
	if n := pendingEntryCount(c.pending); n != 0 {
		t.Fatalf("pending entries = %d, want 0", n)
	}
}

func TestClientSendBatchRejectsMoreThanMaxInflight(t *testing.T) {
	c, _ := newConnectedPipeClientOrFatal(t, Config{MaxInflight: 1})

	_, err := c.SendBatch(context.Background(), []Message{
		{
			ClientMsgNo: "first-too-many",
			ChannelID:   "ch-max-inflight",
			ChannelType: frame.ChannelTypeGroup,
			Payload:     []byte("first"),
		},
		{
			ClientMsgNo: "second-too-many",
			ChannelID:   "ch-max-inflight",
			ChannelType: frame.ChannelTypeGroup,
			Payload:     []byte("second"),
		},
	})
	if !errors.Is(err, ErrSendQueueFull) {
		t.Fatalf("SendBatch() error = %v, want %v", err, ErrSendQueueFull)
	}
	if n := pendingEntryCount(c.pending); n != 0 {
		t.Fatalf("pending entries = %d, want 0", n)
	}
}

func TestBuildSendPacketUsesAssignedSequenceWhenMessageSequenceIsZero(t *testing.T) {
	payload := []byte("payload")
	pkt, err := buildSendPacket(Message{
		ChannelID:   "ch-seq",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     payload,
	}, 42)
	if err != nil {
		t.Fatalf("buildSendPacket() error = %v", err)
	}
	if pkt.ClientSeq != 42 {
		t.Fatalf("ClientSeq = %d, want 42", pkt.ClientSeq)
	}
	payload[0] = 'P'
	if string(pkt.Payload) != "payload" {
		t.Fatalf("Payload = %q, want copied payload", pkt.Payload)
	}
}

func TestClientSendAsyncCopiesPayloadBeforeQueue(t *testing.T) {
	c, serverConn := newConnectedPipeClientOrFatal(t, Config{
		BatchMaxWait: 50 * time.Millisecond,
	})

	payload := []byte("payload")
	future, err := c.SendAsync(context.Background(), Message{
		ClientMsgNo: "copy-payload",
		ChannelID:   "ch-copy",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     payload,
	})
	if err != nil {
		t.Fatalf("SendAsync() error = %v", err)
	}
	payload[0] = 'P'

	serverErr := runTestServer(func() error {
		f, err := readTestFrame(serverConn)
		if err != nil {
			return err
		}
		send, ok := f.(*frame.SendPacket)
		if !ok {
			return fmt.Errorf("server frame = %T, want SEND", f)
		}
		if string(send.Payload) != "payload" {
			return fmt.Errorf("sent payload = %q, want payload", send.Payload)
		}
		return writeTestFrame(serverConn, &frame.SendackPacket{
			ClientSeq:   send.ClientSeq,
			ClientMsgNo: send.ClientMsgNo,
			MessageID:   303,
			MessageSeq:  3,
			ReasonCode:  frame.ReasonSuccess,
		})
	})

	result, err := future.Wait(context.Background())
	if err != nil {
		t.Fatalf("future.Wait() error = %v", err)
	}
	if result.MessageID != 303 {
		t.Fatalf("MessageID = %d, want 303", result.MessageID)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestClientSendAsyncHonorsMaxInflight(t *testing.T) {
	c, serverConn := newConnectedPipeClientOrFatal(t, Config{
		MaxInflight: 1,
		AckTimeout:  time.Second,
	})

	first, err := c.SendAsync(context.Background(), Message{
		ClientMsgNo: "first-inflight",
		ChannelID:   "ch-inflight",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("first"),
	})
	if err != nil {
		t.Fatalf("SendAsync(first) error = %v", err)
	}

	serverErr := runTestServer(func() error {
		firstFrame, err := readTestFrame(serverConn)
		if err != nil {
			return err
		}
		firstSend, ok := firstFrame.(*frame.SendPacket)
		if !ok {
			return fmt.Errorf("first frame = %T, want SEND", firstFrame)
		}

		secondStarted := make(chan struct{})
		secondErr := make(chan error, 1)
		go func() {
			close(secondStarted)
			_, err := c.SendAsync(context.Background(), Message{
				ClientMsgNo: "second-inflight",
				ChannelID:   "ch-inflight",
				ChannelType: frame.ChannelTypeGroup,
				Payload:     []byte("second"),
			})
			secondErr <- err
		}()
		<-secondStarted
		select {
		case err := <-secondErr:
			return fmt.Errorf("second SendAsync returned before first ACK: %v", err)
		case <-time.After(30 * time.Millisecond):
		}

		if err := writeTestFrame(serverConn, &frame.SendackPacket{
			ClientSeq:   firstSend.ClientSeq,
			ClientMsgNo: firstSend.ClientMsgNo,
			MessageID:   501,
			MessageSeq:  51,
			ReasonCode:  frame.ReasonSuccess,
		}); err != nil {
			return err
		}
		if err := <-secondErr; err != nil {
			return err
		}
		return nil
	})

	result, err := first.Wait(context.Background())
	if err != nil {
		t.Fatalf("first future error = %v", err)
	}
	if result.MessageID != 501 {
		t.Fatalf("first MessageID = %d, want 501", result.MessageID)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestClientWriterUsesRequestSessionSnapshot(t *testing.T) {
	c, err := New(Config{Addr: "pipe"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	oldSession := newTestSessionCryptoOrFatal(t, "old-send-key1234", "old-send-iv12345")
	newSession := newTestSessionCryptoOrFatal(t, "new-send-key1234", "new-send-iv12345")
	c.crypto.mu.Lock()
	c.crypto.session = newSession
	c.crypto.mu.Unlock()

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	pkt, err := buildSendPacket(Message{
		ClientMsgNo: "session-snapshot",
		ChannelID:   "ch-session",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("secret"),
	}, 1)
	if err != nil {
		t.Fatalf("buildSendPacket() error = %v", err)
	}

	writeErr := make(chan error, 1)
	go func() {
		_, err := c.writeBatch([]writeRequest{{
			kind:    writeKindSend,
			pkt:     pkt,
			conn:    clientConn,
			session: oldSession,
		}})
		writeErr <- err
	}()

	f, err := readTestFrame(serverConn)
	if err != nil {
		t.Fatalf("readTestFrame() error = %v", err)
	}
	send, ok := f.(*frame.SendPacket)
	if !ok {
		t.Fatalf("frame = %T, want SEND", f)
	}
	plain, err := wkprotoenc.DecryptPayloadWithCrypto(send.Payload, oldSession)
	if err != nil {
		t.Fatalf("DecryptPayloadWithCrypto(old session) error = %v", err)
	}
	if string(plain) != "secret" {
		t.Fatalf("decrypted payload = %q, want secret", plain)
	}
	if err := <-writeErr; err != nil {
		t.Fatalf("writeBatch() error = %v", err)
	}
}

func TestClientSendBatchReturnsResultsInInputOrderWithOutOfOrderSendacks(t *testing.T) {
	c, serverConn := newConnectedPipeClientOrFatal(t, Config{
		BatchMaxWait: 5 * time.Millisecond,
	})

	readDone := make(chan []uint64, 1)
	serverErr := runTestServer(func() error {
		first, err := readTestFrame(serverConn)
		if err != nil {
			return err
		}
		second, err := readTestFrame(serverConn)
		if err != nil {
			return err
		}
		firstSend, ok := first.(*frame.SendPacket)
		if !ok {
			return fmt.Errorf("first frame = %T, want SEND", first)
		}
		secondSend, ok := second.(*frame.SendPacket)
		if !ok {
			return fmt.Errorf("second frame = %T, want SEND", second)
		}
		readDone <- []uint64{firstSend.ClientSeq, secondSend.ClientSeq}

		secondAck := encodeClientTestFrameOrFatal(t, &frame.SendackPacket{
			ClientSeq:   secondSend.ClientSeq,
			ClientMsgNo: secondSend.ClientMsgNo,
			MessageID:   2002,
			MessageSeq:  22,
			ReasonCode:  frame.ReasonSuccess,
		})
		firstAck := encodeClientTestFrameOrFatal(t, &frame.SendackPacket{
			ClientSeq:   firstSend.ClientSeq,
			ClientMsgNo: firstSend.ClientMsgNo,
			MessageID:   1001,
			MessageSeq:  11,
			ReasonCode:  frame.ReasonSuccess,
		})
		_, err = serverConn.Write(append(secondAck, firstAck...))
		return err
	})

	results, err := c.SendBatch(context.Background(), []Message{
		{
			ClientMsgNo: "first",
			ChannelID:   "ch-order",
			ChannelType: frame.ChannelTypeGroup,
			Payload:     []byte("first"),
		},
		{
			ClientMsgNo: "second",
			ChannelID:   "ch-order",
			ChannelType: frame.ChannelTypeGroup,
			Payload:     []byte("second"),
		},
	})
	if err != nil {
		t.Fatalf("SendBatch() error = %v", err)
	}
	if got := <-readDone; len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("server read client seqs = %v, want [1 2]", got)
	}
	if results[0].MessageID != 1001 || results[0].ClientMsgNo != "first" {
		t.Fatalf("first result = %+v, want first ack", results[0])
	}
	if results[1].MessageID != 2002 || results[1].ClientMsgNo != "second" {
		t.Fatalf("second result = %+v, want second ack", results[1])
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server error = %v", err)
	}
}

func TestClientReaderPreservesPartialFrameBytes(t *testing.T) {
	c, serverConn := newConnectedPipeClientOrFatal(t, Config{})

	entry, err := c.pending.add(pendingKey{ClientSeq: 7, ClientMsgNo: "split"}, time.Second)
	if err != nil {
		t.Fatalf("pending.add() error = %v", err)
	}

	ack := encodeClientTestFrameOrFatal(t, &frame.SendackPacket{
		ClientSeq:   7,
		ClientMsgNo: "split",
		MessageID:   7007,
		MessageSeq:  77,
		ReasonCode:  frame.ReasonSuccess,
	})
	cut := len(ack) / 2
	if cut == 0 {
		t.Fatal("encoded SENDACK too short to split")
	}
	if _, err := serverConn.Write(ack[:cut]); err != nil {
		t.Fatalf("server first Write() error = %v", err)
	}
	select {
	case outcome := <-entry.done:
		t.Fatalf("pending resolved before complete frame: %+v", outcome)
	case <-time.After(30 * time.Millisecond):
	}
	if _, err := serverConn.Write(ack[cut:]); err != nil {
		t.Fatalf("server second Write() error = %v", err)
	}

	outcome := readPendingOutcomeOrFatal(t, entry)
	if outcome.err != nil {
		t.Fatalf("pending err = %v", outcome.err)
	}
	if outcome.result.MessageID != 7007 {
		t.Fatalf("MessageID = %d, want 7007", outcome.result.MessageID)
	}
}

func TestRouteInboundRecvEnqueuesWithBoundedOverwriteOldest(t *testing.T) {
	c, err := New(Config{
		Addr:                   "pipe",
		InboundFrameBufferSize: 2,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for i := int64(1); i <= 3; i++ {
		if err := c.routeInboundFrame(&frame.RecvPacket{
			Setting:   frame.SettingNoEncrypt,
			MessageID: i,
			Payload:   []byte{byte(i)},
		}); err != nil {
			t.Fatalf("routeInboundFrame(RECV %d) error = %v", i, err)
		}
	}

	first := <-c.recvCh
	second := <-c.recvCh
	if first.MessageID != 2 || second.MessageID != 3 {
		t.Fatalf("queued MessageIDs = %d,%d, want 2,3", first.MessageID, second.MessageID)
	}
}

func TestEnqueueRecvConcurrentOverwriteDoesNotBlock(t *testing.T) {
	c, err := New(Config{
		Addr:                   "pipe",
		InboundFrameBufferSize: 1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	c.enqueueRecv(&frame.RecvPacket{MessageID: 1})

	var wg sync.WaitGroup
	done := make(chan struct{})
	for i := int64(2); i <= 16; i++ {
		wg.Add(1)
		go func(messageID int64) {
			defer wg.Done()
			c.enqueueRecv(&frame.RecvPacket{MessageID: messageID})
		}(i)
	}
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("concurrent enqueueRecv calls blocked")
	}
}

func TestEnqueueRecvZeroCapacityDoesNotBlock(t *testing.T) {
	c, err := New(Config{Addr: "pipe"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	c.recvCh = make(chan *frame.RecvPacket)

	done := make(chan struct{})
	go func() {
		c.enqueueRecv(&frame.RecvPacket{MessageID: 1})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("enqueueRecv blocked on zero-capacity channel")
	}
}

func TestClientCloseStopsWriterWhenQueueFull(t *testing.T) {
	c, err := New(Config{
		Addr:              "pipe",
		SendQueueCapacity: 1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	c.writeCh <- writeRequest{kind: writeKindSend}
	writerDone := make(chan struct{})
	c.writerDone = writerDone
	go func() {
		defer close(writerDone)
		c.writerLoop()
	}()

	_ = c.Close()
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("writer did not stop after Close with full write queue")
	}
}

func TestClientReaderUsesSessionCryptoSnapshot(t *testing.T) {
	c, err := New(Config{Addr: "pipe"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	oldSession := newTestSessionCryptoOrFatal(t, "old-session-key1", "old-session-iv12")
	newSession := newTestSessionCryptoOrFatal(t, "new-session-key1", "new-session-iv12")
	encrypted, err := wkprotoenc.EncryptPayloadWithCrypto([]byte("old plaintext"), oldSession)
	if err != nil {
		t.Fatalf("EncryptPayloadWithCrypto() error = %v", err)
	}

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = c.Close()
		_ = serverConn.Close()
	})

	c.crypto.mu.Lock()
	c.crypto.session = oldSession
	c.crypto.mu.Unlock()
	c.mu.Lock()
	c.conn = clientConn
	c.startLoops(clientConn, c.pending, oldSession)
	c.mu.Unlock()

	recvData := encodeClientTestFrameOrFatal(t, &frame.RecvPacket{
		MessageID:   42,
		MessageSeq:  7,
		ChannelID:   "u1",
		ChannelType: frame.ChannelTypePerson,
		FromUID:     "u2",
		Payload:     encrypted,
	})
	if len(recvData) < 2 {
		t.Fatal("encoded RECV too short to split")
	}
	if err := serverConn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("server SetWriteDeadline() error = %v", err)
	}
	if _, err := serverConn.Write(recvData[:1]); err != nil {
		t.Fatalf("server first Write() error = %v", err)
	}

	c.crypto.mu.Lock()
	c.crypto.session = newSession
	c.crypto.mu.Unlock()

	if err := serverConn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("server SetWriteDeadline() error = %v", err)
	}
	if _, err := serverConn.Write(recvData[1:]); err != nil {
		t.Fatalf("server second Write() error = %v", err)
	}

	select {
	case pkt := <-c.recvCh:
		if string(pkt.Payload) != "old plaintext" {
			t.Fatalf("recv payload = %q, want old plaintext", pkt.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for RECV decrypted with reader session")
	}
}

func TestRouteInboundDisconnectReturnsReadFailure(t *testing.T) {
	c, err := New(Config{Addr: "pipe"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = c.routeInboundFrame(&frame.DisconnectPacket{
		ReasonCode: frame.ReasonAuthFail,
		Reason:     "bad token",
	})
	if err == nil {
		t.Fatal("routeInboundFrame(DISCONNECT) error = nil")
	}
	if !strings.Contains(err.Error(), "bad token") || !strings.Contains(err.Error(), frame.ReasonAuthFail.String()) {
		t.Fatalf("routeInboundFrame(DISCONNECT) error = %v, want reason fields", err)
	}
}

func TestClientReaderFailureDoesNotCloseReplacedConnection(t *testing.T) {
	c, err := New(Config{Addr: "pipe"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	oldClientConn, oldServerConn := net.Pipe()
	newClientConn, newServerConn := net.Pipe()
	t.Cleanup(func() {
		_ = c.Close()
		_ = oldServerConn.Close()
		_ = newServerConn.Close()
	})

	c.mu.Lock()
	c.conn = oldClientConn
	c.startLoops(oldClientConn, c.pending, c.crypto.currentSession())
	oldReaderDone := c.readerDone
	c.mu.Unlock()

	ack := encodeClientTestFrameOrFatal(t, &frame.SendackPacket{
		ClientSeq:  101,
		MessageID:  1001,
		ReasonCode: frame.ReasonSuccess,
	})
	if _, err := oldServerConn.Write(ack[:1]); err != nil {
		t.Fatalf("old server partial Write() error = %v", err)
	}

	c.mu.Lock()
	c.conn = newClientConn
	c.mu.Unlock()

	if err := oldServerConn.Close(); err != nil {
		t.Fatalf("old server Close() error = %v", err)
	}
	select {
	case <-oldReaderDone:
	case <-time.After(time.Second):
		t.Fatal("old reader did not stop")
	}

	conn, err := c.currentConn()
	if err != nil {
		t.Fatalf("currentConn() error = %v, want replaced connection active", err)
	}
	if conn != newClientConn {
		t.Fatalf("currentConn() = %p, want new connection %p", conn, newClientConn)
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		t.Fatal("client closed after old reader failure")
	}
}

type contextErrDialer struct{}

func (contextErrDialer) DialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	return nil, ctx.Err()
}

type sequenceDialer struct {
	mu     sync.Mutex
	conns  []net.Conn
	called chan int
}

func (d *sequenceDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.conns) == 0 {
		return nil, errors.New("no test connections")
	}
	conn := d.conns[0]
	d.conns = d.conns[1:]
	d.called <- cap(d.called) - len(d.conns)
	return conn, nil
}

func runTestServer(fn func() error) <-chan error {
	done := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- fmt.Errorf("server panic: %v", recovered)
			}
		}()
		done <- fn()
	}()
	return done
}

func newPipeClientServerOrFatal(t *testing.T, cfg Config) (*Client, net.Conn) {
	t.Helper()

	c, serverConn, err := newPipeClientServer(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		_ = serverConn.Close()
	})
	return c, serverConn
}

func newConnectedPipeClientOrFatal(t *testing.T, cfg Config) (*Client, net.Conn) {
	t.Helper()

	cfg.Addr = "pipe"
	clientConn, serverConn := net.Pipe()
	cfg.Dialer = fakeDialer{conn: clientConn}
	c, err := New(cfg)
	if err != nil {
		_ = clientConn.Close()
		_ = serverConn.Close()
		t.Fatalf("New() error = %v", err)
	}
	c.mu.Lock()
	c.conn = clientConn
	c.startLoops(clientConn, c.pending, c.crypto.currentSession())
	c.mu.Unlock()
	t.Cleanup(func() {
		_ = c.Close()
		_ = serverConn.Close()
	})
	return c, serverConn
}

func encodeClientTestFrameOrFatal(t *testing.T, f frame.Frame) []byte {
	t.Helper()

	data, err := codec.New().EncodeFrame(f, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame(%T) error = %v", f, err)
	}
	return data
}

func newTestSessionCryptoOrFatal(t *testing.T, key string, iv string) *wkprotoenc.SessionCrypto {
	t.Helper()

	session, err := wkprotoenc.NewSessionCrypto(wkprotoenc.SessionKeys{
		AESKey: []byte(key),
		AESIV:  []byte(iv),
	})
	if err != nil {
		t.Fatalf("NewSessionCrypto() error = %v", err)
	}
	return session
}

func readPendingOutcomeOrFatal(t *testing.T, entry *pendingEntry) sendOutcome {
	t.Helper()

	select {
	case outcome := <-entry.done:
		return outcome
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for pending outcome")
		return sendOutcome{}
	}
}

func pendingEntryCount(p *pendingTracker) int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

func TestNormalizeConfigAppliesToolingDefaults(t *testing.T) {
	cfg, err := normalizeConfig(Config{Addr: "127.0.0.1:5100"})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	if cfg.OperationTimeout != 5*time.Second {
		t.Fatalf("OperationTimeout = %s, want 5s", cfg.OperationTimeout)
	}
	if cfg.AckTimeout != 5*time.Second {
		t.Fatalf("AckTimeout = %s, want 5s", cfg.AckTimeout)
	}
	if cfg.SendQueueCapacity != 8192 {
		t.Fatalf("SendQueueCapacity = %d, want 8192", cfg.SendQueueCapacity)
	}
	if cfg.MaxInflight != 8192 {
		t.Fatalf("MaxInflight = %d, want 8192", cfg.MaxInflight)
	}
	if cfg.BatchMaxRecords != 512 {
		t.Fatalf("BatchMaxRecords = %d, want 512", cfg.BatchMaxRecords)
	}
	if cfg.BatchMaxBytes != 512*1024 {
		t.Fatalf("BatchMaxBytes = %d, want %d", cfg.BatchMaxBytes, 512*1024)
	}
	if cfg.BatchMaxWait != time.Millisecond {
		t.Fatalf("BatchMaxWait = %s, want 1ms", cfg.BatchMaxWait)
	}
	if cfg.ReadBufferSize != 4096 {
		t.Fatalf("ReadBufferSize = %d, want 4096", cfg.ReadBufferSize)
	}
	if cfg.InboundFrameBufferSize != 1024 {
		t.Fatalf("InboundFrameBufferSize = %d, want 1024", cfg.InboundFrameBufferSize)
	}
	if cfg.GenerateClientMsgNo {
		t.Fatal("GenerateClientMsgNo default = true, want false")
	}
}

func TestNormalizeConfigRequiresAddr(t *testing.T) {
	_, err := normalizeConfig(Config{})
	if !errors.Is(err, ErrMissingAddr) {
		t.Fatalf("normalizeConfig() error = %v, want %v", err, ErrMissingAddr)
	}
}
