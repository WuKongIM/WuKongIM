# Channel Cluster Leader Transfer P0.6 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add explicit, safe, single-channel channel leader transfer from the manager unhealthy-channel replica detail view.

**Architecture:** Keep manager HTTP as a thin adapter, route behavior through `internal/usecase/management`, and wire concrete runtime dependencies only in `internal/app`. The safety-critical target transfer lives in `internal/runtime/channelmeta`, where the authoritative Slot leader rereads metadata, validates the target against latest authoritative state, evaluates only the requested target, persists only leader fields, rereads, and applies authoritative metadata locally. Non-authoritative manager nodes route transfer requests through a dedicated node RPC modeled after channel leader repair.

**Tech Stack:** Go 1.23, Gin manager API, existing `internal/runtime/channelmeta` leader repair/evaluation infrastructure, node RPC binary codecs, React 19, TypeScript, React Router DOM 7, react-intl, Vitest, Bun/Vite.

---

## References

- Design spec: `docs/superpowers/specs/2026-05-12-channel-cluster-leader-transfer-p06-design.md`
- P0.5 plan: `docs/superpowers/plans/2026-05-12-channel-cluster-operations-p05.md`
- Manager restructure doc: `docs/raw/web-admin-restructure.md`
- Runtime flow doc: `internal/runtime/channelmeta/FLOW.md`
- Channel flow docs: `pkg/channel/FLOW.md`, `pkg/channel/replica/FLOW.md`
- Runtime repair: `internal/runtime/channelmeta/repair.go`, `internal/runtime/channelmeta/repair_types.go`, `internal/runtime/channelmeta/interfaces.go`
- Node repair/evaluate RPC: `internal/access/node/channel_leader_repair_rpc.go`, `internal/access/node/channel_leader_evaluate_rpc.go`, `internal/access/node/channel_leader_codec.go`
- Manager P0.5 operations: `internal/usecase/management/channel_cluster_operations.go`, `internal/access/manager/channel_cluster_operations.go`, `internal/app/manager_channel_cluster_operations.go`
- Frontend P0.5 page: `web/src/pages/channel-cluster/unhealthy/page.tsx`
- Follow `@superpowers:test-driven-development` for each implementation slice.
- Run `@superpowers:verification-before-completion` before claiming implementation completion.

## File Structure

Runtime:

- Modify: `internal/runtime/channelmeta/repair_types.go` - add `LeaderTransferRequest` and `LeaderTransferResult` neutral DTOs.
- Modify: `internal/runtime/channelmeta/interfaces.go` - add transfer interfaces and extend remote RPC port.
- Create: `internal/runtime/channelmeta/leader_transfer.go` - target-aware transfer routing and authoritative transfer logic.
- Create: `internal/runtime/channelmeta/leader_transfer_test.go` - transfer validation, candidate evaluation, persistence, and routing tests.
- Modify: `internal/runtime/channelmeta/repair_test.go` - update shared fakes if `RepairRemote` gains a transfer method.
- Modify: `internal/runtime/channelmeta/repair_types_test.go` - DTO neutrality test for transfer DTOs.
- Modify: `internal/runtime/channelmeta/FLOW.md` - mention explicit target transfer if implementation behavior expands flow docs.

Node RPC:

- Modify: `internal/access/node/service_ids.go` - reserve a new `channelLeaderTransferRPCServiceID`.
- Modify: `internal/access/node/options.go` - add `ChannelLeaderTransferer` interface/option/adapter field and register the handler.
- Modify: `internal/access/node/channel_leader_codec.go` - add transfer request/response binary magic, encode/decode helpers, and result pointer helpers.
- Modify: `internal/access/node/channel_leader_codec_test.go` - add transfer codec roundtrip and JSON rejection coverage.
- Create: `internal/access/node/channel_leader_transfer_rpc.go` - transfer DTOs, handler, client method, and runtime conversions.
- Create: `internal/access/node/channel_leader_transfer_rpc_test.go` - redirect, no-safe-candidate, and authoritative-result tests.

Management usecase:

- Modify: `internal/usecase/management/app.go` - add `ChannelLeaderTransferOperator` to `Options` and `App`.
- Modify: `internal/usecase/management/channel_cluster_operations.go` - add transfer DTOs, manager-level errors, validation, and `TransferChannelClusterLeader`.
- Modify: `internal/usecase/management/channel_cluster_operations_test.go` - add usecase validation and delegation tests.

Manager HTTP:

- Modify: `internal/access/manager/server.go` - extend `Management` interface with `TransferChannelClusterLeader`.
- Modify: `internal/access/manager/routes.go` - register `POST /manager/channel-cluster/:channel_type/:channel_id/leader/transfer` under `cluster.channel` write permission.
- Modify: `internal/access/manager/channel_cluster_operations.go` - add request/response DTOs, handler, and error mapping.
- Modify: `internal/access/manager/server_test.go` - add route JSON, permission, validation, and error mapping tests; update `managementStub`.

App wiring:

- Modify: `internal/app/manager_channel_cluster_operations.go` - add transfer adapter that maps management requests to `LeaderRepairer.TransferIfSafe`.
- Modify: `internal/app/manager_channel_cluster_operations_test.go` - add transfer adapter tests and fake support.
- Modify: `internal/app/build.go` - pass the `LeaderRepairer` to node RPC and management transfer wiring.

Frontend:

- Modify: `web/src/lib/manager-api.types.ts` - add transfer input and response types.
- Modify: `web/src/lib/manager-api.ts` - add `transferChannelClusterLeader` API wrapper.
- Modify: `web/src/lib/manager-api.test.ts` - add URL/body/error tests.
- Modify: `web/src/pages/channel-cluster/unhealthy/page.tsx` - add transfer action in replica detail and refresh behavior.
- Modify: `web/src/pages/channel-cluster/unhealthy/page.test.tsx` - add transfer visibility, success refresh, and conflict tests.
- Modify: `web/src/i18n/messages/en.ts` - add English transfer copy.
- Modify: `web/src/i18n/messages/zh-CN.ts` - add Chinese transfer copy.

Docs:

- Modify: `docs/raw/web-admin-restructure.md` - mark single-channel explicit safe transfer as P0.6 implemented after code lands; keep batch leader drain pending.
- Modify: `web/README.md` - include the new transfer endpoint in the channel-cluster unhealthy page matrix.

## Runtime Error Placement

Use package-level runtime errors for state-dependent validation performed by `internal/runtime/channelmeta`, then map them at the app/usecase boundary:

```go
var (
    // ErrLeaderTransferTargetNotReplica means the requested node is not in authoritative Replicas.
    ErrLeaderTransferTargetNotReplica = errors.New("channelmeta: leader transfer target is not a replica")
    // ErrLeaderTransferTargetNotISR means the requested node is not in authoritative ISR.
    ErrLeaderTransferTargetNotISR = errors.New("channelmeta: leader transfer target is not in isr")
    // ErrLeaderTransferInactiveChannel means explicit transfer requires an active channel.
    ErrLeaderTransferInactiveChannel = errors.New("channelmeta: leader transfer requires active channel")
)
```

