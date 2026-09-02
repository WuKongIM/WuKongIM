package cluster

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

func TestChannelMetadataStoreUpdatesAppendCacheOnlyAfterCommittedMetadataMutation(t *testing.T) {
	t.Parallel()

	commitErr := errors.New("commit rejected")
	node := &contractChannelMetadataMutationNode{createErr: commitErr, patchErr: commitErr}
	cache := NewChannelAppendMetadataCache()
	store := NewChannelMetadataStore(node, cache, nil)
	channel := metadb.Channel{
		ChannelID: "g1", ChannelType: 2, Large: 1, SubscriberMutationVersion: 7,
	}
	id := channelappend.ChannelID{ID: "g1", Type: 2}

	if err := store.CreateChannelStrict(context.Background(), channel); !errors.Is(err, commitErr) {
		t.Fatalf("CreateChannelStrict(rejected) error = %v, want %v", err, commitErr)
	}
	if _, ok := cache.Lookup(id); ok {
		t.Fatal("rejected create populated append metadata cache")
	}

	node.createErr = nil
	if err := store.CreateChannelStrict(context.Background(), channel); err != nil {
		t.Fatalf("CreateChannelStrict() error = %v", err)
	}
	if metadata, ok := cache.Lookup(id); !ok || !metadata.Large || metadata.SubscriberMutationVersion != 7 {
		t.Fatalf("committed create cache = %#v ok=%v", metadata, ok)
	}

	if err := store.PatchChannelBusinessFlags(context.Background(), "g1", 2, metadb.ChannelBusinessFlags{Ban: 1}); !errors.Is(err, commitErr) {
		t.Fatalf("PatchChannelBusinessFlags(rejected) error = %v, want %v", err, commitErr)
	}
	if _, ok := cache.Lookup(id); !ok {
		t.Fatal("rejected patch invalidated a still-current cache entry")
	}

	node.patchErr = nil
	if err := store.PatchChannelBusinessFlags(context.Background(), "g1", 2, metadb.ChannelBusinessFlags{Ban: 1}); err != nil {
		t.Fatalf("PatchChannelBusinessFlags() error = %v", err)
	}
	if _, ok := cache.Lookup(id); ok {
		t.Fatal("committed business-flag patch left stale append metadata cached")
	}
}

func TestChannelMetadataStoreClonesSubscriberMutationsAndPreservesVersionFence(t *testing.T) {
	t.Parallel()

	node := &contractChannelMetadataMutationNode{mutateSubscriberInput: true}
	store := NewChannelMetadataStore(node, nil, nil)
	addUIDs := []string{"u1", "u2"}
	removeUIDs := []string{"u3", "u4"}

	if err := store.AddChannelSubscribers(context.Background(), "g1", 2, addUIDs, 17, 99); err != nil {
		t.Fatalf("AddChannelSubscribers() error = %v", err)
	}
	if err := store.RemoveChannelSubscribers(context.Background(), "g1", 2, removeUIDs, 18, 100); err != nil {
		t.Fatalf("RemoveChannelSubscribers() error = %v", err)
	}
	if addUIDs[0] != "u1" || removeUIDs[0] != "u3" {
		t.Fatalf("caller-owned UID slices mutated: add=%#v remove=%#v", addUIDs, removeUIDs)
	}
	if node.addVersion != 17 || node.removeVersion != 18 {
		t.Fatalf("mutation versions add=%d remove=%d, want first fences 17/18", node.addVersion, node.removeVersion)
	}
	if len(node.addUIDs) != 2 || len(node.removeUIDs) != 2 {
		t.Fatalf("forwarded UIDs add=%#v remove=%#v", node.addUIDs, node.removeUIDs)
	}
}

