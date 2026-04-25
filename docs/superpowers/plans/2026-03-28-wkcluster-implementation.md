# wkcluster Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the `wkcluster/` package that coordinates multiraft, raftstore, wkfsm, and wkdb into a functioning distributed cluster with TCP transport, hash-based routing, and leader forwarding for Channel CRUD.

**Architecture:** Single package `wkcluster/` sits on top of all existing packages. TCP connections pooled per node-pair with group-based selection for ordering. Static discovery with interface for future extensibility. Server-side forwarding with requestID correlation.

**Tech Stack:** Go, raw TCP, crc32 hashing, pebble (via raftstore + wkdb), etcd/raft (via multiraft)

---

## File Structure

| File | Responsibility |
|------|---------------|
| Create: `wkcluster/config.go` | Config, NodeConfig, GroupConfig types + validation |
| Create: `wkcluster/discovery.go` | Discovery interface, NodeInfo, NodeEvent types |
| Create: `wkcluster/static_discovery.go` | StaticDiscovery implementation |
| Create: `wkcluster/codec.go` | Wire format encode/decode for TCP messages |
| Create: `wkcluster/transport.go` | TCP Transport implementing multiraft.Transport + connPool |
| Create: `wkcluster/router.go` | Router: channelID → groupID → leader routing |
| Create: `wkcluster/forward.go` | Forwarder: requestID correlation, leader forwarding |
| Create: `wkcluster/cluster.go` | Cluster: main struct, Start/Stop lifecycle |
| Create: `wkcluster/api.go` | Public API: CreateChannel, GetChannel, etc. |
| Create: `wkcluster/errors.go` | Package error variables |
| Modify: `wkfsm/command.go` | Add cmdTypeDeleteChannel + encoder/decoder |
| Modify: `wkdb/batch.go` | Add WriteBatch.DeleteChannel method |
| Create: `wkcluster/config_test.go` | Config validation tests |
| Create: `wkcluster/static_discovery_test.go` | StaticDiscovery tests |
| Create: `wkcluster/codec_test.go` | Wire format round-trip tests |
| Create: `wkcluster/router_test.go` | Router hash + leader lookup tests |
| Create: `wkcluster/transport_test.go` | TCP transport send/receive tests |
| Create: `wkcluster/forward_test.go` | Forwarder correlation tests |
| Create: `wkcluster/cluster_test.go` | Integration: multi-node cluster tests |

---

## Task 1: Prerequisites — DeleteChannel Command

**Files:**
- Modify: `wkdb/batch.go:40-57` (add DeleteChannel after UpsertChannel)
- Modify: `wkfsm/command.go:21-63` (add cmdType + encoder/decoder)
- Test: `wkfsm/state_machine_test.go` (add delete test)

- [ ] **Step 1: Add WriteBatch.DeleteChannel to wkdb**

In `wkdb/batch.go`, add after the `UpsertChannel` method (line 57):

```go
func (b *WriteBatch) DeleteChannel(slot uint64, channelID string, channelType int64) error {
	primaryKey := encodeChannelPrimaryKey(slot, channelID, channelType, channelPrimaryFamilyID)
	if err := b.batch.Delete(primaryKey, nil); err != nil {
		return err
	}
	indexKey := encodeChannelIDIndexKey(slot, channelID, channelType)
	return b.batch.Delete(indexKey, nil)
}
```

- [ ] **Step 2: Add cmdTypeDeleteChannel to wkfsm/command.go**

At `wkfsm/command.go` line 23, add the new constant:

```go
cmdTypeDeleteChannel uint8 = 3
```

Add the deleteChannelCmd struct (after upsertChannelCmd, around line 83):

```go
type deleteChannelCmd struct {
	channelID   string
	channelType int64
}

func (c *deleteChannelCmd) apply(wb *wkdb.WriteBatch, slot uint64) error {
	return wb.DeleteChannel(slot, c.channelID, c.channelType)
}
```

Add the encoder function (after EncodeUpsertChannelCommand):

```go
func EncodeDeleteChannelCommand(channelID string, channelType int64) []byte {
	size := headerSize +
		tlvOverhead + len(channelID) +
		tlvOverhead + 8
	buf := make([]byte, size)
	buf[0] = commandVersion
	buf[1] = cmdTypeDeleteChannel
	off := headerSize
	off = putStringField(buf, off, tagChannelID, channelID)
	putInt64Field(buf, off, tagChannelType, channelType)
	return buf
}
```

Add the decoder function:

```go
func decodeDeleteChannel(data []byte) (command, error) {
	var cmd deleteChannelCmd
	for len(data) > 0 {
		tag, value, rest, err := readTLV(data)
		if err != nil {
			return nil, err
		}
		switch tag {
		case tagChannelID:
			cmd.channelID = string(value)
		case tagChannelType:
			if len(value) != 8 {
				return nil, fmt.Errorf("invalid channel type length: %d", len(value))
			}
			cmd.channelType = int64(binary.BigEndian.Uint64(value))
		}
		data = rest
	}
	return &cmd, nil
}
```

Register in `commandDecoders` map (around line 62):

```go
cmdTypeDeleteChannel: decodeDeleteChannel,
```

- [ ] **Step 3: Write test for DeleteChannel command**

In `wkfsm/state_machine_test.go`, add:

```go
func TestApplyBatch_DeleteChannel(t *testing.T) {
	db := openTestDB(t)
	sm := mustNewStateMachine(t, db, 1)
	ctx := context.Background()

	// First create a channel via upsert
	createCmd := EncodeUpsertChannelCommand(wkdb.Channel{
		ChannelID: "ch1", ChannelType: 1, Ban: 0,
	})
	_, err := sm.Apply(ctx, multiraft.Command{GroupID: 1, Data: createCmd})
	if err != nil {
		t.Fatalf("Apply upsert: %v", err)
	}

	// Verify channel exists
	ch, err := db.ForSlot(1).GetChannel(ctx, "ch1", 1)
	if err != nil {
		t.Fatalf("GetChannel after create: %v", err)
	}
	if ch.ChannelID != "ch1" {
		t.Fatalf("unexpected channel ID: %s", ch.ChannelID)
	}

	// Delete the channel
	deleteCmd := EncodeDeleteChannelCommand("ch1", 1)
	_, err = sm.Apply(ctx, multiraft.Command{GroupID: 1, Data: deleteCmd})
	if err != nil {
		t.Fatalf("Apply delete: %v", err)
	}

	// Verify channel is gone
	_, err = db.ForSlot(1).GetChannel(ctx, "ch1", 1)
	if err != wkdb.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkfsm/ -run TestApplyBatch_DeleteChannel -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wkdb/batch.go wkfsm/command.go wkfsm/state_machine_test.go
git commit -m "feat: add DeleteChannel command to wkfsm and wkdb"
```

