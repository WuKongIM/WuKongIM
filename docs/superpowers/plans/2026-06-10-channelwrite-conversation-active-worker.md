# Channelwrite Conversation Active Worker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move recent-conversation active indexing out of `channelwrite` into a worker-owned UID active cache that is immediately readable and flushed to DB in the background.

**Architecture:** Add `internalv2/runtime/conversationactive` as the active-index runtime. `channelwrite` expands recipients and calls an active admitter; `conversationactive` updates its own cache synchronously, serves active-list reads by merging cache + DB, and flushes dirty rows asynchronously. App and node access packages stay as routing/RPC adapters, not cache owners.

**Tech Stack:** Go, `internalv2/runtime/conversationactive`, `internalv2/contracts/channelwrite`, `internalv2/app`, `internalv2/access/node`, `internalv2/infra/cluster`, `pkg/db/meta`, `go test`.

---

## Reference Spec

- `docs/superpowers/specs/2026-06-10-channelwrite-conversation-active-worker-design.md`

## Scope Check

This plan implements the recent-conversation active worker and wires
`channelwrite` to admit active batches. It does not complete the separate
delivery worker split. Existing delivery processing may remain in the current
recipient delivery path during this plan, but recent-conversation mutation must
leave `channelwrite.RecipientProcessor`.

## File Structure

- Create `internalv2/runtime/conversationactive/FLOW.md`: package flow and ownership.
- Create `internalv2/runtime/conversationactive/types.go`: errors, options, DTOs, store and observer ports.
- Create `internalv2/runtime/conversationactive/manager.go`: active cache admission and active-list merge.
- Create `internalv2/runtime/conversationactive/flush.go`: dirty snapshot, versioned flush, pressure spill, lifecycle.
- Create `internalv2/runtime/conversationactive/manager_test.go`: admission, sender `ReadSeq`, list visibility, merge, and pressure tests.
- Create `internalv2/runtime/conversationactive/flush_test.go`: dirty flush and version tests.
- Modify `internalv2/app/conversation_authority.go`: replace app-owned cache state with adapters around `conversationactive.Manager`.
- Modify `internalv2/app/conversation_route_lifecycle.go`: call the new active manager authority hooks.
- Modify `internalv2/app/wiring.go`: create and wire `conversationactive.Manager`.
- Modify `internalv2/app/channel_write.go`: replace `channelWriteConversationProjector` with an active-batch admitter adapter.
- Modify `internalv2/runtime/channelwrite/options.go`: replace conversation projector option with `ConversationActiveAdmitter`.
- Modify `internalv2/runtime/channelwrite/recipient.go`: build active participants from `expanded recipients + sender UID` and admit them before delivery handoff.
- Modify `internalv2/runtime/channelwrite/delivery.go`: remove conversation patch projection from `RecipientProcessor`.
- Modify tests under `internalv2/runtime/channelwrite` and `internalv2/app` to assert the new ownership.
- Update `internalv2/runtime/channelwrite/FLOW.md`, `internalv2/usecase/conversation/FLOW.md`, `internalv2/app/FLOW.md`, and add the new runtime `FLOW.md`.

## Task 1: Add Conversation Active Runtime Admission

**Files:**
- Create: `internalv2/runtime/conversationactive/types.go`
- Create: `internalv2/runtime/conversationactive/manager.go`
- Test: `internalv2/runtime/conversationactive/manager_test.go`

- [ ] **Step 1: Write failing admission tests**

Create `internalv2/runtime/conversationactive/manager_test.go` with:

```go
package conversationactive

import (
	"context"
	"testing"

	channelwritecontract "github.com/WuKongIM/WuKongIM/internalv2/contracts/channelwrite"
)

func TestAdmitActiveBatchUpdatesCacheAndSenderReadSeq(t *testing.T) {
	manager := NewManager(Options{LocalNodeID: 1, ShardCount: 1})
	target := RouteTarget{HashSlot: 1, SlotID: 10, LeaderNodeID: 1, RouteRevision: 2, AuthorityEpoch: 3}
	err := manager.MarkActive(target)
	if err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	err = manager.AdmitActiveBatch(context.Background(), target, ActiveBatch{
		Event: channelwritecontract.CommittedEnvelope{
			MessageSeq:        9,
			ChannelID:         "g1",
			ChannelType:       2,
			FromUID:           "sender",
			ServerTimestampMS:  1000,
		},
		Recipients: []channelwritecontract.Recipient{{UID: "receiver"}, {UID: "sender"}},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	sender, ok := manager.EntryForTest("sender", "g1", 2)
	if !ok {
		t.Fatalf("sender entry missing")
	}
	if sender.ActiveAt != 1000 || sender.ReadSeq != 9 {
		t.Fatalf("sender entry = %#v, want active_at 1000 read_seq 9", sender)
	}
	receiver, ok := manager.EntryForTest("receiver", "g1", 2)
	if !ok {
		t.Fatalf("receiver entry missing")
	}
	if receiver.ActiveAt != 1000 || receiver.ReadSeq != 0 {
		t.Fatalf("receiver entry = %#v, want active_at 1000 read_seq 0", receiver)
	}
}

func TestAdmitActiveBatchPreservesReceiverReadSeq(t *testing.T) {
	manager := NewManager(Options{LocalNodeID: 1, ShardCount: 1})
	target := RouteTarget{HashSlot: 1, SlotID: 10, LeaderNodeID: 1, RouteRevision: 2, AuthorityEpoch: 3}
	if err := manager.MarkActive(target); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}

	if err := manager.AdmitActiveBatch(context.Background(), target, ActiveBatch{
		Event: channelwritecontract.CommittedEnvelope{
			MessageSeq:        3,
			ChannelID:         "g1",
			ChannelType:       2,
			FromUID:           "u1",
			ServerTimestampMS:  100,
		},
		Recipients: []channelwritecontract.Recipient{{UID: "u1"}},
	}); err != nil {
		t.Fatalf("first AdmitActiveBatch() error = %v", err)
	}
	if err := manager.AdmitActiveBatch(context.Background(), target, ActiveBatch{
		Event: channelwritecontract.CommittedEnvelope{
			MessageSeq:        4,
			ChannelID:         "g1",
			ChannelType:       2,
			FromUID:           "u2",
			ServerTimestampMS:  200,
		},
		Recipients: []channelwritecontract.Recipient{{UID: "u1"}},
	}); err != nil {
		t.Fatalf("second AdmitActiveBatch() error = %v", err)
	}

	entry, ok := manager.EntryForTest("u1", "g1", 2)
	if !ok {
		t.Fatalf("entry missing")
	}
	if entry.ActiveAt != 200 || entry.ReadSeq != 3 {
		t.Fatalf("entry = %#v, want active_at 200 and preserved read_seq 3", entry)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internalv2/runtime/conversationactive -run 'TestAdmitActiveBatch' -count=1
```

Expected: FAIL because the package and `NewManager` do not exist.

- [ ] **Step 3: Add runtime types**

Create `internalv2/runtime/conversationactive/types.go`:

```go
package conversationactive

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/contracts/authority"
	channelwritecontract "github.com/WuKongIM/WuKongIM/internalv2/contracts/channelwrite"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	// ErrRouteNotReady reports that the UID authority route cannot serve active work yet.
	ErrRouteNotReady = errors.New("internalv2/runtime/conversationactive: route not ready")
	// ErrStaleRoute reports that an active request used an outdated UID authority target.
	ErrStaleRoute = errors.New("internalv2/runtime/conversationactive: stale route")
	// ErrNotLeader reports that the target is owned by another node.
	ErrNotLeader = errors.New("internalv2/runtime/conversationactive: not leader")
	// ErrCachePressure reports that the active cache cannot admit more rows.
	ErrCachePressure = errors.New("internalv2/runtime/conversationactive: cache pressure")
)

// RouteTarget identifies the fenced UID authority that should serve active work.
type RouteTarget = authority.Target

// ActiveBatch carries one committed message and the UIDs that should appear active.
type ActiveBatch struct {
	// Event is the committed message that advanced the active row.
	Event channelwritecontract.CommittedEnvelope
	// Recipients are expanded channel recipients; the sender is added by the manager when needed.
	Recipients []channelwritecontract.Recipient
}

// ActiveEntry is the cached recent-conversation row owned by the active runtime.
type ActiveEntry struct {
	// UID owns the conversation row.
	UID string
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ActiveAt is the ordering anchor for recent conversation list pages.
	ActiveAt int64
	// ReadSeq advances only for the sender's own active row.
	ReadSeq uint64
}

// ActivePatch is an internal monotonic update command for one active row.
type ActivePatch struct {
	UID         string
	ChannelID   string
	ChannelType int64
	ActiveAt    int64
	ReadSeq     uint64
	ReadSeqSet  bool
}

// ActiveViewPage is one active-list page before last-message hydration.
type ActiveViewPage struct {
	Rows   []metadb.UserConversationState
	Cursor metadb.UserConversationActiveCursor
	Done   bool
}

// Store persists and reads durable UID-owned active rows.
type Store interface {
	ListUserConversationActivePage(context.Context, string, metadb.UserConversationActiveCursor, int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error)
	TouchUserConversationActiveAtBatch(context.Context, []metadb.UserConversationActivePatch) error
}

// Options configures the active-index runtime.
type Options struct {
	LocalNodeID        uint64
	Store              Store
	ShardCount         int
	MaxRowsPerUID      int
	MaxRows            int
	ListDBWindowMax    int
	FlushInterval      time.Duration
	FlushBatchRows     int
}
```

- [ ] **Step 4: Add minimal manager admission implementation**

Create `internalv2/runtime/conversationactive/manager.go`:

```go
package conversationactive

import (
	"context"
	"hash/fnv"
	"sort"
	"strings"
	"sync"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	defaultShardCount      = 16
	defaultMaxRowsPerUID   = 4096
	defaultMaxRows         = 100000
	defaultListDBWindowMax = 1000
)

// Manager owns UID active cache admission, active-list reads, and dirty flushes.
type Manager struct {
	localNodeID     uint64
	store           Store
	maxRowsPerUID   int
	maxRows         int
	listDBWindowMax int

	shards []activeShard
}

type activeShard struct {
	mu    sync.Mutex
	byUID map[string]map[activeKey]activeEntry
}

type activeKey struct {
	channelID   string
	channelType int64
}

type activeEntry struct {
	ActiveEntry
	target  RouteTarget
	version uint64
	dirty   bool
}

// NewManager creates an active-index runtime with bounded in-memory rows.
func NewManager(opts Options) *Manager {
	shardCount := opts.ShardCount
	if shardCount <= 0 {
		shardCount = defaultShardCount
	}
	maxRowsPerUID := opts.MaxRowsPerUID
	if maxRowsPerUID <= 0 {
		maxRowsPerUID = defaultMaxRowsPerUID
	}
	maxRows := opts.MaxRows
	if maxRows <= 0 {
		maxRows = defaultMaxRows
	}
	listWindow := opts.ListDBWindowMax
	if listWindow <= 0 {
		listWindow = defaultListDBWindowMax
	}
	manager := &Manager{
		localNodeID:     opts.LocalNodeID,
		store:           opts.Store,
		maxRowsPerUID:   maxRowsPerUID,
		maxRows:         maxRows,
		listDBWindowMax: listWindow,
		shards:          make([]activeShard, shardCount),
	}
	for i := range manager.shards {
		manager.shards[i].byUID = make(map[string]map[activeKey]activeEntry)
	}
	return manager
}

// MarkActive records that the local node can admit the target.
func (m *Manager) MarkActive(target RouteTarget) error {
	if m == nil {
		return ErrRouteNotReady
	}
	if err := m.ensureLocalTarget(target); err != nil {
		return err
	}
	return nil
}

// AdmitActiveBatch updates the active cache synchronously.
func (m *Manager) AdmitActiveBatch(ctx context.Context, target RouteTarget, batch ActiveBatch) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.ensureLocalTarget(target); err != nil {
		return err
	}
	patches := activePatches(batch)
	for _, patch := range patches {
		if patch.UID == "" || patch.ChannelID == "" || patch.ChannelType == 0 || patch.ActiveAt <= 0 {
			continue
		}
		shard := m.shardForUID(patch.UID)
		shard.mu.Lock()
		rows := shard.byUID[patch.UID]
		if rows == nil {
			rows = make(map[activeKey]activeEntry)
			shard.byUID[patch.UID] = rows
		}
		key := activeKey{channelID: patch.ChannelID, channelType: patch.ChannelType}
		entry := rows[key]
		entry.ActiveEntry = mergeEntry(entry.ActiveEntry, patch)
		entry.target = target
		entry.version++
		entry.dirty = true
		rows[key] = entry
		shard.mu.Unlock()
	}
	return nil
}

func activePatches(batch ActiveBatch) []ActivePatch {
	seen := make(map[string]struct{}, len(batch.Recipients)+1)
	uids := make([]string, 0, len(batch.Recipients)+1)
	for _, recipient := range batch.Recipients {
		uid := strings.TrimSpace(recipient.UID)
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		uids = append(uids, uid)
	}
	if uid := strings.TrimSpace(batch.Event.FromUID); uid != "" {
		if _, ok := seen[uid]; !ok {
			seen[uid] = struct{}{}
			uids = append(uids, uid)
		}
	}
	sort.Strings(uids)
	patches := make([]ActivePatch, 0, len(uids))
	for _, uid := range uids {
		patch := ActivePatch{
			UID:         uid,
			ChannelID:   batch.Event.ChannelID,
			ChannelType: int64(batch.Event.ChannelType),
			ActiveAt:    batch.Event.ServerTimestampMS,
		}
		if uid == batch.Event.FromUID {
			patch.ReadSeq = batch.Event.MessageSeq
			patch.ReadSeqSet = true
		}
		patches = append(patches, patch)
	}
	return patches
}

func mergeEntry(existing ActiveEntry, patch ActivePatch) ActiveEntry {
	if existing.UID == "" {
		existing = ActiveEntry{UID: patch.UID, ChannelID: patch.ChannelID, ChannelType: patch.ChannelType}
	}
	if patch.ActiveAt > existing.ActiveAt {
		existing.ActiveAt = patch.ActiveAt
	}
	if patch.ReadSeqSet && patch.ReadSeq > existing.ReadSeq {
		existing.ReadSeq = patch.ReadSeq
	}
	return existing
}

func (m *Manager) ensureLocalTarget(target RouteTarget) error {
	if target.LeaderNodeID == 0 {
		return ErrRouteNotReady
	}
	if m.localNodeID != 0 && target.LeaderNodeID != m.localNodeID {
		return ErrNotLeader
	}
	return nil
}

func (m *Manager) shardForUID(uid string) *activeShard {
	if len(m.shards) == 1 {
		return &m.shards[0]
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(uid))
	return &m.shards[int(hash.Sum64()%uint64(len(m.shards)))]
}

// EntryForTest returns a cached entry copy for unit tests.
func (m *Manager) EntryForTest(uid, channelID string, channelType int64) (ActiveEntry, bool) {
	if m == nil {
		return ActiveEntry{}, false
	}
	shard := m.shardForUID(uid)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	rows := shard.byUID[uid]
	entry, ok := rows[activeKey{channelID: channelID, channelType: channelType}]
	return entry.ActiveEntry, ok
}

func stateFromEntry(entry ActiveEntry) metadb.UserConversationState {
	return metadb.UserConversationState{
		UID:         entry.UID,
		ChannelID:   entry.ChannelID,
		ChannelType: entry.ChannelType,
		ActiveAt:    entry.ActiveAt,
		UpdatedAt:   entry.ActiveAt,
		ReadSeq:     entry.ReadSeq,
	}
}
```

