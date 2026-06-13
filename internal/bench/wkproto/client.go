package wkproto

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	defaultOperationTimeout = 5 * time.Second
	defaultFrameBufferSize  = 1024
)

var errClientNotConnected = errors.New("wkproto client: not connected")

// ClientConfig controls one production WKProto TCP client connection.
type ClientConfig struct {
	// Addr is the gateway TCP address in host:port form.
	Addr string
	// Token is the default connect token sent when callers do not override it.
	Token string
	// Dialer overrides TCP dialing for tests; nil uses net.Dialer.
	Dialer interface {
		DialContext(context.Context, string, string) (net.Conn, error)
	}
	// OperationTimeout bounds handshake, read, and write operations when ctx has no deadline.
	OperationTimeout time.Duration
	// FrameBufferSize bounds decoded inbound frames queued by the background reader.
	FrameBufferSize int
}

// Client is a black-box WKProto client used by wkbench workers.
type Client struct {
	inner            *wkclient.Client
	token            string
	operationTimeout time.Duration
	frameBufferSize  int

	mu      sync.Mutex
	frameCh chan frameResult
	stopCh  chan struct{}

	// pendingMu protects pendingSendacks and pendingDone.
	pendingMu sync.Mutex
	// pendingSendacks counts SEND futures that still need to publish a SENDACK frame.
	pendingSendacks int
	// pendingDone closes whenever pendingSendacks reaches zero.
	pendingDone chan struct{}
}

type frameResult struct {
	frame frame.Frame
	err   error
}

// NewClient builds a WKProto client for a single gateway address.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.Addr == "" {
		return nil, fmt.Errorf("wkproto client: addr is required")
	}
	if cfg.Dialer == nil {
		cfg.Dialer = &net.Dialer{}
	}
	if cfg.OperationTimeout <= 0 {
		cfg.OperationTimeout = defaultOperationTimeout
	}
	if cfg.FrameBufferSize <= 0 {
		cfg.FrameBufferSize = defaultFrameBufferSize
	}
	inner, err := wkclient.New(wkclient.Config{
		Addr:                   cfg.Addr,
		Token:                  cfg.Token,
		Dialer:                 cfg.Dialer,
		OperationTimeout:       cfg.OperationTimeout,
		AckTimeout:             cfg.OperationTimeout,
		InboundFrameBufferSize: cfg.FrameBufferSize,
	})
	if err != nil {
		return nil, err
	}
	return &Client{
		inner:            inner,
		token:            cfg.Token,
		operationTimeout: cfg.OperationTimeout,
		frameBufferSize:  cfg.FrameBufferSize,
	}, nil
}

// Connect opens the TCP connection and completes the WKProto connect/connack handshake.
func (c *Client) Connect(ctx context.Context, uid, deviceID string) error {
	if c == nil || c.inner == nil {
		return errClientNotConnected
	}
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()

	if _, err := c.inner.Connect(ctx, wkclient.ConnectOptions{
		UID:        uid,
		DeviceID:   deviceID,
		DeviceFlag: frame.APP,
		Token:      c.token,
	}); err != nil {
		return err
	}

	frameCh := make(chan frameResult, c.frameBufferSize)
	stopCh := make(chan struct{})
	c.mu.Lock()
	oldStop := c.stopCh
	c.frameCh = frameCh
	c.stopCh = stopCh
	c.mu.Unlock()
	if oldStop != nil {
		close(oldStop)
	}
	go c.forwardReadFrames(frameCh, stopCh)
	return nil
}

