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
	store := NewChannelMetadataStore(node, nil, nil)

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

func TestChannelMetadataStoreAdmitsPersonDirectoryTaskOnce(t *testing.T) {
	node := &recordingChannelMetadataNode{
		authoritativeChannel: metadb.Channel{ChannelID: "u1@u2", ChannelType: 1},
		committedTail:        9,
	}
	cache := NewChannelAppendMetadataCache()
	store := NewChannelMetadataStore(node, cache, nil)
	wakes := 0
	store.SetPersonDirectoryWake(func() { wakes++ })
	store.personDirectories.collectWait = 0

	if err := store.AdmitPersonChannelDirectory(context.Background(), "u1@u2", 1); err != nil {
		t.Fatalf("AdmitPersonChannelDirectory(): %v", err)
	}
	if len(node.directoryTasks) != 1 {
		t.Fatalf("directory tasks = %+v", node.directoryTasks)
	}
	task := node.directoryTasks[0]
	if task.ChannelID != "u1@u2" || task.ChannelType != 1 || task.CommittedTail != 9 || task.CreatedAt <= 0 {
		t.Fatalf("directory task = %+v", task)
	}
	if wakes != 1 {
		t.Fatalf("projector wakes = %d, want 1 after durable admission", wakes)
	}
	metadata, ok := cache.Lookup(channelappend.ChannelID{ID: "u1@u2", Type: 1})
	if !ok || metadata.DirectoryProjectionState != metadb.DirectoryProjectionPending {
		t.Fatalf("metadata = %+v ok=%v", metadata, ok)
	}
	node.authoritativeChannel.DirectoryProjectionState = metadb.DirectoryProjectionPending
	if err := store.AdmitPersonChannelDirectory(context.Background(), "u1@u2", 1); err != nil {
		t.Fatalf("AdmitPersonChannelDirectory(authoritative pending): %v", err)
	}
	if len(node.directoryTasks) != 1 {
		t.Fatalf("cached admission repeated writes: tasks=%d", len(node.directoryTasks))
	}
	if wakes != 1 {
		t.Fatalf("cached admission wakes = %d, want unchanged", wakes)
	}
}

func TestChannelMetadataStoreAcceptsAuthoritativePendingDirectoryAdmission(t *testing.T) {
	t.Parallel()

	node := &recordingChannelMetadataNode{
		localChannel:         metadb.Channel{ChannelID: "u1@u2", ChannelType: 1, DirectoryProjectionState: metadb.DirectoryProjectionPending},
		authoritativeChannel: metadb.Channel{ChannelID: "u1@u2", ChannelType: 1, DirectoryProjectionState: metadb.DirectoryProjectionPending},
	}
	store := NewChannelMetadataStore(node, NewChannelAppendMetadataCache(), nil)

	if err := store.AdmitPersonChannelDirectory(context.Background(), "u1@u2", 1); err != nil {
		t.Fatalf("AdmitPersonChannelDirectory() error = %v", err)
	}
	if node.localReadCalls != 0 || node.authoritativeReadCalls != 1 {
		t.Fatalf("directory reads local=%d authoritative=%d, want 0/1", node.localReadCalls, node.authoritativeReadCalls)
	}
	if len(node.directoryTasks) != 0 {
		t.Fatalf("replicated admission proof caused writes: tasks=%d", len(node.directoryTasks))
	}
}

func TestChannelMetadataStoreDoesNotTrustCachedReadyStateAfterDeleteAndRecreate(t *testing.T) {
	t.Parallel()

	node := &recordingChannelMetadataNode{authoritativeErr: metadb.ErrNotFound}
	cache := NewChannelAppendMetadataCache()
	cache.Store(channelappend.ChannelID{ID: "u1@u2", Type: 1}, ChannelAppendMetadata{
		DirectoryProjectionState: metadb.DirectoryProjectionReady,
	})
	store := NewChannelMetadataStore(node, cache, nil)
	store.personDirectories.collectWait = 0

	if err := store.AdmitPersonChannelDirectory(context.Background(), "u1@u2", 1); err != nil {
		t.Fatalf("AdmitPersonChannelDirectory(): %v", err)
	}
	if node.authoritativeReadCalls != 1 {
		t.Fatalf("authoritative reads = %d, want 1 after cached ready state", node.authoritativeReadCalls)
	}
	if len(node.directoryTasks) != 1 {
		t.Fatalf("directory tasks = %d, want recreated channel admission", len(node.directoryTasks))
	}
}

