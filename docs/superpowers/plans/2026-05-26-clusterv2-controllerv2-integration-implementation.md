# clusterv2 ControllerV2 Integration Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `pkg/clusterv2` start and consume a ControllerV2-backed control plane while keeping `pkg/controllerv2` as the reusable implementation package.

**Architecture:** Add a focused `pkg/clusterv2/control` runtime wrapper around `pkg/controllerv2` statefile/FSM/Raft/server/sync primitives, then have `clusterv2.Node` create it by default when no test controller is injected. Control RPC encoding and handlers stay under `pkg/clusterv2/control`; root `node.go` only converts config, owns lifecycle, and applies full control snapshots.

**Tech Stack:** Go, `pkg/controllerv2`, `pkg/clusterv2/net`, etcd raft `raftpb`, JSON codecs for v1 control RPC envelopes, Go unit tests with temp dirs and in-memory RPC.

---

## Preconditions

- Work in a dedicated branch or worktree if possible.
- Do not touch unrelated current workspace changes:
  - `docs/superpowers/specs/2026-05-26-meta-table-codec-design.md`
  - `docs/superpowers/plans/2026-05-26-pkg-gateway-extraction-implementation.md`
  - `docs/superpowers/specs/2026-05-26-pkg-gateway-extraction-design.md`
- In nested `.worktrees/...` worktrees under the repository parent `go.work`, run Go test commands with `GOWORK=off` so the command uses this worktree module.
- Read before editing each package:
  - `pkg/clusterv2/FLOW.md`
  - `pkg/controllerv2/FLOW.md`
  - `docs/superpowers/specs/2026-05-26-clusterv2-controllerv2-integration-design.md`

## File Structure

Create:

- `pkg/clusterv2/config_test.go` - root config defaults and validation tests.
- `pkg/clusterv2/control/runtime.go` - ControllerV2-backed `control.Controller` implementation.
- `pkg/clusterv2/control/runtime_test.go` - voter and mirror runtime tests.
- `pkg/clusterv2/control/codec.go` - v1 ControllerV2 control RPC codecs.
- `pkg/clusterv2/control/codec_test.go` - codec round-trip and rejection tests.
- `pkg/clusterv2/control/transport.go` - Controller Raft transport, sync RPC client/server adapters, handler registration helpers.
- `pkg/clusterv2/control/transport_test.go` - transport and sync adapter tests.

Modify:

- `pkg/clusterv2/config.go` - extend `ControlConfig`, add control/slot defaults and validation.
- `pkg/clusterv2/node.go` - create default ControllerV2 runtime when no controller override exists; stop it through existing control lifecycle.
- `pkg/clusterv2/node_test.go` - add default ControllerV2 wiring coverage and adjust helpers only if required.
- `pkg/clusterv2/net/ids.go` - add a stable typed RPC service id for Controller Raft messages.
- `pkg/clusterv2/FLOW.md` - update after runtime wiring exists.
- `pkg/controllerv2/FLOW.md` - mention that `pkg/clusterv2` hosts the production-shaped integration.

Do not modify `pkg/controllerv2` unless tests reveal a missing exported primitive. Prefer adapters in `pkg/clusterv2/control`.

## Decisions Locked For This Plan

- Expose `ControlRole` and `ControlVoter` in root `pkg/clusterv2/config.go` now.
- Use typed RPC for Controller Raft in the first implementation: add `RPCControlRaft` at the end of the current RPC id list to avoid shifting existing ids.
- Keep mirror mode in the public config because the runtime and tests need it.
- If `Control.ClusterID` is empty and `Control.Voters` is empty, derive `wk-clusterv2-single-node-<NodeID>` for package-level single-node cluster tests. If voters are explicitly configured, require an explicit `ClusterID`.
- Bootstrap happens inside `control.Runtime.Start`; `clusterv2.Node` only starts `control.Controller` and applies snapshots.
- Use explicit `Control.Voters` addresses for Controller bootstrap/sync. Do not require an already-applied control snapshot to contact Controller voters.

---

### Task 1: Root Config Defaults And Validation

**Files:**
- Create: `pkg/clusterv2/config_test.go`
- Modify: `pkg/clusterv2/config.go`

- [ ] **Step 1: Write failing config tests**

Add `pkg/clusterv2/config_test.go`:

```go
package clusterv2

import (
    "errors"
    "path/filepath"
    "testing"
)

func TestConfigDefaultsSingleNodeControl(t *testing.T) {
    cfg := Config{NodeID: 1, ListenAddr: "127.0.0.1:0", DataDir: t.TempDir()}
    cfg.applyDefaults()

    if cfg.Control.StateDir != filepath.Join(cfg.DataDir, "controller") {
        t.Fatalf("Control.StateDir = %q", cfg.Control.StateDir)
    }
    if cfg.Control.Role != ControlRoleVoter {
        t.Fatalf("Control.Role = %q, want voter", cfg.Control.Role)
    }
    if cfg.Control.ClusterID != "wk-clusterv2-single-node-1" {
        t.Fatalf("Control.ClusterID = %q", cfg.Control.ClusterID)
    }
    if len(cfg.Control.Voters) != 1 || cfg.Control.Voters[0].NodeID != 1 || cfg.Control.Voters[0].Addr != cfg.ListenAddr {
        t.Fatalf("Control.Voters = %#v, want local single voter", cfg.Control.Voters)
    }
    if !cfg.Control.AllowBootstrap {
        t.Fatal("Control.AllowBootstrap = false, want true for implicit single-node cluster")
    }
    if cfg.Slots.InitialSlotCount == 0 || cfg.Slots.HashSlotCount == 0 || cfg.Slots.ReplicaCount == 0 {
        t.Fatalf("Slots defaults = %#v, want non-zero", cfg.Slots)
    }
}

func TestConfigRejectsExplicitVotersWithoutClusterID(t *testing.T) {
    cfg := Config{
        NodeID:     1,
        ListenAddr: "127.0.0.1:0",
        DataDir:    t.TempDir(),
        Control: ControlConfig{
            Voters: []ControlVoter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
        },
    }
    cfg.applyDefaults()
    if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
        t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
    }
}

func TestConfigRejectsDuplicateControlVoters(t *testing.T) {
    cfg := Config{
        NodeID:     1,
        ListenAddr: "127.0.0.1:0",
        DataDir:    t.TempDir(),
        Control: ControlConfig{
            ClusterID: "cluster-a",
            Voters: []ControlVoter{
                {NodeID: 1, Addr: "127.0.0.1:10001"},
                {NodeID: 1, Addr: "127.0.0.1:10001"},
            },
        },
    }
    cfg.applyDefaults()
    if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
        t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
    }
}

func TestConfigRejectsVoterRoleMissingLocalNode(t *testing.T) {
    cfg := Config{
        NodeID:     2,
        ListenAddr: "127.0.0.1:0",
        DataDir:    t.TempDir(),
        Control: ControlConfig{
            ClusterID: "cluster-a",
            Role:      ControlRoleVoter,
            Voters:    []ControlVoter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
        },
    }
    cfg.applyDefaults()
    if err := cfg.validate(); !errors.Is(err, ErrInvalidConfig) {
        t.Fatalf("validate() error = %v, want ErrInvalidConfig", err)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./pkg/clusterv2 -run 'TestConfig'`