func TestChannelMetadataStoreRestoreReaderUsesRestoreCapabilityAndSafeFallback(t *testing.T) {
	t.Parallel()

	restoreNode := &contractChannelMetadataMutationNode{
		restoreUIDs: []string{"restored-u1"}, restoreNext: "after-u1", restoreDone: false,
	}
	restoreStore := NewChannelMetadataStore(restoreNode, nil, nil)
	uids, next, done, err := restoreStore.ListChannelSubscribersForRestore(context.Background(), "g1", 2, "before", 1)
	if err != nil || len(uids) != 1 || uids[0] != "restored-u1" || next != "after-u1" || done {
		t.Fatalf("restore-capable page = %#v next=%q done=%v err=%v", uids, next, done, err)
	}
	if restoreNode.restoreAfter != "before" || restoreNode.restoreLimit != 1 {
		t.Fatalf("restore cursor/limit = %q/%d", restoreNode.restoreAfter, restoreNode.restoreLimit)
	}

	fallback := &contractChannelMetadataFallbackNode{uids: []string{"local-u1"}}
	fallbackStore := NewChannelMetadataStore(fallback, nil, nil)
	uids, _, done, err = fallbackStore.ListChannelSubscribersForRestore(context.Background(), "g2", 3, "", 10)
	if err != nil || !done || len(uids) != 1 || uids[0] != "local-u1" || fallback.calls != 1 {
		t.Fatalf("fallback restore page = %#v done=%v calls=%d err=%v", uids, done, fallback.calls, err)
	}

	var nilStore *ChannelMetadataStore
	uids, next, done, err = nilStore.ListChannelSubscribersForRestore(context.Background(), "g3", 4, "cursor", 10)
	if err != nil || !done || len(uids) != 0 || next != "" {
		t.Fatalf("nil restore store = %#v next=%q done=%v err=%v", uids, next, done, err)
	}
}

func TestChannelMetadataStorePreservesMessagePullAndCommittedTailAuthority(t *testing.T) {
	t.Parallel()

	node := &recordingChannelMetadataNode{
		authoritativeChannel: metadb.Channel{ChannelID: "g1", ChannelType: 2, Disband: 1},
		committedTail:        91,
	}
	store := NewChannelMetadataStore(node, nil, nil)
	channel, err := store.GetChannelForMessagePull(context.Background(), "g1", 2)
	if err != nil || channel.Disband != 1 || node.authoritativeReadCalls != 1 || node.localReadCalls != 0 {
		t.Fatalf("GetChannelForMessagePull() = %#v reads=%d/%d err=%v", channel, node.authoritativeReadCalls, node.localReadCalls, err)
	}
	tail, err := store.CommittedChannelTail(context.Background(), "g1", 2)
	if err != nil || tail != 91 || node.committedTailCalls != 1 {
		t.Fatalf("CommittedChannelTail() = %d calls=%d err=%v", tail, node.committedTailCalls, err)
	}

	results := (*ChannelMetadataStore)(nil).ReadPermissionsBatch(context.Background(), []messageusecase.PermissionRead{{}, {}})
	if len(results) != 2 {
		t.Fatalf("nil permission result count = %d, want 2", len(results))
	}
	for index, result := range results {
		if !errors.Is(result.Err, channelappend.ErrRouteNotReady) {
			t.Fatalf("nil permission result[%d] error = %v, want route not ready", index, result.Err)
		}
	}
	if err := (*ChannelMetadataStore)(nil).Stop(context.Background()); err != nil {
		t.Fatalf("nil Stop() error = %v", err)
	}
}

func TestChannelMetadataStorePermissionBatchRejectsUnknownKindsBeforeProxy(t *testing.T) {
	t.Parallel()

	node := &recordingChannelMetadataNode{permissionBatchResults: []slotproxy.PermissionMetadataReadResult{{
		Found: true,
		Channel: metadb.Channel{
			ChannelID:   "g1",
			ChannelType: 2,
		},
	}}}
	store := NewChannelMetadataStore(node, nil, nil)

	results := store.ReadPermissionsBatch(context.Background(), []messageusecase.PermissionRead{
		{Kind: messageusecase.PermissionReadChannel, ChannelID: "g1", ChannelType: 2},
		{Kind: messageusecase.PermissionReadKind(255), ChannelID: "g1", ChannelType: 2},
	})

	if len(results) != 2 {
		t.Fatalf("ReadPermissionsBatch() result count = %d, want 2", len(results))
	}
	if !results[0].Found || results[0].Channel.ChannelID != "g1" || results[0].Err != nil {
		t.Fatalf("valid result = %#v, want authoritative channel fact", results[0])
	}
	if !errors.Is(results[1].Err, metadb.ErrInvalidArgument) {
		t.Fatalf("unknown-kind result error = %v, want %v", results[1].Err, metadb.ErrInvalidArgument)
	}
	if len(node.permissionBatchReads) != 1 || node.permissionBatchReads[0].Kind != slotproxy.PermissionMetadataReadChannel {
		t.Fatalf("proxy reads = %#v, want only the valid read", node.permissionBatchReads)
	}
}