func TestChannelMetadataStoreDoesNotActivateMissingPersonChannelToReadZeroTail(t *testing.T) {
	node := &recordingChannelMetadataNode{authoritativeErr: metadb.ErrNotFound}
	store := NewChannelMetadataStore(node, NewChannelAppendMetadataCache(), nil)

	if err := store.AdmitPersonChannelDirectory(context.Background(), "u1@u2", 1); err != nil {
		t.Fatalf("AdmitPersonChannelDirectory(): %v", err)
	}
	if node.committedTailCalls != 0 {
		t.Fatalf("committed tail calls = %d, want zero for a channel proved absent by authoritative metadata", node.committedTailCalls)
	}
	if len(node.directoryTasks) != 1 || node.directoryTasks[0].CommittedTail != 0 {
		t.Fatalf("directory tasks = %#v, want one zero-tail admission", node.directoryTasks)
	}
}

func TestChannelMetadataStoreReusesBatchScopedMissingChannelFact(t *testing.T) {
	node := &recordingChannelMetadataNode{authoritativeErr: errors.New("unexpected duplicate authoritative read")}
	store := NewChannelMetadataStore(node, NewChannelAppendMetadataCache(), nil)
	store.personDirectories.collectWait = 0

	results := store.AdmitPersonChannelDirectories([]messageusecase.PersonDirectoryAdmission{{
		Context: context.Background(), ChannelID: "u1@u2", ChannelType: 1,
		ChannelFact: &messageusecase.PersonDirectoryChannelFact{Found: false},
	}})
	if len(results) != 1 || results[0] != nil {
		t.Fatalf("AdmitPersonChannelDirectories() = %#v, want success", results)
	}
	if len(node.permissionBatchReads) != 0 || node.authoritativeReadCalls != 0 {
		t.Fatalf("duplicate authoritative reads = batch:%d direct:%d, want zero", len(node.permissionBatchReads), node.authoritativeReadCalls)
	}
	if len(node.directoryTasks) != 1 || node.directoryTasks[0].CommittedTail != 0 {
		t.Fatalf("directory tasks = %#v, want one zero-tail admission", node.directoryTasks)
	}
}

func TestChannelMetadataStoreRefreshesAppendMetadataCache(t *testing.T) {
	node := &recordingChannelMetadataNode{}
	cache := NewChannelAppendMetadataCache()
	store := NewChannelMetadataStore(node, cache, nil)

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
	store := NewChannelMetadataStore(node, nil, nil)

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
	store := NewChannelMetadataStore(node, nil, nil)

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
	store := NewChannelMetadataStore(node, nil, nil)
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
	store := NewChannelMetadataStore(node, nil, nil)

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
	store := NewChannelMetadataStore(node, nil, nil)

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
	directoryTasks         []metadb.PersonDirectoryTask
	localChannel           metadb.Channel
	localErr               error
	authoritativeChannel   metadb.Channel
	authoritativeUIDs      []string
	authoritativeErr       error
	authoritativeReadCalls int
	localReadCalls         int
	addResult              metadb.SubscriberMutationResult
	removeResult           metadb.SubscriberMutationResult
	committedTail          uint64
	committedTailCalls     int
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
	return r.localChannel, r.localErr
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
	if r.permissionBatchResults != nil {
		return append([]slotproxy.PermissionMetadataReadResult(nil), r.permissionBatchResults...)
	}
	results := make([]slotproxy.PermissionMetadataReadResult, len(reads))
	for i, read := range reads {
		r.authoritativeReadCalls++
		if r.authoritativeErr != nil {
			if errors.Is(r.authoritativeErr, metadb.ErrNotFound) {
				continue
			}
			results[i].Err = r.authoritativeErr
			continue
		}
		if read.Kind == slotproxy.PermissionMetadataReadChannel {
			results[i].Found = true
			results[i].Channel = r.authoritativeChannel
		}
	}
	return results
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
	r.committedTailCalls++
	return r.committedTail, nil
}

func (r *recordingChannelMetadataNode) AdmitPersonDirectoryTasks(_ context.Context, tasks []metadb.PersonDirectoryTask) []error {
	r.directoryTasks = append(r.directoryTasks, tasks...)
	return make([]error, len(tasks))
}

func (r *recordingChannelMetadataNode) AdmitPersonDirectoryTaskWaves(_ context.Context, tasks []metadb.PersonDirectoryTask, emit func(int, error)) {
	r.directoryTasks = append(r.directoryTasks, tasks...)
	for i := range tasks {
		emit(i, nil)
	}
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
