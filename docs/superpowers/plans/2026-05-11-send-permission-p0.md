# Send Permission P0 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore P0 send-before-append permissions for current person/group send paths: group Ban/member/deny/allow checks, person receiver denylist checks, and system UID bypass.

**Architecture:** Keep access layers thin and place business authorization in `internal/usecase/message` before durable append. Add authoritative slot-store point lookups so the send hot path does not page-scan subscribers/member lists. Keep `pkg/channel` append logic unchanged because it owns channel log consistency, not business permissions.

**Tech Stack:** Go, existing slot meta Pebble store, slot proxy authoritative RPC, existing `frame.ReasonCode`, TDD with `go test`.

---

## Reference Docs

- Difference analysis: `docs/raw/send-path-business-logic-diff.md`
- Internal layering: `internal/FLOW.md`
- Slot store flow: `pkg/slot/FLOW.md`
- Channel log flow: `pkg/channel/FLOW.md`

## Scope

Implement now:

- Group sends:
  - channel `Ban` -> `frame.ReasonBan`
  - sender in denylist -> `frame.ReasonInBlacklist`
  - sender not ordinary subscriber -> `frame.ReasonSubscriberNotExist`
  - allowlist exists and sender not in allowlist -> `frame.ReasonNotInWhitelist`
- Person sends:
  - receiver denylist contains sender -> `frame.ReasonInBlacklist`
  - person allowlist remains disabled by default, matching old `WhitelistOffOfPerson=true`
- System UID bypass:
  - if sender is a system UID, skip P0 permission checks
- Permission denials return `SendResult{Reason: ...}, nil` so gateway/API can return normal sendack/JSON reason without treating it as an infrastructure error.

Do not implement now:

- `Disband`, `SendBan`, `AllowStranger`, `Large` schema fields
- `NoPersist` non-durable branch
- `SyncOnce` cmd channel conversion
- request-scoped `subscribers`
- `/message/sendbatch`
- plugin/webhook/AI hooks
- agent/customer/visitors/info send support

## Files

Create:

- `internal/contracts/channelmembers/listid.go` — shared internal allowlist/denylist channel ID derivation.
- `internal/contracts/channelmembers/listid_test.go` — compatibility tests for list ID derivation.
- `internal/usecase/message/permission.go` — send permission checker and small interfaces.

Modify:

- `internal/usecase/channel/types.go` — delegate namespaced list ID generation to `internal/contracts/channelmembers`.
- `pkg/slot/meta/subscriber.go` — add point lookup helpers for subscriber existence and non-empty list existence.
- `pkg/slot/meta/subscriber_test.go` — add RED/GREEN tests for point lookups.
- `pkg/slot/proxy/store.go` — expose authoritative permission lookup methods used by message usecase.
- `pkg/slot/proxy/subscriber_rpc.go` — extend subscriber RPC for contains/has-any lookups.
- `pkg/slot/proxy/identity_subscriber_codec.go` — encode/decode new subscriber RPC request/response fields.
- `pkg/slot/proxy/binary_rpc_test.go` — codec coverage for new fields.
- `pkg/slot/proxy/integration_test.go` — authoritative lookup integration tests.
- `internal/usecase/message/app.go` — add `PermissionStore` and `SystemUIDs` options/fields.
- `internal/usecase/message/send.go` — call permission checker after channel normalization and before cluster/append.
- `internal/usecase/message/send_test.go` — P0 permission behavior tests.
- `internal/app/build.go` — wire app store and user system UID cache into message usecase.
- Any app/integration tests that send group messages without channel subscribers — seed channel metadata/subscribers or explicitly assert permission denial.

Do not modify:

- `pkg/channel/handler/append.go` — no business rules here.
- gateway frame mapping except tests if needed — sendack already preserves `SendResult.Reason`.

---

### Task 1: Extract Shared Member-List ID Helpers

**Files:**

- Create: `internal/contracts/channelmembers/listid.go`
- Create: `internal/contracts/channelmembers/listid_test.go`
- Modify: `internal/usecase/channel/types.go`

