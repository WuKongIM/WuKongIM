# Conversation Authority Cache Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make internalv2 conversation generation and listing authoritative on each user's UID authority node so successful `/conversation/list` reads include unflushed conversation cache rows from any API entry node.

**Architecture:** The conversation usecase continues to own list semantics, cursor handling, unread calculation, and last-message hydration. `internalv2/app` owns the UID authority cache runtime and committed-message admission. `internalv2/infra/cluster` resolves UID route targets and forwards authority operations through `internalv2/access/node` RPC when the UID authority is remote.

**Tech Stack:** Go, `internalv2/usecase/conversation`, `internalv2/app`, `internalv2/access/node`, `internalv2/infra/cluster`, `pkg/clusterv2`, `pkg/db/meta`, `go test`.

---

## Reference Spec

- `docs/superpowers/specs/2026-06-08-conversation-authority-cache-design.md`

## File Structure

- Modify `internalv2/usecase/conversation/types.go`: add `RouteTarget`, active patch DTOs, active view page DTOs, and typed errors.
- Modify `internalv2/usecase/conversation/app.go`: call `ListUserConversationActiveView` instead of DB-only `ListUserConversationActivePage`.
- Modify `internalv2/usecase/conversation/projector.go`: add patch projection while preserving the existing `HandleCommitted` compatibility path.
- Modify `internalv2/usecase/conversation/app_test.go` and `projector_test.go`: cover active view and patch projection.
- Modify `pkg/clusterv2/net/ids.go`: reserve `RPCConversationAuthority`.
- Modify `pkg/clusterv2/node_meta.go` and `node_meta_conversation_test.go`: expose UID-routed primary conversation row reads for cache delete-barrier overlay.
- Create `internalv2/app/conversation_authority.go`: local authority cache, active-view merge, admission, flush, and handoff state machine.
- Create `internalv2/app/conversation_authority_test.go`: cache coalesce, cursor, delete barrier, cache pressure, handoff, and timeout tests.
- Create `internalv2/access/node/conversation_codec.go`, `conversation_rpc.go`, and tests: deterministic node RPC codec/client/adapter for authority calls.
- Create `internalv2/infra/cluster/conversation_authority.go` and tests: route target resolution, local/remote authority client, exact-target grouping, retry, and handoff drain calls.
- Modify `internalv2/infra/cluster/conversation.go` and `conversation_projection.go`: adapt existing stores to new active view and active patch ports.
- Modify `internalv2/app/config.go`, `app.go`, and `conversation.go`: configure and wire the authority runtime in place of the current DB-flush-only projector.
- Modify `cmd/wukongimv2/config.go`, config tests, `wukongim.conf.example`, `cmd/wukongimv2/*.conf.example`, and `scripts/wukongimv2/*.conf`: add `WK_CONVERSATION_AUTHORITY_*` settings.
- Modify `internalv2/app/FLOW.md`, `internalv2/access/node/FLOW.md`, and `internalv2/infra/cluster/FLOW.md`: document the new authority cache flow.

## Task 1: Conversation Usecase Active View Contract

**Files:**
- Modify: `internalv2/usecase/conversation/types.go`
- Modify: `internalv2/usecase/conversation/app.go`
- Modify: `internalv2/usecase/conversation/projector.go`
- Test: `internalv2/usecase/conversation/app_test.go`
- Test: `internalv2/usecase/conversation/projector_test.go`

- [ ] **Step 1: Add failing active-view list test**

Add this test to `internalv2/usecase/conversation/app_test.go`:

```go
func TestListUsesAuthoritativeActiveView(t *testing.T) {
	store := &recordingActiveViewStore{
		rows: []metadb.UserConversationState{{
			UID:         "u1",
			ChannelID:   "cache-only",
			ChannelType: 2,
			ActiveAt:    500,
		}},
	}
	app := New(Options{Store: store, Messages: recordingLastMessageStore{}})
	got, err := app.List(context.Background(), ListRequest{UID: "u1", Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ChannelID != "cache-only" {
		t.Fatalf("items = %#v, want cache-only active view row", got.Items)
	}
	if len(store.calls) != 1 || store.calls[0].limit != 11 {
		t.Fatalf("active view calls = %#v, want limit+1", store.calls)
	}
}
```

Add the helper below the existing recording store in the same file:

```go
type activeViewCall struct {
	uid   string
	after metadb.UserConversationActiveCursor
	limit int
}

type recordingActiveViewStore struct {
	rows  []metadb.UserConversationState
	calls []activeViewCall
}

func (s *recordingActiveViewStore) ListUserConversationActiveView(_ context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) (ActiveViewPage, error) {
	s.calls = append(s.calls, activeViewCall{uid: uid, after: after, limit: limit})
	page := append([]metadb.UserConversationState(nil), s.rows...)
	done := len(page) <= limit
	if len(page) > limit {
		page = page[:limit]
	}
	var cursor metadb.UserConversationActiveCursor
	if len(page) > 0 {
		last := page[len(page)-1]
		cursor = metadb.UserConversationActiveCursor{ActiveAt: last.ActiveAt, ChannelID: last.ChannelID, ChannelType: last.ChannelType}
	}
	return ActiveViewPage{Rows: page, Cursor: cursor, Done: done}, nil
}
```

- [ ] **Step 2: Add failing projector patch test**

Add this test to `internalv2/usecase/conversation/projector_test.go`:

```go
func TestProjectorProjectActivePatchesCarriesMessageSeq(t *testing.T) {
	projector := NewProjector(ProjectorOptions{})
	patches, err := projector.ProjectActivePatches(context.Background(), messageevents.MessageCommitted{
		MessageSeq:        42,
		ChannelID:         "u1@u2",
		ChannelType:       testChannelTypePerson,
		FromUID:           "u1",
		ServerTimestampMS: 1000,
	})
	if err != nil {
		t.Fatalf("ProjectActivePatches() error = %v", err)
	}
	if len(patches) != 2 {
		t.Fatalf("patches len = %d, want 2", len(patches))
	}
	for _, patch := range patches {
		if patch.MessageSeq != 42 || patch.ActiveAt != 1000 || !patch.SparseActiveSet {
			t.Fatalf("patch = %#v, want message seq, active at, and sparse flag set", patch)
		}
	}
}
```

- [ ] **Step 3: Run the failing tests**

Run:

```bash
go test ./internalv2/usecase/conversation -run 'TestListUsesAuthoritativeActiveView|TestProjectorProjectActivePatchesCarriesMessageSeq' -count=1
```

Expected: FAIL because `ActiveViewPage`, `ListUserConversationActiveView`, and `ProjectActivePatches` do not exist yet.

- [ ] **Step 4: Add usecase DTOs and typed errors**

In `internalv2/usecase/conversation/types.go`, add:

```go
var (
	// ErrRouteNotReady indicates that the UID authority route cannot serve a request yet.
	ErrRouteNotReady = errors.New("internalv2/usecase/conversation: route not ready")
	// ErrStaleRoute indicates that a request was sent to an outdated UID authority target.
	ErrStaleRoute = errors.New("internalv2/usecase/conversation: stale route")
	// ErrNotLeader indicates that the target node is no longer the UID authority leader.
	ErrNotLeader = errors.New("internalv2/usecase/conversation: not leader")
	// ErrCachePressure indicates that authority cache pressure prevents a complete successful List.
	ErrCachePressure = errors.New("internalv2/usecase/conversation: authority cache pressure")
)

// RouteTarget identifies the fenced UID authority that should serve a conversation request.
type RouteTarget struct {
	// HashSlot is the logical UID hash slot.
	HashSlot uint16
	// SlotID is the physical Slot that owns HashSlot.
	SlotID uint32
	// LeaderNodeID is the authority leader node for this target.
	LeaderNodeID uint64
	// RouteRevision is the route-table revision used to resolve this target.
	RouteRevision uint64
	// AuthorityEpoch fences leadership changes for this hash slot.
	AuthorityEpoch uint64
}

// ActivePatch is an unflushed conversation activity candidate owned by a UID authority.
type ActivePatch struct {
	// UID identifies the user that owns the conversation row.
	UID string
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ReadSeq is the minimum read floor derived from membership visibility.
	ReadSeq uint64
	// DeletedToSeq is the minimum delete floor derived from membership visibility.
	DeletedToSeq uint64
	// ActiveAt is the candidate active-list ordering timestamp.
	ActiveAt int64
	// UpdatedAt records when the projection advanced this row in memory.
	UpdatedAt int64
	// SparseActive is the projected sparse-active mode.
	SparseActive bool
	// MessageSeq fences stale activity after user delete barriers.
	MessageSeq uint64
}

// ToMetaPatch converts the activity candidate to the durable DB patch command.
func (p ActivePatch) ToMetaPatch() metadb.UserConversationActivePatch {
	return metadb.UserConversationActivePatch{
		UID:             p.UID,
		ChannelID:       p.ChannelID,
		ChannelType:     p.ChannelType,
		ActiveAt:        p.ActiveAt,
		MessageSeq:      p.MessageSeq,
		SparseActive:    p.SparseActive,
		SparseActiveSet: true,
	}
}

// ActiveViewPage is one authoritative active-row page before last-message hydration.
type ActiveViewPage struct {
	// Rows contains DB rows merged with unflushed authority cache rows.
	Rows []metadb.UserConversationState
	// Cursor is the active index cursor after the last scanned row.
	Cursor metadb.UserConversationActiveCursor
	// Done reports that no further rows are available in the authoritative view.
	Done bool
}
```

- [ ] **Step 5: Change the active store contract**

In `internalv2/usecase/conversation/app.go`, replace the current `Store` interface with:

```go
// Store pages authoritative UID-owned conversation active rows.
type Store interface {
	ListUserConversationActiveView(ctx context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) (ActiveViewPage, error)
}
```

Then update `List`:

```go
page, err := a.store.ListUserConversationActiveView(ctx, req.UID, req.Cursor.toMeta(), limit+1)
if err != nil {
	return ListResult{}, err
}
rows := page.Rows
hasMore := !page.Done
if len(rows) > limit {
	hasMore = true
	rows = rows[:limit]
}
```

Keep the existing cursor calculation, replacing the old `cursor` variable with `page.Cursor`:

```go
result.NextCursor = cursorFromMeta(page.Cursor)
```

- [ ] **Step 6: Add patch projection while preserving committed handling**

In `internalv2/usecase/conversation/projector.go`, add:

```go
// ProjectActivePatches projects one durable commit into UID-owned active patches.
func (p *Projector) ProjectActivePatches(ctx context.Context, event messageevents.MessageCommitted) ([]ActivePatch, error) {
	states, err := p.projectStates(ctx, event)
	if err != nil || len(states) == 0 {
		return nil, err
	}
	patches := make([]ActivePatch, 0, len(states))
	for _, state := range states {
		patches = append(patches, ActivePatch{
			UID:          state.UID,
			ChannelID:    state.ChannelID,
			ChannelType:  state.ChannelType,
			ReadSeq:      state.ReadSeq,
			DeletedToSeq: state.DeletedToSeq,
			ActiveAt:     state.ActiveAt,
			UpdatedAt:    state.UpdatedAt,
			SparseActive: state.SparseActive,
			MessageSeq:   event.MessageSeq,
		})
	}
	return patches, nil
}

func (p *Projector) projectStates(ctx context.Context, event messageevents.MessageCommitted) ([]metadb.UserConversationState, error) {
	if p == nil {
		return nil, nil
	}
	if event.ChannelType == conversationChannelTypePerson {
		return p.personalStates(event)
	}
	return p.groupStates(ctx, event)
}
```

Change `HandleCommitted` to call `projectStates`:

```go
states, err := p.projectStates(ctx, event)
if err != nil || len(states) == 0 {
	return err
}
```

- [ ] **Step 7: Update existing tests for the new store contract**

In `internalv2/usecase/conversation/app_test.go`, rename the existing fake store method to the new method:

```go
func (s *recordingActiveStore) ListUserConversationActiveView(_ context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) (ActiveViewPage, error) {
	// Keep the existing paging body, then return ActiveViewPage{Rows: page, Cursor: cursor, Done: done}.
}
```

Use this exact return at the end of that helper:

```go
return ActiveViewPage{Rows: page, Cursor: cursor, Done: done}, nil
```

- [ ] **Step 8: Run usecase tests**

Run:

```bash
go test ./internalv2/usecase/conversation -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit Task 1**

```bash
git add internalv2/usecase/conversation/types.go internalv2/usecase/conversation/app.go internalv2/usecase/conversation/projector.go internalv2/usecase/conversation/app_test.go internalv2/usecase/conversation/projector_test.go
git commit -m "feat: add conversation active view contract"
```

## Task 2: clusterv2 Conversation Facades

**Files:**
- Modify: `pkg/clusterv2/node_meta.go`
- Modify: `pkg/clusterv2/node_meta_conversation_test.go`
- Modify: `internalv2/infra/cluster/conversation.go`
- Modify: `internalv2/infra/cluster/conversation_projection.go`
- Test: `internalv2/infra/cluster/conversation_test.go`

- [ ] **Step 1: Add failing clusterv2 primary row read test**

Add to `pkg/clusterv2/node_meta_conversation_test.go`:

```go
func TestClusterV2GetUserConversationStateUsesUIDHashSlot(t *testing.T) {
	node := newDefaultSingleNode(t)
	startNode(t, node)
	t.Cleanup(func() { stopNodes(t, node) })
	ctx := context.Background()
	uid := "u-primary"
	state := metadb.UserConversationState{UID: uid, ChannelID: "g1", ChannelType: 2, ActiveAt: 100}
	if err := node.UpsertUserConversationStatesBatch(ctx, []metadb.UserConversationState{state}); err != nil {
		t.Fatalf("UpsertUserConversationStatesBatch() error = %v", err)
	}
	got, ok, err := node.GetUserConversationState(ctx, uid, "g1", 2)
	if err != nil || !ok {
		t.Fatalf("GetUserConversationState() ok=%v err=%v, want ok", ok, err)
	}
	if got.UID != uid || got.ChannelID != "g1" || got.ActiveAt != 100 {
		t.Fatalf("state = %#v, want %#v", got, state)
	}
}
```

- [ ] **Step 2: Add failing infra active view wrapper test**

Add to `internalv2/infra/cluster/conversation_test.go`:

```go
func TestConversationStoreWrapsActivePageAsView(t *testing.T) {
	node := &conversationNodeFake{
		activeRows: []metadb.UserConversationState{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100}},
	}
	store := NewConversationStore(node)
	page, err := store.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g1" || page.Done {
		t.Fatalf("page = %#v, want one row and fake done=false", page)
	}
}
```

- [ ] **Step 3: Run failing facade tests**

Run:

```bash
go test ./pkg/clusterv2 -run TestClusterV2GetUserConversationStateUsesUIDHashSlot -count=1
go test ./internalv2/infra/cluster -run TestConversationStoreWrapsActivePageAsView -count=1
```

Expected: FAIL because the new facade and active view method are missing.

- [ ] **Step 4: Implement clusterv2 primary row facade**

In `pkg/clusterv2/node_meta.go`, add after `TouchUserConversationActiveAtBatch`:

```go
// GetUserConversationState reads one UID-owned conversation row from Slot metadata storage.
func (n *Node) GetUserConversationState(ctx context.Context, uid, channelID string, channelType int64) (metadb.UserConversationState, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return metadb.UserConversationState{}, false, err
	}
	if err := n.ensureForeground(); err != nil {
		return metadb.UserConversationState{}, false, err
	}
	if n.defaultSlotMetaDB == nil {
		return metadb.UserConversationState{}, false, ErrNotStarted
	}
	route, err := n.RouteKey(uid)
	if err != nil {
		return metadb.UserConversationState{}, false, err
	}
	state, err := n.defaultSlotMetaDB.ForHashSlot(route.HashSlot).GetUserConversationState(ctx, uid, channelID, channelType)
	if err != nil {
		if errors.Is(err, metadb.ErrNotFound) {
			return metadb.UserConversationState{}, false, nil
		}
		return metadb.UserConversationState{}, false, err
	}
	return state, true, nil
}
```

Add `errors` to the imports if it is not already present.

- [ ] **Step 5: Update infra cluster conversation interfaces**

In `internalv2/infra/cluster/conversation.go`, add active view support:

```go
var _ conversationusecase.Store = (*ConversationStore)(nil)

