# Send AllowStranger Permission Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore `AllowStranger` as authoritative channel metadata and make it allow stranger person sends when personal whitelist enforcement is enabled.

**Architecture:** Add `AllowStranger` to the slot-authoritative channel metadata model, propagate it through slot meta, slot FSM, slot proxy, and channel usecase APIs, then consume it only in `internal/usecase/message` person-send permission checks. Receiver denylist and sender `SendBan` keep higher precedence, while default person sends stay open when `PersonWhitelistEnabled=false`.

**Tech Stack:** Go, Pebble-backed slot meta, slot FSM TLV commands, binary slot proxy RPC, `testify/require`, `GOWORK=off go test`.

---

## References

- Spec: `docs/superpowers/specs/2026-05-14-send-allow-stranger-design.md`
- Legacy storage/model references:
  - `learn_project/WuKongIM/pkg/wkdb/model.go`
  - `learn_project/WuKongIM/pkg/wkdb/channel.go`
  - `learn_project/WuKongIM/internal/api/channel_model.go`
- Current permission entry point: `internal/usecase/message/permission.go`
- Current channel metadata model: `pkg/slot/meta/channel.go`

## File Structure

Modify these files:

- `pkg/slot/meta/channel.go`: add `Channel.AllowStranger`, persist through create/update/upsert/get/list.
- `pkg/slot/meta/catalog.go`: add the `allow_stranger` channel table column and include it in the primary family.
- `pkg/slot/meta/codec.go`: encode/decode the new channel family field with old-record default zero.
- `pkg/slot/meta/batch.go`: pass `AllowStranger` through snapshot and batch channel writes.
- `pkg/slot/meta/subscriber.go`: preserve `AllowStranger` when subscriber mutations create/update channel rows.
- `pkg/slot/meta/testutil_test.go`: align low-level expected encoded values.
- `pkg/slot/meta/codec_test.go`: align low-level codec tests with the new channel family helper signature and old-record default-zero behavior.
- `pkg/slot/meta/channel_test.go`: prove create/update/upsert/get/list round-trip the new flag.
- `pkg/slot/fsm/command.go`: add `AllowStranger` TLV field to upsert-channel commands.
- `pkg/slot/fsm/command_inspection.go`: expose `allow_stranger` in command inspection payloads.
- `pkg/slot/fsm/command_inspection_test.go`: assert inspection includes `allow_stranger`.
- `pkg/slot/fsm/state_machine_test.go`: assert command encode/decode preserves `AllowStranger`.
- `pkg/slot/proxy/channel_rpc.go`: add a new response codec version that carries `AllowStranger`; old response versions decode it as zero.
- `pkg/slot/proxy/binary_rpc_test.go`: assert single-channel, scan-page, and legacy response decoding behavior.
- `pkg/slot/proxy/integration_test.go`: assert authoritative `GetChannelForPermission` returns `AllowStranger`.
- `internal/usecase/channel/types.go`: update the `AllowStranger` comment to its restored behavior.
- `internal/usecase/channel/app.go`: map `Info.AllowStranger` to `metadb.Channel.AllowStranger`.
- `internal/usecase/channel/app_test.go`: assert the channel usecase persists `AllowStranger`.
- `internal/usecase/message/permission.go`: consume receiver `AllowStranger` in person send permission.
- `internal/usecase/message/send.go`: preserve permission denial reasons when permission checks return infrastructure errors.
- `internal/usecase/message/send_test.go`: add permission precedence and error-path tests.
- `docs/raw/send-path-business-logic-diff.md`: mark the gap restored and describe precedence.
- `pkg/slot/FLOW.md`: include `AllowStranger` in channel metadata / permission RPC flow.
- `internal/FLOW.md`: update only if its channel/message flow description lists persisted channel flags or person permission steps.

No new config file or `wukongim.conf.example` change is expected.

## Preflight: Use An Isolated Worktree

- [ ] **Step 1: Create a feature worktree**

Run from the current repo:

```bash
git worktree add ../WuKongIM-send-allow-stranger -b feature/send-allow-stranger
cd ../WuKongIM-send-allow-stranger
```

Expected: a clean worktree on `feature/send-allow-stranger`.

- [ ] **Step 2: Verify the plan and spec are present**

```bash
git status --short
ls docs/superpowers/specs/2026-05-14-send-allow-stranger-design.md
ls docs/superpowers/plans/2026-05-14-send-allow-stranger.md
```

Expected: `git status --short` is empty except for unrelated user files already known to be ignored.

---

### Task 1: Persist `AllowStranger` In Slot Meta

