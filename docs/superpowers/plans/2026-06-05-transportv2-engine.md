# transportv2 Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a clean `pkg/transportv2` node-to-node transport engine with fixed wire frames, explicit payload ownership, bounded concurrency, byte-aware backpressure, and pressure-test coverage.

**Architecture:** Implement a new package beside `pkg/transport` with no compatibility layer. Root package files expose the public client/server/config API, while `internal/core` holds shared primitive types used by internal packages to avoid import cycles. TCP connections use one reader goroutine, one writer goroutine, weighted scheduling, per-connection pending RPC state, and bounded service workers on the server.

**Tech Stack:** Go 1.23, standard library `net`, `context`, `sync`, `sync/atomic`, `encoding/binary`, `net.Buffers`, existing `pkg/wklog`, standard `testing` benchmarks and opt-in stress tests.

---

## File Structure

Create these files:

- `pkg/transportv2/types.go`  
  Public aliases and root API types that callers import.
- `pkg/transportv2/config.go`  
  Public configs, default limits, and validation.
- `pkg/transportv2/errors.go`  
  Public sentinel errors and `RemoteError`.
- `pkg/transportv2/client.go`  
  Public `Client` wrapper over the internal peer manager.
- `pkg/transportv2/server.go`  
  Public `Server` wrapper over accepted conns and RPC services.
- `pkg/transportv2/internal/core/types.go`  
  Shared primitive types, limits, frame metadata, payload ownership, and errors.
- `pkg/transportv2/internal/buffer/slab.go`  
  Slab allocator for inbound and copied outbound payloads.
- `pkg/transportv2/wire/frame.go`  
  Fixed 24-byte frame header codec.
- `pkg/transportv2/wire/reader.go`  
  Frame reader with validation before allocation.
- `pkg/transportv2/wire/writer.go`  
  Frame writer helpers for `net.Buffers` batches.
- `pkg/transportv2/internal/sched/scheduler.go`  
  Byte-aware weighted deficit round-robin scheduler.
- `pkg/transportv2/internal/rpc/pending.go`  
  Sharded pending RPC table.
- `pkg/transportv2/internal/rpc/service.go`  
  Service queue and bounded worker registry.
- `pkg/transportv2/internal/conn/conn.go`  
  Connection actor, reader/writer lifecycle, dispatch, pending failure.
- `pkg/transportv2/internal/peer/manager.go`  
  NodeID peer map, connection slot selection, dial single-flight, ClosePeer.
- `pkg/transportv2/testkit/fault_conn.go`  
  Test-only fake net.Conn implementations.
- `pkg/transportv2/testkit/harness.go`  
  Local server/client test harness helpers.
- `pkg/transportv2/stress_test.go`  
  Opt-in mixed workload stress test.
- `pkg/transportv2/benchmark_test.go`  
  Send/RPC benchmark harness.

Create matching tests beside the implementation files:

- `pkg/transportv2/config_test.go`
- `pkg/transportv2/internal/core/types_test.go`
- `pkg/transportv2/internal/buffer/slab_test.go`
- `pkg/transportv2/wire/frame_test.go`
- `pkg/transportv2/wire/reader_test.go`
- `pkg/transportv2/wire/writer_test.go`
- `pkg/transportv2/internal/sched/scheduler_test.go`
- `pkg/transportv2/internal/rpc/pending_test.go`
- `pkg/transportv2/internal/rpc/service_test.go`
- `pkg/transportv2/internal/conn/conn_test.go`
- `pkg/transportv2/internal/peer/manager_test.go`
- `pkg/transportv2/client_server_test.go`

Keep `pkg/transport` unchanged in this plan.

---

### Task 1: Public Contracts, Core Types, and Config Validation

**Files:**
- Create: `pkg/transportv2/internal/core/types.go`
- Create: `pkg/transportv2/types.go`
- Create: `pkg/transportv2/errors.go`
- Create: `pkg/transportv2/config.go`
- Test: `pkg/transportv2/config_test.go`
- Test: `pkg/transportv2/internal/core/types_test.go`

- [ ] **Step 1: Write failing config and owned-buffer tests**

Add `pkg/transportv2/config_test.go`:

```go
package transportv2

import (
	"errors"
	"testing"
	"time"
)

type testDiscovery map[NodeID]string

func (d testDiscovery) Resolve(nodeID NodeID) (string, error) {
	addr, ok := d[nodeID]
	if !ok {
		return "", ErrNodeNotFound
	}
	return addr, nil
}

func TestClientConfigValidateRequiresDiscovery(t *testing.T) {
	_, err := NewClient(ClientConfig{NodeID: 1})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewClient() error = %v, want %v", err, ErrInvalidConfig)
	}
}

func TestClientConfigDefaults(t *testing.T) {
	c, err := NewClient(ClientConfig{
		NodeID:    1,
		Discovery: testDiscovery{2: "127.0.0.1:1"},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	defer c.Stop()

	if c.cfg.PoolSize != DefaultPoolSize {
		t.Fatalf("PoolSize = %d, want %d", c.cfg.PoolSize, DefaultPoolSize)
	}
	if c.cfg.DialTimeout != DefaultDialTimeout {
		t.Fatalf("DialTimeout = %s, want %s", c.cfg.DialTimeout, DefaultDialTimeout)
	}
	if c.cfg.Limits.MaxFrameBodyBytes != DefaultLimits().MaxFrameBodyBytes {
		t.Fatalf("MaxFrameBodyBytes = %d, want default", c.cfg.Limits.MaxFrameBodyBytes)
	}
}

func TestLimitsValidationRejectsImpossibleBatch(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxBatchBytes = 0
	err := limits.Validate()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrInvalidConfig)
	}
}

func TestServiceOptionsValidation(t *testing.T) {
	err := (ServiceOptions{Concurrency: 0, QueueSize: 1, MaxQueueBytes: 1024, Timeout: time.Second}).Validate()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrInvalidConfig)
	}
}
```

Add `pkg/transportv2/internal/core/types_test.go`:

```go
package core

import "testing"

func TestPriorityValidation(t *testing.T) {
	for _, pri := range []Priority{PriorityRaft, PriorityControl, PriorityRPC, PriorityBulk} {
		if !pri.Valid() {
			t.Fatalf("priority %d valid = false, want true", pri)
		}
	}
	if Priority(0).Valid() {
		t.Fatal("priority 0 valid = true, want false")
	}
	if Priority(99).Valid() {
		t.Fatal("priority 99 valid = true, want false")
	}
}

func TestOwnedBufferReleaseOnce(t *testing.T) {
	releases := 0
	buf := NewOwnedBuffer([]byte("abc"), func([]byte) { releases++ })
	if string(buf.Bytes()) != "abc" {
		t.Fatalf("Bytes() = %q, want abc", buf.Bytes())
	}
	buf.Release()
	buf.Release()
	if releases != 1 {
		t.Fatalf("releases = %d, want 1", releases)
	}
}
```

- [ ] **Step 2: Run focused tests and verify they fail**

Run:

```sh
go test ./pkg/transportv2 ./pkg/transportv2/internal/core -run 'Test(ClientConfig|Limits|ServiceOptions|Priority|OwnedBuffer)' -count=1
```

Expected: package or symbol-not-found failures because `pkg/transportv2` does not exist yet.

- [ ] **Step 3: Implement core types and public aliases**

Create `pkg/transportv2/internal/core/types.go` with these exported primitives:

```go
package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type NodeID uint64

type Priority uint8

const (
	PriorityRaft Priority = iota + 1
	PriorityControl
	PriorityRPC
	PriorityBulk
)

func (p Priority) Valid() bool {
	return p >= PriorityRaft && p <= PriorityBulk
}

type FrameKind uint8

const (
	FrameKindData FrameKind = iota + 1
	FrameKindNotify
	FrameKindRPCRequest
	FrameKindRPCResponse
	FrameKindControl
)

func (k FrameKind) Valid() bool {
	return k >= FrameKindData && k <= FrameKindControl
}

type Discovery interface {
	Resolve(nodeID NodeID) (addr string, err error)
}

type Handler func(ctx context.Context, payload []byte) ([]byte, error)

type Observer interface {
	ObserveTransport(event Event)
}

type Event struct {
	Name      string
	NodeID    NodeID
	Priority  Priority
	ServiceID uint16
	Result    string
	Bytes     int
	Duration  time.Duration
}

type Stats struct {
	Peers       int
	Connections int
	QueuedItems int64
	QueuedBytes int64
	PendingRPC  int64
}

type Limits struct {
	MaxFrameBodyBytes   int
	MaxQueuedBytesPerConn int64
	MaxQueuedItemsPerConn int
	MaxBatchBytes       int
	MaxBatchFrames      int
	DialFailureCooldown time.Duration
	WriteTimeout        time.Duration
	ReadIdleTimeout     time.Duration
}

type ServiceOptions struct {
	Concurrency   int
	QueueSize     int
	MaxQueueBytes int64
	Timeout       time.Duration
	MaxPayload    int
}

type RemoteError struct {
	Code    string
	Message string
}

func (e RemoteError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

var (
	ErrStopped        = errors.New("transportv2: stopped")
	ErrTimeout        = errors.New("transportv2: timeout")
	ErrCanceled       = errors.New("transportv2: canceled")
	ErrNodeNotFound   = errors.New("transportv2: node not found")
	ErrQueueFull      = errors.New("transportv2: queue full")
	ErrMsgTooLarge    = errors.New("transportv2: message too large")
	ErrInvalidFrame   = errors.New("transportv2: invalid frame")
	ErrInvalidPriority = errors.New("transportv2: invalid priority")
	ErrDialFailed     = errors.New("transportv2: dial failed")
	ErrBusy           = errors.New("transportv2: busy")
	ErrInvalidConfig  = errors.New("transportv2: invalid config")
)

type ownedState struct {
	data    []byte
	release func([]byte)
	once    sync.Once
}

type OwnedBuffer struct {
	state *ownedState
}

func NewOwnedBuffer(data []byte, release func([]byte)) OwnedBuffer {
	return OwnedBuffer{state: &ownedState{data: data, release: release}}
}

func CopyOwnedBuffer(data []byte) OwnedBuffer {
	copied := append([]byte(nil), data...)
	return NewOwnedBuffer(copied, nil)
}

func (b OwnedBuffer) Bytes() []byte {
	if b.state == nil {
		return nil
	}
	return b.state.data
}

func (b OwnedBuffer) Len() int {
	return len(b.Bytes())
}

func (b OwnedBuffer) Release() {
	if b.state == nil {
		return
	}
	b.state.once.Do(func() {
		if b.state.release != nil {
			b.state.release(b.state.data)
		}
	})
}
```

