package client

import (
	"context"
	"fmt"
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

func serveSendacksUntilClose(conn net.Conn) error {
	defer conn.Close()
	proto := codec.New()
	f, err := proto.DecodePacketWithConn(conn, frame.LatestVersion)
	if err != nil {
		return err
	}
	if _, ok := f.(*frame.ConnectPacket); !ok {
		return fmt.Errorf("test server got %T, want *frame.ConnectPacket", f)
	}
	if err := writeBenchmarkFrame(proto, conn, &frame.ConnackPacket{
		Framer: frame.Framer{
			FrameType:        frame.CONNACK,
			HasServerVersion: true,
		},
		ServerVersion: frame.LatestVersion,
		ReasonCode:    frame.ReasonSuccess,
		NodeId:        1,
	}); err != nil {
		return err
	}
	for {
		f, err := proto.DecodePacketWithConn(conn, frame.LatestVersion)
		if err != nil {
			return err
		}
		send, ok := f.(*frame.SendPacket)
		if !ok {
			return fmt.Errorf("test server got %T, want *frame.SendPacket", f)
		}
		if err := writeBenchmarkFrame(proto, conn, &frame.SendackPacket{
			Framer:      frame.Framer{FrameType: frame.SENDACK},
			ClientSeq:   send.ClientSeq,
			ClientMsgNo: send.ClientMsgNo,
			MessageID:   int64(send.ClientSeq),
			MessageSeq:  send.ClientSeq,
			ReasonCode:  frame.ReasonSuccess,
		}); err != nil {
			return err
		}
	}
}
