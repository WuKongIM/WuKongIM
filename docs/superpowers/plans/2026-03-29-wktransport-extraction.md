# wktransport Extraction Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the network transport layer from `wkcluster/` into a standalone `wktransport/` package with zero external dependencies, enabling business layer reuse.

**Architecture:** New `wktransport/` package provides frame codec, connection pool, TCP server (msgType-based handler dispatch), and client (one-way Send + request/response RPC). `wkcluster/` becomes a thin consumer that registers raft-specific handlers and adapts `wktransport.Client` to `multiraft.Transport`.

**Tech Stack:** Go stdlib only (net, sync, encoding/binary, context). No external dependencies for wktransport.

**Spec:** `docs/superpowers/specs/2026-03-29-wktransport-extraction-design.md`

---

## File Map

### New files (wktransport/)

| File | Responsibility |
|------|----------------|
| `wktransport/types.go` | `NodeID` type alias, `MessageHandler`, `RPCHandler`, `Discovery` interface |
| `wktransport/errors.go` | Network-layer errors and reserved constants |
| `wktransport/codec.go` | Frame encode/decode `[type:1][len:4][body:N]`, buffer pool |
| `wktransport/codec_test.go` | Frame round-trip, large message rejection, msgType=0 rejection |
| `wktransport/pool.go` | Outbound connection pool with per-slot locking |
| `wktransport/pool_test.go` | Get/Release/Reset, concurrency, reconnect |
| `wktransport/server.go` | TCP accept loop, msgType handler dispatch, RPC handling |
| `wktransport/server_test.go` | Handler registration, message dispatch, Stop cleanup |
| `wktransport/client.go` | `Send()` (one-way) + `RPC()` (request/response), readLoop |
| `wktransport/client_test.go` | Send, RPC round-trip, timeout, Stop cancellation |

### Modified files (wkcluster/)

| File | Change |
|------|--------|
| `wkcluster/discovery.go` | Update `Discovery` interface: `Resolve(uint64)` instead of `Resolve(multiraft.NodeID)` |
| `wkcluster/static_discovery.go` | Change `Resolve` parameter to `uint64`, internal map key to `uint64` |
| `wkcluster/static_discovery_test.go` | Adjust for `uint64` parameter |
| `wkcluster/errors.go` | Remove `ErrTimeout`, `ErrNodeNotFound`, `ErrStopped` |
| `wkcluster/codec.go` | Remove frame-level functions (moved to wktransport); keep raft/forward body encode/decode; promote `encodeRaftBody`/`encodeForwardBody`/`encodeRespBody` from test helpers to production code |
| `wkcluster/helpers_test.go` | Remove (encode functions moved to production codec.go) |
| `wkcluster/transport.go` | Replace with ~30 line `raftTransport` adapter |
| `wkcluster/forward.go` | Replace with ~40 line `forwardToLeader` using `Client.RPC` |
| `wkcluster/cluster.go` | Compose `wktransport.Server`/`Pool`/`Client`, update Start/Stop |
| `wkcluster/config.go` | Replace `PoolSize`/`DialTimeout` with `RaftPoolSize` (keep existing field names compatible) |
| `wkcluster/transport_test.go` | Rewrite for raftTransport adapter |
| `wkcluster/forward_test.go` | Rewrite for forwardToLeader |
| `wkcluster/codec_test.go` | Keep raft/forward body tests; remove frame-level tests (moved) |

---

## Task 1: wktransport foundation — types.go + errors.go

**Files:**
- Create: `wktransport/types.go`
- Create: `wktransport/errors.go`

- [ ] **Step 0: Create the wktransport directory**

Run: `mkdir -p wktransport`

- [ ] **Step 1: Create `wktransport/types.go`**

```go
package wktransport

import (
	"context"
	"net"
)

// NodeID identifies a cluster node. Type alias for uint64 — zero-cost
// conversion with multiraft.NodeID (which is a named uint64 type, requiring
// an explicit cast at call sites).
type NodeID = uint64

// MessageHandler processes an inbound message of a specific type.
// conn is provided so the handler can write back on the same connection if needed.
type MessageHandler func(conn net.Conn, body []byte)

// RPCHandler processes an inbound RPC request and returns a response body.
// The ctx passed by the Server is context.Background(). The handler is responsible
// for applying its own timeout (e.g., wkcluster wraps with forwardTimeout).
type RPCHandler func(ctx context.Context, body []byte) ([]byte, error)

// Discovery resolves a NodeID to a network address.
type Discovery interface {
	Resolve(nodeID NodeID) (addr string, err error)
}
```

- [ ] **Step 2: Create `wktransport/errors.go`**

```go
package wktransport

import "errors"

var (
	ErrStopped        = errors.New("wktransport: stopped")
	ErrTimeout        = errors.New("wktransport: request timeout")
	ErrNodeNotFound   = errors.New("wktransport: node not found")
	ErrMsgTooLarge    = errors.New("wktransport: message too large")
	ErrInvalidMsgType = errors.New("wktransport: invalid message type 0")
)

const (
	// MaxMessageSize is the upper bound for a single wire message body.
	MaxMessageSize = 64 << 20 // 64 MB

	// Reserved message types for built-in RPC mechanism.
	MsgTypeRPCRequest  uint8 = 0xFE
	MsgTypeRPCResponse uint8 = 0xFF

	// maxPooledBufCap prevents the buffer pool from retaining huge slices.
	maxPooledBufCap = 64 * 1024

	// msgHeaderSize is [msgType:1][bodyLen:4].
	msgHeaderSize = 5
)
```

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/tt/Desktop/work/go/wraft && go build ./wktransport/...`
Expected: success, no errors.

- [ ] **Step 4: Commit**

```bash
git add wktransport/types.go wktransport/errors.go
git commit -m "feat(wktransport): add types and errors foundation"
```

---

## Task 2: wktransport codec — codec.go + codec_test.go

**Files:**
- Create: `wktransport/codec.go`
- Create: `wktransport/codec_test.go`

- [ ] **Step 1: Write codec tests**

```go
package wktransport

import (
	"bytes"
	"testing"
)

func TestWriteReadMessage_RoundTrip(t *testing.T) {
	var buf bytes.Buffer
	body := []byte("hello")
	if err := WriteMessage(&buf, 1, body); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	msgType, decoded, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msgType != 1 {
		t.Fatalf("expected msgType=1, got %d", msgType)
	}
	if !bytes.Equal(decoded, body) {
		t.Fatalf("body mismatch")
	}
}

func TestWriteReadMessage_EmptyBody(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMessage(&buf, 42, nil); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	msgType, body, err := ReadMessage(&buf)
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msgType != 42 || len(body) != 0 {
		t.Fatalf("unexpected: msgType=%d bodyLen=%d", msgType, len(body))
	}
}

func TestReadMessage_InvalidMsgType0(t *testing.T) {
	// Manually craft a frame with msgType=0
	frame := []byte{0, 0, 0, 0, 5, 'h', 'e', 'l', 'l', 'o'}
	r := bytes.NewReader(frame)
	_, _, err := ReadMessage(r)
	if err != ErrInvalidMsgType {
		t.Fatalf("expected ErrInvalidMsgType, got: %v", err)
	}
}

func TestReadMessage_TooLarge(t *testing.T) {
	// Craft header claiming 100MB body (> 64MB limit)
	hdr := []byte{1, 0x06, 0x40, 0x00, 0x00}
	r := bytes.NewReader(hdr)
	_, _, err := ReadMessage(r)
	if err == nil {
		t.Fatal("expected error for oversized message")
	}
}

