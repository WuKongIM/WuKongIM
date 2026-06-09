# internalv2 Channel Write Reactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the internalv2 append-after-send chain with a channel-authority write reactor that owns append admission, channel flow control, committed continuation, and recipient/delivery scheduling.

**Architecture:** Gateway, HTTP, and node ingress call a channel write router. The router resolves channel authority; remote channels are forwarded by RPC and local-authority channels enter `internalv2/runtime/channelwrite`. Only authority nodes create `channelState`; append and post-commit effects run through bounded worker pools and return completion events to the owning reactor.

**Tech Stack:** Go, `internalv2/runtime/channelwrite`, `internalv2/contracts/channelwrite`, `internalv2/access/node`, `internalv2/infra/cluster`, `pkg/clusterv2`, `pkg/channelv2`, existing online/presence/conversation/delivery primitives, `go test`.

---

## Source Spec

- `docs/superpowers/specs/2026-06-09-internalv2-channel-write-reactor-design.md`

## Working Tree Rule

The repository may already contain unrelated `internalv2` edits. Each task must
stage only the files listed in that task. Do not revert or overwrite unrelated
changes.

## File Map

- Create `internalv2/contracts/channelwrite/FLOW.md`: contract ownership and data flow.
- Create `internalv2/contracts/channelwrite/types.go`: send DTOs, append DTOs, route target, recipient batch DTOs.
- Create `internalv2/contracts/channelwrite/errors.go`: typed channel write, routing, and flow-control errors.
- Modify `internalv2/usecase/message/types.go`: alias send DTOs from `contracts/channelwrite`.
- Modify `internalv2/usecase/message/errors.go`: alias channel write errors where access adapters already depend on message errors.
- Replace most of `internalv2/usecase/message/send.go` and `send_append.go`: make `message.App` a thin facade over a `channelwrite.Submitter` while keeping sync read behavior.
- Create `internalv2/runtime/channelwrite/FLOW.md`: authority-only reactor flow.
- Create `internalv2/runtime/channelwrite/options.go`: reactor group options, defaults, and validation.
- Create `internalv2/runtime/channelwrite/group.go`: lifecycle, reactor routing, public `SubmitLocal`.
- Create `internalv2/runtime/channelwrite/reactor.go`: single reactor event loop and mailbox.
- Create `internalv2/runtime/channelwrite/state.go`: authority `channelState` and watermarks.
- Create `internalv2/runtime/channelwrite/future.go`: item-aligned result futures.
- Create `internalv2/runtime/channelwrite/effects.go`: bounded worker pool and effect completion events.
- Create `internalv2/runtime/channelwrite/prepare.go`: validation, authorization, idempotency, message ID allocation, person/request-scoped normalization.
- Create `internalv2/runtime/channelwrite/append.go`: append batching, append retry, sendtrace, and `SENDACK` completion.
- Create `internalv2/runtime/channelwrite/commit.go`: committed queue, subscriber selection, recipient dispatch, cursor scheduling.
- Create `internalv2/runtime/channelwrite/recipient.go`: recipient-authority batch processor and conversation-before-delivery effect.
- Create `internalv2/runtime/channelwrite/delivery.go`: scoped online delivery using existing presence resolver and owner pusher ports.
- Create `internalv2/runtime/channelwrite/cursor.go`: durable post-commit cursor port and replay coordinator.
- Create focused tests under `internalv2/runtime/channelwrite`.
- Modify `pkg/clusterv2/net/ids.go`: add a channel write RPC service ID.
- Modify `pkg/clusterv2/node.go`: expose a narrow channel authority resolve facade for internalv2 routing.
- Modify `pkg/clusterv2/channels/service.go`: expose `ResolveAppendAuthority` using the same metadata ensure path as append admission.
- Create `internalv2/access/node/channel_write_codec.go`: deterministic binary codec for forwarded send batches.
- Create `internalv2/access/node/channel_write_rpc.go`: channel write RPC adapter and client.
- Modify `internalv2/access/node/FLOW.md`: document channel write RPC and remove sender/recipient authority RPC as primary send path.
- Create `internalv2/infra/cluster/channel_write.go`: clusterv2-backed channel authority resolver and remote forwarder.
- Modify `internalv2/infra/cluster/FLOW.md`: document channel-authority-first routing.
- Modify `internalv2/app/app.go`: replace sender/recipient worker fields with channel write router/runtime fields.
- Modify `internalv2/app/wiring.go`: wire channel write runtime, router, RPC, and message facade.
- Modify `internalv2/app/delivery.go`: keep local owner push and ack behavior; remove committed-message fanout adapter use.
- Delete `internalv2/app/recipient_committed.go`: obsolete recipient committed worker and adapters.
- Delete `internalv2/usecase/recipient/*`: obsolete recipient dispatcher/processor usecase.
- Delete obsolete fanout manager/planner files from `internalv2/runtime/delivery` after channelwrite owns fanout; keep ack tracker, owner push DTOs, route DTOs, and error helpers.
- Keep `internalv2/runtime/delivery/types.go` as the lower-level owner-push and ack DTO surface; channelwrite owns its own effect DTOs and app adapters translate between the two packages.
- Modify `internalv2/FLOW.md`, `internalv2/app/FLOW.md`, `internalv2/usecase/message/FLOW.md`, `internalv2/runtime/delivery/FLOW.md`: reflect the new authority write path.

