# Cluster Idle CPU Code Optimization Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available and explicitly permitted) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce idle CPU per `wk-node` toward `<1%` without slowing Raft tick/election timing or changing operator configuration.

**Architecture:** First remove avoidable `RawNode.Status()` allocation/cloning from the slot runtime hot path. Then add a cluster-specific Raft batch transport frame so one target peer receives one frame per batch instead of one frame per slot message. Finally cache common transport byte metric counters to remove Prometheus label lookup from the hottest send/receive path.

**Tech Stack:** Go 1.23, `go.etcd.io/raft/v3`, existing `pkg/transport` framed TCP transport, Prometheus client metrics, `go test`, `go tool pprof`.

---

## File Structure

- Modify: `pkg/slot/multiraft/slot.go`
  - Change cached status refresh from `RawNode.Status()` to `RawNode.BasicStatus()`.
  - Avoid duplicate status refresh after `processReady()` already refreshes.
- Test: `pkg/slot/multiraft/status_refresh_test.go`
  - Same-package test that locks the performance contract and basic status behavior.
- Modify: `pkg/cluster/codec.go`
  - Add `msgTypeRaftBatch` constant.
  - Add Raft batch encode/decode helpers.
- Modify: `pkg/cluster/transport.go`
  - Group outgoing `multiraft.Envelope` values by target node and send one batch frame per target.
- Modify: `pkg/cluster/cluster.go`
  - Register `msgTypeRaftBatch` in both transport startup paths.
  - Add a batch handler and shared single-record helper.
- Modify: `pkg/cluster/transport_glue.go`
  - Register `msgTypeRaftBatch` in the transport-layer composition path.
- Test: `pkg/cluster/codec_raft_batch_test.go`
  - Round-trip and malformed body tests for the batch format.
- Test: `pkg/cluster/transport_test.go`
  - Update existing tests and add a same-target batching test.
- Modify: `pkg/metrics/transport.go`
  - Cache common `sentBytes` / `receivedBytes` counters.
- Test: `pkg/metrics/registry_test.go`
  - Add assertions that cached labels and fallback labels produce identical public metric output.
- Modify: `pkg/cluster/FLOW.md`
  - Document Slot Raft batch frame behavior.
- Reference: `docs/superpowers/specs/2026-04-27-cluster-idle-cpu-code-optimization-design.md`

---

### Task 1: Make Slot Status Refresh Allocation-Friendly

**Files:**
- Modify: `pkg/slot/multiraft/slot.go`
- Create: `pkg/slot/multiraft/status_refresh_test.go`

- [ ] **Step 1: Write the failing status-refresh guard test**

Create `pkg/slot/multiraft/status_refresh_test.go` with package `multiraft` so the test can inspect unexported implementation details. The test intentionally guards the performance contract because this optimization is specifically about avoiding `RawNode.Status()`.

```go
package multiraft

import (
    "go/ast"
    "go/parser"
    "go/token"
    "testing"
)

func TestRefreshStatusUsesBasicStatus(t *testing.T) {
    fset := token.NewFileSet()
    file, err := parser.ParseFile(fset, "slot.go", nil, 0)
    if err != nil {
        t.Fatalf("ParseFile() error = %v", err)
    }

    var foundRefresh bool
    ast.Inspect(file, func(n ast.Node) bool {
        fn, ok := n.(*ast.FuncDecl)
        if !ok || fn.Name.Name != "refreshStatus" {
            return true
        }
        foundRefresh = true
        ast.Inspect(fn.Body, func(inner ast.Node) bool {
            call, ok := inner.(*ast.CallExpr)
            if !ok {
                return true
            }
            selector, ok := call.Fun.(*ast.SelectorExpr)
            if !ok {
                return true
            }
            if selector.Sel.Name == "Status" {
                t.Fatalf("refreshStatus must use RawNode.BasicStatus, not RawNode.Status")
            }
            return true
        })
        return false
    })

    if !foundRefresh {
        t.Fatal("refreshStatus function not found")
    }
}
```

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
go test ./pkg/slot/multiraft -run TestRefreshStatusUsesBasicStatus -count=1
```

Expected: FAIL with `refreshStatus must use RawNode.BasicStatus`.

- [ ] **Step 3: Implement minimal BasicStatus refresh**

Modify `pkg/slot/multiraft/slot.go`:

```go
func (g *slot) refreshStatus() {
    st := g.rawNode.BasicStatus()
    g.mu.Lock()
    defer g.mu.Unlock()
    prevRole := g.status.Role
    g.status.LeaderID = NodeID(st.Lead)
    g.status.Term = st.Term
    g.status.CommitIndex = st.Commit
    g.status.AppliedIndex = st.Applied
    g.status.Role = mapRole(st.RaftState)
    if prevRole == RoleLeader && g.status.Role != RoleLeader {
        g.failLeadershipDependentLocked(ErrNotLeader)
    }
}
```

Then remove the duplicate refresh in `processSlot` when `processReady` returns true:

```go
if g.processReady(context.Background(), r.opts.Transport) {
    return true
}
g.refreshStatus()
return false
```

Do not remove the refresh inside `processReady`; it keeps committed/apply status fresh after Ready processing.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run:

```bash
go test ./pkg/slot/multiraft -run 'TestRefreshStatusUsesBasicStatus|Test.*Status|Test.*Proposal|Test.*Leader' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 1**

