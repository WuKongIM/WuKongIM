# Send Status P1 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore legacy send-before-append `SendBan` and `Disband` permissions in the current send path.

**Architecture:** Persist the missing channel status flags in slot metadata, carry them through FSM commands and authoritative slot proxy RPC, then enforce them in `internal/usecase/message` before durable append. Keep protocol adapters thin and leave `pkg/channel` append logic focused on log consistency.

**Tech Stack:** Go, existing slot meta Pebble store, slot FSM TLV commands, slot proxy RPC binary codecs, existing `frame.ReasonCode`, TDD with `GOWORK=off go test` in this worktree.

---

## Reference Docs

- Design: `docs/superpowers/specs/2026-05-11-send-status-p1-design.md`
- Difference analysis: `docs/raw/send-path-business-logic-diff.md`
- Internal layering: `internal/FLOW.md`
- Slot store flow: `pkg/slot/FLOW.md`
- P0 permission plan: `docs/superpowers/plans/2026-05-11-send-permission-p0.md`

## Scope

Implement now:

- Persist `metadb.Channel.Disband` and `metadb.Channel.SendBan`.
- Persist `/channel/info` and `internal/usecase/channel.UpdateInfo` status updates for `Ban`, `Disband`, and `SendBan`.
- Preserve new fields through `pkg/slot/fsm` `UpsertChannel` commands.
- Preserve new fields through `pkg/slot/proxy` channel RPC authoritative reads.
- Reject user sends before durable append:
  - sender person-channel `SendBan != 0` -> `frame.ReasonSendBan`
  - group channel `Disband != 0` -> `frame.ReasonDisband`
- Keep system UID bypass ahead of all permission checks.

Do not implement now:

- `AllowStranger`, `Large`, `NoPersist`, `SyncOnce`, cmd-channel conversion, request-scoped subscribers, `/message/sendbatch`, plugin/webhook/AI hooks, or stream compatibility.

## Files

Modify:

- `pkg/slot/meta/catalog.go` — add channel column descriptors for `disband` and `send_ban`.
- `pkg/slot/meta/channel.go` — add fields and persist/read/update them.
- `pkg/slot/meta/codec.go` — encode/decode optional channel fields.
- `pkg/slot/meta/codec_test.go` — codec coverage for new and legacy values.
- `pkg/slot/meta/channel_test.go` — store round-trip and update preservation coverage.
- `pkg/slot/meta/testutil_test.go` — expected raw value helper update.
- `pkg/slot/fsm/command.go` — add channel TLV tags and encode/decode fields.
- `pkg/slot/fsm/state_machine_test.go` — command round-trip coverage.
- `pkg/slot/fsm/command_inspection.go` — include new flags in inspection payload.
- `pkg/slot/fsm/command_inspection_test.go` — inspection coverage.
- `pkg/slot/proxy/store.go` — add richer `UpsertChannel` store method and keep compatibility wrappers.
- `pkg/slot/proxy/channel_rpc.go` — encode/decode channel RPC fields.
- `pkg/slot/proxy/binary_rpc_test.go` — channel RPC binary codec coverage.
- `pkg/slot/proxy/integration_test.go` — authoritative read coverage.
- `internal/usecase/channel/app.go` — store/usecase interface and `UpdateInfo` persistence.
- `internal/usecase/channel/app_test.go` — verify status persistence shape.
- `internal/usecase/message/permission.go` — add global sender `SendBan` and group `Disband` checks.
- `internal/usecase/message/send_test.go` — permission behavior tests.
- `pkg/slot/FLOW.md` — document channel metadata flags if stale.
- `docs/raw/send-path-business-logic-diff.md` — mark P1 restored items after implementation.
- `docs/development/PROJECT_KNOWLEDGE.md` — add one concise project knowledge note only if the behavior is not already captured.

Do not modify:

- `pkg/channel/handler/append.go` — no business permissions here.
- `internal/access/gateway/*` — sendack mapping already carries reason codes.
- Config files — P1 adds no config.

---

### Task 1: Extend Slot Channel Metadata Codec

**Files:**

