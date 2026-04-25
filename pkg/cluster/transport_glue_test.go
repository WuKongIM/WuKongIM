package cluster

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestTransportLayerStartInitializesServerAndClients(t *testing.T) {
	cfg := validTestConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	discovery := NewStaticDiscovery(cfg.Nodes)
	layer := newTransportLayer(cfg, discovery, nil)

	err := layer.Start(
		cfg.ListenAddr,
		func([]byte) {},
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
	)
	if err != nil {
		t.Fatalf("transportLayer.Start() error = %v", err)
	}
	t.Cleanup(layer.Stop)

	if layer.server == nil || layer.server.Listener() == nil {
		t.Fatal("transportLayer.Start() did not initialize server listener")
	}
	if layer.rpcMux == nil {
		t.Fatal("transportLayer.Start() did not initialize rpc mux")
	}
	if layer.raftClient == nil || layer.fwdClient == nil {
		t.Fatal("transportLayer.Start() did not initialize clients")
	}
}

func TestTransportLayerWiresTransportObserver(t *testing.T) {
	cfg := validTestConfig()
	cfg.ListenAddr = "127.0.0.1:0"

	sendTarget, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer sendTarget.Close()
	cfg.Nodes[0].Addr = sendTarget.Addr().String()

	var (
		sentCalls     int
		receivedCalls int
		sentType      uint8
		receivedType  uint8
		sentBytes     int
		receivedBytes int
	)
	cfg.TransportObserver = transport.ObserverHooks{
		OnSend: func(msgType uint8, bytes int) {
			sentCalls++
			sentType = msgType
			sentBytes = bytes
		},
		OnReceive: func(msgType uint8, bytes int) {
			receivedCalls++
			receivedType = msgType
			receivedBytes = bytes
		},
	}

	discovery := NewStaticDiscovery(cfg.Nodes)
	layer := newTransportLayer(cfg, discovery, nil)

	received := make(chan []byte, 1)
	err = layer.Start(
		cfg.ListenAddr,
		func(body []byte) {
			received <- append([]byte(nil), body...)
		},
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
		func(context.Context, []byte) ([]byte, error) { return nil, nil },
	)
	if err != nil {
		t.Fatalf("transportLayer.Start() error = %v", err)
	}
	t.Cleanup(layer.Stop)

	go func() {
		conn, err := sendTarget.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _, _ = transport.ReadMessage(conn)
	}()

	sendPayload := []byte("raft-send")
	if err := layer.raftClient.Send(uint64(cfg.NodeID), 0, msgTypeRaft, sendPayload); err != nil {
		t.Fatalf("raftClient.Send() error = %v", err)
	}

	conn, err := net.Dial("tcp", layer.server.Listener().Addr().String())
	if err != nil {
		t.Fatalf("net.Dial() error = %v", err)
	}
	defer conn.Close()
	receivePayload := []byte("raft-receive")
	if err := transport.WriteMessage(conn, msgTypeRaft, receivePayload); err != nil {
		t.Fatalf("transport.WriteMessage() error = %v", err)
	}

	select {
	case got := <-received:
		if string(got) != string(receivePayload) {
			t.Fatalf("received payload = %q, want %q", got, receivePayload)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for receive")
	}

	waitForTransportLayerObserver(t, func() bool { return sentCalls == 1 && receivedCalls == 1 })
	if sentType != msgTypeRaft || receivedType != msgTypeRaft {
		t.Fatalf("observer msg types sent=%d received=%d, want both %d", sentType, receivedType, msgTypeRaft)
	}
	if sentBytes != len(sendPayload) || receivedBytes != len(receivePayload) {
		t.Fatalf("observer bytes sent=%d received=%d, want %d/%d", sentBytes, receivedBytes, len(sendPayload), len(receivePayload))
	}
}

func waitForTransportLayerObserver(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timeout waiting for transport observer")
}