Also define manager-facing errors in `internal/usecase/management/channel_cluster_operations.go`:

```go
var (
    // ErrChannelLeaderTransferTargetNotReplica means the requested node is not configured as a replica.
    ErrChannelLeaderTransferTargetNotReplica = errors.New("management: channel leader transfer target is not a replica")
    // ErrChannelLeaderTransferTargetNotISR means the requested node is not in the authoritative ISR set.
    ErrChannelLeaderTransferTargetNotISR = errors.New("management: channel leader transfer target is not in isr")
    // ErrChannelLeaderTransferInactiveChannel means the channel is not active.
    ErrChannelLeaderTransferInactiveChannel = errors.New("management: channel leader transfer requires active channel")
)
```

`internal/app` maps `channelmeta` errors to the management errors so HTTP mapping does not import runtime internals.

## Task 1: Add Runtime Transfer DTOs And Neutral Interfaces

**Files:**
- Modify: `internal/runtime/channelmeta/repair_types.go`
- Modify: `internal/runtime/channelmeta/interfaces.go`
- Modify: `internal/runtime/channelmeta/repair_types_test.go`
- Modify: `internal/runtime/channelmeta/repair_test.go`

- [ ] **Step 1: Re-read flow docs before runtime edits**

Run:

```bash
cat internal/runtime/channelmeta/FLOW.md
cat pkg/channel/FLOW.md
cat pkg/channel/replica/FLOW.md
```

Expected: confirm transfer remains cluster-first and authoritative Slot-leader owned.

- [ ] **Step 2: Write failing DTO neutrality test**

In `internal/runtime/channelmeta/repair_types_test.go`, add:

```go
func TestLeaderTransferDTOFieldsAreTransportNeutral(t *testing.T) {
    req := LeaderTransferRequest{
        ChannelID:            channel.ChannelID{ID: "room-1", Type: 2},
        ObservedChannelEpoch: 7,
        ObservedLeaderEpoch:  3,
        TargetNodeID:         2,
    }
    result := LeaderTransferResult{
        Meta: metadb.ChannelRuntimeMeta{ChannelID: "room-1", ChannelType: 2, Leader: 2},
        Changed: true,
    }

    require.Equal(t, uint64(2), req.TargetNodeID)
    require.True(t, result.Changed)
    require.Equal(t, uint64(2), result.Meta.Leader)
}
```

- [ ] **Step 3: Run DTO test to verify RED**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelmeta -run TestLeaderTransferDTOFieldsAreTransportNeutral -count=1
```

Expected: FAIL because `LeaderTransferRequest` and `LeaderTransferResult` are undefined.

- [ ] **Step 4: Add documented transfer DTOs**

In `internal/runtime/channelmeta/repair_types.go`, add:

```go
// LeaderTransferRequest describes an explicit safe channel leader transfer request.
type LeaderTransferRequest struct {
    // ChannelID identifies the channel whose leader should move.
    ChannelID channel.ChannelID
    // ObservedChannelEpoch carries the caller's last observed channel epoch.
    ObservedChannelEpoch uint64
    // ObservedLeaderEpoch carries the caller's last observed leader epoch.
    ObservedLeaderEpoch uint64
    // TargetNodeID is the requested new channel leader.
    TargetNodeID uint64
}

// LeaderTransferResult returns authoritative runtime metadata after transfer validation.
type LeaderTransferResult struct {
    // Meta is the authoritative runtime metadata after transfer or validation.
    Meta metadb.ChannelRuntimeMeta
    // Changed reports whether transfer persisted a changed authoritative record.
    Changed bool
}
```

- [ ] **Step 5: Add transfer interfaces**

In `internal/runtime/channelmeta/interfaces.go`, add:

```go
// Transferer safely transfers one channel leader to an explicit target.
type Transferer interface {
    // TransferIfSafe transfers metadata only after the target proves it can lead.
    TransferIfSafe(ctx context.Context, meta metadb.ChannelRuntimeMeta, targetNodeID uint64) (metadb.ChannelRuntimeMeta, bool, error)
}

// AuthoritativeTransferer handles explicit transfer requests on the authoritative slot leader.
type AuthoritativeTransferer interface {
    // TransferChannelLeaderAuthoritative transfers channel leader from the authoritative slot leader.
    TransferChannelLeaderAuthoritative(ctx context.Context, req LeaderTransferRequest) (LeaderTransferResult, error)
}
```

Extend `RepairRemote`:

```go
// TransferChannelLeader asks the current slot leader to transfer channel leadership.
TransferChannelLeader(ctx context.Context, req LeaderTransferRequest) (LeaderTransferResult, error)
```

Update existing runtime test fakes in `internal/runtime/channelmeta/repair_test.go` with a stub `TransferChannelLeader` method so old repair tests continue compiling.

- [ ] **Step 6: Run DTO test to verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelmeta -run TestLeaderTransferDTOFieldsAreTransportNeutral -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit runtime DTO/interface changes**

```bash
git add internal/runtime/channelmeta/repair_types.go internal/runtime/channelmeta/interfaces.go internal/runtime/channelmeta/repair_types_test.go internal/runtime/channelmeta/repair_test.go
git commit -m "feat: add channel leader transfer runtime DTOs"
```

## Task 2: Implement Runtime Target-Aware Transfer

**Files:**
- Create: `internal/runtime/channelmeta/leader_transfer.go`
- Create: `internal/runtime/channelmeta/leader_transfer_test.go`
- Modify: `internal/runtime/channelmeta/FLOW.md` if flow wording changes

- [ ] **Step 1: Write failing route-to-remote test**

In `internal/runtime/channelmeta/leader_transfer_test.go`, add `TestChannelLeaderTransferRoutesToRemoteSlotLeader`:

```go
func TestChannelLeaderTransferRoutesToRemoteSlotLeader(t *testing.T) {
    meta := metadb.ChannelRuntimeMeta{ChannelID: "transfer-remote", ChannelType: 2, ChannelEpoch: 9, LeaderEpoch: 4, Leader: 1}
    remote := &stubChannelLeaderRepairRemote{
        transferResult: LeaderTransferResult{Meta: withLeader(meta, 2), Changed: true},
    }
    repairer := NewLeaderRepairer(LeaderRepairerOptions{
        Cluster: fakeChannelLeaderRepairCluster{slotID: 7, leaderID: 3},
        Remote:  remote,
    })

    got, changed, err := repairer.TransferIfSafe(context.Background(), meta, 2)

    require.NoError(t, err)
    require.True(t, changed)
    require.Equal(t, uint64(2), got.Leader)
    require.Equal(t, []LeaderTransferRequest{{
        ChannelID:            channel.ChannelID{ID: "transfer-remote", Type: 2},
        ObservedChannelEpoch: 9,
        ObservedLeaderEpoch:  4,
        TargetNodeID:         2,
    }}, remote.transferCalls)
}
```

Add small test helper `withLeader(meta metadb.ChannelRuntimeMeta, leader uint64) metadb.ChannelRuntimeMeta`.

- [ ] **Step 2: Write failing authoritative validation tests**

Add these tests:

- `TestChannelLeaderTransferReturnsUnchangedWhenTargetAlreadyLeader`
- `TestChannelLeaderTransferRejectsTargetOutsideReplicas`
- `TestChannelLeaderTransferRejectsTargetOutsideISR`
- `TestChannelLeaderTransferRejectsInactiveChannel`
- `TestChannelLeaderTransferReturnsLatestMetaWhenObservedEpochsAreStale`

Use `fakeChannelMetaSource` from `repair_test.go` and assert:

```go
require.ErrorIs(t, err, ErrLeaderTransferTargetNotReplica)
require.Empty(t, store.upserts)
```

For inactive, use `Status: uint8(channel.StatusDeleting)` and assert `ErrLeaderTransferInactiveChannel`.

- [ ] **Step 3: Write failing candidate and persist tests**

Add:

- `TestChannelLeaderTransferEvaluatesOnlyRequestedTarget`
  - `Replicas: []uint64{1,2,3}`, `ISR: []uint64{1,2,3}`, `Leader: 1`, target `3`.
  - Configure remote/evaluator reports so node `2` would be better, but assert only node `3` is evaluated and promoted.
- `TestChannelLeaderTransferPersistsOnlyLeaderEpochAndLease`
  - Verify updated metadata changes only `Leader`, `LeaderEpoch`, and `LeaseUntilMS`.
  - Preserve `Replicas`, `ISR`, `MinISR`, `Status`, `Features`, `RetentionThroughSeq`, `RetentionUpdatedAtMS`, and `ChannelEpoch`.
  - Verify authoritative metadata is reread after write and `ApplyAuthoritative` receives the reread value.
- `TestChannelLeaderTransferReturnsNoSafeCandidate`
  - Evaluator report has `CanLead: false`; expect `channel.ErrNoSafeChannelLeader` and no upsert.

- [ ] **Step 4: Run runtime transfer tests to verify RED**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelmeta -run 'TestChannelLeaderTransfer' -count=1
```

