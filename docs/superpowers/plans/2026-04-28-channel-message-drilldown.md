# Channel Message Drilldown Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show channel replica sets and maximum committed message sequence in the channel list, and let operators open a channel's paged messages from that row.

**Architecture:** Extend manager channel runtime DTOs with `max_message_seq` from the existing authoritative channel message reader path. Keep the channel page as an inventory/drilldown entry point and reuse the existing messages page by making it URL-query aware.

**Tech Stack:** Go manager access/usecase tests and implementation; React + TypeScript + React Router + react-intl web UI; Vitest + Testing Library frontend tests.

---

## File Structure

- Modify `internal/usecase/management/app.go`: extend `MessageReader` with a maximum sequence read method used by manager channel metadata.
- Modify `internal/usecase/management/channel_runtime_meta.go`: add `MaxMessageSeq` to `ChannelRuntimeMeta` and populate it for list/detail items.
- Modify `internal/usecase/management/channel_runtime_meta_test.go`: cover list/detail `MaxMessageSeq` behavior.
- Modify `internal/usecase/management/messages_test.go`: keep fake message reader aligned with the extended interface if needed.
- Modify `internal/app/manager_messages.go`: implement the new max sequence method with local/remote authoritative routing.
- Modify `internal/access/node/channel_messages_rpc.go`: add node RPC support for max sequence reads when the channel leader is remote.
- Modify `internal/access/node/channel_messages_rpc_test.go`: cover max sequence RPC behavior.
- Modify `internal/access/manager/channel_runtime_meta.go`: expose `max_message_seq` in list/detail DTOs.
- Modify `internal/access/manager/server_test.go`: assert manager JSON includes `max_message_seq`.
- Modify `web/src/lib/manager-api.types.ts`: add `max_message_seq` to channel metadata type.
- Modify `web/src/pages/channels/page.tsx`: render replicas, max sequence, and messages navigation action.
- Modify `web/src/pages/channels/page.test.tsx`: assert new columns and navigation action.
- Modify `web/src/pages/messages/page.tsx`: read URL params and auto-run existing query.
- Modify `web/src/pages/messages/page.test.tsx`: assert URL params initialize and auto-query.
- Modify `web/src/i18n/messages/en.ts` and `web/src/i18n/messages/zh-CN.ts`: add table/action labels.

## Task 1: Backend Usecase Max Message Sequence

**Files:**
- Modify: `internal/usecase/management/app.go`
- Modify: `internal/usecase/management/channel_runtime_meta.go`
- Test: `internal/usecase/management/channel_runtime_meta_test.go`

- [ ] **Step 1: Write failing list test**

