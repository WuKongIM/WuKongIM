package client

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

// Client is one WKProto TCP session.
type Client struct {
	cfg    Config
	proto  codec.Protocol
	crypto *cryptoState
	seq    atomic.Uint64

	// connectMu serializes CONNECT handshakes so session state is published atomically.
	connectMu  sync.Mutex
	writerOnce sync.Once
	mu         sync.Mutex
	conn       net.Conn
	closed     bool
	writeCh    chan writeRequest
	// pending tracks SEND requests waiting for SENDACK frames from the reader loop.
	pending *pendingTracker
	// recvMu protects bounded overwrite semantics for recvCh.
	recvMu sync.Mutex
	// recvCh buffers inbound RECV frames for future public receive APIs.
	recvCh     chan *frame.RecvPacket
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
		pending: newPendingTracker(),
		recvCh:  make(chan *frame.RecvPacket, cfg.InboundFrameBufferSize),
	}, nil
}

// Connect opens a TCP session and completes the WKProto CONNECT handshake.
func (c *Client) Connect(ctx context.Context, opts ConnectOptions) (*frame.ConnackPacket, error) {
	started := time.Now()
	if opts.Token == "" {
		opts.Token = c.cfg.Token
	}

	c.connectMu.Lock()
	defer c.connectMu.Unlock()

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

	var oldConn net.Conn
	var oldPending *pendingTracker
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		err = ErrClosed
		c.observeConnect(opts, time.Since(started), err)
		return nil, err
	}
	oldConn = c.conn
	oldPending = c.pending
	c.conn = conn
	c.pending = newPendingTracker()
	c.startLoops(conn, c.pending, c.crypto.currentSession())
	c.mu.Unlock()

	if oldPending != nil {
		oldPending.close(ErrClosed)
	}
	if oldConn != nil {
		_ = oldConn.Close()
	}

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
	pending := c.pending
	c.mu.Unlock()

	if pending != nil {
		pending.close(ErrClosed)
	}
	select {
	case c.writeCh <- writeRequest{kind: writeKindClose}:
	default:
	}
	if conn != nil {
		return conn.Close()
	}
	return nil
}

func (c *Client) startLoops(conn net.Conn, pending *pendingTracker, session *wkprotoenc.SessionCrypto) {
	readerDone := make(chan struct{})
	writerDone := make(chan struct{})
	c.readerDone = readerDone
	c.writerOnce.Do(func() {
		c.writerDone = writerDone
		go func() {
			defer close(writerDone)
			c.writerLoop()
		}()
	})
	go func() {
		defer close(readerDone)
		c.readerLoop(conn, pending, session)
	}()
}

func (c *Client) writerLoop() {
	c.runWriterLoop()
}

// Send sends one message and waits for its SENDACK.
func (c *Client) Send(ctx context.Context, msg Message) (SendResult, error) {
	results, err := c.SendBatch(ctx, []Message{msg})
	if err != nil {
		if len(results) > 0 {
			return results[0], err
		}
		return SendResult{}, err
	}
	if len(results) == 0 {
		return SendResult{}, nil
	}
	return results[0], nil
}

// SendBatch sends messages and returns SENDACK results in input order.
func (c *Client) SendBatch(ctx context.Context, msgs []Message) ([]SendResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(msgs) == 0 {
		return nil, nil
	}

	futures := make([]*SendFuture, len(msgs))
	for i := range msgs {
		future, err := c.SendAsync(ctx, msgs[i])
		if err != nil {
			return nil, err
		}
		futures[i] = future
	}

	results := make([]SendResult, len(futures))
	for i, future := range futures {
		result, err := future.Wait(ctx)
		results[i] = result
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

// SendAsync queues one message and returns a future resolved by the matching SENDACK.
func (c *Client) SendAsync(ctx context.Context, msg Message) (*SendFuture, error) {
	if c == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	assignedSeq, err := c.nextClientSeq(msg.ClientSeq)
	if err != nil {
		return nil, err
	}
	msg.ClientSeq = assignedSeq
	if _, err = buildSendPacket(msg, assignedSeq); err != nil {
		return nil, err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	conn := c.conn
	pending := c.pending
	if conn == nil || pending == nil {
		c.mu.Unlock()
		return nil, ErrNotConnected
	}
	c.mu.Unlock()

	entry, err := pending.add(pendingKey{ClientSeq: assignedSeq, ClientMsgNo: msg.ClientMsgNo}, c.cfg.AckTimeout)
	if err != nil {
		return nil, err
	}

	req := writeRequest{
		kind:    writeKindSend,
		msg:     msg,
		entry:   entry,
		ctx:     ctx,
		conn:    conn,
		pending: pending,
	}
	select {
	case c.writeCh <- req:
		c.observeSendQueue("accepted")
		return &SendFuture{done: entry.done}, nil
	case <-ctx.Done():
		pending.fail(entry, ctx.Err())
		c.observeSendQueue("canceled")
		return nil, ctx.Err()
	default:
		pending.fail(entry, ErrSendQueueFull)
		c.observeSendQueue("full")
		return nil, ErrSendQueueFull
	}
}

func (c *Client) nextClientSeq(explicit uint64) (uint64, error) {
	if explicit != 0 {
		if explicit > math.MaxUint32 {
			return 0, ErrClientSeqExhausted
		}
		return explicit, nil
	}
	next := c.seq.Add(1)
	if next > math.MaxUint32 {
		return 0, ErrClientSeqExhausted
	}
	return next, nil
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

func (c *Client) observeSendQueue(result string) {
	if c.cfg.Observer == nil {
		return
	}
	c.cfg.Observer.OnSendQueue(SendQueueEvent{
		Depth:    len(c.writeCh),
		Capacity: cap(c.writeCh),
		Result:   result,
	})
}

func (c *Client) observeSendBatch(records int, bytes int, elapsed time.Duration, err error) {
	if c.cfg.Observer == nil {
		return
	}
	c.cfg.Observer.OnSendBatch(SendBatchEvent{
		Records: records,
		Bytes:   bytes,
		Elapsed: elapsed,
		Err:     err,
	})
}