Expected: FAIL because transfer methods and errors are missing.

- [ ] **Step 5: Implement `leader_transfer.go` errors and routing**

Create `internal/runtime/channelmeta/leader_transfer.go` with package errors from the Runtime Error Placement section and:

```go
func (r *LeaderRepairer) TransferIfSafe(ctx context.Context, meta metadb.ChannelRuntimeMeta, targetNodeID uint64) (metadb.ChannelRuntimeMeta, bool, error) {
    if r == nil {
        return meta, false, nil
    }
    req := LeaderTransferRequest{
        ChannelID:            channel.ChannelID{ID: meta.ChannelID, Type: uint8(meta.ChannelType)},
        ObservedChannelEpoch: meta.ChannelEpoch,
        ObservedLeaderEpoch:  meta.LeaderEpoch,
        TargetNodeID:         targetNodeID,
    }
    // Follow the same local/remote/NotLeader reroute pattern as RepairIfNeeded.
}
```

Use `r.cluster.SlotForKey`, `r.cluster.LeaderOf`, `r.cluster.IsLocal`, `r.remote.TransferChannelLeader`, and retry local/remote once on `raftcluster.ErrNotLeader`, matching `RepairIfNeeded`.

- [ ] **Step 6: Implement authoritative transfer**

In `leader_transfer.go`, implement:

```go
func (r *LeaderRepairer) TransferChannelLeaderAuthoritative(ctx context.Context, req LeaderTransferRequest) (LeaderTransferResult, error) {
    if r == nil || r.store == nil {
        return LeaderTransferResult{}, channel.ErrInvalidConfig
    }
    key := channelhandler.KeyFromChannelID(req.ChannelID)
    value, err, _ := r.sf.Do(string(key), func() (any, error) {
        latest, err := r.store.GetChannelRuntimeMeta(ctx, req.ChannelID.ID, int64(req.ChannelID.Type))
        if err != nil {
            return nil, err
        }
        if observedTransferEpochsStale(req, latest) {
            return LeaderTransferResult{Meta: latest, Changed: false}, nil
        }
        if err := validateLeaderTransferTarget(latest, req.TargetNodeID); err != nil {
            return nil, err
        }
        if latest.Leader == req.TargetNodeID {
            return LeaderTransferResult{Meta: latest, Changed: false}, nil
        }
        report, err := r.evaluateLeaderCandidate(ctx, req.TargetNodeID, latest, "")
        if err != nil {
            return nil, err
        }
        if report.ChannelEpoch != 0 && report.ChannelEpoch != latest.ChannelEpoch {
            return nil, channel.ErrNoSafeChannelLeader
        }
        if !report.CanLead {
            return nil, channel.ErrNoSafeChannelLeader
        }
        updated := latest
        updated.Leader = req.TargetNodeID
        updated.LeaderEpoch++
        updated.LeaseUntilMS = r.currentTime().Add(BootstrapLease).UnixMilli()
        if err := r.store.UpsertChannelRuntimeMetaIfLocalLeader(ctx, updated); err != nil {
            return nil, err
        }
        authoritative, err := r.store.GetChannelRuntimeMeta(ctx, req.ChannelID.ID, int64(req.ChannelID.Type))
        if err != nil {
            return nil, err
        }
        if r.applyAuthoritative != nil {
            if err := r.applyAuthoritative(authoritative); err != nil {
                return nil, err
            }
        }
        return LeaderTransferResult{Meta: authoritative, Changed: true}, nil
    })
    if err != nil {
        return LeaderTransferResult{}, err
    }
    result, ok := value.(LeaderTransferResult)
    if !ok {
        return LeaderTransferResult{}, fmt.Errorf("channelmeta transfer: unexpected singleflight result %T", value)
    }
    return result, nil
}
```

Add helpers:

```go
func validateLeaderTransferTarget(meta metadb.ChannelRuntimeMeta, targetNodeID uint64) error
func observedTransferEpochsStale(req LeaderTransferRequest, latest metadb.ChannelRuntimeMeta) bool
```

Validation order: active status, non-zero target, target in replicas, target in ISR. Return target-not-replica for zero target.

- [ ] **Step 7: Run runtime transfer tests to verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelmeta -run 'Test(ChannelLeaderTransfer|LeaderTransferDTO)' -count=1
```

Expected: PASS.

- [ ] **Step 8: Run existing runtime repair tests for regression**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelmeta -run 'TestChannelLeader.*Repair|TestChannelLeader.*Evaluate|TestChannelLeaderPromotionEvaluator' -count=1
```

