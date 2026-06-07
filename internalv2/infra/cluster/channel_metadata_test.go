package cluster

import (
	"context"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestChannelMetadataStoreProjectsUserMemberships(t *testing.T) {
	node := &recordingChannelMetadataNode{}
	store := NewChannelMetadataStore(node)

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

type recordingChannelMetadataNode struct {
	membershipUpserts []membershipUpsertNodeCall
	membershipDeletes []membershipDeleteNodeCall
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
	return nil, "", true, nil
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
