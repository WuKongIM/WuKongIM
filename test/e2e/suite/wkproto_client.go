//go:build e2e

package suite

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const defaultWKProtoTimeout = 5 * time.Second

// WKProtoClient is a black-box test client for the public WKProto transport.
type WKProtoClient struct {
	// mu protects the active pkg/client session and bridge channels.
	mu sync.Mutex
	// inner owns the WKProto TCP session, crypto state, writer, and reader.
	inner *wkclient.Client
	// ackCh receives synthetic SENDACK packets resolved by pkg/client futures.
	ackCh chan sendAckResult
	// recvCh receives decrypted RECV packets forwarded from pkg/client.
	recvCh chan recvResult
	// closeCh stops per-connection forwarding goroutines.
	closeCh chan struct{}
}

// NewWKProtoClient creates a client with fresh WKProto session keys.
func NewWKProtoClient() (*WKProtoClient, error) {
	return &WKProtoClient{}, nil
}

// Connect opens the TCP connection and completes the WKProto handshake.
func (c *WKProtoClient) Connect(addr, uid, deviceID string) error {
	_, err := c.ConnectContext(context.Background(), addr, uid, deviceID)
	return err
}

// ConnectContext opens the TCP connection and returns the successful Connack.
func (c *WKProtoClient) ConnectContext(ctx context.Context, addr, uid, deviceID string) (*frame.ConnackPacket, error) {
	if c == nil {
		return nil, fmt.Errorf("wkproto client: nil client")
	}
	_ = c.Close()
	if ctx == nil {
		ctx = context.Background()
	}

	inner, err := wkclient.New(wkclient.Config{
		Addr:                   addr,
		OperationTimeout:       defaultWKProtoTimeout,
		AckTimeout:             defaultWKProtoTimeout,
		InboundFrameBufferSize: 1024,
	})
	if err != nil {
		return nil, err
	}
	connack, err := inner.Connect(ctx, wkclient.ConnectOptions{
		UID:        uid,
		DeviceID:   deviceID,
		DeviceFlag: frame.APP,
	})
	if err != nil {
		_ = inner.Close()
		return nil, err
	}

	ackCh := make(chan sendAckResult, 1024)
	recvCh := make(chan recvResult, 1024)
	closeCh := make(chan struct{})

	c.mu.Lock()
	c.inner = inner
	c.ackCh = ackCh
	c.recvCh = recvCh
	c.closeCh = closeCh
	c.mu.Unlock()

	go c.forwardRecv(inner, recvCh, closeCh)
	return connack, nil
}

// SendFrame writes one supported test frame through the shared pkg/client session.
func (c *WKProtoClient) SendFrame(f frame.Frame) error {
	inner, ackCh, _, closeCh, err := c.session()
	if err != nil {
		return err
	}
	switch pkt := f.(type) {
	case *frame.ConnectPacket:
		return fmt.Errorf("wkproto client: CONNECT is handled by ConnectContext")
	case *frame.SendPacket:
		future, err := inner.SendAsync(context.Background(), messageFromSendPacket(pkt))
		if err != nil {
			return err
		}
		go publishSendAck(future, ackCh, closeCh)
		return nil
	case *frame.PingPacket:
		ctx, cancel := context.WithTimeout(context.Background(), defaultWKProtoTimeout)
		defer cancel()
		return inner.Ping(ctx)
	case *frame.RecvackPacket:
		ctx, cancel := context.WithTimeout(context.Background(), defaultWKProtoTimeout)
		defer cancel()
		return inner.RecvAck(ctx, pkt.MessageID, pkt.MessageSeq)
	default:
		return fmt.Errorf("wkproto client: unsupported frame %T", f)
	}
}

// ReadFrame reads one SENDACK or RECV packet exposed by this test client.
func (c *WKProtoClient) ReadFrame() (frame.Frame, error) {
	_, ackCh, recvCh, closeCh, err := c.session()
	if err != nil {
		return nil, err
	}
	timer := time.NewTimer(defaultWKProtoTimeout)
	defer timer.Stop()

	select {
	case result := <-ackCh:
		if result.err != nil {
			return nil, result.err
		}
		if result.ack == nil {
			return nil, fmt.Errorf("wkproto client: sendack result is empty")
		}
		return result.ack, nil
	case result := <-recvCh:
		if result.err != nil {
			return nil, result.err
		}
		if result.recv == nil {
			return nil, fmt.Errorf("wkproto client: recv result is empty")
		}
		return result.recv, nil
	case <-closeCh:
		return nil, fmt.Errorf("wkproto client: not connected")
	case <-timer.C:
		return nil, context.DeadlineExceeded
	}
}