- [ ] **Step 5: Run admission tests**

Run:

```bash
go test ./internalv2/runtime/conversationactive -run 'TestAdmitActiveBatch' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit runtime admission**

```bash
git add internalv2/runtime/conversationactive
git commit -m "feat(internalv2): add conversation active admission cache"
```

## Task 2: Add Active List Cache + DB Merge

**Files:**
- Modify: `internalv2/runtime/conversationactive/manager.go`
- Test: `internalv2/runtime/conversationactive/manager_test.go`

- [ ] **Step 1: Add failing list visibility test**

Append to `internalv2/runtime/conversationactive/manager_test.go`:

```go
func TestListActiveViewReadsCacheBeforeFlush(t *testing.T) {
	store := &recordingActiveStore{}
	manager := NewManager(Options{LocalNodeID: 1, Store: store, ShardCount: 1})
	target := RouteTarget{HashSlot: 1, SlotID: 10, LeaderNodeID: 1, RouteRevision: 2, AuthorityEpoch: 3}
	if err := manager.MarkActive(target); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	if err := manager.AdmitActiveBatch(context.Background(), target, ActiveBatch{
		Event: channelwritecontract.CommittedEnvelope{
			MessageSeq:       7,
			ChannelID:        "g-cache",
			ChannelType:      2,
			FromUID:          "sender",
			ServerTimestampMS: 500,
		},
		Recipients: []channelwritecontract.Recipient{{UID: "receiver"}},
	}); err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	page, err := manager.ListActiveView(context.Background(), target, "receiver", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g-cache" || page.Rows[0].ActiveAt != 500 {
		t.Fatalf("page rows = %#v, want cache row before DB flush", page.Rows)
	}
	if len(store.listCalls) != 1 {
		t.Fatalf("store list calls = %d, want one DB read merged with cache", len(store.listCalls))
	}
}

func TestListActiveViewMergesCacheOverDB(t *testing.T) {
	store := &recordingActiveStore{
		rows: []metadb.UserConversationState{
			{UID: "u1", ChannelID: "g1", ChannelType: 2, ActiveAt: 100, ReadSeq: 1},
			{UID: "u1", ChannelID: "g2", ChannelType: 2, ActiveAt: 90},
		},
	}
	manager := NewManager(Options{LocalNodeID: 1, Store: store, ShardCount: 1})
	target := RouteTarget{HashSlot: 1, SlotID: 10, LeaderNodeID: 1, RouteRevision: 2, AuthorityEpoch: 3}
	if err := manager.MarkActive(target); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	if err := manager.AdmitActiveBatch(context.Background(), target, ActiveBatch{
		Event: channelwritecontract.CommittedEnvelope{
			MessageSeq:       5,
			ChannelID:        "g1",
			ChannelType:      2,
			FromUID:          "u1",
			ServerTimestampMS: 300,
		},
		Recipients: []channelwritecontract.Recipient{{UID: "u1"}},
	}); err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	page, err := manager.ListActiveView(context.Background(), target, "u1", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListActiveView() error = %v", err)
	}
	if got := activeRowIDs(page.Rows); len(got) != 2 || got[0] != "g1" || got[1] != "g2" {
		t.Fatalf("row order = %#v, want cache-updated g1 before g2", got)
	}
	if page.Rows[0].ActiveAt != 300 || page.Rows[0].ReadSeq != 5 {
		t.Fatalf("merged g1 = %#v, want active_at 300 read_seq 5", page.Rows[0])
	}
}

type recordingActiveStore struct {
	rows       []metadb.UserConversationState
	listCalls  []activeListCall
	touchCalls [][]metadb.UserConversationActivePatch
}

type activeListCall struct {
	uid   string
	after metadb.UserConversationActiveCursor
	limit int
}

func (s *recordingActiveStore) ListUserConversationActivePage(_ context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) ([]metadb.UserConversationState, metadb.UserConversationActiveCursor, bool, error) {
	s.listCalls = append(s.listCalls, activeListCall{uid: uid, after: after, limit: limit})
	rows := append([]metadb.UserConversationState(nil), s.rows...)
	if len(rows) > limit {
		rows = rows[:limit]
		return rows, cursorFromState(rows[len(rows)-1]), false, nil
	}
	if len(rows) == 0 {
		return nil, metadb.UserConversationActiveCursor{}, true, nil
	}
	return rows, cursorFromState(rows[len(rows)-1]), true, nil
}

func (s *recordingActiveStore) TouchUserConversationActiveAtBatch(_ context.Context, patches []metadb.UserConversationActivePatch) error {
	s.touchCalls = append(s.touchCalls, append([]metadb.UserConversationActivePatch(nil), patches...))
	return nil
}

func activeRowIDs(rows []metadb.UserConversationState) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ChannelID)
	}
	return out
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internalv2/runtime/conversationactive -run 'TestListActiveView' -count=1
```

Expected: FAIL because `ListActiveView` does not exist.

- [ ] **Step 3: Implement list merge**

Append these functions to `internalv2/runtime/conversationactive/manager.go`:

```go
// ListActiveView returns DB active rows merged with unflushed cache rows.
func (m *Manager) ListActiveView(ctx context.Context, target RouteTarget, uid string, after metadb.UserConversationActiveCursor, limit int) (ActiveViewPage, error) {
	if m == nil || m.store == nil {
		return ActiveViewPage{}, ErrRouteNotReady
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ActiveViewPage{}, err
	}
	if err := m.ensureLocalTarget(target); err != nil {
		return ActiveViewPage{}, err
	}
	if limit <= 0 {
		limit = 1
	}
	cacheRows := m.cacheRowsAfter(uid, after)
	dbLimit := limit + len(cacheRows) + 1
	if dbLimit > m.listDBWindowMax {
		return ActiveViewPage{}, ErrCachePressure
	}
	dbRows, _, done, err := m.store.ListUserConversationActivePage(ctx, uid, after, dbLimit)
	if err != nil {
		return ActiveViewPage{}, err
	}
	merged := mergeActiveRows(dbRows, cacheRows)
	sortActiveRows(merged)
	if len(merged) > limit {
		merged = merged[:limit]
		done = false
	}
	return ActiveViewPage{Rows: merged, Cursor: cursorFromRows(merged, after), Done: done}, nil
}

