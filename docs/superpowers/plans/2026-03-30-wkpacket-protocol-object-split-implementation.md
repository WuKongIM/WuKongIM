# wkpacket Protocol Object Split Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract shared protocol objects into `pkg/wkpacket` so `pkg/wkproto` and `pkg/jsonrpc` become protocol-specific codec packages.

**Architecture:** Introduce a dependency-free `pkg/wkpacket` package that owns shared frame interfaces, enums, constants, and packet structs. Rewire `pkg/wkproto` to perform WuKong binary encoding/decoding against `wkpacket` types, and rewire `pkg/jsonrpc` to map JSON-RPC messages to and from `wkpacket` types without depending on `pkg/wkproto`.

**Tech Stack:** Go, stdlib, existing `github.com/pkg/errors`, `github.com/stretchr/testify`, `github.com/valyala/bytebufferpool`.

**Spec:** `docs/superpowers/specs/2026-03-30-wkpacket-protocol-object-split-design.md`

---

## File Map

### New files

| File | Responsibility |
|------|----------------|
| `pkg/wkpacket/common.go` | `Frame`, `Framer`, `FrameType`, `ReasonCode`, `DeviceFlag`, `DeviceLevel`, `Channel`, `LatestVersion`, size/channel constants |
| `pkg/wkpacket/setting.go` | `Setting` bit flags |
| `pkg/wkpacket/connect.go` | `ConnectPacket` object definition |
| `pkg/wkpacket/connack.go` | `ConnackPacket` object definition |
| `pkg/wkpacket/send.go` | `SendPacket` object definition and object helpers |
| `pkg/wkpacket/sendack.go` | `SendackPacket` object definition |
| `pkg/wkpacket/recv.go` | `StreamFlag`, `RecvPacket`, `Reset`, verification helpers |
| `pkg/wkpacket/recvack.go` | `RecvackPacket` object definition |
| `pkg/wkpacket/disconnect.go` | `DisconnectPacket` object definition |
| `pkg/wkpacket/sub.go` | `SubPacket` object definition |
| `pkg/wkpacket/suback.go` | `Action`, `SubackPacket` object definition |
| `pkg/wkpacket/event.go` | `EventPacket` object definition |
| `pkg/wkpacket/ping.go` | `PingPacket` object definition |
| `pkg/wkpacket/pong.go` | `PongPacket` object definition |
| `pkg/wkpacket/common_test.go` | Shared object smoke tests: flags, latest version, frame type helpers |
| `pkg/wkpacket/packet_test.go` | `SendPacket.UniqueKey`, `RecvPacket.Reset`, `ReasonCode.String` regression checks |
| `pkg/jsonrpc/frame_bridge_test.go` | JSON-RPC to `wkpacket` conversion tests |

### Modified files

| File | Change |
|------|--------|
| `pkg/wkproto/common.go` | Remove shared object definitions; keep only fixed-header codec helpers such as `ToFixHeaderUint8` and `FramerFromUint8`, rewritten against `wkpacket` types |
| `pkg/wkproto/protocol.go` | Import `wkpacket`, switch interface and packet assertions to `wkpacket.Frame` and `wkpacket.*Packet` |
| `pkg/wkproto/connect.go` | Keep only binary codec functions for connect packets, rewritten to use `wkpacket.ConnectPacket` |
| `pkg/wkproto/connack.go` | Keep only binary codec functions for connack packets |
| `pkg/wkproto/send.go` | Keep only binary codec functions for send packets |
| `pkg/wkproto/sendack.go` | Keep only binary codec functions for sendack packets |
| `pkg/wkproto/recv.go` | Keep only binary codec functions for recv packets; remove object-side `Size` methods |
| `pkg/wkproto/recvack.go` | Keep only binary codec functions for recvack packets |
| `pkg/wkproto/disconnect.go` | Keep only binary codec functions for disconnect packets |
| `pkg/wkproto/sub.go` | Keep only binary codec functions for sub packets |
| `pkg/wkproto/suback.go` | Keep only binary codec functions for suback packets |
| `pkg/wkproto/event.go` | Keep only binary codec functions for event packets; remove object-side `Size` methods |
| `pkg/wkproto/ping.go` | Remove packet object definition; keep file only if codec helpers remain necessary |
| `pkg/wkproto/pong.go` | Remove packet object definition; keep file only if codec helpers remain necessary |
| `pkg/wkproto/*_test.go` | Update tests to instantiate/import `wkpacket` packet types and `wkpacket.LatestVersion` |
| `pkg/jsonrpc/types.go` | Replace `wkproto` shared type imports/usages with `wkpacket`; switch default version to `wkpacket.LatestVersion` |
| `pkg/jsonrpc/codec.go` | Change `ToFrame` / `FromFrame` signatures and type switches from `wkproto` to `wkpacket` |
| `pkg/jsonrpc/codec_test.go` | Update imports/assertions if signatures change |
| `pkg/jsonrpc/wukongim_rpc_schema.json` | Update descriptive text from `wkproto.ReasonCode` to `wkpacket.ReasonCode` |