**Files:**
- Modify: `pkg/slot/meta/channel.go`
- Modify: `pkg/slot/meta/catalog.go`
- Modify: `pkg/slot/meta/codec.go`
- Modify: `pkg/slot/meta/batch.go`
- Modify: `pkg/slot/meta/subscriber.go`
- Test: `pkg/slot/meta/channel_test.go`
- Test: `pkg/slot/meta/testutil_test.go`
- Test: `pkg/slot/meta/codec_test.go`

- [ ] **Step 1: Write failing slot-meta tests**

Update `pkg/slot/meta/channel_test.go`:

```go
func TestChannelStatusFlagsRoundTrip(t *testing.T) {
    ctx := context.Background()
    db := openTestDB(t)
    shard := db.ForSlot(7)

    ch := Channel{ChannelID: "group-status", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, AllowStranger: 1, SubscriberMutationVersion: 5}
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

    updated := Channel{ChannelID: ch.ChannelID, ChannelType: ch.ChannelType, Ban: 0, Disband: 1, SendBan: 0, AllowStranger: 1}
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

Add a focused scan assertion in the same file:

```go
func TestShardListChannelsPagePreservesAllowStranger(t *testing.T) {
    ctx := context.Background()
    db := openTestDB(t)
    shard := db.ForSlot(7)

    want := Channel{ChannelID: "person-a", ChannelType: 1, AllowStranger: 1}
    if err := shard.CreateChannel(ctx, want); err != nil {
        t.Fatalf("CreateChannel(): %v", err)
    }

    got, _, done, err := shard.ListChannelsPage(ctx, ChannelCursor{}, 10)
    if err != nil {
        t.Fatalf("ListChannelsPage(): %v", err)
    }
    if !done || len(got) != 1 {
        t.Fatalf("unexpected page: got=%#v done=%v", got, done)
    }
    if !reflect.DeepEqual(got[0], want) {
        t.Fatalf("listed channel = %#v, want %#v", got[0], want)
    }
}

func TestUpsertChannelPreservesAllowStranger(t *testing.T) {
    ctx := context.Background()
    db := openTestDB(t)
    shard := db.ForSlot(7)

    want := Channel{ChannelID: "person-upsert", ChannelType: 1, AllowStranger: 1}
    if err := shard.UpsertChannel(ctx, want); err != nil {
        t.Fatalf("UpsertChannel(): %v", err)
    }

    got, err := shard.GetChannel(ctx, want.ChannelID, want.ChannelType)
    if err != nil {
        t.Fatalf("GetChannel(): %v", err)
    }
    if !reflect.DeepEqual(got, want) {
        t.Fatalf("upserted channel = %#v, want %#v", got, want)
    }
}
```

Update expected encoded channel values in `pkg/slot/meta/testutil_test.go` and low-level channel-family codec assertions in `pkg/slot/meta/codec_test.go` after the production signature changes in Step 3.

- [ ] **Step 2: Run tests to verify failure**

```bash
GOWORK=off go test ./pkg/slot/meta -run 'ChannelStatusFlagsRoundTrip|ShardListChannelsPagePreservesAllowStranger|UpsertChannelPreservesAllowStranger' -count=1
```

Expected: FAIL or compile failure because `Channel.AllowStranger` does not exist yet.

- [ ] **Step 3: Implement slot-meta persistence**

In `pkg/slot/meta/channel.go`, add an English-commented field:

```go
type Channel struct {
    ChannelID   string
    ChannelType int64
    Ban         int64
    // Disband marks a channel as dissolved and blocks sends.
    Disband int64
    // SendBan blocks sends while preserving receive semantics.
    SendBan int64
    // AllowStranger permits stranger sends to a person channel receiver.
    AllowStranger int64
    // SubscriberMutationVersion is the durable version fence for subscriber mutations.
    SubscriberMutationVersion uint64
}
```

In `pkg/slot/meta/catalog.go`:

```go
channelColumnIDAllowStranger uint16 = 7
```

Add `{ID: channelColumnIDAllowStranger, Name: "allow_stranger", Type: ColumnInt64}` and include `channelColumnIDAllowStranger` in the channel primary family `ColumnIDs`.

In `pkg/slot/meta/codec.go`, change the helper shape:

```go
func encodeChannelFamilyValue(ban, disband, sendBan, allowStranger int64, subscriberMutationVersion uint64, key []byte) []byte {
    payload := make([]byte, 0, 40)
    payload = appendIntValue(payload, channelColumnIDBan, 0, ban)
    payload = appendUint64Value(payload, channelColumnIDSubscriberMutationVersion, channelColumnIDBan, subscriberMutationVersion)
    previousColumnID := channelColumnIDSubscriberMutationVersion
    if disband != 0 {
        payload = appendIntValue(payload, channelColumnIDDisband, previousColumnID, disband)
        previousColumnID = channelColumnIDDisband
    }
    if sendBan != 0 {
        payload = appendIntValue(payload, channelColumnIDSendBan, previousColumnID, sendBan)
        previousColumnID = channelColumnIDSendBan
    }
    if allowStranger != 0 {
        payload = appendIntValue(payload, channelColumnIDAllowStranger, previousColumnID, allowStranger)
    }
    return wrapFamilyValue(key, payload)
}
```

Update decode to return and populate `allowStranger`:

```go
func decodeChannelFamilyValue(key, value []byte) (ban, disband, sendBan, allowStranger int64, subscriberMutationVersion uint64, err error) {
    // existing loop
    switch colID {
    case channelColumnIDBan:
        ban = decodeZigZagInt64(raw)
        haveBan = true
    case channelColumnIDDisband:
        disband = decodeZigZagInt64(raw)
    case channelColumnIDSendBan:
        sendBan = decodeZigZagInt64(raw)
    case channelColumnIDAllowStranger:
        allowStranger = decodeZigZagInt64(raw)
    case channelColumnIDSubscriberMutationVersion:
        subscriberMutationVersion = raw
    }
}
```

Also include `channelColumnIDAllowStranger` in invalid uint/bytes column guards.

Use `rg "encodeChannelFamilyValue|decodeChannelFamilyValue" pkg/slot/meta` and update all call sites and low-level assertions in `channel.go`, `batch.go`, `subscriber.go`, `testutil_test.go`, and `codec_test.go` to pass/read `AllowStranger`.

- [ ] **Step 4: Run slot-meta tests**

```bash
GOWORK=off go test ./pkg/slot/meta -run 'ChannelStatusFlagsRoundTrip|ShardListChannelsPagePreservesAllowStranger|UpsertChannelPreservesAllowStranger' -count=1
GOWORK=off go test ./pkg/slot/meta -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit slot-meta changes**