func (m *Manager) cacheRowsAfter(uid string, after metadb.UserConversationActiveCursor) []metadb.UserConversationState {
	shard := m.shardForUID(uid)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	rows := shard.byUID[uid]
	out := make([]metadb.UserConversationState, 0, len(rows))
	for _, entry := range rows {
		row := stateFromEntry(entry.ActiveEntry)
		if activeRowAfter(row, after) {
			out = append(out, row)
		}
	}
	return out
}

func mergeActiveRows(dbRows, cacheRows []metadb.UserConversationState) []metadb.UserConversationState {
	merged := make(map[activeKey]metadb.UserConversationState, len(dbRows)+len(cacheRows))
	for _, row := range dbRows {
		merged[activeKey{channelID: row.ChannelID, channelType: row.ChannelType}] = row
	}
	for _, row := range cacheRows {
		key := activeKey{channelID: row.ChannelID, channelType: row.ChannelType}
		existing, ok := merged[key]
		if !ok || row.ActiveAt > existing.ActiveAt {
			merged[key] = row
			continue
		}
		if row.ActiveAt == existing.ActiveAt && row.ReadSeq > existing.ReadSeq {
			existing.ReadSeq = row.ReadSeq
			merged[key] = existing
		}
	}
	out := make([]metadb.UserConversationState, 0, len(merged))
	for _, row := range merged {
		out = append(out, row)
	}
	return out
}