### Existing dirty files to preserve

| File | Risk |
|------|------|
| `pkg/jsonrpc/codec.go` | Already modified in worktree; inspect current diff before editing |
| `pkg/jsonrpc/types.go` | Already modified in worktree; inspect current diff before editing |

---

### Task 1: Create `pkg/wkpacket` shared object package

**Files:**
- Create: `pkg/wkpacket/common.go`
- Create: `pkg/wkpacket/setting.go`
- Create: `pkg/wkpacket/connect.go`
- Create: `pkg/wkpacket/connack.go`
- Create: `pkg/wkpacket/send.go`
- Create: `pkg/wkpacket/sendack.go`
- Create: `pkg/wkpacket/recv.go`
- Create: `pkg/wkpacket/recvack.go`
- Create: `pkg/wkpacket/disconnect.go`
- Create: `pkg/wkpacket/sub.go`
- Create: `pkg/wkpacket/suback.go`
- Create: `pkg/wkpacket/event.go`
- Create: `pkg/wkpacket/ping.go`
- Create: `pkg/wkpacket/pong.go`
- Create: `pkg/wkpacket/common_test.go`
- Create: `pkg/wkpacket/packet_test.go`

- [ ] **Step 1: Write failing `wkpacket` smoke tests**

```go
package wkpacket

import "testing"

func TestLatestVersionAndFrameTypes(t *testing.T) {
	if LatestVersion != 5 {
		t.Fatalf("expected LatestVersion=5, got %d", LatestVersion)
	}
	if (&PingPacket{}).GetFrameType() != PING {
		t.Fatalf("ping frame type mismatch")
	}
	if (&PongPacket{}).GetFrameType() != PONG {
		t.Fatalf("pong frame type mismatch")
	}
}

func TestSettingFlags(t *testing.T) {
	var s Setting
	s.Set(SettingReceiptEnabled)
	s.Set(SettingTopic)
	if !s.IsSet(SettingReceiptEnabled) || !s.IsSet(SettingTopic) {
		t.Fatalf("expected flags to be set")
	}
	s.Clear(SettingTopic)
	if s.IsSet(SettingTopic) {
		t.Fatalf("expected topic flag to be cleared")
	}
}

func TestSendPacketUniqueKeyAndRecvReset(t *testing.T) {
	send := &SendPacket{
		ChannelID:   "ch",
		ChannelType: 2,
		ClientMsgNo: "msg-1",
		ClientSeq:   9,
	}
	if send.UniqueKey() != "ch-2-msg-1-9" {
		t.Fatalf("unexpected unique key: %s", send.UniqueKey())
	}

	recv := &RecvPacket{
		Framer: Framer{RedDot: true, RemainingLength: 10},
		Setting: SettingReceiptEnabled,
		ChannelID: "ch",
		Payload: []byte("body"),
	}
	recv.Reset()
	if recv.ChannelID != "" || recv.Setting != 0 || recv.RemainingLength != 0 || recv.RedDot {
		t.Fatalf("recv reset did not clear fields")
	}
}
```