---

## Task 2: Config and Errors

**Files:**
- Create: `wkcluster/config.go`
- Create: `wkcluster/errors.go`
- Create: `wkcluster/config_test.go`

- [ ] **Step 1: Create errors.go**

```go
package wkcluster

import "errors"

var (
	ErrNoLeader       = errors.New("wkcluster: no leader for group")
	ErrNotLeader      = errors.New("wkcluster: not leader")
	ErrLeaderNotStable = errors.New("wkcluster: leader not stable after retries")
	ErrGroupNotFound  = errors.New("wkcluster: group not found")
	ErrTimeout        = errors.New("wkcluster: forward timeout")
	ErrNodeNotFound   = errors.New("wkcluster: node not found")
	ErrInvalidConfig  = errors.New("wkcluster: invalid config")
	ErrStopped        = errors.New("wkcluster: cluster stopped")
)
```

- [ ] **Step 2: Create config.go**

```go
package wkcluster

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft"
)

const (
	defaultForwardTimeout = 5 * time.Second
	defaultPoolSize       = 4
)

type Config struct {
	NodeID         multiraft.NodeID
	ListenAddr     string
	GroupCount     uint32
	DataDir        string
	RaftDataDir    string
	Nodes          []NodeConfig
	Groups         []GroupConfig
	ForwardTimeout time.Duration
	PoolSize       int
}

type NodeConfig struct {
	NodeID multiraft.NodeID
	Addr   string
}

type GroupConfig struct {
	GroupID multiraft.GroupID
	Peers   []multiraft.NodeID
}

func (c *Config) validate() error {
	if c.GroupCount == 0 {
		return fmt.Errorf("%w: GroupCount must be > 0", ErrInvalidConfig)
	}
	if uint32(len(c.Groups)) != c.GroupCount {
		return fmt.Errorf("%w: len(Groups)=%d != GroupCount=%d", ErrInvalidConfig, len(c.Groups), c.GroupCount)
	}

	nodeSet := make(map[multiraft.NodeID]bool, len(c.Nodes))
	for _, n := range c.Nodes {
		nodeSet[n.NodeID] = true
	}

	selfFound := false
	for _, g := range c.Groups {
		for _, peer := range g.Peers {
			if !nodeSet[peer] {
				return fmt.Errorf("%w: peer %d in group %d not found in Nodes", ErrInvalidConfig, peer, g.GroupID)
			}
			if peer == c.NodeID {
				selfFound = true
			}
		}
	}
	if !selfFound {
		return fmt.Errorf("%w: NodeID %d not found as peer in any group", ErrInvalidConfig, c.NodeID)
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.ForwardTimeout == 0 {
		c.ForwardTimeout = defaultForwardTimeout
	}
	if c.PoolSize == 0 {
		c.PoolSize = defaultPoolSize
	}
	if c.RaftDataDir == "" {
		c.RaftDataDir = filepath.Join(c.DataDir, "raft")
	}
}

func (c *Config) dataDir() string {
	return filepath.Join(c.DataDir, "data")
}
```

- [ ] **Step 3: Write config validation tests**

```go
package wkcluster

import (
	"errors"
	"testing"
)

func TestConfigValidate_Valid(t *testing.T) {
	cfg := validTestConfig()
	if err := cfg.validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigValidate_GroupCountZero(t *testing.T) {
	cfg := validTestConfig()
	cfg.GroupCount = 0
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidate_GroupCountMismatch(t *testing.T) {
	cfg := validTestConfig()
	cfg.GroupCount = 5
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidate_PeerNotInNodes(t *testing.T) {
	cfg := validTestConfig()
	cfg.Groups[0].Peers = append(cfg.Groups[0].Peers, 99)
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigValidate_SelfNotPeer(t *testing.T) {
	cfg := validTestConfig()
	cfg.NodeID = 99
	cfg.Nodes = append(cfg.Nodes, NodeConfig{NodeID: 99, Addr: ":9999"})
	if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}
}

func TestConfigApplyDefaults(t *testing.T) {
	cfg := validTestConfig()
	cfg.applyDefaults()
	if cfg.ForwardTimeout != defaultForwardTimeout {
		t.Fatalf("expected default ForwardTimeout")
	}
	if cfg.PoolSize != defaultPoolSize {
		t.Fatalf("expected default PoolSize")
	}
	if cfg.RaftDataDir == "" {
		t.Fatalf("expected RaftDataDir to be set")
	}
}

func validTestConfig() Config {
	return Config{
		NodeID:     1,
		ListenAddr: ":9001",
		GroupCount: 1,
		DataDir:    "/tmp/test",
		Nodes: []NodeConfig{
			{NodeID: 1, Addr: "127.0.0.1:9001"},
			{NodeID: 2, Addr: "127.0.0.1:9002"},
			{NodeID: 3, Addr: "127.0.0.1:9003"},
		},
		Groups: []GroupConfig{
			{GroupID: 1, Peers: []multiraft.NodeID{1, 2, 3}},
		},
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkcluster/ -run TestConfig -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wkcluster/
git commit -m "feat(wkcluster): add config types and validation"
```

---

## Task 3: Discovery Interface and Static Implementation

**Files:**
- Create: `wkcluster/discovery.go`
- Create: `wkcluster/static_discovery.go`
- Create: `wkcluster/static_discovery_test.go`

- [ ] **Step 1: Create discovery.go**

```go
package wkcluster

import "github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft"

type NodeInfo struct {
	NodeID multiraft.NodeID
	Addr   string
}

type NodeEvent struct {
	Type string // "join" | "leave"
	Node NodeInfo
}

type Discovery interface {
	GetNodes() []NodeInfo
	Resolve(nodeID multiraft.NodeID) (string, error)
	Watch() <-chan NodeEvent
	Stop()
}
```

- [ ] **Step 2: Create static_discovery.go**