func sortActiveRows(rows []metadb.UserConversationState) {
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

func activeRowAfter(row metadb.UserConversationState, after metadb.UserConversationActiveCursor) bool {
	if after == (metadb.UserConversationActiveCursor{}) {
		return true
	}
	if row.ActiveAt != after.ActiveAt {
		return row.ActiveAt < after.ActiveAt
	}
	if row.ChannelID != after.ChannelID {
		return row.ChannelID > after.ChannelID
	}
	return row.ChannelType > after.ChannelType
}

func cursorFromRows(rows []metadb.UserConversationState, fallback metadb.UserConversationActiveCursor) metadb.UserConversationActiveCursor {
	if len(rows) == 0 {
		return fallback
	}
	return cursorFromState(rows[len(rows)-1])
}

func cursorFromState(row metadb.UserConversationState) metadb.UserConversationActiveCursor {
	return metadb.UserConversationActiveCursor{ActiveAt: row.ActiveAt, ChannelID: row.ChannelID, ChannelType: row.ChannelType}
}
```

- [ ] **Step 4: Run list tests**

Run:

```bash
go test ./internalv2/runtime/conversationactive -run 'TestListActiveView' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit list merge**

```bash
git add internalv2/runtime/conversationactive
git commit -m "feat(internalv2): list conversation active cache rows"
```

## Task 3: Add Dirty Flush and Pressure Spill

**Files:**
- Create: `internalv2/runtime/conversationactive/flush.go`
- Modify: `internalv2/runtime/conversationactive/manager.go`
- Test: `internalv2/runtime/conversationactive/flush_test.go`

- [ ] **Step 1: Add failing flush tests**

Create `internalv2/runtime/conversationactive/flush_test.go`:

```go
package conversationactive

import (
	"context"
	"errors"
	"testing"

	channelwritecontract "github.com/WuKongIM/WuKongIM/internalv2/contracts/channelwrite"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestFlushDirtyPersistsActiveRowsAndClearsDirty(t *testing.T) {
	store := &recordingActiveStore{}
	manager := NewManager(Options{LocalNodeID: 1, Store: store, ShardCount: 1})
	target := RouteTarget{HashSlot: 1, SlotID: 10, LeaderNodeID: 1, RouteRevision: 2, AuthorityEpoch: 3}
	if err := manager.MarkActive(target); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	if err := manager.AdmitActiveBatch(context.Background(), target, ActiveBatch{
		Event: channelwritecontract.CommittedEnvelope{MessageSeq: 11, ChannelID: "g1", ChannelType: 2, FromUID: "u1", ServerTimestampMS: 900},
		Recipients: []channelwritecontract.Recipient{{UID: "u1"}},
	}); err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	if err := manager.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if len(store.touchCalls) != 1 || len(store.touchCalls[0]) != 1 {
		t.Fatalf("touch calls = %#v, want one patch", store.touchCalls)
	}
	patch := store.touchCalls[0][0]
	if patch.UID != "u1" || patch.ChannelID != "g1" || patch.ActiveAt != 900 || patch.ReadSeq != 11 {
		t.Fatalf("patch = %#v, want active_at/read_seq", patch)
	}
	if dirty := manager.DirtyCountForTest(); dirty != 0 {
		t.Fatalf("dirty count = %d, want 0 after successful flush", dirty)
	}
}

func TestFlushFailureKeepsDirty(t *testing.T) {
	store := &recordingActiveStore{touchErr: errors.New("disk unavailable")}
	manager := NewManager(Options{LocalNodeID: 1, Store: store, ShardCount: 1})
	target := RouteTarget{HashSlot: 1, SlotID: 10, LeaderNodeID: 1, RouteRevision: 2, AuthorityEpoch: 3}
	if err := manager.MarkActive(target); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	if err := manager.AdmitActiveBatch(context.Background(), target, ActiveBatch{
		Event: channelwritecontract.CommittedEnvelope{ChannelID: "g1", ChannelType: 2, FromUID: "u1", ServerTimestampMS: 900},
		Recipients: []channelwritecontract.Recipient{{UID: "u1"}},
	}); err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	if err := manager.Flush(context.Background()); !errors.Is(err, store.touchErr) {
		t.Fatalf("Flush() error = %v, want touchErr", err)
	}
	if dirty := manager.DirtyCountForTest(); dirty != 1 {
		t.Fatalf("dirty count = %d, want dirty retained after failed flush", dirty)
	}
}
```

Extend the `recordingActiveStore` helper in `manager_test.go`:

```go
touchErr error
```

and update `TouchUserConversationActiveAtBatch`:

```go
func (s *recordingActiveStore) TouchUserConversationActiveAtBatch(_ context.Context, patches []metadb.UserConversationActivePatch) error {
	s.touchCalls = append(s.touchCalls, append([]metadb.UserConversationActivePatch(nil), patches...))
	return s.touchErr
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internalv2/runtime/conversationactive -run 'TestFlush' -count=1
```

Expected: FAIL because `Flush` and `DirtyCountForTest` do not exist.

- [ ] **Step 3: Implement versioned dirty flush**

Create `internalv2/runtime/conversationactive/flush.go`:

```go
package conversationactive

import (
	"context"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

type dirtySnapshot struct {
	shardIndex int
	uid        string
	key        activeKey
	version    uint64
	entry      ActiveEntry
}

// Flush persists a snapshot of dirty active rows.
func (m *Manager) Flush(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if m.store == nil {
		return ErrRouteNotReady
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshot := m.dirtySnapshot()
	if len(snapshot) == 0 {
		return nil
	}
	patches := make([]metadb.UserConversationActivePatch, 0, len(snapshot))
	for _, item := range snapshot {
		patches = append(patches, metadb.UserConversationActivePatch{
			UID:         item.entry.UID,
			ChannelID:   item.entry.ChannelID,
			ChannelType: item.entry.ChannelType,
			ActiveAt:    item.entry.ActiveAt,
			UpdatedAt:   item.entry.ActiveAt,
			ReadSeq:     item.entry.ReadSeq,
		})
	}
	if err := m.store.TouchUserConversationActiveAtBatch(ctx, patches); err != nil {
		return err
	}
	m.clearDirty(snapshot)
	return nil
}

func (m *Manager) dirtySnapshot() []dirtySnapshot {
	out := make([]dirtySnapshot, 0)
	for i := range m.shards {
		shard := &m.shards[i]
		shard.mu.Lock()
		for uid, rows := range shard.byUID {
			for key, entry := range rows {
				if !entry.dirty {
					continue
				}
				out = append(out, dirtySnapshot{
					shardIndex: i,
					uid:        uid,
					key:        key,
					version:    entry.version,
					entry:      entry.ActiveEntry,
				})
			}
		}
		shard.mu.Unlock()
	}
	return out
}

func (m *Manager) clearDirty(snapshot []dirtySnapshot) {
	for _, item := range snapshot {
		shard := &m.shards[item.shardIndex]
		shard.mu.Lock()
		rows := shard.byUID[item.uid]
		entry, ok := rows[item.key]
		if ok && entry.version == item.version {
			entry.dirty = false
			rows[item.key] = entry
		}
		shard.mu.Unlock()
	}
}

// DirtyCountForTest returns the number of dirty cache entries.
func (m *Manager) DirtyCountForTest() int {
	if m == nil {
		return 0
	}
	count := 0
	for i := range m.shards {
		shard := &m.shards[i]
		shard.mu.Lock()
		for _, rows := range shard.byUID {
			for _, entry := range rows {
				if entry.dirty {
					count++
				}
			}
		}
		shard.mu.Unlock()
	}
	return count
}
```

- [ ] **Step 4: Run flush tests**

Run:

```bash
go test ./internalv2/runtime/conversationactive -run 'TestFlush' -count=1
```

Expected: PASS.

- [ ] **Step 5: Add pressure spill test**

Append to `flush_test.go`:

```go
func TestAdmitUnderCachePressureSpillsDirtyRows(t *testing.T) {
	store := &recordingActiveStore{}
	manager := NewManager(Options{LocalNodeID: 1, Store: store, ShardCount: 1, MaxRowsPerUID: 1, MaxRows: 1})
	target := RouteTarget{HashSlot: 1, SlotID: 10, LeaderNodeID: 1, RouteRevision: 2, AuthorityEpoch: 3}
	if err := manager.MarkActive(target); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	if err := manager.AdmitActiveBatch(context.Background(), target, ActiveBatch{
		Event: channelwritecontract.CommittedEnvelope{ChannelID: "g1", ChannelType: 2, FromUID: "u1", ServerTimestampMS: 100},
		Recipients: []channelwritecontract.Recipient{{UID: "u1"}},
	}); err != nil {
		t.Fatalf("first AdmitActiveBatch() error = %v", err)
	}
	if err := manager.AdmitActiveBatch(context.Background(), target, ActiveBatch{
		Event: channelwritecontract.CommittedEnvelope{ChannelID: "g2", ChannelType: 2, FromUID: "u1", ServerTimestampMS: 200},
		Recipients: []channelwritecontract.Recipient{{UID: "u1"}},
	}); err != nil {
		t.Fatalf("second AdmitActiveBatch() error = %v", err)
	}
	if len(store.touchCalls) == 0 {
		t.Fatalf("touch calls = 0, want pressure spill before admitting second row")
	}
}
```

- [ ] **Step 6: Implement bounded pressure spill**

In `Manager`, add total row accounting:

```go
totalRows int
```

In `AdmitActiveBatch`, before inserting a new key, add:

```go
_, exists := rows[key]
if !exists && (len(rows) >= m.maxRowsPerUID || m.totalRows >= m.maxRows) {
	shard.mu.Unlock()
	if err := m.Flush(ctx); err != nil {
		return ErrCachePressure
	}
	shard.mu.Lock()
	rows = shard.byUID[patch.UID]
	if rows == nil {
		rows = make(map[activeKey]activeEntry)
		shard.byUID[patch.UID] = rows
	}
	_, exists = rows[key]
	if !exists && (len(rows) >= m.maxRowsPerUID || m.totalRows >= m.maxRows) {
		shard.mu.Unlock()
		return ErrCachePressure
	}
}
if !exists {
	m.totalRows++
}
```

In `Flush`, do not decrement `totalRows`; flushed rows remain cached and clean.
Eviction can be designed later if pressure metrics show the cache needs it.

- [ ] **Step 7: Run pressure and flush tests**

Run:

```bash
go test ./internalv2/runtime/conversationactive -run 'TestFlush|TestAdmitUnderCachePressure' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit flush and pressure**

```bash
git add internalv2/runtime/conversationactive
git commit -m "feat(internalv2): flush conversation active cache"
```

## Task 4: Wire Conversation Active Runtime Into App Reads

**Files:**
- Modify: `internalv2/app/conversation_authority.go`
- Modify: `internalv2/app/conversation_route_lifecycle.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/app.go`
- Test: `internalv2/app/app_test.go`
- Test: `internalv2/app/conversation_api_smoke_test.go`

- [ ] **Step 1: Add failing app-level list-before-flush test**

Add this test to `internalv2/app/app_test.go`:

```go
func TestConversationActiveRuntimeListSeesCacheBeforeFlush(t *testing.T) {
	store := &appRecordingConversationAuthorityStore{}
	active := conversationactive.NewManager(conversationactive.Options{
		LocalNodeID: 1,
		Store:       store,
		ShardCount:  1,
	})
	target := conversationactive.RouteTarget{HashSlot: 1, SlotID: 1, LeaderNodeID: 1, RouteRevision: 1, AuthorityEpoch: 1}
	if err := active.MarkActive(target); err != nil {
		t.Fatalf("MarkActive() error = %v", err)
	}
	if err := active.AdmitActiveBatch(context.Background(), target, conversationactive.ActiveBatch{
		Event: channelwrite.CommittedEnvelope{
			MessageSeq:       8,
			ChannelID:        "g-cache",
			ChannelType:      2,
			FromUID:          "u-cache",
			ServerTimestampMS: 1000,
		},
		Recipients: []channelwrite.Recipient{{UID: "u-cache"}},
	}); err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}

	adapter := conversationActiveStoreAdapter{active: active}
	page, err := adapter.ListUserConversationActiveView(context.Background(), "u-cache", metadb.UserConversationActiveCursor{}, 10)
	if err != nil {
		t.Fatalf("ListUserConversationActiveView() error = %v", err)
	}
	if len(page.Rows) != 1 || page.Rows[0].ChannelID != "g-cache" || page.Rows[0].ReadSeq != 8 {
		t.Fatalf("active page rows = %#v, want cache row with sender read seq", page.Rows)
	}
	if store.totalTouchPatches() != 0 {
		t.Fatalf("touch patches = %d, want no DB flush before read", store.totalTouchPatches())
	}
}
```

Add imports to `internalv2/app/app_test.go`:

```go
	"github.com/WuKongIM/WuKongIM/internalv2/runtime/conversationactive"
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internalv2/app -run TestConversationActiveRuntimeListSeesCacheBeforeFlush -count=1
```

Expected: FAIL because `conversationActiveStoreAdapter` and app wiring do not exist.

- [ ] **Step 3: Add app adapter from runtime page to usecase page**

In `internalv2/app/conversation_authority.go`, add:

```go
type conversationActiveStoreAdapter struct {
	active *conversationactive.Manager
}