- Modify: `pkg/slot/meta/catalog.go`
- Modify: `pkg/slot/meta/channel.go`
- Modify: `pkg/slot/meta/codec.go`
- Modify: `pkg/slot/meta/codec_test.go`
- Modify: `pkg/slot/meta/channel_test.go`
- Modify: `pkg/slot/meta/testutil_test.go`

- [ ] **Step 1: Write failing codec tests**

Add tests to `pkg/slot/meta/codec_test.go`:

```go
func TestChannelValueEncodingCarriesStatusFlags(t *testing.T) {
	key := encodeChannelPrimaryKey(7, "group-001", 1, 0)
	got := encodeChannelFamilyValue(1, 2, 3, 11, key)

	ban, disband, sendBan, version, err := decodeChannelFamilyValue(key, got)
	if err != nil {
		t.Fatalf("decodeChannelFamilyValue(): %v", err)
	}
	if ban != 1 || disband != 2 || sendBan != 3 || version != 11 {
		t.Fatalf("decoded channel value = (%d, %d, %d, %d), want (1, 2, 3, 11)", ban, disband, sendBan, version)
	}
}

func TestDecodeChannelFamilyValueDefaultsMissingStatusFlags(t *testing.T) {
	key := encodeChannelPrimaryKey(7, "group-001", 1, 0)
	legacy := encodeLegacyChannelFamilyValue(9, key)

	ban, disband, sendBan, version, err := decodeChannelFamilyValue(key, legacy)
	if err != nil {
		t.Fatalf("decodeChannelFamilyValue(legacy): %v", err)
	}
	if ban != 9 || disband != 0 || sendBan != 0 || version != 0 {
		t.Fatalf("decoded legacy value = (%d, %d, %d, %d), want (9, 0, 0, 0)", ban, disband, sendBan, version)
	}
}
```

Update existing codec tests to call the new signatures and expect returned `disband` and `sendBan` values.

- [ ] **Step 2: Write failing store tests**

Add/update in `pkg/slot/meta/channel_test.go`:

```go
func TestChannelStatusFlagsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(7)

	ch := Channel{ChannelID: "group-status", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, SubscriberMutationVersion: 5}
	if err := shard.CreateChannel(ctx, ch); err != nil {
		t.Fatalf("CreateChannel(): %v", err)
	}

	got, err := shard.GetChannel(ctx, ch.ChannelID, ch.ChannelType)
	if err != nil {
		t.Fatalf("GetChannel(): %v", err)
	}
	if !reflect.DeepEqual(got, ch) {
		t.Fatalf("unexpected channel after create:\n got: %#v\nwant: %#v", got, ch)
	}

	updated := Channel{ChannelID: ch.ChannelID, ChannelType: ch.ChannelType, Ban: 0, Disband: 1, SendBan: 0}
	if err := shard.UpdateChannel(ctx, updated); err != nil {
		t.Fatalf("UpdateChannel(): %v", err)
	}

	got, err = shard.GetChannel(ctx, ch.ChannelID, ch.ChannelType)
	if err != nil {
		t.Fatalf("GetChannel() after update: %v", err)
	}
	updated.SubscriberMutationVersion = ch.SubscriberMutationVersion
	if !reflect.DeepEqual(got, updated) {
		t.Fatalf("unexpected channel after update:\n got: %#v\nwant: %#v", got, updated)
	}
}
```

- [ ] **Step 3: Run RED tests**

Run:

```bash
GOWORK=off go test ./pkg/slot/meta -run 'TestChannel(ValueEncodingCarriesStatusFlags|StatusFlagsRoundTrip|DecodeChannelFamilyValueDefaultsMissingStatusFlags)' -count=1
```

Expected: FAIL because `Channel.Disband`, `Channel.SendBan`, and codec signatures do not exist yet.

- [ ] **Step 4: Implement metadata fields and codec**

In `pkg/slot/meta/catalog.go`, add channel column IDs/descriptors after `subscriber_mutation_version`, for example:

```go
{ID: channelColumnIDDisband, Name: "disband", Type: ColumnInt64},
{ID: channelColumnIDSendBan, Name: "send_ban", Type: ColumnInt64},
```