- [ ] **Step 1: Write failing tests for allow/deny list ID compatibility**

Add `internal/contracts/channelmembers/listid_test.go`:

```go
package channelmembers

import "testing"

func TestAllowDenyListChannelIDsMatchLegacyNamespace(t *testing.T) {
	key := ChannelKey{ChannelID: "g1", ChannelType: 2}
	if got, want := AllowlistChannelID(key), "__wk_internal_memberlist__/allow/2/ZzE"; got != want {
		t.Fatalf("AllowlistChannelID() = %q, want %q", got, want)
	}
	if got, want := DenylistChannelID(key), "__wk_internal_memberlist__/deny/2/ZzE"; got != want {
		t.Fatalf("DenylistChannelID() = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/contracts/channelmembers -run TestAllowDenyListChannelIDsMatchLegacyNamespace -count=1`

Expected: FAIL because package/functions do not exist.

- [ ] **Step 3: Implement shared helper**

Create `internal/contracts/channelmembers/listid.go`:

```go
package channelmembers

import (
	"encoding/base64"
	"fmt"
)

type ChannelKey struct {
	ChannelID   string
	ChannelType uint8
}

func AllowlistChannelID(key ChannelKey) string {
	return namespacedListChannelID("allow", key)
}

func DenylistChannelID(key ChannelKey) string {
	return namespacedListChannelID("deny", key)
}

func TempListChannelID(channelID string) string {
	return namespacedListChannelID("temp", ChannelKey{ChannelID: channelID, ChannelType: 8})
}

func namespacedListChannelID(kind string, key ChannelKey) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(key.ChannelID))
	return fmt.Sprintf("__wk_internal_memberlist__/%s/%d/%s", kind, key.ChannelType, encoded)
}
```

- [ ] **Step 4: Update channel usecase to delegate**

In `internal/usecase/channel/types.go`, import the contract package and replace `namespacedListChannelID` implementation:

```go
import channelmembers "github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"

func namespacedListChannelID(kind memberListKind, key ChannelKey) string {
	contractKey := channelmembers.ChannelKey{ChannelID: key.ChannelID, ChannelType: key.ChannelType}
	switch kind {
	case allowListKind:
		return channelmembers.AllowlistChannelID(contractKey)
	case denyListKind:
		return channelmembers.DenylistChannelID(contractKey)
	case tempListKind:
		return channelmembers.TempListChannelID(key.ChannelID)
	default:
		return ""
	}
}
```

Remove now-unused `encoding/base64` and `fmt` imports from `internal/usecase/channel/types.go`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/contracts/channelmembers ./internal/usecase/channel -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/contracts/channelmembers internal/usecase/channel/types.go
git commit -m "refactor: share channel member list identifiers"
```

---

### Task 2: Add Slot Meta Subscriber Point Lookups

**Files:**

- Modify: `pkg/slot/meta/subscriber.go`
- Modify: `pkg/slot/meta/subscriber_test.go`

- [ ] **Step 1: Write failing tests**

Add to `pkg/slot/meta/subscriber_test.go`:

```go
func TestShardContainsSubscriberUsesPrimaryKey(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(1)

	require.NoError(t, shard.AddSubscribers(ctx, "g1", 2, []string{"u1", "u2"}))

	ok, err := shard.ContainsSubscriber(ctx, "g1", 2, "u1")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = shard.ContainsSubscriber(ctx, "g1", 2, "missing")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestShardHasSubscribersDetectsNonEmptyChannelList(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	shard := db.ForSlot(1)

	ok, err := shard.HasSubscribers(ctx, "g1", 2)
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, shard.AddSubscribers(ctx, "g1", 2, []string{"u1"}))

	ok, err = shard.HasSubscribers(ctx, "g1", 2)
	require.NoError(t, err)
	require.True(t, ok)
}
```

- [ ] **Step 2: Run tests to verify RED**

Run: `go test ./pkg/slot/meta -run 'TestShard(ContainsSubscriber|HasSubscribers)' -count=1`

Expected: FAIL because `ContainsSubscriber` and `HasSubscribers` do not exist.

- [ ] **Step 3: Implement point lookups**

Add methods to `pkg/slot/meta/subscriber.go`:

```go
func (s *ShardStore) ContainsSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return false, err
	}
	if err := validateSubscriberChannel(channelID); err != nil {
		return false, err
	}
	if uid == "" {
		return false, ErrInvalidArgument
	}

	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	key := encodeSubscriberPrimaryKey(s.slot, channelID, channelType, uid, subscriberPrimaryFamilyID)
	return s.db.hasKey(key)
}

