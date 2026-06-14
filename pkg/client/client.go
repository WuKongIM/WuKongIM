package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

var errStaleRecvSnapshot = errors.New("client: stale recv snapshot")

// Client is one WKProto TCP session.
type Client struct {
	cfg    Config
	proto  codec.Protocol
	crypto *cryptoState
	seq    atomic.Uint64

	// connectMu serializes CONNECT handshakes so session state is published atomically.
	connectMu  sync.Mutex
	sendMu     sync.Mutex
	closeOnce  sync.Once
	writerOnce sync.Once
	mu         sync.Mutex
	conn       net.Conn
	session    *wkprotoenc.SessionCrypto
	closed     bool
	writeCh    chan writeRequest
	closeCh    chan struct{}
	// inflight limits SENDs that have been admitted and are waiting for SENDACK.
	inflight chan struct{}
	// pending tracks SEND requests waiting for SENDACK frames from the reader loop.
	pending *pendingTracker
	// recvMu protects bounded overwrite semantics for recvCh.
	recvMu sync.Mutex
	// recvCh buffers inbound RECV frames for future public receive APIs.
	recvCh chan *frame.RecvPacket
	// recvNotify closes when the current receive session becomes unavailable.
	recvNotify chan struct{}
	// recvErr records why Recv waiters should stop waiting for the current session.
	recvErr    error
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
	c := &Client{
		cfg:      cfg,
		proto:    codec.New(),
		crypto:   crypto,
		writeCh:  make(chan writeRequest, cfg.SendQueueCapacity),
		closeCh:  make(chan struct{}),
		inflight: make(chan struct{}, cfg.MaxInflight),
		pending:  newPendingTracker(),
		recvCh:   make(chan *frame.RecvPacket, cfg.InboundFrameBufferSize),
	}
	c.recvNotify = closedNotify()
	c.recvErr = ErrNotConnected
	return c, nil
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
	oldRecvNotify := c.recvNotify
	oldRecvActive := c.recvErr == nil
	c.conn = conn
	c.session = c.crypto.currentSession()
	c.pending = newPendingTracker()
	c.recvCh = make(chan *frame.RecvPacket, c.cfg.InboundFrameBufferSize)
	c.recvNotify = make(chan struct{})
	c.recvErr = nil
	c.startLoops(conn, c.pending, c.session)
	c.mu.Unlock()

	if oldRecvActive && oldRecvNotify != nil {
		close(oldRecvNotify)
	}
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
	c.session = nil
	c.recvCh = make(chan *frame.RecvPacket, c.cfg.InboundFrameBufferSize)
	pending := c.pending
	c.signalRecvUnavailableLocked(ErrClosed)
	c.mu.Unlock()

	c.closeOnce.Do(func() {
		close(c.closeCh)
	})
	if pending != nil {
		pending.close(ErrClosed)
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
	prepared := make([]preparedSend, len(msgs))
	for i := range msgs {
		item, err := c.prepareSend(msgs[i])
		if err != nil {
			return nil, err
		}
		prepared[i] = item
	}
	var err error
	futures, err = c.admitPreparedSends(ctx, prepared)
	if err != nil {
		return nil, err
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

	item, err := c.prepareSend(msg)
	if err != nil {
		return nil, err
	}
	futures, err := c.admitPreparedSends(ctx, []preparedSend{item})
	if err != nil {
		return nil, err
	}
	return futures[0], nil
}

// ReadFrame waits for the next inbound data frame exposed by this client.
func (c *Client) ReadFrame(ctx context.Context) (frame.Frame, error) {
	if c == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil, ErrClosed
		}
		if c.conn == nil {
			err := c.recvErr
			if err == nil {
				err = ErrNotConnected
			}
			c.mu.Unlock()
			return nil, err
		}
		recvCh := c.recvCh
		recvNotify := c.recvNotify
		c.mu.Unlock()

		f, err := c.readFrameFromSnapshot(ctx, recvCh, recvNotify)
		if errors.Is(err, errStaleRecvSnapshot) {
			continue
		}
		return f, err
	}
}