```bash
git add pkg/slot/meta/channel.go pkg/slot/meta/catalog.go pkg/slot/meta/codec.go pkg/slot/meta/batch.go pkg/slot/meta/subscriber.go pkg/slot/meta/channel_test.go pkg/slot/meta/testutil_test.go pkg/slot/meta/codec_test.go
git commit -m "feat: persist channel allow stranger flag"
```

---

### Task 2: Propagate `AllowStranger` Through Slot FSM Commands

**Files:**
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/command_inspection.go`
- Test: `pkg/slot/fsm/command_inspection_test.go`
- Test: `pkg/slot/fsm/state_machine_test.go`

- [ ] **Step 1: Write failing FSM tests**

Update `pkg/slot/fsm/state_machine_test.go`:

```go
func TestEncodeDecodeChannelStatusFlags(t *testing.T) {
    want := metadb.Channel{ChannelID: "c-status", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, AllowStranger: 1}
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

Update `pkg/slot/fsm/command_inspection_test.go`:

```go
got, err := DecodeCommandInspection(EncodeUpsertChannelCommand(metadb.Channel{
    ChannelID: "room-status", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, AllowStranger: 1,
}))
require.NoError(t, err)
require.Equal(t, int64(1), got.Payload["allow_stranger"])
```

- [ ] **Step 2: Run tests to verify failure**

```bash
GOWORK=off go test ./pkg/slot/fsm -run 'EncodeDecodeChannelStatusFlags|DecodeCommandInspectionIncludesChannelStatusFlags' -count=1
```

Expected: FAIL because FSM command encoding/inspection does not carry `AllowStranger` yet.

- [ ] **Step 3: Implement FSM propagation**

In `pkg/slot/fsm/command.go`, add the next channel field tag:

```go
tagChannelAllowStranger uint8 = 6
```

Update `EncodeUpsertChannelCommand` size and field writes:

```go
// header + 1 string field + 5 int64 fields
size := headerSize +
    tlvOverhead + idLen +
    tlvOverhead + 8 +
    tlvOverhead + 8 +
    tlvOverhead + 8 +
    tlvOverhead + 8 +
    tlvOverhead + 8

// existing fields...
off = putInt64Field(buf, off, tagChannelSendBan, ch.SendBan)
_ = putInt64Field(buf, off, tagChannelAllowStranger, ch.AllowStranger)
```

Update channel command decode:

```go
case tagChannelAllowStranger:
    if len(value) != 8 {
        return nil, fmt.Errorf("%w: bad AllowStranger length", metadb.ErrCorruptValue)
    }
    ch.AllowStranger = int64(binary.BigEndian.Uint64(value))
```

In `pkg/slot/fsm/command_inspection.go`, include:

```go
"allow_stranger": channel.AllowStranger,
```

- [ ] **Step 4: Run FSM tests**

```bash
GOWORK=off go test ./pkg/slot/fsm -run 'EncodeDecodeChannelStatusFlags|DecodeCommandInspectionIncludesChannelStatusFlags' -count=1
GOWORK=off go test ./pkg/slot/fsm -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit FSM changes**

```bash
git add pkg/slot/fsm/command.go pkg/slot/fsm/command_inspection.go pkg/slot/fsm/command_inspection_test.go pkg/slot/fsm/state_machine_test.go
git commit -m "feat: include allow stranger in slot commands"
```

---

### Task 3: Propagate `AllowStranger` Through Slot Proxy Channel RPC

**Files:**
- Modify: `pkg/slot/proxy/channel_rpc.go`
- Test: `pkg/slot/proxy/binary_rpc_test.go`
- Test: `pkg/slot/proxy/integration_test.go`

- [ ] **Step 1: Write failing proxy codec tests**

Update `pkg/slot/proxy/binary_rpc_test.go`:

```go
func TestChannelRPCBinaryCodecRoundTripsStatusFlags(t *testing.T) {
    resp := channelRPCResponse{
        Status:   rpcStatusOK,
        LeaderID: 2,
        Channel:  &metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, AllowStranger: 1, SubscriberMutationVersion: 7},
    }
    body := encodeChannelRPCResponseBinary(resp)
    got, err := decodeChannelRPCResponseBinary(body)
    require.NoError(t, err)
    require.Equal(t, resp, got)
}
```

Update the scan-page codec test to include channels with `AllowStranger`:

```go
resp := channelRPCResponse{
    Status: rpcStatusOK,
    Channels: []metadb.Channel{
        {ChannelID: "a", ChannelType: 1, AllowStranger: 1},
        {ChannelID: "b", ChannelType: 2, Ban: 1, SendBan: 1, AllowStranger: 1, SubscriberMutationVersion: 9},
    },
    Cursor: metadb.ChannelCursor{ChannelID: "b", ChannelType: 2},
    Done: true,
}
body := encodeChannelRPCResponseBinary(resp)
got, err := decodeChannelRPCResponseBinary(body)
require.NoError(t, err)
require.Equal(t, resp, got)
```

Add a legacy response decoding test using the old response magic and old channel shape:

```go
func TestChannelRPCBinaryCodecDecodesV2ChannelWithoutAllowStranger(t *testing.T) {
    body := make([]byte, 0, 64)
    body = append(body, channelRPCResponseMagicV2[:]...)
    body = runtimeMetaAppendString(body, rpcStatusOK)
    body = runtimeMetaAppendUvarint(body, 0)
    body = append(body, 1)
    body = appendChannelV2(body, metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, SubscriberMutationVersion: 7})
    body = runtimeMetaAppendUvarint(body, 0)
    body = runtimeMetaAppendChannelCursor(body, metadb.ChannelCursor{})
    body = runtimeMetaAppendBool(body, true)

    got, err := decodeChannelRPCResponseBinary(body)
    require.NoError(t, err)
    require.NotNil(t, got.Channel)
    require.Equal(t, int64(0), got.Channel.AllowStranger)
}

