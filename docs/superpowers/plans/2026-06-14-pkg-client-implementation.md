# pkg/client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a tooling-grade WKProto TCP client in `pkg/client` with high-throughput batched SEND, internal SENDACK matching, RECV/RECVACK handling, and optional multi-session pooling for wkbench, e2e tests, and server-side Go tools.

**Architecture:** Implement one focused `Client` that owns a single authenticated TCP session, a single reader loop, a single writer pump, and a pending SENDACK tracker. Batched sending is implemented as contiguous normal WKProto SEND frames written by the writer pump; no protocol-level SENDBATCH frame is added. Add a thin `Pool` after the single-client behavior is stable, then migrate existing bench/e2e helpers to use the new package.

**Tech Stack:** Go 1.25, `net.Conn`, `context`, `sync`, `sync/atomic`, `pkg/protocol/codec`, `pkg/protocol/frame`, `pkg/protocol/wkprotoenc`, `net.Pipe`/fake dialers for fast unit tests.

---

## File Structure

- Create `pkg/client/FLOW.md`: package responsibilities, lifecycle, SEND flow, RECV flow, pool flow.
- Create `pkg/client/options.go`: `Config`, `ConnectOptions`, `PoolConfig`, defaults, validation.
- Create `pkg/client/message.go`: `Message`, `RoutedMessage`, `Identity`, `SendResult`, `SendFuture`.
- Create `pkg/client/errors.go`: sentinel errors and `SendError`.
- Create `pkg/client/observer.go`: optional low-cardinality observer contracts and event structs.
- Create `pkg/client/crypto.go`: client keypair generation, CONNECT packet construction, session crypto, SEND encryption, RECV decryption.
- Create `pkg/client/pending.go`: pending SENDACK map, futures, timeout and close fanout.
- Create `pkg/client/writer.go`: writer request queue, control frame writes, SEND batch collection, full writes with deadlines.
- Create `pkg/client/reader.go`: partial-frame buffer, single reader loop, SENDACK/RECV/PONG/DISCONNECT routing.
- Create `pkg/client/client.go`: `Client` construction, connect/close, public send/recv/ping/recvack methods.
- Create `pkg/client/pool.go`: optional multi-session pool and round-robin address assignment.
- Create `pkg/client/client_test.go`: handshake, crypto, send batch, recv, close, timeout integration tests.
- Create `pkg/client/pending_test.go`: focused pending tracker tests.
- Create `pkg/client/writer_test.go`: writer batching and queue behavior tests.
- Create `pkg/client/pool_test.go`: pool routing and result reassembly tests.
- Modify `internal/bench/wkproto/client.go`: wrap `pkg/client` while preserving current benchmark API.
- Modify `internal/bench/wkproto/client_test.go`: keep behavior tests passing after wrapper migration.
- Modify `test/e2e/suite/wkproto_client.go`: replace duplicate client implementation with `pkg/client`.
- Modify `test/e2e/suite/wkproto_client_test.go`: keep e2e crypto behavior unchanged.

Do not touch user/channel/subscriber preparation code in this plan.

## Task 1: Package Foundation

**Files:**
- Create: `pkg/client/options.go`
- Create: `pkg/client/message.go`
- Create: `pkg/client/errors.go`
- Create: `pkg/client/observer.go`
- Create: `pkg/client/FLOW.md`
- Test: `pkg/client/client_test.go`

- [ ] **Step 1: Write the failing config defaults test**

Add `pkg/client/client_test.go` with this initial content:

```go
package client

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeConfigAppliesToolingDefaults(t *testing.T) {
	cfg, err := normalizeConfig(Config{Addr: "127.0.0.1:5100"})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	if cfg.OperationTimeout != 5*time.Second {
		t.Fatalf("OperationTimeout = %s, want 5s", cfg.OperationTimeout)
	}
	if cfg.AckTimeout != 5*time.Second {
		t.Fatalf("AckTimeout = %s, want 5s", cfg.AckTimeout)
	}
	if cfg.SendQueueCapacity != 8192 {
		t.Fatalf("SendQueueCapacity = %d, want 8192", cfg.SendQueueCapacity)
	}
	if cfg.MaxInflight != 8192 {
		t.Fatalf("MaxInflight = %d, want 8192", cfg.MaxInflight)
	}
	if cfg.BatchMaxRecords != 512 {
		t.Fatalf("BatchMaxRecords = %d, want 512", cfg.BatchMaxRecords)
	}
	if cfg.BatchMaxBytes != 512*1024 {
		t.Fatalf("BatchMaxBytes = %d, want %d", cfg.BatchMaxBytes, 512*1024)
	}
	if cfg.BatchMaxWait != time.Millisecond {
		t.Fatalf("BatchMaxWait = %s, want 1ms", cfg.BatchMaxWait)
	}
	if cfg.ReadBufferSize != 4096 {
		t.Fatalf("ReadBufferSize = %d, want 4096", cfg.ReadBufferSize)
	}
	if cfg.InboundFrameBufferSize != 1024 {
		t.Fatalf("InboundFrameBufferSize = %d, want 1024", cfg.InboundFrameBufferSize)
	}
	if cfg.GenerateClientMsgNo {
		t.Fatal("GenerateClientMsgNo default = true, want false")
	}
}

func TestNormalizeConfigRequiresAddr(t *testing.T) {
	_, err := normalizeConfig(Config{})
	if !errors.Is(err, ErrMissingAddr) {
		t.Fatalf("normalizeConfig() error = %v, want %v", err, ErrMissingAddr)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./pkg/client
```

Expected: FAIL because `pkg/client` and `normalizeConfig` do not exist yet.

- [ ] **Step 3: Implement public DTOs, errors, observer contracts, and config defaults**

Create `pkg/client/errors.go`:

```go
package client

import (
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

var (
	ErrMissingAddr        = errors.New("client: addr is required")
	ErrNotConnected      = errors.New("client: not connected")
	ErrClosed            = errors.New("client: closed")
	ErrPayloadTooLarge   = errors.New("client: payload too large")
	ErrSendQueueFull     = errors.New("client: send queue full")
	ErrAckTimeout        = errors.New("client: sendack timeout")
	ErrClientSeqExhausted = errors.New("client: client sequence exhausted")
	ErrInvalidMessage    = errors.New("client: invalid message")
)

// SendError reports a non-success SENDACK for one SEND item.
type SendError struct {
	ClientSeq   uint64
	ClientMsgNo string
	ReasonCode  frame.ReasonCode
}

func (e SendError) Error() string {
	return fmt.Sprintf("client: sendack reason=%s client_seq=%d client_msg_no=%q", e.ReasonCode, e.ClientSeq, e.ClientMsgNo)
}
```

Create `pkg/client/message.go`:

```go
package client

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// Identity identifies one WKProto client session.
type Identity struct {
	UID      string
	DeviceID string
	Token    string
}

// Message is one outbound WKProto SEND command.
type Message struct {
	Setting     frame.Setting
	Expire      uint32
	ClientSeq   uint64
	ClientMsgNo string
	ChannelID   string
	ChannelType uint8
	Topic       string
	Payload     []byte
}

// RoutedMessage carries a SEND command plus the sender UID used by Pool.
type RoutedMessage struct {
	UID     string
	Message Message
}

// SendResult mirrors the successful or failed server SENDACK fields.
type SendResult struct {
	ClientSeq   uint64
	ClientMsgNo string
	MessageID   int64
	MessageSeq  uint64
	ReasonCode  frame.ReasonCode
}

// SendFuture resolves when the matching SENDACK arrives or the send fails.
type SendFuture struct {
	done <-chan sendOutcome
}

// Wait waits for the future result.
func (f *SendFuture) Wait(ctx context.Context) (SendResult, error) {
	if f == nil || f.done == nil {
		return SendResult{}, ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case out := <-f.done:
		return out.result, out.err
	case <-ctx.Done():
		return SendResult{}, ctx.Err()
	}
}
```

Create `pkg/client/observer.go`:

