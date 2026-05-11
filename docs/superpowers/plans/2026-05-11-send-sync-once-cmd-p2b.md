# Send SyncOnce Cmd Channel P2b Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore the durable `SyncOnce` / cmd-channel boundary so send-time validation stays on the original channel, durable appends move to the derived cmd channel, delivery still resolves subscribers from the original channel, and clients keep seeing the original channel view.

**Architecture:** Keep the current layering intact: `internal/access/*` only adapts request/packet fields, `internal/usecase/message` owns send-time channel normalization and durable append selection, `internal/usecase/delivery` owns subscriber-source resolution, `internal/app` owns realtime presentation, and `internal/usecase/conversation` owns the projection gate. The derived cmd channel is a storage and delivery identity only; it must never become the source of truth for permission checks or client-facing channel views.

**Tech Stack:** Go 1.23, `context`, `errors`, existing `frame` / `channel` / `slot` packages, `testify`, and the current internal app/usecase/runtime layers.

---

## Scope Check

This plan stays focused on one sequential feature slice:

1. Add shared cmd-channel helpers first so every layer uses the same suffix rules.
2. Move send-time cmd derivation into `internal/usecase/message` so access adapters stop doing irreversible normalization.
3. Update delivery routing and conversation projection so durable cmd messages still fan out correctly but do not pollute ordinary conversation state.
4. Update docs after the code lands.

Do not add request-scoped subscribers, temp channels, `systemcmdonline`, direct non-durable `NoPersist + SyncOnce` realtime delivery, `/message/sendbatch`, plugin/webhook/AI hooks, or any new cmd suffix configuration in this plan.

## File Structure

### Shared cmd-channel identity

- Create: `internal/runtime/channelid/command.go`
  - Responsibility: shared cmd-channel suffix helpers and channel-ID conversions.
- Create: `internal/runtime/channelid/command_test.go`
  - Responsibility: suffix helper coverage and regression tests.
- Modify: `internal/runtime/channelid/person.go`
  - Responsibility: keep person-channel helpers unchanged unless a tiny shared helper fit is needed.

### Send usecase

- Modify: `internal/usecase/message/send.go`
  - Responsibility: strip incoming cmd suffixes, normalize person channels after stripping, check permissions on the original source channel, and derive the durable append channel only when needed.
- Modify: `internal/usecase/message/send_test.go`
  - Responsibility: source-channel permission ordering, already-derived input handling, `SyncOnce` append routing, and `NoPersist + SyncOnce` coverage.

### Access adapters

- Modify: `internal/access/api/message_send.go`
  - Responsibility: map `header.sync_once` and top-level `sync_once` into `frame.Framer.SyncOnce` and stop pre-normalizing person channels.
- Modify: `internal/access/api/server_test.go`
  - Responsibility: request mapping tests for `sync_once`, raw person channel pass-through, and error mapping through the usecase.
- Modify: `internal/access/api/integration_test.go`
  - Responsibility: end-to-end HTTP coverage for `header.sync_once` and `no_persist`.
- Modify: `internal/access/gateway/mapper.go`
  - Responsibility: pass raw send channels through to the usecase and keep `SyncOnce` intact.
- Modify: `internal/access/gateway/handler_test.go`
  - Responsibility: gateway send mapping tests for raw person channels, precomposed person channels, and `SyncOnce`.

### Delivery subscriber and routing

- Modify: `internal/usecase/delivery/subscriber.go`
  - Responsibility: use the shared cmd helper, strip derived suffixes once, and prefer authoritative metadata lookup for source mutation fencing where possible.
- Modify: `internal/usecase/delivery/subscriber_test.go`
  - Responsibility: cmd-group / cmd-person subscriber resolution and source mutation version coverage.
- Modify: `internal/app/deliveryrouting.go`
  - Responsibility: strip cmd suffixes before building client-facing `RecvPacket` views and before grouping remote person routes.
- Modify: `internal/app/deliveryrouting_test.go`
  - Responsibility: realtime packet view tests and distributed push grouping tests for derived cmd messages.

### Conversation projection

- Modify: `internal/usecase/conversation/projector.go`
  - Responsibility: reject durable cmd messages and `SyncOnce` messages at the projector boundary.
- Modify: `internal/usecase/conversation/projector_test.go`
  - Responsibility: projector gate tests for cmd-channel messages and `SyncOnce`.

### Documentation

