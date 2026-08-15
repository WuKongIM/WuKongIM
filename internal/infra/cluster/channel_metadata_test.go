package cluster

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestChannelMetadataStoreProjectsUserMemberships(t *testing.T) {
	node := &recordingChannelMetadataNode{}
	store := NewChannelMetadataStore(node, nil)

	if err := store.UpsertChannelMemberships(context.Background(), "g1", 2, []string{"u1", "u2"}, 9, 7, 123); err != nil {
		t.Fatalf("UpsertChannelMemberships(): %v", err)
	}
	if err := store.TombstoneChannelMemberships(context.Background(), "g1", 2, []string{"u1"}, 8, 456); err != nil {
		t.Fatalf("TombstoneChannelMemberships(): %v", err)
	}

	if got, want := node.membershipUpserts, []membershipUpsertNodeCall{{channelID: "g1", channelType: 2, uids: []string{"u1", "u2"}, committedTail: 9, sourceVersion: 7, updatedAt: 123}}; !equalMembershipUpsertNodeCalls(got, want) {
		t.Fatalf("membership upserts = %#v, want %#v", got, want)
	}
	if got, want := node.membershipDeletes, []membershipDeleteNodeCall{{channelID: "g1", channelType: 2, uids: []string{"u1"}, sourceVersion: 8, updatedAt: 456}}; !equalMembershipDeleteNodeCalls(got, want) {
		t.Fatalf("membership deletes = %#v, want %#v", got, want)
	}
}

func TestChannelMetadataStoreEnsuresPersonDirectoryOnce(t *testing.T) {
	node := &recordingChannelMetadataNode{committedTail: 9}
	cache := NewChannelAppendMetadataCache()
	store := NewChannelMetadataStore(node, cache)

	if err := store.EnsurePersonChannelDirectory(context.Background(), "u1@u2", 1); err != nil {
		t.Fatalf("EnsurePersonChannelDirectory(): %v", err)
	}
	if len(node.membershipUpserts) != 1 {
		t.Fatalf("membership upserts = %+v", node.membershipUpserts)
	}
	call := node.membershipUpserts[0]
	if call.channelID != "u1@u2" || call.channelType != 1 || call.committedTail != 9 || call.sourceVersion != 1 || !equalStringSlices(call.uids, []string{"u1", "u2"}) {
		t.Fatalf("membership upsert = %+v", call)
	}
	if node.directoryReadyCalls != 1 {
		t.Fatalf("directory ready calls = %d, want 1", node.directoryReadyCalls)
	}
	metadata, ok := cache.Lookup(channelappend.ChannelID{ID: "u1@u2", Type: 1})
	if !ok || !metadata.DirectoryReady {
		t.Fatalf("metadata = %+v ok=%v", metadata, ok)
	}

	if err := store.EnsurePersonChannelDirectory(context.Background(), "u1@u2", 1); err != nil {
		t.Fatalf("EnsurePersonChannelDirectory(cached): %v", err)
	}
	if len(node.membershipUpserts) != 1 || node.directoryReadyCalls != 1 {
		t.Fatalf("cached ensure repeated writes: upserts=%d ready=%d", len(node.membershipUpserts), node.directoryReadyCalls)
	}
}

func TestChannelMetadataStoreRefreshesAppendMetadataCache(t *testing.T) {
	node := &recordingChannelMetadataNode{}
	cache := NewChannelAppendMetadataCache()
	store := NewChannelMetadataStore(node, cache)

	channel := metadb.Channel{
		ChannelID:                 "g1",
		ChannelType:               2,
		Large:                     1,
		SubscriberMutationVersion: 7,
	}
	if err := store.UpsertChannel(context.Background(), channel); err != nil {
		t.Fatalf("UpsertChannel() error = %v", err)
	}
	metadata, ok := cache.Lookup(channelappend.ChannelID{ID: "g1", Type: 2})
	if !ok || !metadata.Large || metadata.SubscriberMutationVersion != 7 {
		t.Fatalf("metadata cache = %#v ok=%v, want large version 7", metadata, ok)
	}

	if err := store.DeleteChannel(context.Background(), "g1", 2); err != nil {
		t.Fatalf("DeleteChannel() error = %v", err)
	}
	if metadata, ok := cache.Lookup(channelappend.ChannelID{ID: "g1", Type: 2}); ok {
		t.Fatalf("metadata cache = %#v ok=true, want deleted", metadata)
	}
}