Create root alias files:

```go
// pkg/transportv2/types.go
package transportv2

import "github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"

type NodeID = core.NodeID
type Priority = core.Priority
type FrameKind = core.FrameKind
type Discovery = core.Discovery
type Handler = core.Handler
type Observer = core.Observer
type Event = core.Event
type Stats = core.Stats
type Limits = core.Limits
type ServiceOptions = core.ServiceOptions
type OwnedBuffer = core.OwnedBuffer

const (
	PriorityRaft    = core.PriorityRaft
	PriorityControl = core.PriorityControl
	PriorityRPC     = core.PriorityRPC
	PriorityBulk    = core.PriorityBulk
)

func NewOwnedBuffer(data []byte, release func([]byte)) OwnedBuffer {
	return core.NewOwnedBuffer(data, release)
}
```

```go
// pkg/transportv2/errors.go
package transportv2

import "github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"

type RemoteError = core.RemoteError

var (
	ErrStopped         = core.ErrStopped
	ErrTimeout         = core.ErrTimeout
	ErrCanceled        = core.ErrCanceled
	ErrNodeNotFound    = core.ErrNodeNotFound
	ErrQueueFull       = core.ErrQueueFull
	ErrMsgTooLarge     = core.ErrMsgTooLarge
	ErrInvalidFrame    = core.ErrInvalidFrame
	ErrInvalidPriority = core.ErrInvalidPriority
	ErrDialFailed      = core.ErrDialFailed
	ErrBusy            = core.ErrBusy
	ErrInvalidConfig   = core.ErrInvalidConfig
)
```

- [ ] **Step 4: Implement config validation and minimal Client/Server constructors**

Add `pkg/transportv2/config.go`:

```go
package transportv2

import (
	"fmt"
	"net"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const (
	DefaultPoolSize    = 4
	DefaultDialTimeout = 5 * time.Second
)

type Dialer func(network, addr string, timeout time.Duration) (net.Conn, error)

type ClientConfig struct {
	NodeID      NodeID
	Discovery  Discovery
	PoolSize   int
	DialTimeout time.Duration
	Dialer      Dialer
	Limits      Limits
	Observer    Observer
}

type ServerConfig struct {
	NodeID   NodeID
	Limits   Limits
	Observer Observer
	Logger   wklog.Logger
}

func DefaultLimits() Limits {
	return Limits{
		MaxFrameBodyBytes:      64 << 20,
		MaxQueuedBytesPerConn:  64 << 20,
		MaxQueuedItemsPerConn:  4096,
		MaxBatchBytes:          1 << 20,
		MaxBatchFrames:         64,
		DialFailureCooldown:    50 * time.Millisecond,
		WriteTimeout:           5 * time.Second,
		ReadIdleTimeout:        0,
	}
}

func (l Limits) Validate() error {
	if l.MaxFrameBodyBytes <= 0 {
		return fmt.Errorf("%w: MaxFrameBodyBytes must be positive", ErrInvalidConfig)
	}
	if l.MaxQueuedBytesPerConn <= 0 {
		return fmt.Errorf("%w: MaxQueuedBytesPerConn must be positive", ErrInvalidConfig)
	}
	if l.MaxQueuedItemsPerConn <= 0 {
		return fmt.Errorf("%w: MaxQueuedItemsPerConn must be positive", ErrInvalidConfig)
	}
	if l.MaxBatchBytes <= 0 || l.MaxBatchBytes > l.MaxFrameBodyBytes {
		return fmt.Errorf("%w: MaxBatchBytes must be in 1..MaxFrameBodyBytes", ErrInvalidConfig)
	}
	if l.MaxBatchFrames <= 0 {
		return fmt.Errorf("%w: MaxBatchFrames must be positive", ErrInvalidConfig)
	}
	if l.DialFailureCooldown < 0 || l.WriteTimeout < 0 || l.ReadIdleTimeout < 0 {
		return fmt.Errorf("%w: timeouts must be non-negative", ErrInvalidConfig)
	}
	return nil
}

func (o ServiceOptions) Validate() error {
	if o.Concurrency <= 0 {
		return fmt.Errorf("%w: service concurrency must be positive", ErrInvalidConfig)
	}
	if o.QueueSize <= 0 {
		return fmt.Errorf("%w: service queue size must be positive", ErrInvalidConfig)
	}
	if o.MaxQueueBytes <= 0 {
		return fmt.Errorf("%w: service max queue bytes must be positive", ErrInvalidConfig)
	}
	if o.Timeout < 0 {
		return fmt.Errorf("%w: service timeout must be non-negative", ErrInvalidConfig)
	}
	if o.MaxPayload < 0 {
		return fmt.Errorf("%w: service max payload must be non-negative", ErrInvalidConfig)
	}
	return nil
}

func normalizeClientConfig(cfg ClientConfig) (ClientConfig, error) {
	if cfg.Discovery == nil {
		return ClientConfig{}, fmt.Errorf("%w: discovery is required", ErrInvalidConfig)
	}
	if cfg.PoolSize <= 0 {
		cfg.PoolSize = DefaultPoolSize
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = DefaultDialTimeout
	}
	if cfg.Limits == (core.Limits{}) {
		cfg.Limits = DefaultLimits()
	}
	if err := cfg.Limits.Validate(); err != nil {
		return ClientConfig{}, err
	}
	return cfg, nil
}

func normalizeServerConfig(cfg ServerConfig) (ServerConfig, error) {
	if cfg.Limits == (core.Limits{}) {
		cfg.Limits = DefaultLimits()
	}
	if err := cfg.Limits.Validate(); err != nil {
		return ServerConfig{}, err
	}
	if cfg.Logger == nil {
		cfg.Logger = wklog.NewNop()
	}
	return cfg, nil
}
```

Add minimal constructor shells in `client.go` and `server.go`:

```go
package transportv2

type Client struct {
	cfg ClientConfig
}

func NewClient(cfg ClientConfig) (*Client, error) {
	normalized, err := normalizeClientConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{cfg: normalized}, nil
}

func (c *Client) Stop() {}

func (c *Client) Stats() Stats { return Stats{} }
```

```go
package transportv2

type Server struct {
	cfg ServerConfig
}

func NewServer(cfg ServerConfig) (*Server, error) {
	normalized, err := normalizeServerConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: normalized}, nil
}

func (s *Server) Stop() {}

func (s *Server) Stats() Stats { return Stats{} }
```

- [ ] **Step 5: Run focused tests and commit**

Run:

```sh
go test ./pkg/transportv2 ./pkg/transportv2/internal/core -run 'Test(ClientConfig|Limits|ServiceOptions|Priority|OwnedBuffer)' -count=1
```

Expected: PASS.

Commit:

```sh
git add pkg/transportv2
git commit -m "feat: add transportv2 public contracts"
```

---

### Task 2: Fixed Wire Frame Codec

**Files:**
- Create: `pkg/transportv2/wire/frame.go`
- Create: `pkg/transportv2/wire/reader.go`
- Create: `pkg/transportv2/wire/writer.go`
- Test: `pkg/transportv2/wire/frame_test.go`
- Test: `pkg/transportv2/wire/reader_test.go`
- Test: `pkg/transportv2/wire/writer_test.go`

- [ ] **Step 1: Write failing wire tests**

Add `pkg/transportv2/wire/frame_test.go`:

```go
package wire

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

func TestHeaderRoundTrip(t *testing.T) {
	frame := Frame{
		Kind:      core.FrameKindRPCRequest,
		Priority:  core.PriorityRPC,
		ServiceID: 7,
		RequestID: 99,
		BodyLen:   1234,
	}
	hdr := EncodeHeader(frame)
	got, err := DecodeHeader(hdr[:], 64<<20)
	if err != nil {
		t.Fatalf("DecodeHeader() error = %v", err)
	}
	if got != frame {
		t.Fatalf("frame = %+v, want %+v", got, frame)
	}
}

func TestDecodeHeaderRejectsInvalidMagic(t *testing.T) {
	frame := Frame{Kind: core.FrameKindData, Priority: core.PriorityRaft}
	hdr := EncodeHeader(frame)
	hdr[0] = 0
	_, err := DecodeHeader(hdr[:], 64<<20)
	if !errors.Is(err, core.ErrInvalidFrame) {
		t.Fatalf("DecodeHeader() error = %v, want %v", err, core.ErrInvalidFrame)
	}
}

func TestDecodeHeaderRejectsOversizedBody(t *testing.T) {
	hdr := EncodeHeader(Frame{Kind: core.FrameKindData, Priority: core.PriorityRaft, BodyLen: 8})
	_, err := DecodeHeader(hdr[:], 7)
	if !errors.Is(err, core.ErrMsgTooLarge) {
		t.Fatalf("DecodeHeader() error = %v, want %v", err, core.ErrMsgTooLarge)
	}
}
```