- [ ] **Step 2: Run the new tests and verify they fail**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go test ./pkg/wkpacket -run 'Test(LatestVersionAndFrameTypes|SettingFlags|SendPacketUniqueKeyAndRecvReset)' -v`
Expected: FAIL because `pkg/wkpacket` does not exist yet.

- [ ] **Step 3: Implement `pkg/wkpacket`**

```go
// pkg/wkpacket/common.go
package wkpacket

import "fmt"

const LatestVersion = 5

type Frame interface {
	GetFrameType() FrameType
	GetRemainingLength() uint32
	GetNoPersist() bool
	GetRedDot() bool
	GetsyncOnce() bool
	GetDUP() bool
	GetFrameSize() int64
	GetHasServerVersion() bool
	GetEnd() bool
}

type Framer struct {
	FrameType        FrameType
	RemainingLength  uint32
	NoPersist        bool
	RedDot           bool
	SyncOnce         bool
	DUP              bool
	HasServerVersion bool
	End              bool
	FrameSize        int64
}

func (f Framer) GetFrameType() FrameType        { return f.FrameType }
func (f Framer) GetRemainingLength() uint32     { return f.RemainingLength }
func (f Framer) GetNoPersist() bool             { return f.NoPersist }
func (f Framer) GetRedDot() bool                { return f.RedDot }
func (f Framer) GetsyncOnce() bool              { return f.SyncOnce }
func (f Framer) GetDUP() bool                   { return f.DUP }
func (f Framer) GetFrameSize() int64            { return f.FrameSize }
func (f Framer) GetHasServerVersion() bool      { return f.HasServerVersion }
func (f Framer) GetEnd() bool                   { return f.End }
func (f Framer) String() string                 { return fmt.Sprintf("packetType: %s remainingLength:%d NoPersist:%v redDot:%v syncOnce:%v DUP:%v", f.GetFrameType().String(), f.RemainingLength, f.NoPersist, f.RedDot, f.SyncOnce, f.DUP) }
```

```go
// pkg/wkpacket/send.go
package wkpacket

import "fmt"

type SendPacket struct {
	Framer
	Setting     Setting
	MsgKey      string
	Expire      uint32
	ClientSeq   uint64
	ClientMsgNo string
	StreamNo    string
	ChannelID   string
	ChannelType uint8
	Topic       string
	Payload     []byte
}

func (s *SendPacket) GetFrameType() FrameType { return SEND }
func (s *SendPacket) UniqueKey() string {
	return fmt.Sprintf("%s-%d-%s-%d", s.ChannelID, s.ChannelType, s.ClientMsgNo, s.ClientSeq)
}
```

```go
// pkg/wkpacket/recv.go
package wkpacket

type RecvPacket struct {
	Framer
	Setting     Setting
	MsgKey      string
	Expire      uint32
	MessageID   int64
	MessageSeq  uint32
	ClientMsgNo string
	StreamNo    string
	StreamId    uint64
	StreamFlag  StreamFlag
	Timestamp   int32
	ChannelID   string
	ChannelType uint8
	Topic       string
	FromUID     string
	Payload     []byte
	ClientSeq   uint64
}

func (r *RecvPacket) GetFrameType() FrameType { return RECV }
func (r *RecvPacket) Reset() {
	*r = RecvPacket{}
}
```

All other packet files should follow the same rule: move only object structure, enum definitions, `String()`, `GetFrameType()`, and object-local helpers. Do not move binary codec functions or object methods that require codec-side size helpers.

- [ ] **Step 4: Run the new `wkpacket` tests and verify they pass**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go test ./pkg/wkpacket -run 'Test(LatestVersionAndFrameTypes|SettingFlags|SendPacketUniqueKeyAndRecvReset)' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/wkpacket
git commit -m "refactor(wkpacket): extract shared protocol objects"
```

---

### Task 2: Rewire `pkg/wkproto` to encode/decode `wkpacket` types

