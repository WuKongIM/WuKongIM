package wkproto

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	defaultOperationTimeout = 5 * time.Second
	defaultFrameBufferSize  = 1024
	defaultAckTimeoutSlack  = time.Second
	priorityResultQuota     = 4
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
	// OperationTimeout bounds handshake and short write operations when ctx has no deadline.
	// Streaming ReadFrame calls remain owned exclusively by their caller context.
	OperationTimeout time.Duration
	// AckTimeout bounds the internal pending SENDACK wait; use a value above workload waits.
	AckTimeout time.Duration
	// SendQueueCapacity bounds SEND requests waiting for the shared writer pump.
	SendQueueCapacity int
	// MaxInflight bounds SEND requests waiting for SENDACK.
	MaxInflight int
	// ReadBufferSize is the socket reader scratch-buffer size in bytes.
	ReadBufferSize int
	// FrameBufferSize independently bounds the inner RECV queue and each adapter queue.
	FrameBufferSize int
}

type tcpDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// Client is a black-box WKProto client used by wkbench workers.
type Client struct {
	addr              string
	token             string
	dialer            tcpDialer
	operationTimeout  time.Duration
	ackTimeout        time.Duration
	sendQueueCapacity int
	maxInflight       int
	readBufferSize    int
	frameBufferSize   int

	mu      sync.Mutex
	session *clientSession
}

// QueueSnapshot is a bounded numeric view of one client's receive queues.
// It never exposes queued frames or mutable queue storage.
type QueueSnapshot struct {
	// InnerRecvDepth is the number of RECV packets queued by pkg/client.
	InnerRecvDepth int
	// InnerRecvCapacity is the configured pkg/client inbound RECV bound.
	InnerRecvCapacity int
	// AdapterDepth is the total number of results queued by the bench adapter.
	AdapterDepth int
	// AdapterCapacity is the total fixed capacity of the bench adapter queues.
	AdapterCapacity int
	// RecvDepth is the number of lossless RECV frames waiting for consumers.
	RecvDepth int
	// RecvCapacity is the fixed adapter RECV capacity.
	RecvCapacity int
	// SendackDepth is the number of SENDACK frames waiting for consumers.
	SendackDepth int
	// SendackCapacity is the fixed adapter SENDACK capacity.
	SendackCapacity int
	// ErrorDepth is the number of asynchronous errors waiting for consumers.
	ErrorDepth int
	// ErrorCapacity is the fixed adapter error capacity.
	ErrorCapacity int
	// PublicationCurrent is the number of SEND futures admitted for publication.
	PublicationCurrent int
	// PublicationCapacity is the fixed admission bound for SEND future publishers.
	PublicationCapacity int
	// PublicationPeak is the session high-water mark of admitted publishers.
	PublicationPeak int
	// PublicationBlocked is the number of Send callers waiting for publication admission.
	PublicationBlocked int
	// AdmissionRejected is the cumulative number of non-waiting SEND attempts
	// rejected by adapter or inner-client local capacity.
	AdmissionRejected uint64
}

