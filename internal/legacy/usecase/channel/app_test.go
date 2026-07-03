package channel

import (
	"context"
	"errors"
	"strconv"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestUpsertResetsSubscribersBeforeAddingReplacement(t *testing.T) {
	store := &recordingStore{
		listPages: []listPage{
			{uids: []string{"old1", "old2"}, cursor: "old2", done: true},
		},
	}
	app := New(Options{Store: store})

	err := app.Upsert(context.Background(), UpsertCommand{
		Info:        Info{ChannelID: "g1", ChannelType: 2, Ban: true},
		Reset:       true,
		Subscribers: []string{"u1", "u2"},
	})

	require.NoError(t, err)
	require.Equal(t, []metadb.Channel{{ChannelID: "g1", ChannelType: 2, Ban: 1}}, store.upsertChannels)
	require.Equal(t, []subscriberCall{{channelID: "g1", channelType: 2, uids: []string{"old1", "old2"}, version: 1}}, store.removeSubscribers)
	require.Equal(t, []subscriberCall{{channelID: "g1", channelType: 2, uids: []string{"u1", "u2"}, version: 1}}, store.addSubscribers)
}

func TestUpdateInfoPersistsStatusFlags(t *testing.T) {
	store := &recordingStore{}
	app := New(Options{Store: store})

	err := app.UpdateInfo(context.Background(), Info{ChannelID: "g1", ChannelType: 2, Ban: true, Disband: true, SendBan: true, AllowStranger: true})
	require.NoError(t, err)
	require.Len(t, store.upsertChannels, 1)
	require.Equal(t, metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, Disband: 1, SendBan: 1, AllowStranger: 1}, store.upsertChannels[0])
}

func TestRemoveAllSubscribersDeletesEachPageWithoutAccumulating(t *testing.T) {
	store := &recordingStore{
		listPages: []listPage{
			{uids: []string{"old1", "old2"}, cursor: "old2", done: false},
			{uids: []string{"old3"}, cursor: "old3", done: true},
		},
	}
	app := New(Options{Store: store, SubscriberPageLimit: 2})

	err := app.RemoveAllSubscribers(context.Background(), ChannelKey{ChannelID: "g1", ChannelType: 2})

	require.NoError(t, err)
	require.Equal(t, []listSubscribersCall{
		{channelID: "g1", channelType: 2, afterUID: "", limit: 2},
		{channelID: "g1", channelType: 2, afterUID: "old2", limit: 2},
	}, store.listSubscribers)
	require.Equal(t, []subscriberCall{
		{channelID: "g1", channelType: 2, uids: []string{"old1", "old2"}, version: 1},
		{channelID: "g1", channelType: 2, uids: []string{"old3"}, version: 1},
	}, store.removeSubscribers)
}

func TestSubscriberMutationsAreChunkedByPageLimit(t *testing.T) {
	store := &recordingStore{}
	app := New(Options{Store: store, SubscriberPageLimit: 2})

	err := app.AddSubscribers(context.Background(), SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: 2,
		Subscribers: []string{"u1", "u2", "u3"},
	})
	require.NoError(t, err)

	err = app.RemoveSubscribers(context.Background(), SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: 2,
		Subscribers: []string{"u4", "u5", "u6"},
	})
	require.NoError(t, err)

	require.Equal(t, []subscriberCall{
		{channelID: "g1", channelType: 2, uids: []string{"u1", "u2"}, version: 1},
		{channelID: "g1", channelType: 2, uids: []string{"u3"}, version: 1},
	}, store.addSubscribers)
	require.Equal(t, []subscriberCall{
		{channelID: "g1", channelType: 2, uids: []string{"u4", "u5"}, version: 2},
		{channelID: "g1", channelType: 2, uids: []string{"u6"}, version: 2},
	}, store.removeSubscribers)
}

