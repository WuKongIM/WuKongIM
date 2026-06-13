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
		cfg:    cfg,
		proto:  codec.New(),
		crypto: crypto,
	}, nil
}

// Connect opens a TCP session and completes the WKProto CONNECT handshake.
func (c *Client) Connect(opts ConnectOptions) error {
	started := time.Now()
	if opts.Token == "" {
		opts.Token = c.cfg.Token
	}

	ctx, cancel := c.withDefaultTimeout(context.Background())
	defer cancel()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		err := ErrClosed
		c.observeConnect(opts, time.Since(started), err)
		return err
	}
	c.mu.Unlock()

	conn, err := c.cfg.Dialer.DialContext(ctx, "tcp", c.cfg.Addr)
	if err != nil {
		c.observeConnect(opts, time.Since(started), err)
		return err
	}

	packet := c.crypto.connectPacket(opts)
	if err = c.writeFrameSync(conn, packet); err != nil {
		_ = conn.Close()
		c.observeConnect(opts, time.Since(started), err)
		return err
	}

	f, err := c.readFrameSync(conn)
	if err != nil {
		_ = conn.Close()
		c.observeConnect(opts, time.Since(started), err)
		return err
	}
	ack, ok := f.(*frame.ConnackPacket)
	if !ok {
		_ = conn.Close()
		err = fmt.Errorf("client: expected CONNACK, got %s", f.GetFrameType())
		c.observeConnect(opts, time.Since(started), err)
		return err
	}
	if ack.ReasonCode != frame.ReasonSuccess {
		_ = conn.Close()
		err = fmt.Errorf("client: connack reason=%s", ack.ReasonCode)
		c.observeConnect(opts, time.Since(started), err)
		return err
	}
	if err = c.crypto.applyConnack(ack); err != nil {
		_ = conn.Close()
		c.observeConnect(opts, time.Since(started), err)
		return err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		err = ErrClosed
		c.observeConnect(opts, time.Since(started), err)
		return err
	}
	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.conn = conn
	c.startLoops()
	c.mu.Unlock()

	c.observeConnect(opts, time.Since(started), nil)
	return nil
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
	c.readerDone = make(chan struct{})
	c.writerDone = make(chan struct{})
	close(c.readerDone)
	close(c.writerDone)
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

func (c *Client) writeFrameSync(conn net.Conn, f frame.Frame) error {
	if err := conn.SetWriteDeadline(time.Now().Add(c.cfg.OperationTimeout)); err != nil {
		return err
	}
	data, err := c.proto.EncodeFrame(f, frame.LatestVersion)
	if err != nil {
		return err
	}
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
}

func (c *Client) readFrameSync(conn net.Conn) (frame.Frame, error) {
	if err := conn.SetReadDeadline(time.Now().Add(c.cfg.OperationTimeout)); err != nil {
		return nil, err
	}
	return codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
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