type clientSession struct {
	// inner owns the TCP session, WKProto crypto, reader, writer, and SENDACK matching.
	inner *wkclient.Client
	// recvCh backpressures lossless RECV delivery in wire order.
	recvCh chan frame.Frame
	// sendackCh isolates SEND futures from RECV pressure.
	sendackCh chan *frame.SendackPacket
	// errCh isolates asynchronous send errors and the remote terminal error.
	errCh chan errorResult
	// stopCh closes when this session is no longer active.
	stopCh chan struct{}
	// closeOnce makes session shutdown idempotent.
	closeOnce sync.Once
	// readPermit makes bounded-priority arbitration serial and cancelable.
	readPermit chan struct{}
	// priorityBurst counts consecutive errors or SENDACKs while RECV is queued.
	priorityBurst int
	// terminalDelivered prevents the original remote terminal error from repeating.
	terminalDelivered bool
	// drainCh wakes a terminal publisher when a consumer frees queue capacity.
	drainCh chan struct{}
	// publicationPermit bounds SEND future publisher goroutines before admission.
	publicationPermit chan struct{}
	// publicationCurrent counts admitted SEND future publishers.
	publicationCurrent atomic.Int64
	// publicationPeak is the monotonic admitted-publisher high-water mark.
	publicationPeak atomic.Int64
	// publicationBlocked counts Send callers currently waiting for a permit.
	publicationBlocked atomic.Int64
	// admissionRejected counts non-waiting SEND admission pressure.
	admissionRejected atomic.Uint64
	// pendingMu protects pendingSendacks and pendingDone.
	pendingMu sync.Mutex
	// pendingSendacks counts SEND futures that still need to publish a SENDACK frame.
	pendingSendacks int
	// pendingDone closes whenever pendingSendacks reaches zero.
	pendingDone chan struct{}
}

type errorResult struct {
	err         error
	terminal    bool
	clientSeq   uint64
	clientMsgNo string
}

// ReadErrorKind is the closed disposition of an asynchronous error delivered
// by ReadFrame. Callers may continue after a non-terminal SEND publication
// error, while a terminal error means the remote session reader has exited.
type ReadErrorKind uint8

const (
	// ReadErrorUnknown is not an error emitted from the client's result queue.
	ReadErrorUnknown ReadErrorKind = iota
	// ReadErrorNonTerminal belongs to one asynchronous SEND publication.
	ReadErrorNonTerminal
	// ReadErrorTerminal means the shared remote session reader has exited.
	ReadErrorTerminal
)

type readFrameError struct {
	kind        ReadErrorKind
	clientSeq   uint64
	clientMsgNo string
	err         error
}

func (e *readFrameError) Error() string { return e.err.Error() }
func (e *readFrameError) Unwrap() error { return e.err }

// ReadErrorInfo is the closed redacted ownership carried by one result-queue
// error. ClientMsgNo is populated only for a non-terminal asynchronous SEND.
type ReadErrorInfo struct {
	Kind        ReadErrorKind
	ClientSeq   uint64
	ClientMsgNo string
}

// ReadErrorInfoOf classifies only errors emitted from the bounded result
// queue. Context cancellation and local-close errors remain unclassified so
// owners can use their own generation state.
func ReadErrorInfoOf(err error) (ReadErrorInfo, bool) {
	var readErr *readFrameError
	if !errors.As(err, &readErr) {
		return ReadErrorInfo{}, false
	}
	return ReadErrorInfo{Kind: readErr.kind, ClientSeq: readErr.clientSeq, ClientMsgNo: readErr.clientMsgNo}, true
}

// ReadErrorKindOf is the kind-only projection for callers without SEND retry ownership.
func ReadErrorKindOf(err error) ReadErrorKind {
	info, ok := ReadErrorInfoOf(err)
	if !ok {
		return ReadErrorUnknown
	}
	return info.Kind
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
		addr:              cfg.Addr,
		token:             cfg.Token,
		dialer:            cfg.Dialer,
		operationTimeout:  cfg.OperationTimeout,
		ackTimeout:        cfg.AckTimeout,
		sendQueueCapacity: cfg.SendQueueCapacity,
		maxInflight:       cfg.MaxInflight,
		readBufferSize:    cfg.ReadBufferSize,
		frameBufferSize:   cfg.FrameBufferSize,
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
	if err := session.acquirePublication(ctx); err != nil {
		return err
	}

	session.beginPendingSendack()
	future, err := session.inner.SendAsync(ctx, clientMessage(pkt))
	if err != nil {
		session.completePendingPublication()
		return err
	}
	go c.forwardSendack(session, future, pkt.ClientSeq, pkt.ClientMsgNo)
	return nil
}