// ReadSendAck reads one send-ack packet.
func (c *WKProtoClient) ReadSendAck() (*frame.SendackPacket, error) {
	_, ackCh, _, closeCh, err := c.session()
	if err != nil {
		return nil, err
	}
	timer := time.NewTimer(defaultWKProtoTimeout)
	defer timer.Stop()

	select {
	case result := <-ackCh:
		if result.err != nil {
			return nil, result.err
		}
		if result.ack == nil {
			return nil, fmt.Errorf("wkproto client: sendack result is empty")
		}
		return result.ack, nil
	case <-closeCh:
		return nil, fmt.Errorf("wkproto client: not connected")
	case <-timer.C:
		return nil, context.DeadlineExceeded
	}
}

// ReadRecv reads one recv packet.
func (c *WKProtoClient) ReadRecv() (*frame.RecvPacket, error) {
	_, _, recvCh, closeCh, err := c.session()
	if err != nil {
		return nil, err
	}
	timer := time.NewTimer(defaultWKProtoTimeout)
	defer timer.Stop()

	select {
	case result := <-recvCh:
		if result.err != nil {
			return nil, result.err
		}
		if result.recv == nil {
			return nil, fmt.Errorf("wkproto client: recv result is empty")
		}
		return result.recv, nil
	case <-closeCh:
		return nil, fmt.Errorf("wkproto client: not connected")
	case <-timer.C:
		return nil, context.DeadlineExceeded
	}
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
	if c == nil {
		return nil
	}
	c.mu.Lock()
	inner := c.inner
	closeCh := c.closeCh
	c.inner = nil
	c.ackCh = nil
	c.recvCh = nil
	c.closeCh = nil
	if closeCh != nil {
		close(closeCh)
	}
	c.mu.Unlock()
	if inner == nil {
		return nil
	}
	err := inner.Close()
	if errors.Is(err, wkclient.ErrClosed) {
		return nil
	}
	return err
}

func (c *WKProtoClient) session() (*wkclient.Client, chan sendAckResult, chan recvResult, <-chan struct{}, error) {
	if c == nil {
		return nil, nil, nil, nil, fmt.Errorf("wkproto client: nil client")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inner == nil {
		return nil, nil, nil, nil, fmt.Errorf("wkproto client: not connected")
	}
	return c.inner, c.ackCh, c.recvCh, c.closeCh, nil
}

func (c *WKProtoClient) forwardRecv(inner *wkclient.Client, recvCh chan<- recvResult, closeCh <-chan struct{}) {
	for {
		recv, err := inner.Recv(context.Background())
		result := recvResult{recv: recv, err: err}
		select {
		case recvCh <- result:
		case <-closeCh:
			return
		}
		if err != nil {
			return
		}
	}
}

func publishSendAck(future *wkclient.SendFuture, ackCh chan<- sendAckResult, closeCh <-chan struct{}) {
	result, err := future.Wait(context.Background())
	ack := sendResultToPacket(result)
	if ack != nil {
		err = nil
	}
	select {
	case ackCh <- sendAckResult{ack: ack, err: err}:
	case <-closeCh:
	}
}

func messageFromSendPacket(pkt *frame.SendPacket) wkclient.Message {
	if pkt == nil {
		return wkclient.Message{}
	}
	return wkclient.Message{
		Setting:     pkt.Setting,
		Expire:      pkt.Expire,
		ClientSeq:   pkt.ClientSeq,
		ClientMsgNo: pkt.ClientMsgNo,
		ChannelID:   pkt.ChannelID,
		ChannelType: pkt.ChannelType,
		Topic:       pkt.Topic,
		Payload:     append([]byte(nil), pkt.Payload...),
	}
}

func sendResultToPacket(result wkclient.SendResult) *frame.SendackPacket {
	if result.ClientSeq == 0 && result.ClientMsgNo == "" && result.MessageID == 0 && result.MessageSeq == 0 && result.ReasonCode == 0 {
		return nil
	}
	return &frame.SendackPacket{
		ClientSeq:   result.ClientSeq,
		ClientMsgNo: result.ClientMsgNo,
		MessageID:   result.MessageID,
		MessageSeq:  result.MessageSeq,
		ReasonCode:  result.ReasonCode,
	}
}

type sendAckResult struct {
	ack *frame.SendackPacket
	err error
}

type recvResult struct {
	recv *frame.RecvPacket
	err  error
}