```bash
git add pkg/slot/multiraft/slot.go pkg/slot/multiraft/status_refresh_test.go
git commit -m "perf: use basic raft status in slot runtime"
```

---

### Task 2: Add Raft Batch Codec

**Files:**
- Modify: `pkg/cluster/codec.go`
- Create: `pkg/cluster/codec_raft_batch_test.go`

- [ ] **Step 1: Write failing Raft batch codec tests**

Create `pkg/cluster/codec_raft_batch_test.go`:

```go
package cluster

import (
    "testing"

    "github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
    "go.etcd.io/raft/v3/raftpb"
)

func TestRaftBatchBodyRoundTrip(t *testing.T) {
    input := []multiraft.Envelope{
        {SlotID: 1, Message: raftpb.Message{From: 1, To: 2, Term: 3, Type: raftpb.MsgHeartbeat}},
        {SlotID: 9, Message: raftpb.Message{From: 1, To: 2, Term: 3, Type: raftpb.MsgApp, Index: 7}},
    }

    body, err := encodeRaftBatchBody(input)
    if err != nil {
        t.Fatalf("encodeRaftBatchBody() error = %v", err)
    }
    got, err := decodeRaftBatchBody(body)
    if err != nil {
        t.Fatalf("decodeRaftBatchBody() error = %v", err)
    }
    if len(got) != len(input) {
        t.Fatalf("decoded len = %d, want %d", len(got), len(input))
    }
    for i := range input {
        if got[i].SlotID != input[i].SlotID {
            t.Fatalf("record %d SlotID = %d, want %d", i, got[i].SlotID, input[i].SlotID)
        }
        if got[i].Message.Type != input[i].Message.Type || got[i].Message.To != input[i].Message.To {
            t.Fatalf("record %d Message = %+v, want %+v", i, got[i].Message, input[i].Message)
        }
    }
}

func TestDecodeRaftBatchBodyRejectsMalformedPayload(t *testing.T) {
    malformed := []byte{0, 0, 0, 1, 0, 0, 0}
    if _, err := decodeRaftBatchBody(malformed); err == nil {
        t.Fatal("decodeRaftBatchBody() error = nil, want malformed error")
    }
}
```

- [ ] **Step 2: Run tests and verify RED**

Run:

```bash
go test ./pkg/cluster -run 'TestRaftBatchBodyRoundTrip|TestDecodeRaftBatchBodyRejectsMalformedPayload' -count=1
```

Expected: FAIL because `encodeRaftBatchBody` / `decodeRaftBatchBody` are undefined.

- [ ] **Step 3: Implement the codec**

Modify `pkg/cluster/codec.go`:

```go
const (
    msgTypeRaft            uint8 = 1
    msgTypeObservationHint uint8 = 2
    msgTypeRaftBatch       uint8 = 4
)

type raftBatchRecord struct {
    SlotID  multiraft.SlotID
    Message raftpb.Message
}

func encodeRaftBatchBody(batch []multiraft.Envelope) ([]byte, error) {
    var size int
    encoded := make([][]byte, len(batch))
    for i, env := range batch {
        data, err := env.Message.Marshal()
        if err != nil {
            return nil, err
        }
        encoded[i] = data
        size += 8 + 4 + len(data)
    }

    body := make([]byte, 4+size)
    binary.BigEndian.PutUint32(body[:4], uint32(len(batch)))
    offset := 4
    for i, env := range batch {
        binary.BigEndian.PutUint64(body[offset:offset+8], uint64(env.SlotID))
        offset += 8
        binary.BigEndian.PutUint32(body[offset:offset+4], uint32(len(encoded[i])))
        offset += 4
        copy(body[offset:offset+len(encoded[i])], encoded[i])
        offset += len(encoded[i])
    }
    return body, nil
}

func decodeRaftBatchBody(body []byte) ([]raftBatchRecord, error) {
    if len(body) < 4 {
        return nil, fmt.Errorf("raft batch body too short: %d", len(body))
    }
    count := binary.BigEndian.Uint32(body[:4])
    records := make([]raftBatchRecord, 0, count)
    offset := 4
    for i := uint32(0); i < count; i++ {
        if len(body)-offset < 12 {
            return nil, fmt.Errorf("raft batch record %d header too short", i)
        }
        slotID := binary.BigEndian.Uint64(body[offset : offset+8])
        offset += 8
        messageLen := int(binary.BigEndian.Uint32(body[offset : offset+4]))
        offset += 4
        if messageLen < 0 || len(body)-offset < messageLen {
            return nil, fmt.Errorf("raft batch record %d message too short", i)
        }
        var msg raftpb.Message
        if err := msg.Unmarshal(body[offset : offset+messageLen]); err != nil {
            return nil, err
        }
        offset += messageLen
        records = append(records, raftBatchRecord{SlotID: multiraft.SlotID(slotID), Message: msg})
    }
    if offset != len(body) {
        return nil, fmt.Errorf("raft batch has %d trailing bytes", len(body)-offset)
    }
    return records, nil
}
```

Add imports to `codec.go`:

```go
"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
"go.etcd.io/raft/v3/raftpb"
```

- [ ] **Step 4: Run codec tests and verify GREEN**

Run:

```bash
go test ./pkg/cluster -run 'TestRaftBatchBodyRoundTrip|TestDecodeRaftBatchBodyRejectsMalformedPayload' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 2**

```bash
git add pkg/cluster/codec.go pkg/cluster/codec_raft_batch_test.go
git commit -m "perf: add raft batch wire codec"
```

---

### Task 3: Send and Receive Slot Raft Batches

**Files:**
- Modify: `pkg/cluster/transport.go`
- Modify: `pkg/cluster/cluster.go`
- Modify: `pkg/cluster/transport_glue.go`
- Modify: `pkg/cluster/transport_test.go`
- Modify: `pkg/cluster/FLOW.md`

- [ ] **Step 1: Write failing batching transport test**

Append to `pkg/cluster/transport_test.go`:

```go
func TestRaftTransportBatchesMessagesByTarget(t *testing.T) {
    srv := transport.NewServer()
    received := make(chan []multiraft.Envelope, 1)
    srv.Handle(msgTypeRaftBatch, func(body []byte) {
        records, err := decodeRaftBatchBody(body)
        if err != nil {
            t.Errorf("decodeRaftBatchBody() error = %v", err)
            return
        }
        out := make([]multiraft.Envelope, 0, len(records))
        for _, record := range records {
            out = append(out, multiraft.Envelope{SlotID: record.SlotID, Message: record.Message})
        }
        received <- out
    })
    if err := srv.Start("127.0.0.1:0"); err != nil {
        t.Fatal(err)
    }
    defer srv.Stop()

    d := NewStaticDiscovery([]NodeConfig{{NodeID: 2, Addr: srv.Listener().Addr().String()}})
    pool := transport.NewPool(d, 2, 5*time.Second)
    defer pool.Close()
    client := transport.NewClient(pool)
    defer client.Stop()

    rt := &raftTransport{client: client}
    err := rt.Send(context.Background(), []multiraft.Envelope{
        {SlotID: 1, Message: raftpb.Message{To: 2, From: 1, Type: raftpb.MsgHeartbeat}},
        {SlotID: 2, Message: raftpb.Message{To: 2, From: 1, Type: raftpb.MsgHeartbeat}},
    })
    if err != nil {
        t.Fatal(err)
    }

    select {
    case got := <-received:
        if len(got) != 2 {
            t.Fatalf("batched len = %d, want 2", len(got))
        }
        if got[0].SlotID != 1 || got[1].SlotID != 2 {
            t.Fatalf("batched slot IDs = %d,%d, want 1,2", got[0].SlotID, got[1].SlotID)
        }
    case <-time.After(2 * time.Second):
        t.Fatal("timeout waiting for raft batch")
    }
}
```

Update `TestRaftTransport_Send` to expect `msgTypeRaftBatch` or add a separate legacy single-frame handler test before changing the implementation.

- [ ] **Step 2: Run batching test and verify RED**

Run:

```bash
go test ./pkg/cluster -run TestRaftTransportBatchesMessagesByTarget -count=1
```

Expected: FAIL by timeout because current sender emits `msgTypeRaft` frames, not `msgTypeRaftBatch`.

- [ ] **Step 3: Implement grouped batch send**

Modify `pkg/cluster/transport.go`:

```go
func (t *raftTransport) Send(ctx context.Context, batch []multiraft.Envelope) error {
    grouped := make(map[uint64][]multiraft.Envelope)
    order := make([]uint64, 0)
    for _, env := range batch {
        if err := ctx.Err(); err != nil {
            return err
        }
        target := uint64(env.Message.To)
        if target == 0 {
            continue
        }
        if _, ok := grouped[target]; !ok {
            order = append(order, target)
        }
        grouped[target] = append(grouped[target], env)
    }

    for _, target := range order {
        envelopes := grouped[target]
        body, err := encodeRaftBatchBody(envelopes)
        if err != nil {
            return err
        }
        shardKey := uint64(envelopes[0].SlotID)
        if err := t.client.Send(target, shardKey, msgTypeRaftBatch, body); err != nil {
            t.logSkippedBatch(target, envelopes, err)
        }
    }
    return nil
}
```

Add a small helper to keep the log fields stable and avoid duplicating loop code:

```go
func (t *raftTransport) logSkippedBatch(target uint64, batch []multiraft.Envelope, err error) {
    if t.logger == nil || len(batch) == 0 {
        return
    }
    first := batch[0]
    t.logger.Warn("skip raft transport batch after client error",
        wklog.Event("cluster.transport.raft_batch.skipped"),
        wklog.NodeID(uint64(first.Message.From)),
        wklog.TargetNodeID(target),
        wklog.Uint64("messages", uint64(len(batch))),
        wklog.Error(err),
    )
}
```

Use existing `wklog` helpers that compile in this codebase; if `wklog.Uint64("messages", ...)` is not available, use the closest existing field helper.

- [ ] **Step 4: Implement shared receive helper and batch handler**

Modify `pkg/cluster/cluster.go`:

```go
func (c *Cluster) handleRaftMessage(body []byte) {
    slotID, data, err := decodeRaftBody(body)
    if err != nil {
        return
    }
    c.handleRaftRecord(multiraft.SlotID(slotID), data)
}

