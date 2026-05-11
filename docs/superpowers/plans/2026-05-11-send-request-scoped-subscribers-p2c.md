# Send Request-Scoped Subscribers P2c Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore `/message/send.subscribers` command-style directed delivery by generating internal temp cmd channels and routing exact request-scoped subscriber snapshots through the current durable append and realtime delivery runtime paths.

**Architecture:** Keep access thin and put business-mode validation in `internal/usecase/message`. Durable subscriber sends append to a temp `____cmd` channel and dispatch with `MessageScopedUIDs`; non-durable subscriber sends allocate a transient message ID and submit directly to realtime delivery. Delivery resolution consumes message-scoped UIDs without reusing ordinary channel-level subscriber tags.

**Tech Stack:** Go 1.23, existing `frame` / `channel` / `messageevents` / `deliveryruntime` packages, FNV hashing from the standard library, `testify`, and the current app/usecase/runtime layering.

---

## Scope Check

This plan is one feature slice: request-scoped subscriber sends and their realtime/durable delivery plumbing. It does not implement CMD conversation/offline sync, `/message/sendbatch`, plugin/webhook/AI hooks, or special channel business permissions.

## File Structure

### Runtime channel identity

- Modify: `internal/runtime/channelid/command.go`
  - Responsibility: keep cmd suffix helpers.
- Create: `internal/runtime/channelid/request_subscribers.go`
  - Responsibility: normalize request subscriber lists and derive temp source/cmd channel IDs.
- Create: `internal/runtime/channelid/request_subscribers_test.go`
  - Responsibility: subscriber normalization and temp channel derivation tests.

### Message usecase

- Modify: `internal/usecase/message/command.go`
  - Responsibility: add request-scoped subscribers to `SendCommand` and committed/realtime metadata types if needed.
- Modify: `internal/usecase/message/app.go`
  - Responsibility: add transient message ID generator and realtime dispatcher dependencies.
- Modify: `internal/usecase/message/deps.go`
  - Responsibility: define small interfaces for transient IDs and realtime dispatch.
- Modify: `internal/usecase/message/send.go`
  - Responsibility: branch request-scoped subscriber sends before ordinary channel validation.
- Modify: `internal/usecase/message/send_test.go`
  - Responsibility: validation, durable temp cmd append, non-durable realtime dispatch, and ordinary-send regression coverage.

### Message events, node RPC, and delivery runtime

- Modify: `internal/contracts/messageevents/events.go`
  - Responsibility: carry optional `MessageScopedUIDs` through durable committed dispatch.
- Create or modify: `internal/contracts/messageevents/realtime.go`
  - Responsibility: define non-durable realtime dispatch event if a separate event type is cleaner than reusing committed metadata.
- Modify: `internal/access/node/delivery_submit_codec.go`
  - Responsibility: serialize optional `MessageScopedUIDs` across node-to-node committed delivery submission.
- Modify: `internal/access/node/delivery_submit_rpc_test.go`
  - Responsibility: round-trip and RPC tests for message-scoped committed envelopes.
- Modify: `internal/access/node/rpc_codec_benchmark_test.go`
  - Responsibility: keep benchmark fixture compilation aligned with the envelope shape.
- Modify: `internal/runtime/delivery/types.go`
  - Responsibility: add optional message-scoped UIDs to `CommittedEnvelope`.
- Modify: `internal/runtime/delivery/actor_test.go`
  - Responsibility: ensure zero-seq transient envelopes with unique message IDs dispatch correctly.
- Modify: `internal/usecase/delivery/submit.go`
  - Responsibility: expose a method for realtime/message-scoped submission if needed by app wiring.
- Modify: `internal/usecase/delivery/app_test.go`
  - Responsibility: verify the new submit method forwards metadata to runtime.

### Delivery routing and tags

- Modify: `internal/usecase/delivery/subscriber.go`
  - Responsibility: make temp/message-scoped source fields explicit and keep `ReusableTagState=false`.
- Modify: `internal/usecase/delivery/subscriber_test.go`
  - Responsibility: message-scoped temp snapshots and cmd temp source identity tests.
- Modify: `internal/runtime/deliverytag/manager.go`
  - Responsibility: add ephemeral tag materialization that does not update channel refs.
- Modify: `internal/runtime/deliverytag/manager_test.go`
  - Responsibility: prove ephemeral tags do not replace reusable channel refs.
