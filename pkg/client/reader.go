package client

import (
	"fmt"
	"net"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

// readerLoop reads WKProto frames from the active TCP stream and routes them.
func (c *Client) readerLoop() {
	conn, err := c.currentConn()
	if err != nil {
		c.failRead(err)
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
					c.failRead(decodeErr)
					return
				}
				if f == nil || consumed == 0 {
					break
				}
				detachPayload(f)
				if err := c.routeInboundFrame(f); err != nil {
					c.failRead(err)
					return
				}
				buf = buf[consumed:]
			}
		}
		if readErr != nil {
			c.failRead(readErr)
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
	switch pkt := f.(type) {
	case *frame.SendackPacket:
		if c.pending != nil {
			c.pending.resolve(pkt)
		}
		return nil
	case *frame.RecvPacket:
		if err := c.decryptRecv(pkt); err != nil {
			return err
		}
		c.enqueueRecv(pkt)
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
func (c *Client) decryptRecv(pkt *frame.RecvPacket) error {
	if pkt == nil || pkt.Setting.IsSet(frame.SettingNoEncrypt) || c.crypto == nil || c.crypto.session == nil {
		return nil
	}
	payload, err := wkprotoenc.DecryptPayloadWithCrypto(pkt.Payload, c.crypto.session)
	if err != nil {
		return err
	}
	pkt.Payload = payload
	return nil
}

// enqueueRecv appends a RECV packet, dropping the oldest queued packet when full.
func (c *Client) enqueueRecv(pkt *frame.RecvPacket) {
	if pkt == nil || c.recvCh == nil {
		return
	}
	select {
	case c.recvCh <- pkt:
		return
	default:
	}
	select {
	case <-c.recvCh:
	default:
	}
	c.recvCh <- pkt
}

// failRead terminates pending sends and closes the client after a reader failure.
func (c *Client) failRead(err error) {
	if err == nil {
		err = net.ErrClosed
	}
	if c.pending != nil {
		c.pending.close(err)
	}
	_ = c.Close()
}