// TrySend admits one SEND only when both adapter publication capacity and the
// inner client's local admission bounds are immediately available.
func (c *Client) TrySend(pkt *frame.SendPacket) error {
	if pkt == nil {
		return fmt.Errorf("wkproto client: send packet is nil")
	}
	session, err := c.currentSession()
	if err != nil {
		return err
	}
	if err := session.tryAcquirePublication(); err != nil {
		if errors.Is(err, wkclient.ErrSendQueueFull) {
			session.recordAdmissionRejection()
		}
		return err
	}

	session.beginPendingSendack()
	future, err := session.inner.TrySendAsync(clientMessage(pkt))
	if err != nil {
		if errors.Is(err, wkclient.ErrSendQueueFull) {
			session.recordAdmissionRejection()
		}
		session.completePendingPublication()
		return err
	}
	go c.forwardSendack(session, future, pkt.ClientSeq, pkt.ClientMsgNo)
	return nil
}

func clientMessage(pkt *frame.SendPacket) wkclient.Message {
	return wkclient.Message{
		Setting:     pkt.Setting,
		Expire:      pkt.Expire,
		ClientSeq:   pkt.ClientSeq,
		ClientMsgNo: pkt.ClientMsgNo,
		ChannelID:   pkt.ChannelID,
		ChannelType: pkt.ChannelType,
		Topic:       pkt.Topic,
		Payload:     pkt.Payload,
	}
}

// ReadFrame reads one SENDACK or RECV frame from the connected client stream.
func (c *Client) ReadFrame(ctx context.Context) (frame.Frame, error) {
	if c == nil {
		return nil, errClientNotConnected
	}
	if ctx == nil {
		ctx = context.Background()
	}
	session, err := c.currentSession()
	if err != nil {
		return nil, err
	}
	return session.readFrame(ctx)
}

// QueueSnapshot reports fixed-capacity receive queue occupancy for observability.
func (c *Client) QueueSnapshot() QueueSnapshot {
	if c == nil {
		return QueueSnapshot{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot := QueueSnapshot{InnerRecvCapacity: c.frameBufferSize}
	if c.session == nil {
		return snapshot
	}
	innerSnapshot := c.session.inner.InboundQueueSnapshot()
	snapshot.InnerRecvDepth = innerSnapshot.Depth
	snapshot.InnerRecvCapacity = innerSnapshot.Capacity
	snapshot.RecvDepth = len(c.session.recvCh)
	snapshot.RecvCapacity = cap(c.session.recvCh)
	snapshot.SendackDepth = len(c.session.sendackCh)
	snapshot.SendackCapacity = cap(c.session.sendackCh)
	snapshot.ErrorDepth = len(c.session.errCh)
	snapshot.ErrorCapacity = cap(c.session.errCh)
	snapshot.PublicationCurrent = int(c.session.publicationCurrent.Load())
	snapshot.PublicationCapacity = cap(c.session.publicationPermit)
	snapshot.PublicationPeak = int(c.session.publicationPeak.Load())
	snapshot.PublicationBlocked = int(c.session.publicationBlocked.Load())
	snapshot.AdmissionRejected = c.session.admissionRejected.Load()
	snapshot.AdapterDepth = snapshot.RecvDepth + snapshot.SendackDepth + snapshot.ErrorDepth
	snapshot.AdapterCapacity = snapshot.RecvCapacity + snapshot.SendackCapacity + snapshot.ErrorCapacity
	return snapshot
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

// Close signals adapter waiters and closes the active shared TCP client, if any.
// Shared-client loop joining belongs to the worker lifecycle teardown.
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
			if !session.waitPublishedResultsDrained() {
				return
			}
			session.publishError(errorResult{err: err, terminal: true})
			return
		}
		if !session.publishRecv(f) {
			return
		}
	}
}