```go
package client

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// Observer receives optional low-cardinality client observations.
type Observer interface {
	OnConnect(ConnectEvent)
	OnSendQueue(SendQueueEvent)
	OnSendBatch(SendBatchEvent)
	OnSendAck(SendAckEvent)
	OnRecv(RecvEvent)
	OnError(ErrorEvent)
}

type ConnectEvent struct {
	Addr    string
	UID     string
	Elapsed time.Duration
	Err     error
}

type SendQueueEvent struct {
	Depth    int
	Capacity int
	Result   string
}

type SendBatchEvent struct {
	Records int
	Bytes   int
	Elapsed time.Duration
	Err     error
}

type SendAckEvent struct {
	ReasonCode frame.ReasonCode
	Elapsed    time.Duration
	Err        error
}

type RecvEvent struct {
	ChannelType uint8
	Bytes       int
	Dropped     bool
}

type ErrorEvent struct {
	Op    string
	Class string
	Err   error
}
```

Create `pkg/client/options.go`:

```go
package client

import (
	"context"
	"net"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	defaultOperationTimeout     = 5 * time.Second
	defaultAckTimeout           = 5 * time.Second
	defaultSendQueueCapacity    = 8192
	defaultMaxInflight          = 8192
	defaultBatchMaxRecords      = 512
	defaultBatchMaxBytes        = 512 * 1024
	defaultBatchMaxWait         = time.Millisecond
	defaultReadBufferSize       = 4096
	defaultInboundFrameBuffer   = 1024
	defaultPoolBalanceRoundRobin = "round_robin"
)

// Dialer opens TCP connections for Client.
type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// Config controls one WKProto client session.
type Config struct {
	Addr                   string
	Token                  string
	Dialer                 Dialer
	OperationTimeout       time.Duration
	AckTimeout             time.Duration
	SendQueueCapacity      int
	MaxInflight            int
	BatchMaxRecords        int
	BatchMaxBytes          int
	BatchMaxWait           time.Duration
	ReadBufferSize         int
	InboundFrameBufferSize int
	AutoRecvAck            bool
	GenerateClientMsgNo    bool
	Observer               Observer
}

// ConnectOptions controls the WKProto CONNECT packet.
type ConnectOptions struct {
	UID        string
	DeviceID   string
	DeviceFlag frame.DeviceFlag
	Token      string
}

// PoolConfig controls a set of WKProto sessions.
type PoolConfig struct {
	Addrs                []string
	Balance              string
	Client               Config
	ConnectRatePerSecond int
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.Addr == "" {
		return Config{}, ErrMissingAddr
	}
	if cfg.Dialer == nil {
		cfg.Dialer = &net.Dialer{}
	}
	if cfg.OperationTimeout <= 0 {
		cfg.OperationTimeout = defaultOperationTimeout
	}
	if cfg.AckTimeout <= 0 {
		cfg.AckTimeout = defaultAckTimeout
	}
	if cfg.SendQueueCapacity <= 0 {
		cfg.SendQueueCapacity = defaultSendQueueCapacity
	}
	if cfg.MaxInflight <= 0 {
		cfg.MaxInflight = defaultMaxInflight
	}
	if cfg.BatchMaxRecords <= 0 {
		cfg.BatchMaxRecords = defaultBatchMaxRecords
	}
	if cfg.BatchMaxBytes <= 0 {
		cfg.BatchMaxBytes = defaultBatchMaxBytes
	}
	if cfg.BatchMaxWait == 0 {
		cfg.BatchMaxWait = defaultBatchMaxWait
	} else if cfg.BatchMaxWait < 0 {
		cfg.BatchMaxWait = 0
	}
	if cfg.ReadBufferSize <= 0 {
		cfg.ReadBufferSize = defaultReadBufferSize
	}
	if cfg.InboundFrameBufferSize <= 0 {
		cfg.InboundFrameBufferSize = defaultInboundFrameBuffer
	}
	return cfg, nil
}
```

Make sure `options.go` imports `github.com/WuKongIM/WuKongIM/pkg/protocol/frame`.

Create `pkg/client/FLOW.md`:

```markdown
# pkg/client Flow

`pkg/client` is a tooling-grade WKProto TCP client for wkbench, e2e tests, and server-side Go tools.

It owns protocol connection behavior only: CONNECT/CONNACK, optional session encryption, SEND/SENDACK, RECV/RECVACK, PING/PONG, one writer pump, one reader loop, and optional pooling. It does not prepare users, channels, subscribers, or tokens.

SEND batching writes multiple normal WKProto SEND frames contiguously on one TCP stream. No SENDBATCH frame is introduced.
```

- [ ] **Step 4: Run the config tests**

Run:

```bash
go test ./pkg/client
```

Expected: PASS for the default config tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/client
git commit -m "feat: add pkg client foundation"
```

## Task 2: Test Harness and CONNECT Handshake

**Files:**
- Create: `pkg/client/test_server_test.go`
- Create: `pkg/client/client.go`
- Create: `pkg/client/crypto.go`
- Modify: `pkg/client/client_test.go`

- [ ] **Step 1: Add a fake WKProto server test harness**

Create `pkg/client/test_server_test.go`:

```go
package client

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type fakeDialer struct {
	conn net.Conn
}

func (d fakeDialer) DialContext(context.Context, string, string) (net.Conn, error) {
	return d.conn, nil
}

func newPipeClientServer(t *testing.T, handler func(*testing.T, net.Conn)) (Config, func()) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler(t, serverConn)
		_ = serverConn.Close()
	}()
	cfg := Config{
		Addr:             "pipe",
		Dialer:           fakeDialer{conn: clientConn},
		OperationTimeout: time.Second,
		AckTimeout:       time.Second,
	}
	cleanup := func() {
		_ = clientConn.Close()
		<-done
	}
	return cfg, cleanup
}

func readTestFrame(t *testing.T, conn net.Conn) frame.Frame {
	t.Helper()
	f, err := codec.New().DecodePacketWithConn(conn, frame.LatestVersion)
	if err != nil {
		t.Fatalf("DecodePacketWithConn() error = %v", err)
	}
	return f
}

func writeTestFrame(t *testing.T, conn net.Conn, f frame.Frame) {
	t.Helper()
	payload, err := codec.New().EncodeFrame(f, frame.LatestVersion)
	if err != nil {
		t.Fatalf("EncodeFrame(%T) error = %v", f, err)
	}
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("conn.Write(%T) error = %v", f, err)
	}
}
```

- [ ] **Step 2: Add failing CONNECT tests**

Append to `pkg/client/client_test.go`:

```go
func TestClientConnectSendsConnectPacketAndStartsLoops(t *testing.T) {
	cfg, cleanup := newPipeClientServer(t, func(t *testing.T, conn net.Conn) {
		f := readTestFrame(t, conn)
		connect, ok := f.(*frame.ConnectPacket)
		if !ok {
			t.Fatalf("first frame = %T, want *frame.ConnectPacket", f)
		}
		if connect.UID != "u1" || connect.DeviceID != "d1" || connect.Token != "token-1" {
			t.Fatalf("connect identity = uid=%q device=%q token=%q", connect.UID, connect.DeviceID, connect.Token)
		}
		if connect.ClientKey == "" {
			t.Fatal("connect.ClientKey is empty")
		}
		writeTestFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})
	})
	defer cleanup()

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer c.Close()

	ack, err := c.Connect(context.Background(), ConnectOptions{UID: "u1", DeviceID: "d1", Token: "token-1"})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if ack.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("connack reason = %s, want success", ack.ReasonCode)
	}
}

func TestClientConnectRejectsNonSuccessConnack(t *testing.T) {
	cfg, cleanup := newPipeClientServer(t, func(t *testing.T, conn net.Conn) {
		_ = readTestFrame(t, conn)
		writeTestFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonAuthFail, ServerVersion: frame.LatestVersion})
	})
	defer cleanup()

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer c.Close()

	if _, err := c.Connect(context.Background(), ConnectOptions{UID: "u1", DeviceID: "d1"}); err == nil {
		t.Fatal("Connect() error = nil, want auth failure")
	}
}
```

Make sure `client_test.go` imports `context`, `net`, and `github.com/WuKongIM/WuKongIM/pkg/protocol/frame`.

- [ ] **Step 3: Run tests to verify failure**

Run:

```bash
go test ./pkg/client
```

Expected: FAIL because `New`, `Client`, `Connect`, and loop state are not implemented.

- [ ] **Step 4: Implement minimal client construction and CONNECT**

Create `pkg/client/crypto.go`:

```go
package client

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