- Modify: `internal/app/deliveryrouting.go`
  - Responsibility: pass `MessageScopedUIDs` into `BeginSnapshotWithRequest` and use ephemeral tags for non-reusable sources.
- Modify: `internal/app/deliveryrouting_test.go`
  - Responsibility: verify local/tag delivery uses exact request subscribers and packet views remain client-safe.

### Access and app wiring

- Modify: `internal/access/api/message_send.go`
  - Responsibility: accept `subscribers` and map them into `SendCommand`.
- Modify: `internal/access/api/server_test.go`
  - Responsibility: adapter-level mapping and validation tests.
- Modify: `internal/access/api/integration_test.go`
  - Responsibility: focused HTTP send coverage for request-scoped subscribers.
- Modify: `internal/app/build.go`
  - Responsibility: pass message ID generator and realtime dispatcher into `message.New`.
- Modify: `internal/app/deliveryrouting.go`
  - Responsibility: implement a small adapter from message realtime dispatch to delivery runtime.

### Documentation

- Modify: `docs/raw/send-path-business-logic-diff.md`
  - Responsibility: record P2c implementation status and remaining gaps.
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
  - Responsibility: concise business-rule note after implementation.

## Task 1: Add request-scoped temp channel helpers

**Files:**
- Create: `internal/runtime/channelid/request_subscribers.go`
- Create: `internal/runtime/channelid/request_subscribers_test.go`

- [ ] **Step 1: Write failing normalization tests**

Add tests for a helper like `NormalizeRequestSubscribers([]string) []string`:

```go
require.Equal(t, []string{"u1", "u2", "u3"}, NormalizeRequestSubscribers([]string{" u1 ", "u2", "u1", "", "u3"}))
```

- [ ] **Step 2: Write failing temp channel derivation tests**

Add tests for a helper like `RequestSubscriberChannelFor([]string)`:

```go
ch, err := RequestSubscriberChannelFor([]string{"u1", "u2"})
require.NoError(t, err)
require.Equal(t, frame.ChannelTypeTemp, ch.ChannelType)
require.Equal(t, []string{"u1", "u2"}, ch.Subscribers)
require.Equal(t, ToCommandChannel(ch.SourceChannelID), ch.CommandChannelID)
require.False(t, IsCommandChannel(ch.SourceChannelID))
require.True(t, IsCommandChannel(ch.CommandChannelID))
```

- [ ] **Step 3: Run the focused tests and confirm they fail**

Run: `GOWORK=off go test ./internal/runtime/channelid -run 'TestNormalizeRequestSubscribers|TestRequestSubscriberChannelFor' -v`

Expected: FAIL because the helpers do not exist.

- [ ] **Step 4: Implement the helpers**

Use `strings.TrimSpace`, first-seen de-duplication, `hash/fnv` `New64a`, and `strconv.FormatUint(sum, 10)`. Return an error when the normalized subscriber list is empty.

- [ ] **Step 5: Re-run the focused tests**

Run: `GOWORK=off go test ./internal/runtime/channelid -run 'TestNormalizeRequestSubscribers|TestRequestSubscriberChannelFor' -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/channelid/request_subscribers.go internal/runtime/channelid/request_subscribers_test.go
git commit -m "feat: derive request scoped temp channels"
```

## Task 2: Carry message-scoped subscribers through committed/realtime envelopes