```go
package wkcluster

import "github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft"

type StaticDiscovery struct {
	nodes map[multiraft.NodeID]NodeInfo
}

func NewStaticDiscovery(configs []NodeConfig) *StaticDiscovery {
	nodes := make(map[multiraft.NodeID]NodeInfo, len(configs))
	for _, c := range configs {
		nodes[c.NodeID] = NodeInfo{NodeID: c.NodeID, Addr: c.Addr}
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

func (s *StaticDiscovery) Resolve(nodeID multiraft.NodeID) (string, error) {
	n, ok := s.nodes[nodeID]
	if !ok {
		return "", ErrNodeNotFound
	}
	return n.Addr, nil
}

func (s *StaticDiscovery) Watch() <-chan NodeEvent { return nil }

func (s *StaticDiscovery) Stop() {}
```

- [ ] **Step 3: Write tests**

```go
package wkcluster

import (
	"errors"
	"testing"
)

func TestStaticDiscovery_Resolve(t *testing.T) {
	d := NewStaticDiscovery([]NodeConfig{
		{NodeID: 1, Addr: "10.0.0.1:9001"},
		{NodeID: 2, Addr: "10.0.0.2:9001"},
	})

	addr, err := d.Resolve(1)
	if err != nil {
		t.Fatalf("Resolve(1): %v", err)
	}
	if addr != "10.0.0.1:9001" {
		t.Fatalf("expected 10.0.0.1:9001, got %s", addr)
	}

	_, err = d.Resolve(99)
	if !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("expected ErrNodeNotFound, got: %v", err)
	}
}

func TestStaticDiscovery_GetNodes(t *testing.T) {
	d := NewStaticDiscovery([]NodeConfig{
		{NodeID: 1, Addr: "a"},
		{NodeID: 2, Addr: "b"},
	})
	nodes := d.GetNodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
}

func TestStaticDiscovery_WatchReturnsNil(t *testing.T) {
	d := NewStaticDiscovery(nil)
	if d.Watch() != nil {
		t.Fatal("expected nil channel")
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkcluster/ -run TestStaticDiscovery -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add wkcluster/
git commit -m "feat(wkcluster): add Discovery interface and StaticDiscovery"
```

---

## Task 4: Wire Format Codec

**Files:**
- Create: `wkcluster/codec.go`
- Create: `wkcluster/codec_test.go`

- [ ] **Step 1: Create codec.go**

```go
package wkcluster

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	msgTypeRaft    uint8 = 1
	msgTypeForward uint8 = 2
	msgTypeResp    uint8 = 3

	msgHeaderSize = 5 // [msgType:1][bodyLen:4]
)

// Error codes for forward responses.
const (
	errCodeOK       uint8 = 0
	errCodeNotLeader uint8 = 1
	errCodeTimeout  uint8 = 2
	errCodeNoGroup  uint8 = 3
)

func encodeMessage(msgType uint8, body []byte) []byte {
	buf := make([]byte, msgHeaderSize+len(body))
	buf[0] = msgType
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(body)))
	copy(buf[5:], body)
	return buf
}

func readMessage(r io.Reader) (msgType uint8, body []byte, err error) {
	hdr := make([]byte, msgHeaderSize)
	if _, err = io.ReadFull(r, hdr); err != nil {
		return 0, nil, err
	}
	msgType = hdr[0]
	bodyLen := binary.BigEndian.Uint32(hdr[1:5])
	if bodyLen > 64<<20 { // 64 MB sanity limit
		return 0, nil, fmt.Errorf("message too large: %d bytes", bodyLen)
	}
	body = make([]byte, bodyLen)
	if _, err = io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return msgType, body, nil
}

// encodeRaftBody: [groupID:8][data:N]
func encodeRaftBody(groupID uint64, data []byte) []byte {
	buf := make([]byte, 8+len(data))
	binary.BigEndian.PutUint64(buf[0:8], groupID)
	copy(buf[8:], data)
	return buf
}

func decodeRaftBody(body []byte) (groupID uint64, data []byte, err error) {
	if len(body) < 8 {
		return 0, nil, fmt.Errorf("raft body too short: %d", len(body))
	}
	groupID = binary.BigEndian.Uint64(body[0:8])
	data = body[8:]
	return groupID, data, nil
}

// encodeForwardBody: [requestID:8][groupID:8][cmd:N]
func encodeForwardBody(requestID uint64, groupID uint64, cmd []byte) []byte {
	buf := make([]byte, 16+len(cmd))
	binary.BigEndian.PutUint64(buf[0:8], requestID)
	binary.BigEndian.PutUint64(buf[8:16], groupID)
	copy(buf[16:], cmd)
	return buf
}

func decodeForwardBody(body []byte) (requestID uint64, groupID uint64, cmd []byte, err error) {
	if len(body) < 16 {
		return 0, 0, nil, fmt.Errorf("forward body too short: %d", len(body))
	}
	requestID = binary.BigEndian.Uint64(body[0:8])
	groupID = binary.BigEndian.Uint64(body[8:16])
	cmd = body[16:]
	return requestID, groupID, cmd, nil
}

// encodeRespBody: [requestID:8][errCode:1][data:N]
func encodeRespBody(requestID uint64, errCode uint8, data []byte) []byte {
	buf := make([]byte, 9+len(data))
	binary.BigEndian.PutUint64(buf[0:8], requestID)
	buf[8] = errCode
	copy(buf[9:], data)
	return buf
}

func decodeRespBody(body []byte) (requestID uint64, errCode uint8, data []byte, err error) {
	if len(body) < 9 {
		return 0, 0, nil, fmt.Errorf("resp body too short: %d", len(body))
	}
	requestID = binary.BigEndian.Uint64(body[0:8])
	errCode = body[8]
	data = body[9:]
	return requestID, errCode, data, nil
}
```

- [ ] **Step 2: Write round-trip tests**

