# Diagnostics Dynamic Tracking Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let manager operators temporarily retain message diagnostics for one channel or one sending UID from the existing `web/` diagnostics page.

**Architecture:** Add node-local dynamic diagnostics tracking rules that the sampler checks before normal sampling, then expose create/list/delete through manager APIs that fan out to eligible cluster nodes via node RPC. The web page creates TTL-bound sender or channel rules, lists active rules, and reuses the existing diagnostics event view to query retained events.

**Tech Stack:** Go, Gin, existing node RPC binary codecs, in-memory diagnostics store/sampler, React 19, Vite, Vitest, Testing Library.

---

## Pre-Execution Notes

- Execute from this repository root: `/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM`.
- Read `AGENTS.md` before implementation. Current package scan found no `FLOW.md` files in the target packages; re-check before editing any package.
- Use @superpowers:test-driven-development for each task: write failing tests first, run the narrow command, implement minimal code, rerun, commit.
- Keep this feature runtime-only. Do not persist tracking rules to config files, metadata stores, or disk.
- Keep single-node deployments on the normal cluster path; call them single-node clusters in docs/comments/tests.
- Do not expose `from_uid` in manager/web event DTOs. It is allowed only as an internal match/query key.
- Do not update `wukongim.conf.example` unless implementation adds new `WK_` config keys. This plan avoids new config keys.
- If multiple workers implement this with @superpowers:subagent-driven-development, suggested split:
  - Worker A: Tasks 1-2 (`internal/observability/diagnostics`, `pkg/observability/sendtrace`, sender propagation).
  - Worker B: Tasks 3-5 backend API/RPC/usecase wiring after Worker A lands.
  - Worker C: Tasks 6-7 web client/page after backend DTO shapes land.

## Scope Check

The spec touches diagnostics core, node RPC, manager API, app wiring, and web. These are not independent products: the web UI depends on manager DTOs, manager fanout depends on node RPC, and node RPC depends on node-local rule storage. Keep this as one implementation plan with task-level commits.

## File Structure

### Diagnostics core and sampler

- Create: `internal/observability/diagnostics/tracking.go`
  - Node-local TTL-bound tracking rule store, validation, rule matching, list/delete, and bounded rule limits.
- Create: `internal/observability/diagnostics/tracking_test.go`
  - Unit tests for sender/channel matching, TTL expiry, idempotent update, rule limit, and validation.
- Modify: `internal/observability/diagnostics/event.go`
  - Add `Query.UID`, `QueryResult.UID`, and sender UID comments; keep `Event.FromUID` redacted.
- Modify: `internal/observability/diagnostics/index.go`
  - Add bounded UID index and query lookup.
- Modify: `internal/observability/diagnostics/store.go`
  - Filter by `Query.UID`, include it in envelopes, redact event `FromUID` before return.
- Modify: `internal/observability/diagnostics/store_test.go`
  - Add UID query/redaction coverage.
- Modify: `internal/observability/diagnostics/sampler.go`
  - Add dynamic tracking rule source and check it before static debug matches.
- Modify: `internal/observability/diagnostics/sampler_test.go`
  - Add sampler precedence/expiry coverage.
- Modify: `internal/observability/diagnostics/sendtrace_adapter.go`
  - Map `sendtrace.Event.FromUID` to diagnostics `Event.FromUID` before sampling.
- Modify: `internal/observability/diagnostics/sendtrace_adapter_test.go`
  - Verify dynamic sender rules retain events and redaction remains intact.

### Sender UID propagation

- Modify: `pkg/observability/sendtrace/sendtrace.go`
  - Add `FromUID` to the public event struct with an English comment.
- Modify: `pkg/observability/sendtrace/sendtrace_test.go`
  - Verify `Record` carries `FromUID` unchanged.
- Modify: `internal/access/gateway/frame_router.go`
  - Populate `FromUID` on gateway send and sendack sendtrace events.
- Modify: `internal/access/gateway/handler_test.go`
  - Extend existing sendtrace tests with `FromUID` assertions.
- Modify: `internal/usecase/message/send.go`
  - Populate `FromUID` on durable sendtrace event.
- Modify: `internal/usecase/message/send_test.go`
  - Extend durable sendtrace coverage.
- Modify: `internal/app/channelcluster.go`
  - Populate `FromUID` on local and forwarded append sendtrace events from `req.Message.FromUID`.
- Modify: `internal/app/channelcluster_test.go`
  - Extend append diagnostics coverage.

### Node RPC

- Modify: `internal/access/node/service_ids.go`
  - Add `diagnosticsTrackingRPCServiceID` using the next available service id.
- Modify: `internal/access/node/options.go`
  - Add a separate diagnostics tracking provider interface and register the new RPC handler.
- Create: `internal/access/node/diagnostics_tracking_rpc.go`
  - Add adapter handler and client methods for add/list/delete tracking rules.
- Create: `internal/access/node/diagnostics_tracking_codec.go`
  - Binary request/response codec for tracking rules.
- Create: `internal/access/node/diagnostics_tracking_rpc_test.go`
  - Codec, adapter, and client tests.
- Modify: `internal/access/node/diagnostics_codec.go`
  - Add `Query.UID` to diagnostics query encoding/decoding and query result encoding/decoding.
- Modify: `internal/access/node/diagnostics_rpc_test.go`
  - Add UID query round-trip coverage.

### App wiring

- Modify: `internal/app/app.go`
  - Add diagnostics tracking rule store field.
- Modify: `internal/app/build.go`
  - Construct the tracking rule store when diagnostics are enabled; pass it to sampler, node access, and management usecase wiring.
- Modify: `internal/app/diagnostics.go`
  - Add app methods for add/list/delete local rules and local/remote manager tracking reader.
- Modify: `internal/app/observability_test.go`
  - Verify build wires tracking store/sampler when diagnostics are enabled.
- Modify: `internal/app/diagnostics_test.go`
  - Verify manager tracking reader routes local to app and remote to node client.

### Management usecase and manager HTTP

- Create: `internal/usecase/management/diagnostics_tracking.go`
  - Manager-facing request/response DTOs, validation, channel key conversion, node fanout, aggregate status.
- Create: `internal/usecase/management/diagnostics_tracking_test.go`
  - Unit tests for create/list/delete, invalid input, partial fanout, and single-node cluster targeting.
- Modify: `internal/usecase/management/app.go`
  - Add `DiagnosticsTracking` option and app field.
- Modify: `internal/usecase/management/diagnostics.go`
  - Reuse diagnostics target helper for tracking fanout if practical; no behavior change for queries except `UID` support.
- Modify: `internal/access/manager/server.go`
  - Extend `Management` interface with tracking methods.
- Modify: `internal/access/manager/routes.go`
  - Register read and write tracking routes with `cluster.diagnostics:r` and `cluster.diagnostics:w` respectively.
- Modify: `internal/access/manager/diagnostics.go`
  - Parse `uid` and `channel_key` on `/manager/diagnostics/events` and echo `uid` in query DTO.
- Create: `internal/access/manager/diagnostics_tracking.go`
  - HTTP request/response DTOs, JSON binding, validation error mapping, create/list/delete handlers.
- Modify: `internal/access/manager/diagnostics_test.go`
  - Add events query `uid`/`channel_key` tests.
- Create or Modify: `internal/access/manager/diagnostics_tracking_test.go`
  - HTTP route tests for tracking create/list/delete and permissions.
- Modify: `internal/access/manager/permissions.go`
  - Change `cluster.diagnostics` actions to `r,w` and update description.
- Modify: `internal/access/manager/permissions_test.go`
  - Update expected permission catalog.
- Modify if compile requires: `internal/access/manager/server_test.go`
  - Extend manager test stubs with no-op tracking methods.

### Web API client

- Modify: `web/src/lib/manager-api.types.ts`
  - Add diagnostics tracking types and extend diagnostics events params with `uid` and `channelKey`.
- Modify: `web/src/lib/manager-api.ts`
  - Add tracking create/list/delete functions and query param encoding.
- Modify: `web/src/lib/manager-api.test.ts`
  - Add client tests for all new functions and params.

### Web diagnostics page

- Modify: `web/src/pages/diagnostics/page.tsx`
  - Add tracking rule form, list, delete action, and query-from-rule action.
- Modify: `web/src/pages/diagnostics/page.test.tsx`
  - Add page tests for rule lifecycle and validation.
- Modify: `web/src/i18n/messages/en.ts`
  - Add English tracking strings.
- Modify: `web/src/i18n/messages/zh-CN.ts`
  - Add Chinese tracking strings.

### Docs

- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
  - Add one concise note about dynamic diagnostics tracking rules, TTL, and sender-only UID semantics.

---

## Task 1: Add Diagnostics Dynamic Rule Store And UID Query Support

**Files:**
- Create: `internal/observability/diagnostics/tracking.go`
- Create: `internal/observability/diagnostics/tracking_test.go`
- Modify: `internal/observability/diagnostics/event.go`
- Modify: `internal/observability/diagnostics/index.go`
- Modify: `internal/observability/diagnostics/store.go`
- Modify: `internal/observability/diagnostics/store_test.go`
- Modify: `internal/observability/diagnostics/sampler.go`
- Modify: `internal/observability/diagnostics/sampler_test.go`
- Modify: `internal/observability/diagnostics/sendtrace_adapter.go`
- Modify: `internal/observability/diagnostics/sendtrace_adapter_test.go`

- [ ] **Step 1: Write failing diagnostics tracking tests**

Create `internal/observability/diagnostics/tracking_test.go`:

```go
package diagnostics

import (
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

func TestTrackingRulesKeepSenderUIDUntilTTL(t *testing.T) {
    now := time.Unix(100, 0)
    rules := NewTrackingRules(TrackingRulesOptions{Now: func() time.Time { return now }})

    rule, err := rules.Add(TrackingRuleInput{
        ID:         "rule-1",
        Target:     TrackingTargetSenderUID,
        UID:        "u1",
        TTL:        time.Minute,
        SampleRate: 1,
    })
    require.NoError(t, err)
    require.Equal(t, "rule-1", rule.ID)
    require.Equal(t, now.Add(time.Minute), rule.ExpiresAt)

    require.True(t, rules.Keep(Event{FromUID: "u1"}))
    require.False(t, rules.Keep(Event{FromUID: "u2"}))

    now = now.Add(time.Minute + time.Nanosecond)
    require.False(t, rules.Keep(Event{FromUID: "u1"}))
    require.Empty(t, rules.List())
}

func TestTrackingRulesKeepChannel(t *testing.T) {
    rules := NewTrackingRules(TrackingRulesOptions{Now: func() time.Time { return time.Unix(100, 0) }})
    _, err := rules.Add(TrackingRuleInput{
        ID:         "rule-channel",
        Target:     TrackingTargetChannel,
        ChannelKey: "channel/2/ZzE",
        TTL:        time.Hour,
        SampleRate: 1,
    })
    require.NoError(t, err)

    require.True(t, rules.Keep(Event{ChannelKey: "channel/2/ZzE"}))
    require.False(t, rules.Keep(Event{ChannelKey: "channel/2/ZzI"}))
}

func TestTrackingRulesRejectInvalidInputAndLimit(t *testing.T) {
    rules := NewTrackingRules(TrackingRulesOptions{MaxRules: 1, MaxTTL: time.Hour})

    _, err := rules.Add(TrackingRuleInput{ID: "", Target: TrackingTargetSenderUID, UID: "u1", TTL: time.Minute, SampleRate: 1})
    require.Error(t, err)
    _, err = rules.Add(TrackingRuleInput{ID: "bad-target", Target: TrackingTarget("bad"), UID: "u1", TTL: time.Minute, SampleRate: 1})
    require.Error(t, err)
    _, err = rules.Add(TrackingRuleInput{ID: "bad-ttl", Target: TrackingTargetSenderUID, UID: "u1", TTL: 0, SampleRate: 1})
    require.Error(t, err)
    _, err = rules.Add(TrackingRuleInput{ID: "bad-rate", Target: TrackingTargetSenderUID, UID: "u1", TTL: time.Minute, SampleRate: 1.1})
    require.Error(t, err)

    _, err = rules.Add(TrackingRuleInput{ID: "one", Target: TrackingTargetSenderUID, UID: "u1", TTL: time.Minute, SampleRate: 1})
    require.NoError(t, err)
    _, err = rules.Add(TrackingRuleInput{ID: "two", Target: TrackingTargetSenderUID, UID: "u2", TTL: time.Minute, SampleRate: 1})
    require.Error(t, err)
}

func TestTrackingRulesAddIsIdempotentByID(t *testing.T) {
    now := time.Unix(100, 0)
    rules := NewTrackingRules(TrackingRulesOptions{Now: func() time.Time { return now }})

    _, err := rules.Add(TrackingRuleInput{ID: "same", Target: TrackingTargetSenderUID, UID: "u1", TTL: time.Minute, SampleRate: 1})
    require.NoError(t, err)
    updated, err := rules.Add(TrackingRuleInput{ID: "same", Target: TrackingTargetChannel, ChannelKey: "channel/2/ZzE", TTL: 2 * time.Minute, SampleRate: 1})
    require.NoError(t, err)

    require.Equal(t, TrackingTargetChannel, updated.Target)
    require.False(t, rules.Keep(Event{FromUID: "u1"}))
    require.True(t, rules.Keep(Event{ChannelKey: "channel/2/ZzE"}))
    require.Len(t, rules.List(), 1)
}
```

Append to `internal/observability/diagnostics/store_test.go`:

```go
func TestStoreQueryByUIDRedactsFromUID(t *testing.T) {
    store := NewStore(StoreOptions{NodeID: 1})
    store.Record(Event{TraceID: "trace-u1", FromUID: "u1", Stage: "message.send_durable", Result: ResultOK})
    store.Record(Event{TraceID: "trace-u2", FromUID: "u2", Stage: "message.send_durable", Result: ResultOK})

    result := store.Query(context.Background(), Query{UID: "u1", Limit: 10})

    require.Equal(t, StatusOK, result.Status)
    require.Equal(t, "u1", result.UID)
    require.Len(t, result.Events, 1)
    require.Equal(t, "trace-u1", result.Events[0].TraceID)
    require.Empty(t, result.Events[0].FromUID)
}
```

Append to `internal/observability/diagnostics/sampler_test.go`:

```go
func TestSamplerPrefersDynamicTrackingRules(t *testing.T) {
    now := time.Unix(100, 0)
    rules := NewTrackingRules(TrackingRulesOptions{Now: func() time.Time { return now }})
    _, err := rules.Add(TrackingRuleInput{ID: "uid-u1", Target: TrackingTargetSenderUID, UID: "u1", TTL: time.Minute, SampleRate: 1})
    require.NoError(t, err)

    sampler := NewSampler(SamplerOptions{SampleRate: 0, TrackingRules: rules, Now: func() time.Time { return now }})
    keep, reason := sampler.Keep(Event{FromUID: "u1", Result: ResultOK})

    require.True(t, keep)
    require.Equal(t, "debug", reason)
}
```

Append to `internal/observability/diagnostics/sendtrace_adapter_test.go`:

```go
func TestSendTraceAdapterUsesSenderTrackingAndRedactsUID(t *testing.T) {
    rules := NewTrackingRules(TrackingRulesOptions{})
    _, err := rules.Add(TrackingRuleInput{ID: "sender", Target: TrackingTargetSenderUID, UID: "u1", TTL: time.Hour, SampleRate: 1})
    require.NoError(t, err)

    store := NewStore(StoreOptions{NodeID: 1})
    adapter := NewSendTraceSink(store, NewSampler(SamplerOptions{SampleRate: 0, TrackingRules: rules}))
    adapter.RecordSendTrace(sendtrace.Event{TraceID: "trace-u1", FromUID: "u1", Stage: sendtrace.StageMessageSendDurable, Result: sendtrace.ResultOK})

    result := store.Query(context.Background(), Query{UID: "u1", Limit: 10})
    require.Equal(t, StatusOK, result.Status)
    require.Len(t, result.Events, 1)
    require.Equal(t, "debug", result.Events[0].SampleReason)
    require.Empty(t, result.Events[0].FromUID)
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./internal/observability/diagnostics -run 'Test(TrackingRules|StoreQueryByUIDRedactsFromUID|SamplerPrefersDynamicTrackingRules|SendTraceAdapterUsesSenderTrackingAndRedactsUID)' -count=1
```

Expected: FAIL because `TrackingRules`, `TrackingRuleInput`, `Query.UID`, `SamplerOptions.TrackingRules`, and `sendtrace.Event.FromUID` do not exist.

- [ ] **Step 3: Implement tracking rule store**

Create `internal/observability/diagnostics/tracking.go` with:

```go
package diagnostics

import (
    "errors"
    "math"
    "sync"
    "sync/atomic"
    "time"
)

const (
    DefaultMaxTrackingRules = 100
    DefaultMaxTrackingTTL   = 24 * time.Hour
)

var ErrInvalidTrackingRule = errors.New("diagnostics: invalid tracking rule")
var ErrTrackingRuleLimit = errors.New("diagnostics: tracking rule limit reached")

type TrackingTarget string

const (
    TrackingTargetChannel   TrackingTarget = "channel"
    TrackingTargetSenderUID TrackingTarget = "sender_uid"
)

type TrackingRuleInput struct {
    ID         string
    Target     TrackingTarget
    UID        string
    ChannelKey string
    TTL        time.Duration
    SampleRate float64
}

type TrackingRule struct {
    ID         string         `json:"rule_id"`
    Target     TrackingTarget `json:"target"`
    UID        string         `json:"uid,omitempty"`
    ChannelKey string         `json:"channel_key,omitempty"`
    SampleRate float64        `json:"sample_rate"`
    CreatedAt  time.Time      `json:"created_at"`
    ExpiresAt  time.Time      `json:"expires_at"`
}

type TrackingRulesOptions struct {
    MaxRules int
    MaxTTL   time.Duration
    Now      func() time.Time
}

type trackingRuleState struct {
    rule    TrackingRule
    counter atomic.Uint64
}

type TrackingRules struct {
    mu       sync.Mutex
    rules    map[string]*trackingRuleState
    snapshot atomic.Value // []*trackingRuleState
    maxRules int
    maxTTL   time.Duration
    now      func() time.Time
}
```

Implement:

```go
func NewTrackingRules(opts TrackingRulesOptions) *TrackingRules
func (r *TrackingRules) Add(input TrackingRuleInput) (TrackingRule, error)
func (r *TrackingRules) List() []TrackingRule
func (r *TrackingRules) Delete(id string) bool
func (r *TrackingRules) Keep(event Event) bool
```

Implementation rules:

- `NewTrackingRules` defaults `MaxRules` and `MaxTTL`, initializes `snapshot` to an empty `[]*trackingRuleState`.
- `Add` validates ID, target-specific fields, TTL, TTL <= max, sample rate in `[0,1]` and updates same ID idempotently.
- `Add`, `List`, and `Delete` call `pruneLocked(now)` before returning.
- `Keep` reads only the atomic snapshot, checks expiry using `now`, matches target, then uses existing `keepByRate(rule.SampleRate, &state.counter)`.
- `List` returns a stable copy sorted by `ID` for deterministic tests.

- [ ] **Step 4: Implement UID query, sampler wiring, and adapter mapping**

Modify `internal/observability/diagnostics/event.go`:

```go
type Query struct {
    TraceID     string `json:"trace_id,omitempty"`
    ClientMsgNo string `json:"client_msg_no,omitempty"`
    ChannelKey  string `json:"channel_key,omitempty"`
    UID         string `json:"uid,omitempty"`
    MessageSeq  uint64 `json:"message_seq,omitempty"`
    Stage       Stage  `json:"stage,omitempty"`
    Result      Result `json:"result,omitempty"`
    Limit       int    `json:"limit,omitempty"`
}

type QueryResult struct {
    Scope       string       `json:"scope"`
    NodeID      uint64       `json:"node_id"`
    TraceID     string       `json:"trace_id,omitempty"`
    ClientMsgNo string       `json:"client_msg_no,omitempty"`
    ChannelKey  string       `json:"channel_key,omitempty"`
    UID         string       `json:"uid,omitempty"`
    MessageSeq  uint64       `json:"message_seq,omitempty"`
    Query       Query        `json:"query"`
    Status      Status       `json:"status"`
    StartedAt   time.Time    `json:"started_at,omitempty"`
    DurationMS  int64        `json:"duration_ms,omitempty"`
    Summary     QuerySummary `json:"summary,omitempty"`
    Events      []Event      `json:"events"`
    Notes       []string     `json:"notes,omitempty"`
}
```

Modify `internal/observability/diagnostics/index.go`:

```go
type indexes struct {
    trace       *boundedIndex
    client      *boundedIndex
    channel     *boundedIndex
    uid         *boundedIndex
    channelSeq  *boundedIndex
    node        *boundedIndex
    peerNode    *boundedIndex
    stage       *boundedIndex
    channelSpan *boundedRangeIndex
}
```

Add `event.FromUID` indexing and `q.UID` lookup.

Modify `store.go`:

- `matchesQuery` rejects events whose `FromUID` does not match `q.UID`.
- `notFoundResult` and `buildQueryResult` set `UID: q.UID`.
- `redactEvent` continues clearing `FromUID`.

Modify `sampler.go`:

```go
type SamplerOptions struct {
    SampleRate float64
    SlowThreshold time.Duration
    ErrorSampleRate float64
    ErrorSampleRateSet bool
    DebugMatches []DebugMatch
    TrackingRules *TrackingRules
    Now func() time.Time
}

type Sampler struct {
    trackingRules *TrackingRules
    // existing fields...
}

func (s *Sampler) Keep(event Event) (bool, string) {
    if s == nil {
        return false, ""
    }
    if s.trackingRules != nil && s.trackingRules.Keep(event) {
        return true, "debug"
    }
    // existing debug/slow/error/sample logic...
}
```

Modify `sendtrace_adapter.go` to map `FromUID: event.FromUID`.

- [ ] **Step 5: Run diagnostics tests**

Run:

```bash
GOWORK=off go test ./internal/observability/diagnostics -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit diagnostics core**

```bash
git add internal/observability/diagnostics
git commit -m "feat: add dynamic diagnostics tracking rules"
```

---

## Task 2: Propagate Sender UID Through Sendtrace Events

**Files:**
- Modify: `pkg/observability/sendtrace/sendtrace.go`
- Modify: `pkg/observability/sendtrace/sendtrace_test.go`
- Modify: `internal/access/gateway/frame_router.go`
- Modify: `internal/access/gateway/handler_test.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/app/channelcluster.go`
- Modify: `internal/app/channelcluster_test.go`

- [ ] **Step 1: Write failing sender propagation tests**

Append to `pkg/observability/sendtrace/sendtrace_test.go`:

```go
func TestRecordCarriesFromUID(t *testing.T) {
    sink := &recordingSink{}
    restore := SetSink(sink)
    defer restore()

    Record(Event{Stage: StageGatewayMessagesSend, TraceID: "trace-1", FromUID: "u1", Result: ResultOK})

    require.Len(t, sink.events, 1)
    require.Equal(t, "u1", sink.events[0].FromUID)
}
```

Update existing gateway sendtrace test in `internal/access/gateway/handler_test.go` that captures `sendEvent` and `ackEvent` to assert:

```go
require.Equal(t, "u1", sendEvent.FromUID)
require.Equal(t, "u1", ackEvent.FromUID)
```

Add or update a durable send test in `internal/usecase/message/send_test.go`:

```go
func TestSendDurableRecordsSenderUIDInSendTrace(t *testing.T) {
    sink := &recordingSendTraceSink{}
    restore := sendtrace.SetSink(sink)
    defer restore()

    app := newSendTestApp(t)
    _, err := app.Send(context.Background(), SendCommand{
        FromUID:     "sender-1",
        ChannelID:   "g1",
        ChannelType: frame.ChannelTypeGroup,
        ClientMsgNo: "cm-1",
    })
    require.NoError(t, err)

    event := sink.mustFind(t, sendtrace.StageMessageSendDurable)
    require.Equal(t, "sender-1", event.FromUID)
}
```

If `newSendTestApp` or `recordingSendTraceSink` names differ, reuse the existing test helpers in the file rather than creating a second fixture style.

Update `internal/app/channelcluster_test.go` append diagnostics coverage to assert local/forwarded append events include `FromUID` from `req.Message.FromUID`.

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./pkg/observability/sendtrace ./internal/access/gateway ./internal/usecase/message ./internal/app -run 'Test.*(FromUID|SenderUID|SendTrace|Diagnostics)' -count=1
```

Expected: FAIL because `sendtrace.Event.FromUID` does not exist or is not populated.

- [ ] **Step 3: Add `FromUID` field and populate sendtrace records**

Modify `pkg/observability/sendtrace/sendtrace.go`:

```go
type Event struct {
    // existing fields...
    // FromUID is the sending UID used for diagnostics sampling and must not be exposed in manager event DTOs.
    FromUID string
    // existing fields...
}
```

Modify `internal/access/gateway/frame_router.go` in both `sendtrace.Record` calls:

```go
FromUID: cmd.FromUID,
```

Modify `writeSendackWithTrace` signature to accept `fromUID`:

```go
func (h *Handler) writeSendackWithTrace(ctx *coregateway.Context, pkt *frame.SendPacket, clientMsgNo, traceID, channelKey, fromUID string, result message.SendResult) error
```

Update both call sites to pass `cmd.FromUID`.

Modify `internal/usecase/message/send.go` durable sendtrace record:

```go
FromUID: cmd.FromUID,
```

Modify `internal/app/channelcluster.go` local and forwarded append sendtrace records:

```go
FromUID: req.Message.FromUID,
```

Do not add `channel.Record` diagnostics metadata in this task unless a test proves sender UID rules must retain deeper replica-only stages. Channel tracking already retains those by `channel_key`.

- [ ] **Step 4: Run focused tests**

Run:

```bash
GOWORK=off go test ./pkg/observability/sendtrace ./internal/observability/diagnostics ./internal/access/gateway ./internal/usecase/message ./internal/app -run 'Test.*(FromUID|SenderUID|SendTrace|Diagnostics)' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit sender propagation**

```bash
git add pkg/observability/sendtrace internal/access/gateway internal/usecase/message internal/app
git commit -m "feat: propagate sender uid to diagnostics sendtrace"
```

---

## Task 3: Add Node RPC For Diagnostics Tracking Rules

**Files:**
- Modify: `internal/access/node/service_ids.go`
- Modify: `internal/access/node/options.go`
- Create: `internal/access/node/diagnostics_tracking_rpc.go`
- Create: `internal/access/node/diagnostics_tracking_codec.go`
- Create: `internal/access/node/diagnostics_tracking_rpc_test.go`
- Modify: `internal/access/node/diagnostics_codec.go`
- Modify: `internal/access/node/diagnostics_rpc_test.go`

- [ ] **Step 1: Write failing RPC codec and client tests**

Create `internal/access/node/diagnostics_tracking_rpc_test.go`:

```go
package node

import (
    "context"
    "testing"
    "time"

    "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
    "github.com/stretchr/testify/require"
)

func TestDiagnosticsTrackingCodecRoundTrip(t *testing.T) {
    req := diagnosticsTrackingRequest{
        Op: diagnosticsTrackingOpAdd,
        Rule: diagnostics.TrackingRuleInput{
            ID:         "rule-1",
            Target:     diagnostics.TrackingTargetSenderUID,
            UID:        "u1",
            TTL:        time.Hour,
            SampleRate: 1,
        },
    }

    body, err := encodeDiagnosticsTrackingRequest(req)
    require.NoError(t, err)
    got, err := decodeDiagnosticsTrackingRequest(body)
    require.NoError(t, err)

    require.Equal(t, req.Op, got.Op)
    require.Equal(t, req.Rule.ID, got.Rule.ID)
    require.Equal(t, req.Rule.Target, got.Rule.Target)
    require.Equal(t, req.Rule.UID, got.Rule.UID)
    require.Equal(t, req.Rule.TTL, got.Rule.TTL)
    require.Equal(t, req.Rule.SampleRate, got.Rule.SampleRate)
}

func TestDiagnosticsTrackingAdapterAddListDelete(t *testing.T) {
    provider := &diagnosticsTrackingProviderStub{}
    adapter := New(Options{DiagnosticsTracking: provider})

    addBody, err := encodeDiagnosticsTrackingRequest(diagnosticsTrackingRequest{
        Op: diagnosticsTrackingOpAdd,
        Rule: diagnostics.TrackingRuleInput{ID: "rule-1", Target: diagnostics.TrackingTargetChannel, ChannelKey: "channel/2/ZzE", TTL: time.Minute, SampleRate: 1},
    })
    require.NoError(t, err)
    addRespBody, err := adapter.handleDiagnosticsTrackingRPC(context.Background(), addBody)
    require.NoError(t, err)
    addResp, err := decodeDiagnosticsTrackingResponse(addRespBody)
    require.NoError(t, err)
    require.Equal(t, rpcStatusOK, addResp.Status)
    require.Equal(t, "rule-1", provider.added.ID)

    listBody, err := encodeDiagnosticsTrackingRequest(diagnosticsTrackingRequest{Op: diagnosticsTrackingOpList})
    require.NoError(t, err)
    _, err = adapter.handleDiagnosticsTrackingRPC(context.Background(), listBody)
    require.NoError(t, err)
    require.True(t, provider.listed)

    deleteBody, err := encodeDiagnosticsTrackingRequest(diagnosticsTrackingRequest{Op: diagnosticsTrackingOpDelete, RuleID: "rule-1"})
    require.NoError(t, err)
    _, err = adapter.handleDiagnosticsTrackingRPC(context.Background(), deleteBody)
    require.NoError(t, err)
    require.Equal(t, "rule-1", provider.deleted)
}