**Files:**
- Modify: `pkg/wkproto/common.go`
- Modify: `pkg/wkproto/protocol.go`
- Modify: `pkg/wkproto/connect.go`
- Modify: `pkg/wkproto/connack.go`
- Modify: `pkg/wkproto/send.go`
- Modify: `pkg/wkproto/sendack.go`
- Modify: `pkg/wkproto/recv.go`
- Modify: `pkg/wkproto/recvack.go`
- Modify: `pkg/wkproto/disconnect.go`
- Modify: `pkg/wkproto/sub.go`
- Modify: `pkg/wkproto/suback.go`
- Modify: `pkg/wkproto/event.go`
- Modify: `pkg/wkproto/ping.go`
- Modify: `pkg/wkproto/pong.go`
- Modify: `pkg/wkproto/connect_test.go`
- Modify: `pkg/wkproto/connack_test.go`
- Modify: `pkg/wkproto/send_test.go`
- Modify: `pkg/wkproto/sendack_test.go`
- Modify: `pkg/wkproto/recv_test.go`
- Modify: `pkg/wkproto/recvack_test.go`
- Modify: `pkg/wkproto/disconnect_test.go`
- Modify: `pkg/wkproto/sub_test.go`
- Modify: `pkg/wkproto/suback_test.go`
- Modify: `pkg/wkproto/event_test.go`
- Modify: `pkg/wkproto/ping_test.go`
- Modify: `pkg/wkproto/pong_test.go`

- [ ] **Step 1: Update focused codec tests to use `wkpacket` and let them fail**

```go
package wkproto

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/proto/wkpacket"
	"github.com/stretchr/testify/assert"
)

func TestSendEncodeAndDecode(t *testing.T) {
	var setting wkpacket.Setting
	setting.Set(wkpacket.SettingNoEncrypt)
	packet := &wkpacket.SendPacket{
		Framer: wkpacket.Framer{RedDot: true},
		Expire: 100,
		Setting: setting,
		ClientSeq: 2,
		ChannelID: "34341",
		ChannelType: 2,
		Payload: []byte("dsdsdsd1"),
	}

	codec := New()
	packetBytes, err := codec.EncodeFrame(packet, wkpacket.LatestVersion)
	assert.NoError(t, err)

	resultPacket, _, err := codec.DecodeFrame(packetBytes, wkpacket.LatestVersion)
	assert.NoError(t, err)
	resultSendPacket, ok := resultPacket.(*wkpacket.SendPacket)
	assert.True(t, ok)
	assert.Equal(t, packet.ChannelID, resultSendPacket.ChannelID)
}
```

- [ ] **Step 2: Run focused `wkproto` tests and verify they fail**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go test ./pkg/wkproto -run 'Test(SendEncodeAndDecode|ConnectEncodeAndDecode|EventEncodeAndDecode)' -v`
Expected: FAIL to compile because codec code still references `wkproto` packet definitions.

- [ ] **Step 3: Rewrite `pkg/wkproto` against `wkpacket`**

```go
// pkg/wkproto/protocol.go
package wkproto

import "github.com/WuKongIM/WuKongIM/pkg/proto/wkpacket"

type Protocol interface {
	DecodeFrame(data []byte, version uint8) (wkpacket.Frame, int, error)
	EncodeFrame(packet wkpacket.Frame, version uint8) ([]byte, error)
	WriteFrame(w Writer, packet wkpacket.Frame, version uint8) error
}

type PacketDecodeFunc func(frame wkpacket.Frame, remainingBytes []byte, version uint8) (wkpacket.Frame, error)
type PacketEncodeFunc func(frame wkpacket.Frame, version uint8) ([]byte, error)
```

```go
// pkg/wkproto/common.go
package wkproto

import "github.com/WuKongIM/WuKongIM/pkg/proto/wkpacket"

func ToFixHeaderUint8(f wkpacket.Frame) uint8 {
	typeAndFlags := encodeBool(f.GetDUP())<<3 |
		encodeBool(f.GetsyncOnce())<<2 |
		encodeBool(f.GetRedDot())<<1 |
		encodeBool(f.GetNoPersist())
	if f.GetFrameType() == wkpacket.CONNACK {
		typeAndFlags = encodeBool(f.GetHasServerVersion())
	}
	return byte(int(f.GetFrameType()<<4) | typeAndFlags)
}

func FramerFromUint8(v uint8) wkpacket.Framer {
	p := wkpacket.Framer{}
	// populate flags exactly as before
	return p
}
```

```go
// pkg/wkproto/connect.go
package wkproto