func (c *Client) forwardSendack(session *clientSession, future *wkclient.SendFuture, clientSeq uint64, clientMsgNo string) {
	result, err := future.Wait(context.Background())
	defer session.completePendingPublication()
	if err != nil && result.ClientSeq == 0 && result.ClientMsgNo == "" {
		if wkclient.IsSessionReadError(err) {
			return
		}
		session.publishError(errorResult{err: err, clientSeq: clientSeq, clientMsgNo: clientMsgNo})
		return
	}
	ack := &frame.SendackPacket{
		ClientSeq:   result.ClientSeq,
		ClientMsgNo: result.ClientMsgNo,
		MessageID:   result.MessageID,
		MessageSeq:  result.MessageSeq,
		ReasonCode:  result.ReasonCode,
	}
	session.publishSendack(ack)
}

func (s *clientSession) publishRecv(f frame.Frame) bool {
	select {
	case s.recvCh <- f:
		return true
	case <-s.stopCh:
		return false
	}
}

func (s *clientSession) publishSendack(ack *frame.SendackPacket) bool {
	select {
	case s.sendackCh <- ack:
		return true
	case <-s.stopCh:
		return false
	}
}

func (s *clientSession) publishError(result errorResult) bool {
	select {
	case s.errCh <- result:
		return true
	case <-s.stopCh:
		return false
	}
}