type diagnosticsTrackingProviderStub struct {
    added   diagnostics.TrackingRuleInput
    listed  bool
    deleted string
}

func (s *diagnosticsTrackingProviderStub) AddDiagnosticsTrackingRule(_ context.Context, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
    s.added = input
    return diagnostics.TrackingRule{ID: input.ID, Target: input.Target, UID: input.UID, ChannelKey: input.ChannelKey, SampleRate: input.SampleRate}, nil
}

func (s *diagnosticsTrackingProviderStub) ListDiagnosticsTrackingRules(context.Context) ([]diagnostics.TrackingRule, error) {
    s.listed = true
    return []diagnostics.TrackingRule{{ID: "rule-1"}}, nil
}

func (s *diagnosticsTrackingProviderStub) DeleteDiagnosticsTrackingRule(_ context.Context, ruleID string) error {
    s.deleted = ruleID
    return nil
}
```

Update `internal/access/node/diagnostics_rpc_test.go` query round-trip to include UID:

```go
UID: "u1",
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./internal/access/node -run 'TestDiagnostics(Tracking|Request|Response|RPC)' -count=1
```

Expected: FAIL because diagnostics tracking RPC types, codec, provider option, service id, and `Query.UID` codec support do not exist.

- [ ] **Step 3: Implement provider interfaces and service registration**

Modify `internal/access/node/options.go`:

```go
type DiagnosticsTrackingProvider interface {
    AddDiagnosticsTrackingRule(ctx context.Context, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error)
    ListDiagnosticsTrackingRules(ctx context.Context) ([]diagnostics.TrackingRule, error)
    DeleteDiagnosticsTrackingRule(ctx context.Context, ruleID string) error
}
```

Add to `Options` and `Adapter`:

```go
DiagnosticsTracking DiagnosticsTrackingProvider
```

Register:

```go
opts.Cluster.RPCMux().Handle(diagnosticsTrackingRPCServiceID, adapter.handleDiagnosticsTrackingRPC)
```

Modify `internal/access/node/service_ids.go`:

```go
diagnosticsTrackingRPCServiceID uint8 = 51
```

Use the next free id if `51` is already taken by local changes.

- [ ] **Step 4: Implement tracking RPC codec and client**

Create op constants in `diagnostics_tracking_codec.go`:

```go
type diagnosticsTrackingOp string

const (
    diagnosticsTrackingOpAdd    diagnosticsTrackingOp = "add"
    diagnosticsTrackingOpList   diagnosticsTrackingOp = "list"
    diagnosticsTrackingOpDelete diagnosticsTrackingOp = "delete"
)
```

Use a binary codec with a new magic header, for example:

```go
var diagnosticsTrackingRequestMagic = [...]byte{'W', 'K', 'D', 'T', 'Q', 1}
var diagnosticsTrackingResponseMagic = [...]byte{'W', 'K', 'D', 'T', 'R', 1}
```

Request fields:

- op string
- rule id
- target string
- uid
- channel key
- ttl nanoseconds or milliseconds as varint
- sample rate as string or IEEE bits; prefer existing helpers if available
- delete rule id

Response fields:

- status string
- error string
- one rule
- rule list

Create `internal/access/node/diagnostics_tracking_rpc.go`:

```go
func (a *Adapter) handleDiagnosticsTrackingRPC(ctx context.Context, body []byte) ([]byte, error)
func (c *Client) AddDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error)
func (c *Client) ListDiagnosticsTrackingRules(ctx context.Context, nodeID uint64) ([]diagnostics.TrackingRule, error)
func (c *Client) DeleteDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, ruleID string) error
```

Handler rules:

- If `a.diagnosticsTracking == nil`, return `rpcStatusRejected` with a clear error message.
- `add` calls local provider add.
- `list` calls local provider list.
- `delete` calls local provider delete.
- Unknown op returns an error.

Client rules:

- Use `c.cluster.RPCService(ctx, multiraft.NodeID(nodeID), 0, diagnosticsTrackingRPCServiceID, body)`.
- Decode response and map non-OK status to an error containing the remote error string.

- [ ] **Step 5: Add UID to existing diagnostics query codec**

Modify `appendDiagnosticsQuery` and `readDiagnosticsQuery` in `internal/access/node/diagnostics_codec.go` to write/read `query.UID` after `ChannelKey` and before `MessageSeq`.

Also modify `appendDiagnosticsQueryResult` and `readDiagnosticsQueryResult` to include `result.UID` after `ChannelKey` and before `MessageSeq`.

Because this binary codec is internal and versioned by magic byte, update the magic version from `1` to `2` if compatibility with already-running old nodes is required. For this repo-first implementation, prefer bumping to `2` so decoding failures are explicit across mixed versions.

- [ ] **Step 6: Run node RPC tests**

Run:

```bash
GOWORK=off go test ./internal/access/node -run 'TestDiagnostics' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit node RPC**

```bash
git add internal/access/node
git commit -m "feat: add diagnostics tracking node rpc"
```

---

## Task 4: Wire Diagnostics Tracking Through App

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/diagnostics.go`
- Modify: `internal/app/observability_test.go`
- Modify: `internal/app/diagnostics_test.go`

- [ ] **Step 1: Write failing app wiring tests**

Append to `internal/app/diagnostics_test.go`:

```go
func TestManagementDiagnosticsTrackingReaderRoutesLocalNodeToApp(t *testing.T) {
    local := &managementDiagnosticsTrackingLocalFake{
        addRule: diagnostics.TrackingRule{ID: "rule-local"},
        rules:   []diagnostics.TrackingRule{{ID: "rule-local"}},
    }
    remote := &managementDiagnosticsTrackingRemoteFake{err: errors.New("remote should not be called")}
    reader := managementDiagnosticsTrackingReader{localNodeID: 1, local: local, remote: remote}

    got, err := reader.AddNodeDiagnosticsTrackingRule(context.Background(), 1, diagnostics.TrackingRuleInput{ID: "rule-local"})
    require.NoError(t, err)
    require.Equal(t, "rule-local", got.ID)

    listed, err := reader.ListNodeDiagnosticsTrackingRules(context.Background(), 1)
    require.NoError(t, err)
    require.Len(t, listed, 1)

    require.NoError(t, reader.DeleteNodeDiagnosticsTrackingRule(context.Background(), 1, "rule-local"))
    require.Equal(t, "rule-local", local.deleted)
}

func TestManagementDiagnosticsTrackingReaderRoutesRemoteNodeToClient(t *testing.T) {
    local := &managementDiagnosticsTrackingLocalFake{err: errors.New("local should not be called")}
    remote := &managementDiagnosticsTrackingRemoteFake{addRule: diagnostics.TrackingRule{ID: "rule-remote"}}
    reader := managementDiagnosticsTrackingReader{localNodeID: 1, local: local, remote: remote}

    got, err := reader.AddNodeDiagnosticsTrackingRule(context.Background(), 2, diagnostics.TrackingRuleInput{ID: "rule-remote"})
    require.NoError(t, err)
    require.Equal(t, uint64(2), remote.nodeID)
    require.Equal(t, "rule-remote", got.ID)
}
```

Add fakes in the same file, mirroring existing diagnostics reader fakes:

```go
type managementDiagnosticsTrackingLocalFake struct {
    addRule diagnostics.TrackingRule
    rules   []diagnostics.TrackingRule
    deleted string
    err     error
}

func (f *managementDiagnosticsTrackingLocalFake) AddDiagnosticsTrackingRule(context.Context, diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
    return f.addRule, f.err
}
func (f *managementDiagnosticsTrackingLocalFake) ListDiagnosticsTrackingRules(context.Context) ([]diagnostics.TrackingRule, error) {
    return f.rules, f.err
}
func (f *managementDiagnosticsTrackingLocalFake) DeleteDiagnosticsTrackingRule(_ context.Context, ruleID string) error {
    f.deleted = ruleID
    return f.err
}

type managementDiagnosticsTrackingRemoteFake struct {
    nodeID  uint64
    addRule diagnostics.TrackingRule
    rules   []diagnostics.TrackingRule
    deleted string
    err     error
}