func TestChannelMetadataStoreUsesAuthoritativeChannelReads(t *testing.T) {
	node := &recordingChannelMetadataNode{
		authoritativeChannel: metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1},
		authoritativeUIDs:    []string{"u1"},
	}
	store := NewChannelMetadataStore(node, nil)

	channel, err := store.GetChannel(context.Background(), "g1", 2)
	if err != nil || channel.Ban != 1 {
		t.Fatalf("GetChannel() = %#v err=%v, want authoritative channel", channel, err)
	}
	uids, _, done, err := store.ListChannelSubscribers(context.Background(), "g1", 2, "", 100)
	if err != nil || !done || len(uids) != 1 || uids[0] != "u1" {
		t.Fatalf("ListChannelSubscribers() = %#v done=%v err=%v", uids, done, err)
	}
	ok, err := store.ContainsChannelSubscriber(context.Background(), "g1", 2, "u1")
	if err != nil || !ok {
		t.Fatalf("ContainsChannelSubscriber() = %v err=%v", ok, err)
	}
	ok, err = store.HasChannelSubscribers(context.Background(), "g1", 2)
	if err != nil || !ok {
		t.Fatalf("HasChannelSubscribers() = %v err=%v", ok, err)
	}
	if node.localReadCalls != 0 || node.authoritativeReadCalls != 4 {
		t.Fatalf("read calls local=%d authoritative=%d, want local=0 authoritative=4", node.localReadCalls, node.authoritativeReadCalls)
	}
}

func TestChannelMetadataStoreMapsUnavailablePermissionReadsToRouteNotReady(t *testing.T) {
	node := &recordingChannelMetadataNode{
		authoritativeErr: fmt.Errorf(
			"%w: connection refused",
			transport.ErrDialFailed,
		),
	}
	store := NewChannelMetadataStore(node, nil)

	if _, err := store.GetChannelForPermission(
		context.Background(), "g1", 2,
	); !errors.Is(err, channelappend.ErrRouteNotReady) {
		t.Fatalf("GetChannelForPermission() error = %v, want %v", err, channelappend.ErrRouteNotReady)
	}
	if _, err := store.ContainsChannelSubscriber(
		context.Background(), "g1", 2, "u1",
	); !errors.Is(err, channelappend.ErrRouteNotReady) {
		t.Fatalf("ContainsChannelSubscriber() error = %v, want %v", err, channelappend.ErrRouteNotReady)
	}
	if _, err := store.HasChannelSubscribers(
		context.Background(), "g1", 2,
	); !errors.Is(err, channelappend.ErrRouteNotReady) {
		t.Fatalf("HasChannelSubscribers() error = %v, want %v", err, channelappend.ErrRouteNotReady)
	}
}

func TestChannelMetadataStoreMapsAuthoritativePermissionBatch(t *testing.T) {
	node := &recordingChannelMetadataNode{permissionBatchResults: []slotproxy.PermissionMetadataReadResult{
		{Found: true, Channel: metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1}},
		{Value: true},
		{Value: false},
	}}
	store := NewChannelMetadataStore(node, nil)
	reads := []messageusecase.PermissionRead{
		{Kind: messageusecase.PermissionReadChannel, ChannelID: "g1", ChannelType: 2},
		{Kind: messageusecase.PermissionReadSubscriberContains, ChannelID: "g1", ChannelType: 2, UID: "u1"},
		{Kind: messageusecase.PermissionReadSubscriberHasAny, ChannelID: "g1", ChannelType: 2},
	}

	results := store.ReadPermissionsBatch(context.Background(), reads)

	if len(results) != 3 || !results[0].Found || results[0].Channel.Ban != 1 || !results[1].Value || results[2].Value {
		t.Fatalf("ReadPermissionsBatch() = %#v, want aligned mapped facts", results)
	}
	if got, want := node.permissionBatchReads, []slotproxy.PermissionMetadataRead{
		{Kind: slotproxy.PermissionMetadataReadChannel, ChannelID: "g1", ChannelType: 2},
		{Kind: slotproxy.PermissionMetadataReadSubscriberContains, ChannelID: "g1", ChannelType: 2, UID: "u1"},
		{Kind: slotproxy.PermissionMetadataReadSubscriberHasAny, ChannelID: "g1", ChannelType: 2},
	}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("proxy reads = %#v, want %#v", got, want)
	}
}