func TestChunkedSubscriberMutationsShareOneLogicalVersion(t *testing.T) {
	store := &recordingStore{
		channels: map[string]metadb.Channel{
			recordingChannelKey("g1", 2): {ChannelID: "g1", ChannelType: 2, SubscriberMutationVersion: 7},
		},
	}
	app := New(Options{Store: store, SubscriberPageLimit: 2})

	err := app.AddSubscribers(context.Background(), SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: 2,
		Subscribers: []string{"u1", "u2", "u3"},
	})
	require.NoError(t, err)

	err = app.RemoveSubscribers(context.Background(), SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: 2,
		Subscribers: []string{"u4", "u5", "u6"},
	})
	require.NoError(t, err)

	require.Equal(t, []uint64{8, 8}, subscriberCallVersions(store.addSubscribers))
	require.Equal(t, []uint64{9, 9}, subscriberCallVersions(store.removeSubscribers))
}

func TestResetSubscribersUsesOneVersionForRemoveAndReplacement(t *testing.T) {
	store := &recordingStore{
		channels: map[string]metadb.Channel{
			recordingChannelKey("g1", 2): {ChannelID: "g1", ChannelType: 2, SubscriberMutationVersion: 3},
		},
		listPages: []listPage{
			{uids: []string{"old1", "old2"}, cursor: "old2", done: false},
			{uids: []string{"old3"}, cursor: "old3", done: true},
		},
	}
	app := New(Options{Store: store, SubscriberPageLimit: 2})

	err := app.AddSubscribers(context.Background(), SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: 2,
		Reset:       true,
		Subscribers: []string{"new1", "new2", "new3"},
	})
	require.NoError(t, err)

	require.Equal(t, []uint64{4, 4}, subscriberCallVersions(store.removeSubscribers))
	require.Equal(t, []uint64{4, 4}, subscriberCallVersions(store.addSubscribers))
}

func TestSetAllowlistReplacesSyntheticMemberList(t *testing.T) {
	store := &recordingStore{
		listPages: []listPage{
			{uids: []string{"old"}, cursor: "old", done: true},
		},
	}
	app := New(Options{Store: store})

	err := app.SetAllowlist(context.Background(), MemberCommand{
		ChannelKey: ChannelKey{ChannelID: "g1", ChannelType: 2},
		UIDs:       []string{"u1", "u2"},
	})

	require.NoError(t, err)
	require.Len(t, store.removeSubscribers, 1)
	require.Contains(t, store.removeSubscribers[0].channelID, "allow")
	require.Equal(t, int64(2), store.removeSubscribers[0].channelType)
	require.Equal(t, []string{"old"}, store.removeSubscribers[0].uids)
	require.Len(t, store.addSubscribers, 1)
	require.Equal(t, store.removeSubscribers[0].channelID, store.addSubscribers[0].channelID)
	require.Equal(t, []string{"u1", "u2"}, store.addSubscribers[0].uids)
}

func TestAddSubscribersCreatesChannelWhenMissing(t *testing.T) {
	store := &recordingStore{getChannelErr: metadb.ErrNotFound}
	app := New(Options{Store: store})

	err := app.AddSubscribers(context.Background(), SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: 2,
		Subscribers: []string{"u1"},
	})

	require.NoError(t, err)
	require.Equal(t, []metadb.Channel{{ChannelID: "g1", ChannelType: 2}}, store.upsertChannels)
	require.Equal(t, []subscriberCall{{channelID: "g1", channelType: 2, uids: []string{"u1"}, version: 1}}, store.addSubscribers)
}

func TestAddSubscribersKeepsExistingChannelFlags(t *testing.T) {
	store := &recordingStore{}
	app := New(Options{Store: store})

	err := app.AddSubscribers(context.Background(), SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: 2,
		Subscribers: []string{"u1"},
	})

	require.NoError(t, err)
	require.Empty(t, store.upsertChannels)
	require.Equal(t, []subscriberCall{{channelID: "g1", channelType: 2, uids: []string{"u1"}, version: 1}}, store.addSubscribers)
}