Expected: PASS.

- [ ] **Step 9: Update flow doc if needed**

If `internal/runtime/channelmeta/FLOW.md` still says the repairer only owns repair/evaluation behavior, update that sentence to include explicit safe transfer. Keep wording cluster-first and concise.

- [ ] **Step 10: Commit runtime transfer implementation**

```bash
git add internal/runtime/channelmeta/leader_transfer.go internal/runtime/channelmeta/leader_transfer_test.go internal/runtime/channelmeta/FLOW.md
git commit -m "feat: add safe channel leader transfer runtime"
```

## Task 3: Add Node RPC For Leader Transfer

**Files:**
- Modify: `internal/access/node/service_ids.go`
- Modify: `internal/access/node/options.go`
- Modify: `internal/access/node/channel_leader_codec.go`
- Modify: `internal/access/node/channel_leader_codec_test.go`
- Create: `internal/access/node/channel_leader_transfer_rpc.go`
- Create: `internal/access/node/channel_leader_transfer_rpc_test.go`

- [ ] **Step 1: Write failing transfer codec test**

In `internal/access/node/channel_leader_codec_test.go`, add `TestChannelLeaderTransferBinaryCodecRoundTrip`:

```go
func TestChannelLeaderTransferBinaryCodecRoundTrip(t *testing.T) {
    req := ChannelLeaderTransferRequest{
        ChannelID:            channel.ChannelID{ID: "transfer-binary", Type: 2},
        ObservedChannelEpoch: 11,
        ObservedLeaderEpoch:  10,
        TargetNodeID:         3,
    }

    reqBody, err := encodeChannelLeaderTransferRequestBinary(req)
    require.NoError(t, err)
    require.True(t, isChannelLeaderTransferRequestBinary(reqBody))

    gotReq, err := decodeChannelLeaderTransferRequest(reqBody)
    require.NoError(t, err)
    require.Equal(t, req, gotReq)

    resp := channelLeaderTransferResponse{
        Status:   rpcStatusOK,
        LeaderID: 2,
        Result: &ChannelLeaderTransferResult{
            Meta:    testChannelRuntimeMeta("transfer-binary"),
            Changed: true,
        },
    }
    respBody, err := encodeChannelLeaderTransferResponse(resp)
    require.NoError(t, err)
    require.True(t, isChannelLeaderTransferResponseBinary(respBody))

    gotResp, err := decodeChannelLeaderTransferResponse(respBody)
    require.NoError(t, err)
    require.Equal(t, resp, gotResp)
}
```

Also extend `TestChannelLeaderRPCRejectsJSONPayload` with `adapter.handleChannelLeaderTransferRPC` once the handler exists.

- [ ] **Step 2: Write failing transfer RPC tests**

Create `internal/access/node/channel_leader_transfer_rpc_test.go` with:

- `TestChannelLeaderTransferRPCRedirectsToCurrentSlotLeader`
- `TestChannelLeaderTransferRPCMapsNoSafeCandidateStatus`
- `TestChannelLeaderTransferRPCReturnsAuthoritativeMetaAfterTransfer`

Mirror `channel_leader_repair_rpc_test.go`, using `stubNodeChannelLeaderTransferer`:

```go
type stubNodeChannelLeaderTransferer struct {
    calls  []channelmeta.LeaderTransferRequest
    result channelmeta.LeaderTransferResult
    err    error
}

func (s *stubNodeChannelLeaderTransferer) TransferChannelLeaderAuthoritative(_ context.Context, req channelmeta.LeaderTransferRequest) (channelmeta.LeaderTransferResult, error) {
    s.calls = append(s.calls, req)
    return s.result, s.err
}
```

Assert request carries `TargetNodeID` and observed epochs.

- [ ] **Step 3: Run node transfer tests to verify RED**

Run:

```bash
GOWORK=off go test ./internal/access/node -run 'TestChannelLeaderTransfer|TestChannelLeaderRPCRejectsJSONPayload' -count=1
```

Expected: FAIL because transfer RPC types, codecs, and handler do not exist.

- [ ] **Step 4: Reserve service ID and register handler**

In `internal/access/node/service_ids.go`, add the next free ID:

```go
channelLeaderTransferRPCServiceID uint8 = 46
```

In `internal/access/node/options.go`, add:

```go
// ChannelLeaderTransferer transfers a channel leader on the authoritative slot leader.
type ChannelLeaderTransferer interface {
    TransferChannelLeaderAuthoritative(ctx context.Context, req channelmeta.LeaderTransferRequest) (channelmeta.LeaderTransferResult, error)
}
```

Add `ChannelLeaderTransfer ChannelLeaderTransferer` to `Options` and `Adapter`, assign it in `New`, and register:

```go
opts.Cluster.RPCMux().Handle(channelLeaderTransferRPCServiceID, adapter.handleChannelLeaderTransferRPC)
```

- [ ] **Step 5: Add transfer codec support**

In `internal/access/node/channel_leader_codec.go`, add distinct magic values, e.g.:

```go
channelLeaderTransferRequestMagic  = [...]byte{'W', 'K', 'L', 'T', 1}
channelLeaderTransferResponseMagic = [...]byte{'W', 'K', 'L', 'U', 1}
```

Implement encode/decode helpers matching repair plus `TargetNodeID`:

```go
func encodeChannelLeaderTransferRequestBinary(req ChannelLeaderTransferRequest) ([]byte, error)
func decodeChannelLeaderTransferRequest(body []byte) (ChannelLeaderTransferRequest, error)
func encodeChannelLeaderTransferResponseBinary(resp channelLeaderTransferResponse) ([]byte, error)
func decodeChannelLeaderTransferResponseBinary(body []byte) (channelLeaderTransferResponse, error)
func appendChannelLeaderTransferResultPtr(dst []byte, result *ChannelLeaderTransferResult) []byte
func readChannelLeaderTransferResultPtr(body []byte, offset int) (*ChannelLeaderTransferResult, int, error)
```

Use `appendChannelRuntimeMeta` and `appendNodeBool` for result fields.

- [ ] **Step 6: Add transfer RPC implementation**

Create `internal/access/node/channel_leader_transfer_rpc.go` with access DTOs and conversions:

```go
type ChannelLeaderTransferRequest struct {
    // ChannelID identifies the channel to transfer.
    ChannelID channel.ChannelID `json:"channel_id"`
    // ObservedChannelEpoch carries the caller's last observed channel epoch.
    ObservedChannelEpoch uint64 `json:"observed_channel_epoch,omitempty"`
    // ObservedLeaderEpoch carries the caller's last observed leader epoch.
    ObservedLeaderEpoch uint64 `json:"observed_leader_epoch,omitempty"`
    // TargetNodeID is the requested new leader.
    TargetNodeID uint64 `json:"target_node_id"`
}

type ChannelLeaderTransferResult struct {
    // Meta is authoritative runtime metadata after transfer or validation.
    Meta metadb.ChannelRuntimeMeta `json:"meta"`
    // Changed reports whether transfer persisted changed metadata.
    Changed bool `json:"changed"`
}
```

