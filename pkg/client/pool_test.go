package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestPoolSendBatchReassemblesOriginalOrder(t *testing.T) {
	p := &Pool{
		clients: map[string]poolClient{
			"u1": fakePoolClient{resultID: 1},
			"u2": fakePoolClient{resultID: 2},
		},
	}

	results, err := p.SendBatch(context.Background(), []RoutedMessage{
		{UID: "u2", Message: Message{ClientMsgNo: "m2", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Payload: []byte("b")}},
		{UID: "u1", Message: Message{ClientMsgNo: "m1", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Payload: []byte("a")}},
	})
	if err != nil {
		t.Fatalf("SendBatch() error = %v", err)
	}
	if results[0].MessageID != 2 || results[1].MessageID != 1 {
		t.Fatalf("results = %#v, want IDs 2,1", results)
	}
}

func TestPoolConnectIndexesClientsByUID(t *testing.T) {
	firstClient, firstServer := net.Pipe()
	secondClient, secondServer := net.Pipe()
	dialer := &sequenceDialer{
		conns:  []net.Conn{firstClient, secondClient},
		called: make(chan int, 2),
	}
	p, err := NewPool(PoolConfig{
		Addrs: []string{"gateway-1", "gateway-2"},
		Client: Config{
			Token:  "pool-token",
			Dialer: dialer,
		},
	})
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	t.Cleanup(func() {
		_ = p.Close()
		_ = firstServer.Close()
		_ = secondServer.Close()
	})

	firstErr := runTestServer(func() error {
		return acceptPoolConnect(firstServer, "u1", "d1", "t1")
	})
	secondErr := runTestServer(func() error {
		return acceptPoolConnect(secondServer, "u2", "d2", "t2")
	})

	err = p.Connect(context.Background(), []Identity{
		{UID: "u1", DeviceID: "d1", Token: "t1"},
		{UID: "u2", DeviceID: "d2", Token: "t2"},
	})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if _, ok := p.Client("u1"); !ok {
		t.Fatal("Client(u1) not found")
	}
	if _, ok := p.Client("u2"); !ok {
		t.Fatal("Client(u2) not found")
	}
	if err := <-firstErr; err != nil {
		t.Fatalf("first server error = %v", err)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("second server error = %v", err)
	}
}

func TestPoolConnectFailureClosesEarlierClientsAndLeavesPoolUnchanged(t *testing.T) {
	firstClient, firstServer := net.Pipe()
	secondClient, secondServer := net.Pipe()
	dialer := &sequenceDialer{
		conns:  []net.Conn{firstClient, secondClient},
		called: make(chan int, 2),
	}
	p, err := NewPool(PoolConfig{
		Addrs: []string{"gateway-1"},
		Client: Config{
			Dialer: dialer,
		},
	})
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	t.Cleanup(func() {
		_ = p.Close()
		_ = firstServer.Close()
		_ = secondServer.Close()
	})

	firstErr := runTestServer(func() error {
		return acceptPoolConnect(firstServer, "u1", "d1", "")
	})
	secondErr := runTestServer(func() error {
		if _, err := readTestFrame(secondServer); err != nil {
			return err
		}
		return writeTestFrame(secondServer, &frame.ConnackPacket{
			Framer: frame.Framer{
				FrameType:        frame.CONNACK,
				HasServerVersion: true,
			},
			ServerVersion: frame.LatestVersion,
			ReasonCode:    frame.ReasonAuthFail,
		})
	})

	err = p.Connect(context.Background(), []Identity{
		{UID: "u1", DeviceID: "d1"},
		{UID: "u2", DeviceID: "d2"},
	})
	if err == nil {
		t.Fatal("Connect() error = nil, want second connect failure")
	}
	if _, ok := p.Client("u1"); ok {
		t.Fatal("Client(u1) exists after failed Connect")
	}
	if err := <-firstErr; err != nil {
		t.Fatalf("first server error = %v", err)
	}
	if err := <-secondErr; err != nil {
		t.Fatalf("second server error = %v", err)
	}
	if err := firstServer.SetReadDeadline(time.Now().Add(time.Second)); err == nil {
		var b [1]byte
		if _, err := firstServer.Read(b[:]); err == nil || isTimeoutError(err) {
			t.Fatalf("first connection read error = %v, want closed connection", err)
		}
	}
}

func TestPoolCloseIsTerminalAndClearsClients(t *testing.T) {
	client := &closeTrackingPoolClient{}
	p := &Pool{clients: map[string]poolClient{"u1": client}}

	if err := p.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !client.closed {
		t.Fatal("client was not closed")
	}
	if _, ok := p.Client("u1"); ok {
		t.Fatal("Client(u1) found after Close")
	}
	_, err := p.SendBatch(context.Background(), []RoutedMessage{
		{UID: "u1", Message: Message{ClientSeq: 1, ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Payload: []byte("x")}},
	})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("SendBatch() error = %v, want %v", err, ErrClosed)
	}
	err = p.Connect(context.Background(), []Identity{{UID: "u1", DeviceID: "d1"}})
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("Connect() error = %v, want %v", err, ErrClosed)
	}
}

func TestNewPoolRejectsEmptyAddressInList(t *testing.T) {
	_, err := NewPool(PoolConfig{Addrs: []string{"gateway-1", ""}})
	if err == nil {
		t.Fatal("NewPool() error = nil, want empty address error")
	}
}

func acceptPoolConnect(conn net.Conn, uid string, deviceID string, token string) error {
	f, err := readTestFrame(conn)
	if err != nil {
		return err
	}
	connect, ok := f.(*frame.ConnectPacket)
	if !ok {
		return fmt.Errorf("server read %T, want CONNECT", f)
	}
	if connect.UID != uid || connect.DeviceID != deviceID || connect.Token != token {
		return fmt.Errorf("connect identity = (%q,%q,%q), want (%q,%q,%q)", connect.UID, connect.DeviceID, connect.Token, uid, deviceID, token)
	}
	return writeTestFrame(conn, &frame.ConnackPacket{
		Framer: frame.Framer{
			FrameType:        frame.CONNACK,
			HasServerVersion: true,
		},
		ServerVersion: frame.LatestVersion,
		ReasonCode:    frame.ReasonSuccess,
	})
}

type fakePoolClient struct {
	resultID int64
}

func (f fakePoolClient) SendBatch(context.Context, []Message) ([]SendResult, error) {
	return []SendResult{{MessageID: f.resultID, ReasonCode: frame.ReasonSuccess}}, nil
}

func (f fakePoolClient) Close() error { return nil }

type closeTrackingPoolClient struct {
	closed bool
}

func (c *closeTrackingPoolClient) SendBatch(context.Context, []Message) ([]SendResult, error) {
	return []SendResult{{MessageID: 1, ReasonCode: frame.ReasonSuccess}}, nil
}

func (c *closeTrackingPoolClient) Close() error {
	c.closed = true
	return nil
}

func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