func TestAddSubscribersReturnsLookupErrorBeforeMutating(t *testing.T) {
	store := &recordingStore{getChannelErr: errors.New("lookup failed")}
	app := New(Options{Store: store})

	err := app.AddSubscribers(context.Background(), SubscriberCommand{
		ChannelID:   "g1",
		ChannelType: 2,
		Subscribers: []string{"u1"},
	})

	require.EqualError(t, err, "lookup failed")
	require.Empty(t, store.upsertChannels)
	require.Empty(t, store.addSubscribers)
}

func TestListAllowlistReturnsLegacyMembers(t *testing.T) {
	store := &recordingStore{
		listPages: []listPage{
			{uids: []string{"u1", "u2"}, cursor: "u2", done: true},
		},
	}
	app := New(Options{Store: store})

	result, err := app.ListAllowlist(context.Background(), ChannelKey{ChannelID: "g1", ChannelType: 2})

	require.NoError(t, err)
	require.Equal(t, MemberListResult{
		Members: []Member{{UID: "u1"}, {UID: "u2"}},
	}, result)
	require.Len(t, store.listSubscribers, 1)
	require.Contains(t, store.listSubscribers[0].channelID, "allow")
}

func TestListSubscribersPageReturnsOnePage(t *testing.T) {
	store := &recordingStore{
		listPages: []listPage{{uids: []string{"u1", "u2"}, cursor: "u2", done: false}},
	}
	app := New(Options{Store: store})

	result, err := app.ListSubscribersPage(context.Background(), MemberListPageRequest{
		ChannelKey: ChannelKey{ChannelID: "g1", ChannelType: 2},
		AfterUID:   "u0",
		Limit:      2,
	})

	require.NoError(t, err)
	require.Equal(t, MemberListPageResult{
		Members:    []Member{{UID: "u1"}, {UID: "u2"}},
		NextCursor: "u2",
		HasMore:    true,
	}, result)
	require.Equal(t, []listSubscribersCall{{channelID: "g1", channelType: 2, afterUID: "u0", limit: 2}}, store.listSubscribers)
}

func TestListAllowlistPageUsesNamespacedChannel(t *testing.T) {
	store := &recordingStore{
		listPages: []listPage{{uids: []string{"u1"}, cursor: "u1", done: true}},
	}
	app := New(Options{Store: store})
	key := ChannelKey{ChannelID: "g1", ChannelType: 2}

	result, err := app.ListAllowlistPage(context.Background(), MemberListPageRequest{
		ChannelKey: key,
		Limit:      10,
	})

	require.NoError(t, err)
	require.Equal(t, MemberListPageResult{Members: []Member{{UID: "u1"}}, NextCursor: "u1"}, result)
	require.Equal(t, []listSubscribersCall{{
		channelID:   namespacedListChannelID(allowListKind, key),
		channelType: 2,
		afterUID:    "",
		limit:       10,
	}}, store.listSubscribers)
}

func TestListDenylistPageUsesNamespacedChannel(t *testing.T) {
	store := &recordingStore{
		listPages: []listPage{{uids: []string{"u3"}, cursor: "u3", done: true}},
	}
	app := New(Options{Store: store})
	key := ChannelKey{ChannelID: "g1", ChannelType: 2}

	result, err := app.ListDenylistPage(context.Background(), MemberListPageRequest{
		ChannelKey: key,
		AfterUID:   "u2",
		Limit:      10,
	})

	require.NoError(t, err)
	require.Equal(t, MemberListPageResult{Members: []Member{{UID: "u3"}}, NextCursor: "u3"}, result)
	require.Equal(t, []listSubscribersCall{{
		channelID:   namespacedListChannelID(denyListKind, key),
		channelType: 2,
		afterUID:    "u2",
		limit:       10,
	}}, store.listSubscribers)
}