func (c *Cluster) handleRaftBatchMessage(body []byte) {
    records, err := decodeRaftBatchBody(body)
    if err != nil {
        c.transportLogger().Warn("drop malformed raft batch",
            wklog.Event("cluster.transport.raft_batch.malformed"),
            wklog.Error(err),
        )
        return
    }
    for _, record := range records {
        c.stepRaftMessage(record.SlotID, record.Message)
    }
}

func (c *Cluster) handleRaftRecord(slotID multiraft.SlotID, data []byte) {
    var msg raftpb.Message
    if err := msg.Unmarshal(data); err != nil {
        return
    }
    c.stepRaftMessage(slotID, msg)
}

func (c *Cluster) stepRaftMessage(slotID multiraft.SlotID, msg raftpb.Message) {
    if c.runtime == nil {
        return
    }
    _ = c.runtime.Step(context.Background(), multiraft.Envelope{SlotID: slotID, Message: msg})
}
```

Register `msgTypeRaftBatch` in both startup paths:

```go
c.server.Handle(msgTypeRaft, c.handleRaftMessage)
c.server.Handle(msgTypeRaftBatch, c.handleRaftBatchMessage)
```

Modify `pkg/cluster/transport_glue.go` `Start` signature to accept a batch handler or register a wrapper near existing `msgTypeRaft` registration. The preferred minimal signature is:

```go
func (t *transportLayer) Start(
    listenAddr string,
    handleRaft func([]byte),
    handleRaftBatch func([]byte),
    handleForward func(context.Context, []byte) ([]byte, error),
    handleController func(context.Context, []byte) ([]byte, error),
    handleManagedSlot func(context.Context, []byte) ([]byte, error),
) error
```

Then update caller in `Cluster.startTransportLayer()`.

- [ ] **Step 5: Keep legacy single-frame path tested**

Add or keep a test that sends `msgTypeRaft` with `encodeRaftBody` to a server/cluster handler and verifies it still decodes a single heartbeat. This guards compatibility for tests and mixed local utilities.

Run:

```bash
go test ./pkg/cluster -run 'TestRaftTransport|TestRaftBatch|TestHandleRaft' -count=1
```

Expected: PASS.

- [ ] **Step 6: Update cluster flow docs**

Modify `pkg/cluster/FLOW.md` in the startup/transport section to state:

```markdown
Slot Raft outbound messages are encoded as `msgTypeRaftBatch` by target node. The legacy `msgTypeRaft` single-message handler remains registered for compatibility; both paths route through the same runtime Step helper.
```

- [ ] **Step 7: Commit Task 3**

```bash
git add pkg/cluster/transport.go pkg/cluster/cluster.go pkg/cluster/transport_glue.go pkg/cluster/transport_test.go pkg/cluster/FLOW.md
git commit -m "perf: batch slot raft transport frames"
```

---

### Task 4: Cache Common Transport Byte Metric Counters

**Files:**
- Modify: `pkg/metrics/transport.go`
- Modify: `pkg/metrics/registry_test.go`

- [ ] **Step 1: Write failing metrics cache test**

Append to `pkg/metrics/registry_test.go`:

```go
func TestTransportMetricsPreloadsCommonByteCounters(t *testing.T) {
    reg := New(21, "node-21")

    require.NotNil(t, reg.Transport.sentBytesCache["1"])
    require.NotNil(t, reg.Transport.sentBytesCache["4"])
    require.NotNil(t, reg.Transport.sentBytesCache["rpc_request"])
    require.NotNil(t, reg.Transport.receivedBytesCache["1"])
    require.NotNil(t, reg.Transport.receivedBytesCache["4"])
    require.NotNil(t, reg.Transport.receivedBytesCache["rpc_response"])

    reg.Transport.ObserveSentBytes("1", 10)
    reg.Transport.ObserveSentBytes("4", 20)
    reg.Transport.ObserveSentBytes("custom", 30)
    reg.Transport.ObserveReceivedBytes("1", 40)
    reg.Transport.ObserveReceivedBytes("4", 50)
    reg.Transport.ObserveReceivedBytes("custom", 60)

    families, err := reg.Gather()
    require.NoError(t, err)

    sent := requireMetricFamily(t, families, "wukongim_transport_sent_bytes_total")
    requireMetricValueByLabels(t, sent, map[string]string{"msg_type": "1"}, 10)
    requireMetricValueByLabels(t, sent, map[string]string{"msg_type": "4"}, 20)
    requireMetricValueByLabels(t, sent, map[string]string{"msg_type": "custom"}, 30)

    received := requireMetricFamily(t, families, "wukongim_transport_received_bytes_total")
    requireMetricValueByLabels(t, received, map[string]string{"msg_type": "1"}, 40)
    requireMetricValueByLabels(t, received, map[string]string{"msg_type": "4"}, 50)
    requireMetricValueByLabels(t, received, map[string]string{"msg_type": "custom"}, 60)
}
```

If `requireMetricValueByLabels` does not exist, add this helper near the other test helpers:

```go
func requireMetricValueByLabels(t *testing.T, family *dto.MetricFamily, labels map[string]string, want float64) {
    t.Helper()
    for _, metric := range family.GetMetric() {
        got := make(map[string]string, len(metric.GetLabel()))
        for _, label := range metric.GetLabel() {
            got[label.GetName()] = label.GetValue()
        }
        matched := true
        for key, value := range labels {
            if got[key] != value {
                matched = false
                break
            }
        }
        if matched {
            require.Equal(t, want, metric.GetCounter().GetValue())
            return
        }
    }
    t.Fatalf("metric with labels %#v not found", labels)
}
```

- [ ] **Step 2: Run metrics test and verify current behavior**

Run:

```bash
go test ./pkg/metrics -run TestTransportMetricsPreloadsCommonByteCounters -count=1
```

Expected: FAIL because `sentBytesCache` / `receivedBytesCache` do not exist yet.

- [ ] **Step 3: Implement counter cache**

Modify `pkg/metrics/transport.go`:

```go
type TransportMetrics struct {
    // existing fields...
    sentBytesCache     map[string]prometheus.Counter
    receivedBytesCache map[string]prometheus.Counter
}