func TestChannelRPCBinaryCodecDecodesV1ChannelWithoutAllowStranger(t *testing.T) {
    body := make([]byte, 0, 64)
    body = append(body, channelRPCResponseMagicV1[:]...)
    body = runtimeMetaAppendString(body, rpcStatusOK)
    body = runtimeMetaAppendUvarint(body, 0)
    body = append(body, 1)
    body = appendChannelV2(body, metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, SubscriberMutationVersion: 7})

    got, err := decodeChannelRPCResponseBinary(body)
    require.NoError(t, err)
    require.NotNil(t, got.Channel)
    require.Equal(t, int64(0), got.Channel.AllowStranger)
}
```

The helper names may differ after implementation; keep the test intent: old V1 and V2 response shapes decode with `AllowStranger=0`.

- [ ] **Step 2: Update authoritative integration test**

In `pkg/slot/proxy/integration_test.go`, update `TestStoreGetChannelForPermissionReadsAuthoritativeSlot`:

```go
ch := metadb.Channel{ChannelID: channelID, ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, AllowStranger: 1}
// existing create + GetChannelForPermission assertions
require.Equal(t, ch, got)
```

- [ ] **Step 3: Run tests to verify failure**

```bash
GOWORK=off go test ./pkg/slot/proxy -run 'ChannelRPCBinaryCodec|GetChannelForPermissionReadsAuthoritativeSlot' -count=1
```

Expected: FAIL because proxy binary channel responses do not carry `AllowStranger` yet.

- [ ] **Step 4: Implement proxy response codec versioning**

In `pkg/slot/proxy/channel_rpc.go`, keep the old V2 response magic and bump the current response magic:

```go
var (
    channelRPCRequestMagicV1  = [...]byte{'W', 'K', 'C', 'Q', 1}
    channelRPCRequestMagic    = [...]byte{'W', 'K', 'C', 'Q', 2}
    channelRPCResponseMagicV1 = [...]byte{'W', 'K', 'C', 'S', 1}
    channelRPCResponseMagicV2 = [...]byte{'W', 'K', 'C', 'S', 2}
    channelRPCResponseMagic   = [...]byte{'W', 'K', 'C', 'S', 3}
)
```

Leave request magic unchanged. Response V3 carries `AllowStranger`; response V2 decodes the previous shape.

Implement separate append/read helpers to avoid ambiguous optional trailing reads in channel lists:

```go
func appendChannel(dst []byte, ch metadb.Channel) []byte {
    dst = appendChannelV2(dst, ch)
    dst = runtimeMetaAppendVarint(dst, ch.AllowStranger)
    return dst
}