func (s *ShardStore) HasSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error) {
	if err := s.validate(); err != nil {
		return false, err
	}
	if err := s.db.checkContext(ctx); err != nil {
		return false, err
	}
	if err := validateSubscriberChannel(channelID); err != nil {
		return false, err
	}

	prefix := encodeSubscriberChannelPrefix(s.slot, channelID, channelType)
	s.db.mu.RLock()
	defer s.db.mu.RUnlock()

	iter, err := s.db.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: nextPrefix(prefix)})
	if err != nil {
		return false, err
	}
	defer iter.Close()
	ok := iter.First()
	if err := iter.Error(); err != nil {
		return false, err
	}
	return ok, nil
}
```

`subscriber.go` already imports `pebble`, so no new import should be needed.

- [ ] **Step 4: Run tests to verify GREEN**

Run: `go test ./pkg/slot/meta -run 'TestShard(ContainsSubscriber|HasSubscribers|AddAndPageChannelSubscribers|SnapshotChannelSubscribers)' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/slot/meta/subscriber.go pkg/slot/meta/subscriber_test.go
git commit -m "feat: add subscriber point lookups"
```

---

### Task 3: Add Authoritative Proxy Lookup Methods

**Files:**

- Modify: `pkg/slot/proxy/store.go`
- Modify: `pkg/slot/proxy/subscriber_rpc.go`
- Modify: `pkg/slot/proxy/identity_subscriber_codec.go`
- Modify: `pkg/slot/proxy/binary_rpc_test.go`
- Modify: `pkg/slot/proxy/integration_test.go`

- [ ] **Step 1: Write codec failing test for subscriber lookup fields**

Add to `pkg/slot/proxy/binary_rpc_test.go` near existing subscriber codec tests:

```go
func TestSubscriberRPCBinaryCodecRoundTripsPointLookupFields(t *testing.T) {
	req := subscriberRPCRequest{
		SlotID:      3,
		HashSlot:    7,
		ChannelID:   "g1",
		ChannelType: 2,
		ContainsUID: "u1",
		HasAny:      true,
	}
	body, err := encodeSubscriberRPCRequestBinary(req)
	require.NoError(t, err)
	got, err := decodeSubscriberRPCRequest(body)
	require.NoError(t, err)
	require.Equal(t, req, got)

	resp := subscriberRPCResponse{Status: rpcStatusOK, Contains: true, HasAny: true}
	body, err = encodeSubscriberRPCResponseBinary(resp)
	require.NoError(t, err)
	gotResp, err := decodeSubscriberRPCResponseBinary(body)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}