type cryptoState struct {
	private [32]byte
	public  [32]byte
	session *wkprotoenc.SessionCrypto
}

func newCryptoState() (cryptoState, error) {
	private, public, err := wkprotoenc.GenerateKeyPair()
	if err != nil {
		return cryptoState{}, err
	}
	return cryptoState{private: private, public: public}, nil
}

func (s *cryptoState) connectPacket(opts ConnectOptions, defaultToken string) *frame.ConnectPacket {
	token := opts.Token
	if token == "" {
		token = defaultToken
	}
	flag := opts.DeviceFlag
	if flag == 0 {
		flag = frame.APP
	}
	return &frame.ConnectPacket{
		Version:         frame.LatestVersion,
		ClientKey:       wkprotoenc.EncodePublicKey(s.public),
		DeviceID:        opts.DeviceID,
		DeviceFlag:      flag,
		ClientTimestamp: time.Now().UnixMilli(),
		UID:             opts.UID,
		Token:           token,
	}
}

func (s *cryptoState) applyConnack(ack *frame.ConnackPacket) error {
	if ack == nil || ack.ServerKey == "" || ack.Salt == "" {
		return nil
	}
	keys, err := wkprotoenc.DeriveClientSession(s.private, ack.ServerKey, ack.Salt)
	if err != nil {
		return err
	}
	session, err := wkprotoenc.NewSessionCrypto(keys)
	if err != nil {
		return err
	}
	s.session = session
	return nil
}
```

Create `pkg/client/client.go`:

```go
package client

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// Client owns one authenticated WKProto TCP session.
type Client struct {
	cfg    Config
	proto  *codec.WKProto
	crypto cryptoState

	mu      sync.Mutex
	conn    net.Conn
	closed  bool
	readerDone chan struct{}
	writerDone chan struct{}
}

func New(cfg Config) (*Client, error) {
	normalized, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	crypto, err := newCryptoState()
	if err != nil {
		return nil, err
	}
	return &Client{cfg: normalized, proto: codec.New(), crypto: crypto}, nil
}

func (c *Client) Connect(ctx context.Context, opts ConnectOptions) (*frame.ConnackPacket, error) {
	if c == nil {
		return nil, ErrClosed
	}
	ctx, cancel := c.withDefaultTimeout(ctx)
	defer cancel()

	start := time.Now()
	conn, err := c.cfg.Dialer.DialContext(ctx, "tcp", c.cfg.Addr)
	if err != nil {
		c.observeConnect(opts.UID, time.Since(start), err)
		return nil, err
	}
	c.mu.Lock()
	c.conn = conn
	c.closed = false
	c.mu.Unlock()

	if err := c.writeFrameSync(ctx, c.crypto.connectPacket(opts, c.cfg.Token)); err != nil {
		_ = c.Close()
		c.observeConnect(opts.UID, time.Since(start), err)
		return nil, err
	}
	f, err := c.readFrameSync(ctx)
	if err != nil {
		_ = c.Close()
		c.observeConnect(opts.UID, time.Since(start), err)
		return nil, err
	}
	ack, ok := f.(*frame.ConnackPacket)
	if !ok {
		_ = c.Close()
		err := fmt.Errorf("client: expected *frame.ConnackPacket, got %T", f)
		c.observeConnect(opts.UID, time.Since(start), err)
		return nil, err
	}
	if ack.ReasonCode != frame.ReasonSuccess {
		_ = c.Close()
		err := SendError{ReasonCode: ack.ReasonCode}
		c.observeConnect(opts.UID, time.Since(start), err)
		return nil, err
	}
	if err := c.crypto.applyConnack(ack); err != nil {
		_ = c.Close()
		c.observeConnect(opts.UID, time.Since(start), err)
		return nil, err
	}
	c.startLoops()
	c.observeConnect(opts.UID, time.Since(start), nil)
	return ack, nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.closed = true
	c.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}
```

Create temporary sync helpers in `client.go`; later tasks will replace loop bodies:

```go
func (c *Client) startLoops() {
	c.mu.Lock()
	c.readerDone = make(chan struct{})
	c.writerDone = make(chan struct{})
	close(c.readerDone)
	close(c.writerDone)
	c.mu.Unlock()
}

func (c *Client) withDefaultTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok || c.cfg.OperationTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.cfg.OperationTimeout)
}

func (c *Client) currentConn() (net.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.conn == nil {
		return nil, ErrNotConnected
	}
	return c.conn, nil
}

func (c *Client) writeFrameSync(ctx context.Context, f frame.Frame) error {
	conn, err := c.currentConn()
	if err != nil {
		return err
	}
	payload, err := c.proto.EncodeFrame(f, frame.LatestVersion)
	if err != nil {
		return err
	}
	deadline, ok := ctx.Deadline()
	if ok {
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		defer func() { _ = conn.SetWriteDeadline(time.Time{}) }()
	}
	for len(payload) > 0 {
		n, err := conn.Write(payload)
		if err != nil {
			return err
		}
		payload = payload[n:]
	}
	return ctx.Err()
}

func (c *Client) readFrameSync(ctx context.Context) (frame.Frame, error) {
	conn, err := c.currentConn()
	if err != nil {
		return nil, err
	}
	deadline, ok := ctx.Deadline()
	if ok {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	}
	f, err := c.proto.DecodePacketWithConn(conn, frame.LatestVersion)
	if err != nil {
		return nil, err
	}
	return f, ctx.Err()
}

func (c *Client) observeConnect(uid string, elapsed time.Duration, err error) {
	if c == nil || c.cfg.Observer == nil {
		return
	}
	c.cfg.Observer.OnConnect(ConnectEvent{Addr: c.cfg.Addr, UID: uid, Elapsed: elapsed, Err: err})
}
```

- [ ] **Step 5: Run the connect tests**

Run:

```bash
go test ./pkg/client
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/client
git commit -m "feat: add wkproto client connect"
```

## Task 3: Pending SENDACK Futures

**Files:**
- Create: `pkg/client/pending.go`
- Test: `pkg/client/pending_test.go`

- [ ] **Step 1: Write pending tracker tests**

Create `pkg/client/pending_test.go`:

```go
package client

