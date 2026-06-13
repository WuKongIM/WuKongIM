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
	defaultAckTimeoutSlack  = time.Second
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
	// AckTimeout bounds the internal pending SENDACK wait; use a value above workload waits.
	AckTimeout time.Duration
	// FrameBufferSize bounds decoded inbound frames queued by the background reader.
	FrameBufferSize int
}

type tcpDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// Client is a black-box WKProto client used by wkbench workers.
type Client struct {
	addr             string
	token            string
	dialer           tcpDialer
	operationTimeout time.Duration
	ackTimeout       time.Duration
	frameBufferSize  int

	mu      sync.Mutex
	session *clientSession
}

type clientSession struct {
	// inner owns the TCP session, WKProto crypto, reader, writer, and SENDACK matching.
	inner *wkclient.Client
	// frameCh is the bench-facing queue of synthetic SENDACKs and forwarded RECVs.
	frameCh chan frameResult
	// stopCh closes when this session is no longer active.
	stopCh chan struct{}
	// closeOnce makes session shutdown idempotent.
	closeOnce sync.Once
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
	if cfg.AckTimeout <= 0 {
		cfg.AckTimeout = cfg.OperationTimeout + defaultAckTimeoutSlack
	}
	if cfg.FrameBufferSize <= 0 {
		cfg.FrameBufferSize = defaultFrameBufferSize
	}
	return &Client{
		addr:             cfg.Addr,
		token:            cfg.Token,
		dialer:           cfg.Dialer,
		operationTimeout: cfg.OperationTimeout,
		ackTimeout:       cfg.AckTimeout,
		frameBufferSize:  cfg.FrameBufferSize,
	}, nil
}

// Connect opens the TCP connection and completes the WKProto connect/connack handshake.
func (c *Client) Connect(ctx context.Context, uid, deviceID string) error {
	if c == nil {
		return errClientNotConnected
	}
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()

	inner, err := c.newInner()
	if err != nil {
		return err
	}
	if _, err := inner.Connect(ctx, wkclient.ConnectOptions{
		UID:        uid,
		DeviceID:   deviceID,
		DeviceFlag: frame.APP,
		Token:      c.token,
	}); err != nil {
		_ = inner.Close()
		return err
	}

	session := newClientSession(inner, c.frameBufferSize)
	c.mu.Lock()
	oldSession := c.session
	c.session = session
	c.mu.Unlock()
	if oldSession != nil {
		_ = oldSession.close()
	}
	go c.forwardReadFrames(session)
	return nil
}