```go
package wkcluster

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeMessage(t *testing.T) {
	body := []byte("hello")
	encoded := encodeMessage(msgTypeRaft, body)

	r := bytes.NewReader(encoded)
	msgType, decoded, err := readMessage(r)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if msgType != msgTypeRaft {
		t.Fatalf("expected msgTypeRaft, got %d", msgType)
	}
	if !bytes.Equal(decoded, body) {
		t.Fatalf("body mismatch")
	}
}

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

func TestForwardBodyRoundTrip(t *testing.T) {
	cmd := []byte("test-command")
	body := encodeForwardBody(42, 7, cmd)
	reqID, groupID, decoded, err := decodeForwardBody(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if reqID != 42 || groupID != 7 || !bytes.Equal(decoded, cmd) {
		t.Fatalf("mismatch: reqID=%d groupID=%d", reqID, groupID)
	}
}

func TestRespBodyRoundTrip(t *testing.T) {
	data := []byte("result")
	body := encodeRespBody(42, errCodeOK, data)
	reqID, code, decoded, err := decodeRespBody(body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if reqID != 42 || code != errCodeOK || !bytes.Equal(decoded, data) {
		t.Fatalf("mismatch: reqID=%d code=%d", reqID, code)
	}
}

func TestForwardBodyTooShort(t *testing.T) {
	_, _, _, err := decodeForwardBody([]byte{1, 2, 3})
	if err == nil {
		t.Fatal("expected error for short body")
	}
}

func TestMessageTooLarge(t *testing.T) {
	// Craft a header claiming 100MB body
	hdr := make([]byte, 5)
	hdr[0] = msgTypeRaft
	hdr[1] = 0x06 // 100MB > 64MB limit
	hdr[2] = 0x40
	hdr[3] = 0x00
	hdr[4] = 0x00

	r := bytes.NewReader(hdr)
	_, _, err := readMessage(r)
	if err == nil {
		t.Fatal("expected error for oversized message")
	}
}
```

- [ ] **Step 3: Run tests**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkcluster/ -run TestEncodeDecode -v && go test ./wkcluster/ -run TestForward -v && go test ./wkcluster/ -run TestResp -v && go test ./wkcluster/ -run TestMessage -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add wkcluster/
git commit -m "feat(wkcluster): add wire format codec"
```

---

## Task 5: Router

**Files:**
- Create: `wkcluster/router.go`
- Create: `wkcluster/router_test.go`

- [ ] **Step 1: Create router.go**

```go
package wkcluster

import (
	"hash/crc32"

	"github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft"
)

type Router struct {
	groupCount uint32
	runtime    *multiraft.Runtime
	localNode  multiraft.NodeID
}

func NewRouter(groupCount uint32, localNode multiraft.NodeID, runtime *multiraft.Runtime) *Router {
	return &Router{
		groupCount: groupCount,
		runtime:    runtime,
		localNode:  localNode,
	}
}

func (r *Router) SlotForChannel(channelID string) multiraft.GroupID {
	return multiraft.GroupID(crc32.ChecksumIEEE([]byte(channelID))%r.groupCount + 1)
}

func (r *Router) LeaderOf(groupID multiraft.GroupID) (multiraft.NodeID, error) {
	status, err := r.runtime.Status(groupID)
	if err != nil {
		return 0, err
	}
	if status.LeaderID == 0 {
		return 0, ErrNoLeader
	}
	return status.LeaderID, nil
}

func (r *Router) IsLocal(nodeID multiraft.NodeID) bool {
	return nodeID == r.localNode
}
```

- [ ] **Step 2: Write tests**

```go
package wkcluster

import (
	"fmt"
	"testing"
)

func TestSlotForChannel_Deterministic(t *testing.T) {
	r := &Router{groupCount: 3}
	a := r.SlotForChannel("test-channel")
	b := r.SlotForChannel("test-channel")
	if a != b {
		t.Fatalf("non-deterministic: %d != %d", a, b)
	}
}

func TestSlotForChannel_Range(t *testing.T) {
	r := &Router{groupCount: 10}
	for _, id := range []string{"a", "b", "c", "d", "e", "channel-123", "xyz"} {
		slot := r.SlotForChannel(id)
		if slot < 1 || slot > 10 {
			t.Fatalf("slot %d out of range [1,10] for channelID=%s", slot, id)
		}
	}
}

func TestSlotForChannel_Distribution(t *testing.T) {
	r := &Router{groupCount: 3}
	counts := make(map[uint64]int)
	for i := 0; i < 1000; i++ {
		slot := r.SlotForChannel(fmt.Sprintf("channel-%d", i))
		counts[uint64(slot)]++
	}
	// Each group should have at least some channels (rough check)
	for g := uint64(1); g <= 3; g++ {
		if counts[g] == 0 {
			t.Fatalf("group %d has 0 channels — bad distribution", g)
		}
	}
}

func TestIsLocal(t *testing.T) {
	r := &Router{localNode: 1}
	if !r.IsLocal(1) {
		t.Fatal("expected local")
	}
	if r.IsLocal(2) {
		t.Fatal("expected not local")
	}
}
```

Note: add `"fmt"` to the import in the test file.

- [ ] **Step 3: Run tests**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkcluster/ -run TestSlot -v && go test ./wkcluster/ -run TestIsLocal -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add wkcluster/
git commit -m "feat(wkcluster): add Router with hash-based channel routing"
```

---

## Task 6: TCP Transport

**Files:**
- Create: `wkcluster/transport.go`
- Create: `wkcluster/transport_test.go`

- [ ] **Step 1: Create transport.go**