func TestChannelMetadataStoreRejectsOrdinaryReadsWithoutAuthoritativeCapability(t *testing.T) {
	node := &localOnlyChannelMetadataNode{}
	store := NewChannelMetadataStore(node, nil)

	if _, err := store.GetChannel(context.Background(), "g1", 2); !errors.Is(err, clusterpkg.ErrRouteNotReady) {
		t.Fatalf("GetChannel() error = %v, want %v", err, clusterpkg.ErrRouteNotReady)
	}
	if _, _, _, err := store.ListChannelSubscribers(context.Background(), "g1", 2, "", 100); !errors.Is(err, clusterpkg.ErrRouteNotReady) {
		t.Fatalf("ListChannelSubscribers() error = %v, want %v", err, clusterpkg.ErrRouteNotReady)
	}
	if _, err := store.ContainsChannelSubscriber(context.Background(), "g1", 2, "u1"); !errors.Is(err, clusterpkg.ErrRouteNotReady) {
		t.Fatalf("ContainsChannelSubscriber() error = %v, want %v", err, clusterpkg.ErrRouteNotReady)
	}
	if _, err := store.HasChannelSubscribers(context.Background(), "g1", 2); !errors.Is(err, clusterpkg.ErrRouteNotReady) {
		t.Fatalf("HasChannelSubscribers() error = %v, want %v", err, clusterpkg.ErrRouteNotReady)
	}
	if node.localReadCalls != 0 {
		t.Fatalf("local read calls = %d, want 0", node.localReadCalls)
	}
}

func TestChannelMetadataStoreReturnsCountedMutationResults(t *testing.T) {
	node := &recordingChannelMetadataNode{
		addResult:    metadb.SubscriberMutationResult{RequestedCount: 2, ChangedCount: 1},
		removeResult: metadb.SubscriberMutationResult{RequestedCount: 2, ChangedCount: 0},
	}
	store := NewChannelMetadataStore(node, nil)

	added, err := store.AddChannelSubscribersCounted(context.Background(), "g1", 2, []string{"u1", "u2"}, 3)
	if err != nil || added != node.addResult {
		t.Fatalf("AddChannelSubscribersCounted() = %#v err=%v", added, err)
	}
	removed, err := store.RemoveChannelSubscribersCounted(context.Background(), "g1", 2, []string{"u1", "u2"}, 4)
	if err != nil || removed != node.removeResult {
		t.Fatalf("RemoveChannelSubscribersCounted() = %#v err=%v", removed, err)
	}
}

type localOnlyChannelMetadataNode struct {
	localReadCalls int
}

func (n *localOnlyChannelMetadataNode) GetChannelMetadata(context.Context, string, int64) (metadb.Channel, error) {
	n.localReadCalls++
	return metadb.Channel{}, nil
}

func (*localOnlyChannelMetadataNode) UpsertChannelMetadata(context.Context, metadb.Channel) error {
	return nil
}

func (*localOnlyChannelMetadataNode) DeleteChannelMetadata(context.Context, string, int64) error {
	return nil
}

func (*localOnlyChannelMetadataNode) AddChannelSubscribers(context.Context, string, int64, []string, uint64) error {
	return nil
}

func (*localOnlyChannelMetadataNode) RemoveChannelSubscribers(context.Context, string, int64, []string, uint64) error {
	return nil
}

func (n *localOnlyChannelMetadataNode) ListChannelSubscribersPage(context.Context, string, int64, string, int) ([]string, string, bool, error) {
	n.localReadCalls++
	return nil, "", true, nil
}

