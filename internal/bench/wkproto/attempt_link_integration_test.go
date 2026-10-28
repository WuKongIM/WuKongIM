//go:build integration

package wkproto

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestAttemptLinkBenchmarkReconnectUsesFreshActualConnectKey(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	packets := make(chan *frame.ConnectPacket, 2)
	release := make(chan struct{})
	done := make(chan struct{})
	var once sync.Once
	releaseServer := func() { once.Do(func() { close(release) }) }
	defer releaseServer()
	go func() {
		defer close(done)
		var connections []net.Conn
		defer func() {
			for _, c := range connections {
				_ = c.Close()
			}
		}()
		for i := 0; i < 2; i++ {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			connections = append(connections, conn)
			f, e := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
			if e != nil {
				t.Error(e)
				return
			}
			packet, ok := f.(*frame.ConnectPacket)
			if !ok {
				t.Error("not CONNECT")
				return
			}
			packets <- packet
			writeFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})
		}
		<-release
	}()
	c, err := NewClient(ClientConfig{Addr: listener.Addr().String(), OperationTimeout: time.Second, EnableAttemptLinkDiagnostics: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var digests [2][16]byte
	var keys [2]string
	for i := 0; i < 2; i++ {
		if err = c.Connect(context.Background(), "private-user", "private-device"); err != nil {
			t.Fatal(err)
		}
		packet := <-packets
		digests[i] = c.ConnectFingerprint()
		keys[i] = packet.ClientKey
		if digests[i] != frame.HandshakeFingerprint(packet.ClientKey, packet.ClientTimestamp) {
			t.Fatal("adapter scope not actual CONNECT tuple")
		}
	}
	if keys[0] == keys[1] || digests[0] == digests[1] {
		t.Fatal("closed benchmark wrapper did not create fresh inner crypto at reconnect")
	}
	// This proves this wrapper's fresh-inner behavior only; generic reused
	// tuple/duplicate packet uniqueness remains unknown on the product API.
	_ = c.Close()
	listener.Close()
	releaseServer()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wire fixture did not join")
	}
}