import "github.com/WuKongIM/WuKongIM/pkg/proto/wkpacket"

func decodeConnect(frame wkpacket.Frame, data []byte, version uint8) (wkpacket.Frame, error) {
	dec := NewDecoder(data)
	packet := &wkpacket.ConnectPacket{}
	packet.Framer = frame.(wkpacket.Framer)
	// decode fields exactly in the old order
	return packet, nil
}

func encodeConnect(packet *wkpacket.ConnectPacket, enc *Encoder, _ uint8) error {
	// write fields exactly in the old order
	return nil
}
```

Apply the same conversion pattern to all packet codec files. Keep `ToFixHeaderUint8`, `FramerFromUint8`, `MaxRemaingLength`, `PayloadMaxSize`, and variable-length helpers in `pkg/wkproto`; do not move them into `pkg/wkpacket`. Remove any object-only methods from codec files that now belong in `pkg/wkpacket`, and drop `RecvPacket.Size()`, `RecvPacket.SizeWithProtoVersion()`, and `EventPacket.Size()` instead of recreating a reverse dependency.

- [ ] **Step 4: Run the full `wkproto` test suite and verify it passes**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go test ./pkg/wkproto -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/wkproto
git commit -m "refactor(wkproto): use wkpacket for shared frames"
```

---

### Task 3: Rewire `pkg/jsonrpc` to map against `wkpacket`

**Files:**
- Modify: `pkg/jsonrpc/types.go`
- Modify: `pkg/jsonrpc/codec.go`
- Modify: `pkg/jsonrpc/codec_test.go`
- Create: `pkg/jsonrpc/frame_bridge_test.go`
- Modify: `pkg/jsonrpc/wukongim_rpc_schema.json`

- [ ] **Step 0: Inspect existing local edits before changing JSON-RPC files**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && git diff -- pkg/jsonrpc/codec.go pkg/jsonrpc/types.go`
Expected: Review current worktree diff and preserve unrelated user changes while applying the refactor.

- [ ] **Step 1: Write failing JSON-RPC bridge tests**

```go
package jsonrpc

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/proto/wkpacket"
	"github.com/stretchr/testify/require"
)

func TestConnectParamsToProtoDefaultsToWKPacketLatestVersion(t *testing.T) {
	pkt := ConnectParams{
		UID: "u1",
		Token: "t1",
	}.ToProto()

	require.Equal(t, wkpacket.LatestVersion, pkt.Version)
}

func TestToFrameReturnsWKPacketFrame(t *testing.T) {
	frame, reqID, err := ToFrame(ConnectRequest{
		BaseRequest: BaseRequest{Method: MethodConnect, ID: "req-1"},
		Params: ConnectParams{UID: "u1", Token: "t1"},
	})
	require.NoError(t, err)
	require.Equal(t, "req-1", reqID)
	require.IsType(t, &wkpacket.ConnectPacket{}, frame)
}

func TestFromFrameAcceptsWKPacketConnack(t *testing.T) {
	msg, err := FromFrame("req-1", &wkpacket.ConnackPacket{
		ReasonCode: wkpacket.ReasonSuccess,
	})
	require.NoError(t, err)
	require.IsType(t, ConnectResponse{}, msg)
}
```

- [ ] **Step 2: Run focused JSON-RPC tests and verify they fail**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go test ./pkg/jsonrpc -run 'Test(ConnectParamsToProtoDefaultsToWKPacketLatestVersion|ToFrameReturnsWKPacketFrame|FromFrameAcceptsWKPacketConnack)' -v`
Expected: FAIL because `jsonrpc` still depends on `wkproto` shared types.

- [ ] **Step 3: Update JSON-RPC mapping code**

