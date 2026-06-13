package client

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// Client is one WKProto TCP session.
type Client struct {
	cfg    Config
	proto  codec.Protocol
	crypto *cryptoState

	mu         sync.Mutex
	conn       net.Conn
	closed     bool
	writeCh    chan writeRequest
	readerDone chan struct{}
	writerDone chan struct{}
}

// New creates a WKProto client with normalized configuration defaults.
func New(cfg Config) (*Client, error) {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	crypto, err := newCryptoState()
	if err != nil {
		return nil, err
	}
	return &Client{
		cfg:     cfg,
		proto:   codec.New(),
		crypto:  crypto,
		writeCh: make(chan writeRequest, cfg.SendQueueCapacity),
	}, nil
}

// Connect opens a TCP session and completes the WKProto CONNECT handshake.
func (c *Client) Connect(ctx context.Context, opts ConnectOptions) (*frame.ConnackPacket, error) {
	started := time.Now()
	if opts.Token == "" {
		opts.Token = c.cfg.Token
	}

	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		err := ErrClosed
		c.observeConnect(opts, time.Since(started), err)
		return nil, err
	}
	c.mu.Unlock()

	conn, err := c.cfg.Dialer.DialContext(ctx, "tcp", c.cfg.Addr)
	if err != nil {
		c.observeConnect(opts, time.Since(started), err)
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		_ = conn.Close()
		c.observeConnect(opts, time.Since(started), err)
		return nil, err
	}

	packet := c.crypto.connectPacket(opts)
	if err = c.writeFrameSync(ctx, conn, packet); err != nil {
		_ = conn.Close()
		c.observeConnect(opts, time.Since(started), err)
		return nil, err
	}

	f, err := c.readFrameSync(ctx, conn)
	if err != nil {
		_ = conn.Close()
		c.observeConnect(opts, time.Since(started), err)
		return nil, err
	}
	ack, ok := f.(*frame.ConnackPacket)
	if !ok {
		_ = conn.Close()
		err = fmt.Errorf("client: expected CONNACK, got %s", f.GetFrameType())
		c.observeConnect(opts, time.Since(started), err)
		return nil, err
	}
	if ack.ReasonCode != frame.ReasonSuccess {
		_ = conn.Close()
		err = fmt.Errorf("client: connack reason=%s", ack.ReasonCode)
		c.observeConnect(opts, time.Since(started), err)
		return nil, err
	}
	if err = c.crypto.applyConnack(ack); err != nil {
		_ = conn.Close()
		c.observeConnect(opts, time.Since(started), err)
		return nil, err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		err = ErrClosed
		c.observeConnect(opts, time.Since(started), err)
		return nil, err
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.conn = conn
	c.startLoops()
	c.mu.Unlock()

	c.observeConnect(opts, time.Since(started), nil)
	return ack, nil
}

// Close closes the client connection.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.closed = true
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()

	if conn != nil {
		return conn.Close()
	}
	return nil
}

func (c *Client) startLoops() {
	readerDone := make(chan struct{})
	writerDone := make(chan struct{})
	c.readerDone = readerDone
	c.writerDone = writerDone
	go func() {
		defer close(writerDone)
		c.writerLoop()
	}()
	go func() {
		defer close(readerDone)
		c.readerLoop()
	}()
}

func (c *Client) writerLoop() {
}

func (c *Client) readerLoop() {
}

func (c *Client) withDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, c.cfg.OperationTimeout)
}

func (c *Client) currentConn() (net.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if c.conn == nil {
		return nil, ErrNotConnected
	}
	return c.conn, nil
}

func (c *Client) writeFrameSync(ctx context.Context, conn net.Conn, f frame.Frame) error {
	data, err := c.proto.EncodeFrame(f, frame.LatestVersion)
	if err != nil {
		return err
	}
	return c.withDeadline(ctx, conn.SetWriteDeadline, func() error {
		for len(data) > 0 {
			n, err := conn.Write(data)
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			data = data[n:]
		}
		return nil
	})
}

func (c *Client) readFrameSync(ctx context.Context, conn net.Conn) (frame.Frame, error) {
	var f frame.Frame
	err := c.withDeadline(ctx, conn.SetReadDeadline, func() error {
		var err error
		f, err = codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
		return err
	})
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (c *Client) operationDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(c.cfg.OperationTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		return ctxDeadline
	}
	return deadline
}

func (c *Client) withDeadline(ctx context.Context, setDeadline func(time.Time) error, op func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := setDeadline(c.operationDeadline(ctx)); err != nil {
		return err
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = setDeadline(time.Now())
		case <-done:
		}
	}()

	err := op()
	close(done)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
	}
	return err
}

func (c *Client) observeConnect(opts ConnectOptions, elapsed time.Duration, err error) {
	if c.cfg.Observer == nil {
		return
	}
	c.cfg.Observer.OnConnect(ConnectEvent{
		Addr:    c.cfg.Addr,
		UID:     opts.UID,
		Elapsed: elapsed,
		Err:     err,
	})
}