// ListUserConversationActiveView reads the authoritative active view from clusterv2.
func (s *ConversationStore) ListUserConversationActiveView(ctx context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) (conversationusecase.ActiveViewPage, error) {
	rows, cursor, done, err := s.ListUserConversationActivePage(ctx, uid, after, limit)
	if err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	return conversationusecase.ActiveViewPage{Rows: rows, Cursor: cursor, Done: done}, nil
}
```

In `internalv2/infra/cluster/conversation_projection.go`, extend `ConversationProjectionNode`:

```go
TouchUserConversationActiveAtBatch(context.Context, []metadb.UserConversationActivePatch) error
```

Add:

```go
// TouchUserConversationActiveAtBatch persists UID-owned active-at patches.
func (s *ConversationProjectionStore) TouchUserConversationActiveAtBatch(ctx context.Context, patches []metadb.UserConversationActivePatch) error {
	if s == nil || s.node == nil || len(patches) == 0 {
		return nil
	}
	return s.node.TouchUserConversationActiveAtBatch(ctx, append([]metadb.UserConversationActivePatch(nil), patches...))
}
```

- [ ] **Step 6: Run facade tests**

Run:

```bash
go test ./pkg/clusterv2 -run 'TestClusterV2(GetUserConversationStateUsesUIDHashSlot|TouchUserConversationActiveAtBatchRoutesByUID|ListUserConversationActivePageUsesUIDHashSlot)' -count=1
go test ./internalv2/infra/cluster -run 'TestConversationStore|TestConversationProjection' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 2**

```bash
git add pkg/clusterv2/node_meta.go pkg/clusterv2/node_meta_conversation_test.go internalv2/infra/cluster/conversation.go internalv2/infra/cluster/conversation_projection.go internalv2/infra/cluster/conversation_test.go
git commit -m "feat: expose conversation active facades"
```

## Task 3: Local Authority Cache Runtime

**Files:**
- Create: `internalv2/app/conversation_authority.go`
- Create: `internalv2/app/conversation_authority_test.go`

- [ ] **Step 1: Add failing cache coalesce and list tests**

Create `internalv2/app/conversation_authority_test.go` with:

```go
package app

import (
	"context"
	"errors"
	"testing"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestConversationAuthorityListMergesCacheAndDB(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		activeRows: []metadb.UserConversationState{{UID: "u1", ChannelID: "db", ChannelType: 2, ActiveAt: 100}},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:          1,
		Store:                store,
		MaxRowsPerUID:        10,
		MaxRows:              100,
		ListDBWindowMax:      20,
		AdmissionBatchRows:   10,
		AdmissionConcurrency: 1,
	})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "cache", ChannelType: 2, ActiveAt: 300, UpdatedAt: 300, MessageSeq: 9,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 2 || page.Rows[0].ChannelID != "cache" || page.Rows[1].ChannelID != "db" {
		t.Fatalf("rows = %#v, want cache before db", page.Rows)
	}
}

func TestConversationAuthorityCachePressureFailsList(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{
		LocalNodeID:     1,
		Store:           &recordingConversationAuthorityStore{},
		MaxRowsPerUID:   1,
		MaxRows:         1,
		ListDBWindowMax: 1,
	})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{
		{UID: "u1", ChannelID: "a", ChannelType: 2, ActiveAt: 300, MessageSeq: 3},
		{UID: "u1", ChannelID: "b", ChannelType: 2, ActiveAt: 200, MessageSeq: 2},
	})
	if !errors.Is(err, conversationusecase.ErrCachePressure) {
		t.Fatalf("AdmitPatches() error = %v, want ErrCachePressure", err)
	}
}
```

Add helper fakes in the same file:

```go
type recordingConversationAuthorityStore struct {
	activeRows []metadb.UserConversationState
	primary   map[metadb.ConversationKey]metadb.UserConversationState
	touched   []metadb.UserConversationActivePatch
}

func (s *recordingConversationAuthorityStore) ListUserConversationActivePage(_ context.Context, _ string, _ metadb.UserConversationActiveCursor, _ int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error) {
	rows := append([]metadb.UserConversationState(nil), s.activeRows...)
	var cursor metadb.UserConversationActiveCursor
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		cursor = metadb.UserConversationActiveCursor{ActiveAt: last.ActiveAt, ChannelID: last.ChannelID, ChannelType: last.ChannelType}
	}
	return rows, cursor, true, nil
}

func (s *recordingConversationAuthorityStore) GetUserConversationState(_ context.Context, _ string, channelID string, channelType int64) (metadb.UserConversationState, bool, error) {
	if s.primary == nil {
		return metadb.UserConversationState{}, false, nil
	}
	row, ok := s.primary[metadb.ConversationKey{ChannelID: channelID, ChannelType: channelType}]
	return row, ok, nil
}

func (s *recordingConversationAuthorityStore) TouchUserConversationActiveAtBatch(_ context.Context, patches []metadb.UserConversationActivePatch) error {
	s.touched = append(s.touched, patches...)
	return nil
}
```

- [ ] **Step 2: Run failing cache tests**

Run:

```bash
go test ./internalv2/app -run 'TestConversationAuthority(ListMergesCacheAndDB|CachePressureFailsList)' -count=1
```

Expected: FAIL because the authority runtime does not exist.

- [ ] **Step 3: Implement authority cache skeleton**

Create `internalv2/app/conversation_authority.go` with:

```go
package app

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type conversationAuthorityStore interface {
	ListUserConversationActivePage(context.Context, string, metadb.UserConversationActiveCursor, int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error)
	GetUserConversationState(context.Context, string, string, int64) (metadb.UserConversationState, bool, error)
	TouchUserConversationActiveAtBatch(context.Context, []metadb.UserConversationActivePatch) error
}

type conversationAuthorityOptions struct {
	LocalNodeID          uint64
	Store                conversationAuthorityStore
	MaxRowsPerUID        int
	MaxRows              int
	ListDBWindowMax      int
	AdmissionBatchRows   int
	AdmissionConcurrency int
	HandoffTimeout       time.Duration
}

type conversationAuthority struct {
	mu           sync.Mutex
	localNodeID  uint64
	store        conversationAuthorityStore
	maxRowsByUID int
	maxRows      int
	listWindow   int
	totalRows    int
	byUID        map[string]map[conversationAuthorityKey]conversationAuthorityEntry
	targets      map[conversationAuthorityTargetKey]conversationAuthorityState
}
```

Also add key/entry/state types:

```go
type conversationAuthorityKey struct {
	channelID   string
	channelType int64
}

type conversationAuthorityEntry struct {
	patch conversationusecase.ActivePatch
}

type conversationAuthorityTargetKey struct {
	hashSlot       uint16
	slotID         uint32
	leaderNodeID   uint64
	routeRevision  uint64
	authorityEpoch uint64
}

type conversationAuthorityState string

const (
	conversationAuthorityActive   conversationAuthorityState = "active"
	conversationAuthorityWarming  conversationAuthorityState = "warming"
	conversationAuthorityDraining conversationAuthorityState = "draining"
)
```

- [ ] **Step 4: Implement admission and local target validation**

Add:

```go
func newConversationAuthority(opts conversationAuthorityOptions) *conversationAuthority {
	if opts.MaxRowsPerUID <= 0 {
		opts.MaxRowsPerUID = 4096
	}
	if opts.MaxRows <= 0 {
		opts.MaxRows = 100000
	}
	if opts.ListDBWindowMax <= 0 {
		opts.ListDBWindowMax = 1000
	}
	return &conversationAuthority{
		localNodeID:  opts.LocalNodeID,
		store:        opts.Store,
		maxRowsByUID: opts.MaxRowsPerUID,
		maxRows:      opts.MaxRows,
		listWindow:   opts.ListDBWindowMax,
		byUID:        make(map[string]map[conversationAuthorityKey]conversationAuthorityEntry),
		targets:      make(map[conversationAuthorityTargetKey]conversationAuthorityState),
	}
}

func (a *conversationAuthority) markActive(target conversationusecase.RouteTarget) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.targets[targetKey(target)] = conversationAuthorityActive
}

func (a *conversationAuthority) AdmitPatches(_ context.Context, target conversationusecase.RouteTarget, patches []conversationusecase.ActivePatch) error {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.ensureTargetLocked(target); err != nil {
		return err
	}
	for _, patch := range patches {
		if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType == 0 || patch.ActiveAt <= 0 {
			continue
		}
		rows := a.byUID[patch.UID]
		if rows == nil {
			rows = make(map[conversationAuthorityKey]conversationAuthorityEntry)
			a.byUID[patch.UID] = rows
		}
		key := conversationAuthorityKey{channelID: patch.ChannelID, channelType: patch.ChannelType}
		existing, exists := rows[key]
		if !exists && (len(rows) >= a.maxRowsByUID || a.totalRows >= a.maxRows) {
			return conversationusecase.ErrCachePressure
		}
		if !exists {
			a.totalRows++
		}
		rows[key] = conversationAuthorityEntry{patch: mergeConversationActivePatch(existing.patch, patch)}
	}
	return nil
}
```

Add:

```go
func (a *conversationAuthority) ensureTargetLocked(target conversationusecase.RouteTarget) error {
	if target.LeaderNodeID != 0 && target.LeaderNodeID != a.localNodeID {
		return conversationusecase.ErrNotLeader
	}
	switch a.targets[targetKey(target)] {
	case conversationAuthorityActive:
		return nil
	case conversationAuthorityWarming:
		return conversationusecase.ErrRouteNotReady
	default:
		return conversationusecase.ErrStaleRoute
	}
}

func targetKey(target conversationusecase.RouteTarget) conversationAuthorityTargetKey {
	return conversationAuthorityTargetKey{
		hashSlot:       target.HashSlot,
		slotID:         target.SlotID,
		leaderNodeID:   target.LeaderNodeID,
		routeRevision:  target.RouteRevision,
		authorityEpoch: target.AuthorityEpoch,
	}
}
```

- [ ] **Step 5: Implement active view merge**

Add:

```go
func (a *conversationAuthority) ListUserConversationActiveView(ctx context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) (conversationusecase.ActiveViewPage, error) {
	if a == nil || a.store == nil {
		return conversationusecase.ActiveViewPage{}, conversationusecase.ErrRouteNotReady
	}
	cacheRows := a.cacheRowsAfter(uid, after, limit+1)
	dbLimit := limit + len(cacheRows) + 1
	if dbLimit > a.listWindow {
		return conversationusecase.ActiveViewPage{}, conversationusecase.ErrCachePressure
	}
	dbRows, cursor, done, err := a.store.ListUserConversationActivePage(ctx, uid, after, dbLimit)
	if err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	merged, err := a.mergeRows(ctx, uid, dbRows, cacheRows)
	if err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	sortConversationRows(merged)
	if len(merged) > limit {
		merged = merged[:limit]
		done = false
	}
	return conversationusecase.ActiveViewPage{Rows: merged, Cursor: cursor, Done: done}, nil
}
```

Add `cacheRowsAfter`, `mergeRows`, and `sortConversationRows` using the same ordering as the DB active index:

```go
func sortConversationRows(rows []metadb.UserConversationState) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ActiveAt != rows[j].ActiveAt {
			return rows[i].ActiveAt > rows[j].ActiveAt
		}
		if rows[i].ChannelID != rows[j].ChannelID {
			return rows[i].ChannelID < rows[j].ChannelID
		}
		return rows[i].ChannelType < rows[j].ChannelType
	})
}
```

- [ ] **Step 6: Add delete barrier test and implementation**

Add this test to `internalv2/app/conversation_authority_test.go`:

```go
func TestConversationAuthorityCacheOnlyRowChecksPrimaryDeleteBarrier(t *testing.T) {
	store := &recordingConversationAuthorityStore{
		primary: map[metadb.ConversationKey]metadb.UserConversationState{
			{ChannelID: "hidden", ChannelType: 2}: {UID: "u1", ChannelID: "hidden", ChannelType: 2, DeletedToSeq: 10, ActiveAt: 0},
		},
	}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100, ListDBWindowMax: 20})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "hidden", ChannelType: 2, ActiveAt: 300, MessageSeq: 9,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	page, err := authority.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 0 {
		t.Fatalf("rows = %#v, want hidden cache row blocked", page.Rows)
	}
}
```

In `mergeRows`, when a cache key is absent from DB active rows, call `store.GetUserConversationState`. If a primary row exists and `DeletedToSeq >= patch.MessageSeq`, skip the cache row.

- [ ] **Step 7: Add flush and handoff tests**

Add these tests:

```go
func TestConversationAuthorityFlushUsesTouchPatch(t *testing.T) {
	store := &recordingConversationAuthorityStore{}
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: store, MaxRowsPerUID: 10, MaxRows: 100})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markActive(target)
	if err := authority.AdmitPatches(context.Background(), target, []conversationusecase.ActivePatch{{
		UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 300, MessageSeq: 30, SparseActive: true,
	}}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	if err := authority.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(store.touched) != 1 || store.touched[0].MessageSeq != 30 || !store.touched[0].SparseActiveSet {
		t.Fatalf("touched = %#v, want one fenced sparse active patch", store.touched)
	}
}

func TestConversationAuthorityWarmingRejectsList(t *testing.T) {
	authority := newConversationAuthority(conversationAuthorityOptions{LocalNodeID: 1, Store: &recordingConversationAuthorityStore{}})
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 1, RouteRevision: 3, AuthorityEpoch: 4}
	authority.markWarming(target)
	_, err := authority.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if !errors.Is(err, conversationusecase.ErrRouteNotReady) {
		t.Fatalf("ListUserConversationActiveView() error = %v, want ErrRouteNotReady", err)
	}
}
```

Implement `Flush(ctx)`, `markWarming(target)`, and `DrainAuthority(ctx, target)` with the result enum from the spec.

- [ ] **Step 8: Run app authority tests**

Run:

```bash
go test ./internalv2/app -run 'TestConversationAuthority' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit Task 3**

```bash
git add internalv2/app/conversation_authority.go internalv2/app/conversation_authority_test.go
git commit -m "feat: add conversation authority cache"
```

## Task 4: Conversation Authority Node RPC

**Files:**
- Modify: `pkg/clusterv2/net/ids.go`
- Modify: `internalv2/access/node/presence_rpc.go`
- Create: `internalv2/access/node/conversation_codec.go`
- Create: `internalv2/access/node/conversation_rpc.go`
- Test: `internalv2/access/node/conversation_codec_test.go`
- Test: `internalv2/access/node/conversation_rpc_test.go`
- Modify: `internalv2/access/node/FLOW.md`

- [ ] **Step 1: Reserve the RPC id**

In `pkg/clusterv2/net/ids.go`, append after `RPCChannelLastVisible`:

```go
// RPCConversationAuthority serves internalv2 UID conversation authority cache requests.
RPCConversationAuthority
```

- [ ] **Step 2: Add failing codec roundtrip test**

Create `internalv2/access/node/conversation_codec_test.go`:

```go
package node

