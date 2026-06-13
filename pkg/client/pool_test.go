package client

import (
	"context"
	"fmt"
	"net"
	"testing"

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