```go
package wkcluster

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

type connPool struct {
	addr  string
	size  int
	conns []net.Conn
	mu    []sync.Mutex
}

func newConnPool(addr string, size int) *connPool {
	return &connPool{
		addr:  addr,
		size:  size,
		conns: make([]net.Conn, size),
		mu:    make([]sync.Mutex, size),
	}
}

func (p *connPool) getByGroup(groupID multiraft.GroupID) (net.Conn, int, error) {
	idx := int(uint64(groupID) % uint64(p.size))
	p.mu[idx].Lock()
	if p.conns[idx] == nil {
		conn, err := net.DialTimeout("tcp", p.addr, 5*time.Second)
		if err != nil {
			p.mu[idx].Unlock()
			return nil, idx, err
		}
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.SetKeepAlive(true)
			_ = tc.SetKeepAlivePeriod(30 * time.Second)
		}
		p.conns[idx] = conn
	}
	return p.conns[idx], idx, nil
}

func (p *connPool) release(idx int) {
	p.mu[idx].Unlock()
}

func (p *connPool) resetConn(idx int) {
	if p.conns[idx] != nil {
		_ = p.conns[idx].Close()
		p.conns[idx] = nil
	}
}

func (p *connPool) closeAll() {
	for i := range p.conns {
		p.mu[i].Lock()
		p.resetConn(i)
		p.mu[i].Unlock()
	}
}

type Transport struct {
	nodeID    multiraft.NodeID
	discovery Discovery
	poolSize  int
	pools     map[multiraft.NodeID]*connPool
	mu        sync.RWMutex
	runtime   *multiraft.Runtime
	listener  net.Listener
	handler   forwardHandler
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

// forwardHandler processes incoming forward requests on the leader side.
type forwardHandler interface {
	handleForward(ctx context.Context, groupID multiraft.GroupID, cmd []byte) ([]byte, uint8)
}

func NewTransport(nodeID multiraft.NodeID, discovery Discovery, poolSize int) *Transport {
	return &Transport{
		nodeID:    nodeID,
		discovery: discovery,
		poolSize:  poolSize,
		pools:     make(map[multiraft.NodeID]*connPool),
		stopCh:    make(chan struct{}),
	}
}

func (t *Transport) SetRuntime(rt *multiraft.Runtime) { t.runtime = rt }
func (t *Transport) SetHandler(h forwardHandler)      { t.handler = h }

func (t *Transport) Start(listenAddr string) error {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	t.listener = ln
	t.wg.Add(1)
	go t.acceptLoop()
	return nil
}

func (t *Transport) Stop() {
	close(t.stopCh)
	if t.listener != nil {
		_ = t.listener.Close()
	}
	t.mu.RLock()
	for _, p := range t.pools {
		p.closeAll()
	}
	t.mu.RUnlock()
	t.wg.Wait()
}

// Send implements multiraft.Transport.
func (t *Transport) Send(ctx context.Context, batch []multiraft.Envelope) error {
	for _, env := range batch {
		data, err := env.Message.Marshal()
		if err != nil {
			return err
		}
		body := encodeRaftBody(uint64(env.GroupID), data)
		msg := encodeMessage(msgTypeRaft, body)

		target := multiraft.NodeID(env.Message.To)
		pool := t.getOrCreatePool(target)
		if pool == nil {
			continue // unknown node, skip
		}
		conn, idx, err := pool.getByGroup(env.GroupID)
		if err != nil {
			continue
		}
		_, err = conn.Write(msg)
		if err != nil {
			pool.resetConn(idx)
		}
		pool.release(idx)
	}
	return nil
}

func (t *Transport) getOrCreatePool(nodeID multiraft.NodeID) *connPool {
	t.mu.RLock()
	p, ok := t.pools[nodeID]
	t.mu.RUnlock()
	if ok {
		return p
	}

	addr, err := t.discovery.Resolve(nodeID)
	if err != nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok = t.pools[nodeID]; ok {
		return p
	}
	p = newConnPool(addr, t.poolSize)
	t.pools[nodeID] = p
	return p
}

func (t *Transport) acceptLoop() {
	defer t.wg.Done()
	for {
		conn, err := t.listener.Accept()
		if err != nil {
			select {
			case <-t.stopCh:
				return
			default:
				continue
			}
		}
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.SetKeepAlive(true)
			_ = tc.SetKeepAlivePeriod(30 * time.Second)
		}
		t.wg.Add(1)
		go t.handleConn(conn)
	}
}

func (t *Transport) handleConn(conn net.Conn) {
	defer t.wg.Done()
	defer conn.Close()

	for {
		select {
		case <-t.stopCh:
			return
		default:
		}

		msgType, body, err := readMessage(conn)
		if err != nil {
			if err == io.EOF {
				return
			}
			return
		}

		switch msgType {
		case msgTypeRaft:
			t.handleRaftMessage(body)
		case msgTypeForward:
			t.handleForwardMessage(conn, body)
		case msgTypeResp:
			// Responses are handled by the Forwarder on outgoing connections
		}
	}
}

func (t *Transport) handleRaftMessage(body []byte) {
	if t.runtime == nil {
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
	_ = t.runtime.Step(context.Background(), multiraft.Envelope{
		GroupID: multiraft.GroupID(groupID),
		Message: msg,
	})
}

func (t *Transport) handleForwardMessage(conn net.Conn, body []byte) {
	requestID, groupID, cmd, err := decodeForwardBody(body)
	if err != nil {
		return
	}
	if t.handler == nil {
		resp := encodeRespBody(requestID, errCodeNoGroup, nil)
		_, _ = conn.Write(encodeMessage(msgTypeResp, resp))
		return
	}
	data, errCode := t.handler.handleForward(context.Background(), multiraft.GroupID(groupID), cmd)
	resp := encodeRespBody(requestID, errCode, data)
	_, _ = conn.Write(encodeMessage(msgTypeResp, resp))
}
```

- [ ] **Step 2: Write transport test**

```go
package wkcluster

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft"
)

func TestTransport_StartStop(t *testing.T) {
	d := NewStaticDiscovery([]NodeConfig{
		{NodeID: 1, Addr: "127.0.0.1:0"},
	})
	tr := NewTransport(1, d, 4)
	if err := tr.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	tr.Stop()
}

func TestTransport_SendReceiveRaft(t *testing.T) {
	// Start receiver
	recvDiscovery := NewStaticDiscovery(nil)
	recv := NewTransport(2, recvDiscovery, 4)
	if err := recv.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start recv: %v", err)
	}
	defer recv.Stop()
	recvAddr := recv.listener.Addr().String()

	// Track received messages
	stepped := make(chan multiraft.Envelope, 1)
	recv.runtime = nil // We'll check via handler instead

	// Start sender
	sendDiscovery := NewStaticDiscovery([]NodeConfig{
		{NodeID: 2, Addr: recvAddr},
	})
	sender := NewTransport(1, sendDiscovery, 4)
	if err := sender.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("Start sender: %v", err)
	}
	defer sender.Stop()

	// Send a raft message
	err := sender.Send(context.Background(), []multiraft.Envelope{
		{GroupID: 1, Message: raftpb.Message{To: 2, From: 1, Type: raftpb.MsgHeartbeat}},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Give time for delivery
	time.Sleep(50 * time.Millisecond)
	_ = stepped // In a real test we'd verify via a mock runtime
}
```

Note: add `raftpb "go.etcd.io/raft/v3/raftpb"` to the test imports.

- [ ] **Step 3: Run tests**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkcluster/ -run TestTransport -v -timeout 10s`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add wkcluster/
git commit -m "feat(wkcluster): add TCP Transport with connection pool"
```

---

## Task 7: Forwarder

**Files:**
- Create: `wkcluster/forward.go`
- Create: `wkcluster/forward_test.go`

- [ ] **Step 1: Create forward.go**