```

- [ ] **Step 2: Write integration failing tests**

Add to `pkg/slot/proxy/integration_test.go`:

```go
func TestStoreContainsChannelSubscriberReadsAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newProxyIntegrationStore(t)
	defer cleanup()

	channelID := "g-auth-contains"
	require.NoError(t, store.CreateChannel(ctx, channelID, 2))
	require.NoError(t, store.AddChannelSubscribers(ctx, channelID, 2, []string{"u1"}))

	ok, err := store.ContainsChannelSubscriber(ctx, channelID, 2, "u1")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = store.ContainsChannelSubscriber(ctx, channelID, 2, "missing")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestStoreHasChannelSubscribersReadsAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newProxyIntegrationStore(t)
	defer cleanup()

	channelID := "g-auth-has-any"
	ok, err := store.HasChannelSubscribers(ctx, channelID, 2)
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, store.CreateChannel(ctx, channelID, 2))
	require.NoError(t, store.AddChannelSubscribers(ctx, channelID, 2, []string{"u1"}))
	ok, err = store.HasChannelSubscribers(ctx, channelID, 2)
	require.NoError(t, err)
	require.True(t, ok)
}
```

Use the existing integration test harness names from nearby tests; if the helper is named differently, adapt only the setup, not the assertions.

- [ ] **Step 3: Run tests to verify RED**

Run: `go test ./pkg/slot/proxy -run 'TestSubscriberRPCBinaryCodecRoundTripsPointLookupFields|TestStore(ContainsChannelSubscriber|HasChannelSubscribers)' -count=1`

Expected: FAIL because request/response fields and store methods do not exist.

- [ ] **Step 4: Extend subscriber RPC structs**

In `pkg/slot/proxy/subscriber_rpc.go`, extend types:

```go
type subscriberRPCRequest struct {
	SlotID      uint64 `json:"slot_id"`
	HashSlot    uint16 `json:"hash_slot,omitempty"`
	ChannelID   string `json:"channel_id"`
	ChannelType int64  `json:"channel_type"`
	Snapshot    bool   `json:"snapshot,omitempty"`
	AfterUID    string `json:"after_uid,omitempty"`
	Limit       int    `json:"limit"`
	ContainsUID string `json:"contains_uid,omitempty"`
	HasAny      bool   `json:"has_any,omitempty"`
}

type subscriberRPCResponse struct {
	Status     string   `json:"status"`
	LeaderID   uint64   `json:"leader_id,omitempty"`
	UIDs       []string `json:"uids,omitempty"`
	NextCursor string   `json:"next_cursor,omitempty"`
	Done       bool     `json:"done"`
	Contains   bool     `json:"contains,omitempty"`
	HasAny     bool     `json:"has_any,omitempty"`
}
```

Add public methods on `Store`:

```go
func (s *Store) ContainsChannelSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (bool, error) {
	slotID := s.cluster.SlotForKey(channelID)
	return s.containsChannelSubscriberAuthoritative(ctx, slotID, channelID, channelType, uid)
}

func (s *Store) HasChannelSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error) {
	slotID := s.cluster.SlotForKey(channelID)
	return s.hasChannelSubscribersAuthoritative(ctx, slotID, channelID, channelType)
}
```

Implement helpers that use local DB when `shouldServeSlotLocally` is true and `callSubscriberRPC` otherwise.

- [ ] **Step 5: Update subscriber RPC handler**

In `handleSubscriberRPC`, before snapshot/page handling:

```go
if req.ContainsUID != "" {
	ok, err := s.db.ForHashSlot(hashSlot).ContainsSubscriber(ctx, req.ChannelID, req.ChannelType, req.ContainsUID)
	if err != nil {
		return nil, err
	}
	return encodeSubscriberRPCResponse(subscriberRPCResponse{Status: rpcStatusOK, Contains: ok})
}
if req.HasAny {
	ok, err := s.db.ForHashSlot(hashSlot).HasSubscribers(ctx, req.ChannelID, req.ChannelType)
	if err != nil {
		return nil, err
	}
	return encodeSubscriberRPCResponse(subscriberRPCResponse{Status: rpcStatusOK, HasAny: ok})
}
```

- [ ] **Step 6: Update binary codec**

In `pkg/slot/proxy/identity_subscriber_codec.go`, append fields to request encoding/decoding after `Limit`:

```go	dst = appendBinaryString(dst, req.ContainsUID)
dst = appendBinaryBool(dst, req.HasAny)
```

Decode in the same order. Append response fields after `Done`:

```go	dst = appendBinaryBool(dst, resp.Contains)
dst = appendBinaryBool(dst, resp.HasAny)
```

Decode in the same order.

If helper functions for bool are not present, follow existing marker style in nearby codecs or add small local helpers consistent with this file.

- [ ] **Step 7: Run proxy tests**

Run: `go test ./pkg/slot/proxy -run 'TestSubscriberRPCBinaryCodecRoundTripsPointLookupFields|TestStore(ContainsChannelSubscriber|HasChannelSubscribers|ListChannelSubscribers|SnapshotChannelSubscribers)' -count=1`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/slot/proxy/store.go pkg/slot/proxy/subscriber_rpc.go pkg/slot/proxy/identity_subscriber_codec.go pkg/slot/proxy/binary_rpc_test.go pkg/slot/proxy/integration_test.go
git commit -m "feat: add authoritative subscriber permission lookups"
```