Include them in the channel primary family `ColumnIDs` without changing existing column IDs.

In `pkg/slot/meta/channel.go`, add English comments:

```go
// Disband marks a channel as dissolved and blocks sends.
Disband int64
// SendBan blocks sends while preserving receive semantics.
SendBan int64
```

Update `CreateChannel`, `UpdateChannel`, and `getChannelForPrimaryKeyLocked` to encode/decode/populate the fields while preserving `SubscriberMutationVersion` on status-only updates.

In `pkg/slot/meta/codec.go`, change signatures to:

```go
func encodeChannelFamilyValue(ban, disband, sendBan int64, subscriberMutationVersion uint64, key []byte) []byte
func decodeChannelFamilyValue(key, value []byte) (ban, disband, sendBan int64, subscriberMutationVersion uint64, err error)
```

Encode fields in ascending column order and decode missing `disband`/`send_ban` as zero. Keep missing `ban` corrupt.

- [ ] **Step 5: Update raw-value test helpers**

Update `pkg/slot/meta/testutil_test.go` and any codec call sites to use the new signature. Index values stay `encodeChannelIndexValue(ch.Ban)`.

- [ ] **Step 6: Run package tests**

Run:

```bash
GOWORK=off go test ./pkg/slot/meta -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/slot/meta
git commit -m "feat: persist channel status flags"
```

---

### Task 2: Preserve Status Flags Through Slot FSM Commands

**Files:**

- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/state_machine_test.go`
- Modify: `pkg/slot/fsm/command_inspection.go`
- Modify: `pkg/slot/fsm/command_inspection_test.go`

- [ ] **Step 1: Write failing command round-trip test**

Update `pkg/slot/fsm/state_machine_test.go` channel command tests to include `Disband` and `SendBan`, or add:

```go
func TestEncodeDecodeChannelStatusFlags(t *testing.T) {
	want := metadb.Channel{ChannelID: "c-status", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1}
	decoded, err := decodeCommand(EncodeUpsertChannelCommand(want))
	if err != nil {
		t.Fatalf("decodeCommand(): %v", err)
	}
	cmd, ok := decoded.(*upsertChannelCmd)
	if !ok {
		t.Fatalf("type = %T, want *upsertChannelCmd", decoded)
	}
	if cmd.channel != want {
		t.Fatalf("decoded channel = %#v, want %#v", cmd.channel, want)
	}
}
```

- [ ] **Step 2: Write failing inspection test**

Add to `pkg/slot/fsm/command_inspection_test.go`:

```go
func TestDecodeCommandInspectionIncludesChannelStatusFlags(t *testing.T) {
	got, err := DecodeCommandInspection(EncodeUpsertChannelCommand(metadb.Channel{
		ChannelID: "room-status", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1,
	}))
	require.NoError(t, err)

	require.Equal(t, int64(1), got.Payload["ban"])
	require.Equal(t, int64(1), got.Payload["disband"])
	require.Equal(t, int64(1), got.Payload["send_ban"])
}
```

- [ ] **Step 3: Run RED tests**

Run:

```bash
GOWORK=off go test ./pkg/slot/fsm -run 'TestEncodeDecodeChannelStatusFlags|TestDecodeCommandInspectionIncludesChannelStatusFlags' -count=1
```

Expected: FAIL because the command codec/inspection omits the new fields.

- [ ] **Step 4: Implement FSM tags and inspection**

In `pkg/slot/fsm/command.go`, add new channel tags after `tagChannelBan`, for example:

```go
tagChannelDisband uint8 = 4
tagChannelSendBan uint8 = 5
```

Update `EncodeUpsertChannelCommand` sizing and field writing to include `ch.Disband` and `ch.SendBan`. Update `decodeUpsertChannel` to read the new tags and skip unknown tags as before.

In `pkg/slot/fsm/command_inspection.go`, include:

```go
"disband":  channel.Disband,
"send_ban": channel.SendBan,
```

- [ ] **Step 5: Run package tests**

Run:

```bash
GOWORK=off go test ./pkg/slot/fsm -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/slot/fsm
git commit -m "feat: carry channel status flags in slot fsm"
```

---

### Task 3: Preserve Status Flags Through Slot Proxy Store/RPC

**Files:**

- Modify: `pkg/slot/proxy/store.go`
- Modify: `pkg/slot/proxy/channel_rpc.go`
- Modify: `pkg/slot/proxy/binary_rpc_test.go`
- Modify: `pkg/slot/proxy/integration_test.go`

- [ ] **Step 1: Write failing binary codec test**

Add to `pkg/slot/proxy/binary_rpc_test.go`:

```go
func TestChannelRPCBinaryCodecRoundTripsStatusFlags(t *testing.T) {
	resp := channelRPCResponse{
		Status:   rpcStatusOK,
		LeaderID: 2,
		Channel:  &metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, SubscriberMutationVersion: 7},
	}
	body := encodeChannelRPCResponseBinary(resp)
	got, err := decodeChannelRPCResponseBinary(body)
	require.NoError(t, err)
	require.Equal(t, resp, got)
}
```

- [ ] **Step 2: Update authoritative read test expectation**

In `pkg/slot/proxy/integration_test.go`, change `TestStoreGetChannelForPermissionReadsAuthoritativeSlot` to seed and expect:

```go
ch := metadb.Channel{ChannelID: channelID, ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1}
```

- [ ] **Step 3: Add richer store method test seam if needed**

If there is an existing proxy store mutation test, add one for:

```go
require.NoError(t, nodes[0].store.UpsertChannel(ctx, metadb.Channel{ChannelID: channelID, ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1}))
got, err := nodes[0].store.GetChannelForPermission(ctx, channelID, 2)
require.NoError(t, err)
require.Equal(t, int64(1), got.Disband)
require.Equal(t, int64(1), got.SendBan)
```

If no cheap existing seam exists, rely on `GetChannelForPermissionReadsAuthoritativeSlot` plus FSM tests for mutation preservation.

- [ ] **Step 4: Run RED tests**

Run:

```bash
GOWORK=off go test ./pkg/slot/proxy -run 'TestChannelRPCBinaryCodecRoundTripsStatusFlags|TestStoreGetChannelForPermissionReadsAuthoritativeSlot' -count=1
```

Expected: FAIL until RPC codec/store fields are implemented.

- [ ] **Step 5: Implement proxy support**

In `pkg/slot/proxy/store.go`, add:

```go
// UpsertChannel persists all supported channel metadata flags through the authoritative slot.
func (s *Store) UpsertChannel(ctx context.Context, ch metadb.Channel) error {
	slotID := s.cluster.SlotForKey(ch.ChannelID)
	hashSlot := hashSlotForKey(s.cluster, ch.ChannelID)
	cmd := metafsm.EncodeUpsertChannelCommand(ch)
	return proposeWithHashSlot(ctx, s.cluster, slotID, hashSlot, cmd)
}
```

Update `CreateChannel` and `UpdateChannel` to call `UpsertChannel` so legacy callers keep working while richer callers do not drop fields.

In `pkg/slot/proxy/channel_rpc.go`, append `Disband` and `SendBan` to `appendChannelPtr` after `Ban`, and read them in `readChannelPtr` before `SubscriberMutationVersion`.

- [ ] **Step 6: Run package tests**

Run:

```bash
GOWORK=off go test ./pkg/slot/proxy -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/slot/proxy
git commit -m "feat: expose channel status flags through slot proxy"
```

---

### Task 4: Persist Status Flags From Channel Usecase

**Files:**

- Modify: `internal/usecase/channel/app.go`
- Modify: `internal/usecase/channel/app_test.go`

- [ ] **Step 1: Write failing usecase test**

Add to `internal/usecase/channel/app_test.go`:

```go
func TestUpdateInfoPersistsStatusFlags(t *testing.T) {
	store := &recordingStore{}
	app := New(Options{Store: store})

	err := app.UpdateInfo(context.Background(), Info{ChannelID: "g1", ChannelType: 2, Ban: true, Disband: true, SendBan: true})
	require.NoError(t, err)
	require.Len(t, store.upsertChannels, 1)
	require.Equal(t, metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1}, store.upsertChannels[0])
}
```

Update `recordingStore` to include `upsertChannels []metadb.Channel` and an `UpsertChannel(context.Context, metadb.Channel) error` method after the test is red.

- [ ] **Step 2: Run RED test**

Run:

```bash
GOWORK=off go test ./internal/usecase/channel -run TestUpdateInfoPersistsStatusFlags -count=1
```

Expected: FAIL because `Store` does not expose `UpsertChannel` and `UpdateInfo` still calls `UpdateChannel` with only `Ban`.

- [ ] **Step 3: Implement usecase persistence**

Update `internal/usecase/channel.Store` to include:

```go
UpsertChannel(ctx context.Context, ch metadb.Channel) error
```

Update `UpdateInfo`:

```go
return a.store.UpsertChannel(ctx, metadb.Channel{
	ChannelID:   info.ChannelID,
	ChannelType: int64(info.ChannelType),
	Ban:         boolToInt64(info.Ban),
	Disband:     boolToInt64(info.Disband),
	SendBan:     boolToInt64(info.SendBan),
})
```

Keep `UpdateChannel` in the interface only if other channel usecase code still needs it. If not needed by `internal/usecase/channel`, remove it from the interface and recording fake.

- [ ] **Step 4: Run channel usecase tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/channel -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/channel
git commit -m "feat: persist channel status updates"
```