Expected: FAIL because `ControlRole`, `ControlVoter`, and new defaulting/validation do not exist.

- [ ] **Step 3: Implement config types, defaults, and validation**

In `pkg/clusterv2/config.go`, add imports and types:

```go
import (
    "fmt"
    "path/filepath"
    "time"
)

// ControlRole declares how this node participates in ControllerV2.
type ControlRole string

const (
    // ControlRoleVoter runs ControllerV2 Raft and serves authoritative state.
    ControlRoleVoter ControlRole = "voter"
    // ControlRoleMirror mirrors ControllerV2 state from Controller voters.
    ControlRoleMirror ControlRole = "mirror"
)

// ControlVoter identifies a ControllerV2 Raft voter endpoint.
type ControlVoter struct {
    // NodeID is the stable non-zero node identity of the Controller voter.
    NodeID uint64
    // Addr is the cluster RPC address used to reach this Controller voter.
    Addr string
}
```

Extend `ControlConfig`:

```go
type ControlConfig struct {
    // StateDir stores ControllerV2 cluster-state files for this node.
    StateDir string
    // ClusterID is the stable cluster identity used by ControllerV2 state and sync.
    ClusterID string
    // Role declares whether this node is a Controller voter or state mirror.
    Role ControlRole
    // Voters lists Controller voter node IDs and Controller RPC addresses.
    Voters []ControlVoter
    // AllowBootstrap permits this node to initialize an empty ControllerV2 Raft log.
    AllowBootstrap bool
}
```

Add defaulting helpers:

```go
func (c *Config) applyDefaults() {
    // keep existing timeout/channel defaults
    c.applyControlDefaults()
    c.applySlotDefaults()
}

func (c *Config) applyControlDefaults() {
    if c.Control.StateDir == "" && c.DataDir != "" {
        c.Control.StateDir = filepath.Join(c.DataDir, "controller")
    }
    if c.Control.Role == "" {
        c.Control.Role = ControlRoleVoter
    }
    implicitSingleNode := len(c.Control.Voters) == 0
    if implicitSingleNode && c.NodeID != 0 && c.ListenAddr != "" {
        c.Control.Voters = []ControlVoter{{NodeID: c.NodeID, Addr: c.ListenAddr}}
        if c.Control.ClusterID == "" {
            c.Control.ClusterID = fmt.Sprintf("wk-clusterv2-single-node-%d", c.NodeID)
        }
        c.Control.AllowBootstrap = true
    }
}

func (c *Config) applySlotDefaults() {
    if c.Slots.InitialSlotCount == 0 {
        c.Slots.InitialSlotCount = 1
    }
    if c.Slots.HashSlotCount == 0 {
        c.Slots.HashSlotCount = 16
    }
    if c.Slots.ReplicaCount == 0 {
        c.Slots.ReplicaCount = uint16(len(c.Control.Voters))
        if c.Slots.ReplicaCount == 0 {
            c.Slots.ReplicaCount = 1
        }
    }
}
```

Add validation helpers and call them from `validate()`:

```go
func (c Config) validateControl() error {
    if c.Control.StateDir == "" || c.Control.ClusterID == "" {
        return ErrInvalidConfig
    }
    if c.Control.Role != ControlRoleVoter && c.Control.Role != ControlRoleMirror {
        return ErrInvalidConfig
    }
    seen := make(map[uint64]struct{}, len(c.Control.Voters))
    localFound := false
    for _, voter := range c.Control.Voters {
        if voter.NodeID == 0 || voter.Addr == "" {
            return ErrInvalidConfig
        }
        if _, ok := seen[voter.NodeID]; ok {
            return ErrInvalidConfig
        }
        seen[voter.NodeID] = struct{}{}
        if voter.NodeID == c.NodeID {
            localFound = true
        }
    }
    if len(c.Control.Voters) == 0 {
        return ErrInvalidConfig
    }
    if c.Control.Role == ControlRoleVoter && !localFound {
        return ErrInvalidConfig
    }
    return nil
}

func (c Config) validateSlots() error {
    if c.Slots.InitialSlotCount == 0 || c.Slots.HashSlotCount == 0 || c.Slots.ReplicaCount == 0 {
        return ErrInvalidConfig
    }
    if int(c.Slots.ReplicaCount) > len(c.Control.Voters) {
        return ErrInvalidConfig
    }
    return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOWORK=off go test ./pkg/clusterv2 -run 'TestConfig'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/clusterv2/config.go pkg/clusterv2/config_test.go
git commit -m "feat: add clusterv2 control config defaults"
```

---

### Task 2: ControllerV2 Control RPC Codecs

**Files:**
- Create: `pkg/clusterv2/control/codec.go`
- Create: `pkg/clusterv2/control/codec_test.go`
- Modify: `pkg/clusterv2/net/ids.go`

- [ ] **Step 1: Write failing codec tests**

Add `pkg/clusterv2/control/codec_test.go`:

```go
package control

import (
    "errors"
    "testing"

    clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
    cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
    "go.etcd.io/raft/v3/raftpb"
)

func TestControlRaftBatchCodecRoundTrip(t *testing.T) {
    input := []raftpb.Message{{From: 1, To: 2, Type: raftpb.MsgHeartbeat, Term: 3}}
    payload, err := EncodeRaftBatch(input)
    if err != nil {
        t.Fatalf("EncodeRaftBatch() error = %v", err)
    }
    got, err := DecodeRaftBatch(payload)
    if err != nil {
        t.Fatalf("DecodeRaftBatch() error = %v", err)
    }
    if len(got) != 1 || got[0].From != 1 || got[0].To != 2 || got[0].Type != raftpb.MsgHeartbeat || got[0].Term != 3 {
        t.Fatalf("DecodeRaftBatch() = %#v", got)
    }
}

func TestControlSyncCodecRoundTrip(t *testing.T) {
    req := cv2sync.GetStateRequest{ClusterID: "cluster-a", LocalRevision: 7, LocalChecksum: "crc32c:abcd"}
    payload, err := EncodeStateSyncRequest(req)
    if err != nil {
        t.Fatalf("EncodeStateSyncRequest() error = %v", err)
    }
    got, err := DecodeStateSyncRequest(payload)
    if err != nil {
        t.Fatalf("DecodeStateSyncRequest() error = %v", err)
    }
    if got != req {
        t.Fatalf("DecodeStateSyncRequest() = %#v, want %#v", got, req)
    }

    resp := cv2sync.GetStateResponse{LeaderID: 1, Revision: 8, Checksum: "crc32c:1234", Payload: []byte(`{"revision":8}`)}
    encoded, err := EncodeStateSyncResponse(resp)
    if err != nil {
        t.Fatalf("EncodeStateSyncResponse() error = %v", err)
    }
    decoded, err := DecodeStateSyncResponse(encoded)
    if err != nil {
        t.Fatalf("DecodeStateSyncResponse() error = %v", err)
    }
    if decoded.LeaderID != resp.LeaderID || decoded.Revision != resp.Revision || decoded.Checksum != resp.Checksum || string(decoded.Payload) != string(resp.Payload) {
        t.Fatalf("DecodeStateSyncResponse() = %#v, want %#v", decoded, resp)
    }
}

func TestControlCodecRejectsWrongKind(t *testing.T) {
    frame := clusternet.PutHeader(nil, controlRPCVersion, controlKindRaftBatch+99)
    if _, err := DecodeRaftBatch(frame); !errors.Is(err, clusternet.ErrInvalidFrame) {
        t.Fatalf("DecodeRaftBatch() error = %v, want ErrInvalidFrame", err)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./pkg/clusterv2/control -run 'TestControl.*Codec'`