var commonTransportMsgTypes = []string{"1", "3", "4", "rpc_request", "rpc_response"}
```

In `newTransportMetrics`, after registering vectors or before return:

```go
m.sentBytesCache = make(map[string]prometheus.Counter, len(commonTransportMsgTypes))
m.receivedBytesCache = make(map[string]prometheus.Counter, len(commonTransportMsgTypes))
for _, msgType := range commonTransportMsgTypes {
    m.sentBytesCache[msgType] = m.sentBytes.WithLabelValues(msgType)
    m.receivedBytesCache[msgType] = m.receivedBytes.WithLabelValues(msgType)
}
```

Update observers:

```go
func (m *TransportMetrics) ObserveSentBytes(msgType string, bytes int) {
    if m == nil {
        return
    }
    if counter := m.sentBytesCache[msgType]; counter != nil {
        counter.Add(float64(bytes))
        return
    }
    m.sentBytes.WithLabelValues(msgType).Add(float64(bytes))
}

func (m *TransportMetrics) ObserveReceivedBytes(msgType string, bytes int) {
    if m == nil {
        return
    }
    if counter := m.receivedBytesCache[msgType]; counter != nil {
        counter.Add(float64(bytes))
        return
    }
    m.receivedBytes.WithLabelValues(msgType).Add(float64(bytes))
}
```

- [ ] **Step 4: Run metrics tests and verify GREEN**

Run:

```bash
go test ./pkg/metrics -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 4**