func appendChannelV2(dst []byte, ch metadb.Channel) []byte {
    dst = runtimeMetaAppendString(dst, ch.ChannelID)
    dst = runtimeMetaAppendVarint(dst, ch.ChannelType)
    dst = runtimeMetaAppendVarint(dst, ch.Ban)
    dst = runtimeMetaAppendVarint(dst, ch.Disband)
    dst = runtimeMetaAppendVarint(dst, ch.SendBan)
    dst = runtimeMetaAppendUvarint(dst, ch.SubscriberMutationVersion)
    return dst
}
```

Add V2 readers that default `AllowStranger` to zero:

```go
func readChannelV2(body []byte, offset int) (metadb.Channel, int, error) {
    // same as old readChannel: read ChannelID, ChannelType, Ban, Disband, SendBan, SubscriberMutationVersion
}

func readChannel(body []byte, offset int) (metadb.Channel, int, error) {
    ch, offset, err := readChannelV2(body, offset)
    if err != nil {
        return metadb.Channel{}, offset, err
    }
    if ch.AllowStranger, offset, err = runtimeMetaReadVarint(body, offset); err != nil {
        return metadb.Channel{}, offset, err
    }
    return ch, offset, nil
}
```

Add `readChannelPtrV2` and `readChannelsV2`. Use V2 readers from both `decodeChannelRPCResponseV1` and the new `decodeChannelRPCResponseV2`; neither legacy decoder may call the V3 `readChannel` helper. `decodeChannelRPCResponseBinary` should check in this order:

```go
if runtimeMetaHasMagic(body, channelRPCResponseMagicV1[:]) {
    return decodeChannelRPCResponseV1(body)
}
if runtimeMetaHasMagic(body, channelRPCResponseMagicV2[:]) {
    return decodeChannelRPCResponseV2(body)
}
if !runtimeMetaHasMagic(body, channelRPCResponseMagic[:]) {
    return channelRPCResponse{}, fmt.Errorf("metastore: invalid channel response codec")
}
// existing V3 decode path using readChannel/readChannels
```

Do not make `readChannel` guess whether the final field exists; channel scan-page entries are unframed.

- [ ] **Step 5: Run proxy tests**

```bash
GOWORK=off go test ./pkg/slot/proxy -run 'ChannelRPCBinaryCodec|GetChannelForPermissionReadsAuthoritativeSlot' -count=1
GOWORK=off go test ./pkg/slot/proxy -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit proxy changes**

```bash
git add pkg/slot/proxy/channel_rpc.go pkg/slot/proxy/binary_rpc_test.go pkg/slot/proxy/integration_test.go
git commit -m "feat: carry allow stranger in channel rpc"
```

---

### Task 4: Persist `allow_stranger` From Channel Usecase/API

**Files:**
- Modify: `internal/usecase/channel/types.go`
- Modify: `internal/usecase/channel/app.go`
- Test: `internal/usecase/channel/app_test.go`
- Test: `internal/access/api/channel_management_test.go`

- [ ] **Step 1: Write failing channel-usecase test**

Update `internal/usecase/channel/app_test.go`:

```go
func TestUpdateInfoPersistsStatusFlags(t *testing.T) {
    store := &recordingStore{}
    app := New(Options{Store: store})

    err := app.UpdateInfo(context.Background(), Info{ChannelID: "g1", ChannelType: 2, Ban: true, Disband: true, SendBan: true, AllowStranger: true})
    require.NoError(t, err)
    require.Len(t, store.upsertChannels, 1)
    require.Equal(t, metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, AllowStranger: 1}, store.upsertChannels[0])
}
```

`internal/access/api/channel_management_test.go` already verifies the legacy HTTP request maps `allow_stranger` into `channelusecase.Info`; keep it unchanged unless compilation requires formatting adjustments.

- [ ] **Step 2: Run tests to verify failure**

```bash
GOWORK=off go test ./internal/usecase/channel ./internal/access/api -run 'UpdateInfoPersistsStatusFlags|ChannelCreateMapsLegacyRequestToUsecaseCommand' -count=1
```

Expected: FAIL because `UpdateInfo` does not persist `AllowStranger` yet.