Add `reader_test.go`:

```go
package wire

import (
	"bytes"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

func TestReaderReadsFrameBody(t *testing.T) {
	var out bytes.Buffer
	body := []byte("hello")
	_, err := WriteFrame(&out, Frame{Kind: core.FrameKindData, Priority: core.PriorityRaft, ServiceID: 3}, core.NewOwnedBuffer(body, nil), 64<<20)
	if err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	frame, owned, err := ReadFrame(bytes.NewReader(out.Bytes()), 64<<20)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	defer owned.Release()
	if frame.ServiceID != 3 || string(owned.Bytes()) != "hello" {
		t.Fatalf("frame=%+v body=%q", frame, owned.Bytes())
	}
}
```

Add `writer_test.go`:

```go
package wire

import (
	"bytes"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

func TestWriteFrameRejectsOversizedBody(t *testing.T) {
	_, err := WriteFrame(bytes.NewBuffer(nil), Frame{Kind: core.FrameKindData, Priority: core.PriorityBulk}, core.NewOwnedBuffer(make([]byte, 9), nil), 8)
	if !errors.Is(err, core.ErrMsgTooLarge) {
		t.Fatalf("WriteFrame() error = %v, want %v", err, core.ErrMsgTooLarge)
	}
}
```

- [ ] **Step 2: Run wire tests and verify failure**

Run:

```sh
go test ./pkg/transportv2/wire -count=1
```

Expected: FAIL with missing `Frame`, `EncodeHeader`, `DecodeHeader`, `ReadFrame`, or `WriteFrame`.

- [ ] **Step 3: Implement fixed header codec**

Create `pkg/transportv2/wire/frame.go`:

```go
package wire

import (
	"encoding/binary"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

const (
	Magic      uint16 = 0x574b
	Version    uint8  = 1
	HeaderSize       = 24
)

type Frame struct {
	Kind      core.FrameKind
	Priority  core.Priority
	ServiceID uint16
	RequestID uint64
	BodyLen   uint32
}

func EncodeHeader(frame Frame) [HeaderSize]byte {
	var hdr [HeaderSize]byte
	binary.BigEndian.PutUint16(hdr[0:2], Magic)
	hdr[2] = Version
	hdr[3] = 0
	hdr[4] = byte(frame.Kind)
	hdr[5] = byte(frame.Priority)
	binary.BigEndian.PutUint16(hdr[6:8], frame.ServiceID)
	binary.BigEndian.PutUint64(hdr[8:16], frame.RequestID)
	binary.BigEndian.PutUint32(hdr[16:20], frame.BodyLen)
	binary.BigEndian.PutUint32(hdr[20:24], 0)
	return hdr
}

func DecodeHeader(hdr []byte, maxBodyBytes int) (Frame, error) {
	if len(hdr) < HeaderSize {
		return Frame{}, fmt.Errorf("%w: short header %d", core.ErrInvalidFrame, len(hdr))
	}
	if binary.BigEndian.Uint16(hdr[0:2]) != Magic {
		return Frame{}, fmt.Errorf("%w: magic", core.ErrInvalidFrame)
	}
	if hdr[2] != Version {
		return Frame{}, fmt.Errorf("%w: version %d", core.ErrInvalidFrame, hdr[2])
	}
	if hdr[3] != 0 {
		return Frame{}, fmt.Errorf("%w: flags %d", core.ErrInvalidFrame, hdr[3])
	}
	if binary.BigEndian.Uint32(hdr[20:24]) != 0 {
		return Frame{}, fmt.Errorf("%w: reserved", core.ErrInvalidFrame)
	}
	frame := Frame{
		Kind:      core.FrameKind(hdr[4]),
		Priority:  core.Priority(hdr[5]),
		ServiceID: binary.BigEndian.Uint16(hdr[6:8]),
		RequestID: binary.BigEndian.Uint64(hdr[8:16]),
		BodyLen:   binary.BigEndian.Uint32(hdr[16:20]),
	}
	if !frame.Kind.Valid() {
		return Frame{}, fmt.Errorf("%w: kind %d", core.ErrInvalidFrame, frame.Kind)
	}
	if !frame.Priority.Valid() {
		return Frame{}, fmt.Errorf("%w: priority %d", core.ErrInvalidPriority, frame.Priority)
	}
	if maxBodyBytes > 0 && int64(frame.BodyLen) > int64(maxBodyBytes) {
		return Frame{}, fmt.Errorf("%w: %d bytes", core.ErrMsgTooLarge, frame.BodyLen)
	}
	return frame, nil
}
```

- [ ] **Step 4: Implement reader and writer helpers**

Create `pkg/transportv2/wire/reader.go`:

```go
package wire

import (
	"io"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

func ReadFrame(r io.Reader, maxBodyBytes int) (Frame, core.OwnedBuffer, error) {
	var hdr [HeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, core.OwnedBuffer{}, err
	}
	frame, err := DecodeHeader(hdr[:], maxBodyBytes)
	if err != nil {
		return Frame{}, core.OwnedBuffer{}, err
	}
	if frame.BodyLen == 0 {
		return frame, core.NewOwnedBuffer(nil, nil), nil
	}
	body := make([]byte, int(frame.BodyLen))
	if _, err := io.ReadFull(r, body); err != nil {
		return Frame{}, core.OwnedBuffer{}, err
	}
	return frame, core.NewOwnedBuffer(body, nil), nil
}
```

Create `pkg/transportv2/wire/writer.go`:

```go
package wire

import (
	"fmt"
	"io"
	"net"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

func AppendFrame(bufs *net.Buffers, headers *[][HeaderSize]byte, frame Frame, body core.OwnedBuffer, maxBodyBytes int) error {
	if maxBodyBytes > 0 && body.Len() > maxBodyBytes {
		return fmt.Errorf("%w: %d bytes", core.ErrMsgTooLarge, body.Len())
	}
	frame.BodyLen = uint32(body.Len())
	*headers = append(*headers, EncodeHeader(frame))
	hdr := &(*headers)[len(*headers)-1]
	*bufs = append(*bufs, hdr[:])
	if body.Len() > 0 {
		*bufs = append(*bufs, body.Bytes())
	}
	return nil
}

func WriteFrame(w io.Writer, frame Frame, body core.OwnedBuffer, maxBodyBytes int) (int64, error) {
	var bufs net.Buffers
	headers := make([][HeaderSize]byte, 0, 1)
	if err := AppendFrame(&bufs, &headers, frame, body, maxBodyBytes); err != nil {
		return 0, err
	}
	return bufs.WriteTo(w)
}
```

- [ ] **Step 5: Run wire tests and commit**

Run:

```sh
go test ./pkg/transportv2/wire -count=1
```

Expected: PASS.

Commit:

```sh
git add pkg/transportv2/wire
git commit -m "feat: add transportv2 wire codec"
```

---

### Task 3: Slab Buffer Pool and Payload Ownership Helpers

**Files:**
- Create: `pkg/transportv2/internal/buffer/slab.go`
- Test: `pkg/transportv2/internal/buffer/slab_test.go`
- Modify: `pkg/transportv2/wire/reader.go`
- Test: `pkg/transportv2/wire/reader_test.go`

- [ ] **Step 1: Write failing slab tests**

Add `pkg/transportv2/internal/buffer/slab_test.go`:

```go
package buffer

import "testing"

func TestSlabGetUsesSmallestFittingClass(t *testing.T) {
	pool := NewSlabPool([]int{8, 16})
	buf := pool.Get(9)
	if len(buf.Bytes()) != 9 {
		t.Fatalf("len = %d, want 9", len(buf.Bytes()))
	}
	if cap(buf.Bytes()) != 16 {
		t.Fatalf("cap = %d, want 16", cap(buf.Bytes()))
	}
	buf.Release()
}

func TestSlabGetLargeAllocatesExact(t *testing.T) {
	pool := NewSlabPool([]int{8})
	buf := pool.Get(64)
	if len(buf.Bytes()) != 64 {
		t.Fatalf("len = %d, want 64", len(buf.Bytes()))
	}
	if cap(buf.Bytes()) != 64 {
		t.Fatalf("cap = %d, want 64", cap(buf.Bytes()))
	}
	buf.Release()
}
```

- [ ] **Step 2: Run buffer tests and verify failure**

Run:

```sh
go test ./pkg/transportv2/internal/buffer -count=1
```

Expected: FAIL with missing `NewSlabPool`.

- [ ] **Step 3: Implement slab pool**

Create `pkg/transportv2/internal/buffer/slab.go`:

```go
package buffer

import (
	"sort"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

type SlabPool struct {
	sizes []int
	pools []sync.Pool
}

func NewSlabPool(sizes []int) *SlabPool {
	sorted := append([]int(nil), sizes...)
	sort.Ints(sorted)
	filtered := sorted[:0]
	last := 0
	for _, size := range sorted {
		if size <= 0 || size == last {
			continue
		}
		filtered = append(filtered, size)
		last = size
	}
	p := &SlabPool{sizes: append([]int(nil), filtered...), pools: make([]sync.Pool, len(filtered))}
	for i, size := range p.sizes {
		size := size
		p.pools[i].New = func() any {
			buf := make([]byte, size)
			return &buf
		}
	}
	return p
}

func (p *SlabPool) Get(n int) core.OwnedBuffer {
	if n <= 0 {
		return core.NewOwnedBuffer(nil, nil)
	}
	for i, size := range p.sizes {
		if n <= size {
			ptr := p.pools[i].Get().(*[]byte)
			buf := (*ptr)[:size]
			return core.NewOwnedBuffer(buf[:n], func([]byte) {
				*ptr = buf[:size]
				p.pools[i].Put(ptr)
			})
		}
	}
	buf := make([]byte, n)
	return core.NewOwnedBuffer(buf, nil)
}

var DefaultSlabPool = NewSlabPool([]int{512, 4096, 65536, 1048576})
```