Expected: FAIL because codec functions and constants do not exist.

- [ ] **Step 3: Implement codecs and RPC id**

In `pkg/clusterv2/net/ids.go`, append a new RPC id after the existing constants:

```go
// RPCControlRaft carries ControllerV2 Raft protocol messages.
RPCControlRaft
```

In `pkg/clusterv2/control/codec.go`:

```go
package control

import (
    "encoding/json"

    clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
    cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
    "go.etcd.io/raft/v3/raftpb"
)

const (
    controlRPCVersion uint8 = 1
    controlKindRaftBatch uint8 = 1 + iota
    controlKindStateSyncRequest
    controlKindStateSyncResponse
)

// EncodeRaftBatch encodes ControllerV2 Raft messages for clusterv2 RPC.
func EncodeRaftBatch(messages []raftpb.Message) ([]byte, error) {
    payload, err := json.Marshal(messages)
    if err != nil {
        return nil, err
    }
    out := clusternet.PutHeader(nil, controlRPCVersion, controlKindRaftBatch)
    return append(out, payload...), nil
}

// DecodeRaftBatch decodes ControllerV2 Raft messages from clusterv2 RPC.
func DecodeRaftBatch(data []byte) ([]raftpb.Message, error) {
    payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindRaftBatch)
    if err != nil {
        return nil, err
    }
    var messages []raftpb.Message
    if err := json.Unmarshal(payload, &messages); err != nil {
        return nil, err
    }
    return messages, nil
}

func EncodeStateSyncRequest(req cv2sync.GetStateRequest) ([]byte, error) {
    payload, err := json.Marshal(req)
    if err != nil {
        return nil, err
    }
    out := clusternet.PutHeader(nil, controlRPCVersion, controlKindStateSyncRequest)
    return append(out, payload...), nil
}

func DecodeStateSyncRequest(data []byte) (cv2sync.GetStateRequest, error) {
    payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindStateSyncRequest)
    if err != nil {
        return cv2sync.GetStateRequest{}, err
    }
    var req cv2sync.GetStateRequest
    if err := json.Unmarshal(payload, &req); err != nil {
        return cv2sync.GetStateRequest{}, err
    }
    return req, nil
}

func EncodeStateSyncResponse(resp cv2sync.GetStateResponse) ([]byte, error) {
    payload, err := json.Marshal(resp)
    if err != nil {
        return nil, err
    }
    out := clusternet.PutHeader(nil, controlRPCVersion, controlKindStateSyncResponse)
    return append(out, payload...), nil
}

func DecodeStateSyncResponse(data []byte) (cv2sync.GetStateResponse, error) {
    payload, err := clusternet.CheckHeader(data, controlRPCVersion, controlKindStateSyncResponse)
    if err != nil {
        return cv2sync.GetStateResponse{}, err
    }
    var resp cv2sync.GetStateResponse
    if err := json.Unmarshal(payload, &resp); err != nil {
        return cv2sync.GetStateResponse{}, err
    }
    return resp, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOWORK=off go test ./pkg/clusterv2/control -run 'TestControl.*Codec'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/clusterv2/net/ids.go pkg/clusterv2/control/codec.go pkg/clusterv2/control/codec_test.go
git commit -m "feat: add clusterv2 control rpc codecs"
```

---

### Task 3: ControllerV2 RPC Transport Adapters

**Files:**
- Create: `pkg/clusterv2/control/transport.go`
- Create: `pkg/clusterv2/control/transport_test.go`

- [ ] **Step 1: Write failing transport tests**

Add `pkg/clusterv2/control/transport_test.go`:

```go
package control

import (
    "context"
    "testing"

    clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
    cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
    "go.etcd.io/raft/v3/raftpb"
)

func TestRaftTransportSendsBatchByDestination(t *testing.T) {
    network := clusternet.NewLocalNetwork()
    stepper := &recordingRaftStepper{}
    network.Register(2, clusternet.RPCControlRaft, NewRaftHandler(stepper))

    transport := NewRaftTransport(network)
    transport.Send([]raftpb.Message{
        {From: 1, To: 2, Type: raftpb.MsgHeartbeat, Term: 4},
        {From: 1, To: 0, Type: raftpb.MsgBeat},
    })

    if len(stepper.messages) != 1 || stepper.messages[0].To != 2 || stepper.messages[0].Term != 4 {
        t.Fatalf("stepped messages = %#v", stepper.messages)
    }
}

func TestStateSyncClientCallsRemoteEndpoint(t *testing.T) {
    network := clusternet.NewLocalNetwork()
    server := cv2sync.NewServer(cv2sync.ServerConfig{
        NodeID:    1,
        ClusterID: "cluster-a",
        LeaderID:  func() uint64 { return 1 },
        Ready:     func() bool { return true },
        Snapshot:  func(context.Context) (cv2state.ClusterState, error) { return controllerV2State(), nil },
    })
    network.Register(1, clusternet.RPCControlStateSync, NewStateSyncHandler(server))

    endpoint := NewStateSyncEndpoint(network, 1)
    resp, err := endpoint.GetState(context.Background(), cv2sync.GetStateRequest{ClusterID: "cluster-a"})
    if err != nil {
        t.Fatalf("GetState() error = %v", err)
    }
    if resp.Revision == 0 || len(resp.Payload) == 0 {
        t.Fatalf("GetState() = %#v, want payload", resp)
    }
}

type recordingRaftStepper struct{ messages []raftpb.Message }

func (s *recordingRaftStepper) Step(ctx context.Context, msg raftpb.Message) error {
    if err := ctx.Err(); err != nil {
        return err
    }
    s.messages = append(s.messages, msg)
    return nil
}
```

Use the existing `controllerV2State()` helper from `controllerv2_test.go`; if the compiler cannot see it because of file ordering it is still in the same package and usable. Add this import in the test:

```go
cv2state "github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./pkg/clusterv2/control -run 'Test(RaftTransport|StateSync)'`