func (f *managementDiagnosticsTrackingRemoteFake) AddDiagnosticsTrackingRule(_ context.Context, nodeID uint64, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
    f.nodeID = nodeID
    return f.addRule, f.err
}
func (f *managementDiagnosticsTrackingRemoteFake) ListDiagnosticsTrackingRules(_ context.Context, nodeID uint64) ([]diagnostics.TrackingRule, error) {
    f.nodeID = nodeID
    return f.rules, f.err
}
func (f *managementDiagnosticsTrackingRemoteFake) DeleteDiagnosticsTrackingRule(_ context.Context, nodeID uint64, ruleID string) error {
    f.nodeID = nodeID
    f.deleted = ruleID
    return f.err
}
```

Append to `internal/app/observability_test.go`:

```go
func TestBuildWiresDiagnosticsTrackingRules(t *testing.T) {
    cfg := testConfig(t)
    cfg.Observability.Diagnostics.Enabled = true
    cfg.Observability.Diagnostics.SampleRate = 0

    app, err := Build(cfg)
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, app.Stop(context.Background())) })

    _, err = app.AddDiagnosticsTrackingRule(context.Background(), diagnostics.TrackingRuleInput{
        ID: "rule-1", Target: diagnostics.TrackingTargetSenderUID, UID: "u1", TTL: time.Minute, SampleRate: 1,
    })
    require.NoError(t, err)

    sendtrace.Record(sendtrace.Event{TraceID: "trace-u1", FromUID: "u1", Stage: sendtrace.StageMessageSendDurable, Result: sendtrace.ResultOK})
    result := app.QueryDiagnostics(context.Background(), diagnostics.Query{UID: "u1", Limit: 10})
    require.Equal(t, diagnostics.StatusOK, result.Status)
}
```

Adjust helper names to match existing `observability_test.go` fixtures.

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
GOWORK=off go test ./internal/app -run 'Test(ManagementDiagnosticsTrackingReader|BuildWiresDiagnosticsTrackingRules)' -count=1
```

Expected: FAIL because app tracking fields/methods and tracking reader do not exist.

- [ ] **Step 3: Add app tracking fields and methods**

Modify `internal/app/app.go`:

```go
diagnosticsTracking *obsdiagnostics.TrackingRules
```

Modify `internal/app/build.go` diagnostics block:

```go
if cfg.Observability.Diagnostics.Enabled {
    app.diagnostics = obsdiagnostics.NewStore(diagnosticsStoreOptions(cfg))
    app.diagnosticsTracking = obsdiagnostics.NewTrackingRules(obsdiagnostics.TrackingRulesOptions{})
    samplerOpts := diagnosticsSamplerOptions(cfg)
    samplerOpts.TrackingRules = app.diagnosticsTracking
    sink := obsdiagnostics.NewSendTraceSink(app.diagnostics, obsdiagnostics.NewSampler(samplerOpts))
    // existing metrics and restore wiring
}
```

Pass app as diagnostics tracking provider to node access:

```go
DiagnosticsTracking: app,
```

Pass a tracking reader to management usecase:

```go
DiagnosticsTracking: managementDiagnosticsTrackingReader{
    localNodeID: cfg.Node.ID,
    local:       app,
    remote:      app.nodeClient,
},
```

- [ ] **Step 4: Implement local and routing methods**

Modify `internal/app/diagnostics.go`:

```go
func (a *App) AddDiagnosticsTrackingRule(ctx context.Context, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error) {
    if a == nil || a.diagnosticsTracking == nil {
        return diagnostics.TrackingRule{}, fmt.Errorf("app: diagnostics tracking not configured")
    }
    return a.diagnosticsTracking.Add(input)
}

func (a *App) ListDiagnosticsTrackingRules(ctx context.Context) ([]diagnostics.TrackingRule, error) {
    if a == nil || a.diagnosticsTracking == nil {
        return nil, fmt.Errorf("app: diagnostics tracking not configured")
    }
    return a.diagnosticsTracking.List(), nil
}

func (a *App) DeleteDiagnosticsTrackingRule(ctx context.Context, ruleID string) error {
    if a == nil || a.diagnosticsTracking == nil {
        return fmt.Errorf("app: diagnostics tracking not configured")
    }
    a.diagnosticsTracking.Delete(ruleID)
    return nil
}
```

Add local/remote interfaces and reader:

```go
type managementDiagnosticsTrackingLocal interface {
    AddDiagnosticsTrackingRule(ctx context.Context, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error)
    ListDiagnosticsTrackingRules(ctx context.Context) ([]diagnostics.TrackingRule, error)
    DeleteDiagnosticsTrackingRule(ctx context.Context, ruleID string) error
}

type managementDiagnosticsTrackingRemote interface {
    AddDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error)
    ListDiagnosticsTrackingRules(ctx context.Context, nodeID uint64) ([]diagnostics.TrackingRule, error)
    DeleteDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, ruleID string) error
}
```

Implement `AddNodeDiagnosticsTrackingRule`, `ListNodeDiagnosticsTrackingRules`, and `DeleteNodeDiagnosticsTrackingRule` with the same local short-circuit pattern as `managementDiagnosticsReader.QueryNodeDiagnostics`.

- [ ] **Step 5: Run app tests**

Run:

```bash
GOWORK=off go test ./internal/app -run 'Test(ManagementDiagnostics|BuildWiresDiagnostics|StopRestoresDiagnostics)' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit app wiring**

```bash
git add internal/app
git commit -m "feat: wire diagnostics tracking rules through app"
```

---

## Task 5: Add Management Usecase And Manager HTTP APIs

**Files:**
- Create: `internal/usecase/management/diagnostics_tracking.go`
- Create: `internal/usecase/management/diagnostics_tracking_test.go`
- Modify: `internal/usecase/management/app.go`
- Modify: `internal/usecase/management/diagnostics.go`
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/diagnostics.go`
- Create: `internal/access/manager/diagnostics_tracking.go`
- Modify: `internal/access/manager/diagnostics_test.go`
- Create or Modify: `internal/access/manager/diagnostics_tracking_test.go`
- Modify: `internal/access/manager/permissions.go`
- Modify: `internal/access/manager/permissions_test.go`
- Modify if needed: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write failing management usecase tests**

Create `internal/usecase/management/diagnostics_tracking_test.go`:

```go
package management

import (
    "context"
    "errors"
    "testing"
    "time"

    "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
    controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
    "github.com/stretchr/testify/require"
)

func TestCreateDiagnosticsTrackingRuleForSenderUID(t *testing.T) {
    tracker := &diagnosticsTrackingStub{}
    app := New(Options{
        LocalNodeID: 1,
        Cluster: diagnosticsClusterStub{nodes: []controllermeta.ClusterNode{
            {NodeID: 1, Status: controllermeta.NodeStatusAlive},
            {NodeID: 2, Status: controllermeta.NodeStatusAlive},
        }},
        DiagnosticsTracking: tracker,
        Now: func() time.Time { return time.Unix(100, 0) },
    })

    resp, err := app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{
        Target: "sender_uid", UID: "u1", TTLSeconds: 3600, SampleRate: 1,
    })

    require.NoError(t, err)
    require.Equal(t, DiagnosticsTrackingStatusOK, resp.Status)
    require.NotEmpty(t, resp.Rule.ID)
    require.Equal(t, diagnostics.TrackingTargetSenderUID, resp.Rule.Target)
    require.Equal(t, "u1", resp.Rule.UID)
    require.Len(t, resp.Nodes, 2)
    require.Equal(t, resp.Rule.ID, tracker.added[1].ID)
    require.Equal(t, resp.Rule.ID, tracker.added[2].ID)
}

func TestCreateDiagnosticsTrackingRuleConvertsChannelKey(t *testing.T) {
    tracker := &diagnosticsTrackingStub{}
    app := New(Options{LocalNodeID: 1, Cluster: diagnosticsClusterStub{nodes: []controllermeta.ClusterNode{{NodeID: 1, Status: controllermeta.NodeStatusAlive}}}, DiagnosticsTracking: tracker})

    resp, err := app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{
        Target: "channel", ChannelID: "g1", ChannelType: 2, TTLSeconds: 60, SampleRate: 1,
    })

    require.NoError(t, err)
    require.Equal(t, "channel/2/ZzE", resp.Rule.ChannelKey)
    require.Equal(t, "channel/2/ZzE", tracker.added[1].ChannelKey)
}

func TestCreateDiagnosticsTrackingRuleReturnsPartialWhenNodeFails(t *testing.T) {
    tracker := &diagnosticsTrackingStub{failNodes: map[uint64]error{2: errors.New("rpc timeout")}}
    app := New(Options{LocalNodeID: 1, Cluster: diagnosticsClusterStub{nodes: []controllermeta.ClusterNode{
        {NodeID: 1, Status: controllermeta.NodeStatusAlive},
        {NodeID: 2, Status: controllermeta.NodeStatusAlive},
    }}, DiagnosticsTracking: tracker})

    resp, err := app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{Target: "sender_uid", UID: "u1", TTLSeconds: 60, SampleRate: 1})

    require.NoError(t, err)
    require.Equal(t, DiagnosticsTrackingStatusPartial, resp.Status)
    require.Len(t, resp.Nodes, 2)
}

func TestCreateDiagnosticsTrackingRuleRejectsInvalidInput(t *testing.T) {
    app := New(Options{})
    _, err := app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{Target: "sender_uid", UID: "", TTLSeconds: 60, SampleRate: 1})
    require.Error(t, err)
    _, err = app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{Target: "channel", ChannelID: "g1", ChannelType: 0, TTLSeconds: 60, SampleRate: 1})
    require.Error(t, err)
    _, err = app.CreateDiagnosticsTrackingRule(context.Background(), DiagnosticsTrackingCreateRequest{Target: "sender_uid", UID: "u1", TTLSeconds: 0, SampleRate: 1})
    require.Error(t, err)
}
```

Add stubs in the same file. Reuse existing cluster stub style from `diagnostics_test.go` when possible.

- [ ] **Step 2: Write failing manager HTTP tests**

Create `internal/access/manager/diagnostics_tracking_test.go` with tests for:

```go
func TestManagerDiagnosticsTrackingCreateSenderUID(t *testing.T)
func TestManagerDiagnosticsTrackingCreateChannel(t *testing.T)
func TestManagerDiagnosticsTrackingListRequiresReadPermission(t *testing.T)
func TestManagerDiagnosticsTrackingCreateRequiresWritePermission(t *testing.T)
func TestManagerDiagnosticsTrackingDeleteRequiresWritePermission(t *testing.T)
func TestManagerDiagnosticsTrackingRejectsInvalidRequest(t *testing.T)
```

