//go:build e2e

package suite

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const defaultWKProtoTimeout = 5 * time.Second

// WKProtoClient is a black-box test client for the public WKProto transport.
type WKProtoClient struct {
	conn   net.Conn
	crypto *testkit.WKProtoClient
}

// NewWKProtoClient creates a client with fresh WKProto session keys.
func NewWKProtoClient() (*WKProtoClient, error) {
	crypto, err := testkit.NewWKProtoClient()
	if err != nil {
		return nil, err
	}
	return &WKProtoClient{crypto: crypto}, nil
}

// Connect opens the TCP connection and completes the WKProto handshake.
func (c *WKProtoClient) Connect(addr, uid, deviceID string) error {
	_, err := c.ConnectContext(context.Background(), addr, uid, deviceID)
	return err
}

// ConnectContext opens the TCP connection and returns the successful Connack.
func (c *WKProtoClient) ConnectContext(ctx context.Context, addr, uid, deviceID string) (*frame.ConnackPacket, error) {
	dialer := net.Dialer{Timeout: defaultWKProtoTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c.conn = conn

	if deadline, ok := ctx.Deadline(); ok {
		if err := c.conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
		defer func() { _ = c.conn.SetDeadline(time.Time{}) }()
	}

	connect := &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             uid,
		DeviceID:        deviceID,
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
	}
	if err := c.SendFrame(connect); err != nil {
		return nil, err
	}

	f, err := c.ReadFrame()
	if err != nil {
		return nil, err
	}
	connack, ok := f.(*frame.ConnackPacket)
	if !ok {
		return nil, fmt.Errorf("wkproto connect: expected *frame.ConnackPacket, got %T", f)
	}
	if connack.ReasonCode != frame.ReasonSuccess {
		return nil, fmt.Errorf("wkproto connect: unexpected reason code %s", connack.ReasonCode)
	}
	return connack, nil
}

// SendFrame encodes and writes one frame to the connection.
func (c *WKProtoClient) SendFrame(f frame.Frame) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("wkproto client: not connected")
	}

	switch pkt := f.(type) {
	case *frame.ConnectPacket:
		cloned, err := c.crypto.UseClientKey(pkt)
		if err != nil {
			return err
		}
		f = cloned
	case *frame.SendPacket:
		cloned := *pkt
		if err := c.crypto.EncryptSendPacket(&cloned); err != nil {
			return err
		}
		f = &cloned
	}

	payload, err := codec.New().EncodeFrame(f, frame.LatestVersion)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(payload)
	return err
}

// ReadFrame reads one frame and applies client-side crypto when needed.
func (c *WKProtoClient) ReadFrame() (frame.Frame, error) {
	if c == nil || c.conn == nil {
		return nil, fmt.Errorf("wkproto client: not connected")
	}
	if err := c.conn.SetReadDeadline(time.Now().Add(defaultWKProtoTimeout)); err != nil {
		return nil, err
	}
	defer func() { _ = c.conn.SetReadDeadline(time.Time{}) }()

	f, err := codec.New().DecodePacketWithConn(c.conn, frame.LatestVersion)
	if err != nil {
		return nil, err
	}

	switch pkt := f.(type) {
	case *frame.ConnackPacket:
		if err := c.crypto.ApplyConnack(pkt); err != nil {
			return nil, err
		}
	case *frame.RecvPacket:
		if err := c.crypto.DecryptRecvPacket(pkt); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// ReadSendAck reads one send-ack packet.
func (c *WKProtoClient) ReadSendAck() (*frame.SendackPacket, error) {
	f, err := c.ReadFrame()
	if err != nil {
		return nil, err
	}
	ack, ok := f.(*frame.SendackPacket)
	if !ok {
		return nil, fmt.Errorf("wkproto client: expected *frame.SendackPacket, got %T", f)
	}
	return ack, nil
}

// ReadRecv reads one recv packet.
func (c *WKProtoClient) ReadRecv() (*frame.RecvPacket, error) {
	f, err := c.ReadFrame()
	if err != nil {
		return nil, err
	}
	recv, ok := f.(*frame.RecvPacket)
	if !ok {
		return nil, fmt.Errorf("wkproto client: expected *frame.RecvPacket, got %T", f)
	}
	return recv, nil
}

// RecvAck sends one recv-ack packet for an already received message.
func (c *WKProtoClient) RecvAck(messageID int64, messageSeq uint64) error {
	return c.SendFrame(&frame.RecvackPacket{
		MessageID:  messageID,
		MessageSeq: messageSeq,
	})
}

// Close closes the underlying TCP connection.
func (c *WKProtoClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}