func (a conversationActiveStoreAdapter) ListUserConversationActiveView(ctx context.Context, uid string, after metadb.UserConversationActiveCursor, limit int) (conversationusecase.ActiveViewPage, error) {
	if a.active == nil {
		return conversationusecase.ActiveViewPage{}, conversationusecase.ErrRouteNotReady
	}
	target, err := a.activeTargetForUID(uid)
	if err != nil {
		return conversationusecase.ActiveViewPage{}, mapConversationActiveError(err)
	}
	page, err := a.active.ListActiveView(ctx, target, uid, after, limit)
	if err != nil {
		return conversationusecase.ActiveViewPage{}, mapConversationActiveError(err)
	}
	return conversationusecase.ActiveViewPage{Rows: page.Rows, Cursor: page.Cursor, Done: page.Done}, nil
}

func (a conversationActiveStoreAdapter) activeTargetForUID(uid string) (conversationactive.RouteTarget, error) {
	if a.active == nil {
		return conversationactive.RouteTarget{}, conversationactive.ErrRouteNotReady
	}
	return a.active.TargetForTest(uid), nil
}

func mapConversationActiveError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, conversationactive.ErrNotLeader):
		return conversationusecase.ErrNotLeader
	case errors.Is(err, conversationactive.ErrStaleRoute):
		return conversationusecase.ErrStaleRoute
	case errors.Is(err, conversationactive.ErrCachePressure):
		return conversationusecase.ErrCachePressure
	case errors.Is(err, conversationactive.ErrRouteNotReady):
		return conversationusecase.ErrRouteNotReady
	default:
		return err
	}
}
```

Add this local-target helper to `manager.go` for app adapter tests:

```go
// TargetForTest returns a local active target for adapter tests.
func (m *Manager) TargetForTest(string) RouteTarget {
	if m == nil {
		return RouteTarget{}
	}
	return RouteTarget{LeaderNodeID: m.localNodeID}
}
```

Task 5 replaces callers of `TargetForTest` with cluster route resolution before
the feature is considered complete.

- [ ] **Step 4: Run app adapter test**

Run:

```bash
go test ./internalv2/app -run TestConversationActiveRuntimeListSeesCacheBeforeFlush -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit app read adapter**

```bash
git add internalv2/runtime/conversationactive internalv2/app
git commit -m "feat(internalv2): expose conversation active cache to list reads"
```

## Task 5: Add Cluster-Routed Active Authority Adapter

**Files:**
- Modify: `internalv2/access/node/conversation_rpc.go`
- Modify: `internalv2/access/node/conversation_codec.go`
- Modify: `internalv2/infra/cluster/conversation_authority.go`
- Modify: `internalv2/app/conversation_authority.go`
- Test: `internalv2/access/node/conversation_rpc_test.go`
- Test: `internalv2/infra/cluster/conversation_authority_test.go`

- [ ] **Step 1: Add failing active admit RPC test**

In `internalv2/access/node/conversation_rpc_test.go`, add:

```go
func TestConversationActiveAdmitRPCUpdatesRemoteCache(t *testing.T) {
	target := conversationusecase.RouteTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5}
	active := &recordingConversationActiveAuthority{}
	adapter := New(Options{ConversationActive: active})

	req := conversationActiveAdmitRequest{
		Target: target,
		Batch: conversationactive.ActiveBatch{
			Event: channelwrite.CommittedEnvelope{ChannelID: "g1", ChannelType: 2, FromUID: "u1", ServerTimestampMS: 100},
			Recipients: []channelwrite.Recipient{{UID: "u2"}},
		},
	}
	body, err := encodeConversationActiveAdmitRequest(req)
	if err != nil {
		t.Fatalf("encode request error = %v", err)
	}
	_, err = adapter.HandleConversationActiveAdmitRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleConversationActiveAdmitRPC() error = %v", err)
	}
	if active.calls != 1 || active.target != target || active.batch.Event.ChannelID != "g1" {
		t.Fatalf("active call = %d target=%#v batch=%#v", active.calls, active.target, active.batch)
	}
}
```

Use existing conversation authority codec patterns in `conversation_codec.go` for deterministic binary encoding. The request must carry target, event fields, and recipient UID/join sequence pairs.

- [ ] **Step 2: Run RPC test to verify it fails**

Run:

```bash
go test ./internalv2/access/node -run TestConversationActiveAdmitRPCUpdatesRemoteCache -count=1
```

Expected: FAIL because the active RPC surface does not exist.

- [ ] **Step 3: Add RPC surface**

Add to `internalv2/access/node/conversation_rpc.go`:

```go
const ConversationActiveAdmitRPCServiceID uint8 = 0x36

type ConversationActiveAuthority interface {
	AdmitActiveBatch(context.Context, conversationusecase.RouteTarget, conversationactive.ActiveBatch) error
}

func (a *Adapter) HandleConversationActiveAdmitRPC(ctx context.Context, body []byte) ([]byte, error) {
	req, err := decodeConversationActiveAdmitRequest(body)
	if err != nil {
		return nil, err
	}
	if a.conversationActive == nil {
		return encodeConversationStatus(conversationStatusRouteNotReady), nil
	}
	err = a.conversationActive.AdmitActiveBatch(ctx, req.Target, req.Batch)
	return encodeConversationStatus(conversationStatusFromError(err)), nil
}
```

Extend `Options` and `Adapter` in `internalv2/access/node` with:

```go
ConversationActive ConversationActiveAuthority
```

- [ ] **Step 4: Add infra client forwarding**

In `internalv2/infra/cluster/conversation_authority.go`, add a method on `ConversationAuthorityClient`:

```go
func (c *ConversationAuthorityClient) AdmitActiveBatch(ctx context.Context, target conversationusecase.RouteTarget, batch conversationactive.ActiveBatch) error {
	authority, err := c.authorityForTarget(target)
	if err != nil {
		return err
	}
	if active, ok := authority.(interface {
		AdmitActiveBatch(context.Context, conversationusecase.RouteTarget, conversationactive.ActiveBatch) error
	}); ok {
		return active.AdmitActiveBatch(ctx, target, batch)
	}
	return conversationusecase.ErrRouteNotReady
}
```

Add a remote implementation that calls `accessnode.Client.AdmitConversationActiveBatch`.