---

### Task 5: Enforce Sender SendBan and Group Disband Before Append

**Files:**

- Modify: `internal/usecase/message/permission.go`
- Modify: `internal/usecase/message/send_test.go`

- [ ] **Step 1: Write failing sender SendBan test**

Add to `internal/usecase/message/send_test.go` near existing permission tests:

```go
func TestSendRejectsSenderSendBanBeforeAppend(t *testing.T) {
	cluster := newRecordingCluster()
	app := New(Options{
		Cluster:         cluster,
		PermissionStore: newPermissionStoreWithChannels(map[string]metadb.Channel{
			permissionChannelKey("u1", int64(frame.ChannelTypePerson)): {ChannelID: "u1", ChannelType: int64(frame.ChannelTypePerson), SendBan: 1},
			permissionChannelKey("g1", int64(frame.ChannelTypeGroup)):  {ChannelID: "g1", ChannelType: int64(frame.ChannelTypeGroup)},
		}),
	})

	result, err := app.Send(context.Background(), validSendCommand("u1", "g1", frame.ChannelTypeGroup))
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSendBan, result.Reason)
	require.Equal(t, 0, cluster.appendCalls)
}
```

Adjust helper names to match existing test helpers in `send_test.go`.

- [ ] **Step 2: Write failing group Disband test**

Add:

```go
func TestSendRejectsGroupDisbandBeforeAppend(t *testing.T) {
	cluster := newRecordingCluster()
	permissions := newPermissionStoreWithChannels(map[string]metadb.Channel{
		permissionChannelKey("g1", int64(frame.ChannelTypeGroup)): {ChannelID: "g1", ChannelType: int64(frame.ChannelTypeGroup), Disband: 1},
	})
	permissions.addSubscriber("g1", int64(frame.ChannelTypeGroup), "u1")
	app := New(Options{Cluster: cluster, PermissionStore: permissions})

	result, err := app.Send(context.Background(), validSendCommand("u1", "g1", frame.ChannelTypeGroup))
	require.NoError(t, err)
	require.Equal(t, frame.ReasonDisband, result.Reason)
	require.Equal(t, 0, cluster.appendCalls)
}
```

- [ ] **Step 3: Write/adjust system UID bypass test**

Add or extend an existing system UID test so a system sender with person `SendBan=1` and target `Disband=1` still reaches append:

```go
func TestSendSystemUIDBypassesStatusPermissions(t *testing.T) {
	cluster := newRecordingCluster()
	permissions := newPermissionStoreWithChannels(map[string]metadb.Channel{
		permissionChannelKey("sys", int64(frame.ChannelTypePerson)): {ChannelID: "sys", ChannelType: int64(frame.ChannelTypePerson), SendBan: 1},
		permissionChannelKey("g1", int64(frame.ChannelTypeGroup)):  {ChannelID: "g1", ChannelType: int64(frame.ChannelTypeGroup), Disband: 1},
	})
	app := New(Options{Cluster: cluster, PermissionStore: permissions, SystemUIDs: staticSystemUIDs{"sys": true}})

	result, err := app.Send(context.Background(), validSendCommand("sys", "g1", frame.ChannelTypeGroup))
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, result.Reason)
	require.Equal(t, 1, cluster.appendCalls)
}
```

- [ ] **Step 4: Run RED tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -run 'TestSendRejectsSenderSendBanBeforeAppend|TestSendRejectsGroupDisbandBeforeAppend|TestSendSystemUIDBypassesStatusPermissions' -count=1
```

Expected: FAIL until permission logic is added.

- [ ] **Step 5: Implement permission logic**

In `internal/usecase/message/permission.go`:

- Add `checkSenderSendPermission(ctx, cmd.FromUID)` called after system UID bypass and before channel-type switch.
- `GetChannelForPermission(ctx, fromUID, int64(frame.ChannelTypePerson))`:
  - `ErrNotFound` -> success.
  - other error -> `ReasonSystemError`, error.
  - `SendBan != 0` -> `ReasonSendBan`.
- In `checkGroupSendPermission`, after `Ban` and before list checks:
  - `Disband != 0` -> `ReasonDisband`.

Keep P0 order otherwise unchanged.

- [ ] **Step 6: Run message tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/message -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/usecase/message
git commit -m "feat: enforce send status permissions"
```

---

### Task 6: Update Flow and Difference Documentation

**Files:**

- Modify: `pkg/slot/FLOW.md`
- Modify: `docs/raw/send-path-business-logic-diff.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md` if useful

- [ ] **Step 1: Update slot flow doc if stale**

Read `pkg/slot/FLOW.md` and update channel metadata/FSM/RPC descriptions to mention that channel metadata now carries `Ban`, `Disband`, `SendBan`, and `SubscriberMutationVersion` where appropriate.

- [ ] **Step 2: Update raw difference doc**

In `docs/raw/send-path-business-logic-diff.md`, add a dated P1 status note under `SendBan` and `频道 Ban / Disband`:

```markdown
P1 status (2026-05-11): current project now persists `SendBan`/`Disband` and enforces sender `SendBan` plus group `Disband` before durable append. Remaining out-of-scope compatibility items stay listed below.
```

Do not remove the historical analysis; annotate it so future readers know what changed.

- [ ] **Step 3: Add concise project knowledge only if not duplicated**

If `docs/development/PROJECT_KNOWLEDGE.md` lacks this rule, add one short bullet:

```markdown
- Send permissions live in `internal/usecase/message` before durable append; `pkg/channel` append stays business-rule free.
```

- [ ] **Step 4: Run doc/status check**

Run:

```bash
git diff -- docs/raw/send-path-business-logic-diff.md pkg/slot/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
```

Expected: docs clearly mark P1 restored behavior and do not overclaim remaining legacy parity.

- [ ] **Step 5: Commit**

```bash
git add pkg/slot/FLOW.md docs/raw/send-path-business-logic-diff.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs: update send status permission notes"
```

---

### Task 7: Final Verification

**Files:**

- All modified files from Tasks 1-6

- [ ] **Step 1: Run focused verification**

Run:

```bash
GOWORK=off go test ./internal/usecase/message ./internal/usecase/channel ./internal/access/api ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader slot/internal verification**

Run:

```bash
GOWORK=off go test ./internal/... ./pkg/slot/... -count=1
```

Expected: PASS.

- [ ] **Step 3: Inspect git status and commit log**

Run:

```bash
git status --short
git log --oneline --decorate -8
```

Expected: clean worktree except intentionally uncommitted files, and recent commits match the tasks.

- [ ] **Step 4: Report completion**

Summarize:

- behavior restored (`SendBan`, `Disband`)
- key files changed
- verification commands and results
- any residual risks, especially unimplemented legacy compatibility items