import (
	"reflect"
	"testing"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestConversationAuthorityCodecRoundTripAdmit(t *testing.T) {
	req := conversationAuthorityRequest{
		Op:     conversationOpAdmitPatches,
		Target: conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5},
		Patches: []conversationusecase.ActivePatch{{
			UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, UpdatedAt: 101, SparseActive: true, MessageSeq: 9,
		}},
	}
	body, err := encodeConversationAuthorityRequest(req)
	if err != nil {
		t.Fatalf("encodeConversationAuthorityRequest() error = %v", err)
	}
	got, err := decodeConversationAuthorityRequest(body)
	if err != nil {
		t.Fatalf("decodeConversationAuthorityRequest() error = %v", err)
	}
	if !reflect.DeepEqual(got, req) {
		t.Fatalf("decoded = %#v, want %#v", got, req)
	}
}

func TestConversationAuthorityCodecRoundTripListResponse(t *testing.T) {
	resp := conversationAuthorityResponse{
		Status: conversationRPCStatusOK,
		Page: conversationusecase.ActiveViewPage{
			Rows:   []metadb.UserConversationState{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100}},
			Cursor: metadb.UserConversationActiveCursor{ActiveAt: 100, ChannelID: "g1", ChannelType: 2},
			Done:   true,
		},
	}
	body, err := encodeConversationAuthorityResponse(resp)
	if err != nil {
		t.Fatalf("encodeConversationAuthorityResponse() error = %v", err)
	}
	got, err := decodeConversationAuthorityResponse(body)
	if err != nil {
		t.Fatalf("decodeConversationAuthorityResponse() error = %v", err)
	}
	if !reflect.DeepEqual(got, resp) {
		t.Fatalf("decoded = %#v, want %#v", got, resp)
	}
}
```

- [ ] **Step 3: Implement codec DTOs**

Create `internalv2/access/node/conversation_codec.go` with magic constants:

```go
var (
	conversationAuthorityRequestMagic  = [...]byte{'W', 'K', 'V', 'C', 1}
	conversationAuthorityResponseMagic = [...]byte{'W', 'K', 'V', 'c', 1}
)

const maxConversationAuthorityCollectionLen = 4096

const (
	conversationOpAdmitPatches = "admit_conversation_active_patches"
	conversationOpList         = "list_conversations"
	conversationOpDrain        = "drain_conversation_authority"

	conversationRPCStatusOK            = rpcStatusOK
	conversationRPCStatusNotLeader     = rpcStatusNotLeader
	conversationRPCStatusStaleRoute    = rpcStatusStaleRoute
	conversationRPCStatusRouteNotReady = rpcStatusRouteNotReady
	conversationRPCStatusCachePressure = "cache_pressure"
	conversationRPCStatusRejected      = rpcStatusRejected

	conversationDrainResultDrained     = "drained"
	conversationDrainResultTransferred = "transferred"
	conversationDrainResultNoDirty     = "no_dirty"
	conversationDrainResultBusy        = "busy"
)
```

Add request/response structs:

```go
type conversationAuthorityRequest struct {
	Op      string
	Target  conversationusecase.RouteTarget
	UID     string
	After   metadb.UserConversationActiveCursor
	Limit   int
	Patches []conversationusecase.ActivePatch
}

type conversationAuthorityResponse struct {
	Status      string
	Page        conversationusecase.ActiveViewPage
	DrainResult string
}
```

Implement encode/decode using existing helpers from presence/delivery codecs: `appendString`, `readString`, `appendUvarint`, `readUvarint`, `appendVarint`, `readVarint`, and `hasMagic`.

- [ ] **Step 4: Add failing RPC dispatch tests**

Create `internalv2/access/node/conversation_rpc_test.go`:

```go
package node

import (
	"context"
	"testing"

	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestConversationAuthorityRPCDispatchesList(t *testing.T) {
	local := &fakeConversationAuthority{page: conversationusecase.ActiveViewPage{
		Rows: []metadb.UserConversationState{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100}},
		Done: true,
	}}
	adapter := New(Options{ConversationAuthority: local})
	req := conversationAuthorityRequest{Op: conversationOpList, UID: "u1", Limit: 10}
	body, err := encodeConversationAuthorityRequest(req)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	respBody, err := adapter.HandleConversationAuthorityRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleConversationAuthorityRPC() error = %v", err)
	}
	resp, err := decodeConversationAuthorityResponse(respBody)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != conversationRPCStatusOK || len(resp.Page.Rows) != 1 {
		t.Fatalf("resp = %#v, want ok with row", resp)
	}
}

type fakeConversationAuthority struct {
	page conversationusecase.ActiveViewPage
}

func (f *fakeConversationAuthority) AdmitPatches(context.Context, conversationusecase.RouteTarget, []conversationusecase.ActivePatch) error {
	return nil
}

func (f *fakeConversationAuthority) ListUserConversationActiveView(context.Context, string, metadb.UserConversationActiveCursor, int) (conversationusecase.ActiveViewPage, error) {
	return f.page, nil
}

func (f *fakeConversationAuthority) DrainAuthority(context.Context, conversationusecase.RouteTarget) (string, error) {
	return conversationDrainResultNoDirty, nil
}
```

- [ ] **Step 5: Add adapter interfaces and handler**

In `internalv2/access/node/presence_rpc.go`, extend `Options` and `Adapter`:

```go
// ConversationAuthority handles UID-owned conversation active cache requests.
type ConversationAuthority interface {
	AdmitPatches(context.Context, conversationusecase.RouteTarget, []conversationusecase.ActivePatch) error
	ListUserConversationActiveView(context.Context, string, metadb.UserConversationActiveCursor, int) (conversationusecase.ActiveViewPage, error)
	DrainAuthority(context.Context, conversationusecase.RouteTarget) (string, error)
}
```

Add `ConversationAuthority ConversationAuthority` to `Options`, `conversation ConversationAuthority` to `Adapter`, and wire it in `New`.

Create `internalv2/access/node/conversation_rpc.go` with `ConversationAuthorityRPCServiceID`, `HandleConversationAuthorityRPC`, and `Client` methods:

```go
// ConversationAuthorityRPCServiceID is the clusterv2 RPC service for UID conversation authority calls.
const ConversationAuthorityRPCServiceID uint8 = clusternet.RPCConversationAuthority

func (a *Adapter) HandleConversationAuthorityRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeConversationAuthorityRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("conversation authority rpc decode failed", wklog.Int("payloadBytes", len(payload)), wklog.Error(err))
		return nil, err
	}
	if a == nil || a.conversation == nil {
		return encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusRejected})
	}
	switch req.Op {
	case conversationOpAdmitPatches:
		err = a.conversation.AdmitPatches(ctx, req.Target, req.Patches)
		return encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusForError(err)})
	case conversationOpList:
		page, err := a.conversation.ListUserConversationActiveView(ctx, req.UID, req.After, req.Limit)
		return encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusForError(err), Page: page})
	case conversationOpDrain:
		result, err := a.conversation.DrainAuthority(ctx, req.Target)
		return encodeConversationAuthorityResponse(conversationAuthorityResponse{Status: conversationRPCStatusForError(err), DrainResult: result})
	default:
		return nil, fmt.Errorf("internalv2/access/node: unknown conversation authority op %q", req.Op)
	}
}
```

- [ ] **Step 6: Run access node tests**

Run:

```bash
go test ./internalv2/access/node -run 'TestConversationAuthority' -count=1
```

Expected: PASS.

- [ ] **Step 7: Update access node FLOW**

Add a "Conversation Authority RPC" section to `internalv2/access/node/FLOW.md`:

```text
remote conversation authority client
  -> encode W K V C 1 request
  -> clusterv2 RPCConversationAuthority
  -> Adapter.HandleConversationAuthorityRPC
  -> ConversationAuthority port
  -> encode W K V c 1 response
```

- [ ] **Step 8: Commit Task 4**

```bash
git add pkg/clusterv2/net/ids.go internalv2/access/node/presence_rpc.go internalv2/access/node/conversation_codec.go internalv2/access/node/conversation_rpc.go internalv2/access/node/conversation_codec_test.go internalv2/access/node/conversation_rpc_test.go internalv2/access/node/FLOW.md
git commit -m "feat: add conversation authority rpc"
```

## Task 5: Cluster Authority Client

**Files:**
- Create: `internalv2/infra/cluster/conversation_authority.go`
- Test: `internalv2/infra/cluster/conversation_authority_test.go`
- Modify: `internalv2/infra/cluster/FLOW.md`

- [ ] **Step 1: Add failing local/remote routing tests**

Create `internalv2/infra/cluster/conversation_authority_test.go`:

```go
package cluster