- [ ] **Step 3: Implement channel usecase mapping**

In `internal/usecase/channel/app.go`:

```go
return a.store.UpsertChannel(ctx, metadb.Channel{
    ChannelID:     info.ChannelID,
    ChannelType:   int64(info.ChannelType),
    Ban:           boolToInt64(info.Ban),
    Disband:       boolToInt64(info.Disband),
    SendBan:       boolToInt64(info.SendBan),
    AllowStranger: boolToInt64(info.AllowStranger),
})
```

In `internal/usecase/channel/types.go`, update the comment:

```go
// AllowStranger permits stranger sends to person channels when personal whitelist enforcement is enabled.
AllowStranger bool
```

- [ ] **Step 4: Run channel/API tests**

```bash
GOWORK=off go test ./internal/usecase/channel ./internal/access/api -run 'UpdateInfoPersistsStatusFlags|ChannelCreateMapsLegacyRequestToUsecaseCommand' -count=1
GOWORK=off go test ./internal/usecase/channel ./internal/access/api -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit channel mapping changes**

```bash
git add internal/usecase/channel/types.go internal/usecase/channel/app.go internal/usecase/channel/app_test.go internal/access/api/channel_management_test.go
git commit -m "feat: persist allow stranger channel info"
```

---

### Task 5: Enforce `AllowStranger` In Person Send Permission

**Files:**
- Modify: `internal/usecase/message/permission.go`
- Modify: `internal/usecase/message/send.go`
- Test: `internal/usecase/message/send_test.go`

- [ ] **Step 1: Extend the fake permission store for error injection**

In `internal/usecase/message/send_test.go`, extend `fakePermissionStore`:

```go
type fakePermissionStore struct {
    channels    map[string]metadb.Channel
    channelErrs map[string]error
    members     map[string]map[string]bool
    hasAny      map[string]bool
}

func newFakePermissionStore() *fakePermissionStore {
    return &fakePermissionStore{
        channels:    make(map[string]metadb.Channel),
        channelErrs: make(map[string]error),
        members:     make(map[string]map[string]bool),
        hasAny:      make(map[string]bool),
    }
}

func (s *fakePermissionStore) GetChannelForPermission(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
    key := permissionKey(channelID, channelType)
    if err, ok := s.channelErrs[key]; ok {
        return metadb.Channel{}, err
    }
    ch, ok := s.channels[key]
    if !ok {
        return metadb.Channel{}, metadb.ErrNotFound
    }
    return ch, nil
}
```

- [ ] **Step 2: Write failing message permission tests**

Add tests near the existing person-whitelist tests in `internal/usecase/message/send_test.go`:

```go
func TestSendAllowsPersonStrangerWhenReceiverAllowsStrangerAndWhitelistEnabled(t *testing.T) {
    cluster := &fakeChannelCluster{sendReplies: []fakeChannelClusterSendReply{{result: channel.AppendResult{MessageID: 710, MessageSeq: 40}}}}
    permissions := newFakePermissionStore()
    permissions.channels[permissionKey("u2", int64(frame.ChannelTypePerson))] = metadb.Channel{ChannelID: "u2", ChannelType: int64(frame.ChannelTypePerson), AllowStranger: 1}
    app := New(Options{Now: fixedNowFn, Cluster: cluster, MetaRefresher: &fakeMetaRefresher{}, PermissionStore: permissions, PersonWhitelistEnabled: true})

    result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("hi")})

    require.NoError(t, err)
    require.Equal(t, frame.ReasonSuccess, result.Reason)
    require.Equal(t, int64(710), result.MessageID)
    require.Equal(t, uint64(40), result.MessageSeq)
    require.Len(t, cluster.sendRequests, 1)
}