func (s *clientSession) readFrame(ctx context.Context) (frame.Frame, error) {
	if err := s.acquireReadPermit(ctx); err != nil {
		return nil, err
	}
	defer s.releaseReadPermit()
	for {
		if s.terminalDelivered {
			return nil, errClientNotConnected
		}
		if s.isStopped() {
			return nil, errClientNotConnected
		}

		if s.priorityBurst < priorityResultQuota {
			select {
			case result := <-s.errCh:
				return s.consumeError(result)
			default:
			}
			select {
			case ack := <-s.sendackCh:
				return s.consumeSendack(ack)
			default:
			}
		}
		select {
		case recv := <-s.recvCh:
			return s.consumeRecv(recv)
		default:
		}
		select {
		case result := <-s.errCh:
			return s.consumeError(result)
		default:
		}
		select {
		case ack := <-s.sendackCh:
			return s.consumeSendack(ack)
		default:
		}

		if s.priorityBurst >= priorityResultQuota {
			select {
			case recv := <-s.recvCh:
				return s.consumeRecv(recv)
			case ack := <-s.sendackCh:
				return s.consumeSendack(ack)
			case result := <-s.errCh:
				return s.consumeError(result)
			case <-s.stopCh:
				return nil, errClientNotConnected
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		select {
		case ack := <-s.sendackCh:
			return s.consumeSendack(ack)
		case recv := <-s.recvCh:
			return s.consumeRecv(recv)
		case result := <-s.errCh:
			return s.consumeError(result)
		case <-s.stopCh:
			return nil, errClientNotConnected
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (s *clientSession) acquireReadPermit(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stopCh:
		return errClientNotConnected
	case <-s.readPermit:
		return nil
	}
}

func (s *clientSession) acquirePublication(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.isStopped() {
		return errClientNotConnected
	}
	select {
	case <-s.publicationPermit:
		if s.isStopped() {
			s.publicationPermit <- struct{}{}
			return errClientNotConnected
		}
		s.recordPublicationAdmission()
		return nil
	default:
	}

	s.publicationBlocked.Add(1)
	defer s.publicationBlocked.Add(-1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stopCh:
		return errClientNotConnected
	case <-s.publicationPermit:
		if s.isStopped() {
			s.publicationPermit <- struct{}{}
			return errClientNotConnected
		}
		s.recordPublicationAdmission()
		return nil
	}
}

func (s *clientSession) tryAcquirePublication() error {
	if s.isStopped() {
		return errClientNotConnected
	}
	select {
	case <-s.publicationPermit:
		if s.isStopped() {
			s.publicationPermit <- struct{}{}
			return errClientNotConnected
		}
		s.recordPublicationAdmission()
		return nil
	default:
		return wkclient.ErrSendQueueFull
	}
}

func (s *clientSession) recordPublicationAdmission() {
	current := s.publicationCurrent.Add(1)
	for {
		peak := s.publicationPeak.Load()
		if current <= peak || s.publicationPeak.CompareAndSwap(peak, current) {
			return
		}
	}
}

func (s *clientSession) recordAdmissionRejection() {
	for {
		current := s.admissionRejected.Load()
		if current == ^uint64(0) || s.admissionRejected.CompareAndSwap(current, current+1) {
			return
		}
	}
}

func (s *clientSession) releasePublication() {
	s.publicationCurrent.Add(-1)
	s.publicationPermit <- struct{}{}
}

func (s *clientSession) releaseReadPermit() {
	s.readPermit <- struct{}{}
}

func (s *clientSession) consumeSendack(ack *frame.SendackPacket) (frame.Frame, error) {
	s.notifyQueueDrain()
	if s.isStopped() {
		return nil, errClientNotConnected
	}
	if s.priorityBurst < priorityResultQuota {
		s.priorityBurst++
	}
	return ack, nil
}

func (s *clientSession) consumeRecv(recv frame.Frame) (frame.Frame, error) {
	s.notifyQueueDrain()
	if s.isStopped() {
		return nil, errClientNotConnected
	}
	s.priorityBurst = 0
	return recv, nil
}

func (s *clientSession) consumeError(result errorResult) (frame.Frame, error) {
	s.notifyQueueDrain()
	if s.isStopped() {
		return nil, errClientNotConnected
	}
	if result.terminal {
		s.terminalDelivered = true
	}
	if s.priorityBurst < priorityResultQuota {
		s.priorityBurst++
	}
	kind := ReadErrorNonTerminal
	if result.terminal {
		kind = ReadErrorTerminal
	}
	return nil, &readFrameError{kind: kind, clientSeq: result.clientSeq, clientMsgNo: result.clientMsgNo, err: result.err}
}

func (s *clientSession) isStopped() bool {
	select {
	case <-s.stopCh:
		return true
	default:
		return false
	}
}

func (s *clientSession) notifyQueueDrain() {
	select {
	case s.drainCh <- struct{}{}:
	default:
	}
}

func newClientSession(inner *wkclient.Client, frameBufferSize int) *clientSession {
	session := &clientSession{
		inner:             inner,
		recvCh:            make(chan frame.Frame, frameBufferSize),
		sendackCh:         make(chan *frame.SendackPacket, frameBufferSize),
		errCh:             make(chan errorResult, frameBufferSize),
		stopCh:            make(chan struct{}),
		drainCh:           make(chan struct{}, 1),
		readPermit:        make(chan struct{}, 1),
		publicationPermit: make(chan struct{}, frameBufferSize),
	}
	session.readPermit <- struct{}{}
	for i := 0; i < frameBufferSize; i++ {
		session.publicationPermit <- struct{}{}
	}
	return session
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

func (s *clientSession) completePendingPublication() {
	s.pendingMu.Lock()
	if s.pendingSendacks > 0 {
		s.pendingSendacks--
		s.publicationCurrent.Add(-1)
		s.publicationPermit <- struct{}{}
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

func (s *clientSession) waitPublishedResultsDrained() bool {
	for {
		if len(s.recvCh) == 0 && len(s.sendackCh) == 0 && len(s.errCh) == 0 {
			return true
		}
		select {
		case <-s.drainCh:
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
	return wkclient.New(c.innerConfig())
}

func (c *Client) innerConfig() wkclient.Config {
	return wkclient.Config{
		Addr:                   c.addr,
		Token:                  c.token,
		Dialer:                 c.dialer,
		OperationTimeout:       c.operationTimeout,
		AckTimeout:             c.ackTimeout,
		SendQueueCapacity:      c.sendQueueCapacity,
		MaxInflight:            c.maxInflight,
		ReadBufferSize:         c.readBufferSize,
		InboundFrameBufferSize: c.frameBufferSize,
	}
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