- [ ] **Step 5: Run access and infra tests**

Run:

```bash
go test ./internalv2/access/node ./internalv2/infra/cluster -run 'ConversationActive|AdmitActive' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit active RPC routing**

```bash
git add internalv2/access/node internalv2/infra/cluster internalv2/app
git commit -m "feat(internalv2): route conversation active admissions"
```

## Task 6: Change Channelwrite to Admit Active Batches

**Files:**
- Modify: `internalv2/runtime/channelwrite/options.go`
- Modify: `internalv2/runtime/channelwrite/commit.go`
- Modify: `internalv2/runtime/channelwrite/recipient.go`
- Modify: `internalv2/runtime/channelwrite/delivery.go`
- Test: `internalv2/runtime/channelwrite/recipient_test.go`
- Test: `internalv2/runtime/channelwrite/delivery_test.go`

- [ ] **Step 1: Add failing active handoff test**

Add to `internalv2/runtime/channelwrite/recipient_test.go`:

```go
func TestCommittedRecipientsAdmitConversationActiveWithSender(t *testing.T) {
	active := &recordingActiveAdmitterForRecipientTest{}
	err := dispatchCommittedRecipients(context.Background(), CommittedEnvelope{
		MessageID:          1,
		MessageSeq:         9,
		ChannelID:          "g1",
		ChannelType:        2,
		FromUID:            "sender",
		ServerTimestampMS:   1000,
		MessageScopedUIDs:  []string{"receiver"},
	}, commitPorts{
		recipientAuthorityResolver: staticRecipientAuthorityResolverForRecipientTest{target: recipientAuthorityTargetForTest(1, 7, 1)},
		conversationActive:         active,
		recipientBatchSize:         16,
	})
	if err != nil {
		t.Fatalf("dispatchCommittedRecipients() error = %v", err)
	}
	if active.calls != 1 {
		t.Fatalf("active calls = %d, want 1", active.calls)
	}
	if got := recipientUIDsForTest(active.batch.Recipients); !reflect.DeepEqual(got, []string{"receiver", "sender"}) {
		t.Fatalf("active recipients = %#v, want receiver + sender", got)
	}
}

type recordingActiveAdmitterForRecipientTest struct {
	calls  int
	target RecipientAuthorityTarget
	batch  RecipientBatch
}

