package cluster

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelappend"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

func TestChannelMetadataStoreProjectsUserMemberships(t *testing.T) {
	node := &recordingChannelMetadataNode{}
	store := NewChannelMetadataStore(node, nil)

	if err := store.UpsertChannelMemberships(context.Background(), "g1", 2, []string{"u1", "u2"}, 9, 123); err != nil {
		t.Fatalf("UpsertChannelMemberships(): %v", err)
	}
	if err := store.DeleteChannelMemberships(context.Background(), "g1", 2, []string{"u1"}, 456); err != nil {
		t.Fatalf("DeleteChannelMemberships(): %v", err)
	}

	if got, want := node.membershipUpserts, []membershipUpsertNodeCall{{channelID: "g1", channelType: 2, uids: []string{"u1", "u2"}, joinSeq: 9, updatedAt: 123}}; !equalMembershipUpsertNodeCalls(got, want) {
		t.Fatalf("membership upserts = %#v, want %#v", got, want)
	}
	if got, want := node.membershipDeletes, []membershipDeleteNodeCall{{channelID: "g1", channelType: 2, uids: []string{"u1"}, updatedAt: 456}}; !equalMembershipDeleteNodeCalls(got, want) {
		t.Fatalf("membership deletes = %#v, want %#v", got, want)
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
}

type membershipUpsertNodeCall struct {
	channelID   string
	channelType int64
	uids        []string
	joinSeq     uint64
	updatedAt   int64
}

type membershipDeleteNodeCall struct {
	channelID   string
	channelType int64
	uids        []string
	updatedAt   int64
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

func (r *recordingChannelMetadataNode) AddChannelSubscribersCounted(context.Context, string, int64, []string, uint64) (metadb.SubscriberMutationResult, error) {
	return r.addResult, nil
}

func (r *recordingChannelMetadataNode) RemoveChannelSubscribersCounted(context.Context, string, int64, []string, uint64) (metadb.SubscriberMutationResult, error) {
	return r.removeResult, nil
}

func (r *recordingChannelMetadataNode) UpsertUserChannelMemberships(_ context.Context, channelID string, channelType int64, uids []string, joinSeq uint64, updatedAt int64) error {
	r.membershipUpserts = append(r.membershipUpserts, membershipUpsertNodeCall{
		channelID:   channelID,
		channelType: channelType,
		uids:        append([]string(nil), uids...),
		joinSeq:     joinSeq,
		updatedAt:   updatedAt,
	})
	return nil
}

func (r *recordingChannelMetadataNode) DeleteUserChannelMemberships(_ context.Context, channelID string, channelType int64, uids []string, updatedAt int64) error {
	r.membershipDeletes = append(r.membershipDeletes, membershipDeleteNodeCall{
		channelID:   channelID,
		channelType: channelType,
		uids:        append([]string(nil), uids...),
		updatedAt:   updatedAt,
	})
	return nil
}

func equalMembershipUpsertNodeCalls(a, b []membershipUpsertNodeCall) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].channelID != b[i].channelID || a[i].channelType != b[i].channelType || a[i].joinSeq != b[i].joinSeq || a[i].updatedAt != b[i].updatedAt || !equalStringSlices(a[i].uids, b[i].uids) {
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
		if a[i].channelID != b[i].channelID || a[i].channelType != b[i].channelType || a[i].updatedAt != b[i].updatedAt || !equalStringSlices(a[i].uids, b[i].uids) {
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
