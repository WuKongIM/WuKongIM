package wkproto

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	protocolenc "github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
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
	addr   string
	dialer interface {
		DialContext(context.Context, string, string) (net.Conn, error)
	}
	operationTimeout time.Duration
	frameBufferSize  int
	proto            *codec.WKProto
	token            string

	mu            sync.Mutex
	readMu        sync.Mutex
	writeMu       sync.Mutex
	conn          net.Conn
	readBuf       []byte
	frameCh       chan frameResult
	readLoopStop  chan struct{}
	readErr       error
	privateKey    [32]byte
	publicKey     [32]byte
	sessionCrypto *protocolenc.SessionCrypto
}

type frameResult struct {
	frame frame.Frame
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
	private, public, err := protocolenc.GenerateKeyPair()
	if err != nil {
		return nil, err
	}
	return &Client{
		addr:             cfg.Addr,
		dialer:           cfg.Dialer,
		operationTimeout: cfg.OperationTimeout,
		frameBufferSize:  cfg.FrameBufferSize,
		proto:            codec.New(),
		token:            cfg.Token,
		privateKey:       private,
		publicKey:        public,
	}, nil
}

// Connect opens the TCP connection and completes the WKProto connect/connack handshake.
func (c *Client) Connect(ctx context.Context, uid, deviceID string) error {
	if c == nil {
		return errClientNotConnected
	}
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()

	conn, err := c.dialer.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return err
	}
	c.mu.Lock()
	oldConn := c.conn
	oldStop := c.readLoopStop
	c.conn = conn
	c.sessionCrypto = nil
	c.frameCh = nil
	c.readLoopStop = nil
	c.readErr = nil
	c.mu.Unlock()
	if oldStop != nil {
		close(oldStop)
	}
	if oldConn != nil {
		_ = oldConn.Close()
	}
	c.readMu.Lock()
	c.readBuf = c.readBuf[:0]
	c.readMu.Unlock()

	connect := &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		UID:             uid,
		DeviceID:        deviceID,
		DeviceFlag:      frame.APP,
		ClientTimestamp: time.Now().UnixMilli(),
		ClientKey:       protocolenc.EncodePublicKey(c.publicKey),
		Token:           c.token,
	}
	if err := c.writeFrame(ctx, connect); err != nil {
		_ = c.Close()
		return err
	}
	f, err := c.readFrame(ctx)
	if err != nil {
		_ = c.Close()
		return err
	}
	connack, ok := f.(*frame.ConnackPacket)
	if !ok {
		_ = c.Close()
		return fmt.Errorf("wkproto connect: expected *frame.ConnackPacket, got %T", f)
	}
	if connack.ReasonCode != frame.ReasonSuccess {
		_ = c.Close()
		return fmt.Errorf("wkproto connect: unexpected reason code %s", connack.ReasonCode)
	}
	c.startReadLoop()
	return nil
}

// Send encrypts when the negotiated session requires it, then writes one send packet.
func (c *Client) Send(ctx context.Context, pkt *frame.SendPacket) error {
	if c == nil {
		return errClientNotConnected
	}
	if pkt == nil {
		return fmt.Errorf("wkproto client: send packet is nil")
	}
	cloned := *pkt
	if c.cryptoEnabled() && !cloned.Setting.IsSet(frame.SettingNoEncrypt) {
		crypto := c.currentCrypto()
		encrypted, err := protocolenc.EncryptPayloadWithCrypto(cloned.Payload, crypto)
		if err != nil {
			return err
		}
		cloned.Payload = encrypted
		msgKey, err := protocolenc.SendMsgKeyWithCrypto(&cloned, crypto)
		if err != nil {
			return err
		}
		cloned.MsgKey = msgKey
	}
	return c.writeFrame(ctx, &cloned)
}

// ReadFrame reads and decodes one frame from the gateway connection.
func (c *Client) ReadFrame(ctx context.Context) (frame.Frame, error) {
	if c == nil {
		return nil, errClientNotConnected
	}
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()
	ch, err := c.currentFrameQueue()
	if err != nil {
		return nil, err
	}
	if ch != nil {
		select {
		case result, ok := <-ch:
			if !ok {
				if err := c.currentReadErr(); err != nil {
					return nil, err
				}
				return nil, errClientNotConnected
			}
			return result.frame, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.readFrame(ctx)
}

// RecvAck sends one receive acknowledgment for a delivered message.
func (c *Client) RecvAck(ctx context.Context, messageID int64, messageSeq uint64) error {
	if c == nil {
		return errClientNotConnected
	}
	return c.writeFrame(ctx, &frame.RecvackPacket{MessageID: messageID, MessageSeq: messageSeq})
}

// Ping sends a WKProto heartbeat ping frame on the active connection.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil {
		return errClientNotConnected
	}
	return c.writeFrame(ctx, &frame.PingPacket{})
}

// Close closes the active TCP connection, if any.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	conn := c.conn
	stop := c.readLoopStop
	c.conn = nil
	c.frameCh = nil
	c.readLoopStop = nil
	c.readErr = errClientNotConnected
	c.sessionCrypto = nil
	c.mu.Unlock()
	if stop != nil {
		close(stop)
	}
	if conn == nil {
		return nil
	}
	return conn.Close()
}

func (c *Client) startReadLoop() {
	ch := make(chan frameResult, c.frameBufferSize)
	stop := make(chan struct{})
	c.mu.Lock()
	c.frameCh = ch
	c.readLoopStop = stop
	c.readErr = nil
	c.mu.Unlock()
	go c.readLoop(ch, stop)
}