- [ ] **Step 4: Use slab pool in wire reader**

Modify `wire.ReadFrame` body allocation:

```go
owned := buffer.DefaultSlabPool.Get(int(frame.BodyLen))
if _, err := io.ReadFull(r, owned.Bytes()); err != nil {
	owned.Release()
	return Frame{}, core.OwnedBuffer{}, err
}
return frame, owned, nil
```

Add import:

```go
"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/buffer"
```

- [ ] **Step 5: Run buffer and wire tests, then commit**

Run:

```sh
go test ./pkg/transportv2/internal/buffer ./pkg/transportv2/wire -count=1
```

Expected: PASS.

Commit:

```sh
git add pkg/transportv2/internal/buffer pkg/transportv2/wire
git commit -m "feat: add transportv2 buffer ownership"
```

---

### Task 4: Byte-Aware Priority Scheduler

**Files:**
- Create: `pkg/transportv2/internal/sched/scheduler.go`
- Test: `pkg/transportv2/internal/sched/scheduler_test.go`

- [ ] **Step 1: Write failing scheduler tests**

Add `pkg/transportv2/internal/sched/scheduler_test.go`:

```go
package sched

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

func TestSchedulerRejectsInvalidPriority(t *testing.T) {
	s := New(Config{MaxItems: 8, MaxBytes: 1024, MaxBatchFrames: 4, MaxBatchBytes: 1024})
	err := s.Enqueue(context.Background(), Item{Priority: core.Priority(99), Bytes: 1})
	if !errors.Is(err, core.ErrInvalidPriority) {
		t.Fatalf("Enqueue() error = %v, want %v", err, core.ErrInvalidPriority)
	}
}

func TestSchedulerEnforcesByteLimit(t *testing.T) {
	s := New(Config{MaxItems: 8, MaxBytes: 10, MaxBatchFrames: 4, MaxBatchBytes: 1024})
	if err := s.Enqueue(context.Background(), Item{Priority: core.PriorityRaft, Bytes: 8}); err != nil {
		t.Fatalf("first enqueue error = %v", err)
	}
	err := s.Enqueue(context.Background(), Item{Priority: core.PriorityRPC, Bytes: 8})
	if !errors.Is(err, core.ErrQueueFull) {
		t.Fatalf("second enqueue error = %v, want %v", err, core.ErrQueueFull)
	}
}

func TestSchedulerWeightedBatchIncludesLowerPriority(t *testing.T) {
	s := New(Config{MaxItems: 16, MaxBytes: 4096, MaxBatchFrames: 8, MaxBatchBytes: 4096})
	for i := 0; i < 4; i++ {
		if err := s.Enqueue(context.Background(), Item{Priority: core.PriorityRaft, Bytes: 100, Value: i}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Enqueue(context.Background(), Item{Priority: core.PriorityBulk, Bytes: 100, Value: 99}); err != nil {
		t.Fatal(err)
	}
	batch := s.NextBatch()
	foundBulk := false
	for _, item := range batch {
		if item.Priority == core.PriorityBulk {
			foundBulk = true
		}
	}
	if !foundBulk {
		t.Fatalf("batch = %+v, want one bulk item eventually", batch)
	}
}
```

- [ ] **Step 2: Run scheduler tests and verify failure**

Run:

```sh
go test ./pkg/transportv2/internal/sched -count=1
```

Expected: FAIL with missing scheduler types.

- [ ] **Step 3: Implement scheduler**

Create `pkg/transportv2/internal/sched/scheduler.go`:

```go
package sched

import (
	"context"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

type Config struct {
	MaxItems       int
	MaxBytes       int64
	MaxBatchFrames int
	MaxBatchBytes  int
}

type Item struct {
	Priority  core.Priority
	Bytes     int
	Value     any
	OnRelease func(error)
}

type lane struct {
	items   []Item
	deficit int
	weight  int
}

type Scheduler struct {
	mu      sync.Mutex
	cond    *sync.Cond
	cfg     Config
	lanes   map[core.Priority]*lane
	order   []core.Priority
	queuedItems int
	queuedBytes int64
	stopped bool
}

func New(cfg Config) *Scheduler {
	s := &Scheduler{
		cfg: cfg,
		lanes: map[core.Priority]*lane{
			core.PriorityRaft:    {weight: 8},
			core.PriorityControl: {weight: 6},
			core.PriorityRPC:     {weight: 4},
			core.PriorityBulk:    {weight: 1},
		},
		order: []core.Priority{core.PriorityRaft, core.PriorityControl, core.PriorityRPC, core.PriorityBulk},
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *Scheduler) Enqueue(ctx context.Context, item Item) error {
	if !item.Priority.Valid() {
		return core.ErrInvalidPriority
	}
	if item.Bytes < 0 {
		item.Bytes = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return core.ErrStopped
	}
	if s.cfg.MaxItems > 0 && s.queuedItems >= s.cfg.MaxItems {
		return core.ErrQueueFull
	}
	if s.cfg.MaxBytes > 0 && s.queuedBytes+int64(item.Bytes) > s.cfg.MaxBytes {
		return core.ErrQueueFull
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	ln := s.lanes[item.Priority]
	ln.items = append(ln.items, item)
	s.queuedItems++
	s.queuedBytes += int64(item.Bytes)
	s.cond.Signal()
	return nil
}

func (s *Scheduler) NextBatch() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queuedItems == 0 {
		return nil
	}
	return s.nextBatchLocked()
}

func (s *Scheduler) WaitBatch() ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.queuedItems == 0 && !s.stopped {
		s.cond.Wait()
	}
	if s.stopped {
		return nil, core.ErrStopped
	}
	return s.nextBatchLocked(), nil
}

func (s *Scheduler) nextBatchLocked() []Item {
	maxFrames := s.cfg.MaxBatchFrames
	if maxFrames <= 0 {
		maxFrames = 64
	}
	maxBytes := s.cfg.MaxBatchBytes
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	batch := make([]Item, 0, maxFrames)
	usedBytes := 0
	for len(batch) < maxFrames && s.queuedItems > 0 {
		progress := false
		for _, pri := range s.order {
			ln := s.lanes[pri]
			ln.deficit += ln.weight * 1024
			for len(ln.items) > 0 && len(batch) < maxFrames {
				next := ln.items[0]
				if usedBytes > 0 && usedBytes+next.Bytes > maxBytes {
					break
				}
				if next.Bytes > ln.deficit && len(batch) > 0 {
					break
				}
				ln.items = ln.items[1:]
				ln.deficit -= next.Bytes
				s.queuedItems--
				s.queuedBytes -= int64(next.Bytes)
				usedBytes += next.Bytes
				batch = append(batch, next)
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	return batch
}

func (s *Scheduler) Stop(err error) []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	var remaining []Item
	for _, pri := range s.order {
		ln := s.lanes[pri]
		remaining = append(remaining, ln.items...)
		ln.items = nil
	}
	s.queuedItems = 0
	s.queuedBytes = 0
	s.cond.Broadcast()
	return remaining
}
```

- [ ] **Step 4: Run scheduler tests and commit**

Run:

```sh
go test ./pkg/transportv2/internal/sched -count=1
```

Expected: PASS.

Commit:

```sh
git add pkg/transportv2/internal/sched
git commit -m "feat: add transportv2 priority scheduler"
```

---

### Task 5: Pending RPC Table

**Files:**
- Create: `pkg/transportv2/internal/rpc/pending.go`
- Test: `pkg/transportv2/internal/rpc/pending_test.go`

- [ ] **Step 1: Write failing pending tests**

Add `pkg/transportv2/internal/rpc/pending_test.go`:

```go
package rpc

import (
	"errors"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

func TestPendingStoreComplete(t *testing.T) {
	p := NewPendingTable(16)
	ch := make(chan Response, 1)
	p.Store(7, ch)
	if !p.Complete(7, Response{Payload: []byte("ok")}) {
		t.Fatal("Complete() = false, want true")
	}
	resp := <-ch
	if string(resp.Payload) != "ok" {
		t.Fatalf("payload = %q, want ok", resp.Payload)
	}
	if p.Complete(7, Response{}) {
		t.Fatal("Complete() = true after delete, want false")
	}
}

func TestPendingFailAll(t *testing.T) {
	p := NewPendingTable(16)
	ch := make(chan Response, 1)
	p.Store(1, ch)
	p.FailAll(core.ErrStopped)
	resp := <-ch
	if !errors.Is(resp.Err, core.ErrStopped) {
		t.Fatalf("err = %v, want %v", resp.Err, core.ErrStopped)
	}
	if p.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", p.Len())
	}
}

func TestPendingConcurrentStoreDelete(t *testing.T) {
	p := NewPendingTable(16)
	var wg sync.WaitGroup
	for i := 0; i < 128; i++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			ch := make(chan Response, 1)
			p.Store(id, ch)
			p.Delete(id)
		}(uint64(i + 1))
	}
	wg.Wait()
	if p.Len() != 0 {
		t.Fatalf("Len() = %d, want 0", p.Len())
	}
}
```

- [ ] **Step 2: Run pending tests and verify failure**

Run:

```sh
go test ./pkg/transportv2/internal/rpc -run TestPending -count=1
```

Expected: FAIL with missing pending table.