**Files:**
- Modify: `internal/contracts/messageevents/events.go`
- Modify: `internal/contracts/messageevents/events_test.go`
- Modify: `internal/access/node/delivery_submit_codec.go`
- Modify: `internal/access/node/delivery_submit_rpc_test.go`
- Modify: `internal/access/node/rpc_codec_benchmark_test.go`
- Modify: `internal/runtime/delivery/types.go`
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/deliveryrouting_test.go`

- [ ] **Step 1: Write failing event clone tests**

In the messageevents package, assert `MessageCommitted.Clone()` deep-copies `MessageScopedUIDs`:

```go
event := MessageCommitted{MessageScopedUIDs: []string{"u1", "u2"}}
clone := event.Clone()
clone.MessageScopedUIDs[0] = "changed"
require.Equal(t, "u1", event.MessageScopedUIDs[0])
```

- [ ] **Step 2: Write failing delivery routing tests**

In `internal/app/deliveryrouting_test.go`, add a resolver spy or fake subscriber resolver that records whether `BeginSnapshotWithRequest` receives `MessageScopedUIDs` from a committed envelope.

- [ ] **Step 3: Write failing node RPC codec tests**

In `internal/access/node/delivery_submit_rpc_test.go`, extend the binary codec round-trip so `MessageScopedUIDs: []string{"u1", "u2"}` survives `encodeDeliverySubmitRequestBinary` and `decodeDeliverySubmitRequest`.

- [ ] **Step 4: Run focused tests and confirm they fail**

Run: `GOWORK=off go test ./internal/contracts/messageevents ./internal/access/node ./internal/app -run 'Test.*MessageScoped|Test.*ScopedSubscriber|TestDeliverySubmit.*MessageScoped' -v`

Expected: FAIL because the metadata is not present or not passed to resolver requests.

- [ ] **Step 5: Add metadata fields**

Add `MessageScopedUIDs []string` to `messageevents.MessageCommitted` and `deliveryruntime.CommittedEnvelope`. Ensure conversions in `committedEnvelopeFromMessageEvent` and `deliveryRuntimeCommittedSubmitter.SubmitCommitted` copy the slice.

- [ ] **Step 6: Update node RPC encoding**

Extend `appendCommittedEnvelope` and `readCommittedEnvelope` in `internal/access/node/delivery_submit_codec.go` to write/read a bounded string slice for `MessageScopedUIDs`. Add an explicit cap such as `maxDeliverySubmitMessageScopedUIDs = 10000` and tests for normal round-trip plus an over-limit payload. Keep the binary format version/tag check compatible with the existing codec style in that file.

- [ ] **Step 7: Route metadata into subscriber snapshots**

Update `localDeliveryResolver.BeginResolve` and `tagDeliveryResolver.BeginResolve` to call `BeginSnapshotWithRequest` when `env.MessageScopedUIDs` is non-empty; otherwise keep `BeginSnapshot`.

- [ ] **Step 8: Re-run focused tests**

Run: `GOWORK=off go test ./internal/contracts/messageevents ./internal/access/node ./internal/app -run 'Test.*MessageScoped|Test.*ScopedSubscriber|TestDeliverySubmit.*MessageScoped' -v`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/contracts/messageevents internal/access/node internal/runtime/delivery/types.go internal/app/deliveryrouting.go internal/app/deliveryrouting_test.go
git commit -m "feat: carry message scoped delivery subscribers"
```

## Task 3: Make message-scoped delivery tags ephemeral

**Files:**
- Modify: `internal/runtime/deliverytag/manager.go`
- Modify: `internal/runtime/deliverytag/manager_test.go`
- Modify: `internal/app/deliveryrouting.go`
- Modify: `internal/app/deliveryrouting_test.go`

- [ ] **Step 1: Write failing delivery-tag tests**

Create or extend tests so a reusable channel ref is built first, then an ephemeral request-scoped tag is built for the same channel key, and `CurrentRef(channelKey)` still points to the reusable tag.

- [ ] **Step 2: Write failing app routing test**

In `internal/app/deliveryrouting_test.go`, verify a message-scoped delivery does not overwrite an existing ordinary channel tag ref.

- [ ] **Step 3: Run focused tests and confirm they fail**

Run: `GOWORK=off go test ./internal/runtime/deliverytag ./internal/app -run 'Test.*Ephemeral|Test.*MessageScoped.*Tag' -v`

Expected: FAIL because `BuildLeaderTag` currently updates channel refs.

- [ ] **Step 4: Implement ephemeral tag materialization**

Add either `BuildEphemeralTag(request BuildRequest)` or `BuildRequest.Ephemeral bool`. The implementation must mint/store a tag body but must not update `cache.channelRef`.

- [ ] **Step 5: Use ephemeral tags for non-reusable sources**

Update `tagDeliveryResolver.leaderTagFromSnapshot` so `source.ReusableTagState=false` skips all `CurrentRef` reuse and calls the ephemeral materialization path.

- [ ] **Step 6: Re-run focused tests**

