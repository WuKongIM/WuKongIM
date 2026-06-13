package client

import (
	"context"
	"net"
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

func newPipeClientServer(cfg Config) (*Client, net.Conn, error) {
	clientConn, serverConn := net.Pipe()
	cfg.Addr = "pipe"
	cfg.Dialer = fakeDialer{conn: clientConn}
	cfg.OperationTimeout = time.Second

	c, err := New(cfg)
	if err != nil {
		_ = clientConn.Close()
		_ = serverConn.Close()
		return nil, nil, err
	}
	return c, serverConn, nil
}

func readTestFrame(conn net.Conn) (frame.Frame, error) {
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return nil, err
	}
	f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func writeTestFrame(conn net.Conn, f frame.Frame) error {
	if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	data, err := codec.New().EncodeFrame(f, frame.LatestVersion)
	if err != nil {
		return err
	}
	if _, err := conn.Write(data); err != nil {
		return err
	}
	return nil
}