Expected: FAIL because transport adapters do not exist.

- [ ] **Step 3: Implement transport adapters**

In `pkg/clusterv2/control/transport.go`:

```go
package control

import (
    "context"
    "time"

    clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
    cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
    "go.etcd.io/raft/v3/raftpb"
)

const defaultControlRPCTimeout = 200 * time.Millisecond

type raftStepper interface {
    Step(context.Context, raftpb.Message) error
}

// RaftTransport sends ControllerV2 Raft messages over clusterv2 typed RPC.
type RaftTransport struct {
    caller clusternet.Caller
    timeout time.Duration
}

func NewRaftTransport(caller clusternet.Caller) *RaftTransport {
    return &RaftTransport{caller: caller, timeout: defaultControlRPCTimeout}
}

func (t *RaftTransport) Send(messages []raftpb.Message) {
    if t == nil || t.caller == nil {
        return
    }
    byNode := make(map[uint64][]raftpb.Message)
    for _, msg := range messages {
        if msg.To == 0 {
            continue
        }
        byNode[msg.To] = append(byNode[msg.To], msg)
    }
    for nodeID, batch := range byNode {
        payload, err := EncodeRaftBatch(batch)
        if err != nil {
            continue
        }
        ctx, cancel := context.WithTimeout(context.Background(), t.timeout)
        _, _ = t.caller.Call(ctx, nodeID, clusternet.RPCControlRaft, payload)
        cancel()
    }
}

func NewRaftHandler(stepper raftStepper) clusternet.Handler {
    return clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
        messages, err := DecodeRaftBatch(payload)
        if err != nil {
            return nil, err
        }
        for _, msg := range messages {
            if stepper == nil {
                continue
            }
            if err := stepper.Step(ctx, msg); err != nil {
                return nil, err
            }
        }
        return nil, nil
    })
}

// StateSyncEndpoint adapts clusterv2 RPC to controllerv2/sync.Endpoint.
type StateSyncEndpoint struct {
    caller clusternet.Caller
    nodeID uint64
}

func NewStateSyncEndpoint(caller clusternet.Caller, nodeID uint64) *StateSyncEndpoint {
    return &StateSyncEndpoint{caller: caller, nodeID: nodeID}
}

func (e *StateSyncEndpoint) GetState(ctx context.Context, req cv2sync.GetStateRequest) (cv2sync.GetStateResponse, error) {
    payload, err := EncodeStateSyncRequest(req)
    if err != nil {
        return cv2sync.GetStateResponse{}, err
    }
    resp, err := e.caller.Call(ctx, e.nodeID, clusternet.RPCControlStateSync, payload)
    if err != nil {
        return cv2sync.GetStateResponse{}, err
    }
    return DecodeStateSyncResponse(resp)
}

func NewStateSyncHandler(endpoint cv2sync.Endpoint) clusternet.Handler {
    return clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
        req, err := DecodeStateSyncRequest(payload)
        if err != nil {
            return nil, err
        }
        resp, err := endpoint.GetState(ctx, req)
        if err != nil {
            return nil, err
        }
        return EncodeStateSyncResponse(resp)
    })
}
```

If `controllerv2/raft.Transport.Send` is called inline on the raft loop, keep the RPC calls timeout-bound and best-effort. Do not block indefinitely.

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOWORK=off go test ./pkg/clusterv2/control -run 'Test(RaftTransport|StateSync)'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/clusterv2/control/transport.go pkg/clusterv2/control/transport_test.go
git commit -m "feat: add controllerv2 rpc transport adapters"
```

---

### Task 4: ControllerV2 Runtime Single-Node Voter

**Files:**
- Create: `pkg/clusterv2/control/runtime.go`
- Create/Modify: `pkg/clusterv2/control/runtime_test.go`

- [ ] **Step 1: Write failing runtime tests**

Add `pkg/clusterv2/control/runtime_test.go`:

```go
package control

import (
    "context"
    "path/filepath"
    "testing"
    "time"
)