import (
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestPendingTrackerResolvesMatchingSendack(t *testing.T) {
	p := newPendingTracker()
	entry, err := p.add(pendingKey{clientSeq: 7, clientMsgNo: "m7"}, time.Second)
	if err != nil {
		t.Fatalf("add() error = %v", err)
	}
	p.resolve(&frame.SendackPacket{
		ClientSeq:   7,
		ClientMsgNo: "m7",
		MessageID:   99,
		MessageSeq:  3,
		ReasonCode:  frame.ReasonSuccess,
	})
	out := <-entry.done
	if out.err != nil {
		t.Fatalf("future err = %v", out.err)
	}
	if out.result.MessageID != 99 || out.result.MessageSeq != 3 {
		t.Fatalf("result = %#v, want message 99 seq 3", out.result)
	}
}

func TestPendingTrackerReportsNonSuccessSendack(t *testing.T) {
	p := newPendingTracker()
	entry, err := p.add(pendingKey{clientSeq: 8, clientMsgNo: "m8"}, time.Second)
	if err != nil {
		t.Fatalf("add() error = %v", err)
	}
	p.resolve(&frame.SendackPacket{ClientSeq: 8, ClientMsgNo: "m8", ReasonCode: frame.ReasonRateLimit})
	out := <-entry.done
	var sendErr SendError
	if !errors.As(out.err, &sendErr) {
		t.Fatalf("future err = %v, want SendError", out.err)
	}
	if sendErr.ReasonCode != frame.ReasonRateLimit {
		t.Fatalf("reason = %s, want rate limit", sendErr.ReasonCode)
	}
}

func TestPendingTrackerCloseFailsAllEntries(t *testing.T) {
	p := newPendingTracker()
	first, _ := p.add(pendingKey{clientSeq: 1}, time.Second)
	second, _ := p.add(pendingKey{clientSeq: 2, clientMsgNo: "m2"}, time.Second)
	p.close(ErrClosed)
	for _, entry := range []*pendingEntry{first, second} {
		out := <-entry.done
		if !errors.Is(out.err, ErrClosed) {
			t.Fatalf("future err = %v, want ErrClosed", out.err)
		}
	}
}
```

- [ ] **Step 2: Run the pending tests to verify failure**

Run:

```bash
go test ./pkg/client -run PendingTracker
```

Expected: FAIL because `newPendingTracker` and related types do not exist.

- [ ] **Step 3: Implement pending tracker**

Create `pkg/client/pending.go`:

```go
package client

import (
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type sendOutcome struct {
	result SendResult
	err    error
}

type pendingKey struct {
	clientSeq   uint64
	clientMsgNo string
}

type pendingEntry struct {
	key       pendingKey
	done      chan sendOutcome
	timer     *time.Timer
	startedAt time.Time
}

type pendingTracker struct {
	mu      sync.Mutex
	entries map[pendingKey]*pendingEntry
	closed  bool
}

func newPendingTracker() *pendingTracker {
	return &pendingTracker{entries: make(map[pendingKey]*pendingEntry)}
}

func (p *pendingTracker) add(key pendingKey, timeout time.Duration) (*pendingEntry, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrClosed
	}
	entry := &pendingEntry{key: key, done: make(chan sendOutcome, 1), startedAt: time.Now()}
	p.entries[key] = entry
	if timeout > 0 {
		entry.timer = time.AfterFunc(timeout, func() {
			p.fail(key, ErrAckTimeout)
		})
	}
	return entry, nil
}

func (p *pendingTracker) resolve(ack *frame.SendackPacket) bool {
	if ack == nil {
		return false
	}
	key := pendingKey{clientSeq: ack.ClientSeq, clientMsgNo: ack.ClientMsgNo}
	entry := p.take(key)
	if entry == nil && ack.ClientMsgNo != "" {
		entry = p.take(pendingKey{clientSeq: ack.ClientSeq})
	}
	if entry == nil {
		return false
	}
	result := SendResult{
		ClientSeq:   ack.ClientSeq,
		ClientMsgNo: ack.ClientMsgNo,
		MessageID:   ack.MessageID,
		MessageSeq:  ack.MessageSeq,
		ReasonCode:  ack.ReasonCode,
	}
	var err error
	if ack.ReasonCode != frame.ReasonSuccess {
		err = SendError{ClientSeq: ack.ClientSeq, ClientMsgNo: ack.ClientMsgNo, ReasonCode: ack.ReasonCode}
	}
	entry.done <- sendOutcome{result: result, err: err}
	return true
}

func (p *pendingTracker) fail(key pendingKey, err error) {
	entry := p.take(key)
	if entry == nil {
		return
	}
	entry.done <- sendOutcome{result: SendResult{ClientSeq: key.clientSeq, ClientMsgNo: key.clientMsgNo}, err: err}
}

func (p *pendingTracker) take(key pendingKey) *pendingEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entries[key]
	if entry == nil {
		return nil
	}
	delete(p.entries, key)
	if entry.timer != nil {
		entry.timer.Stop()
	}
	return entry
}

func (p *pendingTracker) close(err error) {
	p.mu.Lock()
	entries := p.entries
	p.entries = make(map[pendingKey]*pendingEntry)
	p.closed = true
	p.mu.Unlock()
	for _, entry := range entries {
		if entry.timer != nil {
			entry.timer.Stop()
		}
		entry.done <- sendOutcome{result: SendResult{ClientSeq: entry.key.clientSeq, ClientMsgNo: entry.key.clientMsgNo}, err: err}
	}
}
```

- [ ] **Step 4: Run pending tests**

Run:

```bash
go test ./pkg/client -run PendingTracker
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/client/pending.go pkg/client/pending_test.go
git commit -m "feat: add client sendack pending tracker"
```

## Task 4: Writer Pump and Batch Collection

**Files:**
- Create: `pkg/client/writer.go`
- Test: `pkg/client/writer_test.go`
- Modify: `pkg/client/client.go`

- [ ] **Step 1: Write writer batch tests**

Create `pkg/client/writer_test.go`:

```go
package client

import (
	"testing"
	"time"
)

func TestWriterBatchCollectorHonorsMaxRecords(t *testing.T) {
	collector := newWriteBatchCollector(batchLimits{maxRecords: 2, maxBytes: 1024})
	reqs := []writeRequest{
		{kind: writeKindSend, msg: Message{ClientMsgNo: "m1", Payload: []byte("a")}},
		{kind: writeKindSend, msg: Message{ClientMsgNo: "m2", Payload: []byte("b")}},
		{kind: writeKindSend, msg: Message{ClientMsgNo: "m3", Payload: []byte("c")}},
	}
	first, rest := collector.collect(reqs)
	if len(first) != 2 {
		t.Fatalf("first batch len = %d, want 2", len(first))
	}
	if len(rest) != 1 || rest[0].msg.ClientMsgNo != "m3" {
		t.Fatalf("rest = %#v, want m3", rest)
	}
}

func TestWriterBatchCollectorHonorsMaxBytes(t *testing.T) {
	collector := newWriteBatchCollector(batchLimits{maxRecords: 8, maxBytes: 3})
	reqs := []writeRequest{
		{kind: writeKindSend, msg: Message{ClientMsgNo: "m1", Payload: []byte("aa")}},
		{kind: writeKindSend, msg: Message{ClientMsgNo: "m2", Payload: []byte("bb")}},
	}
	first, rest := collector.collect(reqs)
	if len(first) != 1 || first[0].msg.ClientMsgNo != "m1" {
		t.Fatalf("first = %#v, want m1 only", first)
	}
	if len(rest) != 1 || rest[0].msg.ClientMsgNo != "m2" {
		t.Fatalf("rest = %#v, want m2", rest)
	}
}

func TestWriterBatchCollectorAllowsZeroWaitReadyBatch(t *testing.T) {
	collector := newWriteBatchCollector(batchLimits{maxRecords: 8, maxBytes: 1024, maxWait: 0})
	first, rest := collector.collect([]writeRequest{
		{kind: writeKindSend, msg: Message{ClientMsgNo: "ready", Payload: []byte("x")}},
	})
	if len(first) != 1 || len(rest) != 0 {
		t.Fatalf("first/rest = %d/%d, want 1/0", len(first), len(rest))
	}
	_ = time.Millisecond
}
```

- [ ] **Step 2: Run writer tests to verify failure**

Run:

```bash
go test ./pkg/client -run WriterBatchCollector
```

Expected: FAIL because writer types do not exist.

- [ ] **Step 3: Implement writer request types and collector**

Create `pkg/client/writer.go`:

```go
package client

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type writeKind uint8

const (
	writeKindSend writeKind = iota + 1
	writeKindFrame
	writeKindClose
)

type writeRequest struct {
	kind   writeKind
	msg    Message
	frame  frame.Frame
	entry  *pendingEntry
	result chan error
	ctx    context.Context
}

type batchLimits struct {
	maxRecords int
	maxBytes   int
	maxWait    time.Duration
}

type writeBatchCollector struct {
	limits batchLimits
}

func newWriteBatchCollector(limits batchLimits) *writeBatchCollector {
	if limits.maxRecords <= 0 {
		limits.maxRecords = 1
	}
	return &writeBatchCollector{limits: limits}
}