func (c *Client) readFrameFromSnapshot(ctx context.Context, recvCh <-chan *frame.RecvPacket, recvNotify <-chan struct{}) (frame.Frame, error) {
	for {
		select {
		case pkt := <-recvCh:
			if pkt == nil {
				return nil, ErrClosed
			}
			if !c.isCurrentRecvSnapshot(recvCh) {
				return nil, c.staleRecvSnapshotError()
			}
			return pkt, nil
		default:
		}

		select {
		case pkt := <-recvCh:
			if pkt == nil {
				return nil, ErrClosed
			}
			if !c.isCurrentRecvSnapshot(recvCh) {
				return nil, c.staleRecvSnapshotError()
			}
			return pkt, nil
		case <-recvNotify:
			if !c.isCurrentRecvSnapshot(recvCh) {
				return nil, c.staleRecvSnapshotError()
			}
			return nil, c.recvUnavailableError()
		case <-c.closeCh:
			return nil, ErrClosed
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (c *Client) isCurrentRecvSnapshot(recvCh <-chan *frame.RecvPacket) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && sameRecvChannel(c.recvCh, recvCh)
}

func (c *Client) recvUnavailableError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	err := c.recvErr
	if err == nil {
		err = ErrNotConnected
	}
	return err
}

func (c *Client) staleRecvSnapshotError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	return errStaleRecvSnapshot
}

func sameRecvChannel(a chan *frame.RecvPacket, b <-chan *frame.RecvPacket) bool {
	return (<-chan *frame.RecvPacket)(a) == b
}

// Recv waits for the next inbound RECV packet.
func (c *Client) Recv(ctx context.Context) (*frame.RecvPacket, error) {
	f, err := c.ReadFrame(ctx)
	if err != nil {
		return nil, err
	}
	recv, ok := f.(*frame.RecvPacket)
	if !ok {
		return nil, fmt.Errorf("client: expected *frame.RecvPacket, got %T", f)
	}
	return recv, nil
}

// RecvAck sends a RECVACK control frame for one received message.
func (c *Client) RecvAck(ctx context.Context, messageID int64, messageSeq uint64) error {
	return c.writeControl(ctx, &frame.RecvackPacket{MessageID: messageID, MessageSeq: messageSeq})
}

// Ping sends a PING control frame.
func (c *Client) Ping(ctx context.Context) error {
	return c.writeControl(ctx, &frame.PingPacket{})
}