// Send writes one send packet for the connected client.
func (c *Client) Send(ctx context.Context, pkt *frame.SendPacket) error {
	if pkt == nil {
		return fmt.Errorf("wkproto client: send packet is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	session, err := c.currentSession()
	if err != nil {
		return err
	}

	session.beginPendingSendack()
	future, err := session.inner.SendAsync(ctx, wkclient.Message{
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
		session.finishPendingSendack()
		return err
	}
	go c.forwardSendack(session, future)
	return nil
}

// ReadFrame reads one SENDACK or RECV frame from the connected client stream.
func (c *Client) ReadFrame(ctx context.Context) (frame.Frame, error) {
	if c == nil {
		return nil, errClientNotConnected
	}
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()
	session, err := c.currentSession()
	if err != nil {
		return nil, err
	}
	select {
	case result := <-session.frameCh:
		if result.err != nil {
			return nil, result.err
		}
		return result.frame, nil
	case <-session.stopCh:
		return nil, errClientNotConnected
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// RecvAck sends one receive acknowledgment for a delivered message.
func (c *Client) RecvAck(ctx context.Context, messageID int64, messageSeq uint64) error {
	session, err := c.currentSession()
	if err != nil {
		return err
	}
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()
	return session.inner.RecvAck(ctx, messageID, messageSeq)
}

// Ping sends a WKProto heartbeat ping frame on the active connection.
func (c *Client) Ping(ctx context.Context) error {
	session, err := c.currentSession()
	if err != nil {
		return err
	}
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()
	return session.inner.Ping(ctx)
}

// Close closes the active TCP connection, if any.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	session := c.session
	c.session = nil
	c.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.close()
}

func (c *Client) forwardReadFrames(session *clientSession) {
	for {
		f, err := session.inner.ReadFrame(context.Background())
		if err != nil {
			if !session.waitPendingSendacks() {
				return
			}
			session.publishFrameResult(frameResult{err: err})
			return
		}
		session.publishFrameResult(frameResult{frame: f})
	}
}

func (c *Client) forwardSendack(session *clientSession, future *wkclient.SendFuture) {
	result, err := future.Wait(context.Background())
	if err != nil && result.ClientSeq == 0 && result.ClientMsgNo == "" {
		session.finishPendingSendack()
		session.publishFrameResult(frameResult{err: err})
		return
	}
	ack := &frame.SendackPacket{
		ClientSeq:   result.ClientSeq,
		ClientMsgNo: result.ClientMsgNo,
		MessageID:   result.MessageID,
		MessageSeq:  result.MessageSeq,
		ReasonCode:  result.ReasonCode,
	}
	session.finishPendingSendack()
	session.publishFrameResult(frameResult{frame: ack})
}

func (c *Client) publishFrameResult(frameCh chan frameResult, stopCh <-chan struct{}, result frameResult) {
	publishFrameResult(frameCh, stopCh, result)
}

func (s *clientSession) publishFrameResult(result frameResult) {
	publishFrameResult(s.frameCh, s.stopCh, result)
}

func publishFrameResult(frameCh chan frameResult, stopCh <-chan struct{}, result frameResult) {
	select {
	case frameCh <- result:
		return
	case <-stopCh:
		return
	default:
	}
	if !isPriorityFrameResult(result) {
		return
	}
	buffered := make([]frameResult, 0, len(frameCh))
	droppedNonPriority := false
	for {
		select {
		case queued := <-frameCh:
			if !droppedNonPriority && !isPriorityFrameResult(queued) {
				droppedNonPriority = true
				continue
			}
			buffered = append(buffered, queued)
		default:
			goto drained
		}
	}

drained:
	for _, queued := range buffered {
		select {
		case frameCh <- queued:
		case <-stopCh:
			return
		default:
			return
		}
	}
	if !droppedNonPriority {
		return
	}
	select {
	case frameCh <- result:
	case <-stopCh:
	default:
	}
}

func isPriorityFrameResult(result frameResult) bool {
	if result.err != nil {
		return true
	}
	_, ok := result.frame.(*frame.SendackPacket)
	return ok
}

func newClientSession(inner *wkclient.Client, frameBufferSize int) *clientSession {
	return &clientSession{
		inner:   inner,
		frameCh: make(chan frameResult, frameBufferSize),
		stopCh:  make(chan struct{}),
	}
}

func (s *clientSession) close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.stopCh)
		if s.inner != nil {
			err = s.inner.Close()
			if errors.Is(err, wkclient.ErrClosed) {
				err = nil
			}
		}
	})
	return err
}

func (s *clientSession) beginPendingSendack() {
	s.pendingMu.Lock()
	if s.pendingSendacks == 0 {
		s.pendingDone = make(chan struct{})
	}
	s.pendingSendacks++
	s.pendingMu.Unlock()
}

func (s *clientSession) finishPendingSendack() {
	s.pendingMu.Lock()
	if s.pendingSendacks > 0 {
		s.pendingSendacks--
		if s.pendingSendacks == 0 {
			close(s.pendingDone)
		}
	}
	s.pendingMu.Unlock()
}

func (s *clientSession) waitPendingSendacks() bool {
	for {
		s.pendingMu.Lock()
		if s.pendingSendacks == 0 {
			s.pendingMu.Unlock()
			return true
		}
		done := s.pendingDone
		s.pendingMu.Unlock()

		select {
		case <-done:
		case <-s.stopCh:
			return false
		}
	}
}

func (c *Client) currentSession() (*clientSession, error) {
	if c == nil {
		return nil, errClientNotConnected
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil {
		return nil, errClientNotConnected
	}
	return c.session, nil
}

func (c *Client) newInner() (*wkclient.Client, error) {
	return wkclient.New(wkclient.Config{
		Addr:                   c.addr,
		Token:                  c.token,
		Dialer:                 c.dialer,
		OperationTimeout:       c.operationTimeout,
		AckTimeout:             c.ackTimeout,
		InboundFrameBufferSize: c.frameBufferSize,
	})
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