```bash
git add pkg/metrics/transport.go pkg/metrics/registry_test.go
git commit -m "perf: cache transport byte metric counters"
```

---

### Task 5: Run Focused Verification and Capture Idle CPU Evidence

**Files:**
- No source changes expected.
- Optional report: `docs/superpowers/reports/2026-04-27-cluster-idle-cpu-code-optimization.md`

- [ ] **Step 1: Run focused unit tests**

Run:

```bash
go test ./pkg/slot/multiraft ./pkg/cluster ./pkg/metrics -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader related tests if focused tests pass**

Run:

```bash
go test ./pkg/transport ./internal/app -count=1
```

Expected: PASS. If this is too slow locally, at least run `go test ./pkg/transport -count=1` and document why `internal/app` was skipped.

- [ ] **Step 3: Rebuild and restart the local dev cluster**

Run:

```bash
docker compose up -d --build wk-node1 wk-node2 wk-node3
```

Wait until `/debug/cluster` on all nodes returns healthy assignments/runtime views.

- [ ] **Step 4: Capture docker stats baseline**

Run:

```bash
out=/tmp/wukongim-idle-pprof/$(date +%Y%m%d-%H%M%S)-after-code-opt
mkdir -p "$out"
docker stats --no-stream --format 'table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}\t{{.BlockIO}}\t{{.PIDs}}' | tee "$out/docker-stats.txt"
```

Expected: each `wukongim-wk-node*-1` should be materially below the previous `~5%~6%` baseline and ideally near or below `1%`.

- [ ] **Step 5: Capture fresh pprof profiles**

Run:

```bash
for node in 1 2 3; do
  port=$((15000+node))
  curl -fsS "http://127.0.0.1:${port}/debug/pprof/profile?seconds=30" -o "$out/node${node}.cpu.pb.gz"
  go tool pprof -top -nodecount=40 "$out/node${node}.cpu.pb.gz" > "$out/node${node}.cpu.top.txt"
done
```

Expected:

- `RawNode.Status`, `getStatus`, `tracker.Config.Clone` are gone or negligible.
- `MuxConn.readLoop` and `priorityWriter.loop` are materially lower than before.
- If per-node idle CPU remains above `1%`, inspect the next hotspot before making more changes.

- [ ] **Step 6: Optional send smoke test**

If a local client/smoke script is available, send one message through the gateway and verify the request does not wait for slower timing. Do not change config to pass this smoke.

- [ ] **Step 7: Record verification evidence**

If runtime verification is run, create `docs/superpowers/reports/2026-04-27-cluster-idle-cpu-code-optimization.md` with:

```markdown
# Cluster Idle CPU Code Optimization Report

- Date: 2026-04-27
- Build/commit: <commit>
- Docker stats: <summary>
- Pprof artifact dir: <path>
- Main remaining hotspots: <summary>
- Target result: <met/not met>
```

- [ ] **Step 8: Commit verification report if created**

```bash
git add docs/superpowers/reports/2026-04-27-cluster-idle-cpu-code-optimization.md
git commit -m "docs: report idle cpu optimization results"
```

---

## Completion Criteria

- Focused tests pass: `go test ./pkg/slot/multiraft ./pkg/cluster ./pkg/metrics -count=1`.
- No config slowdown is introduced.
- Slot Raft batch frame is used for outbound Slot Raft traffic while legacy single-frame handler remains supported.
- `RawNode.Status()` is removed from the slot runtime hot status refresh path.
- Transport byte metrics preserve existing public output.
- Fresh pprof/docker stats are captured or explicitly documented as not run.