Run: `GOWORK=off go test ./internal/runtime/deliverytag ./internal/app -run 'Test.*Ephemeral|Test.*MessageScoped.*Tag' -v`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/runtime/deliverytag/manager.go internal/runtime/deliverytag/manager_test.go internal/app/deliveryrouting.go internal/app/deliveryrouting_test.go
git commit -m "feat: use ephemeral tags for message scoped delivery"
```

## Task 4: Update subscriber resolver semantics for temp message-scoped snapshots

**Files:**
- Modify: `internal/usecase/delivery/subscriber.go`
- Modify: `internal/usecase/delivery/subscriber_test.go`

- [ ] **Step 1: Write failing resolver tests**

Add tests for `BeginSnapshotWithRequest` on a temp cmd channel:

```go
token, err := resolver.BeginSnapshotWithRequest(ctx, channel.ChannelID{ID: channelid.ToCommandChannel("tmp1"), Type: frame.ChannelTypeTemp}, SubscriberSnapshotRequest{MessageScopedUIDs: []string{"u1", "u2", "u1"}})
require.NoError(t, err)
source := token.Source()
require.Equal(t, channelid.ToCommandChannel("tmp1"), source.ChannelID)
require.Equal(t, "tmp1", source.SourceChannelID)
require.False(t, source.ReusableTagState)
```

Then assert `NextPage` returns `u1,u2` once.

- [ ] **Step 2: Run focused tests and confirm they fail or expose incomplete source fields**

Run: `GOWORK=off go test ./internal/usecase/delivery -run 'TestSubscriberResolver.*MessageScoped|TestSubscriberResolver.*Temp.*Command' -v`

Expected: FAIL if source fields or de-duplication do not match the desired behavior.

- [ ] **Step 3: Implement resolver adjustments**

Ensure temp/message-scoped snapshots set `SourceChannelID` to the stripped temp source channel, set `SourceChannelType` to temp, set `Kind=message_scoped`, and set `ReusableTagState=false`.

- [ ] **Step 4: Re-run focused tests**

Run: `GOWORK=off go test ./internal/usecase/delivery -run 'TestSubscriberResolver.*MessageScoped|TestSubscriberResolver.*Temp.*Command' -v`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/delivery/subscriber.go internal/usecase/delivery/subscriber_test.go
git commit -m "feat: resolve temp message scoped subscribers"
```

## Task 5: Implement message usecase request-scoped validation and durable path

**Files:**
- Modify: `internal/usecase/message/command.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/deps.go`

- [ ] **Step 1: Add failing validation tests**

In `internal/usecase/message/send_test.go`, add tests that request-scoped sends reject:

- subscribers without `Framer.SyncOnce`;
- subscribers with a non-empty `ChannelID`;
- subscribers that normalize to an empty list.

- [ ] **Step 2: Add failing durable path test**

Assert a durable request-scoped send appends to a temp cmd channel and submits `MessageScopedUIDs`:

```go
result, err := app.Send(ctx, SendCommand{FromUID: "system", Framer: frame.Framer{SyncOnce: true}, RequestSubscribers: []string{"u1", "u2"}, Payload: []byte("cmd")})
require.NoError(t, err)
require.Equal(t, frame.ChannelTypeTemp, cluster.sendRequests[0].ChannelID.Type)
require.True(t, channelid.IsCommandChannel(cluster.sendRequests[0].ChannelID.ID))
require.Equal(t, []string{"u1", "u2"}, dispatcher.calls[0].MessageScopedUIDs)
require.Equal(t, frame.ReasonSuccess, result.Reason)
```

- [ ] **Step 3: Run focused tests and confirm they fail**

Run: `GOWORK=off go test ./internal/usecase/message -run 'TestSendRequestScoped|TestSendSubscribers' -v`

Expected: FAIL because `SendCommand` has no subscriber field and send rejects temp channels.

- [ ] **Step 4: Add command fields and validation errors**

Add `RequestSubscribers []string` to `SendCommand`. Add explicit errors such as `ErrRequestSubscribersRequireSyncOnce`, `ErrRequestSubscribersConflictChannel`, and `ErrRequestSubscribersRequired`. Map them later in access.

- [ ] **Step 5: Implement durable request-scoped send**

In `Send`, detect `len(cmd.RequestSubscribers)>0` before ordinary channel-type validation. Derive the temp cmd channel, build an internal command with `ChannelTypeTemp`, `ChannelID=tempCmdID`, `Framer.SyncOnce=true`, and call `sendDurable`. Pass `MessageScopedUIDs` to the committed dispatcher.