import (
	"context"
	"reflect"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestConversationAuthorityClientUsesLocalAuthority(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	node := &fakeConversationAuthorityNode{nodeID: 1, route: clusterv2.Route{HashSlot: 7, SlotID: 2, Leader: 1, Revision: 3, AuthorityEpoch: 4}}
	client := NewConversationAuthorityClient(node, local)
	patch := conversationusecase.ActivePatch{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, MessageSeq: 1}
	if err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{patch}); err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	if len(local.patches) != 1 || !reflect.DeepEqual(local.patches[0], patch) {
		t.Fatalf("local patches = %#v, want %#v", local.patches, patch)
	}
}

func TestConversationAuthorityClientRoutesRemoteList(t *testing.T) {
	remoteAuthority := &fakeConversationAuthorityLocal{page: conversationusecase.ActiveViewPage{
		Rows: []metadb.UserConversationState{{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100}},
		Done: true,
	}}
	adapter := accessnode.New(accessnode.Options{ConversationAuthority: remoteAuthority})
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		route:  clusterv2.Route{HashSlot: 7, SlotID: 2, Leader: 2, Revision: 3, AuthorityEpoch: 4},
		handler: nodeRPCHandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
			return adapter.HandleConversationAuthorityRPC(ctx, payload)
		}),
	}
	client := NewConversationAuthorityClient(node, nil)
	page, err := client.ListUserConversationActiveView(context.Background(), "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g1" {
		t.Fatalf("page = %#v, want remote row", page)
	}
}
```

- [ ] **Step 2: Implement cluster client surfaces**

Create `internalv2/infra/cluster/conversation_authority.go`:

```go
package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	conversationusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// ConversationAuthorityNode is the clusterv2 surface needed by authority routing.
type ConversationAuthorityNode interface {
	NodeID() uint64
	RouteKey(string) (clusterv2.Route, error)
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
	RegisterRPC(uint8, clusterv2.NodeRPCHandler)
	WatchRouteAuthorities() <-chan clusterv2.RouteAuthorityEvent
}

// ConversationAuthorityClient routes conversation authority operations to UID leaders.
type ConversationAuthorityClient struct {
	node   ConversationAuthorityNode
	local accessnode.ConversationAuthority
	remote *accessnode.Client
}
```

Add constructor and methods:

```go
func NewConversationAuthorityClient(node ConversationAuthorityNode, local accessnode.ConversationAuthority) *ConversationAuthorityClient {
	return &ConversationAuthorityClient{node: node, local: local, remote: accessnode.NewClient(node)}
}

func (c *ConversationAuthorityClient) AdmitPatches(ctx context.Context, patches []conversationusecase.ActivePatch) error {
	grouped, err := c.groupByTarget(patches)
	if err != nil {
		return err
	}
	for target, group := range grouped {
		if err := c.authorityForTarget(target).AdmitPatches(ctx, target, group); err != nil {
			return err
		}
	}
	return nil
}

func (c *ConversationAuthorityClient) ListUserConversationActiveView(ctx context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) (conversationusecase.ActiveViewPage, error) {
	target, err := c.resolve(uid)
	if err != nil {
		return conversationusecase.ActiveViewPage{}, err
	}
	return c.authorityForTarget(target).ListUserConversationActiveView(ctx, uid, after, limit)
}
```

Map clusterv2 route errors to conversation errors in the same style as `presence.go`.

- [ ] **Step 3: Add exact-target grouping and retry tests**

Add tests:

```go
func TestConversationAuthorityClientGroupsByExactTarget(t *testing.T) {
	local := &fakeConversationAuthorityLocal{}
	node := &fakeConversationAuthorityNode{
		nodeID: 1,
		routesByUID: map[string]clusterv2.Route{
			"u1": {HashSlot: 1, SlotID: 2, Leader: 1, Revision: 10, AuthorityEpoch: 20},
			"u2": {HashSlot: 2, SlotID: 2, Leader: 1, Revision: 11, AuthorityEpoch: 21},
		},
	}
	client := NewConversationAuthorityClient(node, local)
	err := client.AdmitPatches(context.Background(), []conversationusecase.ActivePatch{
		{UID: "u1", ChannelID: "g", ChannelType: 2, ActiveAt: 10},
		{UID: "u2", ChannelID: "g", ChannelType: 2, ActiveAt: 20},
	})
	if err != nil {
		t.Fatalf("AdmitPatches() error = %v", err)
	}
	if len(local.targets) != 2 {
		t.Fatalf("targets = %#v, want two exact route targets", local.targets)
	}
}
```

- [ ] **Step 4: Add drain method**

Implement:

```go
func (c *ConversationAuthorityClient) DrainAuthority(ctx context.Context, target conversationusecase.RouteTarget) (string, error) {
	return c.authorityForTarget(target).DrainAuthority(ctx, target)
}
```

Remote drain uses the access/node client method created in Task 4.

- [ ] **Step 5: Run cluster infra tests**

Run:

```bash
go test ./internalv2/infra/cluster -run 'TestConversationAuthorityClient' -count=1
```

Expected: PASS.

- [ ] **Step 6: Update infra FLOW**

Add to `internalv2/infra/cluster/FLOW.md`:

```text
ConversationAuthorityClient
  -> RouteKey(uid)
  -> local conversation authority when target leader is this node
  -> access/node ConversationAuthority RPC when target leader is remote