Use a `channelLeaderTransferResponse` with `Status`, `LeaderID`, and `Result`. Handler behavior mirrors repair:

- Decode binary request.
- Determine Slot from `req.ChannelID.ID`.
- Return `not_leader`, `no_leader`, or `no_slot` from `authoritativeRPCStatus`.
- Require `a.channelLeaderTransfer != nil`.
- Call `TransferChannelLeaderAuthoritative`.
- Map `channel.ErrNoSafeChannelLeader` to `rpcStatusNoSafeCandidate`.
- Return `ok` plus result.

Client method:

```go
func (c *Client) TransferChannelLeader(ctx context.Context, req channelmeta.LeaderTransferRequest) (channelmeta.LeaderTransferResult, error)
```

Copy the repair client redirect loop and service status mapping.

- [ ] **Step 7: Run node transfer tests to verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/access/node -run 'TestChannelLeaderTransfer|TestChannelLeaderRPCRejectsJSONPayload' -count=1
```

Expected: PASS.

- [ ] **Step 8: Run broader node RPC regression tests**

Run:

```bash
GOWORK=off go test ./internal/access/node -run 'TestChannelLeader(Repair|Evaluate|Transfer)|TestChannelLeaderRPCRejectsJSONPayload' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit node RPC changes**

```bash
git add internal/access/node/service_ids.go internal/access/node/options.go internal/access/node/channel_leader_codec.go internal/access/node/channel_leader_codec_test.go internal/access/node/channel_leader_transfer_rpc.go internal/access/node/channel_leader_transfer_rpc_test.go
git commit -m "feat: add channel leader transfer node RPC"
```

## Task 4: Add Management Usecase Transfer

**Files:**
- Modify: `internal/usecase/management/app.go`
- Modify: `internal/usecase/management/channel_cluster_operations.go`
- Modify: `internal/usecase/management/channel_cluster_operations_test.go`

- [ ] **Step 1: Write failing usecase tests**

In `internal/usecase/management/channel_cluster_operations_test.go`, add:

- `TestTransferChannelClusterLeaderValidatesTarget`
  - Empty `ChannelID`, non-positive `ChannelType`, and zero `TargetNodeID` return `metadb.ErrInvalidArgument`.
- `TestTransferChannelClusterLeaderRejectsTargetOutsideReplicas`
  - Current meta replicas `[1,2]`, target `3`; expect `ErrChannelLeaderTransferTargetNotReplica`; operator not called.
- `TestTransferChannelClusterLeaderRejectsTargetOutsideISR`
  - Replicas `[1,2,3]`, ISR `[1,2]`, target `3`; expect `ErrChannelLeaderTransferTargetNotISR`.
- `TestTransferChannelClusterLeaderRejectsInactiveChannel`
  - Status deleting; expect `ErrChannelLeaderTransferInactiveChannel`.
- `TestTransferChannelClusterLeaderReturnsUnchangedWhenTargetAlreadyLeads`
  - Leader `2`, target `2`; response changed false and operator not called.
- `TestTransferChannelClusterLeaderDelegatesSafeTransfer`
  - Current leader `1`, target `2`; fake operator returns changed meta with leader `2`; assert response channel leader `2` and request fields.

Use a fake operator:

```go
type fakeChannelLeaderTransferOperator struct {
    calls  []TransferChannelClusterLeaderRequest
    result TransferChannelClusterLeaderResult
    err    error
}

func (f *fakeChannelLeaderTransferOperator) TransferChannelLeader(_ context.Context, req TransferChannelClusterLeaderRequest) (TransferChannelClusterLeaderResult, error) {
    f.calls = append(f.calls, req)
    return f.result, f.err
}
```

- [ ] **Step 2: Run usecase transfer tests to verify RED**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run TestTransferChannelClusterLeader -count=1
```

Expected: FAIL because usecase DTOs, port, and method do not exist.

- [ ] **Step 3: Add management port and app wiring fields**

In `internal/usecase/management/app.go`, add:

```go
// ChannelLeaderTransferOperator exposes explicit safe channel leader transfer.
type ChannelLeaderTransferOperator interface {
    // TransferChannelLeader safely transfers authoritative channel leadership.
    TransferChannelLeader(ctx context.Context, req TransferChannelClusterLeaderRequest) (TransferChannelClusterLeaderResult, error)
}
```

Add `ChannelLeaderTransfer ChannelLeaderTransferOperator` to `Options`, add `channelLeaderTransfer ChannelLeaderTransferOperator` to `App`, and assign it in `New`.

- [ ] **Step 4: Add transfer DTOs and errors**

In `internal/usecase/management/channel_cluster_operations.go`, add documented DTOs:

```go
type TransferChannelClusterLeaderRequest struct {
    // ChannelID identifies the channel whose leader should move.
    ChannelID string
    // ChannelType identifies the channel type.
    ChannelType int64
    // TargetNodeID is the replica node that should become leader.
    TargetNodeID uint64
}

type TransferChannelClusterLeaderResult struct {
    // Changed reports whether authoritative metadata changed.
    Changed bool
    // Meta is authoritative metadata after transfer or validation.
    Meta metadb.ChannelRuntimeMeta
}

type TransferChannelClusterLeaderResponse struct {
    // Changed reports whether authoritative metadata changed.
    Changed bool
    // Channel is authoritative manager metadata after transfer or validation.
    Channel ChannelRuntimeMetaDetail
}
```

Add management errors from Runtime Error Placement.

- [ ] **Step 5: Implement validation and delegation**

Implement:

```go
func (a *App) TransferChannelClusterLeader(ctx context.Context, req TransferChannelClusterLeaderRequest) (TransferChannelClusterLeaderResponse, error)
```

Behavior:

1. Validate `ChannelID != ""`, `ChannelType > 0`, `TargetNodeID > 0`; otherwise `metadb.ErrInvalidArgument`.
2. Read current authoritative metadata using `a.GetChannelRuntimeMeta`.
3. Fail fast when status is not active, target not in replicas, or target not in ISR.
4. Return `Changed=false` with current detail if target is already leader.
5. Require `a.channelLeaderTransfer != nil`, otherwise `channel.ErrInvalidConfig`.
6. Delegate to `a.channelLeaderTransfer.TransferChannelLeader`.
7. Convert returned metadata with `a.channelRuntimeMetaDetailFromMeta(ctx, result.Meta)`.

Keep runtime as source of truth: this method is an early guard only; do not mutate metadata here.

- [ ] **Step 6: Run usecase transfer tests to verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run TestTransferChannelClusterLeader -count=1
```