func TestChannelMetadataStorePermissionBatchKeepsInvalidEvidenceWhenProxyCardinalityBreaks(t *testing.T) {
	t.Parallel()

	node := &recordingChannelMetadataNode{permissionBatchResults: []slotproxy.PermissionMetadataReadResult{}}
	store := NewChannelMetadataStore(node, nil, nil)
	results := store.ReadPermissionsBatch(context.Background(), []messageusecase.PermissionRead{
		{Kind: messageusecase.PermissionReadChannel, ChannelID: "g1", ChannelType: 2},
		{Kind: messageusecase.PermissionReadKind(255), ChannelID: "g1", ChannelType: 2},
		{Kind: messageusecase.PermissionReadSubscriberHasAny, ChannelID: "g1", ChannelType: 2},
	})

	if len(results) != 3 || results[0].Err == nil || results[2].Err == nil {
		t.Fatalf("valid cardinality failures = %#v", results)
	}
	if !errors.Is(results[1].Err, metadb.ErrInvalidArgument) {
		t.Fatalf("invalid-kind error = %v, want preserved %v", results[1].Err, metadb.ErrInvalidArgument)
	}
	if len(node.permissionBatchReads) != 2 {
		t.Fatalf("proxy read count = %d, want only two valid reads", len(node.permissionBatchReads))
	}
}

type contractChannelMetadataMutationNode struct {
	createErr             error
	patchErr              error
	mutateSubscriberInput bool
	addUIDs               []string
	removeUIDs            []string
	addVersion            uint64
	removeVersion         uint64
	restoreUIDs           []string
	restoreNext           string
	restoreDone           bool
	restoreAfter          string
	restoreLimit          int
}

func (*contractChannelMetadataMutationNode) GetChannelMetadata(context.Context, string, int64) (metadb.Channel, error) {
	return metadb.Channel{}, metadb.ErrNotFound
}

func (*contractChannelMetadataMutationNode) UpsertChannelMetadata(context.Context, metadb.Channel) error {
	return nil
}

func (*contractChannelMetadataMutationNode) DeleteChannelMetadata(context.Context, string, int64) error {
	return nil
}

func (n *contractChannelMetadataMutationNode) AddChannelSubscribers(_ context.Context, _ string, _ int64, uids []string, version uint64) error {
	n.addUIDs = append([]string(nil), uids...)
	n.addVersion = version
	if n.mutateSubscriberInput && len(uids) > 0 {
		uids[0] = "node-mutated"
	}
	return nil
}

func (n *contractChannelMetadataMutationNode) RemoveChannelSubscribers(_ context.Context, _ string, _ int64, uids []string, version uint64) error {
	n.removeUIDs = append([]string(nil), uids...)
	n.removeVersion = version
	if n.mutateSubscriberInput && len(uids) > 0 {
		uids[0] = "node-mutated"
	}
	return nil
}

func (*contractChannelMetadataMutationNode) ListChannelSubscribersPage(context.Context, string, int64, string, int) ([]string, string, bool, error) {
	return nil, "", true, nil
}

func (n *contractChannelMetadataMutationNode) CreateChannelMetadataStrict(context.Context, metadb.Channel) error {
	return n.createErr
}

func (n *contractChannelMetadataMutationNode) PatchChannelBusinessFlags(context.Context, string, int64, metadb.ChannelBusinessFlags) error {
	return n.patchErr
}

func (n *contractChannelMetadataMutationNode) ListRestoreChannelSubscribersPage(_ context.Context, _ string, _ int64, after string, limit int) ([]string, string, bool, error) {
	n.restoreAfter, n.restoreLimit = after, limit
	return append([]string(nil), n.restoreUIDs...), n.restoreNext, n.restoreDone, nil
}

type contractChannelMetadataFallbackNode struct {
	uids  []string
	calls int
}

func (*contractChannelMetadataFallbackNode) GetChannelMetadata(context.Context, string, int64) (metadb.Channel, error) {
	return metadb.Channel{}, metadb.ErrNotFound
}

func (*contractChannelMetadataFallbackNode) UpsertChannelMetadata(context.Context, metadb.Channel) error {
	return nil
}

func (*contractChannelMetadataFallbackNode) DeleteChannelMetadata(context.Context, string, int64) error {
	return nil
}

func (*contractChannelMetadataFallbackNode) AddChannelSubscribers(context.Context, string, int64, []string, uint64) error {
	return nil
}

func (*contractChannelMetadataFallbackNode) RemoveChannelSubscribers(context.Context, string, int64, []string, uint64) error {
	return nil
}

func (n *contractChannelMetadataFallbackNode) ListChannelSubscribersPage(context.Context, string, int64, string, int) ([]string, string, bool, error) {
	n.calls++
	return append([]string(nil), n.uids...), "", true, nil
}