func TestRuntimeSingleVoterBootstrapsSnapshot(t *testing.T) {
    cfg := RuntimeConfig{
        NodeID:           1,
        Addr:             "127.0.0.1:10001",
        StateDir:         t.TempDir(),
        ClusterID:        "cluster-single",
        Role:             RuntimeRoleVoter,
        Voters:           []RuntimeVoter{{NodeID: 1, Addr: "127.0.0.1:10001"}},
        AllowBootstrap:   true,
        InitialSlotCount: 1,
        HashSlotCount:    4,
        ReplicaCount:     1,
        TickInterval:     5 * time.Millisecond,
    }
    runtime, err := NewRuntime(cfg)
    if err != nil {
        t.Fatalf("NewRuntime() error = %v", err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    if err := runtime.Start(ctx); err != nil {
        t.Fatalf("Start() error = %v", err)
    }
    t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

    snap, err := runtime.LocalSnapshot(context.Background())
    if err != nil {
        t.Fatalf("LocalSnapshot() error = %v", err)
    }
    if snap.Revision == 0 || snap.ControllerID != 1 || len(snap.Slots) != 1 || snap.HashSlots.Count != 4 {
        t.Fatalf("snapshot = %#v, want bootstrapped control state", snap)
    }
    if runtime.LeaderID() != 1 {
        t.Fatalf("LeaderID() = %d, want 1", runtime.LeaderID())
    }
    if _, err := os.Stat(filepath.Join(cfg.StateDir, "cluster-state.json")); err != nil {
        t.Fatalf("cluster-state.json missing: %v", err)
    }
}

func TestRuntimeRestartReusesExistingState(t *testing.T) {
    dir := t.TempDir()
    cfg := RuntimeConfig{NodeID: 1, Addr: "n1", StateDir: dir, ClusterID: "cluster-restart", Role: RuntimeRoleVoter, Voters: []RuntimeVoter{{NodeID: 1, Addr: "n1"}}, AllowBootstrap: true, InitialSlotCount: 1, HashSlotCount: 4, ReplicaCount: 1, TickInterval: 5 * time.Millisecond}
    first, err := NewRuntime(cfg)
    if err != nil { t.Fatalf("NewRuntime(first) error = %v", err) }
    if err := first.Start(context.Background()); err != nil { t.Fatalf("Start(first) error = %v", err) }
    firstSnap, err := first.LocalSnapshot(context.Background())
    if err != nil { t.Fatalf("LocalSnapshot(first) error = %v", err) }
    if err := first.Stop(context.Background()); err != nil { t.Fatalf("Stop(first) error = %v", err) }

    second, err := NewRuntime(cfg)
    if err != nil { t.Fatalf("NewRuntime(second) error = %v", err) }
    if err := second.Start(context.Background()); err != nil { t.Fatalf("Start(second) error = %v", err) }
    t.Cleanup(func() { _ = second.Stop(context.Background()) })
    secondSnap, err := second.LocalSnapshot(context.Background())
    if err != nil { t.Fatalf("LocalSnapshot(second) error = %v", err) }

    if secondSnap.Revision != firstSnap.Revision || len(secondSnap.Slots) != len(firstSnap.Slots) {
        t.Fatalf("restart snapshot = %#v, first %#v", secondSnap, firstSnap)
    }
}
```

Add missing import in the test:

```go
import "os"
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `GOWORK=off go test ./pkg/clusterv2/control -run 'TestRuntime(SingleVoter|Restart)'`

Expected: FAIL because `RuntimeConfig`, `NewRuntime`, and runtime roles do not exist.

- [ ] **Step 3: Implement minimal voter runtime**

In `pkg/clusterv2/control/runtime.go`, implement:

```go
package control

import (
    "context"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "sync"
    "time"

    cv2command "github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
    cv2fsm "github.com/WuKongIM/WuKongIM/pkg/controllerv2/fsm"
    cv2raft "github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft"
    cv2server "github.com/WuKongIM/WuKongIM/pkg/controllerv2/server"
    cv2state "github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
    cv2statefile "github.com/WuKongIM/WuKongIM/pkg/controllerv2/statefile"
    cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
)

type RuntimeRole string

const (
    RuntimeRoleVoter RuntimeRole = "voter"
    RuntimeRoleMirror RuntimeRole = "mirror"
)

type RuntimeVoter struct {
    NodeID uint64
    Addr string
}

type RuntimeConfig struct {
    NodeID uint64
    Addr string
    StateDir string
    ClusterID string
    Role RuntimeRole
    Voters []RuntimeVoter
    AllowBootstrap bool
    InitialSlotCount uint32
    HashSlotCount uint16
    ReplicaCount uint16
    TickInterval time.Duration
    RaftTransport cv2raft.Transport
    SyncClient *cv2sync.Client
    Now func() time.Time
}

type Runtime struct {
    cfg RuntimeConfig
    mu sync.RWMutex
    snapshot Snapshot
    watch chan SnapshotEvent
    store *cv2statefile.Store
    sm *cv2fsm.StateMachine
    raft *cv2raft.Service
    server *cv2server.Server
}
```

Core methods:

```go
func NewRuntime(cfg RuntimeConfig) (*Runtime, error) {
    if cfg.Role == "" { cfg.Role = RuntimeRoleVoter }
    if cfg.TickInterval == 0 { cfg.TickInterval = 20 * time.Millisecond }
    if cfg.Now == nil { cfg.Now = time.Now }
    if cfg.NodeID == 0 || cfg.StateDir == "" || cfg.ClusterID == "" || len(cfg.Voters) == 0 {
        return nil, fmt.Errorf("control runtime: invalid config")
    }
    return &Runtime{cfg: cfg, watch: make(chan SnapshotEvent, 16)}, nil
}

func (r *Runtime) Start(ctx context.Context) error {
    if ctx == nil { ctx = context.Background() }
    if err := os.MkdirAll(r.cfg.StateDir, 0o755); err != nil { return err }
    r.store = cv2statefile.New(filepath.Join(r.cfg.StateDir, "cluster-state.json"))
    switch r.cfg.Role {
    case RuntimeRoleVoter:
        return r.startVoter(ctx)
    case RuntimeRoleMirror:
        return r.startMirror(ctx)
    default:
        return fmt.Errorf("control runtime: invalid role %q", r.cfg.Role)
    }
}
```

Voter path outline:

```go
func (r *Runtime) startVoter(ctx context.Context) error {
    sm, err := cv2fsm.New(r.store)
    if err != nil { return err }
    transport := r.cfg.RaftTransport
    if transport == nil { transport = noopRaftTransport{} }
    service, err := cv2raft.NewService(cv2raft.Config{
        NodeID: r.cfg.NodeID,
        Peers: r.raftPeers(),
        AllowBootstrap: r.cfg.AllowBootstrap,
        RaftDir: filepath.Join(r.cfg.StateDir, "raft"),
        StateMachine: sm,
        Transport: transport,
        TickInterval: r.cfg.TickInterval,
    })
    if err != nil { return err }
    if err := service.Start(ctx); err != nil { return err }
    r.sm, r.raft = sm, service
    srv, err := cv2server.New(cv2server.Config{StateSource: sm, Proposer: service, Now: r.cfg.Now})
    if err != nil { _ = service.Stop(); return err }
    r.server = srv
    if err := r.bootstrapIfNeeded(ctx); err != nil { _ = service.Stop(); return err }
    return r.publishFromState(ctx)
}
```

Bootstrap helpers:

```go
func (r *Runtime) bootstrapIfNeeded(ctx context.Context) error {
    st := r.sm.Snapshot(ctx)
    if st.Revision != 0 {
        return r.runBootstrapPlanner(ctx)
    }
    if !r.cfg.AllowBootstrap {
        return errors.New("control runtime: empty state and bootstrap disabled")
    }
    if err := r.waitLocalLeader(ctx); err != nil { return err }
    if err := r.raft.Propose(ctx, r.initCommand()); err != nil { return err }
    return r.runBootstrapPlanner(ctx)
}

func (r *Runtime) runBootstrapPlanner(ctx context.Context) error {
    for i := uint32(0); i < r.cfg.InitialSlotCount; i++ {
        before := r.sm.Snapshot(ctx)
        if len(before.Slots) >= int(r.cfg.InitialSlotCount) {
            return nil
        }
        if err := r.server.TickPlanner(ctx); err != nil { return err }
        after := r.sm.Snapshot(ctx)
        if after.Revision == before.Revision {
            return nil
        }
    }
    return nil
}

func (r *Runtime) waitLocalLeader(ctx context.Context) error {
    ticker := time.NewTicker(r.cfg.TickInterval)
    defer ticker.Stop()
    for {
        if r.raft.LeaderID() == r.cfg.NodeID || r.raft.Status().Role == cv2raft.RoleLeader { return nil }
        select {
        case <-ctx.Done(): return ctx.Err()
        case <-ticker.C:
        }
    }
}
```

State construction helpers:

```go
func (r *Runtime) initCommand() cv2command.Command {
    controllers := make([]cv2state.ControllerVoter, 0, len(r.cfg.Voters))
    nodes := make([]cv2state.Node, 0, len(r.cfg.Voters))
    for _, voter := range r.cfg.Voters {
        controllers = append(controllers, cv2state.ControllerVoter{NodeID: voter.NodeID, Addr: voter.Addr, Role: cv2state.ControllerRoleVoter})
        nodes = append(nodes, cv2state.Node{NodeID: voter.NodeID, Addr: voter.Addr, Roles: []cv2state.NodeRole{cv2state.NodeRoleControllerVoter, cv2state.NodeRoleData}, JoinState: cv2state.NodeJoinStateActive, Status: cv2state.NodeStatusAlive, CapacityWeight: 1})
    }
    return cv2command.Command{Kind: cv2command.KindInitClusterState, IssuedAt: r.cfg.Now().UTC(), Init: &cv2command.InitClusterState{ClusterID: r.cfg.ClusterID, Config: cv2state.ClusterConfig{SlotCount: r.cfg.InitialSlotCount, HashSlotCount: r.cfg.HashSlotCount, ReplicaCount: r.cfg.ReplicaCount, DefaultCapacityWeight: 1}, Controllers: controllers, Nodes: nodes}}
}

func (r *Runtime) raftPeers() []cv2raft.Peer {
    peers := make([]cv2raft.Peer, 0, len(r.cfg.Voters))
    for _, voter := range r.cfg.Voters {
        peers = append(peers, cv2raft.Peer{NodeID: voter.NodeID, Addr: voter.Addr})
    }
    return peers
}
```

Controller interface methods:

```go
func (r *Runtime) Stop(ctx context.Context) error {
    if ctx != nil && ctx.Err() != nil { return ctx.Err() }
    if r.raft != nil { return r.raft.Stop() }
    return nil
}

func (r *Runtime) LocalSnapshot(ctx context.Context) (Snapshot, error) {
    if ctx != nil && ctx.Err() != nil { return Snapshot{}, ctx.Err() }
    r.mu.RLock(); defer r.mu.RUnlock()
    return r.snapshot.Clone(), nil
}

func (r *Runtime) LeaderID() uint64 {
    if r.raft != nil { return r.raft.LeaderID() }
    return r.snapshot.ControllerID
}

func (r *Runtime) Step(ctx context.Context, msg raftpb.Message) error {
    if r.raft == nil { return nil }
    return r.raft.Step(ctx, msg)
}

func (r *Runtime) Watch() <-chan SnapshotEvent { return r.watch }

func (r *Runtime) ReportNode(ctx context.Context, report NodeReport) error { if ctx != nil { return ctx.Err() }; return nil }
func (r *Runtime) ReportSlots(ctx context.Context, report SlotRuntimeReport) error { if ctx != nil { return ctx.Err() }; return nil }

func (r *Runtime) publishFromState(ctx context.Context) error {
    st := r.sm.Snapshot(ctx)
    snap, err := SnapshotFromControllerV2(st)
    if err != nil { return err }
    r.mu.Lock()
    r.snapshot = snap.Clone()
    r.mu.Unlock()
    select { case r.watch <- SnapshotEvent{Snapshot: snap.Clone()}: default: }
    return nil
}

type noopRaftTransport struct{}
func (noopRaftTransport) Send([]raftpb.Message) {}
```

Adjust names/imports to satisfy `gofmt` and avoid import aliases that collide with the local `sync` package.

- [ ] **Step 4: Run tests to verify they pass**

Run: `GOWORK=off go test ./pkg/clusterv2/control -run 'TestRuntime(SingleVoter|Restart)'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/clusterv2/control/runtime.go pkg/clusterv2/control/runtime_test.go
git commit -m "feat: add controllerv2-backed control runtime"
```

---

### Task 5: Wire Default Control Runtime Into Node

**Files:**
- Modify: `pkg/clusterv2/node.go`
- Modify: `pkg/clusterv2/node_test.go`

- [ ] **Step 1: Write failing node test**

Add to `pkg/clusterv2/node_test.go`:

```go
func TestNodeInitializesDefaultControllerV2WhenOptionMissing(t *testing.T) {
    cfg := validNodeConfig(t)
    cfg.Channel.TickInterval = time.Millisecond
    cfg.Control.ClusterID = "node-default-control"
    cfg.Slots.InitialSlotCount = 1
    cfg.Slots.HashSlotCount = 4
    cfg.Slots.ReplicaCount = 1

    node, err := New(cfg)
    if err != nil {
        t.Fatalf("New() error = %v", err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    if err := node.Start(ctx); err != nil {
        t.Fatalf("Start() error = %v", err)
    }
    t.Cleanup(func() { _ = node.Stop(context.Background()) })

    snap := node.Snapshot()
    if !snap.RoutesReady || snap.StateRevision == 0 || snap.SlotCount != 1 || snap.HashSlotCount != 4 {
        t.Fatalf("Snapshot() = %#v, want ControllerV2-backed ready routes", snap)
    }
    route, err := node.RouteHashSlot(0)
    if err != nil && !errors.Is(err, ErrNoSlotLeader) {
        t.Fatalf("RouteHashSlot() error = %v, want route or no slot leader", err)
    }
    if err == nil && route.SlotID != 1 {
        t.Fatalf("RouteHashSlot() = %#v, want slot 1", route)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./pkg/clusterv2 -run TestNodeInitializesDefaultControllerV2WhenOptionMissing`

Expected: FAIL because `Node.Start` still allows `control` to be nil and does not create the default runtime.

- [ ] **Step 3: Implement default runtime creation**

In `pkg/clusterv2/node.go`:

- Change `ensureDefaultRuntime()` to also create the control runtime before `Start` calls `n.control.Start(ctx)`.
- Add a helper that converts root config to control runtime config.

Snippet:

```go
func (n *Node) ensureDefaultRuntime() (bool, error) {
    if n.control == nil {
        runtime, err := control.NewRuntime(control.RuntimeConfig{
            NodeID:           n.cfg.NodeID,
            Addr:             n.cfg.ListenAddr,
            StateDir:         n.cfg.Control.StateDir,
            ClusterID:        n.cfg.Control.ClusterID,
            Role:             control.RuntimeRole(n.cfg.Control.Role),
            Voters:           convertControlVoters(n.cfg.Control.Voters),
            AllowBootstrap:   n.cfg.Control.AllowBootstrap,
            InitialSlotCount: n.cfg.Slots.InitialSlotCount,
            HashSlotCount:    n.cfg.Slots.HashSlotCount,
            ReplicaCount:     n.cfg.Slots.ReplicaCount,
        })
        if err != nil {
            return false, err
        }
        n.control = runtime
    }
    // keep existing proposer and channel default creation
}

func convertControlVoters(in []ControlVoter) []control.RuntimeVoter {
    out := make([]control.RuntimeVoter, 0, len(in))
    for _, voter := range in {
        out = append(out, control.RuntimeVoter{NodeID: voter.NodeID, Addr: voter.Addr})
    }
    return out
}
```

Keep `withController(control.NewStaticController(...))` behavior unchanged: if tests or harnesses inject a controller, do not create the default runtime.

- [ ] **Step 4: Run focused tests**

Run: `GOWORK=off go test ./pkg/clusterv2 -run 'TestNode(InitializesDefaultControllerV2|StartAppliesControlSnapshot|ControlWatchUpdatesRouteRevision)'`

Expected: PASS.

- [ ] **Step 5: Run package tests**

Run: `GOWORK=off go test ./pkg/clusterv2/...`

Expected: PASS. If runtime startup makes existing tests too slow, adjust tests to inject `control.NewStaticController(...)` only where the test does not care about default control runtime. Do not add a production bypass branch.

- [ ] **Step 6: Commit**

```bash
git add pkg/clusterv2/node.go pkg/clusterv2/node_test.go
git commit -m "feat: wire default controllerv2 runtime into clusterv2 node"
```

---

### Task 6: Mirror Runtime And Full-File Sync

**Files:**
- Modify: `pkg/clusterv2/control/runtime.go`
- Modify: `pkg/clusterv2/control/runtime_test.go`
- Modify: `pkg/clusterv2/control/transport.go`

- [ ] **Step 1: Write failing mirror test**

Add to `pkg/clusterv2/control/runtime_test.go`:

```go
func TestRuntimeMirrorSyncsLeaderState(t *testing.T) {
    network := clusternet.NewLocalNetwork()
    leaderState := controllerV2State()
    syncServer := cv2sync.NewServer(cv2sync.ServerConfig{
        NodeID:    1,
        ClusterID: leaderState.ClusterID,
        LeaderID:  func() uint64 { return 1 },
        Ready:     func() bool { return true },
        Snapshot:  func(context.Context) (cv2state.ClusterState, error) { return leaderState, nil },
    })
    network.Register(1, clusternet.RPCControlStateSync, NewStateSyncHandler(syncServer))

    runtime, err := NewRuntime(RuntimeConfig{
        NodeID:     4,
        Addr:       "n4",
        StateDir:   t.TempDir(),
        ClusterID:  leaderState.ClusterID,
        Role:       RuntimeRoleMirror,
        Voters:     []RuntimeVoter{{NodeID: 1, Addr: "n1"}},
        SyncPeers:  NewStaticPeerPicker(network, []RuntimeVoter{{NodeID: 1, Addr: "n1"}}),
    })
    if err != nil { t.Fatalf("NewRuntime() error = %v", err) }
    if err := runtime.Start(context.Background()); err != nil { t.Fatalf("Start() error = %v", err) }
    t.Cleanup(func() { _ = runtime.Stop(context.Background()) })

    snap, err := runtime.LocalSnapshot(context.Background())
    if err != nil { t.Fatalf("LocalSnapshot() error = %v", err) }
    if snap.Revision != leaderState.Revision || len(snap.Nodes) != len(leaderState.Nodes) {
        t.Fatalf("mirror snapshot = %#v, want revision %d", snap, leaderState.Revision)
    }
}
```

Add imports:

```go
clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
cv2state "github.com/WuKongIM/WuKongIM/pkg/controllerv2/state"
cv2sync "github.com/WuKongIM/WuKongIM/pkg/controllerv2/sync"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./pkg/clusterv2/control -run TestRuntimeMirrorSyncsLeaderState`

Expected: FAIL because mirror runtime and peer picker helpers do not exist.

- [ ] **Step 3: Implement mirror runtime and peer picker**

Extend `RuntimeConfig`:

```go
SyncPeers cv2sync.PeerPicker
```

Implement `startMirror`:

```go
func (r *Runtime) startMirror(ctx context.Context) error {
    peers := r.cfg.SyncPeers
    if peers == nil {
        return errors.New("control runtime: mirror sync peers required")
    }
    client := cv2sync.NewClient(cv2sync.ClientConfig{ClusterID: r.cfg.ClusterID, Store: r.store, Peers: peers})
    srv, err := cv2server.New(cv2server.Config{SyncClient: syncClientAdapter{client: client}})
    if err != nil { return err }
    r.server = srv
    if err := srv.SyncOnce(ctx); err != nil { return err }
    st := srv.LocalState()
    snap, err := SnapshotFromControllerV2(st)
    if err != nil { return err }
    r.mu.Lock(); r.snapshot = snap.Clone(); r.mu.Unlock()
    select { case r.watch <- SnapshotEvent{Snapshot: snap.Clone()}: default: }
    return nil
}

type syncClientAdapter struct { client *cv2sync.Client }
func (a syncClientAdapter) SyncOnce(ctx context.Context) (cv2state.ClusterState, error) {
    if err := a.client.SyncOnce(ctx); err != nil { return cv2state.ClusterState{}, err }
    st, ok := a.client.LocalState()
    if !ok { return cv2state.ClusterState{}, errors.New("control runtime: sync produced no state") }
    return st, nil
}
```

Add a static peer picker helper in `transport.go`:

```go
type StaticPeerPicker struct {
    endpoints map[uint64]cv2sync.Endpoint
    ids []uint64
}

func NewStaticPeerPicker(caller clusternet.Caller, voters []RuntimeVoter) *StaticPeerPicker {
    p := &StaticPeerPicker{endpoints: make(map[uint64]cv2sync.Endpoint, len(voters)), ids: make([]uint64, 0, len(voters))}
    for _, voter := range voters {
        p.ids = append(p.ids, voter.NodeID)
        p.endpoints[voter.NodeID] = NewStateSyncEndpoint(caller, voter.NodeID)
    }
    return p
}

func (p *StaticPeerPicker) Endpoint(nodeID uint64) (cv2sync.Endpoint, bool) { ep, ok := p.endpoints[nodeID]; return ep, ok }
func (p *StaticPeerPicker) PeerIDs() []uint64 { return append([]uint64(nil), p.ids...) }
```

- [ ] **Step 4: Run mirror tests**

Run: `GOWORK=off go test ./pkg/clusterv2/control -run 'TestRuntime(Mirror|SingleVoter|Restart)'`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/clusterv2/control/runtime.go pkg/clusterv2/control/runtime_test.go pkg/clusterv2/control/transport.go
git commit -m "feat: add controllerv2 mirror sync runtime"
```

---

### Task 7: Three-Voter In-Process Control Runtime Test

**Files:**
- Modify: `pkg/clusterv2/control/runtime_test.go`

- [ ] **Step 1: Write failing three-voter test**

Add to `pkg/clusterv2/control/runtime_test.go`:

```go
func TestRuntimeThreeVotersConverge(t *testing.T) {
    network := clusternet.NewLocalNetwork()
    voters := []RuntimeVoter{{NodeID: 1, Addr: "n1"}, {NodeID: 2, Addr: "n2"}, {NodeID: 3, Addr: "n3"}}
    runtimes := make([]*Runtime, 0, len(voters))
    for _, voter := range voters {
        rt, err := NewRuntime(RuntimeConfig{
            NodeID:           voter.NodeID,
            Addr:             voter.Addr,
            StateDir:         t.TempDir(),
            ClusterID:        "cluster-three",
            Role:             RuntimeRoleVoter,
            Voters:           voters,
            AllowBootstrap:   true,
            InitialSlotCount: 1,
            HashSlotCount:    4,
            ReplicaCount:     3,
            TickInterval:     10 * time.Millisecond,
            RaftTransport:    NewRaftTransport(network),
        })
        if err != nil { t.Fatalf("NewRuntime(%d) error = %v", voter.NodeID, err) }
        network.Register(voter.NodeID, clusternet.RPCControlRaft, NewRaftHandler(rt))
        runtimes = append(runtimes, rt)
    }
    for _, rt := range runtimes {
        if err := rt.Start(context.Background()); err != nil { t.Fatalf("Start(%d) error = %v", rt.cfg.NodeID, err) }
        t.Cleanup(func() { _ = rt.Stop(context.Background()) })
    }

    deadline := time.Now().Add(5 * time.Second)
    for time.Now().Before(deadline) {
        ready := true
        var revision uint64
        for _, rt := range runtimes {
            snap, err := rt.LocalSnapshot(context.Background())
            if err != nil || snap.Revision == 0 || len(snap.Slots) != 1 || snap.ControllerID == 0 {
                ready = false
                break
            }
            if revision == 0 { revision = snap.Revision }
            if snap.Revision != revision { ready = false; break }
        }
        if ready { return }
        time.Sleep(10 * time.Millisecond)
    }
    for _, rt := range runtimes {
        snap, _ := rt.LocalSnapshot(context.Background())
        t.Logf("runtime %d snapshot: %#v", rt.cfg.NodeID, snap)
    }
    t.Fatal("runtimes did not converge")
}
```

- [ ] **Step 2: Run test to verify it fails or exposes missing refresh behavior**

Run: `GOWORK=off go test ./pkg/clusterv2/control -run TestRuntimeThreeVotersConverge -count=1`

Expected before fixes: likely FAIL because follower runtimes do not refresh/publish after remote Raft apply.

- [ ] **Step 3: Add runtime refresh loop or apply notification polling**

Because `controllerv2/raft.Service` does not currently expose apply callbacks, keep this slice simple and bounded:

- After voter `Start`, start a low-frequency internal refresh loop that polls `sm.Snapshot(ctx)` every `TickInterval * 5`.
- If the observed revision/checksum changes, call `publishFromState(ctx)`.
- Stop the loop in `Stop` before stopping raft.

Implementation fields:

```go
refreshCancel context.CancelFunc
refreshWG sync.WaitGroup
lastChecksum string
```

Implementation sketch:

```go
func (r *Runtime) startRefreshLoop() {
    ctx, cancel := context.WithCancel(context.Background())
    r.refreshCancel = cancel
    r.refreshWG.Add(1)
    go func() {
        defer r.refreshWG.Done()
        ticker := time.NewTicker(r.cfg.TickInterval * 5)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done(): return
            case <-ticker.C:
                _ = r.publishIfChanged(ctx)
            }
        }
    }()
}