Add a test in `internal/usecase/management/channel_runtime_meta_test.go` that configures the fake message reader with max sequence values for two channels, calls `ListChannelRuntimeMeta`, and asserts each returned item has the expected `MaxMessageSeq`.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off go test ./internal/usecase/management -run TestListChannelRuntimeMetaIncludesMaxMessageSeq -count=1`

Expected: FAIL because `ChannelRuntimeMeta.MaxMessageSeq` or the reader method is missing.

- [ ] **Step 3: Add usecase interface and field**

In `internal/usecase/management/app.go`, extend `MessageReader`:

```go
// MaxMessageSeq returns the maximum committed message sequence for a channel.
MaxMessageSeq(ctx context.Context, id channel.ChannelID) (uint64, error)
```

In `internal/usecase/management/channel_runtime_meta.go`, add to `ChannelRuntimeMeta`:

```go
// MaxMessageSeq is the maximum committed message sequence for the channel.
MaxMessageSeq uint64
```

- [ ] **Step 4: Populate list items minimally**

Update `ListChannelRuntimeMeta` so after scanning each page, it fills `MaxMessageSeq` for each item through `a.messages.MaxMessageSeq` when `a.messages != nil`. Return any error because the list is intended to be authoritative.

- [ ] **Step 5: Run list test to verify it passes**

Run: `GOWORK=off go test ./internal/usecase/management -run TestListChannelRuntimeMetaIncludesMaxMessageSeq -count=1`

Expected: PASS.

- [ ] **Step 6: Write failing detail test**

Add a test that calls `GetChannelRuntimeMeta` and asserts `MaxMessageSeq` is set from the fake message reader.

- [ ] **Step 7: Run detail test to verify it fails**

Run: `GOWORK=off go test ./internal/usecase/management -run TestGetChannelRuntimeMetaIncludesMaxMessageSeq -count=1`

Expected: FAIL because detail does not yet populate the field.

- [ ] **Step 8: Populate detail item**

Update `GetChannelRuntimeMeta` to call `a.messages.MaxMessageSeq` for the requested channel when available and assign it to the embedded `ChannelRuntimeMeta`.

- [ ] **Step 9: Run usecase tests**

Run: `GOWORK=off go test ./internal/usecase/management -count=1`

Expected: PASS.

## Task 2: Backend App and Node RPC Max Message Sequence

**Files:**
- Modify: `internal/app/manager_messages.go`
- Modify: `internal/access/node/channel_messages_rpc.go`
- Test: `internal/access/node/channel_messages_rpc_test.go`

- [ ] **Step 1: Write failing app-level/unit-oriented test if a suitable test file exists**

If there is already a focused `internal/app/manager_messages_test.go`, add coverage for `managerMessageReader.MaxMessageSeq` using a local channel log and remote stub. If no focused test harness exists, cover the remote node path in access-node tests and rely on management usecase fake coverage for orchestration.

- [ ] **Step 2: Write failing node RPC test**

In `internal/access/node/channel_messages_rpc_test.go`, add a test that sends a channel message query with a max-seq mode and asserts the response contains the committed high watermark without returning message rows.

- [ ] **Step 3: Run node RPC test to verify it fails**

Run: `GOWORK=off go test ./internal/access/node -run TestChannelMessagesRPCReturnsMaxMessageSeq -count=1`

Expected: FAIL because the RPC request/response has no max-seq mode.

- [ ] **Step 4: Extend node RPC types and handler**

In `internal/access/node/channel_messages_rpc.go`, add request field:

```go
// MaxSeqOnly requests only the maximum committed message sequence.
MaxSeqOnly bool `json:"max_seq_only,omitempty"`
```

Add response field:

```go
// MaxMessageSeq is the maximum committed message sequence for the channel.
MaxMessageSeq uint64 `json:"max_message_seq,omitempty"`
```

When `MaxSeqOnly` is true, load committed HW and return it without calling `QueryMessages`.

- [ ] **Step 5: Implement `managerMessageReader.MaxMessageSeq`**

In `internal/app/manager_messages.go`, add method:

```go
func (r managerMessageReader) MaxMessageSeq(ctx context.Context, id channel.ChannelID) (uint64, error)
```

Use the same metadata lookup and leader routing as `QueryMessages`. Local path returns `channelhandler.LoadCommittedHW(r.channelLog, id)`. Remote path calls `QueryChannelMessages` with `MaxSeqOnly: true` and returns `page.MaxMessageSeq`.

- [ ] **Step 6: Run node/app targeted tests**

Run: `GOWORK=off go test ./internal/access/node ./internal/app -run 'TestChannelMessagesRPCReturnsMaxMessageSeq|TestManagerMessageReaderMaxMessageSeq' -count=1`

Expected: PASS, or PASS for the node test and no matching app test if no app unit test was added.

## Task 3: Manager Access API DTO

**Files:**
- Modify: `internal/access/manager/channel_runtime_meta.go`
- Test: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write failing manager JSON test updates**

Update `TestManagerChannelRuntimeMetaReturnsPagedList` and `TestManagerChannelRuntimeMetaDetailReturnsObject` in `internal/access/manager/server_test.go` so stubbed data includes `MaxMessageSeq` and expected JSON includes `"max_message_seq":<value>`.

- [ ] **Step 2: Run tests to verify failure**

Run: `GOWORK=off go test ./internal/access/manager -run 'TestManagerChannelRuntimeMetaReturnsPagedList|TestManagerChannelRuntimeMetaDetailReturnsObject' -count=1`

Expected: FAIL because DTOs do not serialize `max_message_seq`.

- [ ] **Step 3: Extend DTO and mapper**

In `internal/access/manager/channel_runtime_meta.go`, add to `ChannelRuntimeMetaDTO`:

```go
// MaxMessageSeq is the maximum committed message sequence for the channel.
MaxMessageSeq uint64 `json:"max_message_seq"`
```

Set it in `channelRuntimeMetaDTO`.

- [ ] **Step 4: Run manager tests**

Run: `GOWORK=off go test ./internal/access/manager -run 'TestManagerChannelRuntimeMeta|TestManagerMessages' -count=1`

Expected: PASS.

## Task 4: Frontend API Types and Channel List UI

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/pages/channels/page.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`
- Test: `web/src/pages/channels/page.test.tsx`