func (c *Client) writeControl(ctx context.Context, f frame.Frame) error {
	if c == nil {
		return ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	conn := c.conn
	if conn == nil {
		c.mu.Unlock()
		return ErrNotConnected
	}
	c.mu.Unlock()

	return c.writeControlToConn(ctx, f, conn, true)
}

func (c *Client) writeControlToConn(ctx context.Context, f frame.Frame, conn net.Conn, wait bool) error {
	if conn == nil {
		return ErrNotConnected
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	var result chan error
	if wait {
		result = make(chan error, 1)
	}
	req := writeRequest{
		kind:   writeKindFrame,
		frame:  f,
		result: result,
		ctx:    ctx,
		conn:   conn,
	}
	if !wait {
		select {
		case c.writeCh <- req:
			return nil
		case <-c.closeCh:
			return ErrClosed
		case <-ctx.Done():
			return ctx.Err()
		default:
			return ErrSendQueueFull
		}
	}

	select {
	case c.writeCh <- req:
	case <-c.closeCh:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-result:
		return err
	case <-c.closeCh:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func closedNotify() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (c *Client) signalRecvUnavailableLocked(err error) {
	if err == nil {
		err = net.ErrClosed
	}
	if c.recvErr != nil {
		return
	}
	c.recvErr = err
	close(c.recvNotify)
}

type preparedSend struct {
	msg Message
	pkt *frame.SendPacket
	key pendingKey
}

func (c *Client) prepareSend(msg Message) (preparedSend, error) {
	assignedSeq, err := c.nextClientSeq(msg.ClientSeq)
	if err != nil {
		return preparedSend{}, err
	}
	msg.ClientSeq = assignedSeq
	if c.cfg.GenerateClientMsgNo && msg.ClientMsgNo == "" {
		msg.ClientMsgNo = generatedClientMsgNo(assignedSeq)
	}
	pkt, err := buildSendPacket(msg, assignedSeq)
	if err != nil {
		return preparedSend{}, err
	}
	return preparedSend{
		msg: msg,
		pkt: pkt,
		key: pendingKey{ClientSeq: pkt.ClientSeq, ClientMsgNo: pkt.ClientMsgNo},
	}, nil
}

func (c *Client) admitPreparedSends(ctx context.Context, prepared []preparedSend) ([]*SendFuture, error) {
	if len(prepared) == 0 {
		return nil, nil
	}

	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	conn := c.conn
	pending := c.pending
	session := c.session
	if conn == nil || pending == nil {
		c.mu.Unlock()
		return nil, ErrNotConnected
	}
	c.mu.Unlock()

	if len(c.writeCh)+len(prepared) > cap(c.writeCh) {
		c.observeSendQueue("full")
		return nil, ErrSendQueueFull
	}
	reserved, err := c.reserveInflight(ctx, len(prepared))
	if err != nil {
		c.releaseInflightN(reserved)
		if err == ErrSendQueueFull {
			c.observeSendQueue("full")
		}
		return nil, err
	}

	futures := make([]*SendFuture, len(prepared))
	reqs := make([]writeRequest, len(prepared))
	for i, item := range prepared {
		entry, err := pending.addWithFinish(item.key, c.cfg.AckTimeout, c.releaseInflight)
		if err != nil {
			for _, req := range reqs[:i] {
				pending.fail(req.entry, err)
			}
			c.releaseInflightN(len(prepared) - i)
			return nil, err
		}
		reqs[i] = writeRequest{
			kind:    writeKindSend,
			msg:     item.msg,
			pkt:     item.pkt,
			entry:   entry,
			ctx:     ctx,
			conn:    conn,
			pending: pending,
			session: session,
		}
		futures[i] = &SendFuture{done: entry.done}
	}

	if err := ctx.Err(); err != nil {
		for _, req := range reqs {
			pending.fail(req.entry, err)
		}
		return nil, err
	}

	for _, req := range reqs {
		select {
		case c.writeCh <- req:
		case <-c.closeCh:
			for _, failed := range reqs {
				pending.fail(failed.entry, ErrClosed)
			}
			return nil, ErrClosed
		case <-ctx.Done():
			for _, failed := range reqs {
				pending.fail(failed.entry, ctx.Err())
			}
			return nil, ctx.Err()
		}
	}
	for range reqs {
		c.observeSendQueue("accepted")
	}
	return futures, nil
}

func (c *Client) reserveInflight(ctx context.Context, n int) (int, error) {
	if n == 0 {
		return 0, nil
	}
	if n > cap(c.inflight) {
		return 0, ErrSendQueueFull
	}

	acquired := 0
	for acquired < n {
		select {
		case c.inflight <- struct{}{}:
			acquired++
		case <-c.closeCh:
			return acquired, ErrClosed
		case <-ctx.Done():
			return acquired, ctx.Err()
		}
	}
	return acquired, nil
}

func (c *Client) releaseInflight() {
	select {
	case <-c.inflight:
	default:
	}
}

func (c *Client) releaseInflightN(n int) {
	for i := 0; i < n; i++ {
		c.releaseInflight()
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

func generatedClientMsgNo(seq uint64) string {
	return "wkclient-" + strconv.FormatUint(seq, 10)
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
	var deadlineWG sync.WaitGroup
	deadlineWG.Add(1)
	go func() {
		defer deadlineWG.Done()
		select {
		case <-ctx.Done():
			_ = setDeadline(time.Now())
		case <-done:
		}
	}()

	err := op()
	close(done)
	deadlineWG.Wait()
	_ = setDeadline(time.Time{})
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