---

### Task 1: Channel Write Contracts And Message Type Aliases

**Files:**
- Create: `internalv2/contracts/channelwrite/FLOW.md`
- Create: `internalv2/contracts/channelwrite/types.go`
- Create: `internalv2/contracts/channelwrite/errors.go`
- Modify: `internalv2/usecase/message/types.go`
- Modify: `internalv2/usecase/message/errors.go`
- Test: `internalv2/contracts/channelwrite/types_test.go`
- Test: `internalv2/usecase/message/import_boundary_test.go`

- [ ] **Step 1: Write failing contract tests**

Add tests that verify:

```go
func TestSendCommandCloneClonesSlices(t *testing.T) {
    cmd := channelwrite.SendCommand{
        Payload:           []byte("hello"),
        MessageScopedUIDs: []string{"u1", "u2"},
    }
    cloned := cmd.Clone()
    cmd.Payload[0] = 'x'
    cmd.MessageScopedUIDs[0] = "changed"
    if string(cloned.Payload) != "hello" {
        t.Fatalf("payload clone mutated: %q", cloned.Payload)
    }
    if got := cloned.MessageScopedUIDs[0]; got != "u1" {
        t.Fatalf("scoped uid clone mutated: %q", got)
    }
}

func TestMessagePackageAliasesChannelWriteTypes(t *testing.T) {
    var _ channelwrite.SendCommand = message.SendCommand{}
    if message.ReasonSuccess != channelwrite.ReasonSuccess {
        t.Fatalf("reason alias mismatch")
    }
}
```

- [ ] **Step 2: Run failing tests**

Run:

```bash
go test ./internalv2/contracts/channelwrite ./internalv2/usecase/message
```

Expected before implementation: compile failures because the contract package
does not exist and message aliases are not defined.

- [ ] **Step 3: Create contract package**

Implementation constraints:

- Move the public send-path DTO surface into `internalv2/contracts/channelwrite`.
- Include English comments on exported types and fields.
- Include `Clone` helpers for commands, messages, append requests, committed envelopes, and recipient batches that own slices.
- Keep reason constants and append error sentinels stable enough for access adapters to map `SENDACK`.
- Define `AuthorityTarget` for channel authority routing with these fields:

```go
type AuthorityTarget struct {
    ChannelID     ChannelID
    ChannelKey    string
    LeaderNodeID  uint64
    Epoch         uint64
    LeaderEpoch   uint64
    RouteRevision uint64
}
```

- Define `Submitter`:

```go
type Submitter interface {
    Send(context.Context, SendCommand) (SendResult, error)
    SendBatch([]SendBatchItem) []SendBatchItemResult
}
```

- In `internalv2/usecase/message`, turn the existing send DTOs and reason constants into type aliases and const aliases to the contract package.
- Keep message sync read DTOs in `internalv2/usecase/message`.
- Keep the import-boundary test rejecting concrete gateway, access, and clusterv2 imports.

- [ ] **Step 4: Verify Task 1**

Run:

```bash
go test ./internalv2/contracts/channelwrite ./internalv2/usecase/message
```

Expected after implementation: PASS.

- [ ] **Step 5: Commit Task 1**

```bash
git add internalv2/contracts/channelwrite internalv2/usecase/message/types.go internalv2/usecase/message/errors.go internalv2/usecase/message/import_boundary_test.go
git commit -m "feat(internalv2): add channel write contracts"
```

---

### Task 2: Authority-Only Reactor Group Skeleton

**Files:**
- Create: `internalv2/runtime/channelwrite/FLOW.md`
- Create: `internalv2/runtime/channelwrite/options.go`
- Create: `internalv2/runtime/channelwrite/group.go`
- Create: `internalv2/runtime/channelwrite/reactor.go`
- Create: `internalv2/runtime/channelwrite/state.go`
- Create: `internalv2/runtime/channelwrite/future.go`
- Test: `internalv2/runtime/channelwrite/group_test.go`
- Test: `internalv2/runtime/channelwrite/state_test.go`

- [ ] **Step 1: Write failing reactor ownership tests**

Add tests that assert:

```go
func TestSubmitLocalCreatesStateOnlyForLocalAuthority(t *testing.T) {
    group := newStartedTestGroup(t, channelwrite.Options{LocalNodeID: 1})
    target := channelwrite.AuthorityTarget{
        ChannelID:    channelwrite.ChannelID{ID: "room", Type: 2},
        ChannelKey:   "2:room",
        LeaderNodeID: 1,
        Epoch:        10,
        LeaderEpoch:  3,
    }
    future, err := group.SubmitLocal(context.Background(), target, []channelwrite.SendBatchItem{testSendItem("u1", "room")})
    if err != nil {
        t.Fatalf("SubmitLocal() error = %v", err)
    }
    if future == nil {
        t.Fatalf("future is nil")
    }
    if !group.HasStateForTest(target.ChannelID) {
        t.Fatalf("authority state was not created")
    }
}

func TestSubmitLocalRejectsRemoteAuthorityWithoutState(t *testing.T) {
    group := newStartedTestGroup(t, channelwrite.Options{LocalNodeID: 1})
    target := channelwrite.AuthorityTarget{ChannelID: channelwrite.ChannelID{ID: "room", Type: 2}, LeaderNodeID: 2}
    _, err := group.SubmitLocal(context.Background(), target, []channelwrite.SendBatchItem{testSendItem("u1", "room")})
    if !errors.Is(err, channelwrite.ErrNotChannelAuthority) {
        t.Fatalf("SubmitLocal() error = %v, want ErrNotChannelAuthority", err)
    }
    if group.StateCountForTest() != 0 {
        t.Fatalf("remote authority created state")
    }
}
```

- [ ] **Step 2: Run failing tests**

Run:

```bash
go test ./internalv2/runtime/channelwrite
```

Expected before implementation: compile failures for the new runtime package.

- [ ] **Step 3: Implement group and reactor lifecycle**

Implementation constraints:

- `Options` includes `LocalNodeID`, `ReactorCount`, `MailboxSize`, `PendingItemHighWatermark`, `AppendInflightLimit`, `EffectWorkerCount`, and `Clock`.
- Defaults are conservative: one reactor, bounded mailbox, one append inflight per channel.
- `Group.Start(ctx)` starts all reactors; `Stop(ctx)` stops admission and drains accepted events until context expiry.
- `SubmitLocal(ctx, target, items)` rejects non-local targets before hashing and never creates proxy state.
- Reactor hash uses `channelwrite.ChannelID` / channel key, not sender UID.
- `channelState` is created lazily only after local authority validation.
- Public test helpers live in `_test.go` files only.

- [ ] **Step 4: Verify Task 2**

Run:

```bash
go test ./internalv2/runtime/channelwrite
```

Expected after implementation: PASS for skeleton ownership tests.

- [ ] **Step 5: Commit Task 2**

```bash
git add internalv2/runtime/channelwrite
git commit -m "feat(internalv2): add authority channel write reactors"
```

---

### Task 3: Reactor Pre-Append Pipeline

**Files:**
- Modify: `internalv2/runtime/channelwrite/options.go`
- Create: `internalv2/runtime/channelwrite/prepare.go`
- Modify: `internalv2/runtime/channelwrite/state.go`
- Modify: `internalv2/runtime/channelwrite/effects.go`
- Test: `internalv2/runtime/channelwrite/prepare_test.go`
- Test: `internalv2/runtime/channelwrite/reactor_test.go`

- [ ] **Step 1: Write failing pre-append tests**

Cover these cases:

- Empty sender returns `ReasonAuthFail`.
- Empty channel or payload returns `ReasonInvalidRequest`.
- `NoPersist` returns `ReasonUnsupported`.
- Request-scoped sends require `SyncOnce` and derive the request subscriber channel.
- Person-channel normalization uses the sender UID and target UID.
- Authorizer denial returns the authorizer's reason.
- Existing idempotency result bypasses message ID allocation and append.
- Context-canceled items complete with the context error without blocking other items.
- Valid items enter the channel pending queue in input order.

Example test shape:

```go
func TestPrepareRequestScopedSendDerivesChannel(t *testing.T) {
    group := newPreparedGroup(t, preparePorts{ids: sequenceIDs(100)})
    item := channelwrite.SendBatchItem{Command: channelwrite.SendCommand{
        FromUID:           "u1",
        Payload:           []byte("payload"),
        SyncOnce:          true,
        RequestScoped:     true,
        MessageScopedUIDs: []string{"u2", "u3"},
    }}
    got := group.submitAndDrainPrepare(t, item)
    if got.Prepared[0].Command.ChannelID == "" || got.Prepared[0].Command.ChannelType == 0 {
        t.Fatalf("request-scoped channel was not derived: %#v", got.Prepared[0].Command)
    }
}
```

- [ ] **Step 2: Run failing tests**

Run:

```bash
go test ./internalv2/runtime/channelwrite
```

Expected before implementation: failing prepare assertions or compile failures
for pre-append ports.

- [ ] **Step 3: Implement prepare effects**

Implementation constraints:

- Define ports in `runtime/channelwrite`:

```go
type Authorizer interface {
    AuthorizeSend(context.Context, channelwrite.SendCommand) (channelwrite.Decision, error)
}
type MessageIDAllocator interface { Next() uint64 }
type IdempotencyStore interface {
    LookupSend(context.Context, channelwrite.IdempotencyQuery) (channelwrite.SendResult, bool, error)
}
type SenderFenceValidator interface {
    ValidateSender(context.Context, channelwrite.SendCommand) error
}
```

- Run sender fence validation, authorization, idempotency lookup, and ID allocation as worker effects.
- Mutate `channelState` only from the reactor completion event.
- Preserve one server timestamp per accepted send.
- Preserve item-aligned results and never let one rejected item cancel the whole batch.
- Keep request-scoped/person-channel behavior equivalent to the current `message.App` behavior.

- [ ] **Step 4: Verify Task 3**

Run:

```bash
go test ./internalv2/runtime/channelwrite
```

Expected after implementation: PASS.

- [ ] **Step 5: Commit Task 3**

```bash
git add internalv2/runtime/channelwrite
git commit -m "feat(internalv2): prepare sends inside channel reactors"
```

---

### Task 4: Append Effect, Flow Control, And SENDACK Futures

**Files:**
- Create: `internalv2/runtime/channelwrite/append.go`
- Modify: `internalv2/runtime/channelwrite/state.go`
- Modify: `internalv2/runtime/channelwrite/future.go`
- Modify: `internalv2/runtime/channelwrite/effects.go`
- Test: `internalv2/runtime/channelwrite/append_test.go`
- Test: `internalv2/runtime/channelwrite/flow_control_test.go`

- [ ] **Step 1: Write failing append and flow-control tests**

Cover these cases:

- One channel preserves append order.
- Two unrelated channels can append independently when assigned to different reactors.
- Append success completes item-aligned futures with `ReasonSuccess`, message ID, and sequence.
- Short append result returns `ErrAppendResultMissing` for the missing item.
- Batch-level `ErrRouteNotReady`, `ErrNotLeader`, and `ErrStaleRoute` retry until the item deadline.
- High watermark blocks admission before append and returns `ErrChannelBusy` or context deadline when capacity never opens.
- Append success enqueues committed work without waiting for recipient effects.

Example high-watermark assertion:

```go
func TestHighWatermarkRejectsBeforeAppend(t *testing.T) {
    appender := &recordingAppender{}
    group := newStartedTestGroup(t, channelwrite.Options{
        LocalNodeID:              1,
        PendingItemHighWatermark: 1,
        Appender:                 appender,
    })
    target := localTarget("room")
    first := mustSubmitNoWait(t, group, target, testSendItem("u1", "room"))
    ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
    defer cancel()
    second, err := group.SubmitLocal(ctx, target, []channelwrite.SendBatchItem{testSendItem("u2", "room")})
    if err == nil && second != nil {
        _, err = second.Wait(ctx)
    }
    if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, channelwrite.ErrChannelBusy) {
        t.Fatalf("overflow error = %v", err)
    }
    if appender.Calls() > 1 {
        t.Fatalf("overflow reached append path")
    }
    first.ReleaseForTest()
}
```

- [ ] **Step 2: Run failing tests**

Run:

```bash
go test ./internalv2/runtime/channelwrite
```

- [ ] **Step 3: Implement append scheduling**

Implementation constraints:

- Define `Appender.AppendBatch(ctx, channelwrite.AppendBatchRequest)`.
- Build one append batch per channel from prepared pending items.
- Keep one append inflight per channel initially unless `AppendInflightLimit` is raised.
- Use worker pool for the blocking append call.
- Return append completions to the owning reactor.
- Clone payloads at the append boundary.
- Record append sendtrace and observer events from append completion.
- Complete `SENDACK` futures immediately after append result processing.
- Put committed events into the same authority state after successful append.

- [ ] **Step 4: Verify Task 4**

Run:

```bash
go test ./internalv2/runtime/channelwrite
```

- [ ] **Step 5: Commit Task 4**

```bash
git add internalv2/runtime/channelwrite
git commit -m "feat(internalv2): append through channel write reactors"
```

---

### Task 5: Channel Authority Resolver And Router

**Files:**
- Modify: `pkg/clusterv2/channels/service.go`
- Modify: `pkg/clusterv2/node.go`
- Test: `pkg/clusterv2/channels/channels_test.go`
- Test: `pkg/clusterv2/node_channel_test.go`
- Create: `internalv2/infra/cluster/channel_write.go`
- Test: `internalv2/infra/cluster/channel_write_test.go`
- Create: `internalv2/runtime/channelwrite/router.go`
- Test: `internalv2/runtime/channelwrite/router_test.go`

- [ ] **Step 1: Write failing authority resolver tests**

Add tests asserting:

- `channels.Service.ResolveAppendAuthority(ctx, id)` uses the same ensure path as append.
- Missing metadata can be created by the resolver when append would create it.
- Returned target carries `ChannelID`, `ChannelKey`, `LeaderNodeID`, `Epoch`, and `LeaderEpoch`.
- Router local path calls `SubmitLocal`.
- Router remote path calls `RemoteForwarder.ForwardSendBatch`.
- Router retries stale/not-leader/route-not-ready errors within deadline.
- Router never creates channel state for remote targets.

- [ ] **Step 2: Run failing tests**

Run:

```bash
go test ./pkg/clusterv2/channels ./pkg/clusterv2 ./internalv2/infra/cluster ./internalv2/runtime/channelwrite
```

- [ ] **Step 3: Implement resolver and router**

Implementation constraints:

- Add a narrow clusterv2 facade:

```go
func (n *Node) ResolveChannelAppendAuthority(ctx context.Context, id channelv2.ChannelID) (channelv2.Meta, error)
```

- The facade must call the hosted ChannelV2 service, not duplicate metadata policy in `Node`.
- `internalv2/infra/cluster.ChannelWriteClient` maps `channelv2.Meta` to `channelwrite.AuthorityTarget`.
- Route errors map to channelwrite sentinels:
  - `channelv2.ErrNotLeader` -> `channelwrite.ErrNotChannelAuthority`
  - `channelv2.ErrStaleMeta` -> `channelwrite.ErrStaleRoute`
  - `channelv2.ErrNotReady` and clusterv2 readiness errors -> `channelwrite.ErrRouteNotReady`
- Router per-target outbound limits are keyed by `LeaderNodeID`, not channel.
- Remote forwarding returns item-aligned results without interpreting success payloads.

- [ ] **Step 4: Verify Task 5**

Run:

```bash
go test ./pkg/clusterv2/channels ./pkg/clusterv2 ./internalv2/infra/cluster ./internalv2/runtime/channelwrite
```

- [ ] **Step 5: Commit Task 5**

```bash
git add pkg/clusterv2/channels/service.go pkg/clusterv2/node.go pkg/clusterv2/channels/channels_test.go pkg/clusterv2/node_channel_test.go internalv2/infra/cluster/channel_write.go internalv2/infra/cluster/channel_write_test.go internalv2/runtime/channelwrite/router.go internalv2/runtime/channelwrite/router_test.go
git commit -m "feat(internalv2): route sends to channel authority"
```

---

### Task 6: Channel Write Node RPC

**Files:**
- Modify: `pkg/clusterv2/net/ids.go`
- Test: `pkg/clusterv2/net/ids_test.go`
- Create: `internalv2/access/node/channel_write_codec.go`
- Create: `internalv2/access/node/channel_write_rpc.go`
- Test: `internalv2/access/node/channel_write_codec_test.go`
- Test: `internalv2/access/node/channel_write_rpc_test.go`
- Modify: `internalv2/access/node/FLOW.md`

- [ ] **Step 1: Write failing RPC tests**

Add codec tests for:

- Empty request rejects.
- One item round trips all `SendCommand` fields.
- Payload and scoped UID slices are cloned.
- Oversized item collections are rejected.
- Result errors preserve stable statuses: `ok`, `not_leader`, `stale_route`, `route_not_ready`, `channel_busy`, `context_canceled`, `context_deadline_exceeded`, `rejected`.

Add RPC tests:

```go
func TestChannelWriteRPCSubmitsToLocalAuthority(t *testing.T) {
    submitter := &recordingChannelWriteSubmitter{}
    adapter := node.New(node.Options{ChannelWrite: submitter})
    req := channelWriteRequest{Target: localTarget("room"), Items: []channelwrite.SendBatchItem{testSendItem("u1", "room")}}
    payload, _ := encodeChannelWriteRequest(req)
    body, err := adapter.HandleChannelWriteRPC(context.Background(), payload)
    if err != nil {
        t.Fatalf("HandleChannelWriteRPC() error = %v", err)
    }
    resp, _ := decodeChannelWriteResponse(body)
    if len(resp.Results) != 1 || resp.Results[0].Result.Reason != channelwrite.ReasonSuccess {
        t.Fatalf("response = %#v", resp)
    }
    if submitter.Calls() != 1 {
        t.Fatalf("submitter calls = %d", submitter.Calls())
    }
}
```

- [ ] **Step 2: Run failing tests**

Run:

```bash
go test ./pkg/clusterv2/net ./internalv2/access/node
```

- [ ] **Step 3: Implement RPC codec and adapter**

Implementation constraints:

- Add `RPCChannelWrite` to `pkg/clusterv2/net/ids.go` and test name mapping.
- Magic headers:
  - request: `W K V W 1`
  - response: `W K V w 1`
- The RPC request carries one exact `AuthorityTarget` and a batch of send items.
- The server calls `ChannelWrite.SubmitForAuthority(ctx, target, items)`; it must not resolve routes.
- Stable statuses map back to channelwrite sentinel errors.
- Decode rejects trailing bytes and oversized collections.