// Send writes one send packet for the connected client.
func (c *Client) Send(ctx context.Context, pkt *frame.SendPacket) error {
	if c == nil || c.inner == nil {
		return errClientNotConnected
	}
	if pkt == nil {
		return fmt.Errorf("wkproto client: send packet is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	c.beginPendingSendack()
	future, err := c.inner.SendAsync(ctx, wkclient.Message{
		Setting:     pkt.Setting,
		Expire:      pkt.Expire,
		ClientSeq:   pkt.ClientSeq,
		ClientMsgNo: pkt.ClientMsgNo,
		ChannelID:   pkt.ChannelID,
		ChannelType: pkt.ChannelType,
		Topic:       pkt.Topic,
		Payload:     pkt.Payload,
	})
	if err != nil {
		c.finishPendingSendack()
		return err
	}
	frameCh, stopCh, err := c.currentFrameQueue()
	if err != nil {
		c.finishPendingSendack()
		return err
	}
	go c.forwardSendack(future, frameCh, stopCh)
	return nil
}

// ReadFrame reads one SENDACK or RECV frame from the connected client stream.
func (c *Client) ReadFrame(ctx context.Context) (frame.Frame, error) {
	if c == nil {
		return nil, errClientNotConnected
	}
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()
	frameCh, stopCh, err := c.currentFrameQueue()
	if err != nil {
		return nil, err
	}
	select {
	case result := <-frameCh:
		if result.err != nil {
			return nil, result.err
		}
		return result.frame, nil
	case <-stopCh:
		return nil, errClientNotConnected
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// RecvAck sends one receive acknowledgment for a delivered message.
func (c *Client) RecvAck(ctx context.Context, messageID int64, messageSeq uint64) error {
	if c == nil || c.inner == nil {
		return errClientNotConnected
	}
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()
	return c.inner.RecvAck(ctx, messageID, messageSeq)
}

// Ping sends a WKProto heartbeat ping frame on the active connection.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.inner == nil {
		return errClientNotConnected
	}
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()
	return c.inner.Ping(ctx)
}

// Close closes the active TCP connection, if any.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	stopCh := c.stopCh
	c.stopCh = nil
	c.frameCh = nil
	c.mu.Unlock()
	if stopCh != nil {
		close(stopCh)
	}
	if c.inner == nil {
		return nil
	}
	return c.inner.Close()
}

func (c *Client) forwardReadFrames(frameCh chan<- frameResult, stopCh <-chan struct{}) {
	for {
		f, err := c.inner.ReadFrame(context.Background())
		if err != nil {
			if !c.waitPendingSendacks(stopCh) {
				return
			}
			c.publishFrameResult(frameCh, stopCh, frameResult{err: err})
			return
		}
		c.publishFrameResult(frameCh, stopCh, frameResult{frame: f})
	}
}

func (c *Client) forwardSendack(future *wkclient.SendFuture, frameCh chan<- frameResult, stopCh <-chan struct{}) {
	defer c.finishPendingSendack()
	result, err := future.Wait(context.Background())
	if err != nil && result.ClientSeq == 0 && result.ClientMsgNo == "" {
		c.publishFrameResult(frameCh, stopCh, frameResult{err: err})
		return
	}
	ack := &frame.SendackPacket{
		ClientSeq:   result.ClientSeq,
		ClientMsgNo: result.ClientMsgNo,
		MessageID:   result.MessageID,
		MessageSeq:  result.MessageSeq,
		ReasonCode:  result.ReasonCode,
	}
	c.publishFrameResult(frameCh, stopCh, frameResult{frame: ack})
}

func (c *Client) publishFrameResult(frameCh chan<- frameResult, stopCh <-chan struct{}, result frameResult) {
	select {
	case frameCh <- result:
	case <-stopCh:
	}
}

func (c *Client) beginPendingSendack() {
	c.pendingMu.Lock()
	if c.pendingSendacks == 0 {
		c.pendingDone = make(chan struct{})
	}
	c.pendingSendacks++
	c.pendingMu.Unlock()
}

func (c *Client) finishPendingSendack() {
	c.pendingMu.Lock()
	if c.pendingSendacks > 0 {
		c.pendingSendacks--
		if c.pendingSendacks == 0 {
			close(c.pendingDone)
		}
	}
	c.pendingMu.Unlock()
}

func (c *Client) waitPendingSendacks(stopCh <-chan struct{}) bool {
	for {
		c.pendingMu.Lock()
		if c.pendingSendacks == 0 {
			c.pendingMu.Unlock()
			return true
		}
		done := c.pendingDone
		c.pendingMu.Unlock()

		select {
		case <-done:
		case <-stopCh:
			return false
		}
	}
}

func (c *Client) currentFrameQueue() (chan frameResult, <-chan struct{}, error) {
	if c == nil {
		return nil, nil, errClientNotConnected
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frameCh == nil || c.stopCh == nil {
		return nil, nil, errClientNotConnected
	}
	return c.frameCh, c.stopCh, nil
}

func (c *Client) withDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok || c.operationTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.operationTimeout)
}
