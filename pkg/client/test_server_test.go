package client

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type fakeDialer struct {
	conn net.Conn
}

func (d fakeDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return d.conn, nil
}

func newPipeClientServer(t *testing.T, cfg Config) (*Client, net.Conn) {
	t.Helper()

	clientConn, serverConn := net.Pipe()
	cfg.Addr = "pipe"
	cfg.Dialer = fakeDialer{conn: clientConn}
	cfg.OperationTimeout = time.Second

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		_ = serverConn.Close()
	})
	return c, serverConn
}

func readTestFrame(t *testing.T, conn net.Conn) frame.Frame {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
	if err != nil {
		t.Fatalf("DecodePacketWithConn() error = %v", err)
	}
	return f
}

func writeTestFrame(t *testing.T, conn net.Conn, f frame.Frame) {
	t.Helper()

	if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetWriteDeadline() error = %v", err)
	}
	data, err := codec.New().EncodeFrame(f, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame() error = %v", err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
}