- Modify: `docs/raw/send-path-business-logic-diff.md`
  - Responsibility: mark the P2b cmd-channel boundary as implemented and keep the remaining gaps visible.
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
  - Responsibility: record the durable cmd-channel and `NoPersist + SyncOnce` rules concisely.

## Task 1: Add shared cmd-channel helpers

**Files:**
- Create: `internal/runtime/channelid/command.go`
- Create: `internal/runtime/channelid/command_test.go`

- [ ] **Step 1: Write the failing tests**

Add tests that assert:

- `IsCommandChannel("g1____cmd") == true`
- `IsCommandChannel("g1") == false`
- `ToCommandChannel("g1") == "g1____cmd"`
- `ToCommandChannel("g1____cmd") == "g1____cmd"`
- `FromCommandChannel("g1____cmd") == ("g1", true)`
- `FromCommandChannel("g1") == ("g1", false)`
- `FromCommandChannel("u2@u1____cmd") == ("u2@u1", true)`

- [ ] **Step 2: Run the focused tests and confirm they fail**

Run: `GOWORK=off go test ./internal/runtime/channelid -run 'TestIsCommandChannel|TestToCommandChannel|TestFromCommandChannel' -v`

Expected: FAIL because the new helper file does not exist yet.

- [ ] **Step 3: Implement the shared helper**

Add the legacy suffix constant and the three helper functions in `internal/runtime/channelid/command.go`. Keep the helper pure and avoid any access/usecase dependencies.

- [ ] **Step 4: Re-run the focused tests**

Run: `GOWORK=off go test ./internal/runtime/channelid -run 'TestIsCommandChannel|TestToCommandChannel|TestFromCommandChannel' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/channelid/command.go internal/runtime/channelid/command_test.go
git commit -m "feat: add shared cmd channel helpers"
```

## Task 2: Restore durable SyncOnce cmd send semantics in the usecase