func TestWriteReadMessage_MultipleMessages(t *testing.T) {
	var buf bytes.Buffer
	msgs := []struct {
		msgType uint8
		body    []byte
	}{
		{1, []byte("first")},
		{2, []byte("second")},
		{0xFE, []byte("rpc-req")},
	}
	for _, m := range msgs {
		if err := WriteMessage(&buf, m.msgType, m.body); err != nil {
			t.Fatalf("WriteMessage(%d): %v", m.msgType, err)
		}
	}
	for _, m := range msgs {
		msgType, body, err := ReadMessage(&buf)
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		if msgType != m.msgType || !bytes.Equal(body, m.body) {
			t.Fatalf("expected type=%d body=%q, got type=%d body=%q",
				m.msgType, m.body, msgType, body)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wktransport/ -run TestWriteRead -v`
Expected: FAIL — `WriteMessage` / `ReadMessage` not defined.

- [ ] **Step 3: Implement codec.go**

```go
package wktransport

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 512)
		return b
	},
}

func getBuf(n int) []byte {
	buf := bufPool.Get().([]byte)
	if cap(buf) >= n {
		return buf[:n]
	}
	return make([]byte, n)
}

func putBuf(buf []byte) {
	if cap(buf) <= maxPooledBufCap {
		//nolint:staticcheck // SA6002: slice header is fine here
		bufPool.Put(buf[:0])
	}
}

// WriteMessage encodes and writes a framed message [msgType:1][bodyLen:4][body:N].
// Uses a buffer pool to avoid per-message allocations on the hot path.
func WriteMessage(w io.Writer, msgType uint8, body []byte) error {
	totalSize := msgHeaderSize + len(body)
	buf := getBuf(totalSize)
	buf[0] = msgType
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(body)))
	copy(buf[5:], body)
	_, err := w.Write(buf)
	putBuf(buf)
	return err
}

// ReadMessage reads a framed message from r.
// Returns ErrInvalidMsgType for msgType=0, ErrMsgTooLarge if body exceeds MaxMessageSize.
func ReadMessage(r io.Reader) (msgType uint8, body []byte, err error) {
	var hdr [msgHeaderSize]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	msgType = hdr[0]
	if msgType == 0 {
		return 0, nil, ErrInvalidMsgType
	}
	bodyLen := binary.BigEndian.Uint32(hdr[1:5])
	if bodyLen > MaxMessageSize {
		return 0, nil, fmt.Errorf("%w: %d bytes", ErrMsgTooLarge, bodyLen)
	}
	if bodyLen == 0 {
		return msgType, nil, nil
	}
	body = make([]byte, bodyLen)
	if _, err = io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return msgType, body, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wktransport/ -run TestWriteRead -v && go test ./wktransport/ -run TestReadMessage -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add wktransport/codec.go wktransport/codec_test.go
git commit -m "feat(wktransport): add wire protocol codec with buffer pool"
```

---

## Task 3: wktransport connection pool — pool.go + pool_test.go

**Files:**
- Create: `wktransport/pool.go`
- Create: `wktransport/pool_test.go`

- [ ] **Step 1: Write pool tests**

```go
package wktransport

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// staticDiscovery is a test helper implementing Discovery.
type staticDiscovery struct {
	addrs map[NodeID]string
}

func (d *staticDiscovery) Resolve(nodeID NodeID) (string, error) {
	addr, ok := d.addrs[nodeID]
	if !ok {
		return "", ErrNodeNotFound
	}
	return addr, nil
}

func TestPool_GetRelease(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()

	d := &staticDiscovery{addrs: map[NodeID]string{2: ln.Addr().String()}}
	pool := NewPool(d, 2, 5*time.Second)
	defer pool.Close()

	conn, idx, err := pool.Get(2, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if conn == nil {
		t.Fatal("conn is nil")
	}
	pool.Release(2, idx)
}

func TestPool_GetError_NoRelease(t *testing.T) {
	d := &staticDiscovery{addrs: map[NodeID]string{2: "127.0.0.1:1"}} // bad port
	pool := NewPool(d, 2, 100*time.Millisecond)
	defer pool.Close()

	_, _, err := pool.Get(2, 0)
	if err == nil {
		t.Fatal("expected error for bad address")
	}
	// Must NOT call Release — verify no panic/deadlock by getting again
	_, _, err = pool.Get(2, 0)
	if err == nil {
		t.Fatal("expected error again")
	}
}

func TestPool_NodeNotFound(t *testing.T) {
	d := &staticDiscovery{addrs: map[NodeID]string{}}
	pool := NewPool(d, 2, 5*time.Second)
	defer pool.Close()

	_, _, err := pool.Get(99, 0)
	if err != ErrNodeNotFound {
		t.Fatalf("expected ErrNodeNotFound, got: %v", err)
	}
}

func TestPool_Reset_Reconnects(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn
		}
	}()

	d := &staticDiscovery{addrs: map[NodeID]string{2: ln.Addr().String()}}
	pool := NewPool(d, 2, 5*time.Second)
	defer pool.Close()

	conn1, idx, err := pool.Get(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	pool.Reset(2, idx)
	pool.Release(2, idx)

	// Next Get should create a new connection
	conn2, idx2, err := pool.Get(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if conn1 == conn2 {
		t.Fatal("expected new connection after Reset")
	}
	pool.Release(2, idx2)
}

func TestPool_ShardKey(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn
		}
	}()

	d := &staticDiscovery{addrs: map[NodeID]string{2: ln.Addr().String()}}
	pool := NewPool(d, 4, 5*time.Second)
	defer pool.Close()

	// shardKey 0 and 4 should map to the same index (0 % 4 == 4 % 4)
	_, idx0, err := pool.Get(2, 0)
	if err != nil {
		t.Fatal(err)
	}
	pool.Release(2, idx0)

	_, idx4, err := pool.Get(2, 4)
	if err != nil {
		t.Fatal(err)
	}
	pool.Release(2, idx4)

	if idx0 != idx4 {
		t.Fatalf("expected same index for shardKey 0 and 4, got %d and %d", idx0, idx4)
	}
}

func TestPool_Concurrent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn
		}
	}()

	d := &staticDiscovery{addrs: map[NodeID]string{2: ln.Addr().String()}}
	pool := NewPool(d, 4, 5*time.Second)
	defer pool.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(key uint64) {
			defer wg.Done()
			conn, idx, err := pool.Get(2, key)
			if err != nil {
				t.Errorf("Get(%d): %v", key, err)
				return
			}
			if conn == nil {
				t.Errorf("Get(%d): nil conn", key)
			}
			pool.Release(2, idx)
		}(uint64(i))
	}
	wg.Wait()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wktransport/ -run TestPool -v`
Expected: FAIL — `NewPool` not defined.

- [ ] **Step 3: Implement pool.go**

```go
package wktransport

import (
	"net"
	"sync"
	"time"
)

const tcpKeepAlivePeriod = 30 * time.Second

// Pool manages outbound TCP connections to remote nodes.
// Each Pool instance is fully independent — create separate instances
// for raft and business traffic to keep connections physically isolated.
type Pool struct {
	discovery   Discovery
	size        int
	dialTimeout time.Duration
	nodes       map[NodeID]*nodeConns
	mu          sync.RWMutex
}

type nodeConns struct {
	addr  string
	conns []net.Conn
	mu    []sync.Mutex
}