func TestSendRejectsPersonDenylistBeforeAllowStranger(t *testing.T) {
    cluster := &fakeChannelCluster{}
    permissions := newFakePermissionStore()
    permissions.channels[permissionKey("u2", int64(frame.ChannelTypePerson))] = metadb.Channel{ChannelID: "u2", ChannelType: int64(frame.ChannelTypePerson), AllowStranger: 1}
    denyID := channelmembers.DenylistChannelID(channelmembers.ChannelKey{ChannelID: "u2", ChannelType: frame.ChannelTypePerson})
    permissions.members[permissionKey(denyID, int64(frame.ChannelTypePerson))] = map[string]bool{"u1": true}
    app := New(Options{Now: fixedNowFn, Cluster: cluster, MetaRefresher: &fakeMetaRefresher{}, PermissionStore: permissions, PersonWhitelistEnabled: true})

    result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("hi")})

    require.NoError(t, err)
    require.Equal(t, frame.ReasonInBlacklist, result.Reason)
    require.Empty(t, cluster.sendRequests)
}
```

Add sender `SendBan`, metadata missing, default-open, and store-error tests. Also keep the existing `TestSendRejectsThirdPartyPrecomposedPersonChannel` assertion that invalid person-channel errors return `SendResult{}`; it protects the new `send.go` error-result guard from turning no-reason errors into `ReasonUnknown` results:

```go
func TestSendRejectsSenderSendBanBeforeReceiverAllowStranger(t *testing.T) {
    permissions := newFakePermissionStore()
    permissions.channels[permissionKey("u1", int64(frame.ChannelTypePerson))] = metadb.Channel{ChannelID: "u1", ChannelType: int64(frame.ChannelTypePerson), SendBan: 1}
    permissions.channels[permissionKey("u2", int64(frame.ChannelTypePerson))] = metadb.Channel{ChannelID: "u2", ChannelType: int64(frame.ChannelTypePerson), AllowStranger: 1}
    cluster := &fakeChannelCluster{}
    app := New(Options{Now: fixedNowFn, Cluster: cluster, MetaRefresher: &fakeMetaRefresher{}, PermissionStore: permissions, PersonWhitelistEnabled: true})

    result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("hi")})

    require.NoError(t, err)
    require.Equal(t, frame.ReasonSendBan, result.Reason)
    require.Empty(t, cluster.sendRequests)
}

func TestSendRejectsPersonStrangerWhenWhitelistEnabledAndReceiverMetadataMissing(t *testing.T) {
    permissions := newFakePermissionStore()
    cluster := &fakeChannelCluster{}
    app := New(Options{Now: fixedNowFn, Cluster: cluster, MetaRefresher: &fakeMetaRefresher{}, PermissionStore: permissions, PersonWhitelistEnabled: true})

    result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("hi")})

    require.NoError(t, err)
    require.Equal(t, frame.ReasonNotInWhitelist, result.Reason)
    require.Empty(t, cluster.sendRequests)
}

func TestSendAllowsPersonWhenWhitelistDisabledEvenReceiverDoesNotAllowStranger(t *testing.T) {
    permissions := newFakePermissionStore()
    permissions.channels[permissionKey("u2", int64(frame.ChannelTypePerson))] = metadb.Channel{ChannelID: "u2", ChannelType: int64(frame.ChannelTypePerson), AllowStranger: 0}
    cluster := &fakeChannelCluster{sendReplies: []fakeChannelClusterSendReply{{result: channel.AppendResult{MessageID: 711, MessageSeq: 41}}}}
    app := New(Options{Now: fixedNowFn, Cluster: cluster, MetaRefresher: &fakeMetaRefresher{}, PermissionStore: permissions})

    result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("hi")})

    require.NoError(t, err)
    require.Equal(t, frame.ReasonSuccess, result.Reason)
    require.Len(t, cluster.sendRequests, 1)
}

func TestSendReturnsSystemErrorWhenReceiverAllowStrangerLookupFails(t *testing.T) {
    permissions := newFakePermissionStore()
    permissions.channelErrs[permissionKey("u2", int64(frame.ChannelTypePerson))] = errors.New("receiver metadata failed")
    cluster := &fakeChannelCluster{}
    app := New(Options{Now: fixedNowFn, Cluster: cluster, MetaRefresher: &fakeMetaRefresher{}, PermissionStore: permissions, PersonWhitelistEnabled: true})

    result, err := app.Send(context.Background(), SendCommand{FromUID: "u1", ChannelID: "u2", ChannelType: frame.ChannelTypePerson, Payload: []byte("hi")})

    require.Error(t, err)
    require.Equal(t, frame.ReasonSystemError, result.Reason)
    require.Empty(t, cluster.sendRequests)
}
```

Ensure imports still include `errors` and `channelmembers`; they are already used in this test file today.

- [ ] **Step 3: Run tests to verify failure**

```bash
GOWORK=off go test ./internal/usecase/message -run 'AllowStranger|ReceiverMetadataMissing|PersonStranger|SenderSendBanBeforeReceiver' -count=1
```

Expected: FAIL because non-allowlisted strangers are still rejected even when receiver `AllowStranger=1`.

- [ ] **Step 4: Implement message permission behavior**

In `internal/usecase/message/send.go`, preserve non-success permission reasons when a permission check returns an infrastructure error:

```go
reason, err := a.checkSendPermission(ctx, cmd)
if err != nil {
    if reason != 0 && reason != frame.ReasonSuccess {
        return SendResult{Reason: reason}, err
    }
    return SendResult{}, err
}
```

This keeps invalid-channel errors that return zero/no reason unchanged as `SendResult{}` while making store failures surface `ReasonSystemError` as required by the spec.

In `internal/usecase/message/permission.go`, keep the existing order through denylist. Then replace the final personal allowlist block with:

```go
allowed, err := a.permissions.ContainsChannelSubscriber(ctx, channelmembers.AllowlistChannelID(key), int64(frame.ChannelTypePerson), cmd.FromUID)
if err != nil {
    return frame.ReasonSystemError, err
}
if allowed {
    return frame.ReasonSuccess, nil
}

