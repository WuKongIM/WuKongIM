package client

import (
	"fmt"
	"net"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

// readerLoop reads WKProto frames from one captured TCP stream and routes them.
func (c *Client) readerLoop(conn net.Conn, pending *pendingTracker, session *wkprotoenc.SessionCrypto) {
	if conn == nil {
		c.failRead(nil, nil, ErrNotConnected)
		return
	}

	scratch := make([]byte, c.cfg.ReadBufferSize)
	buf := make([]byte, 0, c.cfg.ReadBufferSize)
	for {
		n, readErr := conn.Read(scratch)
		if n > 0 {
			buf = append(buf, scratch[:n]...)
			for len(buf) > 0 {
				f, consumed, decodeErr := c.proto.DecodeFrame(buf, frame.LatestVersion)
				if decodeErr != nil {
					c.failRead(conn, pending, decodeErr)
					return
				}
				if f == nil || consumed == 0 {
					break
				}
				detachPayload(f)
				if err := c.routeInboundFrameWithPending(f, pending, session, conn); err != nil {
					c.failRead(conn, pending, err)
					return
				}
				buf = buf[consumed:]
			}
		}
		if readErr != nil {
			c.failRead(conn, pending, readErr)
			return
		}
	}
}

// detachPayload copies payload slices that were decoded from the shared read buffer.
func detachPayload(f frame.Frame) {
	switch pkt := f.(type) {
	case *frame.SendPacket:
		pkt.Payload = append([]byte(nil), pkt.Payload...)
	case *frame.RecvPacket:
		pkt.Payload = append([]byte(nil), pkt.Payload...)
	}
}

// routeInboundFrame dispatches decoded inbound frames to pending sends or receive buffers.
func (c *Client) routeInboundFrame(f frame.Frame) error {
	c.mu.Lock()
	pending := c.pending
	session := c.crypto.currentSession()
	conn := c.conn
	c.mu.Unlock()
	return c.routeInboundFrameWithPending(f, pending, session, conn)
}

func (c *Client) routeInboundFrameWithPending(f frame.Frame, pending *pendingTracker, session *wkprotoenc.SessionCrypto, conn net.Conn) error {
	switch pkt := f.(type) {
	case *frame.SendackPacket:
		if pending != nil {
			pending.resolve(pkt)
		}
		return nil
	case *frame.RecvPacket:
		if err := c.decryptRecv(pkt, session); err != nil {
			return err
		}
		c.enqueueRecv(pkt)
		if c.cfg.AutoRecvAck {
			_ = c.writeControlToConn(nil, &frame.RecvackPacket{MessageID: pkt.MessageID, MessageSeq: pkt.MessageSeq}, conn, false)
		}
		return nil
	case *frame.PongPacket:
		return nil
	case *frame.DisconnectPacket:
		return fmt.Errorf("client: disconnect reason=%s message=%q", pkt.ReasonCode, pkt.Reason)
	default:
		return nil
	}
}

// decryptRecv decrypts encrypted RECV payloads when the negotiated session requires it.
func (c *Client) decryptRecv(pkt *frame.RecvPacket, session *wkprotoenc.SessionCrypto) error {
	if pkt == nil || pkt.Setting.IsSet(frame.SettingNoEncrypt) || session == nil {
		return nil
	}
	payload, err := wkprotoenc.DecryptPayloadWithCrypto(pkt.Payload, session)
	if err != nil {
		return recvDecryptError(pkt, err)
	}
	pkt.Payload = payload
	return nil
}

func recvDecryptError(pkt *frame.RecvPacket, err error) error {
	if pkt == nil {
		return fmt.Errorf("client: decrypt recv payload: packet is nil: %w", err)
	}
	const maxPrefix = 32
	prefix := pkt.Payload
	if len(prefix) > maxPrefix {
		prefix = prefix[:maxPrefix]
	}
	return fmt.Errorf(
		"client: decrypt recv payload: channel_id=%q channel_type=%d from_uid=%q client_msg_no=%q message_id=%d message_seq=%d setting=%d msg_key_empty=%t payload_len=%d payload_prefix=%q payload_prefix_hex=%x: %w",
		pkt.ChannelID,
		pkt.ChannelType,
		pkt.FromUID,
		pkt.ClientMsgNo,
		pkt.MessageID,
		pkt.MessageSeq,
		pkt.Setting.Uint8(),
		pkt.MsgKey == "",
		len(pkt.Payload),
		string(prefix),
		prefix,
		err,
	)
}

// enqueueRecv appends a RECV packet, dropping the oldest queued packet when full.
func (c *Client) enqueueRecv(pkt *frame.RecvPacket) {
	if pkt == nil || c.recvCh == nil || cap(c.recvCh) == 0 {
		return
	}
	c.recvMu.Lock()
	defer c.recvMu.Unlock()
	select {
	case c.recvCh <- pkt:
		return
	default:
	}
	select {
	case <-c.recvCh:
	default:
	}
	select {
	case c.recvCh <- pkt:
	default:
	}
}

// failRead terminates the failed reader session without closing newer sessions.
func (c *Client) failRead(conn net.Conn, pending *pendingTracker, err error) {
	if err == nil {
		err = net.ErrClosed
	}
	var closePending bool
	c.mu.Lock()
	if conn != nil {
		if c.conn != conn {
			c.mu.Unlock()
			return
		}
		c.conn = nil
		if c.pending == pending {
			c.pending = newPendingTracker()
		}
		c.signalRecvUnavailableLocked(err)
		closePending = true
	}
	c.mu.Unlock()
	if closePending && pending != nil {
		pending.close(err)
	}
	if conn != nil {
		_ = conn.Close()
	}
}