```go
// pkg/jsonrpc/types.go
package jsonrpc

import "github.com/WuKongIM/WuKongIM/pkg/proto/wkpacket"

func (h Header) toProtoInternal() *wkpacket.Framer {
	return &wkpacket.Framer{
		NoPersist: h.NoPersist,
		RedDot: h.RedDot,
		SyncOnce: h.SyncOnce,
		DUP: h.Dup,
		End: h.End,
	}
}

func (sf SettingFlags) ToProto() wkpacket.Setting {
	var setting wkpacket.Setting
	if sf.Receipt {
		setting |= wkpacket.SettingReceiptEnabled
	}
	if sf.Signal {
		setting |= wkpacket.SettingSignal
	}
	if sf.Stream {
		setting |= wkpacket.SettingStream
	}
	if sf.Topic {
		setting |= wkpacket.SettingTopic
	}
	return setting
}

func (p ConnectParams) ToProto() *wkpacket.ConnectPacket {
	version := uint8(p.Version)
	if p.Version == 0 {
		version = wkpacket.LatestVersion
	}
	return &wkpacket.ConnectPacket{Version: version, UID: p.UID, Token: p.Token}
}
```

```go
// pkg/jsonrpc/codec.go
package jsonrpc

import "github.com/WuKongIM/WuKongIM/pkg/proto/wkpacket"

func ToFrame(packet interface{}) (wkpacket.Frame, string, error) {
	switch p := packet.(type) {
	case ConnectRequest:
		return p.Params.ToProto(), p.ID, nil
	case SendRequest:
		return p.Params.ToProto(), p.ID, nil
	case PingRequest:
		return &wkpacket.PingPacket{}, p.ID, nil
	case DisconnectRequest:
		return p.Params.ToProto(), p.ID, nil
	case RecvAckNotification:
		return p.Params.ToProto(), "", nil
	}
	return nil, "", fmt.Errorf("unknown packet type: %T", packet)
}
```

Update all `FromProto...` helpers, `headerToFramer`, `fromProtoHeader`, `fromProtoSetting`, and `FromFrame` type switches to use `wkpacket`. Preserve JSON-RPC wire shape exactly. Update schema text to refer to `wkpacket.ReasonCode`.

- [ ] **Step 4: Run the full JSON-RPC test suite and verify it passes**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go test ./pkg/jsonrpc -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/jsonrpc
git commit -m "refactor(jsonrpc): map frames through wkpacket"
```

---

### Task 4: Rewrite remaining imports and finish verification

**Files:**
- Modify: any remaining files returned by `rg -n "wkproto\\.(ConnectPacket|ConnackPacket|SendPacket|SendackPacket|RecvPacket|RecvackPacket|DisconnectPacket|SubPacket|SubackPacket|EventPacket|PingPacket|PongPacket|Frame|Framer|ReasonCode|Setting|DeviceFlag|FrameType|LatestVersion)" .`

- [ ] **Step 1: Find leftover shared-object imports**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && rg -n 'wkproto\\.(ConnectPacket|ConnackPacket|SendPacket|SendackPacket|RecvPacket|RecvackPacket|DisconnectPacket|SubPacket|SubackPacket|EventPacket|PingPacket|PongPacket|Frame|Framer|ReasonCode|Setting|DeviceFlag|FrameType|LatestVersion)' .`
Expected: only intentional codec references remain, or no matches.

- [ ] **Step 2: Fix any leftovers**

```go
// Example of the final desired direction:
import (
	"github.com/WuKongIM/WuKongIM/pkg/proto/wkpacket"
	"github.com/WuKongIM/WuKongIM/pkg/proto/wkproto"
)

var frame wkpacket.Frame
codec := wkproto.New()
```

Update any remaining code or tests that still use `wkproto` as the shared object package. Do not leave object aliases behind in `wkproto`.

- [ ] **Step 3: Run formatting**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && gofmt -w pkg/wkpacket/*.go pkg/wkproto/*.go pkg/jsonrpc/*.go`
Expected: success, no output.

- [ ] **Step 4: Run targeted verification**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go test ./pkg/wkpacket ./pkg/wkproto ./pkg/jsonrpc -v`
Expected: PASS.

- [ ] **Step 5: Run repository-wide verification**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v3.1 && go test ./...`
Expected: PASS. If unrelated pre-existing failures appear, capture them explicitly before proceeding.

- [ ] **Step 6: Commit final cleanup**

```bash
git add pkg/wkpacket pkg/wkproto pkg/jsonrpc
git commit -m "refactor: split protocol objects into wkpacket"
```