// NewPool creates a connection pool.
// size is the number of connections per remote node.
func NewPool(discovery Discovery, size int, dialTimeout time.Duration) *Pool {
	return &Pool{
		discovery:   discovery,
		size:        size,
		dialTimeout: dialTimeout,
		nodes:       make(map[NodeID]*nodeConns),
	}
}

// Get selects a connection by shardKey % size, creating one if needed.
// On success, the connection slot lock is held — caller MUST call Release.
// On error, the lock is NOT held — caller MUST NOT call Release.
func (p *Pool) Get(nodeID NodeID, shardKey uint64) (net.Conn, int, error) {
	nc, err := p.getOrCreateNodeConns(nodeID)
	if err != nil {
		return nil, 0, err
	}
	idx := int(shardKey % uint64(p.size))
	nc.mu[idx].Lock()
	if nc.conns[idx] == nil {
		conn, err := net.DialTimeout("tcp", nc.addr, p.dialTimeout)
		if err != nil {
			nc.mu[idx].Unlock()
			return nil, 0, err
		}
		setTCPKeepAlive(conn)
		nc.conns[idx] = conn
	}
	return nc.conns[idx], idx, nil
}

// Release unlocks the connection slot. Must be called after a successful Get.
func (p *Pool) Release(nodeID NodeID, idx int) {
	p.mu.RLock()
	nc, ok := p.nodes[nodeID]
	p.mu.RUnlock()
	if ok {
		nc.mu[idx].Unlock()
	}
}

// Reset closes and clears a connection slot. Caller must hold the slot lock
// (i.e., call Reset between Get and Release). Does not release the lock.
func (p *Pool) Reset(nodeID NodeID, idx int) {
	p.mu.RLock()
	nc, ok := p.nodes[nodeID]
	p.mu.RUnlock()
	if ok && nc.conns[idx] != nil {
		_ = nc.conns[idx].Close()
		nc.conns[idx] = nil
	}
}

// Close closes all connections in all pools.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, nc := range p.nodes {
		for i := range nc.conns {
			nc.mu[i].Lock()
			if nc.conns[i] != nil {
				_ = nc.conns[i].Close()
				nc.conns[i] = nil
			}
			nc.mu[i].Unlock()
		}
	}
}

func (p *Pool) getOrCreateNodeConns(nodeID NodeID) (*nodeConns, error) {
	p.mu.RLock()
	nc, ok := p.nodes[nodeID]
	p.mu.RUnlock()
	if ok {
		return nc, nil
	}

	addr, err := p.discovery.Resolve(nodeID)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if nc, ok = p.nodes[nodeID]; ok {
		return nc, nil
	}
	nc = &nodeConns{
		addr:  addr,
		conns: make([]net.Conn, p.size),
		mu:    make([]sync.Mutex, p.size),
	}
	p.nodes[nodeID] = nc
	return nc, nil
}