func (c *writeBatchCollector) collect(reqs []writeRequest) ([]writeRequest, []writeRequest) {
	if len(reqs) == 0 {
		return nil, nil
	}
	batch := make([]writeRequest, 0, c.limits.maxRecords)
	byteCount := 0
	for i, req := range reqs {
		if req.kind != writeKindSend {
			if len(batch) == 0 {
				return reqs[:1], reqs[1:]
			}
			return batch, reqs[i:]
		}
		reqBytes := len(req.msg.Payload)
		if len(batch) > 0 && c.limits.maxBytes > 0 && byteCount+reqBytes > c.limits.maxBytes {
			return batch, reqs[i:]
		}
		batch = append(batch, req)
		byteCount += reqBytes
		if len(batch) >= c.limits.maxRecords {
			return batch, reqs[i+1:]
		}
	}
	return batch, nil
}
```

- [ ] **Step 4: Add writer pump lifecycle placeholders**

Modify `pkg/client/client.go` by adding fields:

```go
writeCh chan writeRequest
```

In `New`, initialize:

```go
writeCh: make(chan writeRequest, normalized.SendQueueCapacity),
```

Replace `startLoops` with:

```go
func (c *Client) startLoops() {
	c.mu.Lock()
	c.readerDone = make(chan struct{})
	c.writerDone = make(chan struct{})
	c.mu.Unlock()
	go func() {
		defer close(c.writerDone)
		c.writerLoop()
	}()
	go func() {
		defer close(c.readerDone)
		c.readerLoop()
	}()
}
```

Add temporary loop methods:

```go
func (c *Client) writerLoop() {}
func (c *Client) readerLoop() {}
```

- [ ] **Step 5: Run writer tests**

Run:

```bash
go test ./pkg/client -run WriterBatchCollector
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/client
git commit -m "feat: add client writer batch collector"
```

## Task 5: Reader Loop and Frame Routing

**Files:**
- Create: `pkg/client/reader.go`
- Modify: `pkg/client/client.go`
- Modify: `pkg/client/client_test.go`

- [ ] **Step 1: Write reader tests for partial and concatenated frames**

Append to `pkg/client/client_test.go`:

```go
func TestClientReaderMatchesOutOfOrderSendacks(t *testing.T) {
	cfg, cleanup := newPipeClientServer(t, func(t *testing.T, conn net.Conn) {
		_ = readTestFrame(t, conn)
		writeTestFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})
		first := readTestFrame(t, conn).(*frame.SendPacket)
		second := readTestFrame(t, conn).(*frame.SendPacket)
		writeTestFrame(t, conn, &frame.SendackPacket{ClientSeq: second.ClientSeq, ClientMsgNo: second.ClientMsgNo, ReasonCode: frame.ReasonSuccess, MessageID: 2})
		writeTestFrame(t, conn, &frame.SendackPacket{ClientSeq: first.ClientSeq, ClientMsgNo: first.ClientMsgNo, ReasonCode: frame.ReasonSuccess, MessageID: 1})
	})
	defer cleanup()

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer c.Close()
	if _, err := c.Connect(context.Background(), ConnectOptions{UID: "u1", DeviceID: "d1"}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	results, err := c.SendBatch(context.Background(), []Message{
		{ClientMsgNo: "m1", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("a")},
		{ClientMsgNo: "m2", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("b")},
	})
	if err != nil {
		t.Fatalf("SendBatch() error = %v", err)
	}
	if results[0].MessageID != 1 || results[1].MessageID != 2 {
		t.Fatalf("results = %#v, want input order message IDs 1,2", results)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run:

```bash
go test ./pkg/client -run OutOfOrderSendacks
```

Expected: FAIL because public send and reader routing are not implemented.

- [ ] **Step 3: Implement reader loop**

Create `pkg/client/reader.go`:

```go
package client

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

func (c *Client) readerLoop() {
	buf := make([]byte, 0, c.cfg.ReadBufferSize)
	scratch := make([]byte, c.cfg.ReadBufferSize)
	for {
		conn, err := c.currentConn()
		if err != nil {
			c.failRead(err)
			return
		}
		n, err := conn.Read(scratch)
		if n > 0 {
			buf = append(buf, scratch[:n]...)
			for {
				f, consumed, decErr := c.proto.DecodeFrame(buf, frame.LatestVersion)
				if decErr != nil {
					c.failRead(decErr)
					return
				}
				if f == nil || consumed == 0 {
					break
				}
				detachInboundPayload(f)
				if consumed >= len(buf) {
					buf = buf[:0]
				} else {
					copy(buf, buf[consumed:])
					buf = buf[:len(buf)-consumed]
				}
				if err := c.routeInboundFrame(f); err != nil {
					c.failRead(err)
					return
				}
			}
		}
		if err != nil {
			c.failRead(err)
			return
		}
	}
}

func detachInboundPayload(f frame.Frame) {
	switch pkt := f.(type) {
	case *frame.SendPacket:
		pkt.Payload = append([]byte(nil), pkt.Payload...)
	case *frame.RecvPacket:
		pkt.Payload = append([]byte(nil), pkt.Payload...)
	}
}

func (c *Client) routeInboundFrame(f frame.Frame) error {
	switch pkt := f.(type) {
	case *frame.SendackPacket:
		if c.pending != nil {
			c.pending.resolve(pkt)
		}
	case *frame.RecvPacket:
		if err := c.decryptRecv(pkt); err != nil {
			return err
		}
		c.enqueueRecv(pkt)
	case *frame.PongPacket:
		return nil
	case *frame.DisconnectPacket:
		return fmt.Errorf("client: disconnected by server: %s", pkt.Reason)
	}
	return nil
}

func (c *Client) decryptRecv(pkt *frame.RecvPacket) error {
	if pkt == nil || pkt.Setting.IsSet(frame.SettingNoEncrypt) || c.crypto.session == nil {
		return nil
	}
	plain, err := wkprotoenc.DecryptPayloadWithCrypto(pkt.Payload, c.crypto.session)
	if err != nil {
		return err
	}
	pkt.Payload = plain
	return nil
}

func (c *Client) failRead(err error) {
	if c.pending != nil {
		c.pending.close(err)
	}
	_ = c.Close()
}

func (c *Client) readFrameSync(ctx context.Context) (frame.Frame, error) {
	conn, err := c.currentConn()
	if err != nil {
		return nil, err
	}
	deadline, ok := ctx.Deadline()
	if ok {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return nil, err
		}
		defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	}
	f, err := c.proto.DecodePacketWithConn(conn, frame.LatestVersion)
	if err != nil {
		return nil, err
	}
	return f, ctx.Err()
}
```

Move the old `readFrameSync` out of `client.go` to avoid duplicate definitions.

- [ ] **Step 4: Add client fields for pending and recv queue**

Modify `pkg/client/client.go`:

```go
pending *pendingTracker
recvCh  chan *frame.RecvPacket
```

Initialize in `New`:

```go
pending: newPendingTracker(),
recvCh:  make(chan *frame.RecvPacket, normalized.InboundFrameBufferSize),
```

Add `enqueueRecv`:

```go
func (c *Client) enqueueRecv(pkt *frame.RecvPacket) {
	select {
	case c.recvCh <- pkt:
	default:
		select {
		case <-c.recvCh:
		default:
		}
		c.recvCh <- pkt
	}
}
```

- [ ] **Step 5: Run the reader test**

Run:

```bash
go test ./pkg/client -run OutOfOrderSendacks
```

Expected: still FAIL until Task 6 implements send enqueue/write.

- [ ] **Step 6: Commit reader scaffolding**

```bash
git add pkg/client
git commit -m "feat: add client reader routing"
```

## Task 6: Public Send, SendBatch, SendAsync, and Writer Loop

**Files:**
- Modify: `pkg/client/client.go`
- Modify: `pkg/client/writer.go`
- Modify: `pkg/client/crypto.go`
- Modify: `pkg/client/client_test.go`

- [ ] **Step 1: Add send validation tests**

Append to `pkg/client/client_test.go`:

```go
func TestClientSendRejectsOversizedPayload(t *testing.T) {
	c, err := New(Config{Addr: "127.0.0.1:5100"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = c.Send(context.Background(), Message{
		ClientMsgNo: "too-large",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     make([]byte, codec.PayloadMaxSize+1),
	})
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("Send() error = %v, want ErrPayloadTooLarge", err)
	}
}

func TestClientSendGeneratesClientSeq(t *testing.T) {
	msg := Message{ClientMsgNo: "m1", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("x")}
	pkt, err := buildSendPacket(msg, 9)
	if err != nil {
		t.Fatalf("buildSendPacket() error = %v", err)
	}
	if pkt.ClientSeq != 9 {
		t.Fatalf("ClientSeq = %d, want 9", pkt.ClientSeq)
	}
}
```

Add imports for `errors` and `github.com/WuKongIM/WuKongIM/pkg/protocol/codec`.

- [ ] **Step 2: Run send tests to verify failure**

Run:

```bash
go test ./pkg/client -run 'ClientSend|OutOfOrderSendacks'
```

Expected: FAIL because send methods are incomplete.

- [ ] **Step 3: Implement send packet building and encryption**

Add to `pkg/client/crypto.go`:

```go
func buildSendPacket(msg Message, assignedSeq uint64) (*frame.SendPacket, error) {
	if len(msg.Payload) > codec.PayloadMaxSize {
		return nil, ErrPayloadTooLarge
	}
	if msg.ChannelID == "" || msg.ChannelType == 0 {
		return nil, ErrInvalidMessage
	}
	seq := msg.ClientSeq
	if seq == 0 {
		seq = assignedSeq
	}
	return &frame.SendPacket{
		Setting:     msg.Setting,
		Expire:      msg.Expire,
		ClientSeq:   seq,
		ClientMsgNo: msg.ClientMsgNo,
		ChannelID:   msg.ChannelID,
		ChannelType: msg.ChannelType,
		Topic:       msg.Topic,
		Payload:     append([]byte(nil), msg.Payload...),
	}, nil
}

func (s *cryptoState) sealSend(pkt *frame.SendPacket) (*frame.SendPacket, error) {
	if pkt == nil || pkt.Setting.IsSet(frame.SettingNoEncrypt) || s.session == nil {
		return pkt, nil
	}
	cloned := *pkt
	encrypted, err := wkprotoenc.EncryptPayloadWithCrypto(cloned.Payload, s.session)
	if err != nil {
		return nil, err
	}
	cloned.Payload = encrypted
	msgKey, err := wkprotoenc.SendMsgKeyWithCrypto(&cloned, s.session)
	if err != nil {
		return nil, err
	}
	cloned.MsgKey = msgKey
	return &cloned, nil
}
```

Import `github.com/WuKongIM/WuKongIM/pkg/protocol/codec` in `crypto.go`.

- [ ] **Step 4: Implement public send methods**

Add to `pkg/client/client.go`:

```go
seq atomic.Uint64
```

Import `math`, `sync/atomic`, and `github.com/WuKongIM/WuKongIM/pkg/protocol/codec` where needed.

Add methods:

```go
func (c *Client) Send(ctx context.Context, msg Message) (SendResult, error) {
	results, err := c.SendBatch(ctx, []Message{msg})
	if err != nil {
		return SendResult{}, err
	}
	if len(results) != 1 {
		return SendResult{}, ErrClosed
	}
	return results[0], nil
}

func (c *Client) SendAsync(ctx context.Context, msg Message) (*SendFuture, error) {
	if c == nil {
		return nil, ErrClosed
	}
	seq, err := c.nextClientSeq(msg.ClientSeq)
	if err != nil {
		return nil, err
	}
	pkt, err := buildSendPacket(msg, seq)
	if err != nil {
		return nil, err
	}
	key := pendingKey{clientSeq: pkt.ClientSeq, clientMsgNo: pkt.ClientMsgNo}
	entry, err := c.pending.add(key, c.cfg.AckTimeout)
	if err != nil {
		return nil, err
	}
	req := writeRequest{kind: writeKindSend, msg: msg, entry: entry, ctx: ctx}
	req.msg.ClientSeq = pkt.ClientSeq
	select {
	case c.writeCh <- req:
		return &SendFuture{done: entry.done}, nil
	default:
		c.pending.fail(key, ErrSendQueueFull)
		return nil, ErrSendQueueFull
	}
}

func (c *Client) SendBatch(ctx context.Context, msgs []Message) ([]SendResult, error) {
	futures := make([]*SendFuture, len(msgs))
	results := make([]SendResult, len(msgs))
	for i, msg := range msgs {
		future, err := c.SendAsync(ctx, msg)
		if err != nil {
			return nil, err
		}
		futures[i] = future
	}
	for i, future := range futures {
		result, err := future.Wait(ctx)
		results[i] = result
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

func (c *Client) nextClientSeq(explicit uint64) (uint64, error) {
	if explicit > 0 {
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
```

- [ ] **Step 5: Implement writer loop**

Replace temporary `writerLoop` in `pkg/client/client.go` with a call to a real helper in `writer.go`:

```go
func (c *Client) writerLoop() {
	c.runWriterLoop()
}
```

Add to `pkg/client/writer.go`:

```go
func (c *Client) runWriterLoop() {
	collector := newWriteBatchCollector(batchLimits{
		maxRecords: c.cfg.BatchMaxRecords,
		maxBytes:   c.cfg.BatchMaxBytes,
		maxWait:    c.cfg.BatchMaxWait,
	})
	pending := make([]writeRequest, 0, c.cfg.BatchMaxRecords)
	for {
		req, ok := <-c.writeCh
		if !ok {
			return
		}
		pending = append(pending, req)
		if collector.limits.maxWait > 0 {
			timer := time.NewTimer(collector.limits.maxWait)
		Collect:
			for len(pending) < collector.limits.maxRecords {
				select {
				case next, ok := <-c.writeCh:
					if !ok {
						break Collect
					}
					pending = append(pending, next)
				case <-timer.C:
					break Collect
				}
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		for len(pending) > 0 {
			batch, rest := collector.collect(pending)
			if err := c.writeBatch(batch); err != nil {
				for _, item := range batch {
					if item.entry != nil {
						c.pending.fail(item.entry.key, err)
					}
					if item.result != nil {
						item.result <- err
					}
				}
			}
			pending = append(pending[:0], rest...)
		}
	}
}

func (c *Client) writeBatch(batch []writeRequest) error {
	conn, err := c.currentConn()
	if err != nil {
		return err
	}
	buf := make([]byte, 0, c.cfg.BatchMaxBytes)
	for _, req := range batch {
		switch req.kind {
		case writeKindSend:
			pkt, err := buildSendPacket(req.msg, req.msg.ClientSeq)
			if err != nil {
				return err
			}
			sealed, err := c.crypto.sealSend(pkt)
			if err != nil {
				return err
			}
			payload, err := c.proto.EncodeFrame(sealed, frame.LatestVersion)
			if err != nil {
				return err
			}
			buf = append(buf, payload...)
		case writeKindFrame:
			payload, err := c.proto.EncodeFrame(req.frame, frame.LatestVersion)
			if err != nil {
				return err
			}
			buf = append(buf, payload...)
		}
	}
	return fullWrite(conn, buf)
}

func fullWrite(conn net.Conn, payload []byte) error {
	for len(payload) > 0 {
		n, err := conn.Write(payload)
		if err != nil {
			return err
		}
		payload = payload[n:]
	}
	return nil
}
```

Import `net` and `github.com/WuKongIM/WuKongIM/pkg/protocol/frame` in `writer.go`.

- [ ] **Step 6: Run send tests**

Run:

```bash
go test ./pkg/client -run 'ClientSend|OutOfOrderSendacks|WriterBatchCollector'
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/client
git commit -m "feat: add client batched send"
```

## Task 7: RECV, AutoRecvAck, Ping, and Close Semantics

**Files:**
- Modify: `pkg/client/client.go`
- Modify: `pkg/client/reader.go`
- Modify: `pkg/client/writer.go`
- Modify: `pkg/client/client_test.go`

- [ ] **Step 1: Add RECV and AutoRecvAck tests**

Append to `pkg/client/client_test.go`:

```go
func TestClientRecvDecryptsAndAutoAcks(t *testing.T) {
	cfg, cleanup := newPipeClientServer(t, func(t *testing.T, conn net.Conn) {
		connect := readTestFrame(t, conn).(*frame.ConnectPacket)
		keys, serverKey, err := wkprotoenc.NegotiateServerSession(connect.ClientKey)
		if err != nil {
			t.Fatalf("NegotiateServerSession() error = %v", err)
		}
		writeTestFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion, ServerKey: serverKey, Salt: string(keys.AESIV)})
		sealed, err := wkprotoenc.SealRecvPacket(&frame.RecvPacket{
			MessageID:   42,
			MessageSeq:  9,
			ClientMsgNo: "m42",
			ChannelID:   "u1",
			ChannelType: frame.ChannelTypePerson,
			FromUID:     "u2",
			Payload:     []byte("hello"),
		}, keys)
		if err != nil {
			t.Fatalf("SealRecvPacket() error = %v", err)
		}
		writeTestFrame(t, conn, sealed)
		ack := readTestFrame(t, conn).(*frame.RecvackPacket)
		if ack.MessageID != 42 || ack.MessageSeq != 9 {
			t.Fatalf("recvack = (%d,%d), want (42,9)", ack.MessageID, ack.MessageSeq)
		}
	})
	defer cleanup()
	cfg.AutoRecvAck = true

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer c.Close()
	if _, err := c.Connect(context.Background(), ConnectOptions{UID: "u1", DeviceID: "d1"}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	recv, err := c.Recv(context.Background())
	if err != nil {
		t.Fatalf("Recv() error = %v", err)
	}
	if string(recv.Payload) != "hello" {
		t.Fatalf("recv payload = %q, want hello", recv.Payload)
	}
}
```

Import `github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc`.

- [ ] **Step 2: Add close pending test**

Append:

```go
func TestClientCloseFailsPendingSend(t *testing.T) {
	cfg, cleanup := newPipeClientServer(t, func(t *testing.T, conn net.Conn) {
		_ = readTestFrame(t, conn)
		writeTestFrame(t, conn, &frame.ConnackPacket{ReasonCode: frame.ReasonSuccess, ServerVersion: frame.LatestVersion})
		_ = readTestFrame(t, conn)
	})
	defer cleanup()

	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := c.Connect(context.Background(), ConnectOptions{UID: "u1", DeviceID: "d1"}); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	future, err := c.SendAsync(context.Background(), Message{ClientMsgNo: "m1", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("x")})
	if err != nil {
		t.Fatalf("SendAsync() error = %v", err)
	}
	_ = c.Close()
	_, err = future.Wait(context.Background())
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("future err = %v, want ErrClosed", err)
	}
}
```

- [ ] **Step 3: Run tests to verify failure**

Run:

```bash
go test ./pkg/client -run 'RecvDecrypts|CloseFailsPending'
```

Expected: FAIL because `Recv`, `RecvAck`, and close pending fanout are incomplete.

- [ ] **Step 4: Implement Recv, RecvAck, Ping, and control writes**

Add to `pkg/client/client.go`:

```go
func (c *Client) ReadFrame(ctx context.Context) (frame.Frame, error) {
	if c == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case pkt := <-c.recvCh:
		if pkt == nil {
			return nil, ErrClosed
		}
		return pkt, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

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

func (c *Client) RecvAck(ctx context.Context, messageID int64, messageSeq uint64) error {
	return c.writeControl(ctx, &frame.RecvackPacket{MessageID: messageID, MessageSeq: messageSeq})
}

func (c *Client) Ping(ctx context.Context) error {
	return c.writeControl(ctx, &frame.PingPacket{})
}

func (c *Client) writeControl(ctx context.Context, f frame.Frame) error {
	if c == nil {
		return ErrClosed
	}
	result := make(chan error, 1)
	req := writeRequest{kind: writeKindFrame, frame: f, result: result, ctx: ctx}
	select {
	case c.writeCh <- req:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
```

In `writeBatch`, send `nil` on `req.result` after a successful control frame batch:

```go
for _, req := range batch {
	if req.result != nil {
		req.result <- nil
	}
}
```

In `routeInboundFrame`, after enqueueing RECV:

```go
if c.cfg.AutoRecvAck {
	_ = c.RecvAck(context.Background(), pkt.MessageID, pkt.MessageSeq)
}
```

In `Close`, add:

```go
if c.pending != nil {
	c.pending.close(ErrClosed)
}
```

- [ ] **Step 5: Run client tests**

Run:

```bash
go test ./pkg/client
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/client
git commit -m "feat: add client recv and control frames"
```

## Task 8: Pool

**Files:**
- Create: `pkg/client/pool.go`
- Test: `pkg/client/pool_test.go`

- [ ] **Step 1: Write pool tests**

Create `pkg/client/pool_test.go`:

```go
package client

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestPoolSendBatchReassemblesOriginalOrder(t *testing.T) {
	p := &Pool{
		clients: map[string]poolClient{
			"u1": fakePoolClient{resultID: 1},
			"u2": fakePoolClient{resultID: 2},
		},
	}
	results, err := p.SendBatch(context.Background(), []RoutedMessage{
		{UID: "u2", Message: Message{ClientMsgNo: "m2", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Payload: []byte("b")}},
		{UID: "u1", Message: Message{ClientMsgNo: "m1", ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, Payload: []byte("a")}},
	})
	if err != nil {
		t.Fatalf("SendBatch() error = %v", err)
	}
	if results[0].MessageID != 2 || results[1].MessageID != 1 {
		t.Fatalf("results = %#v, want IDs 2,1", results)
	}
}

type fakePoolClient struct {
	resultID int64
}

func (f fakePoolClient) SendBatch(context.Context, []Message) ([]SendResult, error) {
	return []SendResult{{MessageID: f.resultID, ReasonCode: frame.ReasonSuccess}}, nil
}
func (f fakePoolClient) Close() error { return nil }
```

- [ ] **Step 2: Run pool tests to verify failure**

Run:

```bash
go test ./pkg/client -run Pool
```

Expected: FAIL because Pool is missing.

- [ ] **Step 3: Implement Pool**

Create `pkg/client/pool.go`:

```go
package client

import (
	"context"
	"fmt"
)

type poolClient interface {
	SendBatch(context.Context, []Message) ([]SendResult, error)
	Close() error
}

// Pool owns multiple WKProto sessions for tooling workloads.
type Pool struct {
	cfg     PoolConfig
	clients map[string]poolClient
}

func NewPool(cfg PoolConfig) (*Pool, error) {
	if len(cfg.Addrs) == 0 && cfg.Client.Addr == "" {
		return nil, ErrMissingAddr
	}
	return &Pool{cfg: cfg, clients: make(map[string]poolClient)}, nil
}

func (p *Pool) SendBatch(ctx context.Context, msgs []RoutedMessage) ([]SendResult, error) {
	results := make([]SendResult, len(msgs))
	type grouped struct {
		client poolClient
		indexes []int
		msgs []Message
	}
	groups := make(map[string]*grouped)
	for i, msg := range msgs {
		client := p.clients[msg.UID]
		if client == nil {
			return nil, fmt.Errorf("client pool: missing client for uid %q", msg.UID)
		}
		group := groups[msg.UID]
		if group == nil {
			group = &grouped{client: client}
			groups[msg.UID] = group
		}
		group.indexes = append(group.indexes, i)
		group.msgs = append(group.msgs, msg.Message)
	}
	for _, group := range groups {
		groupResults, err := group.client.SendBatch(ctx, group.msgs)
		if err != nil {
			return results, err
		}
		if len(groupResults) != len(group.indexes) {
			return results, fmt.Errorf("client pool: result count mismatch")
		}
		for i, result := range groupResults {
			results[group.indexes[i]] = result
		}
	}
	return results, nil
}

func (p *Pool) Close() error {
	if p == nil {
		return nil
	}
	var first error
	for _, client := range p.clients {
		if err := client.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
```

- [ ] **Step 4: Add Connect implementation for real Pool clients**

Add:

```go
func (p *Pool) Connect(ctx context.Context, identities []Identity) error {
	if p == nil {
		return ErrClosed
	}
	addrs := p.cfg.Addrs
	if len(addrs) == 0 {
		addrs = []string{p.cfg.Client.Addr}
	}
	for i, identity := range identities {
		cfg := p.cfg.Client
		cfg.Addr = addrs[i%len(addrs)]
		cfg.Token = identity.Token
		c, err := New(cfg)
		if err != nil {
			return err
		}
		if _, err := c.Connect(ctx, ConnectOptions{UID: identity.UID, DeviceID: identity.DeviceID, Token: identity.Token}); err != nil {
			_ = c.Close()
			return err
		}
		p.clients[identity.UID] = c
	}
	return nil
}

func (p *Pool) Client(uid string) (*Client, bool) {
	c, ok := p.clients[uid].(*Client)
	return c, ok
}
```

- [ ] **Step 5: Run pool tests**

Run:

```bash
go test ./pkg/client -run Pool
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/client/pool.go pkg/client/pool_test.go
git commit -m "feat: add wkproto client pool"
```

## Task 9: Bench Client Migration

**Files:**
- Modify: `internal/bench/wkproto/client.go`
- Modify: `internal/bench/wkproto/client_test.go`
- Modify: `internal/bench/FLOW.md` if behavior wording changes

- [ ] **Step 1: Run existing bench client tests before changing code**

Run:

```bash
go test ./internal/bench/wkproto
```

Expected: PASS before migration.

- [ ] **Step 2: Replace bench client internals with pkg/client wrapper**

Modify `internal/bench/wkproto/client.go` so `Client` holds a `*client.Client`:

```go
import wkclient "github.com/WuKongIM/WuKongIM/pkg/client"

type Client struct {
	inner *wkclient.Client
	token string
}
```

Implement the existing API by delegating:

```go
func NewClient(cfg ClientConfig) (*Client, error) {
	inner, err := wkclient.New(wkclient.Config{
		Addr:                   cfg.Addr,
		Token:                  cfg.Token,
		Dialer:                 cfg.Dialer,
		OperationTimeout:       cfg.OperationTimeout,
		InboundFrameBufferSize: cfg.FrameBufferSize,
		AutoRecvAck:            false,
	})
	if err != nil {
		return nil, err
	}
	return &Client{inner: inner, token: cfg.Token}, nil
}

func (c *Client) Connect(ctx context.Context, uid, deviceID string) error {
	_, err := c.inner.Connect(ctx, wkclient.ConnectOptions{UID: uid, DeviceID: deviceID, Token: c.token})
	return err
}

func (c *Client) Send(ctx context.Context, pkt *frame.SendPacket) error {
	_, err := c.inner.Send(ctx, wkclient.Message{
		Setting: pkt.Setting, Expire: pkt.Expire, ClientSeq: pkt.ClientSeq,
		ClientMsgNo: pkt.ClientMsgNo, ChannelID: pkt.ChannelID,
		ChannelType: pkt.ChannelType, Topic: pkt.Topic, Payload: pkt.Payload,
	})
	return err
}

func (c *Client) ReadFrame(ctx context.Context) (frame.Frame, error) {
	return c.inner.ReadFrame(ctx)
}

func (c *Client) RecvAck(ctx context.Context, messageID int64, messageSeq uint64) error {
	return c.inner.RecvAck(ctx, messageID, messageSeq)
}

func (c *Client) Ping(ctx context.Context) error { return c.inner.Ping(ctx) }
func (c *Client) Close() error { return c.inner.Close() }
```

- [ ] **Step 3: Run bench client tests**

Run:

```bash
go test ./internal/bench/wkproto
```

Expected: PASS.

- [ ] **Step 4: Run workload tests that use matching clients**

Run:

```bash
go test ./internal/bench/workload
```

Expected: PASS. If tests fail because SEND now waits for SENDACK, update the wrapper to preserve old `Send` semantics by using `SendAsync` and returning after enqueue for bench compatibility, then keep higher-level workload waiting behavior unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/bench/wkproto internal/bench/workload internal/bench/FLOW.md
git commit -m "refactor: use pkg client in wkbench wkproto"
```

## Task 10: E2E Client Migration

**Files:**
- Modify: `test/e2e/suite/wkproto_client.go`
- Modify: `test/e2e/suite/wkproto_client_test.go`

- [ ] **Step 1: Run e2e helper package compile test**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run WKProtoClient -count=1
```

Expected: current helper tests pass before migration.

- [ ] **Step 2: Replace e2e helper internals with pkg/client**

Modify `test/e2e/suite/wkproto_client.go`:

```go
type WKProtoClient struct {
	inner *client.Client
}

func NewWKProtoClient() (*WKProtoClient, error) {
	return &WKProtoClient{}, nil
}

func (c *WKProtoClient) ConnectContext(ctx context.Context, addr, uid, deviceID string) (*frame.ConnackPacket, error) {
	inner, err := client.New(client.Config{Addr: addr, OperationTimeout: defaultWKProtoTimeout})
	if err != nil {
		return nil, err
	}
	ack, err := inner.Connect(ctx, client.ConnectOptions{UID: uid, DeviceID: deviceID})
	if err != nil {
		_ = inner.Close()
		return nil, err
	}
	c.inner = inner
	return ack, nil
}
```

Delegate `SendFrame`, `ReadSendAck`, `ReadRecv`, `RecvAck`, and `Close` to `pkg/client` methods. For `SendFrame`, support only the frame types used by e2e tests: CONNECT is handled by `ConnectContext`, SEND maps to `client.Message`, PING maps to `Ping`, RECVACK maps to `RecvAck`.

- [ ] **Step 3: Run e2e helper tests**

Run:

```bash
go test -tags=e2e ./test/e2e/suite -run WKProtoClient -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add test/e2e/suite/wkproto_client.go test/e2e/suite/wkproto_client_test.go
git commit -m "refactor: use pkg client in e2e wkproto helper"
```

## Task 11: Final Verification and Documentation

**Files:**
- Modify: `pkg/client/FLOW.md`
- Modify: `docs/superpowers/specs/2026-06-13-pkg-client-design.md` only if implementation intentionally diverges.

- [ ] **Step 1: Expand `pkg/client/FLOW.md`**

Update the file with sections for:

```markdown
## Connection Lifecycle
New -> Connect -> reader loop + writer loop -> Close.

## SEND Flow
SendBatch -> pending tracker -> writer queue -> contiguous frame write -> reader SENDACK -> future resolution.

## RECV Flow
reader RECV -> decrypt -> optional RecvAck -> bounded inbound queue.

## Pool Flow
Pool.Connect creates one Client per identity and assigns gateway addresses round-robin.
```

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./pkg/client ./internal/bench/wkproto ./internal/bench/workload
```

Expected: PASS.

- [ ] **Step 3: Run targeted broader tests**

Run:

```bash
go test ./pkg/gateway/... ./internal/bench/... ./test/e2e/suite -run WKProtoClient -tags=e2e
```

Expected: PASS. If `-tags=e2e` changes package selection unexpectedly, run:

```bash
go test ./pkg/gateway/... ./internal/bench/...
go test -tags=e2e ./test/e2e/suite -run WKProtoClient
```

- [ ] **Step 4: Check status**

Run:

```bash
git status --short
```

Expected: only intentional `pkg/client`, bench wrapper, e2e wrapper, and FLOW/doc files are modified.

- [ ] **Step 5: Commit final docs**

```bash
git add pkg/client/FLOW.md docs/superpowers/specs/2026-06-13-pkg-client-design.md
git commit -m "docs: describe pkg client flow"
```

## Self-Review

- Spec coverage: the plan covers the single-client lifecycle, session crypto, batched SEND, SENDACK matching, RECV/AutoRecvAck, Pool, observer/error DTOs, tests, and migration from existing bench/e2e clients.
- Scope: the plan does not add HTTP bench API support, business SDK methods, reconnect replay, or a new WKProto batch frame.
- Type consistency: public names match the design spec: `Client`, `Config`, `ConnectOptions`, `Message`, `SendResult`, `SendFuture`, `Pool`, `Identity`, and `RoutedMessage`.
- Testing: every implementation task has a focused `go test` command and expected outcome.