- [ ] **Step 1: Write failing channel page test**

Update `channelRow` in `web/src/pages/channels/page.test.tsx` with `max_message_seq: 42`. Assert the rendered table shows `1, 2, 3` and `42`. Assert clicking `Messages` navigates to `/messages?channel_id=alpha&channel_type=1`.

- [ ] **Step 2: Run channel page test to verify failure**

Run: `cd web && bun run test -- src/pages/channels/page.test.tsx`

Expected: FAIL because columns/action are missing.

- [ ] **Step 3: Add API type field**

In `web/src/lib/manager-api.types.ts`, add `max_message_seq: number` to `ManagerChannelRuntimeMeta`.

- [ ] **Step 4: Add i18n labels**

Add English and Chinese message IDs:

```ts
"channels.table.replicas": "Replicas",
"channels.table.maxMessageSeq": "Max messageSeq",
"channels.viewMessages": "View channel {id} messages",
"channels.messagesAction": "Messages",
```

Use Chinese equivalents in `zh-CN.ts`.

- [ ] **Step 5: Render columns and action**

In `web/src/pages/channels/page.tsx`, import `useNavigate`, render `Replicas` and `Max messageSeq` table columns, and add a row button that navigates to:

```ts
`/messages?channel_id=${encodeURIComponent(channel.channel_id)}&channel_type=${channel.channel_type}`
```

- [ ] **Step 6: Run channel page test**

Run: `cd web && bun run test -- src/pages/channels/page.test.tsx`

Expected: PASS.

## Task 5: Frontend Messages URL Auto-Query

**Files:**
- Modify: `web/src/pages/messages/page.tsx`
- Test: `web/src/pages/messages/page.test.tsx`

- [ ] **Step 1: Write failing messages page test**

Add a test rendering `MessagesPage` under a memory router at `/messages?channel_id=room-1&channel_type=2`. Mock `getMessages` and assert it is called once with `{ channelId: "room-1", channelType: 2, limit: 50, clientMsgNo: "" }` and the returned row is displayed.

- [ ] **Step 2: Run test to verify failure**

Run: `cd web && bun run test -- src/pages/messages/page.test.tsx`

Expected: FAIL because URL params are ignored.

- [ ] **Step 3: Implement URL param initialization**

In `web/src/pages/messages/page.tsx`, import `useSearchParams`, derive initial form values from `channel_id` and `channel_type`, and add a guarded `useEffect` that auto-runs `runQuery` once when URL params are valid.

- [ ] **Step 4: Run messages page test**

Run: `cd web && bun run test -- src/pages/messages/page.test.tsx`

Expected: PASS.

## Task 6: Targeted Verification and Commits

**Files:**
- All modified files above.

- [ ] **Step 1: Run backend targeted tests**

Run: `GOWORK=off go test ./internal/usecase/management ./internal/access/manager ./internal/access/node ./internal/app -count=1`

Expected: PASS.

- [ ] **Step 2: Run frontend targeted tests**

Run: `cd web && bun run test -- src/pages/channels/page.test.tsx src/pages/messages/page.test.tsx src/lib/manager-api.test.ts`

Expected: PASS.

- [ ] **Step 3: Run frontend typecheck/build if available**

Run: `cd web && bun run build`

Expected: PASS.

- [ ] **Step 4: Inspect git diff**

Run: `git diff --stat && git diff --check`

Expected: no whitespace errors; diff only contains intended files.

- [ ] **Step 5: Commit implementation**

Run:

```bash
git add internal/usecase/management internal/app internal/access/node internal/access/manager web/src docs/superpowers/plans/2026-04-28-channel-message-drilldown.md
git commit -m "feat: add channel message drilldown"
```

Expected: commit succeeds.