func (a *recordingActiveAdmitterForRecipientTest) AdmitActiveBatch(_ context.Context, target RecipientAuthorityTarget, batch RecipientBatch) error {
	a.calls++
	a.target = target
	a.batch = batch.Clone()
	return nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internalv2/runtime/channelwrite -run TestCommittedRecipientsAdmitConversationActiveWithSender -count=1
```

Expected: FAIL because `commitPorts.conversationActive` does not exist.

- [ ] **Step 3: Add active admitter port**

In `internalv2/runtime/channelwrite/options.go`, add:

```go
// ConversationActiveAdmitter admits expanded recipients into the recent-conversation active cache.
type ConversationActiveAdmitter interface {
	AdmitActiveBatch(context.Context, RecipientAuthorityTarget, RecipientBatch) error
}
```

Add to `Options`:

```go
// ConversationActive admits expanded recipients into the recent-conversation active cache.
ConversationActive ConversationActiveAdmitter
```

Add to `commitPorts` in `commit.go`:

```go
conversationActive ConversationActiveAdmitter
```

and wire it in `commitPortsFromOptions`.

- [ ] **Step 4: Admit active batch before delivery handoff**

In `dispatchRecipientTarget`, before calling `router.DispatchRecipientBatch`, add:

```go
activeBatch := batch.Clone()
activeBatch.Recipients = activeRecipientsForEvent(event, activeBatch.Recipients)
if ports.conversationActive != nil {
	if err := ports.conversationActive.AdmitActiveBatch(ctx, target, activeBatch); err != nil {
		detail := postCommitTargetDetail(target)
		detail.Phase = "conversation_active_admit"
		detail.UID = firstRecipientUID(activeBatch.Recipients)
		detail.RecipientCount = len(activeBatch.Recipients)
		return withPostCommitFailureDetail(err, detail)
	}
}
```

Add helper:

```go
func activeRecipientsForEvent(event CommittedEnvelope, recipients []Recipient) []Recipient {
	out := make([]Recipient, 0, len(recipients)+1)
	seen := make(map[string]struct{}, len(recipients)+1)
	for _, recipient := range recipients {
		uid := strings.TrimSpace(recipient.UID)
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		recipient.UID = uid
		out = append(out, recipient)
	}
	if uid := strings.TrimSpace(event.FromUID); uid != "" {
		if _, ok := seen[uid]; !ok {
			out = append(out, Recipient{UID: uid})
		}
	}
	return out
}
```

- [ ] **Step 5: Remove conversation projection from RecipientProcessor**

In `internalv2/runtime/channelwrite/delivery.go`, remove:

```go
ConversationProjector ConversationProjector
conversations ConversationProjector
ErrConversationProjectorRequired
conversationPatchesForRecipients
```

Change `processRecipientBatch` so it only resolves presence and pushes delivery when delivery ports are configured. Delivery must no longer require a conversation projector.

- [ ] **Step 6: Update delivery tests**

Replace `TestRecipientProcessorUpdatesConversationBeforeResolvingAndPushingDelivery` with a delivery-only ordering test:

```go
func TestRecipientProcessorResolvesPresenceAndPushesDelivery(t *testing.T) {
	resolver := &recordingPresenceResolverForDeliveryTest{
		routes: []Route{{UID: "u2", OwnerNodeID: 3, SessionID: 20}},
	}
	pusher := &recordingOwnerPusherForDeliveryTest{}
	err := processRecipientBatch(context.Background(), RecipientBatch{
		Event: CommittedEnvelope{MessageID: 1, MessageSeq: 2, ChannelID: "g1", ChannelType: 2, FromUID: "u1"},
		Recipients: []Recipient{{UID: "u2"}},
	}, recipientPorts{presence: resolver, pusher: pusher})
	if err != nil {
		t.Fatalf("processRecipientBatch() error = %v", err)
	}
	if pusher.callCount() != 1 {
		t.Fatalf("push calls = %d, want 1", pusher.callCount())
	}
}
```

Delete `TestDeliveryRequiresConversationProjectorWhenPushConfigured`.

- [ ] **Step 7: Run channelwrite tests**

Run:

```bash
go test ./internalv2/runtime/channelwrite -run 'Recipient|Delivery|Commit' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit channelwrite active handoff**

```bash
git add internalv2/runtime/channelwrite
git commit -m "refactor(internalv2): hand off conversation active batches from channelwrite"
```

## Task 7: Wire App Channelwrite to Conversation Active

**Files:**
- Modify: `internalv2/app/channel_write.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/app.go`
- Test: `internalv2/app/channel_write_test.go`
- Test: `internalv2/app/conversation_api_smoke_test.go`

- [ ] **Step 1: Add failing app adapter test**

Replace `TestChannelWriteConversationProjectorAdmitsPatchesAndLogsFailure` in `internalv2/app/channel_write_test.go` with:

```go
func TestChannelWriteConversationActiveAdmitterForwardsBatch(t *testing.T) {
	target := channelwrite.RecipientAuthorityTarget{HashSlot: 1, SlotID: 2, LeaderNodeID: 3, RouteRevision: 4, AuthorityEpoch: 5}
	active := &recordingConversationActiveClient{}
	adapter := channelWriteConversationActiveAdmitter{client: active}

	err := adapter.AdmitActiveBatch(context.Background(), target, channelwrite.RecipientBatch{
		Event: channelwrite.CommittedEnvelope{ChannelID: "g1", ChannelType: 2, FromUID: "u1", ServerTimestampMS: 100},
		Recipients: []channelwrite.Recipient{{UID: "u2"}},
	})
	if err != nil {
		t.Fatalf("AdmitActiveBatch() error = %v", err)
	}
	if active.calls != 1 || active.target.LeaderNodeID != 3 || active.batch.Event.ChannelID != "g1" {
		t.Fatalf("active calls=%d target=%#v batch=%#v", active.calls, active.target, active.batch)
	}
}

type recordingConversationActiveClient struct {
	calls  int
	target conversationusecase.RouteTarget
	batch  conversationactive.ActiveBatch
}

func (c *recordingConversationActiveClient) AdmitActiveBatch(_ context.Context, target conversationusecase.RouteTarget, batch conversationactive.ActiveBatch) error {
	c.calls++
	c.target = target
	c.batch = batch
	return nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
go test ./internalv2/app -run TestChannelWriteConversationActiveAdmitterForwardsBatch -count=1
```

Expected: FAIL because the adapter does not exist.

- [ ] **Step 3: Add channelwrite active adapter**

In `internalv2/app/channel_write.go`, replace `channelWriteConversationProjector` with:

```go
type channelWriteConversationActiveAdmitter struct {
	client channelWriteConversationActiveClient
}

type channelWriteConversationActiveClient interface {
	AdmitActiveBatch(context.Context, conversationusecase.RouteTarget, conversationactive.ActiveBatch) error
}

func (a channelWriteConversationActiveAdmitter) AdmitActiveBatch(ctx context.Context, target channelwrite.RecipientAuthorityTarget, batch channelwrite.RecipientBatch) error {
	if a.client == nil {
		return nil
	}
	return a.client.AdmitActiveBatch(ctx, conversationusecase.RouteTarget{
		HashSlot:       target.HashSlot,
		SlotID:         target.SlotID,
		LeaderNodeID:   target.LeaderNodeID,
		RouteRevision:  target.RouteRevision,
		AuthorityEpoch: target.AuthorityEpoch,
	}, conversationactive.ActiveBatch{
		Event:      batch.Event,
		Recipients: append([]channelwrite.Recipient(nil), batch.Recipients...),
	})
}
```

- [ ] **Step 4: Wire channelwrite options**

In `wireChannelWrite`, replace:

```go
opts.ConversationProjector = channelWriteConversationProjector{...}
```

with:

```go
if a.conversationAuthorityClient != nil {
	opts.ConversationActive = channelWriteConversationActiveAdmitter{client: a.conversationAuthorityClient}
}
```

When constructing `channelwrite.NewRecipientProcessor`, stop passing conversation options.

- [ ] **Step 5: Run app channelwrite tests**

Run:

```bash
go test ./internalv2/app -run 'ChannelWriteConversationActive|ConversationListAPIReadsAuthorityCacheAfterRecipientDispatch' -count=1
```

Expected: PASS after updating the smoke test name or expected log strings from authority cache to active cache.

- [ ] **Step 6: Commit app wiring**

```bash
git add internalv2/app
git commit -m "feat(internalv2): wire conversation active runtime"
```

## Task 8: Update Docs, Config Comments, and Full Verification

**Files:**
- Create: `internalv2/runtime/conversationactive/FLOW.md`
- Modify: `internalv2/runtime/channelwrite/FLOW.md`
- Modify: `internalv2/usecase/conversation/FLOW.md`
- Modify: `internalv2/app/FLOW.md`
- Modify: `docs/superpowers/specs/2026-06-10-channelwrite-conversation-active-worker-design.md` only when a completed task intentionally changes the approved design.

- [ ] **Step 1: Add runtime FLOW**

Create `internalv2/runtime/conversationactive/FLOW.md`:

```markdown
# internalv2/runtime/conversationactive Flow

## Responsibility

`internalv2/runtime/conversationactive` owns UID-authority recent conversation
active rows. It admits expanded recipient batches after durable append, updates
its own in-memory active cache synchronously, serves active-list reads by
merging cache rows with DB rows, and flushes dirty active rows to storage.

It does not scan subscribers, resolve presence, push delivery, hydrate last
messages, or own generic unread/read-state semantics.

## Admit Flow

```text
AdmitActiveBatch(target, batch)
  -> validate exact UID authority target
  -> build active participants from expanded recipients plus sender UID
  -> set ActiveAt from committed ServerTimestampMS
  -> set ReadSeq only for sender from committed MessageSeq
  -> merge into UID-sharded cache
  -> mark changed rows dirty
  -> return after cache is readable
```

## List Flow

```text
ListActiveView(uid, cursor, limit)
  -> validate exact UID authority target
  -> read UID cache rows after cursor
  -> read DB active rows after cursor
  -> merge by (uid, channel_id, channel_type)
  -> sort by active_at desc, channel_id asc, channel_type asc
  -> return rows for last-message hydration
```

## Flush Flow

```text
Flush
  -> snapshot dirty rows with versions
  -> persist active_at/read_seq patches
  -> clear dirty only when version did not advance during flush
```
```

- [ ] **Step 2: Update existing FLOW files**

Update the existing flow docs with these statements:

- `internalv2/runtime/channelwrite/FLOW.md`: post-commit now expands recipients and calls conversation active admission; recent-conversation cache/list/flush is not owned by channelwrite.
- `internalv2/usecase/conversation/FLOW.md`: active list Store may be backed by `conversationactive` and sees cache rows before DB flush.
- `internalv2/app/FLOW.md`: app wires `conversationactive.Manager`, route/RPC adapters, and channelwrite active admitter.

- [ ] **Step 3: Run focused verification**

Run:

```bash
go test ./internalv2/runtime/conversationactive ./internalv2/runtime/channelwrite ./internalv2/app ./internalv2/access/node ./internalv2/infra/cluster -count=1
```

Expected: PASS.

- [ ] **Step 4: Run broader internalv2 verification**

Run:

```bash
go test ./internalv2/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit docs and final fixes**

```bash
git add internalv2/runtime/conversationactive/FLOW.md internalv2/runtime/channelwrite/FLOW.md internalv2/usecase/conversation/FLOW.md internalv2/app/FLOW.md docs/superpowers/specs/2026-06-10-channelwrite-conversation-active-worker-design.md
git commit -m "docs: document conversation active worker flow"
```

## Final Verification

After all tasks are complete, run:

```bash
go test ./internalv2/... ./pkg/... -count=1
```

Expected: PASS.

If this is too slow for the working session, run:

```bash
go test ./internalv2/runtime/conversationactive ./internalv2/runtime/channelwrite ./internalv2/app ./internalv2/access/node ./internalv2/infra/cluster -count=1
```

and record that the broader package set was not run.