```go
package wkcluster

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft"
)

type forwardResp struct {
	errCode uint8
	data    []byte
}

type Forwarder struct {
	nodeID    multiraft.NodeID
	transport *Transport
	timeout   time.Duration
	nextReqID atomic.Uint64
	pending   sync.Map // requestID → chan forwardResp

	// readLoops tracks which connections have a reader goroutine
	readLoops sync.Map // "nodeID-connIdx" → bool
	wg        sync.WaitGroup
	stopCh    chan struct{}
}

func NewForwarder(nodeID multiraft.NodeID, transport *Transport, timeout time.Duration) *Forwarder {
	return &Forwarder{
		nodeID:    nodeID,
		transport: transport,
		timeout:   timeout,
		stopCh:    make(chan struct{}),
	}
}

func (f *Forwarder) Stop() {
	close(f.stopCh)
	// Cancel all pending requests
	f.pending.Range(func(key, value any) bool {
		ch := value.(chan forwardResp)
		select {
		case ch <- forwardResp{errCode: errCodeTimeout}:
		default:
		}
		return true
	})
	f.wg.Wait()
}

func (f *Forwarder) Forward(ctx context.Context, targetNode multiraft.NodeID, groupID multiraft.GroupID, cmdBytes []byte) ([]byte, error) {
	pool := f.transport.getOrCreatePool(targetNode)
	if pool == nil {
		return nil, ErrNodeNotFound
	}

	requestID := f.nextReqID.Add(1)
	respCh := make(chan forwardResp, 1)
	f.pending.Store(requestID, respCh)
	defer f.pending.Delete(requestID)

	conn, idx, err := pool.getByGroup(groupID)
	if err != nil {
		return nil, fmt.Errorf("connect to leader: %w", err)
	}

	// Ensure a read loop for this connection
	f.ensureReadLoop(leaderID, idx, conn)

	body := encodeForwardBody(requestID, uint64(groupID), cmdBytes)
	msg := encodeMessage(msgTypeForward, body)
	_, err = conn.Write(msg)
	pool.release(idx)
	if err != nil {
		pool.mu[idx].Lock()
		pool.resetConn(idx)
		pool.mu[idx].Unlock()
		return nil, fmt.Errorf("write forward: %w", err)
	}

	// Wait for response
	deadline := time.After(f.timeout)
	select {
	case resp := <-respCh:
		return f.handleResp(resp)
	case <-deadline:
		return nil, ErrTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.stopCh:
		return nil, ErrStopped
	}
}

func (f *Forwarder) handleResp(resp forwardResp) ([]byte, error) {
	switch resp.errCode {
	case errCodeOK:
		return resp.data, nil
	case errCodeNotLeader:
		return nil, ErrNotLeader
	case errCodeTimeout:
		return nil, ErrTimeout
	case errCodeNoGroup:
		return nil, ErrGroupNotFound
	default:
		return nil, fmt.Errorf("unknown error code: %d", resp.errCode)
	}
}

func (f *Forwarder) ensureReadLoop(nodeID multiraft.NodeID, idx int, conn net.Conn) {
	key := fmt.Sprintf("%d-%d", nodeID, idx)
	if _, loaded := f.readLoops.LoadOrStore(key, true); loaded {
		return
	}
	f.wg.Add(1)
	go f.readLoop(key, conn)
}

func (f *Forwarder) readLoop(key string, conn net.Conn) {
	defer f.wg.Done()
	defer f.readLoops.Delete(key)

	r := io.Reader(conn)
	for {
		select {
		case <-f.stopCh:
			return
		default:
		}

		msgType, body, err := readMessage(r)
		if err != nil {
			return
		}
		if msgType != msgTypeResp {
			continue
		}

		requestID, errCode, data, err := decodeRespBody(body)
		if err != nil {
			continue
		}

		if v, ok := f.pending.LoadAndDelete(requestID); ok {
			ch := v.(chan forwardResp)
			ch <- forwardResp{errCode: errCode, data: data}
		}
	}
}

```

- [ ] **Step 2: Write tests**

```go
package wkcluster

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestForwarder_RoundTrip(t *testing.T) {
	// Create a server that echoes forward requests as responses
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			msgType, body, err := readMessage(conn)
			if err != nil {
				return
			}
			if msgType == msgTypeForward {
				reqID, _, cmd, _ := decodeForwardBody(body)
				resp := encodeRespBody(reqID, errCodeOK, cmd) // echo cmd as data
				_, _ = conn.Write(encodeMessage(msgTypeResp, resp))
			}
		}
	}()

	// Create forwarder with a direct connection
	d := NewStaticDiscovery([]NodeConfig{{NodeID: 2, Addr: ln.Addr().String()}})
	tr := NewTransport(1, d, 2)
	f := NewForwarder(1, tr, 5*time.Second)
	defer f.Stop()

	// Manually set up pool for nodeID=2
	pool := tr.getOrCreatePool(2)
	if pool == nil {
		t.Fatal("pool is nil")
	}

	// Test: forward and get echo back
	conn, idx, err := pool.getByGroup(1)
	if err != nil {
		t.Fatalf("getByGroup: %v", err)
	}
	pool.release(idx)

	// Set up read loop
	f.ensureReadLoop(2, idx, conn)

	// Send forward
	requestID := f.nextReqID.Add(1)
	respCh := make(chan forwardResp, 1)
	f.pending.Store(requestID, respCh)

	body := encodeForwardBody(requestID, 1, []byte("test-cmd"))
	msg := encodeMessage(msgTypeForward, body)
	pool.mu[idx].Lock()
	_, err = conn.Write(msg)
	pool.mu[idx].Unlock()
	if err != nil {
		t.Fatal(err)
	}

	select {
	case resp := <-respCh:
		if resp.errCode != errCodeOK {
			t.Fatalf("expected errCodeOK, got %d", resp.errCode)
		}
		if string(resp.data) != "test-cmd" {
			t.Fatalf("expected echo, got %s", resp.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for response")
	}
}

func TestForwarder_Timeout(t *testing.T) {
	// Server that never responds
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, _ := ln.Accept()
		if conn != nil {
			defer conn.Close()
			select {} // block forever
		}
	}()

	d := NewStaticDiscovery([]NodeConfig{{NodeID: 2, Addr: ln.Addr().String()}})
	tr := NewTransport(1, d, 2)
	f := NewForwarder(1, tr, 100*time.Millisecond)
	defer f.Stop()

	// Manually trigger a forward with timeout
	pool := tr.getOrCreatePool(2)
	conn, idx, err := pool.getByGroup(1)
	if err != nil {
		t.Fatal(err)
	}
	pool.release(idx)
	f.ensureReadLoop(2, idx, conn)

	reqID := f.nextReqID.Add(1)
	respCh := make(chan forwardResp, 1)
	f.pending.Store(reqID, respCh)
	defer f.pending.Delete(reqID)

	body := encodeForwardBody(reqID, 1, []byte("test"))
	pool.mu[idx].Lock()
	_, _ = conn.Write(encodeMessage(msgTypeForward, body))
	pool.mu[idx].Unlock()

	select {
	case <-respCh:
		t.Fatal("should not receive response")
	case <-time.After(200 * time.Millisecond):
		// expected timeout
	}
}
```