**Files:**
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`

- [ ] **Step 1: Write the failing tests**

Add tests that assert:

- A plain durable person send still normalizes to the canonical person channel and appends exactly once.
- `SyncOnce=true` on a normal source channel appends to `source____cmd` after permission checks pass.
- Already-derived group input like `g1____cmd` is checked against `g1` and appends to `g1____cmd` without double suffixes.
- Already-derived person input like `u1@u2____cmd` is stripped before person normalization and appends to the canonical person channel with the cmd suffix restored once.
- `NoPersist=true` plus `SyncOnce=true` still returns success with zero IDs / seq and never reaches durable append.

- [ ] **Step 2: Run the focused tests and confirm they fail**

Run: `GOWORK=off go test ./internal/usecase/message -run 'TestSendSyncOnce|TestSendAlreadyDerived|TestSendNoPersist.*SyncOnce' -v`

Expected: FAIL because the send flow still treats the input channel as the only identity.

- [ ] **Step 3: Implement the send-path rewrite**

Update `internal/usecase/message/send.go` so the flow is:

1. reject empty sender;
2. reject unsupported channel type;
3. strip an incoming cmd suffix and remember whether the input was already derived;
4. normalize the stripped source for person channels;
5. run permission checks on the normalized source channel;
6. return the permission reason immediately on denial;
7. honor `NoPersist` before any durable cmd derivation;
8. derive the append channel with `ToCommandChannel(source)` when `SyncOnce` is set or the input was already derived;
9. require cluster access only for the durable path.

Do not move permission logic into `pkg/channel`. Keep the durable append payload and committed event flow unchanged apart from the channel identity selection.

- [ ] **Step 4: Re-run the focused tests**

Run: `GOWORK=off go test ./internal/usecase/message -run 'TestSendSyncOnce|TestSendAlreadyDerived|TestSendNoPersist.*SyncOnce' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/message/send.go internal/usecase/message/send_test.go
git commit -m "feat: restore durable sync once cmd send"
```

## Task 3: Make access adapters pass raw send channels and map `sync_once`

**Files:**
- Modify: `internal/access/api/message_send.go`
- Modify: `internal/access/api/server_test.go`
- Modify: `internal/access/api/integration_test.go`
- Modify: `internal/access/gateway/mapper.go`
- Modify: `internal/access/gateway/handler_test.go`

- [ ] **Step 1: Write the failing tests**

Add or update tests so they assert:

- HTTP `/message/send` accepts `header.sync_once` and top-level `sync_once`, and both set `SendCommand.Framer.SyncOnce`.
- HTTP send passes raw person channel IDs to the usecase instead of pre-normalizing them.
- Gateway send mapping passes raw person channel IDs through unchanged.
- Precomposed person channels such as `u1@u2` also reach the usecase unchanged; the usecase owns canonicalization now.
- Invalid person channels are surfaced through the usecase error mapping instead of being rejected by the adapter before the usecase runs.

Use a fake usecase that returns `runtimechannelid.ErrInvalidPersonChannel` or a real usecase stub, so the adapter test proves error mapping rather than adapter-side normalization.

- [ ] **Step 2: Run the focused tests and confirm they fail**

Run: `GOWORK=off go test ./internal/access/api ./internal/access/gateway -run 'TestSendMessage|TestHandlerOnFrameSend' -v`

Expected: FAIL because the adapters still normalize person channels locally.

- [ ] **Step 3: Implement the adapter changes**

In `internal/access/api/message_send.go`:

- add `SyncOnce int` to `sendMessageHeaderRequest`;
- add top-level `SyncOnce int` to `sendMessageRequest`;
- map `sync_once` from either location into `frame.Framer.SyncOnce`;
- stop normalizing person channels before calling `message.App.Send`.

In `internal/access/gateway/mapper.go`:

- stop normalizing person channels before building `SendCommand`;
- keep `pkt.Framer` intact, including `SyncOnce`.

Leave the error maps alone unless the tests show a missing mapping; `ErrInvalidPersonChannel` is already mapped in both HTTP and gateway adapters.

- [ ] **Step 4: Re-run the focused tests**

Run: `GOWORK=off go test ./internal/access/api ./internal/access/gateway -run 'TestSendMessage|TestHandlerOnFrameSend' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/access/api/message_send.go internal/access/api/server_test.go internal/access/api/integration_test.go internal/access/gateway/mapper.go internal/access/gateway/handler_test.go
git commit -m "feat: pass raw send channels through access adapters"
```

## Task 4: Resolve cmd-channel subscribers from the original source

**Files:**
- Modify: `internal/usecase/delivery/subscriber.go`
- Modify: `internal/usecase/delivery/subscriber_test.go`

- [ ] **Step 1: Write the failing tests**

Add tests that assert:

- `BeginSnapshot` on `g1____cmd` resolves the subscriber source from `g1`, not from the derived cmd ID.
- `BeginSnapshot` on `u2@u1____cmd` resolves the subscriber source from the original person channel and still exposes the derived request ID in the token.
- `SnapshotToken.Source()` preserves the requested channel identity while `SourceChannelID` / `SourceChannelType` point to the original source.
- The source mutation version for cmd channels is read from authoritative metadata when the store offers that path.

If needed, add a fake metadata store that records whether `GetChannel` or `GetChannelForPermission` was used.

- [ ] **Step 2: Run the focused tests and confirm they fail**

Run: `GOWORK=off go test ./internal/usecase/delivery -run 'TestSubscriberResolver|TestPersonChannelCodec' -v`

Expected: FAIL because the resolver still has its own cmd suffix constant and does not yet prefer the shared helper.

- [ ] **Step 3: Implement the resolver rewrite**

Update `internal/usecase/delivery/subscriber.go` to:

- use `internal/runtime/channelid.FromCommandChannel` instead of the local suffix constant;
- keep the requested channel ID in the token;
- resolve `SourceChannelID`, `SourceChannelType`, and `SourceSubscriberMutationVersion` from the stripped source channel;
- prefer an authoritative metadata lookup when the injected metadata store provides it;
- keep the existing derived behavior for person, group, and other store-backed channel types.

Do not add request-scoped subscribers or temp-channel generation here.

- [ ] **Step 4: Re-run the focused tests**

Run: `GOWORK=off go test ./internal/usecase/delivery -run 'TestSubscriberResolver|TestPersonChannelCodec' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/delivery/subscriber.go internal/usecase/delivery/subscriber_test.go
git commit -m "feat: resolve cmd channel subscribers from source"
```

## Task 5: Present original channel views during realtime delivery

**Files:**
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/deliveryrouting_test.go`

- [ ] **Step 1: Write the failing tests**

Add tests that assert:

- `buildRealtimeRecvPacket` strips `____cmd` before building the client-facing `RecvPacket.ChannelID`.
- person-channel view selection still returns the other participant, even when the stored durable message channel ends with `____cmd`.
- distributed person delivery groups remote routes by the stripped recipient view, not by a derived cmd string.
- `accessnode.DeliveryPushItem.ChannelID` stays equal to the durable cmd channel for ack ownership, while the encoded `RecvPacket.ChannelID` shows the original view.