func (r *Runtime) publishIfChanged(ctx context.Context) error {
    if r.sm == nil { return nil }
    st := r.sm.Snapshot(ctx)
    if st.Revision == 0 { return nil }
    r.mu.RLock()
    currentRevision := r.snapshot.Revision
    r.mu.RUnlock()
    if st.Revision == currentRevision { return nil }
    return r.publishFromState(ctx)
}
```

Call `startRefreshLoop()` after `publishFromState` in `startVoter`. In `Stop`, cancel and wait before stopping raft.

- [ ] **Step 4: Run three-voter test**

Run: `GOWORK=off go test ./pkg/clusterv2/control -run TestRuntimeThreeVotersConverge -count=1`

Expected: PASS.

- [ ] **Step 5: Run control package tests**

Run: `GOWORK=off go test ./pkg/clusterv2/control -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/clusterv2/control/runtime.go pkg/clusterv2/control/runtime_test.go
git commit -m "test: cover controllerv2 runtime convergence"
```

---

### Task 8: Documentation And Final Verification

**Files:**
- Modify: `pkg/clusterv2/FLOW.md`
- Modify: `pkg/controllerv2/FLOW.md`
- Optional Modify: `docs/development/PROJECT_KNOWLEDGE.md` only if implementation discovers a durable project rule.

- [ ] **Step 1: Update `pkg/clusterv2/FLOW.md`**

Change the V1 limitation that says full ControllerV2 Raft bootstrap is left to production wiring. Replace it with a current limitation such as:

```markdown
- ControllerV2 integration now supports ControllerV2-backed runtime startup, single-node cluster bootstrap, mirror sync, and in-process multi-voter convergence tests. Production app config wiring and operator workflows remain outside this package-level slice.
```

- [ ] **Step 2: Update `pkg/controllerv2/FLOW.md`**

Add one sentence under Responsibility or Server Facade Flow:

```markdown
`pkg/clusterv2/control` hosts the production-shaped integration wrapper; `pkg/controllerv2` remains the reusable Controller engine and does not import `pkg/clusterv2`.
```

- [ ] **Step 3: Run formatting**

Run: `gofmt -w pkg/clusterv2/config.go pkg/clusterv2/config_test.go pkg/clusterv2/node.go pkg/clusterv2/node_test.go pkg/clusterv2/control/*.go pkg/clusterv2/net/ids.go`

Expected: command exits 0.

- [ ] **Step 4: Run focused tests**

Run: `GOWORK=off go test ./pkg/clusterv2/... ./pkg/controllerv2/...`

Expected: PASS.

- [ ] **Step 5: Run broader related tests**

Run: `GOWORK=off go test ./internal/... ./pkg/...`

Expected: PASS. If this is too slow locally, run at minimum `go test ./pkg/clusterv2/... ./pkg/controllerv2/... ./pkg/channelv2/... ./pkg/slot/...` and record the reduced scope in the final response.

- [ ] **Step 6: Inspect git diff**

Run: `git status --short && git diff --stat`

Expected: only files listed in this plan are modified, plus optional `PROJECT_KNOWLEDGE.md` if a new durable rule was added.

- [ ] **Step 7: Commit docs and final integration**

```bash
git add pkg/clusterv2/FLOW.md pkg/controllerv2/FLOW.md
git commit -m "docs: update clusterv2 controllerv2 flow"
```

If Tasks 4-7 were squashed or not committed individually, create a final feature commit with all remaining implementation files:

```bash
git add pkg/clusterv2/config.go pkg/clusterv2/config_test.go pkg/clusterv2/node.go pkg/clusterv2/node_test.go pkg/clusterv2/net/ids.go pkg/clusterv2/control/*.go pkg/clusterv2/FLOW.md pkg/controllerv2/FLOW.md
git commit -m "feat: integrate controllerv2 into clusterv2"
```

---

## Final Acceptance Criteria

- `pkg/clusterv2.New(Config{NodeID, ListenAddr, DataDir})` can start as a single-node cluster and apply a ControllerV2-backed control snapshot.
- Existing tests that inject `control.StaticController` still bypass only the test control implementation, not production cluster semantics.
- `pkg/controllerv2` does not import `pkg/clusterv2`.
- ControllerV2 state sync and Controller Raft messages flow through `pkg/clusterv2/net` typed RPC adapters.
- `RouteHashSlot` reads the route snapshot produced from ControllerV2 state; it does not call ControllerV2 synchronously.
- Restart from an existing state directory reuses the existing `cluster-state.json` and does not create a second cluster.
- Unit tests remain short; any slow real-network or multi-process coverage is tagged as integration.