- [ ] **Step 3: Implement pending table**

Create `pkg/transportv2/internal/rpc/pending.go`:

```go
package rpc

import "sync"

type Response struct {
	Payload []byte
	Err     error
}

type PendingTable struct {
	shards []pendingShard
	mask   uint64
}

type pendingShard struct {
	mu sync.Mutex
	m  map[uint64]chan Response
}

func NewPendingTable(numShards int) *PendingTable {
	if numShards <= 0 || numShards&(numShards-1) != 0 {
		numShards = 16
	}
	p := &PendingTable{shards: make([]pendingShard, numShards), mask: uint64(numShards - 1)}
	for i := range p.shards {
		p.shards[i].m = make(map[uint64]chan Response)
	}
	return p
}

func (p *PendingTable) shard(id uint64) *pendingShard {
	return &p.shards[id&p.mask]
}

func (p *PendingTable) Store(id uint64, ch chan Response) {
	sh := p.shard(id)
	sh.mu.Lock()
	sh.m[id] = ch
	sh.mu.Unlock()
}

func (p *PendingTable) Delete(id uint64) {
	sh := p.shard(id)
	sh.mu.Lock()
	delete(sh.m, id)
	sh.mu.Unlock()
}

func (p *PendingTable) Complete(id uint64, resp Response) bool {
	sh := p.shard(id)
	sh.mu.Lock()
	ch, ok := sh.m[id]
	if ok {
		delete(sh.m, id)
	}
	sh.mu.Unlock()
	if !ok {
		return false
	}
	ch <- resp
	return true
}

func (p *PendingTable) FailAll(err error) {
	for i := range p.shards {
		sh := &p.shards[i]
		sh.mu.Lock()
		snapshot := sh.m
		sh.m = make(map[uint64]chan Response)
		sh.mu.Unlock()
		for _, ch := range snapshot {
			select {
			case ch <- Response{Err: err}:
			default:
			}
		}
	}
}

func (p *PendingTable) Len() int {
	total := 0
	for i := range p.shards {
		sh := &p.shards[i]
		sh.mu.Lock()
		total += len(sh.m)
		sh.mu.Unlock()
	}
	return total
}
```

- [ ] **Step 4: Run pending tests and commit**

Run:

```sh
go test ./pkg/transportv2/internal/rpc -run TestPending -count=1
```

Expected: PASS.

Commit:

```sh
git add pkg/transportv2/internal/rpc
git commit -m "feat: add transportv2 pending rpc table"
```

---

### Task 6: Connection Actor

**Files:**
- Create: `pkg/transportv2/internal/conn/conn.go`
- Test: `pkg/transportv2/internal/conn/conn_test.go`
- Modify: `pkg/transportv2/wire/frame.go`

- [ ] **Step 1: Write failing connection tests**

Add `pkg/transportv2/internal/conn/conn_test.go`:

```go
package conn

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/wire"
)

func TestConnSendWritesFrame(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	c := New(client, Config{Limits: testLimits()}, nil)
	c.Start()
	defer c.Close(core.ErrStopped)

	if err := c.Send(context.Background(), Outbound{Kind: core.FrameKindData, Priority: core.PriorityRaft, ServiceID: 3, Payload: core.CopyOwnedBuffer([]byte("ping"))}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	frame, body, err := wire.ReadFrame(server, testLimits().MaxFrameBodyBytes)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	defer body.Release()
	if frame.ServiceID != 3 || string(body.Bytes()) != "ping" {
		t.Fatalf("frame=%+v body=%q", frame, body.Bytes())
	}
}

func TestConnDispatchesInboundFrame(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	received := make(chan string, 1)
	c := New(client, Config{Limits: testLimits()}, DispatchFunc(func(ctx context.Context, inbound Inbound) {
		received <- string(inbound.Payload.Bytes())
		inbound.Payload.Release()
	}))
	c.Start()
	defer c.Close(core.ErrStopped)

	_, err := wire.WriteFrame(server, wire.Frame{Kind: core.FrameKindNotify, Priority: core.PriorityRPC, ServiceID: 4}, core.NewOwnedBuffer([]byte("hello"), nil), testLimits().MaxFrameBodyBytes)
	if err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	select {
	case got := <-received:
		if got != "hello" {
			t.Fatalf("received = %q, want hello", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for inbound dispatch")
	}
}

func TestConnCloseFailsPendingRPC(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	c := New(client, Config{Limits: testLimits()}, nil)
	c.Start()
	errCh := make(chan error, 1)
	go func() {
		_, err := c.Call(context.Background(), Outbound{Priority: core.PriorityRPC, ServiceID: 9, Payload: core.CopyOwnedBuffer([]byte("req"))})
		errCh <- err
	}()
	if _, _, err := wire.ReadFrame(server, testLimits().MaxFrameBodyBytes); err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	c.Close(core.ErrStopped)
	select {
	case err := <-errCh:
		if !errors.Is(err, core.ErrStopped) {
			t.Fatalf("Call() error = %v, want %v", err, core.ErrStopped)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for pending RPC failure")
	}
}

func testLimits() core.Limits {
	return core.Limits{
		MaxFrameBodyBytes:      1 << 20,
		MaxQueuedBytesPerConn:  1 << 20,
		MaxQueuedItemsPerConn:  128,
		MaxBatchBytes:          1 << 20,
		MaxBatchFrames:         16,
		WriteTimeout:           time.Second,
	}
}
```

- [ ] **Step 2: Run connection tests and verify failure**

Run:

```sh
go test ./pkg/transportv2/internal/conn -count=1
```

Expected: FAIL with missing connection actor.

- [ ] **Step 3: Add response error codec constants to wire**

Extend `wire/frame.go` with response helpers:

```go
const (
	ResponseOK   uint8 = 0
	ResponseErr  uint8 = 1
)
```

RPC response bodies use:

```text
status uint8
payload []byte
```

This keeps the first version small. Structured `RemoteError` codes can be layered into payload JSON in the server task.

- [ ] **Step 4: Implement connection actor**

Create `pkg/transportv2/internal/conn/conn.go` with these public internal types and methods:

```go
package conn

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/rpc"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/sched"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/wire"
)

type Config struct {
	Limits core.Limits
}

type Outbound struct {
	Kind      core.FrameKind
	Priority  core.Priority
	ServiceID uint16
	RequestID uint64
	Payload   core.OwnedBuffer
}

type Inbound struct {
	Kind      core.FrameKind
	Priority  core.Priority
	ServiceID uint16
	RequestID uint64
	Payload   core.OwnedBuffer
	Conn      *Conn
}

type Dispatch interface {
	Dispatch(context.Context, Inbound)
}

type DispatchFunc func(context.Context, Inbound)

func (f DispatchFunc) Dispatch(ctx context.Context, inbound Inbound) { f(ctx, inbound) }

type Conn struct {
	raw      net.Conn
	cfg      Config
	dispatch Dispatch
	scheduler *sched.Scheduler
	pending  *rpc.PendingTable
	nextID   atomic.Uint64
	ctx      context.Context
	cancel   context.CancelFunc
	closeOnce sync.Once
	done     chan struct{}
}

func New(raw net.Conn, cfg Config, dispatch Dispatch) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	return &Conn{
		raw: raw,
		cfg: cfg,
		dispatch: dispatch,
		scheduler: sched.New(sched.Config{
			MaxItems: cfg.Limits.MaxQueuedItemsPerConn,
			MaxBytes: cfg.Limits.MaxQueuedBytesPerConn,
			MaxBatchFrames: cfg.Limits.MaxBatchFrames,
			MaxBatchBytes: cfg.Limits.MaxBatchBytes,
		}),
		pending: rpc.NewPendingTable(16),
		ctx: ctx,
		cancel: cancel,
		done: make(chan struct{}),
	}
}

func (c *Conn) Start() {
	go c.readLoop()
	go c.writeLoop()
}

func (c *Conn) Send(ctx context.Context, outbound Outbound) error {
	if outbound.Kind == 0 {
		outbound.Kind = core.FrameKindData
	}
	if outbound.Payload.Len() > c.cfg.Limits.MaxFrameBodyBytes {
		outbound.Payload.Release()
		return core.ErrMsgTooLarge
	}
	err := c.scheduler.Enqueue(ctx, sched.Item{Priority: outbound.Priority, Bytes: outbound.Payload.Len() + wire.HeaderSize, Value: outbound})
	if err != nil {
		return err
	}
	return nil
}

func (c *Conn) Call(ctx context.Context, outbound Outbound) ([]byte, error) {
	reqID := c.nextID.Add(1)
	outbound.Kind = core.FrameKindRPCRequest
	outbound.RequestID = reqID
	ch := make(chan rpc.Response, 1)
	c.pending.Store(reqID, ch)
	if err := c.Send(ctx, outbound); err != nil {
		c.pending.Delete(reqID)
		return nil, err
	}
	select {
	case resp := <-ch:
		return resp.Payload, resp.Err
	case <-ctx.Done():
		c.pending.Delete(reqID)
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, core.ErrCanceled
		}
		return nil, ctx.Err()
	case <-c.ctx.Done():
		c.pending.Delete(reqID)
		return nil, core.ErrStopped
	}
}

func (c *Conn) Close(err error) {
	if err == nil {
		err = core.ErrStopped
	}
	c.closeOnce.Do(func() {
		c.cancel()
		_ = c.raw.Close()
		for _, item := range c.scheduler.Stop(err) {
			if outbound, ok := item.Value.(Outbound); ok {
				outbound.Payload.Release()
			}
		}
		c.pending.FailAll(err)
		<-c.done
	})
}

func (c *Conn) readLoop() {
	defer close(c.done)
	for {
		frame, body, err := wire.ReadFrame(c.raw, c.cfg.Limits.MaxFrameBodyBytes)
		if err != nil {
			c.cancel()
			c.pending.FailAll(err)
			return
		}
		if frame.Kind == core.FrameKindRPCResponse {
			c.handleRPCResponse(frame.RequestID, body)
			continue
		}
		if c.dispatch == nil {
			body.Release()
			continue
		}
		c.dispatch.Dispatch(c.ctx, Inbound{
			Kind: frame.Kind, Priority: frame.Priority, ServiceID: frame.ServiceID,
			RequestID: frame.RequestID, Payload: body, Conn: c,
		})
	}
}

func (c *Conn) writeLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}
		batch, err := c.scheduler.WaitBatch()
		if err != nil {
			return
		}
		for _, item := range batch {
			outbound := item.Value.(Outbound)
			_, err := wire.WriteFrame(c.raw, wire.Frame{
				Kind: outbound.Kind, Priority: outbound.Priority, ServiceID: outbound.ServiceID, RequestID: outbound.RequestID,
			}, outbound.Payload, c.cfg.Limits.MaxFrameBodyBytes)
			outbound.Payload.Release()
			if err != nil {
				c.cancel()
				_ = c.raw.Close()
				c.pending.FailAll(err)
				return
			}
		}
	}
}

func (c *Conn) handleRPCResponse(reqID uint64, body core.OwnedBuffer) {
	defer body.Release()
	data := body.Bytes()
	if len(data) == 0 {
		c.pending.Complete(reqID, rpc.Response{})
		return
	}
	status := data[0]
	payload := append([]byte(nil), data[1:]...)
	if status != wire.ResponseOK {
		c.pending.Complete(reqID, rpc.Response{Err: core.RemoteError{Code: "remote_error", Message: string(payload)}})
		return
	}
	c.pending.Complete(reqID, rpc.Response{Payload: payload})
}

func EncodeRPCResponse(status uint8, payload []byte) core.OwnedBuffer {
	buf := make([]byte, 1+len(payload))
	buf[0] = status
	copy(buf[1:], payload)
	return core.NewOwnedBuffer(buf, nil)
}

func RequestIDFromPayload(payload []byte) uint64 {
	if len(payload) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(payload[:8])
}
```