type recordingChannelMetadataNode struct {
	membershipUpserts      []membershipUpsertNodeCall
	membershipDeletes      []membershipDeleteNodeCall
	authoritativeChannel   metadb.Channel
	authoritativeUIDs      []string
	authoritativeErr       error
	authoritativeReadCalls int
	localReadCalls         int
	addResult              metadb.SubscriberMutationResult
	removeResult           metadb.SubscriberMutationResult
	committedTail          uint64
	directoryReadyCalls    int
	permissionBatchReads   []slotproxy.PermissionMetadataRead
	permissionBatchResults []slotproxy.PermissionMetadataReadResult
}

type membershipUpsertNodeCall struct {
	channelID     string
	channelType   int64
	uids          []string
	committedTail uint64
	sourceVersion uint64
	updatedAt     int64
}

type membershipDeleteNodeCall struct {
	channelID     string
	channelType   int64
	uids          []string
	sourceVersion uint64
	updatedAt     int64
}

func (r *recordingChannelMetadataNode) GetChannelMetadata(context.Context, string, int64) (metadb.Channel, error) {
	r.localReadCalls++
	return metadb.Channel{}, nil
}

func (r *recordingChannelMetadataNode) UpsertChannelMetadata(context.Context, metadb.Channel) error {
	return nil
}

func (r *recordingChannelMetadataNode) DeleteChannelMetadata(context.Context, string, int64) error {
	return nil
}

func (r *recordingChannelMetadataNode) AddChannelSubscribers(context.Context, string, int64, []string, uint64) error {
	return nil
}

func (r *recordingChannelMetadataNode) RemoveChannelSubscribers(context.Context, string, int64, []string, uint64) error {
	return nil
}

func (r *recordingChannelMetadataNode) ListChannelSubscribersPage(context.Context, string, int64, string, int) ([]string, string, bool, error) {
	r.localReadCalls++
	return nil, "", true, nil
}

func (r *recordingChannelMetadataNode) ContainsChannelSubscriber(context.Context, string, int64, string) (bool, error) {
	r.localReadCalls++
	return false, nil
}

func (r *recordingChannelMetadataNode) HasChannelSubscribers(context.Context, string, int64) (bool, error) {
	r.localReadCalls++
	return false, nil
}

func (r *recordingChannelMetadataNode) GetChannelMetadataAuthoritative(context.Context, string, int64) (metadb.Channel, error) {
	r.authoritativeReadCalls++
	if r.authoritativeErr != nil {
		return metadb.Channel{}, r.authoritativeErr
	}
	return r.authoritativeChannel, nil
}

func (r *recordingChannelMetadataNode) ListChannelSubscribersAuthoritative(context.Context, string, int64, string, int) ([]string, string, bool, error) {
	r.authoritativeReadCalls++
	if r.authoritativeErr != nil {
		return nil, "", false, r.authoritativeErr
	}
	return append([]string(nil), r.authoritativeUIDs...), "", true, nil
}

func (r *recordingChannelMetadataNode) ContainsChannelSubscriberAuthoritative(_ context.Context, _ string, _ int64, uid string) (bool, error) {
	r.authoritativeReadCalls++
	if r.authoritativeErr != nil {
		return false, r.authoritativeErr
	}
	return uid == "u1", nil
}

func (r *recordingChannelMetadataNode) HasChannelSubscribersAuthoritative(context.Context, string, int64) (bool, error) {
	r.authoritativeReadCalls++
	if r.authoritativeErr != nil {
		return false, r.authoritativeErr
	}
	return len(r.authoritativeUIDs) > 0, nil
}

func (r *recordingChannelMetadataNode) ReadPermissionMetadataBatchAuthoritative(_ context.Context, reads []slotproxy.PermissionMetadataRead) []slotproxy.PermissionMetadataReadResult {
	r.permissionBatchReads = append([]slotproxy.PermissionMetadataRead(nil), reads...)
	return append([]slotproxy.PermissionMetadataReadResult(nil), r.permissionBatchResults...)
}

func (r *recordingChannelMetadataNode) AddChannelSubscribersCounted(context.Context, string, int64, []string, uint64) (metadb.SubscriberMutationResult, error) {
	return r.addResult, nil
}