Core create test shape:

```go
func TestManagerDiagnosticsTrackingCreateSenderUID(t *testing.T) {
    var received managementusecase.DiagnosticsTrackingCreateRequest
    srv := newDiagnosticsTrackingTestServer(t, diagnosticsTrackingHTTPStub{
        createSink: &received,
        createResp: managementusecase.DiagnosticsTrackingMutationResponse{
            Status: managementusecase.DiagnosticsTrackingStatusOK,
            Rule: managementusecase.DiagnosticsTrackingRule{ID: "rule-1", Target: "sender_uid", UID: "u1", SampleRate: 1},
            Nodes: []managementusecase.DiagnosticsTrackingNodeResult{{NodeID: 1, Status: "ok"}},
        },
    })

    rec := performDiagnosticsRequest(t, srv, "/manager/diagnostics/tracking-rules", "admin", http.MethodPost, `{"target":"sender_uid","uid":"u1","ttl_seconds":3600,"sample_rate":1}`)

    require.Equal(t, http.StatusOK, rec.Code)
    require.Equal(t, "sender_uid", received.Target)
    require.Equal(t, "u1", received.UID)
    require.JSONEq(t, `{"status":"ok","rule":{"rule_id":"rule-1","target":"sender_uid","uid":"u1","sample_rate":1},"nodes":[{"node_id":1,"status":"ok","notes":[]}],"notes":[]}`, rec.Body.String())
}
```

If `performDiagnosticsRequest` currently only supports GET, either extend it locally or create a new helper that accepts method/body.

Add to `internal/access/manager/diagnostics_test.go`:

```go
func TestManagerDiagnosticsEventsQueriesUIDAndChannelKey(t *testing.T) {
    var received managementusecase.DiagnosticsQueryRequest
    srv := newDiagnosticsTestServer(t, diagnosticsHTTPStub{reqSink: &received, response: diagnosticsHTTPResponse()})

    rec := performDiagnosticsRequest(t, srv, "/manager/diagnostics/events?uid=u1&channel_key=channel%2F2%2FZzE", "admin")

    require.Equal(t, http.StatusOK, rec.Code)
    require.Equal(t, diagnostics.Query{UID: "u1", ChannelKey: "channel/2/ZzE", Limit: 100}, received.Query)
}
```

- [ ] **Step 3: Run usecase and manager tests to verify failure**

Run:

```bash
GOWORK=off go test ./internal/usecase/management ./internal/access/manager -run 'Test.*Diagnostics.*(Tracking|UID|ChannelKey)|TestManagerPermissions' -count=1
```

Expected: FAIL because tracking methods, DTOs, routes, permissions, and query parsing do not exist.

- [ ] **Step 4: Implement management tracking usecase**

Modify `internal/usecase/management/app.go`:

```go
type DiagnosticsTrackingOperator interface {
    AddNodeDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, input diagnostics.TrackingRuleInput) (diagnostics.TrackingRule, error)
    ListNodeDiagnosticsTrackingRules(ctx context.Context, nodeID uint64) ([]diagnostics.TrackingRule, error)
    DeleteNodeDiagnosticsTrackingRule(ctx context.Context, nodeID uint64, ruleID string) error
}
```

Add `DiagnosticsTracking DiagnosticsTrackingOperator` to `Options` and `diagnosticsTracking DiagnosticsTrackingOperator` to `App`.

Create `internal/usecase/management/diagnostics_tracking.go` with:

```go
type DiagnosticsTrackingStatus string

const (
    DiagnosticsTrackingStatusOK      DiagnosticsTrackingStatus = "ok"
    DiagnosticsTrackingStatusPartial DiagnosticsTrackingStatus = "partial"
    DiagnosticsTrackingStatusError   DiagnosticsTrackingStatus = "error"
)

type DiagnosticsTrackingCreateRequest struct {
    Target      string
    UID         string
    ChannelID   string
    ChannelType uint8
    TTLSeconds  int
    SampleRate  float64
}

type DiagnosticsTrackingRule struct {
    ID          string
    Target      string
    UID         string
    ChannelKey  string
    ChannelID   string
    ChannelType uint8
    SampleRate  float64
    CreatedAt   time.Time
    ExpiresAt   time.Time
}

type DiagnosticsTrackingNodeResult struct {
    NodeID uint64
    Status string
    Notes  []string
}

type DiagnosticsTrackingMutationResponse struct {
    Status DiagnosticsTrackingStatus
    Rule   DiagnosticsTrackingRule
    Nodes  []DiagnosticsTrackingNodeResult
    Notes  []string
}

type DiagnosticsTrackingListResponse struct {
    Status DiagnosticsTrackingStatus
    Rules  []DiagnosticsTrackingRule
    Nodes  []DiagnosticsTrackingNodeResult
    Notes  []string
}

type DiagnosticsTrackingDeleteResponse struct {
    Status DiagnosticsTrackingStatus
    RuleID string
    Nodes  []DiagnosticsTrackingNodeResult
    Notes  []string
}
```

Implement:

```go
func (a *App) CreateDiagnosticsTrackingRule(ctx context.Context, req DiagnosticsTrackingCreateRequest) (DiagnosticsTrackingMutationResponse, error)
func (a *App) ListDiagnosticsTrackingRules(ctx context.Context) (DiagnosticsTrackingListResponse, error)
func (a *App) DeleteDiagnosticsTrackingRule(ctx context.Context, ruleID string) (DiagnosticsTrackingDeleteResponse, error)
```

Implementation details:

- Normalize target, UID, channel ID.
- Default `SampleRate` to `1.0` only when request value is `0` and target/TTL are valid? Because explicit zero cannot be distinguished in Go DTO. If supporting explicit zero matters, make HTTP DTO carry `*float64` and pass a normalized value to usecase. Usecase can treat zero as valid; HTTP handler should default nil to 1.
- Validate TTL `> 0` and `<= diagnostics.DefaultMaxTrackingTTL/time.Second`.
- Convert channel key with `channelhandler.KeyFromChannelID(channel.ChannelID{ID: req.ChannelID, Type: req.ChannelType})`.
- Generate `rule_id` using `crypto/rand` hex or another stable unique helper; tests only require non-empty.
- Use `diagnosticsTargets(ctx, 0)` so single-node clusters use the same path as queries.
- Apply operations concurrently with bounded semaphore like `queryDiagnosticsTargets`.
- Node statuses: `ok`, `unavailable`, `skipped`.
- Aggregate status: all ok => `ok`; at least one ok and one fail/skipped => `partial`; zero ok => `error`.

- [ ] **Step 5: Implement manager HTTP handlers and routes**

Modify `internal/access/manager/server.go` `Management` interface:

```go
CreateDiagnosticsTrackingRule(ctx context.Context, req managementusecase.DiagnosticsTrackingCreateRequest) (managementusecase.DiagnosticsTrackingMutationResponse, error)
ListDiagnosticsTrackingRules(ctx context.Context) (managementusecase.DiagnosticsTrackingListResponse, error)
DeleteDiagnosticsTrackingRule(ctx context.Context, ruleID string) (managementusecase.DiagnosticsTrackingDeleteResponse, error)
```

Modify `routes.go`:

```go
diagnosticsRead := s.engine.Group("/manager")
if s.auth.enabled() {
    diagnosticsRead.Use(s.requirePermission("cluster.diagnostics", "r"))
}
diagnosticsRead.GET("/diagnostics/trace/:trace_id", s.handleDiagnosticsTrace)
diagnosticsRead.GET("/diagnostics/message", s.handleDiagnosticsMessage)
diagnosticsRead.GET("/diagnostics/events", s.handleDiagnosticsEvents)
diagnosticsRead.GET("/diagnostics/tracking-rules", s.handleDiagnosticsTrackingRules)

diagnosticsWrite := s.engine.Group("/manager")
if s.auth.enabled() {
    diagnosticsWrite.Use(s.requirePermission("cluster.diagnostics", "w"))
}
diagnosticsWrite.POST("/diagnostics/tracking-rules", s.handleCreateDiagnosticsTrackingRule)
diagnosticsWrite.DELETE("/diagnostics/tracking-rules/:rule_id", s.handleDeleteDiagnosticsTrackingRule)
```

Modify `diagnostics.go`:

- Add `UID string 'json:"uid,omitempty"'` to `DiagnosticsQueryDTO`.
- In `handleDiagnosticsEvents`, parse:

```go
if uid := strings.TrimSpace(c.Query("uid")); uid != "" {
    req.Query.UID = uid
}
if channelKey := strings.TrimSpace(c.Query("channel_key")); channelKey != "" {
    req.Query.ChannelKey = channelKey
}
```

Create `diagnostics_tracking.go` with HTTP DTOs:

```go
type DiagnosticsTrackingCreateRequest struct {
    Target      string   `json:"target"`
    UID         string   `json:"uid,omitempty"`
    ChannelID   string   `json:"channel_id,omitempty"`
    ChannelType uint8    `json:"channel_type,omitempty"`
    TTLSeconds  int      `json:"ttl_seconds"`
    SampleRate  *float64 `json:"sample_rate,omitempty"`
}
```

Default `SampleRate` to `1.0` when nil. Keep explicit `0.0` valid.

Map usecase DTOs to JSON with snake_case fields matching the spec.

- [ ] **Step 6: Update permissions catalog**

Modify `internal/access/manager/permissions.go`:

```go
{Resource: "cluster.diagnostics", Actions: []string{"r", "w"}, Description: "Read diagnostics and manage temporary message trace sampling rules."},
```

Update `permissions_test.go` expected JSON.

- [ ] **Step 7: Run backend manager/usecase tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/management ./internal/access/manager -run 'Test.*Diagnostics|TestManagerPermissions' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit management and manager APIs**