- [ ] **Step 5: Run connection tests**

Run:

```sh
go test ./pkg/transportv2/internal/conn -count=1
```

Expected: PASS. `writeLoop` blocks in `WaitBatch` while the scheduler is empty, so the test must not require sleeps or spin-loop mitigation.

- [ ] **Step 6: Run race test for connection startup**

Run:

```sh
go test -race ./pkg/transportv2/internal/conn -run TestConnDispatchesInboundFrame -count=1
```

Expected: PASS with no race report.

- [ ] **Step 7: Commit**

```sh
git add pkg/transportv2/internal/conn pkg/transportv2/wire
git commit -m "feat: add transportv2 connection actor"
```

---

### Task 7: Peer Manager and Public Client Send Path

**Files:**
- Create: `pkg/transportv2/internal/peer/manager.go`
- Test: `pkg/transportv2/internal/peer/manager_test.go`
- Modify: `pkg/transportv2/client.go`
- Test: `pkg/transportv2/client_server_test.go`

- [ ] **Step 1: Write failing peer manager tests**

Add `pkg/transportv2/internal/peer/manager_test.go`:

```go
package peer

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

type discovery map[core.NodeID]string

func (d discovery) Resolve(nodeID core.NodeID) (string, error) {
	addr, ok := d[nodeID]
	if !ok {
		return "", core.ErrNodeNotFound
	}
	return addr, nil
}

func TestManagerAcquireDialsOncePerSlot(t *testing.T) {
	var dials atomic.Int32
	manager := NewManager(Config{
		Discovery: discovery{2: "pipe"},
		PoolSize: 1,
		Limits: testLimits(),
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dials.Add(1)
			server, client := net.Pipe()
			go server.Close()
			return client, nil
		},
	})
	defer manager.Stop()
	if _, err := manager.Acquire(context.Background(), 2, 0); err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if _, err := manager.Acquire(context.Background(), 2, 0); err != nil {
		t.Fatalf("second Acquire() error = %v", err)
	}
	if dials.Load() != 1 {
		t.Fatalf("dials = %d, want 1", dials.Load())
	}
}

func TestManagerStopPreventsDial(t *testing.T) {
	manager := NewManager(Config{Discovery: discovery{2: "pipe"}, PoolSize: 1, Limits: testLimits()})
	manager.Stop()
	_, err := manager.Acquire(context.Background(), 2, 0)
	if !errors.Is(err, core.ErrStopped) {
		t.Fatalf("Acquire() error = %v, want %v", err, core.ErrStopped)
	}
}

func testLimits() core.Limits {
	return core.Limits{
		MaxFrameBodyBytes: 1 << 20, MaxQueuedBytesPerConn: 1 << 20,
		MaxQueuedItemsPerConn: 128, MaxBatchBytes: 1 << 20, MaxBatchFrames: 16,
		DialFailureCooldown: 50 * time.Millisecond,
	}
}
```

- [ ] **Step 2: Run peer tests and verify failure**

Run:

```sh
go test ./pkg/transportv2/internal/peer -count=1
```

Expected: FAIL with missing peer manager.

- [ ] **Step 3: Implement peer manager**

Create `pkg/transportv2/internal/peer/manager.go` with `Config`, `Manager`, `Acquire`, `ClosePeer`, and `Stop`. Use the existing `pkg/transport` pattern as a reference, but keep the lifecycle stricter:

```go
type Config struct {
	Discovery core.Discovery
	PoolSize  int
	Limits    core.Limits
	Dial      func(ctx context.Context, network, addr string) (net.Conn, error)
}
```

The implementation rules:

- `Acquire` returns `core.ErrStopped` after `Stop`.
- A slot has `mu`, `ready chan struct{}`, `conn *conn.Conn`, `lastErr`, and `lastDialFail`.
- Only the caller that installs `ready` performs the dial.
- Dial uses `net.Dialer.DialContext` when `Config.Dial` is nil.
- New connections are created with `conn.New(raw, conn.Config{Limits: cfg.Limits}, nil)` and `Start`.
- `ClosePeer` closes all slot connections and deletes the peer from the manager.

- [ ] **Step 4: Run peer tests**

Run:

```sh
go test ./pkg/transportv2/internal/peer -count=1
```

Expected: PASS.

- [ ] **Step 5: Wire public Client to peer manager**

Replace `pkg/transportv2/client.go` shell with:

```go
package transportv2

import (
	"context"
	"net"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/conn"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/peer"
)

type Client struct {
	cfg     ClientConfig
	manager *peer.Manager
}

func NewClient(cfg ClientConfig) (*Client, error) {
	normalized, err := normalizeClientConfig(cfg)
	if err != nil {
		return nil, err
	}
	m := peer.NewManager(peer.Config{
		Discovery: normalized.Discovery,
		PoolSize: normalized.PoolSize,
		Limits: normalized.Limits,
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if normalized.Dialer != nil {
				return normalized.Dialer(network, addr, normalized.DialTimeout)
			}
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	})
	return &Client{cfg: normalized, manager: m}, nil
}

func (c *Client) Send(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload []byte) error {
	return c.SendOwned(ctx, nodeID, shardKey, pri, serviceID, core.CopyOwnedBuffer(payload))
}

func (c *Client) SendOwned(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload OwnedBuffer) error {
	mc, err := c.manager.Acquire(ctx, core.NodeID(nodeID), shardKey)
	if err != nil {
		payload.Release()
		return err
	}
	return mc.Send(ctx, conn.Outbound{Kind: core.FrameKindData, Priority: core.Priority(pri), ServiceID: serviceID, Payload: payload})
}

func (c *Client) Notify(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload []byte) error {
	mc, err := c.manager.Acquire(ctx, core.NodeID(nodeID), shardKey)
	if err != nil {
		return err
	}
	return mc.Send(ctx, conn.Outbound{Kind: core.FrameKindNotify, Priority: core.Priority(pri), ServiceID: serviceID, Payload: core.CopyOwnedBuffer(payload)})
}

func (c *Client) ClosePeer(nodeID NodeID) { c.manager.ClosePeer(core.NodeID(nodeID)) }
func (c *Client) Stop() { c.manager.Stop() }
func (c *Client) Stats() Stats { return c.manager.Stats() }
```

- [ ] **Step 6: Add public send-path test**

Add a focused test to `pkg/transportv2/client_server_test.go`:

```go
package transportv2

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
	"github.com/WuKongIM/WuKongIM/pkg/transportv2/wire"
)

func TestClientSendWritesFrame(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	received := make(chan string, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		_, body, err := wire.ReadFrame(raw, DefaultLimits().MaxFrameBodyBytes)
		if err == nil {
			received <- string(body.Bytes())
			body.Release()
		}
	}()
	client, err := NewClient(ClientConfig{NodeID: 1, Discovery: testDiscovery{2: ln.Addr().String()}, PoolSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	if err := client.Send(context.Background(), 2, 0, PriorityRaft, 7, []byte("hello")); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	select {
	case got := <-received:
		if got != "hello" {
			t.Fatalf("received = %q, want hello", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for frame")
	}
	_ = core.PriorityRaft
}
```

- [ ] **Step 7: Run client and peer tests, then commit**

Run:

```sh
go test ./pkg/transportv2 ./pkg/transportv2/internal/peer -run 'Test(ClientSend|Manager)' -count=1
```

Expected: PASS.

Commit:

```sh
git add pkg/transportv2
git commit -m "feat: add transportv2 peer manager"
```

---

### Task 8: Server Service Registry and Bounded Workers

**Files:**
- Create: `pkg/transportv2/internal/rpc/service.go`
- Test: `pkg/transportv2/internal/rpc/service_test.go`
- Modify: `pkg/transportv2/server.go`
- Test: `pkg/transportv2/client_server_test.go`

- [ ] **Step 1: Write failing service tests**

Add `pkg/transportv2/internal/rpc/service_test.go`:

```go
package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

func TestServiceQueueFullReturnsBusy(t *testing.T) {
	svc := NewService(7, func(ctx context.Context, payload []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024})
	defer svc.Stop()
	if err := svc.Enqueue(Request{Payload: core.NewOwnedBuffer([]byte("a"), nil)}); err != nil {
		t.Fatalf("first enqueue error = %v", err)
	}
	if err := svc.Enqueue(Request{Payload: core.NewOwnedBuffer([]byte("b"), nil)}); err != nil {
		t.Fatalf("second enqueue error = %v", err)
	}
	err := svc.Enqueue(Request{Payload: core.NewOwnedBuffer([]byte("c"), nil)})
	if !errors.Is(err, core.ErrBusy) {
		t.Fatalf("third enqueue error = %v, want %v", err, core.ErrBusy)
	}
}

func TestServiceTimeout(t *testing.T) {
	done := make(chan Response, 1)
	svc := NewService(7, func(ctx context.Context, payload []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 1024, Timeout: 10 * time.Millisecond})
	defer svc.Stop()
	err := svc.Enqueue(Request{Payload: core.NewOwnedBuffer([]byte("a"), nil), Reply: done})
	if err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	resp := <-done
	if !errors.Is(resp.Err, core.ErrTimeout) {
		t.Fatalf("response error = %v, want %v", resp.Err, core.ErrTimeout)
	}
}
```

- [ ] **Step 2: Run service tests and verify failure**

Run:

```sh
go test ./pkg/transportv2/internal/rpc -run TestService -count=1
```

Expected: FAIL with missing service registry.

- [ ] **Step 3: Implement service worker**

Create `pkg/transportv2/internal/rpc/service.go`:

```go
package rpc

import (
	"context"
	"errors"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

type Request struct {
	Payload core.OwnedBuffer
	Reply   chan Response
}

type Service struct {
	id      uint16
	handler core.Handler
	opts    core.ServiceOptions
	queue   chan Request
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

func NewService(id uint16, handler core.Handler, opts core.ServiceOptions) *Service {
	s := &Service{id: id, handler: handler, opts: opts, queue: make(chan Request, opts.QueueSize), stopCh: make(chan struct{})}
	for i := 0; i < opts.Concurrency; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	return s
}

func (s *Service) Enqueue(req Request) error {
	if s.opts.MaxPayload > 0 && req.Payload.Len() > s.opts.MaxPayload {
		req.Payload.Release()
		return core.ErrMsgTooLarge
	}
	select {
	case s.queue <- req:
		return nil
	case <-s.stopCh:
		req.Payload.Release()
		return core.ErrStopped
	default:
		req.Payload.Release()
		return core.ErrBusy
	}
}

func (s *Service) Stop() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
	s.wg.Wait()
}

func (s *Service) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stopCh:
			return
		case req := <-s.queue:
			s.handle(req)
		}
	}
}

func (s *Service) handle(req Request) {
	defer req.Payload.Release()
	ctx := context.Background()
	cancel := func() {}
	if s.opts.Timeout > 0 {
		var c context.CancelFunc
		ctx, c = context.WithTimeout(ctx, s.opts.Timeout)
		cancel = c
	}
	defer cancel()
	resp, err := s.handler(ctx, req.Payload.Bytes())
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = core.ErrTimeout
	}
	if req.Reply != nil {
		req.Reply <- Response{Payload: resp, Err: err}
	}
}
```

- [ ] **Step 4: Run service tests**

Run:

```sh
go test ./pkg/transportv2/internal/rpc -run TestService -count=1
```

Expected: PASS.

- [ ] **Step 5: Implement public Server accept and dispatch**

Replace `pkg/transportv2/server.go` shell with a server that:

- Stores handlers in `map[uint16]*rpc.Service` protected by `sync.RWMutex`.
- `Handle` validates `ServiceOptions`, rejects duplicate service ids, and creates `rpc.NewService`.
- `ListenAndServe` opens a TCP listener and runs `acceptLoop`.
- `acceptLoop` creates `conn.Conn` with a dispatch function.
- Dispatch enqueues `FrameKindData`, `FrameKindNotify`, and `FrameKindRPCRequest` to the target service.
- RPC requests pass a reply channel; a goroutine waits for the reply and sends `FrameKindRPCResponse` through the same connection.

Use this response encoding:

```go
func encodeServiceResponse(resp rpc.Response) core.OwnedBuffer {
	if resp.Err != nil {
		return conn.EncodeRPCResponse(wire.ResponseErr, []byte(resp.Err.Error()))
	}
	return conn.EncodeRPCResponse(wire.ResponseOK, resp.Payload)
}
```

- [ ] **Step 6: Add public server notify test**

Append to `client_server_test.go`:

```go
func TestServerHandlesNotify(t *testing.T) {
	server, err := NewServer(ServerConfig{NodeID: 2})
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan string, 1)
	if err := server.Handle(7, func(ctx context.Context, payload []byte) ([]byte, error) {
		received <- string(payload)
		return nil, nil
	}, ServiceOptions{Concurrency: 1, QueueSize: 8, MaxQueueBytes: 1024}); err != nil {
		t.Fatal(err)
	}
	if err := server.ListenAndServe("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	client, err := NewClient(ClientConfig{NodeID: 1, Discovery: testDiscovery{2: server.Addr()}, PoolSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	if err := client.Notify(context.Background(), 2, 0, PriorityRPC, 7, []byte("hello")); err != nil {
		t.Fatalf("Notify() error = %v", err)
	}
	select {
	case got := <-received:
		if got != "hello" {
			t.Fatalf("received = %q, want hello", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for notify")
	}
}
```

- [ ] **Step 7: Run public server tests and commit**

Run:

```sh
go test ./pkg/transportv2 ./pkg/transportv2/internal/rpc -run 'Test(ServerHandlesNotify|Service)' -count=1
```

Expected: PASS.

Commit:

```sh
git add pkg/transportv2
git commit -m "feat: add transportv2 server services"
```

---

### Task 9: RPC Round Trip and Stop Semantics

**Files:**
- Modify: `pkg/transportv2/client.go`
- Modify: `pkg/transportv2/server.go`
- Test: `pkg/transportv2/client_server_test.go`

- [ ] **Step 1: Write failing RPC tests**

Append to `client_server_test.go`:

```go
func TestClientCallRoundTrip(t *testing.T) {
	server, err := NewServer(ServerConfig{NodeID: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Handle(9, func(ctx context.Context, payload []byte) ([]byte, error) {
		return append([]byte("ok:"), payload...), nil
	}, ServiceOptions{Concurrency: 2, QueueSize: 8, MaxQueueBytes: 4096}); err != nil {
		t.Fatal(err)
	}
	if err := server.ListenAndServe("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	client, err := NewClient(ClientConfig{NodeID: 1, Discovery: testDiscovery{2: server.Addr()}, PoolSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()
	resp, err := client.Call(context.Background(), 2, 0, PriorityRPC, 9, []byte("ping"))
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if string(resp) != "ok:ping" {
		t.Fatalf("resp = %q, want ok:ping", resp)
	}
}

func TestClientStopFailsPendingCall(t *testing.T) {
	server, err := NewServer(ServerConfig{NodeID: 2})
	if err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	if err := server.Handle(9, func(ctx context.Context, payload []byte) ([]byte, error) {
		<-block
		return []byte("late"), nil
	}, ServiceOptions{Concurrency: 1, QueueSize: 8, MaxQueueBytes: 4096}); err != nil {
		t.Fatal(err)
	}
	if err := server.ListenAndServe("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	defer close(block)

	client, err := NewClient(ClientConfig{NodeID: 1, Discovery: testDiscovery{2: server.Addr()}, PoolSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() {
		_, err := client.Call(context.Background(), 2, 0, PriorityRPC, 9, []byte("ping"))
		errCh <- err
	}()
	time.Sleep(20 * time.Millisecond)
	client.Stop()
	select {
	case err := <-errCh:
		if !errors.Is(err, ErrStopped) {
			t.Fatalf("Call() error = %v, want %v", err, ErrStopped)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for call failure")
	}
}
```

- [ ] **Step 2: Run RPC tests and verify failure**

Run:

```sh
go test ./pkg/transportv2 -run 'TestClient(CallRoundTrip|StopFailsPendingCall)' -count=1
```

Expected: FAIL because `Client.Call` is not wired yet or response dispatch is incomplete.

- [ ] **Step 3: Implement `Client.Call`**

Add to `client.go`:

```go
func (c *Client) Call(ctx context.Context, nodeID NodeID, shardKey uint64, pri Priority, serviceID uint16, payload []byte) ([]byte, error) {
	mc, err := c.manager.Acquire(ctx, core.NodeID(nodeID), shardKey)
	if err != nil {
		return nil, err
	}
	return mc.Call(ctx, conn.Outbound{Priority: core.Priority(pri), ServiceID: serviceID, Payload: core.CopyOwnedBuffer(payload)})
}
```

- [ ] **Step 4: Complete server RPC response path**

In server dispatch for `FrameKindRPCRequest`, enqueue `rpc.Request{Payload: inbound.Payload, Reply: reply}` and start one small response sender goroutine:

```go
reply := make(chan rpc.Response, 1)
if err := svc.Enqueue(rpc.Request{Payload: inbound.Payload, Reply: reply}); err != nil {
	resp := conn.EncodeRPCResponse(wire.ResponseErr, []byte(err.Error()))
	_ = inbound.Conn.Send(ctx, conn.Outbound{Kind: core.FrameKindRPCResponse, Priority: core.PriorityRPC, ServiceID: inbound.ServiceID, RequestID: inbound.RequestID, Payload: resp})
	return
}
go func() {
	select {
	case resp := <-reply:
		_ = inbound.Conn.Send(ctx, conn.Outbound{
			Kind: core.FrameKindRPCResponse, Priority: core.PriorityRPC,
			ServiceID: inbound.ServiceID, RequestID: inbound.RequestID,
			Payload: encodeServiceResponse(resp),
		})
	case <-ctx.Done():
	}
}()
```

- [ ] **Step 5: Run RPC tests and race smoke**

Run:

```sh
go test ./pkg/transportv2 -run 'TestClient(CallRoundTrip|StopFailsPendingCall)' -count=1
go test -race ./pkg/transportv2 -run TestClientCallRoundTrip -count=1
```

Expected: both PASS.

- [ ] **Step 6: Commit**

```sh
git add pkg/transportv2
git commit -m "feat: add transportv2 rpc round trip"
```

---

### Task 10: Testkit, Benchmarks, and Opt-In Stress

**Files:**
- Create: `pkg/transportv2/testkit/fault_conn.go`
- Create: `pkg/transportv2/testkit/harness.go`
- Create: `pkg/transportv2/benchmark_test.go`
- Create: `pkg/transportv2/stress_test.go`

- [ ] **Step 1: Add testkit fault connection**

Create `pkg/transportv2/testkit/fault_conn.go`:

```go
package testkit

import (
	"io"
	"net"
	"sync"
	"time"
)

type BlockingConn struct {
	closeOnce sync.Once
	closed chan struct{}
	ReleaseWrite chan struct{}
}

func NewBlockingConn() *BlockingConn {
	return &BlockingConn{closed: make(chan struct{}), ReleaseWrite: make(chan struct{})}
}

func (c *BlockingConn) Read(_ []byte) (int, error) {
	<-c.closed
	return 0, io.ErrClosedPipe
}

func (c *BlockingConn) Write(b []byte) (int, error) {
	select {
	case <-c.ReleaseWrite:
		return len(b), nil
	case <-c.closed:
		return 0, io.ErrClosedPipe
	}
}

func (c *BlockingConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *BlockingConn) LocalAddr() net.Addr { return addr("local") }
func (c *BlockingConn) RemoteAddr() net.Addr { return addr("remote") }
func (c *BlockingConn) SetDeadline(time.Time) error { return nil }
func (c *BlockingConn) SetReadDeadline(time.Time) error { return nil }
func (c *BlockingConn) SetWriteDeadline(time.Time) error { return nil }

type addr string
func (a addr) Network() string { return "test" }
func (a addr) String() string { return string(a) }
```

- [ ] **Step 2: Add local harness**

Create `pkg/transportv2/testkit/harness.go` with:

```go
package testkit

import (
	"testing"

	transport "github.com/WuKongIM/WuKongIM/pkg/transportv2"
)

type StaticDiscovery map[transport.NodeID]string

func (d StaticDiscovery) Resolve(nodeID transport.NodeID) (string, error) {
	addr, ok := d[nodeID]
	if !ok {
		return "", transport.ErrNodeNotFound
	}
	return addr, nil
}

type Harness struct {
	Server *transport.Server
	Client *transport.Client
}

func NewHarness(t testing.TB, handler transport.Handler) *Harness {
	t.Helper()
	server, err := transport.NewServer(transport.ServerConfig{NodeID: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Handle(7, handler, transport.ServiceOptions{Concurrency: 4, QueueSize: 1024, MaxQueueBytes: 8 << 20}); err != nil {
		t.Fatal(err)
	}
	if err := server.ListenAndServe("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	client, err := transport.NewClient(transport.ClientConfig{NodeID: 1, Discovery: StaticDiscovery{2: server.Addr()}, PoolSize: 4})
	if err != nil {
		server.Stop()
		t.Fatal(err)
	}
	return &Harness{Server: server, Client: client}
}

func (h *Harness) Close() {
	if h.Client != nil {
		h.Client.Stop()
	}
	if h.Server != nil {
		h.Server.Stop()
	}
}
```

- [ ] **Step 3: Add benchmark smoke**

Create `pkg/transportv2/benchmark_test.go`:

```go
package transportv2

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/testkit"
)

func BenchmarkTransportV2RPC(b *testing.B) {
	h := testkit.NewHarness(b, func(ctx context.Context, payload []byte) ([]byte, error) {
		return []byte("ok"), nil
	})
	defer h.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := h.Client.Call(context.Background(), 2, uint64(i), PriorityRPC, 7, []byte("ping")); err != nil {
			b.Fatal(err)
		}
	}
}
```

- [ ] **Step 4: Add opt-in mixed stress test**

Create `pkg/transportv2/stress_test.go`:

```go
package transportv2

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/testkit"
)

func TestTransportV2StressMixedRPCAndSend(t *testing.T) {
	if os.Getenv("WK_TRANSPORTV2_STRESS") != "1" {
		t.Skip("set WK_TRANSPORTV2_STRESS=1 to enable")
	}
	var received atomic.Uint64
	h := testkit.NewHarness(t, func(ctx context.Context, payload []byte) ([]byte, error) {
		received.Add(1)
		return []byte("ok"), nil
	})
	defer h.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for ctx.Err() == nil {
				_, _ = h.Client.Call(ctx, 2, uint64(worker), PriorityRPC, 7, []byte("ping"))
			}
		}(i)
	}
	wg.Wait()
	if received.Load() == 0 {
		t.Fatal("received = 0, want stress traffic")
	}
	t.Logf("transportv2 stress rpc count=%d", received.Load())
}
```

- [ ] **Step 5: Run benchmark compile and opt-in skip test**

Run:

```sh
go test ./pkg/transportv2 ./pkg/transportv2/testkit -run TestTransportV2StressMixedRPCAndSend -count=1
go test ./pkg/transportv2 -bench BenchmarkTransportV2RPC -run '^$' -benchtime=100x
```

Expected: stress test skips without env; benchmark completes.

- [ ] **Step 6: Commit**

```sh
git add pkg/transportv2
git commit -m "test: add transportv2 pressure harness"
```

---

### Task 11: Full Verification and Package Cleanup

**Files:**
- Modify: `pkg/transportv2/types.go`
- Modify: `pkg/transportv2/config.go`
- Modify: `pkg/transportv2/errors.go`
- Modify: `pkg/transportv2/client.go`
- Modify: `pkg/transportv2/server.go`
- Modify: `pkg/transportv2/wire/frame.go`
- Modify: `pkg/transportv2/wire/reader.go`
- Modify: `pkg/transportv2/wire/writer.go`
- Modify: `pkg/transportv2/internal/core/types.go`
- Modify: `pkg/transportv2/internal/buffer/slab.go`
- Modify: `pkg/transportv2/internal/sched/scheduler.go`
- Modify: `pkg/transportv2/internal/rpc/pending.go`
- Modify: `pkg/transportv2/internal/rpc/service.go`
- Modify: `pkg/transportv2/internal/conn/conn.go`
- Modify: `pkg/transportv2/internal/peer/manager.go`
- Do not modify: `pkg/transport/**`

- [ ] **Step 1: Run full transportv2 tests**

Run:

```sh
go test ./pkg/transportv2/... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run race tests**

Run:

```sh
go test -race ./pkg/transportv2/... -count=1
```

Expected: PASS with no race reports.

- [ ] **Step 3: Run focused benchmark**

Run:

```sh
go test ./pkg/transportv2 -bench 'BenchmarkTransportV2RPC' -run '^$' -benchtime=1000x -benchmem
```

Expected: benchmark reports `ns/op`, `B/op`, and `allocs/op` without failures.

- [ ] **Step 4: Run existing transport tests for isolation**

Run:

```sh
go test ./pkg/transport -count=1
```

Expected: PASS. This confirms `pkg/transportv2` did not disturb the old transport package.

- [ ] **Step 5: Run formatting**

Run:

```sh
gofmt -w pkg/transportv2
go test ./pkg/transportv2/... -count=1
```

Expected: PASS after formatting.

- [ ] **Step 6: Final commit**

```sh
git add pkg/transportv2
git commit -m "feat: complete transportv2 engine foundation"
```

---

## Plan Self-Review

Spec coverage:

- New `pkg/transportv2` package: Tasks 1-11.
- Fixed 24-byte wire header: Task 2.
- Explicit payload ownership and `SendOwned`: Tasks 1, 3, and 7.
- Strict lifecycle and no new dial after stop: Tasks 6, 7, 9, and 11.
- Byte-aware backpressure and priority scheduling: Task 4.
- Pending RPC failure on close: Tasks 5, 6, and 9.
- Bounded service worker queues: Task 8.
- Non-blocking observer foundation: Task 1 defines event/observer types; implementation keeps hot-path hooks out of the first slice.
- Testkit, benchmarks, and opt-in stress: Task 10.
- Full race and package verification: Task 11.

Scope notes:

- This plan intentionally excludes clusterv2 integration. The design requires one narrow integration after transportv2 tests stabilize; that belongs in a follow-up plan.
- This plan intentionally implements remote errors with a stable `RemoteError` wrapper and string payload. Rich error-code serialization can be expanded after the first RPC path is green.