- [ ] **Step 4: Verify Task 6**

Run:

```bash
go test ./pkg/clusterv2/net ./internalv2/access/node
```

- [ ] **Step 5: Commit Task 6**

```bash
git add pkg/clusterv2/net/ids.go pkg/clusterv2/net/ids_test.go internalv2/access/node/channel_write_codec.go internalv2/access/node/channel_write_rpc.go internalv2/access/node/channel_write_codec_test.go internalv2/access/node/channel_write_rpc_test.go internalv2/access/node/FLOW.md
git commit -m "feat(internalv2): add channel write rpc"
```

---

### Task 7: Recipient, Conversation, And Delivery Effects

**Files:**
- Create: `internalv2/runtime/channelwrite/commit.go`
- Create: `internalv2/runtime/channelwrite/recipient.go`
- Create: `internalv2/runtime/channelwrite/delivery.go`
- Modify: `internalv2/runtime/channelwrite/options.go`
- Test: `internalv2/runtime/channelwrite/commit_test.go`
- Test: `internalv2/runtime/channelwrite/recipient_test.go`
- Test: `internalv2/runtime/channelwrite/delivery_test.go`

- [ ] **Step 1: Write failing post-commit tests**

Cover these cases:

- Append success enqueues committed events and `SENDACK` is already complete.
- Scoped UIDs bypass subscriber scan.
- Person channel derives exactly the two canonical participants.
- Group channel pages subscribers with bounded page size and does not load all subscribers before dispatch.
- Recipient batches are grouped by recipient UID authority target.
- Recipient processor updates conversation before resolving/pushing delivery.
- Same-session sender echo is skipped only when sender owner node and session match.
- Retryable owner push routes are retried with bounded backoff.

Example conversation-before-delivery assertion:

```go
func TestRecipientProcessorUpdatesConversationBeforeDelivery(t *testing.T) {
    order := &recordingOrder{}
    processor := channelwrite.NewRecipientProcessor(channelwrite.RecipientProcessorOptions{
        Conversation: recordingConversation{order: order},
        Presence:     recordingPresence{order: order},
        Pusher:       recordingPusher{order: order},
    })
    err := processor.Process(context.Background(), channelwrite.RecipientBatch{
        Event:      committedEvent("room", 1),
        Recipients: []channelwrite.Recipient{{UID: "u1"}},
    })
    if err != nil {
        t.Fatalf("Process() error = %v", err)
    }
    if got := order.String(); got != "conversation,presence,push" {
        t.Fatalf("order = %s", got)
    }
}
```

- [ ] **Step 2: Run failing tests**

Run:

```bash
go test ./internalv2/runtime/channelwrite
```

- [ ] **Step 3: Implement post-commit effects**

Implementation constraints:

- Define ports:

```go
type SubscriberSource interface {
    NextSubscriberPage(context.Context, channelwrite.SubscriberPageRequest) (channelwrite.SubscriberPage, error)
}
type RecipientAuthorityRouter interface {
    DispatchRecipientBatch(context.Context, channelwrite.RecipientBatch) error
}
type ConversationProjector interface {
    AdmitRecipientPatches(context.Context, []channelwrite.ConversationPatch) error
}
type PresenceResolver interface {
    EndpointsByUIDs(context.Context, []string) ([]channelwrite.Route, error)
}
type OwnerPusher interface {
    Push(context.Context, channelwrite.PushCommand) (channelwrite.PushResult, error)
}
```

- Keep recipient selection in authority `channelState`.
- Keep conversation-before-delivery inside the recipient authority processor.
- Owner-local concrete session writes remain outside the channel reactor state.
- Worker effects return compact completion events to the reactor.
- Do not use `internalv2/usecase/recipient`.
- Do not call `runtime/delivery.Manager.SubmitCommitted`.

- [ ] **Step 4: Verify Task 7**

Run:

```bash
go test ./internalv2/runtime/channelwrite
```

- [ ] **Step 5: Commit Task 7**

```bash
git add internalv2/runtime/channelwrite
git commit -m "feat(internalv2): run post-commit effects from channel reactors"
```

---

### Task 8: Durable Post-Commit Cursor And Replay

**Files:**
- Create: `internalv2/runtime/channelwrite/cursor.go`
- Modify: `internalv2/runtime/channelwrite/commit.go`
- Modify: `internalv2/runtime/channelwrite/state.go`
- Create: `internalv2/infra/cluster/channel_write_cursor.go`
- Test: `internalv2/runtime/channelwrite/cursor_test.go`
- Test: `internalv2/infra/cluster/channel_write_cursor_test.go`

- [ ] **Step 1: Write failing cursor tests**

Cover these cases:

- New authority state loads cursor before accepting post-commit replay.
- Replay starts at `lastCompletedSeq + 1`.
- Cursor is checkpointed after recipient authority dispatch is accepted.
- Restart after append success but before cursor checkpoint replays the message.
- Duplicate replay is tolerated by idempotent conversation projection.
- Cursor store errors keep the committed event retryable and do not alter `SENDACK`.

- [ ] **Step 2: Run failing tests**

Run:

```bash
go test ./internalv2/runtime/channelwrite ./internalv2/infra/cluster
```

- [ ] **Step 3: Implement cursor port and adapter**

Implementation constraints:

- Runtime port:

```go
type CursorStore interface {
    LoadPostCommitCursor(context.Context, channelwrite.ChannelID) (uint64, error)
    StorePostCommitCursor(context.Context, channelwrite.ChannelID, uint64) error
}
type CommittedReader interface {
    ReadCommittedFrom(context.Context, channelwrite.ChannelID, uint64, int) ([]channelwrite.CommittedMessage, error)
}
```

- The runtime must work with a nil cursor store by replaying only in-memory committed events; app wiring must provide the durable adapter when clusterv2 is available.
- The clusterv2 adapter stores cursor rows in UID/channel metadata through a narrow app-local key, not in the channel log.
- Replay page size is bounded and configured in `Options`.
- Cursor checkpoints are monotonic.

- [ ] **Step 4: Verify Task 8**

Run:

```bash
go test ./internalv2/runtime/channelwrite ./internalv2/infra/cluster
```

- [ ] **Step 5: Commit Task 8**

```bash
git add internalv2/runtime/channelwrite/cursor.go internalv2/runtime/channelwrite/commit.go internalv2/runtime/channelwrite/state.go internalv2/infra/cluster/channel_write_cursor.go internalv2/runtime/channelwrite/cursor_test.go internalv2/infra/cluster/channel_write_cursor_test.go
git commit -m "feat(internalv2): persist channel post-commit cursors"
```

---

### Task 9: Message Facade And App Wiring