---

### Task 4: Add Authoritative Channel Metadata Read for Permission

**Files:**

- Modify or create: `pkg/slot/proxy/channel_rpc.go`
- Modify: `pkg/slot/proxy/store.go`
- Modify: `pkg/slot/proxy/integration_test.go`

- [ ] **Step 1: Write failing integration test**

Add to `pkg/slot/proxy/integration_test.go`:

```go
func TestStoreGetChannelForPermissionReadsAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	store, cleanup := newProxyIntegrationStore(t)
	defer cleanup()

	channelID := "g-auth-channel"
	require.NoError(t, store.UpdateChannel(ctx, channelID, 2, 1))

	got, err := store.GetChannelForPermission(ctx, channelID, 2)
	require.NoError(t, err)
	require.Equal(t, channelID, got.ChannelID)
	require.Equal(t, int64(2), got.ChannelType)
	require.Equal(t, int64(1), got.Ban)
}
```

- [ ] **Step 2: Run test to verify RED**

Run: `go test ./pkg/slot/proxy -run TestStoreGetChannelForPermissionReadsAuthoritativeSlot -count=1`

Expected: FAIL because `GetChannelForPermission` does not exist.

- [ ] **Step 3: Implement authoritative channel metadata method**

Add `GetChannelForPermission` to `pkg/slot/proxy.Store` without changing existing local `GetChannel` semantics:

```go
func (s *Store) GetChannelForPermission(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	slotID := s.cluster.SlotForKey(channelID)
	hashSlot := hashSlotForKey(s.cluster, channelID)
	if s.shouldServeSlotLocally(slotID) {
		return s.db.ForHashSlot(hashSlot).GetChannel(ctx, channelID, channelType)
	}
	return s.getChannelForPermissionAuthoritative(ctx, slotID, hashSlot, channelID, channelType)
}
```

Implement `channel_rpc.go` using the same authoritative RPC pattern as `identity_rpc.go` and `runtime_meta_rpc.go`.

Use a new proxy service ID that does not collide with existing proxy IDs, for example:

```go
const channelRPCServiceID uint8 = 12
```

Register it in `proxy.New`:

```go
cluster.RPCMux().Handle(channelRPCServiceID, store.handleChannelRPC)
```

Response should carry `metadb.Channel` and support `not_found` via existing authoritative RPC status handling.

- [ ] **Step 4: Run test to verify GREEN**

Run: `go test ./pkg/slot/proxy -run TestStoreGetChannelForPermissionReadsAuthoritativeSlot -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/slot/proxy/store.go pkg/slot/proxy/channel_rpc.go pkg/slot/proxy/integration_test.go
git commit -m "feat: add authoritative channel permission reads"
```

---

### Task 5: Add Message Permission Checker Tests

**Files:**

- Create: `internal/usecase/message/permission.go`
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`

- [ ] **Step 1: Add fake permission store and system UID checker in tests**

Add to `internal/usecase/message/send_test.go` helper section:

```go
type fakePermissionStore struct {
	channels map[string]metadb.Channel
	members  map[string]map[string]bool
	hasAny   map[string]bool
}

func permissionKey(channelID string, channelType int64) string {
	return channelID + "#" + strconv.FormatInt(channelType, 10)
}