func setTCPKeepAlive(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(tcpKeepAlivePeriod)
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wktransport/ -run TestPool -v -count=1`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add wktransport/pool.go wktransport/pool_test.go
git commit -m "feat(wktransport): add connection pool with per-slot locking"
```

---

## Task 4: wktransport server — server.go + server_test.go

**Files:**
- Create: `wktransport/server.go`
- Create: `wktransport/server_test.go`

- [ ] **Step 1: Write server tests**

```go
package wktransport

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServer_StartStop(t *testing.T) {
	s := NewServer()
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if s.Listener() == nil {
		t.Fatal("Listener is nil")
	}
	s.Stop()
}

func TestServer_HandleMessage(t *testing.T) {
	s := NewServer()

	var received atomic.Int32
	s.Handle(1, func(conn net.Conn, body []byte) {
		if string(body) == "ping" {
			received.Add(1)
		}
	})

	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	conn, err := net.Dial("tcp", s.Listener().Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := WriteMessage(conn, 1, []byte("ping")); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	if received.Load() != 1 {
		t.Fatalf("expected 1 message received, got %d", received.Load())
	}
}

func TestServer_HandleMultipleTypes(t *testing.T) {
	s := NewServer()

	var type1, type2 atomic.Int32
	s.Handle(1, func(_ net.Conn, _ []byte) { type1.Add(1) })
	s.Handle(2, func(_ net.Conn, _ []byte) { type2.Add(1) })

	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	conn, err := net.Dial("tcp", s.Listener().Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	WriteMessage(conn, 1, []byte("a"))
	WriteMessage(conn, 2, []byte("b"))
	WriteMessage(conn, 1, []byte("c"))

	time.Sleep(50 * time.Millisecond)
	if type1.Load() != 2 || type2.Load() != 1 {
		t.Fatalf("type1=%d type2=%d", type1.Load(), type2.Load())
	}
}

func TestServer_HandleRPC(t *testing.T) {
	s := NewServer()
	s.HandleRPC(func(ctx context.Context, body []byte) ([]byte, error) {
		return append([]byte("echo:"), body...), nil
	})

	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	conn, err := net.Dial("tcp", s.Listener().Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send RPC request: [requestID:8][payload:N]
	reqBody := encodeRPCRequest(42, []byte("hello"))
	if err := WriteMessage(conn, MsgTypeRPCRequest, reqBody); err != nil {
		t.Fatal(err)
	}

	// Read response
	msgType, respBody, err := ReadMessage(conn)
	if err != nil {
		t.Fatal(err)
	}
	if msgType != MsgTypeRPCResponse {
		t.Fatalf("expected 0xFF, got %d", msgType)
	}
	reqID, errCode, data, err := decodeRPCResponse(respBody)
	if err != nil {
		t.Fatal(err)
	}
	if reqID != 42 || errCode != 0 || string(data) != "echo:hello" {
		t.Fatalf("unexpected: reqID=%d errCode=%d data=%q", reqID, errCode, data)
	}
}

func TestServer_UnregisteredType_Ignored(t *testing.T) {
	s := NewServer()
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	conn, err := net.Dial("tcp", s.Listener().Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send unknown type — should not crash
	WriteMessage(conn, 99, []byte("unknown"))
	time.Sleep(50 * time.Millisecond)
}

func TestServer_StopClosesConnections(t *testing.T) {
	s := NewServer()

	var connected sync.WaitGroup
	connected.Add(1)
	s.Handle(1, func(_ net.Conn, _ []byte) {
		connected.Done()
	})

	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}

	conn, err := net.Dial("tcp", s.Listener().Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	WriteMessage(conn, 1, []byte("hi"))
	connected.Wait()

	// Stop should close the connection
	s.Stop()

	// Subsequent read should fail
	_, _, err = ReadMessage(conn)
	if err == nil {
		t.Fatal("expected error after server Stop")
	}
	conn.Close()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wktransport/ -run TestServer -v`
Expected: FAIL — `NewServer` not defined.

- [ ] **Step 3: Implement server.go**

```go
package wktransport

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
)

// Server is a TCP server that accepts connections and dispatches messages
// by msgType to registered handlers.
type Server struct {
	listener   net.Listener
	handlers   map[uint8]MessageHandler
	rpcHandler RPCHandler
	mu         sync.RWMutex
	accepted   map[net.Conn]struct{}
	acceptedMu sync.Mutex
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// NewServer creates a new Server. Register handlers before calling Start.
func NewServer() *Server {
	return &Server{
		handlers: make(map[uint8]MessageHandler),
		accepted: make(map[net.Conn]struct{}),
		stopCh:   make(chan struct{}),
	}
}

// Handle registers a handler for a message type.
// Panics if msgType is 0, 0xFE, or 0xFF (reserved).
func (s *Server) Handle(msgType uint8, h MessageHandler) {
	if msgType == 0 || msgType == MsgTypeRPCRequest || msgType == MsgTypeRPCResponse {
		panic("wktransport: reserved message type")
	}
	s.mu.Lock()
	s.handlers[msgType] = h
	s.mu.Unlock()
}

// HandleRPC registers the RPC request handler for 0xFE messages.
func (s *Server) HandleRPC(h RPCHandler) {
	s.mu.Lock()
	s.rpcHandler = h
	s.mu.Unlock()
}

// Start begins listening on addr.
func (s *Server) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.listener = ln
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// Stop closes the listener and all accepted connections, then waits for
// all goroutines to exit.
func (s *Server) Stop() {
	close(s.stopCh)
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.acceptedMu.Lock()
	for c := range s.accepted {
		_ = c.Close()
	}
	s.acceptedMu.Unlock()
	s.wg.Wait()
}

// Listener returns the underlying net.Listener.
func (s *Server) Listener() net.Listener {
	return s.listener
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stopCh:
				return
			default:
				continue
			}
		}
		setTCPKeepAlive(conn)
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()

	s.acceptedMu.Lock()
	s.accepted[conn] = struct{}{}
	s.acceptedMu.Unlock()

	defer func() {
		conn.Close()
		s.acceptedMu.Lock()
		delete(s.accepted, conn)
		s.acceptedMu.Unlock()
	}()

	var writeMu sync.Mutex

	for {
		select {
		case <-s.stopCh:
			return
		default:
		}

		msgType, body, err := ReadMessage(conn)
		if err != nil {
			return
		}

		switch msgType {
		case MsgTypeRPCRequest:
			s.mu.RLock()
			h := s.rpcHandler
			s.mu.RUnlock()
			if h != nil {
				s.wg.Add(1)
				go s.handleRPCRequest(conn, &writeMu, h, body)
			}
		case MsgTypeRPCResponse:
			// Responses should not arrive on server-accepted connections
		default:
			s.mu.RLock()
			h := s.handlers[msgType]
			s.mu.RUnlock()
			if h != nil {
				h(conn, body)
			}
		}
	}
}

func (s *Server) handleRPCRequest(conn net.Conn, writeMu *sync.Mutex, handler RPCHandler, body []byte) {
	defer s.wg.Done()

	if len(body) < 8 {
		return
	}
	requestID := binary.BigEndian.Uint64(body[0:8])
	payload := body[8:]

	ctx := context.Background()
	respData, err := handler(ctx, payload)
	var errCode uint8
	if err != nil {
		errCode = 1
		respData = []byte(err.Error())
	}

	respBody := encodeRPCResponse(requestID, errCode, respData)
	writeMu.Lock()
	_ = WriteMessage(conn, MsgTypeRPCResponse, respBody)
	writeMu.Unlock()
}

// encodeRPCRequest encodes [requestID:8][payload:N].
func encodeRPCRequest(requestID uint64, payload []byte) []byte {
	buf := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint64(buf[0:8], requestID)
	copy(buf[8:], payload)
	return buf
}

// encodeRPCResponse encodes [requestID:8][errCode:1][data:N].
func encodeRPCResponse(requestID uint64, errCode uint8, data []byte) []byte {
	buf := make([]byte, 9+len(data))
	binary.BigEndian.PutUint64(buf[0:8], requestID)
	buf[8] = errCode
	copy(buf[9:], data)
	return buf
}

// decodeRPCResponse decodes [requestID:8][errCode:1][data:N].
func decodeRPCResponse(body []byte) (requestID uint64, errCode uint8, data []byte, err error) {
	if len(body) < 9 {
		return 0, 0, nil, fmt.Errorf("wktransport: rpc response body too short: %d", len(body))
	}
	requestID = binary.BigEndian.Uint64(body[0:8])
	errCode = body[8]
	data = body[9:]
	return requestID, errCode, data, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wktransport/ -run TestServer -v -count=1`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add wktransport/server.go wktransport/server_test.go
git commit -m "feat(wktransport): add TCP server with msgType handler dispatch"
```

---

## Task 5: wktransport client — client.go + client_test.go

**Files:**
- Create: `wktransport/client.go`
- Create: `wktransport/client_test.go`

- [ ] **Step 1: Write client tests**

```go
package wktransport

import (
	"bytes"
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_Send(t *testing.T) {
	s := NewServer()
	var received atomic.Int32
	s.Handle(1, func(_ net.Conn, body []byte) {
		if string(body) == "hello" {
			received.Add(1)
		}
	})
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	d := &staticDiscovery{addrs: map[NodeID]string{2: s.Listener().Addr().String()}}
	pool := NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := NewClient(pool)
	defer client.Stop()

	if err := client.Send(2, 0, 1, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if received.Load() != 1 {
		t.Fatalf("expected 1, got %d", received.Load())
	}
}

func TestClient_RPC_RoundTrip(t *testing.T) {
	s := NewServer()
	s.HandleRPC(func(ctx context.Context, body []byte) ([]byte, error) {
		return append([]byte("echo:"), body...), nil
	})
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	d := &staticDiscovery{addrs: map[NodeID]string{2: s.Listener().Addr().String()}}
	pool := NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := NewClient(pool)
	defer client.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.RPC(ctx, 2, 0, []byte("ping"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(resp, []byte("echo:ping")) {
		t.Fatalf("expected echo:ping, got %q", resp)
	}
}

func TestClient_RPC_ContextCancel(t *testing.T) {
	s := NewServer()
	s.HandleRPC(func(ctx context.Context, body []byte) ([]byte, error) {
		time.Sleep(5 * time.Second) // slow handler
		return nil, nil
	})
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	d := &staticDiscovery{addrs: map[NodeID]string{2: s.Listener().Addr().String()}}
	pool := NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := NewClient(pool)
	defer client.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := client.RPC(ctx, 2, 0, []byte("slow"))
	if err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got: %v", err)
	}
}

func TestClient_Stop_CancelsPending(t *testing.T) {
	s := NewServer()
	s.HandleRPC(func(ctx context.Context, body []byte) ([]byte, error) {
		time.Sleep(10 * time.Second)
		return nil, nil
	})
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	d := &staticDiscovery{addrs: map[NodeID]string{2: s.Listener().Addr().String()}}
	pool := NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := NewClient(pool)

	errCh := make(chan error, 1)
	go func() {
		ctx := context.Background()
		_, err := client.RPC(ctx, 2, 0, []byte("long"))
		errCh <- err
	}()

	time.Sleep(50 * time.Millisecond) // let RPC start
	client.Stop()

	select {
	case err := <-errCh:
		if err != ErrStopped {
			t.Fatalf("expected ErrStopped, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for RPC to cancel")
	}
}

func TestClient_RPC_MultipleSequential(t *testing.T) {
	s := NewServer()
	var count atomic.Int32
	s.HandleRPC(func(ctx context.Context, body []byte) ([]byte, error) {
		n := count.Add(1)
		return []byte{byte(n)}, nil
	})
	if err := s.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	d := &staticDiscovery{addrs: map[NodeID]string{2: s.Listener().Addr().String()}}
	pool := NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := NewClient(pool)
	defer client.Stop()

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		resp, err := client.RPC(ctx, 2, 0, []byte("req"))
		if err != nil {
			t.Fatalf("RPC %d: %v", i, err)
		}
		if resp[0] != byte(i+1) {
			t.Fatalf("expected %d, got %d", i+1, resp[0])
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wktransport/ -run TestClient -v`
Expected: FAIL — `NewClient` not defined.

- [ ] **Step 3: Implement client.go**

```go
package wktransport

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// Client provides one-way Send and request/response RPC over a Pool.
type Client struct {
	pool      *Pool
	nextReqID atomic.Uint64
	pending   sync.Map // requestID → chan rpcResponse
	readLoops sync.Map // readLoopKey → net.Conn
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

type rpcResponse struct {
	body []byte
	err  error
}

// NewClient creates a Client bound to the given Pool.
func NewClient(pool *Pool) *Client {
	return &Client{
		pool:   pool,
		stopCh: make(chan struct{}),
	}
}

// Send writes a one-way message. No response expected.
func (c *Client) Send(nodeID NodeID, shardKey uint64, msgType uint8, body []byte) error {
	conn, idx, err := c.pool.Get(nodeID, shardKey)
	if err != nil {
		return err
	}
	err = WriteMessage(conn, msgType, body)
	if err != nil {
		c.pool.Reset(nodeID, idx)
	}
	c.pool.Release(nodeID, idx)
	return err
}

// RPC sends a request and waits for a response.
func (c *Client) RPC(ctx context.Context, nodeID NodeID, shardKey uint64, payload []byte) ([]byte, error) {
	reqID := c.nextReqID.Add(1)
	respCh := make(chan rpcResponse, 1)
	c.pending.Store(reqID, respCh)
	defer c.pending.Delete(reqID)

	conn, idx, err := c.pool.Get(nodeID, shardKey)
	if err != nil {
		return nil, err
	}

	c.ensureReadLoop(nodeID, idx, conn)

	reqBody := encodeRPCRequest(reqID, payload)
	err = WriteMessage(conn, MsgTypeRPCRequest, reqBody)
	if err != nil {
		c.pool.Reset(nodeID, idx)
	}
	c.pool.Release(nodeID, idx)
	if err != nil {
		return nil, err
	}

	select {
	case resp := <-respCh:
		if resp.err != nil {
			return nil, resp.err
		}
		return resp.body, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.stopCh:
		return nil, ErrStopped
	}
}

// Stop cancels all pending RPCs and waits for goroutines to exit.
func (c *Client) Stop() {
	close(c.stopCh)
	// Close tracked connections to unblock readLoops
	c.readLoops.Range(func(key, value any) bool {
		if conn, ok := value.(net.Conn); ok {
			_ = conn.Close()
		}
		return true
	})
	// Cancel pending RPCs
	c.pending.Range(func(key, value any) bool {
		ch := value.(chan rpcResponse)
		select {
		case ch <- rpcResponse{err: ErrStopped}:
		default:
		}
		return true
	})
	c.wg.Wait()
}

// readLoopKey produces a compact key for the readLoops sync.Map.
func readLoopKey(nodeID NodeID, idx int) uint64 {
	return nodeID<<32 | uint64(idx)
}

func (c *Client) ensureReadLoop(nodeID NodeID, idx int, conn net.Conn) {
	key := readLoopKey(nodeID, idx)
	for {
		existing, loaded := c.readLoops.LoadOrStore(key, conn)
		if !loaded {
			c.wg.Add(1)
			go c.readLoop(key, conn)
			return
		}
		if existing.(net.Conn) == conn {
			return
		}
		if c.readLoops.CompareAndSwap(key, existing, conn) {
			c.wg.Add(1)
			go c.readLoop(key, conn)
			return
		}
	}
}

func (c *Client) readLoop(key uint64, conn net.Conn) {
	defer c.wg.Done()
	defer c.readLoops.Delete(key)

	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		msgType, body, err := ReadMessage(conn)
		if err != nil {
			return
		}
		if msgType != MsgTypeRPCResponse {
			continue
		}

		if len(body) < 9 {
			continue
		}
		requestID := binary.BigEndian.Uint64(body[0:8])
		errCode := body[8]
		data := body[9:]

		if v, ok := c.pending.LoadAndDelete(requestID); ok {
			ch := v.(chan rpcResponse)
			var resp rpcResponse
			if errCode != 0 {
				resp.err = fmt.Errorf("wktransport: remote handler error: %s", data)
				resp.body = data
			} else {
				resp.body = data
			}
			ch <- resp
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wktransport/ -run TestClient -v -count=1`
Expected: all PASS.

- [ ] **Step 5: Run full wktransport test suite**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wktransport/ -v -count=1`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add wktransport/client.go wktransport/client_test.go
git commit -m "feat(wktransport): add Client with Send and RPC"
```

---

## Task 6: Refactor wkcluster discovery — uint64 signature change

**Files:**
- Modify: `wkcluster/discovery.go`
- Modify: `wkcluster/static_discovery.go`
- Modify: `wkcluster/static_discovery_test.go`

- [ ] **Step 1: Run existing tests to verify they pass before changes**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkcluster/ -run TestStaticDiscovery -v`
Expected: PASS.

- [ ] **Step 2: Update `wkcluster/discovery.go`**

Replace the entire file. Change `Resolve` to accept `uint64` and embed `wktransport.Discovery`:

```go
package wkcluster

// Discovery extends wktransport.Discovery with cluster-level concerns.
type Discovery interface {
	GetNodes() []NodeInfo
	Resolve(nodeID uint64) (string, error)
	Stop()
}
```

- [ ] **Step 3: Update `wkcluster/static_discovery.go`**

Change map key from `multiraft.NodeID` to `uint64`, change `Resolve` parameter to `uint64`:

```go
package wkcluster

type StaticDiscovery struct {
	nodes map[uint64]NodeInfo
}

func NewStaticDiscovery(configs []NodeConfig) *StaticDiscovery {
	nodes := make(map[uint64]NodeInfo, len(configs))
	for _, c := range configs {
		nodes[uint64(c.NodeID)] = NodeInfo{NodeID: c.NodeID, Addr: c.Addr}
	}
	return &StaticDiscovery{nodes: nodes}
}

func (s *StaticDiscovery) GetNodes() []NodeInfo {
	out := make([]NodeInfo, 0, len(s.nodes))
	for _, n := range s.nodes {
		out = append(out, n)
	}
	return out
}

func (s *StaticDiscovery) Resolve(nodeID uint64) (string, error) {
	n, ok := s.nodes[nodeID]
	if !ok {
		return "", ErrNodeNotFound
	}
	return n.Addr, nil
}

func (s *StaticDiscovery) Stop() {}
```

- [ ] **Step 4: Update `wkcluster/static_discovery_test.go`**

The test calls `d.Resolve(1)` and `d.Resolve(99)` — since `NodeConfig.NodeID` is `multiraft.NodeID` and `Resolve` now takes `uint64`, the integer literals `1` and `99` will be typed as `int` and auto-convert. No changes needed if compilation succeeds.

- [ ] **Step 5: Fix any compilation errors in other wkcluster files**

Run: `cd /Users/tt/Desktop/work/go/wraft && go build ./wkcluster/...`

There will be errors in `transport.go:190` where `t.discovery.Resolve(nodeID)` passes `multiraft.NodeID`. Add explicit cast: `t.discovery.Resolve(uint64(nodeID))`. Similarly in `static_discovery.go` the `NodeInfo.NodeID` field is `multiraft.NodeID` — no change needed since it's used as a value, not a map key.

Fix each call site. The places that call `Resolve` in `wkcluster/`:
- `wkcluster/transport.go:190` — `t.discovery.Resolve(nodeID)` where `nodeID` is `multiraft.NodeID`

- [ ] **Step 6: Run tests**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkcluster/ -run TestStaticDiscovery -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add wkcluster/discovery.go wkcluster/static_discovery.go wkcluster/static_discovery_test.go wkcluster/transport.go
git commit -m "refactor(wkcluster): change Discovery.Resolve to accept uint64"
```

---

## Task 7: Refactor wkcluster errors — remove network errors

**Files:**
- Modify: `wkcluster/errors.go`

- [ ] **Step 1: Update `wkcluster/errors.go`**

Remove `ErrTimeout`, `ErrNodeNotFound`, `ErrStopped`. Keep raft-semantic errors only:

```go
package wkcluster

import "errors"

var (
	ErrNoLeader        = errors.New("wkcluster: no leader for group")
	ErrNotLeader       = errors.New("wkcluster: not leader")
	ErrLeaderNotStable = errors.New("wkcluster: leader not stable after retries")
	ErrGroupNotFound   = errors.New("wkcluster: group not found")
	ErrInvalidConfig   = errors.New("wkcluster: invalid config")
)
```

- [ ] **Step 2: Fix compilation — update references to removed errors**

Run: `cd /Users/tt/Desktop/work/go/wraft && go build ./wkcluster/... 2>&1`

Fix each reference:
- `static_discovery.go` uses `ErrNodeNotFound` → change to `wktransport.ErrNodeNotFound` (add import)
- `forward.go` uses `ErrNodeNotFound`, `ErrTimeout`, `ErrStopped` → update to `wktransport.*`
- `cluster.go` uses `ErrStopped` → update to `wktransport.ErrStopped`

Add `"github.com/WuKongIM/WuKongIM/pkg/wktransport"` import where needed.

- [ ] **Step 3: Verify compilation and tests**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkcluster/ -run TestStaticDiscovery -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add wkcluster/errors.go wkcluster/static_discovery.go wkcluster/forward.go wkcluster/cluster.go
git commit -m "refactor(wkcluster): move network errors to wktransport"
```

---

## Task 8: Refactor wkcluster codec — keep only raft/forward body encoders

**Files:**
- Modify: `wkcluster/codec.go`
- Delete: `wkcluster/helpers_test.go`
- Modify: `wkcluster/codec_test.go`

- [ ] **Step 1: Rewrite `wkcluster/codec.go`**

Remove all frame-level functions (moved to wktransport). Promote encode helpers from `helpers_test.go` to production. Keep decode functions. Remove `bufPool`, `getBuf`, `putBuf`, `readMessage`, `writeRaftMessage`, `writeForwardMessage`, `writeRespMessage`.

New `wkcluster/codec.go`:

```go
package wkcluster

import (
	"encoding/binary"
	"fmt"
)

// Raft message types used when registering with wktransport.Server.
const (
	msgTypeRaft uint8 = 1
)

// Forward error codes (encoded within RPC payload, not wire-level).
const (
	errCodeOK        uint8 = 0
	errCodeNotLeader uint8 = 1
	errCodeTimeout   uint8 = 2
	errCodeNoGroup   uint8 = 3
)

// encodeRaftBody encodes [groupID:8][data:N].
func encodeRaftBody(groupID uint64, data []byte) []byte {
	buf := make([]byte, 8+len(data))
	binary.BigEndian.PutUint64(buf[0:8], groupID)
	copy(buf[8:], data)
	return buf
}

// decodeRaftBody decodes [groupID:8][data:N].
func decodeRaftBody(body []byte) (groupID uint64, data []byte, err error) {
	if len(body) < 8 {
		return 0, nil, fmt.Errorf("raft body too short: %d", len(body))
	}
	groupID = binary.BigEndian.Uint64(body[0:8])
	data = body[8:]
	return groupID, data, nil
}

// encodeForwardPayload encodes [groupID:8][cmd:N] for RPC payload.
func encodeForwardPayload(groupID uint64, cmd []byte) []byte {
	buf := make([]byte, 8+len(cmd))
	binary.BigEndian.PutUint64(buf[0:8], groupID)
	copy(buf[8:], cmd)
	return buf
}

// decodeForwardPayload decodes [groupID:8][cmd:N] from RPC payload.
func decodeForwardPayload(payload []byte) (groupID uint64, cmd []byte, err error) {
	if len(payload) < 8 {
		return 0, nil, fmt.Errorf("forward payload too short: %d", len(payload))
	}
	groupID = binary.BigEndian.Uint64(payload[0:8])
	cmd = payload[8:]
	return groupID, cmd, nil
}

// encodeForwardResp encodes [errCode:1][data:N] for RPC response payload.
func encodeForwardResp(errCode uint8, data []byte) []byte {
	buf := make([]byte, 1+len(data))
	buf[0] = errCode
	copy(buf[1:], data)
	return buf
}

// decodeForwardResp decodes [errCode:1][data:N] from RPC response payload.
func decodeForwardResp(payload []byte) (errCode uint8, data []byte, err error) {
	if len(payload) < 1 {
		return 0, nil, fmt.Errorf("forward resp too short")
	}
	return payload[0], payload[1:], nil
}
```

- [ ] **Step 2: Delete `wkcluster/helpers_test.go`**

The encode functions are now in production codec.go.

```bash
rm wkcluster/helpers_test.go
```

- [ ] **Step 3: Update `wkcluster/codec_test.go`**

Remove `TestEncodeDecodeMessage` and `TestMessageTooLarge` (frame-level, moved to wktransport). Keep body-level tests, adapted to new function names:

```go
package wkcluster

import (
	"bytes"
	"testing"
)

func TestRaftBodyRoundTrip(t *testing.T) {
	data := []byte("raft-data")
	body := encodeRaftBody(7, data)
	groupID, decoded, err := decodeRaftBody(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if groupID != 7 || !bytes.Equal(decoded, data) {
		t.Fatalf("mismatch: groupID=%d", groupID)
	}
}

func TestForwardPayloadRoundTrip(t *testing.T) {
	cmd := []byte("test-command")
	payload := encodeForwardPayload(7, cmd)
	groupID, decoded, err := decodeForwardPayload(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if groupID != 7 || !bytes.Equal(decoded, cmd) {
		t.Fatalf("mismatch: groupID=%d", groupID)
	}
}

func TestForwardRespRoundTrip(t *testing.T) {
	data := []byte("result")
	resp := encodeForwardResp(errCodeOK, data)
	code, decoded, err := decodeForwardResp(resp)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code != errCodeOK || !bytes.Equal(decoded, data) {
		t.Fatalf("mismatch: code=%d", code)
	}
}
```

- [ ] **Step 4: Rewrite `wkcluster/transport.go`** (must be done atomically with codec to keep codebase compilable)

```go
package wkcluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wktransport"
)

// raftTransport adapts wktransport.Client to multiraft.Transport.
type raftTransport struct {
	client *wktransport.Client
}

func (t *raftTransport) Send(ctx context.Context, batch []multiraft.Envelope) error {
	for _, env := range batch {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		data, err := env.Message.Marshal()
		if err != nil {
			return err
		}
		body := encodeRaftBody(uint64(env.GroupID), data)
		// Individual send failures are silently skipped — the raft layer
		// handles retransmission. Only context cancellation is propagated.
		_ = t.client.Send(uint64(env.Message.To), uint64(env.GroupID), msgTypeRaft, body)
	}
	return nil
}
```

- [ ] **Step 5: Rewrite `wkcluster/transport_test.go`**

```go
package wkcluster

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wktransport"
	"go.etcd.io/raft/v3/raftpb"
)

func TestRaftTransport_Send(t *testing.T) {
	// Start a server that captures raft messages
	srv := wktransport.NewServer()
	var receivedBody []byte
	done := make(chan struct{})
	srv.Handle(msgTypeRaft, func(_ net.Conn, body []byte) {
		receivedBody = body
		close(done)
	})
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	d := NewStaticDiscovery([]NodeConfig{{NodeID: 2, Addr: srv.Listener().Addr().String()}})
	pool := wktransport.NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := wktransport.NewClient(pool)
	defer client.Stop()

	rt := &raftTransport{client: client}

	msg := raftpb.Message{To: 2, From: 1, Type: raftpb.MsgHeartbeat}
	err := rt.Send(context.Background(), []multiraft.Envelope{
		{GroupID: 1, Message: msg},
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}

	// Verify body is a valid raft body
	groupID, data, err := decodeRaftBody(receivedBody)
	if err != nil {
		t.Fatal(err)
	}
	if groupID != 1 {
		t.Fatalf("expected groupID=1, got %d", groupID)
	}
	var decoded raftpb.Message
	if err := decoded.Unmarshal(data); err != nil {
		t.Fatal(err)
	}
	if decoded.Type != raftpb.MsgHeartbeat {
		t.Fatalf("expected MsgHeartbeat, got %v", decoded.Type)
	}
}

func TestRaftTransport_CtxCancel(t *testing.T) {
	d := NewStaticDiscovery([]NodeConfig{})
	pool := wktransport.NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := wktransport.NewClient(pool)
	defer client.Stop()

	rt := &raftTransport{client: client}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := rt.Send(ctx, []multiraft.Envelope{
		{GroupID: 1, Message: raftpb.Message{To: 2, From: 1}},
	})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}
```

- [ ] **Step 6: Rewrite `wkcluster/forward.go`**

```go
package wkcluster

import (
	"context"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/wktransport"
)

func (c *Cluster) forwardToLeader(ctx context.Context, leaderID multiraft.NodeID, groupID multiraft.GroupID, cmd []byte) error {
	payload := encodeForwardPayload(uint64(groupID), cmd)
	resp, err := c.fwdClient.RPC(ctx, uint64(leaderID), uint64(groupID), payload)
	if err != nil {
		return err
	}
	errCode, _, decodeErr := decodeForwardResp(resp)
	if decodeErr != nil {
		return fmt.Errorf("decode forward response: %w", decodeErr)
	}
	switch errCode {
	case errCodeOK:
		return nil
	case errCodeNotLeader:
		return ErrNotLeader
	case errCodeTimeout:
		return wktransport.ErrTimeout
	case errCodeNoGroup:
		return ErrGroupNotFound
	default:
		return fmt.Errorf("unknown forward error code: %d", errCode)
	}
}

// handleForwardRPC is the server-side RPC handler for forwarded proposals.
func (c *Cluster) handleForwardRPC(ctx context.Context, body []byte) ([]byte, error) {
	groupID, cmd, err := decodeForwardPayload(body)
	if err != nil {
		return encodeForwardResp(errCodeNoGroup, nil), nil
	}
	if c.stopped.Load() {
		return encodeForwardResp(errCodeTimeout, nil), nil
	}
	_, err = c.runtime.Status(multiraft.GroupID(groupID))
	if err != nil {
		return encodeForwardResp(errCodeNoGroup, nil), nil
	}
	future, err := c.runtime.Propose(ctx, multiraft.GroupID(groupID), cmd)
	if err != nil {
		return encodeForwardResp(errCodeNotLeader, nil), nil
	}
	result, err := future.Wait(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return encodeForwardResp(errCodeTimeout, nil), nil
		}
		return encodeForwardResp(errCodeNotLeader, nil), nil
	}
	return encodeForwardResp(errCodeOK, result.Data), nil
}
```

- [ ] **Step 7: Rewrite `wkcluster/forward_test.go`**

```go
package wkcluster

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wktransport"
)

func TestForwardToLeader_RoundTrip(t *testing.T) {
	// Server echoes the forward payload back with errCodeOK
	srv := wktransport.NewServer()
	srv.HandleRPC(func(ctx context.Context, body []byte) ([]byte, error) {
		groupID, cmd, err := decodeForwardPayload(body)
		if err != nil {
			return nil, err
		}
		_ = groupID
		return encodeForwardResp(errCodeOK, cmd), nil
	})
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	d := NewStaticDiscovery([]NodeConfig{{NodeID: 2, Addr: srv.Listener().Addr().String()}})
	pool := wktransport.NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := wktransport.NewClient(pool)
	defer client.Stop()

	c := &Cluster{fwdClient: client}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := c.forwardToLeader(ctx, 2, 1, []byte("test-cmd"))
	if err != nil {
		t.Fatalf("forwardToLeader: %v", err)
	}
}

func TestForwardToLeader_NotLeader(t *testing.T) {
	srv := wktransport.NewServer()
	srv.HandleRPC(func(ctx context.Context, body []byte) ([]byte, error) {
		return encodeForwardResp(errCodeNotLeader, nil), nil
	})
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()

	d := NewStaticDiscovery([]NodeConfig{{NodeID: 2, Addr: srv.Listener().Addr().String()}})
	pool := wktransport.NewPool(d, 2, 5*time.Second)
	defer pool.Close()
	client := wktransport.NewClient(pool)
	defer client.Stop()

	c := &Cluster{fwdClient: client}

	ctx := context.Background()
	err := c.forwardToLeader(ctx, 2, 1, []byte("test"))
	if err != ErrNotLeader {
		t.Fatalf("expected ErrNotLeader, got: %v", err)
	}
}
```

- [ ] **Step 8: Verify full compilation**

Run: `cd /Users/tt/Desktop/work/go/wraft && go build ./wkcluster/...`
Expected: success (no compilation errors — all three files rewritten atomically).

- [ ] **Step 9: Run codec and transport and forward tests**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkcluster/ -run "TestRaftBody|TestForwardPayload|TestForwardResp|TestRaftTransport|TestForwardToLeader" -v -count=1`
Expected: all PASS.

- [ ] **Step 10: Commit (all three files together to keep codebase compilable)**

```bash
git add wkcluster/codec.go wkcluster/codec_test.go wkcluster/transport.go wkcluster/transport_test.go wkcluster/forward.go wkcluster/forward_test.go
git rm wkcluster/helpers_test.go
git commit -m "refactor(wkcluster): slim codec + raftTransport adapter + forwardToLeader via wktransport.RPC"
```

---

## Task 9: Rewrite wkcluster cluster.go — compose wktransport components

**Files:**
- Rewrite: `wkcluster/cluster.go`
- Modify: `wkcluster/api.go` (update `proposeOrForward` to use `forwardToLeader`)
- Modify: `wkcluster/config.go` (rename `PoolSize` → `RaftPoolSize` if desired, or keep)

- [ ] **Step 1: Rewrite `wkcluster/cluster.go`**

```go
package wkcluster

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/controller/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controller/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkfsm"
	"github.com/WuKongIM/WuKongIM/pkg/wktransport"
	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

type Cluster struct {
	cfg        Config
	server     *wktransport.Server
	raftPool   *wktransport.Pool
	raftClient *wktransport.Client
	fwdClient  *wktransport.Client
	runtime    *multiraft.Runtime
	router     *Router
	discovery  *StaticDiscovery
	db         *wkdb.DB
	raftDB     *raftstore.DB
	stopped    atomic.Bool
}

func NewCluster(cfg Config) (*Cluster, error) {
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Cluster{cfg: cfg}, nil
}

func (c *Cluster) Start() error {
	// 1. Open databases
	var err error
	c.db, err = wkdb.Open(c.cfg.dataDir())
	if err != nil {
		return fmt.Errorf("open wkdb: %w", err)
	}
	c.raftDB, err = raftstore.Open(c.cfg.RaftDataDir)
	if err != nil {
		_ = c.db.Close()
		return fmt.Errorf("open raftstore: %w", err)
	}

	// 2. Discovery
	c.discovery = NewStaticDiscovery(c.cfg.Nodes)

	// 3. Server
	c.server = wktransport.NewServer()
	c.server.Handle(msgTypeRaft, c.handleRaftMessage)
	c.server.HandleRPC(c.handleForwardRPC)
	if err := c.server.Start(c.cfg.ListenAddr); err != nil {
		_ = c.raftDB.Close()
		_ = c.db.Close()
		return fmt.Errorf("start server: %w", err)
	}

	// 4. Pools + Clients
	c.raftPool = wktransport.NewPool(c.discovery, c.cfg.PoolSize, c.cfg.DialTimeout)
	c.raftClient = wktransport.NewClient(c.raftPool)
	c.fwdClient = wktransport.NewClient(c.raftPool)

	// 5. Runtime
	c.runtime, err = multiraft.New(multiraft.Options{
		NodeID:       c.cfg.NodeID,
		TickInterval: c.cfg.TickInterval,
		Workers:      c.cfg.RaftWorkers,
		Transport:    &raftTransport{client: c.raftClient},
		Raft: multiraft.RaftOptions{
			ElectionTick:  c.cfg.ElectionTick,
			HeartbeatTick: c.cfg.HeartbeatTick,
		},
	})
	if err != nil {
		c.fwdClient.Stop()
		c.raftClient.Stop()
		c.raftPool.Close()
		c.server.Stop()
		_ = c.raftDB.Close()
		_ = c.db.Close()
		return fmt.Errorf("create runtime: %w", err)
	}

	// 6. Router
	c.router = NewRouter(c.cfg.GroupCount, c.cfg.NodeID, c.runtime)

	// 7. Open groups
	ctx := context.Background()
	for _, g := range c.cfg.Groups {
		if err := c.openOrBootstrapGroup(ctx, g); err != nil {
			c.Stop()
			return fmt.Errorf("open group %d: %w", g.GroupID, err)
		}
	}

	return nil
}

func (c *Cluster) openOrBootstrapGroup(ctx context.Context, g GroupConfig) error {
	storage := c.raftDB.ForGroup(uint64(g.GroupID))
	sm, err := wkfsm.NewStateMachine(c.db, uint64(g.GroupID))
	if err != nil {
		return err
	}
	opts := multiraft.GroupOptions{
		ID:           g.GroupID,
		Storage:      storage,
		StateMachine: sm,
	}

	initialState, err := storage.InitialState(ctx)
	if err != nil {
		return err
	}
	if !raft.IsEmptyHardState(initialState.HardState) {
		return c.runtime.OpenGroup(ctx, opts)
	}
	return c.runtime.BootstrapGroup(ctx, multiraft.BootstrapGroupRequest{
		Group:  opts,
		Voters: g.Peers,
	})
}

func (c *Cluster) Stop() {
	c.stopped.Store(true)

	if c.fwdClient != nil {
		c.fwdClient.Stop()
	}
	if c.raftClient != nil {
		c.raftClient.Stop()
	}
	if c.runtime != nil {
		_ = c.runtime.Close()
	}
	if c.server != nil {
		c.server.Stop()
	}
	if c.raftPool != nil {
		c.raftPool.Close()
	}
	if c.raftDB != nil {
		_ = c.raftDB.Close()
	}
	if c.db != nil {
		_ = c.db.Close()
	}
}

// handleRaftMessage is the server handler for msgTypeRaft.
func (c *Cluster) handleRaftMessage(_ net.Conn, body []byte) {
	if c.runtime == nil {
		return
	}
	groupID, data, err := decodeRaftBody(body)
	if err != nil {
		return
	}
	var msg raftpb.Message
	if err := msg.Unmarshal(data); err != nil {
		return
	}
	_ = c.runtime.Step(context.Background(), multiraft.Envelope{
		GroupID: multiraft.GroupID(groupID),
		Message: msg,
	})
}

// Server returns the underlying wktransport.Server, allowing business layer
// to register additional handlers on the shared listener.
func (c *Cluster) Server() *wktransport.Server {
	return c.server
}

// Discovery returns the cluster's Discovery instance for creating business pools.
func (c *Cluster) Discovery() Discovery {
	return c.discovery
}
```

- [ ] **Step 2: Update `wkcluster/api.go`** — change `c.forwarder.Forward` to `c.forwardToLeader`

Replace `proposeOrForward` to call `forwardToLeader` instead of `c.forwarder.Forward`:

```go
func (c *Cluster) proposeOrForward(ctx context.Context, groupID multiraft.GroupID, cmd []byte) error {
	if c.stopped.Load() {
		return wktransport.ErrStopped
	}
	const maxRetries = 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt) * 50 * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		leaderID, err := c.router.LeaderOf(groupID)
		if err != nil {
			return err
		}
		if c.router.IsLocal(leaderID) {
			future, err := c.runtime.Propose(ctx, groupID, cmd)
			if err != nil {
				return err
			}
			_, err = future.Wait(ctx)
			return err
		}
		err = c.forwardToLeader(ctx, leaderID, groupID, cmd)
		if errors.Is(err, ErrNotLeader) {
			continue
		}
		return err
	}
	return ErrLeaderNotStable
}
```

Add imports: `"errors"`, `"time"`, `"github.com/WuKongIM/WuKongIM/pkg/wktransport"`.

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/tt/Desktop/work/go/wraft && go build ./wkcluster/...`
Expected: success.

- [ ] **Step 4: Run all wkcluster tests**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkcluster/ -v -count=1 -timeout 120s`
Expected: all PASS (including integration tests like `TestCluster_ThreeNode_ForwardToLeader`).

- [ ] **Step 5: Commit**

```bash
git add wkcluster/cluster.go wkcluster/api.go wkcluster/config.go
git commit -m "refactor(wkcluster): compose wktransport Server/Pool/Client"
```

---

## Task 10: Final verification — full test suite

**Files:** None (verification only)

- [ ] **Step 1: Run wktransport tests**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wktransport/ -v -count=1`
Expected: all PASS.

- [ ] **Step 2: Run wkcluster tests**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkcluster/ -v -count=1 -timeout 120s`
Expected: all PASS.

- [ ] **Step 3: Run entire project**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./... -count=1 -timeout 300s`
Expected: all PASS.

- [ ] **Step 4: Run go mod tidy**

Run: `cd /Users/tt/Desktop/work/go/wraft && go mod tidy`
Expected: no changes (wktransport has no external deps), but ensures module is clean.

- [ ] **Step 5: Verify no unused imports or dead code**

Run: `cd /Users/tt/Desktop/work/go/wraft && go vet ./wktransport/... ./wkcluster/...`
Expected: no issues.

- [ ] **Step 6: Final commit if any fixups needed**

```bash
git add -A
git commit -m "chore: fixups from final verification"
```