- [ ] **Step 6: Re-run focused tests**

Run: `GOWORK=off go test ./internal/usecase/message -run 'TestSendRequestScoped|TestSendSubscribers' -v`

Expected: durable request-scoped tests PASS; non-durable tests may still be pending for Task 6.

- [ ] **Step 7: Commit**

```bash
git add internal/usecase/message/command.go internal/usecase/message/send.go internal/usecase/message/send_test.go internal/usecase/message/app.go internal/usecase/message/deps.go
git commit -m "feat: send durable request scoped subscribers"
```

## Task 6: Add non-durable request-scoped realtime dispatch

**Files:**
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/deps.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/usecase/delivery/submit.go`
- Modify: `internal/usecase/delivery/app_test.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/deliveryrouting.go`

- [ ] **Step 1: Write failing non-durable message tests**

Assert `NoPersist + SyncOnce + RequestSubscribers`:

- does not call durable append;
- allocates a non-zero transient message ID;
- submits a realtime envelope with `MessageSeq=0`, `Framer.NoPersist=true`, and `MessageScopedUIDs`;
- returns `ReasonSuccess` and `MessageSeq=0`.

- [ ] **Step 2: Write failing app wiring and owner tests**

Use the smallest existing app or delivery test seam to verify the realtime dispatcher adapter submits to the local delivery runtime with message-scoped UIDs. Add a distributed push test showing a remote route push uses the local access node as `OwnerNodeID`, and an ack-routing test showing a recvack for that route returns to the transient message owner rather than the remote session node.

- [ ] **Step 3: Run focused tests and confirm they fail**

Run: `GOWORK=off go test ./internal/usecase/message ./internal/usecase/delivery ./internal/app -run 'TestSendRequestScopedNoPersist|Test.*Realtime.*Scoped' -v`

Expected: FAIL because no realtime dependency or transient ID generator is wired.

- [ ] **Step 4: Add message ID generator dependency**

Add a small interface in `internal/usecase/message/deps.go`:

```go
type MessageIDGenerator interface { Next() uint64 }
```

Add it to `message.Options` and wire the existing snowflake generator from `internal/app/build.go`.

- [ ] **Step 5: Add realtime dispatcher dependency**

Add an interface such as:

```go
type RealtimeDispatcher interface {
    SubmitRealtime(ctx context.Context, event messageevents.MessageRealtime) error
}
```

Alternatively, if simpler, add a delivery-usecase method that accepts a `deliveryruntime.CommittedEnvelope` with `Framer.NoPersist=true`. Keep `message` depending only on an interface.

- [ ] **Step 6: Implement non-durable request-scoped branch**

When request-scoped mode has `Framer.NoPersist=true`, build a `channel.Message` with temp cmd channel, transient message ID, `MessageSeq=0`, and submit it with `MessageScopedUIDs`. Do not call `sendDurable` and do not call the ordinary committed dispatcher. The local access node is the ack/retry owner for this transient delivery.

- [ ] **Step 7: Re-run focused tests**

Run: `GOWORK=off go test ./internal/usecase/message ./internal/usecase/delivery ./internal/app -run 'TestSendRequestScopedNoPersist|Test.*Realtime.*Scoped' -v`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/usecase/message internal/usecase/delivery internal/app
git commit -m "feat: deliver non durable request scoped commands"
```

## Task 7: Add HTTP `/message/send.subscribers` mapping

**Files:**
- Modify: `internal/access/api/message_send.go`
- Modify: `internal/access/api/server_test.go`
- Modify: `internal/access/api/integration_test.go`

- [ ] **Step 1: Write failing API mapping tests**

Add tests that POST `/message/send` with `subscribers`, `header.sync_once=1`, no `channel_id`, and no `channel_type`, then assert the fake message usecase is called with `RequestSubscribers` exactly and `ChannelID=""`, `ChannelType=0`.

Add another request-scoped test with `subscribers + channel_type` but no `channel_id`; it should still call the usecase and ignore the provided `channel_type` by passing `ChannelType=0` into `SendCommand`.

Also add a regression test that ordinary sends without subscribers still require `channel_id` and `channel_type`.

- [ ] **Step 2: Write failing API validation tests**

Add tests for:

- `subscribers + channel_id` returns HTTP 400 before durable work;
- `subscribers` without `sync_once` returns HTTP 400 or mapped validation error;
- empty `subscribers` with no `channel_id` remains invalid;
- ordinary sends without `channel_id` or `channel_type` remain invalid.

- [ ] **Step 3: Run focused API tests and confirm they fail**

Run: `GOWORK=off go test ./internal/access/api -run 'TestSendMessage.*Subscribers|TestHandleSendMessage.*Subscribers' -v`

Expected: FAIL because the request struct lacks `subscribers`.

- [ ] **Step 4: Implement request mapping**

Add `Subscribers []string` to `sendMessageRequest`. Split handler validation into two modes:

- ordinary mode (`len(req.Subscribers)==0`): keep requiring `from_uid`, `channel_id`, `channel_type`, and `payload`;
- request-scoped mode (`len(req.Subscribers)>0`): require `from_uid` and `payload`, allow missing `channel_id/channel_type`, reject non-empty `channel_id`, and pass `RequestSubscribers` to the usecase.

Add validation/error mapping for the new usecase errors in `mapSendError`.

- [ ] **Step 5: Re-run API tests**

Run: `GOWORK=off go test ./internal/access/api -run 'TestSendMessage.*Subscribers|TestHandleSendMessage.*Subscribers' -v`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/access/api/message_send.go internal/access/api/server_test.go internal/access/api/integration_test.go
git commit -m "feat: accept request scoped send subscribers"
```

## Task 8: Verify realtime packet behavior for temp cmd channels

**Files:**
- Modify: `internal/app/deliveryrouting_test.go`
- Modify: `internal/app/deliveryrouting.go` if needed

- [ ] **Step 1: Write or extend packet view tests**

Assert a message with `ChannelID=ToCommandChannel("tmp1")`, `ChannelType=frame.ChannelTypeTemp`, `Framer.SyncOnce=true`, and optionally `NoPersist=true` builds a recv packet with:

- `ChannelID="tmp1"`;
- `ChannelType=frame.ChannelTypeTemp`;
- `Framer.SyncOnce=true`;
- `Framer.NoPersist` preserved.

- [ ] **Step 2: Run focused tests**

Run: `GOWORK=off go test ./internal/app -run 'TestBuildRealtimeRecvPacket.*Command|Test.*Temp.*Command' -v`

Expected: PASS if P2b packet stripping already covers this; FAIL only if temp-specific behavior is missing.

- [ ] **Step 3: Implement only if needed**

If tests fail, adjust `buildRealtimeRecvPacket` to reuse the existing `FromCommandChannel` client-facing view for temp channels.

- [ ] **Step 4: Commit if code changed**

```bash
git add internal/app/deliveryrouting.go internal/app/deliveryrouting_test.go
git commit -m "test: cover temp cmd realtime packet view"
```

If no code changed, commit only the test or fold it into the previous relevant commit.

## Task 9: Update docs and project knowledge

**Files:**
- Modify: `docs/raw/send-path-business-logic-diff.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Update raw send-path diff**

Add a P2c status section documenting:

- `/message/send.subscribers` is restored;
- durable directed sends append to temp cmd channels;
- non-durable directed sends use online realtime delivery;
- CMD conversation/offline sync and sendbatch remain missing.

- [ ] **Step 2: Update project knowledge concisely**

Add no more than 2-4 bullets covering the new rules for request-scoped subscribers.

- [ ] **Step 3: Commit docs**

```bash
git add docs/raw/send-path-business-logic-diff.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs: record request scoped subscriber send rules"
```

## Task 10: Final verification

**Files:**
- No source edits expected.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
GOWORK=off go test ./internal/runtime/channelid ./internal/contracts/messageevents ./internal/usecase/message ./internal/usecase/delivery ./internal/runtime/delivery ./internal/runtime/deliverytag ./internal/access/api ./internal/access/node ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader internal smoke tests**

Run:

```bash
GOWORK=off go test ./internal/... ./pkg/slot/... -count=1
```

Expected: PASS.

- [ ] **Step 3: Review git status**

Run: `git status --short`

Expected: only intended committed changes; no dirty files.

- [ ] **Step 4: Request code review**

Use `superpowers:requesting-code-review` with emphasis on:

- request validation and legacy compatibility;
- message-scoped subscriber metadata copying;
- ephemeral tag behavior;
- no regression to P2a/P2b ordinary send behavior.