- [ ] **Step 3: Run tests**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkcluster/ -run TestForwarder -v -timeout 10s`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add wkcluster/
git commit -m "feat(wkcluster): add Forwarder with requestID correlation"
```

---

## Task 8: Cluster Lifecycle and API

**Files:**
- Create: `wkcluster/cluster.go`
- Create: `wkcluster/api.go`

- [ ] **Step 1: Create cluster.go**

```go
package wkcluster

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/controller/raftstore"
	"github.com/WuKongIM/WuKongIM/pkg/controller/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkfsm"
	"go.etcd.io/raft/v3"
)

type Cluster struct {
	cfg       Config
	runtime   *multiraft.Runtime
	transport *Transport
	discovery Discovery
	router    *Router
	forwarder *Forwarder
	db        *wkdb.DB
	raftDB    *raftstore.DB
	stopped   atomic.Bool
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
		c.db.Close()
		return fmt.Errorf("open raftstore: %w", err)
	}

	// 2. Discovery
	c.discovery = NewStaticDiscovery(c.cfg.Nodes)

	// 3. Transport
	c.transport = NewTransport(c.cfg.NodeID, c.discovery, c.cfg.PoolSize)
	if err := c.transport.Start(c.cfg.ListenAddr); err != nil {
		c.raftDB.Close()
		c.db.Close()
		return fmt.Errorf("start transport: %w", err)
	}

	// 4. Runtime
	c.runtime, err = multiraft.New(multiraft.Options{
		NodeID:       c.cfg.NodeID,
		TickInterval: 100 * time.Millisecond,
		Workers:      2,
		Transport:    c.transport,
		Raft: multiraft.RaftOptions{
			ElectionTick:  10,
			HeartbeatTick: 1,
		},
	})
	if err != nil {
		c.transport.Stop()
		c.raftDB.Close()
		c.db.Close()
		return fmt.Errorf("create runtime: %w", err)
	}
	c.transport.SetRuntime(c.runtime)

	// 5. Router and Forwarder
	c.router = NewRouter(c.cfg.GroupCount, c.cfg.NodeID, c.runtime)
	c.forwarder = NewForwarder(c.cfg.NodeID, c.transport, c.cfg.ForwardTimeout)
	c.transport.SetHandler(c)

	// 6. Open groups
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

	bs, err := storage.InitialState(ctx)
	if err != nil {
		return err
	}
	if !raft.IsEmptyHardState(bs.HardState) {
		return c.runtime.OpenGroup(ctx, opts)
	}
	return c.runtime.BootstrapGroup(ctx, multiraft.BootstrapGroupRequest{
		Group:  opts,
		Voters: g.Peers,
	})
}

func (c *Cluster) Stop() {
	c.stopped.Store(true)

	if c.forwarder != nil {
		c.forwarder.Stop()
	}
	if c.runtime != nil {
		_ = c.runtime.Close()
	}
	if c.transport != nil {
		c.transport.Stop()
	}
	if c.raftDB != nil {
		_ = c.raftDB.Close()
	}
	if c.db != nil {
		_ = c.db.Close()
	}
}

// handleForward implements forwardHandler for the Transport.
// Called when this node (as leader) receives a forwarded request.
func (c *Cluster) handleForward(ctx context.Context, groupID multiraft.GroupID, cmd []byte) ([]byte, uint8) {
	if c.stopped.Load() {
		return nil, errCodeTimeout
	}
	_, err := c.runtime.Status(groupID)
	if err != nil {
		return nil, errCodeNoGroup
	}
	future, err := c.runtime.Propose(ctx, groupID, cmd)
	if err != nil {
		return nil, errCodeNotLeader
	}
	result, err := future.Wait(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errCodeTimeout
		}
		return nil, errCodeNotLeader
	}
	return result.Data, errCodeOK
}
```

- [ ] **Step 2: Create api.go**

```go
package wkcluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/controller/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkfsm"
)

func (c *Cluster) CreateChannel(ctx context.Context, channelID string, channelType int64) error {
	groupID := c.router.SlotForChannel(channelID)
	cmd := wkfsm.EncodeUpsertChannelCommand(wkdb.Channel{
		ChannelID:   channelID,
		ChannelType: channelType,
	})
	return c.proposeOrForward(ctx, groupID, cmd)
}

func (c *Cluster) UpdateChannel(ctx context.Context, channelID string, channelType int64, ban int64) error {
	groupID := c.router.SlotForChannel(channelID)
	cmd := wkfsm.EncodeUpsertChannelCommand(wkdb.Channel{
		ChannelID:   channelID,
		ChannelType: channelType,
		Ban:         ban,
	})
	return c.proposeOrForward(ctx, groupID, cmd)
}

func (c *Cluster) DeleteChannel(ctx context.Context, channelID string, channelType int64) error {
	groupID := c.router.SlotForChannel(channelID)
	cmd := wkfsm.EncodeDeleteChannelCommand(channelID, channelType)
	return c.proposeOrForward(ctx, groupID, cmd)
}

func (c *Cluster) GetChannel(ctx context.Context, channelID string, channelType int64) (wkdb.Channel, error) {
	groupID := c.router.SlotForChannel(channelID)
	store := c.db.ForSlot(uint64(groupID))
	return store.GetChannel(ctx, channelID, channelType)
}

func (c *Cluster) proposeOrForward(ctx context.Context, groupID multiraft.GroupID, cmd []byte) error {
	if c.stopped.Load() {
		return ErrStopped
	}
	for attempt := 0; attempt < 3; attempt++ {
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
		_, err = c.forwarder.Forward(ctx, leaderID, groupID, cmd)
		if errors.Is(err, ErrNotLeader) {
			continue
		}
		return err
	}
	return ErrLeaderNotStable
}
```

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/tt/Desktop/work/go/wraft && go build ./wkcluster/`
Expected: Compiles successfully

- [ ] **Step 4: Commit**

```bash
git add wkcluster/
git commit -m "feat(wkcluster): add Cluster lifecycle and Channel CRUD API"
```

---

## Task 9: Integration Test — Single-Node Cluster

**Files:**
- Create: `wkcluster/cluster_test.go`

- [ ] **Step 1: Write single-node cluster integration test**

```go
package wkcluster

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCluster_SingleNode_CreateAndGetChannel(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		NodeID:     1,
		ListenAddr: "127.0.0.1:0",
		GroupCount: 1,
		DataDir:    dir,
		Nodes:      []NodeConfig{{NodeID: 1, Addr: "127.0.0.1:0"}},
		Groups:     []GroupConfig{{GroupID: 1, Peers: []multiraft.NodeID{1}}},
	}

	c, err := NewCluster(cfg)
	if err != nil {
		t.Fatalf("NewCluster: %v", err)
	}
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Wait for leader election
	waitForLeader(t, c, 1)

	// Create channel
	if err := c.CreateChannel(ctx, "ch-1", 1); err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}

	// Read back
	ch, err := c.GetChannel(ctx, "ch-1", 1)
	if err != nil {
		t.Fatalf("GetChannel: %v", err)
	}
	if ch.ChannelID != "ch-1" || ch.ChannelType != 1 {
		t.Fatalf("unexpected channel: %+v", ch)
	}
}