Expected: PASS.

- [ ] **Step 7: Run management usecase regression tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run 'Test(GetChannelClusterReplicaDetail|RepairChannelClusterLeader|TransferChannelClusterLeader)' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit management usecase changes**

```bash
git add internal/usecase/management/app.go internal/usecase/management/channel_cluster_operations.go internal/usecase/management/channel_cluster_operations_test.go
git commit -m "feat: add channel leader transfer usecase"
```

## Task 5: Add Manager HTTP Route

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/channel_cluster_operations.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write failing HTTP success and permission tests**

In `internal/access/manager/server_test.go`, add:

- `TestManagerChannelClusterLeaderTransferReturnsChangedChannel`
  - Auth user has `cluster.channel` write.
  - POST `/manager/channel-cluster/2/room-1/leader/transfer` with `{"target_node_id":2}`.
  - Assert stub receives `TransferChannelClusterLeaderRequest{ChannelID:"room-1", ChannelType:2, TargetNodeID:2}`.
  - Assert JSON includes `changed:true` and `channel.leader:2`.
- `TestManagerChannelClusterLeaderTransferRequiresWritePermission`
  - User only has read permission; expect 403.

- [ ] **Step 2: Write failing HTTP error mapping tests**

Extend `TestManagerChannelClusterOperationsMapErrors` with transfer cases:

- invalid channel type -> 400.
- invalid body -> 400.
- missing/zero `target_node_id` -> 400.
- target not replica -> 400.
- target not ISR -> 409.
- inactive channel -> 409.
- `metadb.ErrNotFound` -> 404.
- `channel.ErrNoSafeChannelLeader` -> 409.
- `raftcluster.ErrNoLeader` -> 503.

- [ ] **Step 3: Run HTTP tests to verify RED**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerChannelClusterLeaderTransfer|TestManagerChannelClusterOperationsMapErrors' -count=1
```

Expected: FAIL because interface, route, handler, DTO, or stub methods are missing.

- [ ] **Step 4: Extend manager interface and stub**

In `internal/access/manager/server.go`, add:

```go
// TransferChannelClusterLeader safely transfers one channel leader to an explicit replica.
TransferChannelClusterLeader(ctx context.Context, req managementusecase.TransferChannelClusterLeaderRequest) (managementusecase.TransferChannelClusterLeaderResponse, error)
```

In `server_test.go`, add stub fields and method:

```go
channelClusterTransferReqSink *managementusecase.TransferChannelClusterLeaderRequest
channelClusterTransfer managementusecase.TransferChannelClusterLeaderResponse
channelClusterTransferErr error
```

- [ ] **Step 5: Register route**

In `internal/access/manager/routes.go`, under `channelClusterWrites`, add:

```go
channelClusterWrites.POST("/channel-cluster/:channel_type/:channel_id/leader/transfer", s.handleChannelClusterLeaderTransfer)
```

- [ ] **Step 6: Add HTTP DTO and handler**

In `internal/access/manager/channel_cluster_operations.go`, add:

```go
type TransferChannelClusterLeaderRequestDTO struct {
    // TargetNodeID is the requested new leader.
    TargetNodeID uint64 `json:"target_node_id"`
}

type TransferChannelClusterLeaderResponseDTO struct {
    // Changed reports whether authoritative metadata changed.
    Changed bool `json:"changed"`
    // Channel is authoritative channel metadata after transfer or validation.
    Channel ChannelRuntimeMetaDetailDTO `json:"channel"`
}
```

Implement `handleChannelClusterLeaderTransfer`:

- Management nil -> 503.
- Parse `channel_type` with existing `parseChannelRuntimeMetaChannelTypeParam`.
- Bind JSON; invalid or EOF body -> 400 because `target_node_id` is required for transfer.
- Reject `TargetNodeID == 0` with 400.
- Call `s.management.TransferChannelClusterLeader`.
- Return success DTO.

- [ ] **Step 7: Extend error mapping**

Update `handleChannelClusterOperationError`:

```go
case errors.Is(err, metadb.ErrInvalidArgument),
    errors.Is(err, managementusecase.ErrUnsupportedChannelClusterRepairReason),
    errors.Is(err, managementusecase.ErrChannelLeaderTransferTargetNotReplica):
    jsonError(c, http.StatusBadRequest, "bad_request", err.Error())
case errors.Is(err, channel.ErrNoSafeChannelLeader),
    errors.Is(err, managementusecase.ErrChannelLeaderTransferTargetNotISR),
    errors.Is(err, managementusecase.ErrChannelLeaderTransferInactiveChannel):
    message := err.Error()
    if errors.Is(err, channel.ErrNoSafeChannelLeader) {
        message = "no safe channel leader candidate"
    }
    jsonError(c, http.StatusConflict, "conflict", message)
```

Use stable messages such as `no safe channel leader candidate` for `channel.ErrNoSafeChannelLeader`; otherwise `err.Error()` is acceptable.

- [ ] **Step 8: Run HTTP tests to verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerChannelClusterLeaderTransfer|TestManagerChannelClusterOperationsMapErrors' -count=1
```

Expected: PASS.

- [ ] **Step 9: Run manager operation regression tests**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'TestManagerChannelCluster(Replicas|Repair|LeaderTransfer|Operations)' -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit manager HTTP changes**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/channel_cluster_operations.go internal/access/manager/server_test.go
git commit -m "feat: expose channel leader transfer API"
```

## Task 6: Wire App Adapters

**Files:**
- Modify: `internal/app/manager_channel_cluster_operations.go`
- Modify: `internal/app/manager_channel_cluster_operations_test.go`
- Modify: `internal/app/build.go`

- [ ] **Step 1: Write failing app adapter tests**

In `internal/app/manager_channel_cluster_operations_test.go`, add:

- `TestManagerChannelClusterTransferAdapterCallsTransferer`
  - Fake meta getter returns meta with leader `1`, epochs set.
  - Fake transferer records meta and target, returns changed meta with leader `2`.
  - Assert result changed and fake observed `targetNodeID == 2`.
- `TestManagerChannelClusterTransferAdapterMapsRuntimeErrors`
  - Runtime transferer returns `channelmeta.ErrLeaderTransferTargetNotISR`, expect `managementusecase.ErrChannelLeaderTransferTargetNotISR`.
  - Repeat for target-not-replica and inactive if using table test.

Fake:

```go
type fakeManagerChannelLeaderTransferer struct {
    meta metadb.ChannelRuntimeMeta
    changed bool
    err error
    observedMeta metadb.ChannelRuntimeMeta
    targetNodeID uint64
}