func TestListSubscribersPageValidatesStoreAndLimit(t *testing.T) {
	app := New(Options{})
	_, err := app.ListSubscribersPage(context.Background(), MemberListPageRequest{
		ChannelKey: ChannelKey{ChannelID: "g1", ChannelType: 2},
		Limit:      10,
	})
	require.ErrorIs(t, err, ErrStoreRequired)

	app = New(Options{Store: &recordingStore{}})
	_, err = app.ListSubscribersPage(context.Background(), MemberListPageRequest{
		ChannelKey: ChannelKey{ChannelID: "g1", ChannelType: 2},
	})
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

type recordingStore struct {
	upsertChannels    []metadb.Channel
	deleteChannels    []channelKeyCall
	addSubscribers    []subscriberCall
	removeSubscribers []subscriberCall
	listSubscribers   []listSubscribersCall
	listPages         []listPage
	channels          map[string]metadb.Channel
	getChannelErr     error
}

type channelKeyCall struct {
	channelID   string
	channelType int64
}

type subscriberCall struct {
	channelID   string
	channelType int64
	uids        []string
	version     uint64
}

type listSubscribersCall struct {
	channelID   string
	channelType int64
	afterUID    string
	limit       int
}

type listPage struct {
	uids   []string
	cursor string
	done   bool
}

func (r *recordingStore) UpsertChannel(ctx context.Context, ch metadb.Channel) error {
	r.upsertChannels = append(r.upsertChannels, ch)
	if r.channels == nil {
		r.channels = make(map[string]metadb.Channel)
	}
	r.channels[recordingChannelKey(ch.ChannelID, ch.ChannelType)] = ch
	return nil
}

func (r *recordingStore) DeleteChannel(ctx context.Context, channelID string, channelType int64) error {
	r.deleteChannels = append(r.deleteChannels, channelKeyCall{channelID: channelID, channelType: channelType})
	return nil
}

func (r *recordingStore) AddChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error {
	version := firstMutationVersion(subscriberMutationVersion)
	r.addSubscribers = append(r.addSubscribers, subscriberCall{channelID: channelID, channelType: channelType, uids: append([]string(nil), uids...), version: version})
	r.recordSubscriberMutationVersion(channelID, channelType, version)
	return nil
}

func (r *recordingStore) RemoveChannelSubscribers(ctx context.Context, channelID string, channelType int64, uids []string, subscriberMutationVersion ...uint64) error {
	version := firstMutationVersion(subscriberMutationVersion)
	r.removeSubscribers = append(r.removeSubscribers, subscriberCall{channelID: channelID, channelType: channelType, uids: append([]string(nil), uids...), version: version})
	r.recordSubscriberMutationVersion(channelID, channelType, version)
	return nil
}

func (r *recordingStore) ListChannelSubscribers(ctx context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	r.listSubscribers = append(r.listSubscribers, listSubscribersCall{channelID: channelID, channelType: channelType, afterUID: afterUID, limit: limit})
	if len(r.listPages) == 0 {
		return nil, afterUID, true, nil
	}
	page := r.listPages[0]
	r.listPages = r.listPages[1:]
	return append([]string(nil), page.uids...), page.cursor, page.done, nil
}

func (r *recordingStore) GetChannel(ctx context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	if r.getChannelErr != nil {
		return metadb.Channel{}, r.getChannelErr
	}
	if r.channels != nil {
		if ch, ok := r.channels[recordingChannelKey(channelID, channelType)]; ok {
			return ch, nil
		}
	}
	return metadb.Channel{ChannelID: channelID, ChannelType: channelType}, nil
}

func (r *recordingStore) recordSubscriberMutationVersion(channelID string, channelType int64, version uint64) {
	if version == 0 {
		return
	}
	if r.channels == nil {
		r.channels = make(map[string]metadb.Channel)
	}
	key := recordingChannelKey(channelID, channelType)
	ch := r.channels[key]
	ch.ChannelID = channelID
	ch.ChannelType = channelType
	ch.SubscriberMutationVersion = version
	r.channels[key] = ch
}

func firstMutationVersion(versions []uint64) uint64 {
	if len(versions) == 0 {
		return 0
	}
	return versions[0]
}

func subscriberCallVersions(calls []subscriberCall) []uint64 {
	out := make([]uint64, 0, len(calls))
	for _, call := range calls {
		out = append(out, call.version)
	}
	return out
}

func recordingChannelKey(channelID string, channelType int64) string {
	return channelID + ":" + strconv.FormatInt(channelType, 10)
}