```

- [ ] **Step 7: Commit Task 5**

```bash
git add internalv2/infra/cluster/conversation_authority.go internalv2/infra/cluster/conversation_authority_test.go internalv2/infra/cluster/FLOW.md
git commit -m "feat: route conversation authority operations"
```

## Task 6: App Wiring and Configuration

**Files:**
- Modify: `internalv2/app/config.go`
- Modify: `internalv2/app/app.go`
- Modify: `internalv2/app/conversation.go`
- Modify: `internalv2/app/app_test.go`
- Modify: `cmd/wukongimv2/config.go`
- Modify: `cmd/wukongimv2/config_test.go`
- Modify: `wukongim.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node1.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node2.conf.example`
- Modify: `cmd/wukongimv2/wukongimv2-node3.conf.example`
- Modify: `scripts/wukongimv2/wukongimv2.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node1.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node2.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node3.conf`

- [ ] **Step 1: Add failing config default/validation test**

Add to `internalv2/app/app_test.go` or `internalv2/app/config_test.go` if present:

```go
func TestDefaultConversationAuthorityConfig(t *testing.T) {
	cfg := defaultConversationConfig(ConversationConfig{})
	if cfg.AuthorityCacheMaxRowsPerUID != 4096 ||
		cfg.AuthorityCacheMaxRows != 100000 ||
		cfg.AuthorityListDBWindowMax != 1000 ||
		cfg.AuthorityAdmissionTimeout != 500*time.Millisecond ||
		cfg.AuthorityHandoffTimeout != 3*time.Second ||
		cfg.AuthorityRPCTimeout != 500*time.Millisecond ||
		cfg.AuthorityRPCBatchRows != 512 ||
		cfg.AuthorityRPCConcurrency != 16 {
		t.Fatalf("conversation authority defaults = %#v", cfg)
	}
}
```

- [ ] **Step 2: Add conversation config fields**

In `internalv2/app/config.go`, add fields to `ConversationConfig` with English comments:

```go
// AuthorityCacheMaxRowsPerUID bounds unflushed authority cache rows for one UID.
AuthorityCacheMaxRowsPerUID int
// AuthorityCacheMaxRows bounds all unflushed authority cache rows on this node.
AuthorityCacheMaxRows int
// AuthorityListDBWindowMax bounds DB active rows read while merging cache rows for one List request.
AuthorityListDBWindowMax int
// AuthorityAdmissionTimeout bounds foreground cache admission after a durable message commit.
AuthorityAdmissionTimeout time.Duration
// AuthorityHandoffTimeout bounds how long a new authority waits for old-authority drain before explicit abandon.
AuthorityHandoffTimeout time.Duration
// AuthorityRPCTimeout bounds one conversation authority node RPC.
AuthorityRPCTimeout time.Duration
// AuthorityRPCBatchRows limits active patches carried by one authority admission RPC.
AuthorityRPCBatchRows int
// AuthorityRPCConcurrency limits concurrent authority admission RPCs per committed event.
AuthorityRPCConcurrency int
```

Update `defaultConversationConfig` and `validateConversationConfig` with the spec bounds:

```go
if cfg.AuthorityCacheMaxRowsPerUID == 0 { cfg.AuthorityCacheMaxRowsPerUID = 4096 }
if cfg.AuthorityCacheMaxRows == 0 { cfg.AuthorityCacheMaxRows = 100000 }
if cfg.AuthorityListDBWindowMax == 0 { cfg.AuthorityListDBWindowMax = 1000 }
if cfg.AuthorityAdmissionTimeout == 0 { cfg.AuthorityAdmissionTimeout = 500 * time.Millisecond }
if cfg.AuthorityHandoffTimeout == 0 { cfg.AuthorityHandoffTimeout = 3 * time.Second }
if cfg.AuthorityRPCTimeout == 0 { cfg.AuthorityRPCTimeout = 500 * time.Millisecond }
if cfg.AuthorityRPCBatchRows == 0 { cfg.AuthorityRPCBatchRows = 512 }
if cfg.AuthorityRPCConcurrency == 0 { cfg.AuthorityRPCConcurrency = 16 }
```

- [ ] **Step 3: Add wukongimv2 config parsing**

In `cmd/wukongimv2/config.go`, map:

```go
WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID
WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS
WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX
WK_CONVERSATION_AUTHORITY_ADMISSION_TIMEOUT
WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT
WK_CONVERSATION_AUTHORITY_RPC_TIMEOUT
WK_CONVERSATION_AUTHORITY_RPC_BATCH_ROWS
WK_CONVERSATION_AUTHORITY_RPC_CONCURRENCY
```

to the new `internalv2/app.ConversationConfig` fields. Add a config test that sets each env key and asserts the parsed value.

- [ ] **Step 4: Wire authority runtime**

In `internalv2/app/app.go`, replace current conversation projector wiring in the message setup area with:

```go
if node, ok := app.cluster.(clusterinfra.ConversationAuthorityNode); ok {
	store := clusterinfra.NewConversationProjectionStore(node)
	if app.conversationProjector == nil {
		authority := newConversationAuthority(conversationAuthorityOptions{
			LocalNodeID:          node.NodeID(),
			Store:                store,
			MaxRowsPerUID:        app.cfg.Conversation.AuthorityCacheMaxRowsPerUID,
			MaxRows:              app.cfg.Conversation.AuthorityCacheMaxRows,
			ListDBWindowMax:      app.cfg.Conversation.AuthorityListDBWindowMax,
			AdmissionBatchRows:   app.cfg.Conversation.AuthorityRPCBatchRows,
			AdmissionConcurrency: app.cfg.Conversation.AuthorityRPCConcurrency,
			HandoffTimeout:       app.cfg.Conversation.AuthorityHandoffTimeout,
		})
		client := clusterinfra.NewConversationAuthorityClient(node, authority)
		app.conversationProjector = newConversationAuthorityCommittedSink(conversationAuthorityCommittedOptions{
			Projector:            conversationusecase.NewProjector(conversationusecase.ProjectorOptions{Members: store, SmallGroupFanoutLimit: app.cfg.Conversation.SmallGroupFanoutLimit}),
			Authority:            client,
			AdmissionTimeout:     app.cfg.Conversation.AuthorityAdmissionTimeout,
			Observer:             app.conversationProjectorObserver(),
		})
		adapter := accessnode.New(accessnode.Options{ConversationAuthority: authority, Logger: app.logger.Named("node")})
		node.RegisterRPC(accessnode.ConversationAuthorityRPCServiceID, nodeRPCHandlerFunc(adapter.HandleConversationAuthorityRPC))
	}
	committedSinks = append(committedSinks, app.conversationProjector)
}
```

Keep the exact structure compatible with the surrounding code; do not remove delivery sink wiring.

- [ ] **Step 5: Make conversation List use authority client**

When creating `app.conversations`, if the cluster exposes the new authority surface, pass the authority client as `Store` and keep the existing `ConversationStore` as `Messages`:

```go
readStore := clusterinfra.NewConversationStore(node, clusterinfra.ConversationStoreOptions{
	MaxLastMessageConcurrency: app.cfg.Conversation.MaxLastMessageConcurrency,
})
authorityStore := clusterinfra.NewConversationAuthorityClient(node, authority)
app.conversations = conversationusecase.New(conversationusecase.Options{
	Store:    authorityStore,
	Messages: readStore,
})
```

If authority is not available, `ConversationStore.ListUserConversationActiveView` from Task 2 preserves DB-only behavior for tests and limited harnesses.

- [ ] **Step 6: Run app/config tests**

Run:

```bash
go test ./internalv2/app -run 'TestDefaultConversationAuthorityConfig|TestNew|TestConversation' -count=1
go test ./cmd/wukongimv2 -run 'Test.*ConversationAuthority|Test.*Config' -count=1
```

Expected: PASS.

- [ ] **Step 7: Update config examples**

Add the new keys with defaults to:

```text
wukongim.conf.example
cmd/wukongimv2/wukongimv2.conf.example
cmd/wukongimv2/wukongimv2-node1.conf.example
cmd/wukongimv2/wukongimv2-node2.conf.example
cmd/wukongimv2/wukongimv2-node3.conf.example
scripts/wukongimv2/wukongimv2.conf
scripts/wukongimv2/wukongimv2-node1.conf
scripts/wukongimv2/wukongimv2-node2.conf
scripts/wukongimv2/wukongimv2-node3.conf
```

Use:

```text
WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS_PER_UID=4096
WK_CONVERSATION_AUTHORITY_CACHE_MAX_ROWS=100000
WK_CONVERSATION_AUTHORITY_LIST_DB_WINDOW_MAX=1000
WK_CONVERSATION_AUTHORITY_ADMISSION_TIMEOUT=500ms
WK_CONVERSATION_AUTHORITY_HANDOFF_TIMEOUT=3s
WK_CONVERSATION_AUTHORITY_RPC_TIMEOUT=500ms
WK_CONVERSATION_AUTHORITY_RPC_BATCH_ROWS=512
WK_CONVERSATION_AUTHORITY_RPC_CONCURRENCY=16
```

- [ ] **Step 8: Commit Task 6**

```bash
git add internalv2/app/config.go internalv2/app/app.go internalv2/app/conversation.go internalv2/app/app_test.go cmd/wukongimv2/config.go cmd/wukongimv2/config_test.go wukongim.conf.example cmd/wukongimv2/wukongimv2.conf.example cmd/wukongimv2/wukongimv2-node1.conf.example cmd/wukongimv2/wukongimv2-node2.conf.example cmd/wukongimv2/wukongimv2-node3.conf.example scripts/wukongimv2/wukongimv2.conf scripts/wukongimv2/wukongimv2-node1.conf scripts/wukongimv2/wukongimv2-node2.conf scripts/wukongimv2/wukongimv2-node3.conf
git commit -m "feat: wire conversation authority cache"
```

## Task 7: Authority Runtime Edge Cases and Metrics

**Files:**
- Modify: `internalv2/app/conversation_authority.go`
- Modify: `internalv2/app/conversation_authority_test.go`
- Modify: `internalv2/app/observability.go`
- Modify: `internalv2/app/observability_test.go`

- [ ] **Step 1: Add admission timeout test**

Add to `internalv2/app/conversation_authority_test.go`:

```go
func TestConversationAuthorityAdmissionTimeoutIsNonFatalSinkError(t *testing.T) {
	sink := newConversationAuthorityCommittedSink(conversationAuthorityCommittedOptions{
		Projector: &blockingPatchProjector{},
		Authority: &recordingPatchAuthority{},
		AdmissionTimeout: time.Nanosecond,
	})
	err := sink.Submit(context.Background(), messageevents.MessageCommitted{
		MessageSeq: 1, ChannelID: "u1@u2", ChannelType: 1, FromUID: "u1", ServerTimestampMS: 100,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Submit() error = %v, want DeadlineExceeded", err)
	}
}
```

Define helper fakes:

```go
type blockingPatchProjector struct{}

func (blockingPatchProjector) ProjectActivePatches(ctx context.Context, event messageevents.MessageCommitted) ([]conversationusecase.ActivePatch, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type recordingPatchAuthority struct{}

func (recordingPatchAuthority) AdmitPatches(context.Context, []conversationusecase.ActivePatch) error { return nil }
```

- [ ] **Step 2: Implement committed sink wrapper**

In `internalv2/app/conversation_authority.go`, add:

```go
type conversationPatchProjector interface {
	ProjectActivePatches(context.Context, messageevents.MessageCommitted) ([]conversationusecase.ActivePatch, error)
}

type conversationPatchAuthority interface {
	AdmitPatches(context.Context, []conversationusecase.ActivePatch) error
}

type conversationAuthorityCommittedSink struct {
	projector        conversationPatchProjector
	authority        conversationPatchAuthority
	admissionTimeout time.Duration
	observer         conversationProjectorObserver
}

type conversationAuthorityCommittedOptions struct {
	Projector        conversationPatchProjector
	Authority        conversationPatchAuthority
	AdmissionTimeout time.Duration
	Observer         conversationProjectorObserver
}

func newConversationAuthorityCommittedSink(opts conversationAuthorityCommittedOptions) *conversationAuthorityCommittedSink {
	return &conversationAuthorityCommittedSink{projector: opts.Projector, authority: opts.Authority, admissionTimeout: opts.AdmissionTimeout, observer: opts.Observer}
}
```

Implement `Submit`, `SubmitMetadata`, and `RequiresCommittedPayload() bool { return false }`. `SubmitMetadata` must clone no payload and must use a timeout context for projection/admission.

- [ ] **Step 3: Add low-cardinality observer events**

Extend the existing conversation projector metrics adapter to record:

```text
authority_admit_result
authority_cache_pressure
authority_list_result
authority_handoff_result
```

Keep labels limited to result/status buckets. Do not add UID or channel labels.

- [ ] **Step 4: Run observability tests**

Run:

```bash
go test ./internalv2/app -run 'TestConversationAuthority|TestConversationProjector|TestObservability' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 7**

```bash
git add internalv2/app/conversation_authority.go internalv2/app/conversation_authority_test.go internalv2/app/observability.go internalv2/app/observability_test.go
git commit -m "feat: observe conversation authority cache"
```

## Task 8: App-Level Smoke Coverage and FLOW Docs

**Files:**
- Modify: `internalv2/app/conversation_api_smoke_test.go`
- Modify: `internalv2/app/FLOW.md`
- Modify: `internalv2/access/node/FLOW.md`
- Modify: `internalv2/infra/cluster/FLOW.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md` only when implementation discovers a durable project rule.

- [ ] **Step 1: Add SEND to List before flush smoke test**

In `internalv2/app/conversation_api_smoke_test.go`, add a test shaped like existing conversation API smoke tests:

```go
func TestConversationListSeesAuthorityCacheBeforeFlush(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	cfg.API.ListenAddr = "127.0.0.1:0"
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})
	startCtx, startCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	node, ok := app.cluster.(*clusterv2.Node)
	if !ok {
		t.Fatalf("cluster runtime = %T, want *clusterv2.Node", app.cluster)
	}
	waitSingleNodeClusterRouteLeader(t, node, "sender", cfg.NodeID)
	waitSingleNodeClusterRouteLeader(t, node, "receiver", cfg.NodeID)
	apiSrv, ok := app.api.(*accessapi.Server)
	if !ok {
		t.Fatalf("api runtime = %T, want *accessapi.Server", app.api)
	}

	handler := apiSrv.Handler()
	postAppJSON(t, handler, "/message/send", `{"from_uid":"sender","channel_id":"receiver","channel_type":1,"client_msg_no":"client-cache-before-flush","payload":"aGVsbG8="}`, http.StatusOK)

	got := decodeConversationListSmokeResponse(t, postAppJSON(t, handler, "/conversation/list", `{"uid":"receiver","limit":10}`, http.StatusOK))
	if len(got.Conversations) != 1 {
		t.Fatalf("conversation count = %d, want one before projector flush", len(got.Conversations))
	}
	if got.Conversations[0].ChannelID != "sender" ||
		got.Conversations[0].ChannelType != int64(frame.ChannelTypePerson) ||
		got.Conversations[0].LastMessage == nil ||
		got.Conversations[0].LastMessage.ClientMsgNo != "client-cache-before-flush" {
		t.Fatalf("conversations = %#v, want sender person conversation before DB flush", got.Conversations)
	}
}
```

Keep the assertion before calling any manual flush.

- [ ] **Step 2: Run the remote authority regression test**

Run the remote routing test from Task 5 as the multi-node authority guard:

```bash
go test ./internalv2/infra/cluster -run TestConversationAuthorityClientRoutesRemoteList -count=1
```

Expected: PASS and verifies non-local List calls use ConversationAuthority RPC instead of DB-only fallback.

- [ ] **Step 3: Update FLOW docs**

Update `internalv2/app/FLOW.md` conversation section to include:

```text
MessageCommitted
  -> conversation authority committed sink
  -> immediate active patch projection and UID authority admission
  -> local authority cache or ConversationAuthority RPC
  -> async TouchUserConversationActiveAtBatch flush

Conversation List
  -> usecase List
  -> authority active view store
  -> local cache+DB merge or remote ConversationAuthority RPC active-row page
  -> usecase last-message hydration
```

Update `internalv2/infra/cluster/FLOW.md` and `internalv2/access/node/FLOW.md` with the same boundaries.

- [ ] **Step 4: Run focused internalv2 tests**

Run:

```bash
go test ./internalv2/usecase/conversation ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/app -count=1
```

Expected: PASS.

- [ ] **Step 5: Run broader targeted tests**

Run:

```bash
go test ./internalv2/... ./pkg/clusterv2 -count=1
```

Expected: PASS. If this is too slow locally, run the four package command from Step 4 plus `go test ./pkg/clusterv2 -count=1` and record the unrun broader command in the final handoff.

- [ ] **Step 6: Commit Task 8**

```bash
git add internalv2/app/conversation_api_smoke_test.go internalv2/app/FLOW.md internalv2/access/node/FLOW.md internalv2/infra/cluster/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "test: cover conversation authority cache flow"
```

Only include `docs/development/PROJECT_KNOWLEDGE.md` if the implementation actually changed it.

## Final Verification

- [ ] Run focused unit suite:

```bash
go test ./internalv2/usecase/conversation ./internalv2/access/node ./internalv2/infra/cluster ./internalv2/app ./pkg/clusterv2 -count=1
```

- [ ] Run targeted project suite:

```bash
go test ./internalv2/... ./pkg/clusterv2 ./pkg/db/meta -count=1
```

- [ ] Check git state:

```bash
git status --short
```

Expected: only intentional changes remain.

- [ ] Request review after implementation:

Use `superpowers:requesting-code-review` with the implementation commit range before merging or opening a PR.

## Plan Self-Review

- Spec coverage: Tasks cover active view reads, immediate cache admission, authority route/RPC, cache pressure, delete barrier primary lookup, handoff warming/drain, config defaults/bounds, observability, tests, and FLOW docs.
- Placeholder scan: no unresolved marker words or unspecified implementation sections are required for execution.
- Type consistency: `conversationusecase.ActivePatch`, `RouteTarget`, `ActiveViewPage`, `ListUserConversationActiveView`, and `TouchUserConversationActiveAtBatch` are introduced before downstream tasks use them.