ch, err := a.permissions.GetChannelForPermission(ctx, receiver, int64(frame.ChannelTypePerson))
if errors.Is(err, metadb.ErrNotFound) {
    return frame.ReasonNotInWhitelist, nil
}
if err != nil {
    return frame.ReasonSystemError, err
}
if ch.AllowStranger != 0 {
    return frame.ReasonSuccess, nil
}
return frame.ReasonNotInWhitelist, nil
```

Do not move this lookup before denylist or before the `PersonWhitelistEnabled=false` early return.

- [ ] **Step 5: Run message tests**

```bash
GOWORK=off go test ./internal/usecase/message -run 'AllowStranger|ReceiverMetadataMissing|PersonStranger|SenderSendBanBeforeReceiver|PersonWhitelist|ThirdPartyPrecomposedPersonChannel' -count=1
GOWORK=off go test ./internal/usecase/message -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit message permission changes**

```bash
git add internal/usecase/message/permission.go internal/usecase/message/send.go internal/usecase/message/send_test.go
git commit -m "feat: allow person strangers via receiver flag"
```

---

### Task 6: Update Documentation And Run Final Verification

**Files:**
- Modify: `docs/raw/send-path-business-logic-diff.md`
- Modify: `pkg/slot/FLOW.md`
- Modify: `internal/FLOW.md` only if existing text becomes stale

- [ ] **Step 1: Update raw send-path diff documentation**

In `docs/raw/send-path-business-logic-diff.md`, replace the remaining `AllowStranger` unresolved references with a dated restored note. Keep the text concise. Include this precedence:

```markdown
P2e 状态（2026-05-14）：当前项目已恢复 `AllowStranger` 的个人频道发送语义。`allow_stranger` 通过频道 API 持久化到 slot channel metadata，并通过 slot FSM / proxy 权限读取链路传播。个人发送权限中，发送者 `SendBan` 与接收方 denylist 仍优先；只有 `WK_MESSAGE_PERSON_WHITELIST_ENABLED=true` 且发送者不在接收方 allowlist 时，才读取接收方个人频道 `AllowStranger`，非零则允许陌生人发送，否则返回 `ReasonNotInWhitelist`。默认未开启个人白名单时，`AllowStranger=0` 不会新增拒绝路径。
```

Do not remove unrelated unresolved items like sendbatch, `expire`, plugin/webhook/AI, or temp-channel gaps.

- [ ] **Step 2: Update FLOW docs if stale**

In `pkg/slot/FLOW.md`, update channel metadata descriptions such as:

```markdown
| `User` / `Channel` / `Device` | meta/*.go | 业务数据模型；`Channel` 现在持久化 `Ban` / `Disband` / `SendBan` / `AllowStranger` / `SubscriberMutationVersion` |
```

And permission RPC descriptions such as:

```markdown
| `channelRPCServiceID` | 12 | Channel 权限元数据查询与物理 Slot 权威分页扫描（Ban / Disband / SendBan / AllowStranger / SubscriberMutationVersion） | proxy/channel_rpc.go |
```

Read `internal/FLOW.md`. If it lists person permission steps or persisted channel flags, update it to include the `AllowStranger` check. If it does not contain stale detail, leave it unchanged.

- [ ] **Step 3: Run focused verification**

```bash
GOWORK=off go test ./pkg/slot/meta ./pkg/slot/fsm ./pkg/slot/proxy ./internal/usecase/channel ./internal/access/api ./internal/usecase/message -count=1
```

Expected: PASS.

- [ ] **Step 4: Run boundary/import verification if available**

```bash
GOWORK=off go test ./internal -run TestInternalImportBoundaries -count=1
```

Expected: PASS, or `testing: warning: no tests to run` with package success.

- [ ] **Step 5: Run full unit test suite**

```bash
GOWORK=off go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit docs and verification-ready state**

```bash
git add docs/raw/send-path-business-logic-diff.md pkg/slot/FLOW.md internal/FLOW.md
git commit -m "docs: mark allow stranger send restored"
```

If `internal/FLOW.md` is unchanged, omit it from `git add`.

- [ ] **Step 7: Inspect final branch state**

```bash
git status --short
git log --oneline --decorate -n 8
```

Expected: clean worktree except ignored/unrelated user files, with the task commits present on `feature/send-allow-stranger`.

---

## Final Handoff

When all tasks pass:

1. Summarize the commits and verification commands.
2. Mention any unrelated user files left untouched.
3. Ask whether to merge `feature/send-allow-stranger` back to `main`.