func (c *Client) readLoop(ch chan<- frameResult, stop <-chan struct{}) {
	defer close(ch)
	for {
		f, err := c.readFrame(context.Background())
		if err != nil {
			c.setReadErr(err)
			return
		}
		select {
		case ch <- frameResult{frame: f}:
		case <-stop:
			c.setReadErr(errClientNotConnected)
			return
		}
	}
}

func (c *Client) writeFrame(ctx context.Context, f frame.Frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()
	conn, err := c.currentConn()
	if err != nil {
		return err
	}
	payload, err := c.proto.EncodeFrame(f, frame.LatestVersion)
	if err != nil {
		return err
	}
	if err := withDeadline(ctx, conn.SetWriteDeadline, func() error {
		for len(payload) > 0 {
			n, err := conn.Write(payload)
			if err != nil {
				return err
			}
			payload = payload[n:]
		}
		return nil
	}); err != nil {
		return err
	}
	return ctx.Err()
}

func (c *Client) readFrame(ctx context.Context) (frame.Frame, error) {
	conn, err := c.currentConn()
	if err != nil {
		return nil, err
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	var f frame.Frame
	if err := withDeadline(ctx, conn.SetReadDeadline, func() error {
		var scratch [4096]byte
		for {
			decoded, ok, err := c.nextBufferedFrame()
			if err != nil {
				return err
			}
			if ok {
				f = decoded
				return nil
			}
			n, readErr := conn.Read(scratch[:])
			if n > 0 {
				c.readBuf = append(c.readBuf, scratch[:n]...)
				decoded, ok, err = c.nextBufferedFrame()
				if err != nil {
					return err
				}
				if ok {
					f = decoded
					return nil
				}
			}
			if readErr != nil {
				return readErr
			}
		}
	}); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	switch pkt := f.(type) {
	case *frame.ConnackPacket:
		if err := c.applyConnack(pkt); err != nil {
			return nil, err
		}
	case *frame.RecvPacket:
		if c.cryptoEnabled() && !pkt.Setting.IsSet(frame.SettingNoEncrypt) {
			plain, err := protocolenc.DecryptPayloadWithCrypto(pkt.Payload, c.currentCrypto())
			if err != nil {
				return nil, recvDecryptError(pkt, err)
			}
			pkt.Payload = plain
		}
	}
	return f, nil
}

func recvDecryptError(pkt *frame.RecvPacket, err error) error {
	if pkt == nil {
		return fmt.Errorf("wkproto client: decrypt recv payload: packet is nil: %w", err)
	}
	const maxPrefix = 32
	prefix := pkt.Payload
	if len(prefix) > maxPrefix {
		prefix = prefix[:maxPrefix]
	}
	return fmt.Errorf(
		"wkproto client: decrypt recv payload: channel_id=%q channel_type=%d from_uid=%q client_msg_no=%q message_id=%d message_seq=%d setting=%d msg_key_empty=%t payload_len=%d payload_prefix=%q payload_prefix_hex=%x: %w",
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

func (c *Client) nextBufferedFrame() (frame.Frame, bool, error) {
	if len(c.readBuf) == 0 {
		return nil, false, nil
	}
	f, consumed, err := c.proto.DecodeFrame(c.readBuf, frame.LatestVersion)
	if err != nil {
		return nil, false, err
	}
	if f == nil || consumed == 0 {
		return nil, false, nil
	}
	detachFramePayload(f)
	if consumed >= len(c.readBuf) {
		c.readBuf = c.readBuf[:0]
	} else {
		copy(c.readBuf, c.readBuf[consumed:])
		c.readBuf = c.readBuf[:len(c.readBuf)-consumed]
	}
	return f, true, nil
}

func detachFramePayload(f frame.Frame) {
	switch pkt := f.(type) {
	case *frame.SendPacket:
		pkt.Payload = append([]byte(nil), pkt.Payload...)
	case *frame.RecvPacket:
		pkt.Payload = append([]byte(nil), pkt.Payload...)
	}
}

func (c *Client) applyConnack(connack *frame.ConnackPacket) error {
	if connack == nil || connack.ServerKey == "" || connack.Salt == "" {
		return nil
	}
	keys, err := protocolenc.DeriveClientSession(c.privateKey, connack.ServerKey, connack.Salt)
	if err != nil {
		return err
	}
	sessionCrypto, err := protocolenc.NewSessionCrypto(keys)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.sessionCrypto = sessionCrypto
	c.mu.Unlock()
	return nil
}

func (c *Client) currentConn() (net.Conn, error) {
	if c == nil {
		return nil, errClientNotConnected
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil, errClientNotConnected
	}
	return c.conn, nil
}

func (c *Client) currentFrameQueue() (<-chan frameResult, error) {
	if c == nil {
		return nil, errClientNotConnected
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil, errClientNotConnected
	}
	return c.frameCh, nil
}

func (c *Client) setReadErr(err error) {
	c.mu.Lock()
	c.readErr = err
	c.mu.Unlock()
}

func (c *Client) currentReadErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readErr
}

func (c *Client) currentCrypto() *protocolenc.SessionCrypto {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionCrypto
}

func (c *Client) cryptoEnabled() bool {
	return c.currentCrypto() != nil
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

func withDeadline(ctx context.Context, setDeadline func(time.Time) error, run func() error) error {
	deadline, hasDeadline := ctx.Deadline()
	if hasDeadline {
		if err := setDeadline(deadline); err != nil {
			return err
		}
	}
	defer func() { _ = setDeadline(time.Time{}) }()
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = setDeadline(time.Now())
		case <-done:
		}
	}()
	defer close(done)
	return run()
}