```bash
git add internal/usecase/management internal/access/manager
git commit -m "feat: add manager diagnostics tracking api"
```

---

## Task 6: Add Web Manager API Client For Tracking Rules

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API client tests**

Append to `web/src/lib/manager-api.test.ts`:

```ts
it("builds diagnostics events query with uid and channel key", async () => {
  mockFetchJson(emptyDiagnosticsResponse())

  await getDiagnosticsEvents({ uid: "u1", channelKey: "channel/2/ZzE", limit: 50 })

  expect(fetch).toHaveBeenCalledWith("/manager/diagnostics/events?limit=50&uid=u1&channel_key=channel%2F2%2FZzE", expect.any(Object))
})

it("lists diagnostics tracking rules", async () => {
  mockFetchJson({ status: "ok", rules: [], nodes: [], notes: [] })

  await listDiagnosticsTrackingRules()

  expect(fetch).toHaveBeenCalledWith("/manager/diagnostics/tracking-rules", expect.any(Object))
})

it("creates sender uid diagnostics tracking rule", async () => {
  mockFetchJson({ status: "ok", rule: { rule_id: "rule-1", target: "sender_uid", uid: "u1", sample_rate: 1 }, nodes: [], notes: [] })

  await createDiagnosticsTrackingRule({ target: "sender_uid", uid: "u1", ttlSeconds: 3600, sampleRate: 1 })

  expect(fetch).toHaveBeenCalledWith("/manager/diagnostics/tracking-rules", expect.objectContaining({
    method: "POST",
    body: JSON.stringify({ target: "sender_uid", uid: "u1", ttl_seconds: 3600, sample_rate: 1 }),
  }))
})

it("creates channel diagnostics tracking rule", async () => {
  mockFetchJson({ status: "ok", rule: { rule_id: "rule-2", target: "channel", channel_key: "channel/2/ZzE", sample_rate: 1 }, nodes: [], notes: [] })

  await createDiagnosticsTrackingRule({ target: "channel", channelId: "g1", channelType: 2, ttlSeconds: 600, sampleRate: 1 })

  expect(fetch).toHaveBeenCalledWith("/manager/diagnostics/tracking-rules", expect.objectContaining({
    method: "POST",
    body: JSON.stringify({ target: "channel", channel_id: "g1", channel_type: 2, ttl_seconds: 600, sample_rate: 1 }),
  }))
})

it("deletes diagnostics tracking rule", async () => {
  mockFetchJson({ status: "ok", rule_id: "rule-1", nodes: [], notes: [] })

  await deleteDiagnosticsTrackingRule("rule-1")

  expect(fetch).toHaveBeenCalledWith("/manager/diagnostics/tracking-rules/rule-1", expect.objectContaining({ method: "DELETE" }))
})
```

Adjust query parameter order to match existing helper behavior. Keep test expectations deterministic.

- [ ] **Step 2: Run web API tests and verify failure**

Run:

```bash
cd web && bun test src/lib/manager-api.test.ts
```

Expected: FAIL because tracking types/functions and event params do not exist.

- [ ] **Step 3: Add TypeScript types**

Modify `web/src/lib/manager-api.types.ts`:

```ts
export type DiagnosticsTrackingTarget = "sender_uid" | "channel"
export type DiagnosticsTrackingStatus = "ok" | "partial" | "error"

export type ManagerDiagnosticsTrackingRule = {
  rule_id: string
  target: DiagnosticsTrackingTarget
  uid?: string
  channel_key?: string
  channel_id?: string
  channel_type?: number
  sample_rate: number
  created_at?: string
  expires_at?: string
}

export type ManagerDiagnosticsTrackingNodeResult = {
  node_id: number
  status: "ok" | "unavailable" | "skipped"
  notes: string[]
}

export type ManagerDiagnosticsTrackingMutationResponse = {
  status: DiagnosticsTrackingStatus
  rule: ManagerDiagnosticsTrackingRule
  nodes: ManagerDiagnosticsTrackingNodeResult[]
  notes: string[]
}

export type ManagerDiagnosticsTrackingListResponse = {
  status: DiagnosticsTrackingStatus
  rules: ManagerDiagnosticsTrackingRule[]
  nodes: ManagerDiagnosticsTrackingNodeResult[]
  notes: string[]
}

export type ManagerDiagnosticsTrackingDeleteResponse = {
  status: DiagnosticsTrackingStatus
  rule_id: string
  nodes: ManagerDiagnosticsTrackingNodeResult[]
  notes: string[]
}

export type CreateDiagnosticsTrackingRuleInput =
  | { target: "sender_uid"; uid: string; ttlSeconds: number; sampleRate?: number }
  | { target: "channel"; channelId: string; channelType: number; ttlSeconds: number; sampleRate?: number }
```

Extend:

```ts
export type DiagnosticsEventsParams = DiagnosticsCommonParams & { stage?: string; result?: string; uid?: string; channelKey?: string }
```

- [ ] **Step 4: Add API client functions**

Modify `web/src/lib/manager-api.ts`:

```ts
export function listDiagnosticsTrackingRules() {
  return jsonManagerFetch<ManagerDiagnosticsTrackingListResponse>("/manager/diagnostics/tracking-rules")
}

export function createDiagnosticsTrackingRule(input: CreateDiagnosticsTrackingRuleInput) {
  const body = input.target === "sender_uid"
    ? { target: input.target, uid: input.uid, ttl_seconds: input.ttlSeconds, sample_rate: input.sampleRate ?? 1 }
    : { target: input.target, channel_id: input.channelId, channel_type: input.channelType, ttl_seconds: input.ttlSeconds, sample_rate: input.sampleRate ?? 1 }

  return jsonManagerFetch<ManagerDiagnosticsTrackingMutationResponse>("/manager/diagnostics/tracking-rules", {
    method: "POST",
    body: JSON.stringify(body),
  })
}

export function deleteDiagnosticsTrackingRule(ruleId: string) {
  return jsonManagerFetch<ManagerDiagnosticsTrackingDeleteResponse>(`/manager/diagnostics/tracking-rules/${encodeURIComponent(ruleId)}`, {
    method: "DELETE",
  })
}
```

Extend `getDiagnosticsEvents`:

```ts
if (params?.uid) search.set("uid", params.uid)
if (params?.channelKey) search.set("channel_key", params.channelKey)
```

- [ ] **Step 5: Run API client tests**

Run:

```bash
cd web && bun test src/lib/manager-api.test.ts
```

Expected: PASS.

- [ ] **Step 6: Commit web API client**

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat: add diagnostics tracking web client"
```

---

## Task 7: Add Tracking Rules UI To Diagnostics Page

**Files:**
- Modify: `web/src/pages/diagnostics/page.tsx`
- Modify: `web/src/pages/diagnostics/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing page tests**

Extend the manager API mock in `web/src/pages/diagnostics/page.test.tsx`:

```ts
const listDiagnosticsTrackingRulesMock = vi.fn()
const createDiagnosticsTrackingRuleMock = vi.fn()
const deleteDiagnosticsTrackingRuleMock = vi.fn()

vi.mock("@/lib/manager-api", async () => ({
  ...(await vi.importActual<typeof import("@/lib/manager-api")>("@/lib/manager-api")),
  getDiagnosticsTrace: (...args: unknown[]) => getDiagnosticsTraceMock(...args),
  getDiagnosticsMessage: (...args: unknown[]) => getDiagnosticsMessageMock(...args),
  getDiagnosticsEvents: (...args: unknown[]) => getDiagnosticsEventsMock(...args),
  listDiagnosticsTrackingRules: (...args: unknown[]) => listDiagnosticsTrackingRulesMock(...args),
  createDiagnosticsTrackingRule: (...args: unknown[]) => createDiagnosticsTrackingRuleMock(...args),
  deleteDiagnosticsTrackingRule: (...args: unknown[]) => deleteDiagnosticsTrackingRuleMock(...args),
}))
```

Add tests:

```ts
test("creates a sender uid tracking rule", async () => {
  const user = userEvent.setup()
  listDiagnosticsTrackingRulesMock.mockResolvedValue({ status: "ok", rules: [], nodes: [], notes: [] })
  createDiagnosticsTrackingRuleMock.mockResolvedValue({
    status: "ok",
    rule: { rule_id: "rule-1", target: "sender_uid", uid: "u1", sample_rate: 1, expires_at: "2026-05-14T10:00:00Z" },
    nodes: [{ node_id: 1, status: "ok", notes: [] }],
    notes: [],
  })

  renderDiagnosticsPage()

  await user.selectOptions(await screen.findByLabelText("Tracking target"), "sender_uid")
  await user.type(screen.getByLabelText("Sender UID"), "u1")
  await user.selectOptions(screen.getByLabelText("TTL"), "3600")
  await user.click(screen.getByRole("button", { name: "Start tracking" }))

  await waitFor(() => expect(createDiagnosticsTrackingRuleMock).toHaveBeenCalledWith({ target: "sender_uid", uid: "u1", ttlSeconds: 3600, sampleRate: 1 }))
  expect(await screen.findByText("u1")).toBeInTheDocument()
})

test("creates a channel tracking rule", async () => {
  const user = userEvent.setup()
  listDiagnosticsTrackingRulesMock.mockResolvedValue({ status: "ok", rules: [], nodes: [], notes: [] })
  createDiagnosticsTrackingRuleMock.mockResolvedValue({ status: "ok", rule: { rule_id: "rule-2", target: "channel", channel_key: "channel/2/ZzE", channel_id: "g1", channel_type: 2, sample_rate: 1 }, nodes: [], notes: [] })

  renderDiagnosticsPage()

  await user.selectOptions(await screen.findByLabelText("Tracking target"), "channel")
  await user.type(screen.getByLabelText("Channel ID"), "g1")
  await user.type(screen.getByLabelText("Channel Type"), "2")
  await user.click(screen.getByRole("button", { name: "Start tracking" }))

  await waitFor(() => expect(createDiagnosticsTrackingRuleMock).toHaveBeenCalledWith({ target: "channel", channelId: "g1", channelType: 2, ttlSeconds: 3600, sampleRate: 1 }))
})

test("queries recent events from a tracking rule", async () => {
  const user = userEvent.setup()
  listDiagnosticsTrackingRulesMock.mockResolvedValue({ status: "ok", rules: [{ rule_id: "rule-1", target: "sender_uid", uid: "u1", sample_rate: 1 }], nodes: [], notes: [] })
  getDiagnosticsEventsMock.mockResolvedValue(diagnosticsResponse())

  renderDiagnosticsPage()

  await user.click(await screen.findByRole("button", { name: "Query recent events" }))

  await waitFor(() => expect(getDiagnosticsEventsMock).toHaveBeenCalledWith({ uid: "u1", limit: 100 }))
})

test("deletes a tracking rule", async () => {
  const user = userEvent.setup()
  listDiagnosticsTrackingRulesMock.mockResolvedValueOnce({ status: "ok", rules: [{ rule_id: "rule-1", target: "sender_uid", uid: "u1", sample_rate: 1 }], nodes: [], notes: [] })
  deleteDiagnosticsTrackingRuleMock.mockResolvedValue({ status: "ok", rule_id: "rule-1", nodes: [], notes: [] })
  listDiagnosticsTrackingRulesMock.mockResolvedValueOnce({ status: "ok", rules: [], nodes: [], notes: [] })

  renderDiagnosticsPage()

  await user.click(await screen.findByRole("button", { name: "Stop tracking" }))

  await waitFor(() => expect(deleteDiagnosticsTrackingRuleMock).toHaveBeenCalledWith("rule-1"))
})
```