- [ ] **Step 2: Run the focused tests and confirm they fail**

Run: `GOWORK=off go test ./internal/app -run 'TestBuildRealtimeRecvPacket|TestLocalDeliveryPushBuildsPersonChannelViewPerRouteUID|TestDistributedDeliveryPushBatchesPersonRoutesByRecipientChannelView' -v`

Expected: FAIL because the routing code still decodes the stored channel ID directly.

- [ ] **Step 3: Implement the presentation rewrite**

Update `internal/app/deliveryrouting.go` so:

- `buildRealtimeRecvPacket` always derives the client-facing channel view from the stripped source channel;
- `recipientChannelView` strips a cmd suffix before person-channel decoding;
- `distributedDeliveryPush.deliveryPushItems` groups person routes by stripped recipient view;
- remote delivery push items keep the durable cmd `ChannelID` for ack ownership, but the encoded frame uses the original channel view.

Keep group-channel presentation unchanged apart from the cmd suffix stripping.

- [ ] **Step 4: Re-run the focused tests**

Run: `GOWORK=off go test ./internal/app -run 'TestBuildRealtimeRecvPacket|TestLocalDeliveryPushBuildsPersonChannelViewPerRouteUID|TestDistributedDeliveryPushBatchesPersonRoutesByRecipientChannelView' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/deliveryrouting.go internal/app/deliveryrouting_test.go
git commit -m "feat: present original channel views for cmd delivery"
```

## Task 6: Gate ordinary conversation projection against cmd / SyncOnce messages

**Files:**
- Modify: `internal/usecase/conversation/projector.go`
- Modify: `internal/usecase/conversation/projector_test.go`

- [ ] **Step 1: Write the failing tests**

Add tests that assert:

- `SubmitCommitted` ignores durable messages whose `ChannelID` ends with `____cmd`.
- `SubmitCommitted` ignores messages whose `Framer.SyncOnce` is true.
- ordinary person/group committed messages still enqueue and flush exactly as before.

- [ ] **Step 2: Run the focused tests and confirm they fail**

Run: `GOWORK=off go test ./internal/usecase/conversation -run 'TestProjector' -v`

Expected: FAIL because the projector still accepts every durable message.

- [ ] **Step 3: Implement the projector gate**

Add a small top-of-function gate in `SubmitCommitted` that returns immediately for cmd-derived channel IDs or `SyncOnce` messages before enqueueing active hints.

Keep `Flush`, queueing, and group fanout behavior unchanged for normal messages.

- [ ] **Step 4: Re-run the focused tests**

Run: `GOWORK=off go test ./internal/usecase/conversation -run 'TestProjector' -v`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/conversation/projector.go internal/usecase/conversation/projector_test.go
git commit -m "feat: gate conversation projector for cmd messages"
```

## Task 7: Update the raw diff note and project knowledge

**Files:**
- Modify: `docs/raw/send-path-business-logic-diff.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Write the documentation edits**

Update `docs/raw/send-path-business-logic-diff.md` so it says P2b restored:

- durable `SyncOnce` sends onto the cmd channel;
- permission checks on the original channel;
- subscriber resolution from the original channel;
- original channel presentation in realtime delivery;
- ordinary conversation projection filtering for cmd / `SyncOnce`.

Keep the remaining non-goals visible, especially request-scoped subscribers, temp channels, online-only cmd delivery, CMD conversation/offline sync, and sendbatch.

Update `docs/development/PROJECT_KNOWLEDGE.md` with a short note that:

- `SyncOnce` durable sends now append to `____cmd`;
- permission checks still run on the original channel;
- `NoPersist + SyncOnce` still returns success without durable append or realtime delivery in this phase.

- [ ] **Step 2: Commit the docs**

```bash
git add docs/raw/send-path-business-logic-diff.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs: record p2b cmd channel send behavior"
```

## Task 8: Final verification

**Files:**
- No code changes expected; this is the validation pass.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelid ./internal/usecase/message ./internal/access/api ./internal/access/gateway ./internal/usecase/delivery ./internal/app ./internal/usecase/conversation -count=1
```

Expected: PASS

- [ ] **Step 2: Run the broader internal + slot test sweep**

Run:

```bash
GOWORK=off go test ./internal/... ./pkg/slot/... -count=1
```

Expected: PASS

- [ ] **Step 3: Sanity-check the worktree**

Run:

```bash
git status --short
```

Expected: only the intended plan/code/doc commits are present, with no accidental files left behind.