func (f *fakeManagerChannelLeaderTransferer) TransferIfSafe(_ context.Context, meta metadb.ChannelRuntimeMeta, targetNodeID uint64) (metadb.ChannelRuntimeMeta, bool, error) {
    f.observedMeta = meta
    f.targetNodeID = targetNodeID
    if f.err != nil {
        return metadb.ChannelRuntimeMeta{}, false, f.err
    }
    return f.meta, f.changed, nil
}
```

- [ ] **Step 2: Run app adapter tests to verify RED**

Run:

```bash
GOWORK=off go test ./internal/app -run TestManagerChannelClusterTransferAdapter -count=1
```

Expected: FAIL because adapter does not exist.

- [ ] **Step 3: Add app transfer adapter**

In `internal/app/manager_channel_cluster_operations.go`, add:

```go
type managerChannelLeaderTransferer interface {
    TransferIfSafe(ctx context.Context, meta metadb.ChannelRuntimeMeta, targetNodeID uint64) (metadb.ChannelRuntimeMeta, bool, error)
}

type managerChannelLeaderTransferOperator struct {
    metas      managerChannelRuntimeMetaGetter
    transferer managerChannelLeaderTransferer
}

func (o managerChannelLeaderTransferOperator) TransferChannelLeader(ctx context.Context, req managementusecase.TransferChannelClusterLeaderRequest) (managementusecase.TransferChannelClusterLeaderResult, error) {
    if o.metas == nil || o.transferer == nil {
        return managementusecase.TransferChannelClusterLeaderResult{}, channel.ErrInvalidConfig
    }
    meta, err := o.metas.GetChannelRuntimeMeta(ctx, req.ChannelID, req.ChannelType)
    if err != nil {
        return managementusecase.TransferChannelClusterLeaderResult{}, err
    }
    transferred, changed, err := o.transferer.TransferIfSafe(ctx, meta, req.TargetNodeID)
    if err != nil {
        return managementusecase.TransferChannelClusterLeaderResult{}, mapChannelLeaderTransferError(err)
    }
    return managementusecase.TransferChannelClusterLeaderResult{Changed: changed, Meta: transferred}, nil
}
```

Implement `mapChannelLeaderTransferError` mapping runtime channelmeta errors to management errors, preserving unknown errors.

- [ ] **Step 4: Wire management and node dependencies in build**

In `internal/app/build.go`:

- In `node.Options`, add `ChannelLeaderTransfer: channelLeaderRepairer`.
- In `managementusecase.Options`, add:

```go
ChannelLeaderTransfer: managerChannelLeaderTransferOperator{
    metas:      app.store,
    transferer: channelLeaderRepairer,
},
```

Use the same `channelRuntimeMeta` source pattern as `managerChannelLeaderRepairOperator` currently uses; avoid adding a second concrete composition path.

- [ ] **Step 5: Run app adapter tests to verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/app -run TestManagerChannelClusterTransferAdapter -count=1
```

Expected: PASS.

- [ ] **Step 6: Run focused app wiring regression tests**

Run:

```bash
GOWORK=off go test ./internal/app -run 'TestManagerChannelCluster(Status|Repair|Transfer)Adapter|TestBuildWiresObservabilityIntoAPIAndChannelCluster|TestNewBuildsOptionalManagerServerWhenConfigured' -count=1
```

Expected: PASS or no tests matched for optional build names; investigate failures rather than assuming.

- [ ] **Step 7: Commit app wiring changes**

```bash
git add internal/app/manager_channel_cluster_operations.go internal/app/manager_channel_cluster_operations_test.go internal/app/build.go
git commit -m "feat: wire channel leader transfer"
```

## Task 7: Add Frontend API And Replica Detail Action

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`
- Modify: `web/src/pages/channel-cluster/unhealthy/page.tsx`
- Modify: `web/src/pages/channel-cluster/unhealthy/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing API client test**

In `web/src/lib/manager-api.test.ts`, import `transferChannelClusterLeader` and add:

```ts
it("transfers a channel cluster leader", async () => {
  const response = { changed: true, channel: channelRuntimeMetaDetail }
  fetchMock.mockResolvedValueOnce(jsonResponse(response))

  await expect(transferChannelClusterLeader(2, "room-1", { target_node_id: 3 })).resolves.toEqual(response)

  expect(fetchMock.mock.calls[0]?.[0]).toBe("/manager/channel-cluster/2/room-1/leader/transfer")
  expect(fetchMock.mock.calls[0]?.[1]).toMatchObject({ method: "POST", body: JSON.stringify({ target_node_id: 3 }) })
})
```

If the existing tests use `targetNodeId` camelCase for inputs, keep the spec's exact snake-case `target_node_id` for this transfer API unless the implementation adds an explicit conversion layer and tests it.

- [ ] **Step 2: Write failing page tests**

In `web/src/pages/channel-cluster/unhealthy/page.test.tsx`:

- Mock `transferChannelClusterLeader`.
- Add `shows transfer button only for active non-leader ISR replicas`:
  - Detail channel status `active`.
  - Replicas: leader row `{is_leader:true,in_isr:true}`, follower ISR row `{is_leader:false,in_isr:true}`, follower non-ISR row `{is_leader:false,in_isr:false}`.
  - Assert only follower ISR row has transfer button.
- Add `transfers leader and refreshes detail and unhealthy list`:
  - Click transfer for node `2`.
  - Assert API called with `(2, "room-1", { target_node_id: 2 })`.
  - Assert detail reload and first page reload are called.
- Add `keeps replica detail visible on transfer conflict`:
  - API rejects `new ManagerApiError(409, "conflict", "no safe candidate")`.
  - Assert conflict copy renders and the replica row remains visible.

- [ ] **Step 3: Run frontend tests to verify RED**

Run:

```bash
cd web && bun run test src/lib/manager-api.test.ts src/pages/channel-cluster/unhealthy/page.test.tsx
```

Expected: FAIL because API wrapper and UI action do not exist.

- [ ] **Step 4: Add frontend API types and wrapper**

In `web/src/lib/manager-api.types.ts`, add:

```ts
export type TransferChannelClusterLeaderInput = {
  target_node_id: number
}

export type ManagerChannelClusterLeaderTransferResponse = {
  changed: boolean
  channel: ManagerChannelRuntimeMetaDetailResponse
}
```

In `web/src/lib/manager-api.ts`, add:

```ts
export function transferChannelClusterLeader(
  channelType: number,
  channelId: string,
  input: TransferChannelClusterLeaderInput,
) {
  return jsonManagerFetch<ManagerChannelClusterLeaderTransferResponse>(
    `/manager/channel-cluster/${channelType}/${encodeURIComponent(channelId)}/leader/transfer`,
    { method: "POST", body: JSON.stringify(input) },
  )
}
```

- [ ] **Step 5: Add UI state and transfer handler**

In `web/src/pages/channel-cluster/unhealthy/page.tsx`:

- Import `transferChannelClusterLeader`.
- Rename generic repair notice state if useful, or add a separate transfer notice. Keep repair behavior unchanged.
- Track pending transfer by row key, e.g. `const transferKey = `${channelKey(channel)}:${replica.node_id}``.
- Add helper:

```ts
function canTransferLeader(detail: ManagerChannelClusterReplicaDetailResponse, replica: ManagerChannelClusterReplicaStatusResponse) {
  return detail.channel.status === "active" && replica.in_isr && !replica.is_leader
}
```

- Add handler:

```ts
const transferLeader = useCallback(async (channel, targetNodeId) => {
  const key = `${channelKey(channel)}:${targetNodeId}`
  setTransferState({ loadingKey: key, notice: null })
  try {
    const result = await transferChannelClusterLeader(channel.channel_type, channel.channel_id, { target_node_id: targetNodeId })
    setTransferState({
      loadingKey: null,
      notice: {
        kind: "success",
        message: intl.formatMessage(
          {
            id: result.changed
              ? "channelCluster.replicaDetail.transferSuccessChanged"
              : "channelCluster.replicaDetail.transferSuccessUnchanged",
          },
          { leader: result.channel.leader },
        ),
      },
    })
    await loadReplicaDetail(channel)
    await loadFirstPage(true)
  } catch (error) {
    const isConflict = error instanceof ManagerApiError && error.status === 409
    setTransferState({
      loadingKey: null,
      notice: {
        kind: isConflict ? "conflict" : "error",
        message: intl.formatMessage({
          id: isConflict
            ? "channelCluster.replicaDetail.transferConflict"
            : "channelCluster.replicaDetail.transferError",
        }),
      },
    })
  }
}, [intl, loadFirstPage, loadReplicaDetail])
```

- Add an actions column in the replica detail table.
- Render `Transfer leader` only when `canTransferLeader(detailState.detail, replica)` is true.
- Do not require `replica.reported === true`.
- On 409, do not clear `detailState`.

- [ ] **Step 6: Add i18n messages**

Add messages:

English:

```ts
"channelCluster.replicaDetail.transferLeader": "Transfer leader",
"channelCluster.replicaDetail.transferSuccessChanged": "Leader transferred to node {leader}.",
"channelCluster.replicaDetail.transferSuccessUnchanged": "Leader already validated as node {leader}.",
"channelCluster.replicaDetail.transferConflict": "No safe leader candidate for that target.",
"channelCluster.replicaDetail.transferError": "Leader transfer failed.",
```

Chinese:

```ts
"channelCluster.replicaDetail.transferLeader": "转移 Leader",
"channelCluster.replicaDetail.transferSuccessChanged": "Leader 已转移到节点 {leader}。",
"channelCluster.replicaDetail.transferSuccessUnchanged": "Leader 已验证为节点 {leader}。",
"channelCluster.replicaDetail.transferConflict": "目标节点不是安全的 Leader 候选。",
"channelCluster.replicaDetail.transferError": "Leader 转移失败。",
```

- [ ] **Step 7: Run frontend tests to verify GREEN**

Run:

```bash
cd web && bun run test src/lib/manager-api.test.ts src/pages/channel-cluster/unhealthy/page.test.tsx
```

Expected: PASS.

- [ ] **Step 8: Run frontend build**

Run:

```bash
cd web && bun run build
```

Expected: PASS.

- [ ] **Step 9: Commit frontend changes**

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts web/src/pages/channel-cluster/unhealthy/page.tsx web/src/pages/channel-cluster/unhealthy/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: add channel leader transfer UI"
```

## Task 8: Update Docs And Run Verification

**Files:**
- Modify: `docs/raw/web-admin-restructure.md`
- Modify: `web/README.md`

- [ ] **Step 1: Update docs**

In `docs/raw/web-admin-restructure.md`:

- Change the `POST /manager/channel-cluster/:type/:id/leader/transfer` status to implemented P0.6.
- Update the P0.5/P0.6 section so explicit single-channel safe transfer is done.
- Keep `POST /manager/channel-cluster/batch/leader-drain/:node_id` pending.
- Keep wording aligned with cluster semantics; do not introduce standalone deployment wording.

In `web/README.md`:

- Add `POST /manager/channel-cluster/:type/:id/leader/transfer` to the `/channel-cluster/unhealthy` row.
- Change the note from hidden explicit transfer to implemented single-channel transfer; keep batch leader drain hidden/pending.

- [ ] **Step 2: Run backend focused verification**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelmeta ./internal/access/node ./internal/usecase/management ./internal/access/manager ./internal/app -run 'Test(ChannelLeaderTransfer|TransferChannelCluster|ManagerChannelClusterLeaderTransfer|ManagerChannelClusterTransferAdapter|NewBuildsOptionalManagerServerWhenConfigured)' -count=1
```

Expected: PASS or no tests matched for optional build names; investigate real failures.

- [ ] **Step 3: Run package regression tests**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelmeta ./internal/access/node ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 4: Run import boundary test**

Run:

```bash
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```

Expected: PASS; confirms runtime did not import access/usecase/app layers.

- [ ] **Step 5: Run frontend verification**

Run:

```bash
cd web && bun run test src/lib/manager-api.test.ts src/pages/channel-cluster/unhealthy/page.test.tsx
cd web && bun run build
```

Expected: PASS.

- [ ] **Step 6: Optional full unit suite**

Run only if time is acceptable:

```bash
GOWORK=off go test ./...
```

Expected: PASS. Do not run integration tests unless explicitly requested.

- [ ] **Step 7: Review changed docs and git status**

Run:

```bash
git diff -- docs/raw/web-admin-restructure.md web/README.md
git status --short
```

Expected: only intended files remain changed. Do not stage unrelated untracked files.

- [ ] **Step 8: Commit docs and verification updates**

```bash
git add docs/raw/web-admin-restructure.md web/README.md
git commit -m "docs: document channel leader transfer"
```

## Final Acceptance Criteria

- `POST /manager/channel-cluster/:type/:id/leader/transfer` exists and requires `cluster.channel` write permission.
- Request body is `{ "target_node_id": <node> }` and zero/missing target returns 400.
- Transfer returns 400 when target is not a configured replica.
- Transfer returns 409 when channel is inactive, target is not in ISR, or target cannot safely lead.
- Runtime authoritative transfer rereads latest metadata, rejects stale observed epochs as unchanged, evaluates only the requested target, and writes only `Leader`, `LeaderEpoch`, and `LeaseUntilMS`.
- Runtime preserves `Replicas`, `ISR`, `MinISR`, `Status`, `Features`, `RetentionThroughSeq`, `RetentionUpdatedAtMS`, and `ChannelEpoch`.
- Non-authoritative manager nodes route through node RPC and follow Slot leader redirects.
- Frontend shows transfer only for active non-leader ISR replicas and does not require `reported=true`.
- Frontend refreshes replica detail and unhealthy first page after success.
- Frontend keeps conflict rows visible on 409.
- Docs mark single-channel explicit safe transfer implemented and keep batch leader drain pending.