Use current test labels if i18n/default messages differ. Keep existing tests passing by defaulting `listDiagnosticsTrackingRulesMock` in `beforeEach`:

```ts
listDiagnosticsTrackingRulesMock.mockResolvedValue({ status: "ok", rules: [], nodes: [], notes: [] })
```

- [ ] **Step 2: Run page tests and verify failure**

Run:

```bash
cd web && bun test src/pages/diagnostics/page.test.tsx
```

Expected: FAIL because tracking UI and API calls are missing.

- [ ] **Step 3: Add diagnostics tracking UI state**

Modify `page.tsx` imports to include:

```ts
createDiagnosticsTrackingRule,
deleteDiagnosticsTrackingRule,
listDiagnosticsTrackingRules,
```

Add state types:

```ts
type TrackingForm = {
  target: "sender_uid" | "channel"
  uid: string
  channelId: string
  channelType: string
  ttlSeconds: string
  customTTLSeconds: string
  sampleRate: string
}
```

Default:

```ts
const defaultTrackingForm: TrackingForm = {
  target: "sender_uid",
  uid: "",
  channelId: "",
  channelType: "2",
  ttlSeconds: "3600",
  customTTLSeconds: "",
  sampleRate: "1",
}
```

Add `useEffect` to load tracking rules on page mount. Keep failures non-fatal: show an inline warning in the tracking card and leave existing query UI usable.

- [ ] **Step 4: Add validation and handlers**

Add helper:

```ts
function parseTrackingForm(form: TrackingForm) {
  const sampleRate = Number(form.sampleRate || "1")
  const ttl = form.ttlSeconds === "custom" ? positiveInteger(form.customTTLSeconds) : positiveInteger(form.ttlSeconds)
  // return { errors, input }
}
```

Validation rules:

- Sender target requires non-empty UID.
- Channel target requires non-empty channel ID and positive channel type.
- TTL must be positive.
- Sample rate must be number in `[0,1]`.

Add handlers:

```ts
async function refreshTrackingRules()
async function createTrackingRule()
async function stopTrackingRule(ruleId: string)
async function queryTrackingRule(rule: ManagerDiagnosticsTrackingRule)
```

`queryTrackingRule` calls:

```ts
if (rule.target === "sender_uid" && rule.uid) {
  response = await getDiagnosticsEvents({ uid: rule.uid, limit: positiveInteger(form.limit) ?? 100 })
}
if (rule.target === "channel" && rule.channel_key) {
  response = await getDiagnosticsEvents({ channelKey: rule.channel_key, limit: positiveInteger(form.limit) ?? 100 })
}
```

- [ ] **Step 5: Render Tracking Rules card**

Add a `SectionCard` before the existing query card:

- Title: `diagnostics.tracking.title`.
- Description warns that rules affect only future events and may evict older diagnostics sooner.
- Form controls:
  - `Tracking target` select.
  - `Sender UID` input or `Channel ID` + `Channel Type` inputs.
  - `TTL` select with `600`, `3600`, `21600`, `custom`.
  - `Sample rate` input.
  - `Start tracking` button.
- Rule list rows:
  - target summary (`Sender UID: u1`, `Channel: channel/2/ZzE`).
  - expires at or `-`.
  - sample rate.
  - buttons `Query recent events` and `Stop tracking`.
- Display aggregate `partial` or node notes after create/list/delete responses.

Preserve existing diagnostics query form and results layout.

- [ ] **Step 6: Add i18n strings**

Modify `web/src/i18n/messages/en.ts`:

```ts
"diagnostics.tracking.title": "Tracking Rules",
"diagnostics.tracking.description": "Temporarily retain future diagnostics events for one channel or one sender UID.",
"diagnostics.tracking.target": "Tracking target",
"diagnostics.tracking.senderUid": "Sender UID",
"diagnostics.tracking.channelId": "Channel ID",
"diagnostics.tracking.channelType": "Channel Type",
"diagnostics.tracking.ttl": "TTL",
"diagnostics.tracking.sampleRate": "Sample rate",
"diagnostics.tracking.start": "Start tracking",
"diagnostics.tracking.stop": "Stop tracking",
"diagnostics.tracking.query": "Query recent events",
"diagnostics.tracking.warning": "Tracking only affects future events and can evict older diagnostics sooner.",
"diagnostics.validation.senderUid": "Sender UID is required.",
"diagnostics.validation.channelType": "Channel type must be a positive integer.",
"diagnostics.validation.ttl": "TTL must be a positive integer.",
"diagnostics.validation.sampleRate": "Sample rate must be between 0 and 1."
```

Add matching Chinese strings to `web/src/i18n/messages/zh-CN.ts`.

- [ ] **Step 7: Run diagnostics page tests**

Run:

```bash
cd web && bun test src/pages/diagnostics/page.test.tsx
```

Expected: PASS.

- [ ] **Step 8: Commit web UI**

```bash
git add web/src/pages/diagnostics/page.tsx web/src/pages/diagnostics/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat: add diagnostics tracking UI"
```

---

## Task 8: Docs, Focused Verification, And Integration Sweep

**Files:**
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
- Possibly modify: `web/README.md` only if diagnostics page status text mentions read-only diagnostics.

- [ ] **Step 1: Update project knowledge**

Add one concise bullet to `docs/development/PROJECT_KNOWLEDGE.md` near existing diagnostics notes:

```md
- Manager diagnostics tracking rules are runtime-only, TTL-bound sampler overrides for future events; sender UID rules match messages sent by that UID and never expose `from_uid` in manager event DTOs.
```

Do not add lengthy operational docs here.

- [ ] **Step 2: Run focused backend verification**

Run:

```bash
GOWORK=off go test ./internal/observability/diagnostics ./pkg/observability/sendtrace ./internal/access/node ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 3: Run focused web verification**

Run:

```bash
cd web && bun test src/lib/manager-api.test.ts src/pages/diagnostics/page.test.tsx
```

Expected: PASS.

- [ ] **Step 4: Run broader related Go package tests**

Run:

```bash
GOWORK=off go test ./internal/access/gateway ./internal/usecase/message ./pkg/observability/sendtrace ./internal/observability/diagnostics -count=1
```

Expected: PASS.

- [ ] **Step 5: Run repository-wide unit tests if time allows**

Run:

```bash
go test ./...
```

Expected: PASS. If this is too slow or fails in unrelated packages, capture the exact failing package/test and run the focused suites above before reporting.

- [ ] **Step 6: Run full web tests if time allows**

Run:

```bash
cd web && bun test
```

Expected: PASS. If this is too slow or fails in unrelated tests, capture the exact failure and report focused test results.

- [ ] **Step 7: Inspect final diff**

Run:

```bash
git status --short
git diff --stat
git diff --check
```

Expected:

- Only intended files changed.
- `git diff --check` prints no whitespace errors.

- [ ] **Step 8: Commit docs and final verification notes**

```bash
git add docs/development/PROJECT_KNOWLEDGE.md web/README.md
git commit -m "docs: record diagnostics tracking behavior"
```

Skip `web/README.md` in the `git add` if it was not changed.

---

## Final Acceptance Criteria

- Operators can create a temporary sender UID tracking rule from `web/src/pages/diagnostics/page.tsx`.
- Operators can create a temporary channel tracking rule from the same page using `channel_id` and `channel_type`, not a hand-written channel key.
- Active rules are listed and can be stopped.
- `Query recent events` from a sender rule calls diagnostics events query with `uid` and renders the existing diagnostics result UI.
- `Query recent events` from a channel rule calls diagnostics events query with `channel_key` and renders the existing diagnostics result UI.
- Sender UID rules retain future sendtrace events where `FromUID` matches.
- Channel rules retain future events where `ChannelKey` matches.
- `from_uid` is not present in manager event DTOs or web event tables.
- Manager read/list routes require `cluster.diagnostics:r`; create/delete routes require `cluster.diagnostics:w`.
- Single-node clusters use the same manager fanout logic and do not introduce bypass-cluster business branches.
- Focused backend and web tests pass.