func (r *recordingChannelMetadataNode) RemoveChannelSubscribersCounted(context.Context, string, int64, []string, uint64) (metadb.SubscriberMutationResult, error) {
	return r.removeResult, nil
}

func (r *recordingChannelMetadataNode) UpsertUserChannelMemberships(_ context.Context, channelID string, channelType int64, uids []string, committedTail, sourceVersion uint64, updatedAt int64) error {
	r.membershipUpserts = append(r.membershipUpserts, membershipUpsertNodeCall{
		channelID:     channelID,
		channelType:   channelType,
		uids:          append([]string(nil), uids...),
		committedTail: committedTail,
		sourceVersion: sourceVersion,
		updatedAt:     updatedAt,
	})
	return nil
}

func (r *recordingChannelMetadataNode) TombstoneUserChannelMemberships(_ context.Context, channelID string, channelType int64, uids []string, sourceVersion uint64, updatedAt int64) error {
	r.membershipDeletes = append(r.membershipDeletes, membershipDeleteNodeCall{
		channelID:     channelID,
		channelType:   channelType,
		uids:          append([]string(nil), uids...),
		sourceVersion: sourceVersion,
		updatedAt:     updatedAt,
	})
	return nil
}

func (r *recordingChannelMetadataNode) CommittedChannelTail(context.Context, string, int64) (uint64, error) {
	return r.committedTail, nil
}

func (r *recordingChannelMetadataNode) EnsureChannelDirectoryReady(context.Context, string, int64) error {
	r.directoryReadyCalls++
	r.authoritativeChannel.DirectoryReady = 1
	return nil
}

func (r *recordingChannelMetadataNode) UpsertUserChannelMembershipBatch(_ context.Context, memberships []metadb.UserChannelMembership) error {
	type key struct {
		channelID   string
		channelType int64
	}
	groups := make(map[key][]metadb.UserChannelMembership)
	order := make([]key, 0)
	for _, membership := range memberships {
		groupKey := key{channelID: membership.ChannelID, channelType: membership.ChannelType}
		if _, ok := groups[groupKey]; !ok {
			order = append(order, groupKey)
		}
		groups[groupKey] = append(groups[groupKey], membership)
	}
	for _, groupKey := range order {
		group := groups[groupKey]
		uids := make([]string, len(group))
		for i, membership := range group {
			uids[i] = membership.UID
		}
		r.membershipUpserts = append(r.membershipUpserts, membershipUpsertNodeCall{
			channelID: groupKey.channelID, channelType: groupKey.channelType, uids: uids,
			committedTail: group[0].ReadSeq, sourceVersion: group[0].SourceVersion, updatedAt: group[0].UpdatedAt,
		})
	}
	return nil
}

func (r *recordingChannelMetadataNode) PreparePersonChannelDirectoryBatch(ctx context.Context, memberships []metadb.UserChannelMembership, _ []metadb.ChannelKey) error {
	return r.UpsertUserChannelMembershipBatch(ctx, memberships)
}

func (r *recordingChannelMetadataNode) EnsureChannelDirectoriesReady(_ context.Context, channels []metadb.ChannelKey) error {
	r.directoryReadyCalls += len(channels)
	if len(channels) > 0 {
		r.authoritativeChannel.DirectoryReady = 1
	}
	return nil
}

func equalMembershipUpsertNodeCalls(a, b []membershipUpsertNodeCall) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].channelID != b[i].channelID || a[i].channelType != b[i].channelType || a[i].committedTail != b[i].committedTail || a[i].sourceVersion != b[i].sourceVersion || a[i].updatedAt != b[i].updatedAt || !equalStringSlices(a[i].uids, b[i].uids) {
			return false
		}
	}
	return true
}

func equalMembershipDeleteNodeCalls(a, b []membershipDeleteNodeCall) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].channelID != b[i].channelID || a[i].channelType != b[i].channelType || a[i].sourceVersion != b[i].sourceVersion || a[i].updatedAt != b[i].updatedAt || !equalStringSlices(a[i].uids, b[i].uids) {
			return false
		}
	}
	return true
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