func TestCluster_SingleNode_DeleteChannel(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		NodeID:     1,
		ListenAddr: "127.0.0.1:0",
		GroupCount: 1,
		DataDir:    dir,
		Nodes:      []NodeConfig{{NodeID: 1, Addr: "127.0.0.1:0"}},
		Groups:     []GroupConfig{{GroupID: 1, Peers: []multiraft.NodeID{1}}},
	}

	c, err := NewCluster(cfg)
	if err != nil {
		t.Fatalf("NewCluster: %v", err)
	}
	if err := c.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	waitForLeader(t, c, 1)

	// Create then delete
	if err := c.CreateChannel(ctx, "ch-del", 1); err != nil {
		t.Fatalf("CreateChannel: %v", err)
	}
	if err := c.DeleteChannel(ctx, "ch-del", 1); err != nil {
		t.Fatalf("DeleteChannel: %v", err)
	}

	// Verify deleted
	_, err = c.GetChannel(ctx, "ch-del", 1)
	if err == nil {
		t.Fatal("expected error for deleted channel")
	}
}

func waitForLeader(t testing.TB, c *Cluster, groupID uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		status, err := c.runtime.Status(multiraft.GroupID(groupID))
		if err == nil && status.LeaderID != 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no leader elected for group %d", groupID)
}
```

Note: imports needed: `"context"`, `"testing"`, `"time"`, `"github.com/WuKongIM/WuKongIM/pkg/consensus/multiraft"`. For multi-node test (Task 10): also `"fmt"`, `"net"`, `"path/filepath"`.

- [ ] **Step 2: Run integration test**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkcluster/ -run TestCluster_SingleNode -v -timeout 30s`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add wkcluster/
git commit -m "test(wkcluster): add single-node cluster integration tests"
```

---

## Task 10: Integration Test — Multi-Node Cluster with Forwarding

**Files:**
- Modify: `wkcluster/cluster_test.go`

- [ ] **Step 1: Write multi-node test**

Add to `wkcluster/cluster_test.go`:

```go
func TestCluster_ThreeNode_ForwardToLeader(t *testing.T) {
	clusters := startThreeNodeCluster(t)
	defer func() {
		for _, c := range clusters {
			c.Stop()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Wait for leader on group 1
	var leader *Cluster
	for _, c := range clusters {
		waitForLeader(t, c, 1)
	}

	// Find a follower for group 1
	var follower *Cluster
	for _, c := range clusters {
		status, _ := c.runtime.Status(1)
		if status.LeaderID == c.cfg.NodeID {
			leader = c
		} else {
			follower = c
		}
	}
	if follower == nil || leader == nil {
		t.Fatal("could not identify leader/follower")
	}

	// Create channel on follower — should forward to leader
	if err := follower.CreateChannel(ctx, "forwarded-ch", 1); err != nil {
		t.Fatalf("CreateChannel on follower: %v", err)
	}

	// Verify on leader
	time.Sleep(100 * time.Millisecond) // allow replication
	ch, err := leader.GetChannel(ctx, "forwarded-ch", 1)
	if err != nil {
		t.Fatalf("GetChannel on leader: %v", err)
	}
	if ch.ChannelID != "forwarded-ch" {
		t.Fatalf("unexpected: %+v", ch)
	}
}

func startThreeNodeCluster(t testing.TB) []*Cluster {
	t.Helper()

	// Phase 1: Allocate ports by starting TCP listeners first
	listeners := make([]net.Listener, 3)
	for i := 0; i < 3; i++ {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen %d: %v", i, err)
		}
		listeners[i] = ln
	}

	nodes := make([]NodeConfig, 3)
	for i := 0; i < 3; i++ {
		nodes[i] = NodeConfig{
			NodeID: multiraft.NodeID(i + 1),
			Addr:   listeners[i].Addr().String(),
		}
		listeners[i].Close() // Release port; Start() will rebind
	}

	// Phase 2: Start clusters with known addresses
	clusters := make([]*Cluster, 3)
	for i := 0; i < 3; i++ {
		cfg := Config{
			NodeID:     multiraft.NodeID(i + 1),
			ListenAddr: nodes[i].Addr,
			GroupCount: 1,
			DataDir:    filepath.Join(t.TempDir(), fmt.Sprintf("n%d", i+1)),
			Nodes:      nodes,
			Groups: []GroupConfig{
				{GroupID: 1, Peers: []multiraft.NodeID{1, 2, 3}},
			},
		}
		c, err := NewCluster(cfg)
		if err != nil {
			t.Fatalf("NewCluster node %d: %v", i+1, err)
		}
		if err := c.Start(); err != nil {
			t.Fatalf("Start node %d: %v", i+1, err)
		}
		clusters[i] = c
	}

	return clusters
}
```

- [ ] **Step 2: Run test**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./wkcluster/ -run TestCluster_ThreeNode -v -timeout 30s`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add wkcluster/
git commit -m "test(wkcluster): add multi-node cluster integration test with forwarding"
```

---

## Task 11: Final Verification

- [ ] **Step 1: Run all tests**

Run: `cd /Users/tt/Desktop/work/go/wraft && go test ./... -v -timeout 60s`
Expected: All tests PASS

- [ ] **Step 2: Run vet and build**

Run: `cd /Users/tt/Desktop/work/go/wraft && go vet ./... && go build ./...`
Expected: No issues

- [ ] **Step 3: Final commit if any fixes needed**

Fix any compilation or test issues discovered, commit.