**Files:**
- Modify: `internalv2/usecase/message/app.go`
- Modify: `internalv2/usecase/message/send.go`
- Modify: `internalv2/usecase/message/send_append.go`
- Modify: `internalv2/usecase/message/FLOW.md`
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/delivery.go`
- Test: `internalv2/usecase/message/send_test.go`
- Test: `internalv2/app/app_test.go`

- [ ] **Step 1: Write failing facade and wiring tests**

Add tests asserting:

- `message.App.SendBatch` delegates to a configured `channelwrite.Submitter`.
- `message.App.Send` returns the first delegated result.
- Gateway/API message facades use channel write router, not sender authority router.
- App lifecycle starts/stops the channel write group.
- App registers channel write RPC.
- `recipientCommittedWorker` is not wired.
- `deliveryRuntimeAdapter.SubmitCommitted` is no longer used for send fanout.

- [ ] **Step 2: Run failing tests**

Run:

```bash
go test ./internalv2/usecase/message ./internalv2/app
```

- [ ] **Step 3: Wire new path**

Implementation constraints:

- `message.App` keeps channel message sync reads and becomes a thin send facade:

```go
type Options struct {
    Submitter ChannelWriteSubmitter
    Reader    ChannelMessageReader
}
```

- `wireMessages` wires `message.App` to the channel write router.
- `wireChannelWrite` creates:
  - cluster channel authority resolver
  - channel write RPC client
  - channel write router
  - channel write reactor group
  - recipient processor ports
  - local owner pusher and ack tracker ports
- Remove `wireSenderAuthority` from construction.
- Remove `wireRecipientAuthority` as a committed-event worker path; keep conversation authority and presence authority wiring.
- Preserve gateway `SENDACK` mapping by returning `message.SendBatchItemResult` aliases.

- [ ] **Step 4: Verify Task 9**

Run:

```bash
go test ./internalv2/usecase/message ./internalv2/app
```

- [ ] **Step 5: Commit Task 9**

```bash
git add internalv2/usecase/message/app.go internalv2/usecase/message/send.go internalv2/usecase/message/send_append.go internalv2/usecase/message/FLOW.md internalv2/app/app.go internalv2/app/wiring.go internalv2/app/delivery.go internalv2/usecase/message/send_test.go internalv2/app/app_test.go
git commit -m "feat(internalv2): wire sends through channel write reactors"
```

---

### Task 10: Delete Obsolete Sender/Recipient/Fanout Layers

**Files:**
- Delete: `internalv2/app/authority_message.go`
- Delete: `internalv2/app/recipient_committed.go`
- Delete: `internalv2/usecase/message/sender_authority.go`
- Delete: `internalv2/usecase/message/sender_authority_test.go`
- Delete: `internalv2/usecase/recipient/*`
- Delete or reduce: `internalv2/runtime/delivery/manager*.go`
- Delete or reduce: `internalv2/runtime/delivery/planner.go`
- Delete or reduce: `internalv2/runtime/delivery/fanout_worker.go`
- Delete or reduce: `internalv2/runtime/delivery/retry_scheduler.go`
- Keep: `internalv2/runtime/delivery/ack*.go`
- Keep: `internalv2/runtime/delivery/types.go` with owner-push DTOs, route DTOs, recvack DTOs, and session-close DTOs
- Modify: affected tests under `internalv2/app`, `internalv2/usecase/message`, `internalv2/runtime/delivery`

- [ ] **Step 1: Write deletion guard checks**

Before deleting, add or update tests/import checks that fail if these names
remain referenced:

```bash
rg -n "SenderAuthorityRouter|recipientCommittedWorker|usecase/recipient|SubmitCommitted\\(context.Context, messageevents.MessageCommitted\\)|FanoutWorker|RetryScheduler" internalv2
```

Expected after final deletion: no matches except historical design/spec/plan docs
and explicit negative assertions in tests.

- [ ] **Step 2: Delete obsolete files and update call sites**

Implementation constraints:

- Remove the old sender-authority-first path completely.
- Remove recipient committed worker tests instead of rewriting them around the new runtime.
- Remove delivery fanout manager/planner tests that test subscriber rescanning after commit.
- Keep recvack/session-close behavior by using the ack tracker directly or a reduced owner-delivery facade.
- Keep English comments on remaining exported delivery types.

- [ ] **Step 3: Run focused compile tests**

Run:

```bash
go test ./internalv2/usecase/message ./internalv2/runtime/delivery ./internalv2/app
```

- [ ] **Step 4: Run deletion scan**

Run:

```bash
rg -n "SenderAuthorityRouter|recipientCommittedWorker|usecase/recipient|FanoutWorker|RetryScheduler" internalv2
```

Expected: no matches in Go files.

- [ ] **Step 5: Commit Task 10**

```bash
git add -A internalv2/app internalv2/usecase/message internalv2/usecase/recipient internalv2/runtime/delivery
git commit -m "refactor(internalv2): remove obsolete send fanout layers"
```

---

### Task 11: FLOW Docs, Observability, And Integration Verification

**Files:**
- Modify: `internalv2/FLOW.md`
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/access/gateway/FLOW.md`
- Modify: `internalv2/access/node/FLOW.md`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Modify: `internalv2/runtime/delivery/FLOW.md`
- Create/modify: focused integration tests under `internalv2/app` and `test/e2e/message` only if an existing harness already covers the path.

- [ ] **Step 1: Update FLOW docs**

Document this final flow:

```text
Gateway/API/Node SEND
  -> message facade
  -> ChannelWriteRouter
  -> local ChannelWriteReactor or ForwardSendBatch RPC
  -> authority channelState
  -> pre-append effects
  -> append effect
  -> SENDACK future
  -> committed queue
  -> recipient/conversation/delivery effects
```

State these invariants exactly:

- Only channel authority nodes own `channelState`.
- Non-authority nodes do not create proxy `channelState`.
- `SENDACK` success means durable append success.
- Recipient/conversation/delivery failure does not roll back `SENDACK`.
- Conversation projection precedes delivery for each recipient UID group.

- [ ] **Step 2: Add integration coverage**

Add tests for:

- Single-node cluster gateway `SEND -> SENDACK -> RECV`.
- Remote channel authority forward returns item-aligned `SENDACK`.
- Hot channel high watermark does not block unrelated channel.
- Restart/recreate reactor after append success replays uncheckpointed committed messages.

If existing e2e harness setup is too slow for unit test runs, put slow cases
behind the existing `integration` build tag and keep focused app-level tests in
normal unit tests.

- [ ] **Step 3: Run targeted tests**

Run:

```bash
go test ./internalv2/contracts/... ./internalv2/runtime/channelwrite ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/usecase/message ./internalv2/app
```

- [ ] **Step 4: Run broad internalv2 tests**

Run:

```bash
go test ./internalv2/...
```

- [ ] **Step 5: Commit Task 11**

```bash
git add internalv2/FLOW.md internalv2/app/FLOW.md internalv2/access/gateway/FLOW.md internalv2/access/node/FLOW.md internalv2/infra/cluster/FLOW.md internalv2/runtime/delivery/FLOW.md internalv2/app internalv2/runtime/channelwrite test/e2e/message
git commit -m "docs(internalv2): document channel write reactor flow"
```

---

## Final Verification

Run:

```bash
go test ./internalv2/... ./pkg/clusterv2/... ./pkg/channelv2/...
```

If the implementation touched gateway packet behavior or black-box message
delivery, also run the existing targeted e2e message suite rather than the full
integration suite.

Before final handoff, run:

```bash
rg -n "recipientCommittedWorker|SenderAuthorityRouter|internalv2/usecase/recipient|FanoutWorker|RetryScheduler" internalv2
git status --short
```

Expected:

- No obsolete send fanout symbols in Go files.
- Only intentional files changed.
- All focused tests pass.