func (s *fakePermissionStore) GetChannelForPermission(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	ch, ok := s.channels[permissionKey(channelID, channelType)]
	if !ok {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return ch, nil
}

func (s *fakePermissionStore) ContainsChannelSubscriber(_ context.Context, channelID string, channelType int64, uid string) (bool, error) {
	return s.members[permissionKey(channelID, channelType)][uid], nil
}

func (s *fakePermissionStore) HasChannelSubscribers(_ context.Context, channelID string, channelType int64) (bool, error) {
	return s.hasAny[permissionKey(channelID, channelType)], nil
}

type fakeSystemUIDChecker map[string]bool

func (f fakeSystemUIDChecker) IsSystemUID(uid string) bool { return f[uid] }
```

Add `strconv` import.

- [ ] **Step 2: Write failing group permission tests**

Add tests:

```go
func TestSendRejectsBannedGroupBeforeDurableAppend(t *testing.T) { ... }
func TestSendRejectsGroupSenderInDenylistBeforeDurableAppend(t *testing.T) { ... }
func TestSendRejectsGroupSenderThatIsNotSubscriberBeforeDurableAppend(t *testing.T) { ... }
func TestSendRejectsGroupSenderNotInNonEmptyAllowlistBeforeDurableAppend(t *testing.T) { ... }
func TestSendAllowsGroupSenderWhenSubscriberAndAllowlistEmpty(t *testing.T) { ... }
```

Each denial test should assert:

```go
require.NoError(t, err)
require.Equal(t, frame.ReasonBan /* or expected reason */, result.Reason)
require.Empty(t, cluster.sendRequests)
```

The allow test should seed:

- channel `g1` with `Ban=0`
- ordinary subscriber list contains `u1`
- denylist does not contain `u1`
- allowlist has no members
- cluster append reply returns success

Then assert durable append happens once.

- [ ] **Step 3: Write failing person permission tests**

Add tests:

```go
func TestSendRejectsPersonSenderInReceiverDenylistBeforeDurableAppend(t *testing.T) { ... }
func TestSendAllowsPersonSenderWhenReceiverDenylistMisses(t *testing.T) { ... }
```

For receiver denylist, `FromUID=u1`, `ChannelID=u2`, normalized channel is `u2@u1`, receiver is `u2`, denylist source is receiver person channel key.

- [ ] **Step 4: Write failing system UID bypass test**

Add:

```go
func TestSendSystemUIDBypassesPermissionChecks(t *testing.T) { ... }
```

Seed a group channel where ordinary subscriber does not contain system UID and denylist contains system UID, but `SystemUIDs` reports true. Assert durable append still happens.

- [ ] **Step 5: Run message tests to verify RED**

Run: `go test ./internal/usecase/message -run 'TestSend(RejectsBannedGroup|RejectsGroupSender|AllowsGroupSender|RejectsPersonSender|AllowsPersonSender|SystemUIDBypasses)' -count=1`

Expected: FAIL because permission options/checker do not exist.

---

### Task 6: Implement Message Permission Checker

**Files:**

- Create: `internal/usecase/message/permission.go`
- Modify: `internal/usecase/message/app.go`
- Modify: `internal/usecase/message/send.go`
- Modify: `internal/usecase/message/send_test.go`

- [ ] **Step 1: Add message dependencies**

In `internal/usecase/message/app.go`, add:

```go
type PermissionStore interface {
	GetChannelForPermission(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error)
	ContainsChannelSubscriber(ctx context.Context, channelID string, channelType int64, uid string) (bool, error)
	HasChannelSubscribers(ctx context.Context, channelID string, channelType int64) (bool, error)
}

type SystemUIDChecker interface {
	IsSystemUID(uid string) bool
}
```

Add fields to `Options` and `App`:

```go
PermissionStore PermissionStore
SystemUIDs      SystemUIDChecker
```

```go
permissions PermissionStore
systemUIDs   SystemUIDChecker
```

Update `New` and `TestNewPreservesInjectedCollaborators`.

- [ ] **Step 2: Implement `permission.go`**

Create `internal/usecase/message/permission.go`:

```go
package message

import (
	"context"
	"errors"

	channelmembers "github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"
	runtimechannelid "github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
)

func (a *App) checkSendPermission(ctx context.Context, cmd SendCommand) (frame.ReasonCode, error) {
	if a == nil || a.permissions == nil {
		return frame.ReasonSuccess, nil
	}
	if a.systemUIDs != nil && a.systemUIDs.IsSystemUID(cmd.FromUID) {
		return frame.ReasonSuccess, nil
	}

	switch cmd.ChannelType {
	case frame.ChannelTypePerson:
		return a.checkPersonSendPermission(ctx, cmd)
	case frame.ChannelTypeGroup:
		return a.checkGroupSendPermission(ctx, cmd)
	default:
		return frame.ReasonSuccess, nil
	}
}

func (a *App) checkGroupSendPermission(ctx context.Context, cmd SendCommand) (frame.ReasonCode, error) {
	ch, err := a.permissions.GetChannelForPermission(ctx, cmd.ChannelID, int64(cmd.ChannelType))
	if errors.Is(err, metadb.ErrNotFound) {
		return frame.ReasonChannelNotExist, nil
	}
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if ch.Ban != 0 {
		return frame.ReasonBan, nil
	}

	key := channelmembers.ChannelKey{ChannelID: cmd.ChannelID, ChannelType: cmd.ChannelType}
	denied, err := a.permissions.ContainsChannelSubscriber(ctx, channelmembers.DenylistChannelID(key), int64(cmd.ChannelType), cmd.FromUID)
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if denied {
		return frame.ReasonInBlacklist, nil
	}

	subscriber, err := a.permissions.ContainsChannelSubscriber(ctx, cmd.ChannelID, int64(cmd.ChannelType), cmd.FromUID)
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if !subscriber {
		return frame.ReasonSubscriberNotExist, nil
	}

	allowID := channelmembers.AllowlistChannelID(key)
	hasAllowlist, err := a.permissions.HasChannelSubscribers(ctx, allowID, int64(cmd.ChannelType))
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if !hasAllowlist {
		return frame.ReasonSuccess, nil
	}
	allowed, err := a.permissions.ContainsChannelSubscriber(ctx, allowID, int64(cmd.ChannelType), cmd.FromUID)
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if !allowed {
		return frame.ReasonNotInWhitelist, nil
	}
	return frame.ReasonSuccess, nil
}

func (a *App) checkPersonSendPermission(ctx context.Context, cmd SendCommand) (frame.ReasonCode, error) {
	left, right, err := runtimechannelid.DecodePersonChannel(cmd.ChannelID)
	if err != nil {
		return 0, err
	}
	receiver := right
	if cmd.FromUID == right {
		receiver = left
	}
	key := channelmembers.ChannelKey{ChannelID: receiver, ChannelType: frame.ChannelTypePerson}
	denied, err := a.permissions.ContainsChannelSubscriber(ctx, channelmembers.DenylistChannelID(key), int64(frame.ChannelTypePerson), cmd.FromUID)
	if err != nil {
		return frame.ReasonSystemError, err
	}
	if denied {
		return frame.ReasonInBlacklist, nil
	}
	return frame.ReasonSuccess, nil
}
```

- [ ] **Step 3: Call checker before durable append**

In `internal/usecase/message/send.go`, after person normalization and before cluster required check:

```go
reason, err := a.checkSendPermission(ctx, cmd)
if err != nil {
	return SendResult{}, err
}
if reason != frame.ReasonSuccess {
	return SendResult{Reason: reason}, nil
}
```

- [ ] **Step 4: Run message tests**

Run: `go test ./internal/usecase/message -run 'TestSend|TestNewPreservesInjectedCollaborators' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/usecase/message/app.go internal/usecase/message/send.go internal/usecase/message/permission.go internal/usecase/message/send_test.go
git commit -m "feat: enforce p0 send permissions"
```

---

### Task 7: Wire App Dependencies and Fix Integration Expectations

**Files:**

- Modify: `internal/app/build.go`
- Modify: affected `internal/app/*test.go` files that now need group subscribers seeded.

- [ ] **Step 1: Wire dependencies**

In `internal/app/build.go`, change message app construction:

```go
app.messageApp = message.New(message.Options{
	IdentityStore:    app.store,
	ChannelStore:     app.store,
	PermissionStore:  app.store,
	SystemUIDs:       userApp,
	Cluster:          app.channelLog,
	// existing fields unchanged
})
```

- [ ] **Step 2: Run focused app tests to discover required fixture updates**

Run: `go test ./internal/app -run 'Test.*Group|Test.*Send|Test.*Message' -count=1`

Expected: Some tests may fail with permission reasons if they send group messages without seeding channel metadata/subscribers.

- [ ] **Step 3: Update fixtures, not production behavior**

For group send tests that should succeed, seed both channel metadata and ordinary subscriber membership before sending:

```go
require.NoError(t, app.Store().UpdateChannel(ctx, channelID, int64(frame.ChannelTypeGroup), 0))
require.NoError(t, app.Store().AddChannelSubscribers(ctx, channelID, int64(frame.ChannelTypeGroup), []string{senderUID}))
```

If the test has multiple group senders, add all sending UIDs. If a test is specifically about unauthorized sends, assert the expected `ReasonSubscriberNotExist` / `ReasonInBlacklist` instead.

- [ ] **Step 4: Run app tests again**

Run: `go test ./internal/app -run 'Test.*Group|Test.*Send|Test.*Message' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/app/build.go internal/app/*test.go
git commit -m "test: seed group send permissions in app fixtures"
```

---

### Task 8: Verify Access-Layer Behavior

**Files:**

- Modify only if failing: `internal/access/gateway/*_test.go`, `internal/access/api/*_test.go`

- [ ] **Step 1: Add/adjust gateway test if not already covered**

If no test verifies business denial reason sendack, add a gateway test with a fake `messages.Send` returning `SendResult{Reason: frame.ReasonInBlacklist}` and assert sendack reason is preserved. This should not require gateway code changes.

- [ ] **Step 2: Add/adjust API test if not already covered**

If no test verifies business denial reason JSON response, add an API test with fake message app returning `SendResult{Reason: frame.ReasonSubscriberNotExist}` and assert HTTP 200 with `reason` field. This should not require API code changes.

- [ ] **Step 3: Run access tests**

Run: `go test ./internal/access/gateway ./internal/access/api -count=1`

Expected: PASS.

- [ ] **Step 4: Commit if tests changed**

```bash
git add internal/access/gateway internal/access/api
git commit -m "test: cover send permission denial responses"
```

Skip commit if no files changed.

---

### Task 9: Final Verification

**Files:** all touched files.

- [ ] **Step 1: Run package-level verification**

Run:

```bash
go test ./internal/contracts/channelmembers ./internal/usecase/channel ./internal/usecase/message ./pkg/slot/meta ./pkg/slot/proxy ./internal/access/gateway ./internal/access/api ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 2: Run targeted internal/pkg verification**

Run:

```bash
go test ./internal/... ./pkg/slot/... -count=1
```

Expected: PASS. If this is too slow, report exact command duration and failing/skipped packages; do not claim full verification.

- [ ] **Step 3: Inspect diff**

Run:

```bash
git status --short
git diff --stat
git diff --check
```

Expected:

- Only planned files changed.
- `git diff --check` reports no whitespace errors.

- [ ] **Step 4: Update docs if behavior differs from `docs/raw/send-path-business-logic-diff.md`**

If implementation changes P0 scope or chooses different reason semantics, update:

- `docs/raw/send-path-business-logic-diff.md`

- [ ] **Step 5: Final commit if needed**

```bash
git add docs/raw/send-path-business-logic-diff.md
git commit -m "docs: align send permission analysis with p0 implementation"
```

Skip if no docs changed.

---

## Acceptance Criteria

- Group send is denied before durable append when channel is banned.
- Group send is denied before durable append when sender is in denylist.
- Group send is denied before durable append when sender is not a subscriber.
- Group send is denied before durable append when allowlist exists and sender is not in it.
- Group send succeeds when sender is a subscriber, not denied, and allowlist is empty.
- Person send is denied before durable append when receiver denylist contains sender.
- Person send succeeds when receiver denylist misses sender.
- System UID bypasses P0 permission checks.
- Permission denials return send result reasons, not infrastructure errors.
- `pkg/channel` append remains business-rule free.
- All targeted tests pass with fresh command output.
