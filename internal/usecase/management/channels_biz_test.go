package management

import (
	"context"
	"hash/crc32"
	"strconv"
	"testing"

	channelmembers "github.com/WuKongIM/WuKongIM/internal/contracts/channelmembers"
	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestListBusinessChannelsAggregatesAndFiltersBusinessRows(t *testing.T) {
	reader := newFakeChannelBusinessReader()
	reader.slotPages[1] = map[metadb.ChannelCursor]fakeChannelBusinessPage{
		{}: {
			items: []metadb.Channel{
				{ChannelID: "__wk_internal_memberlist__/allow/2/ZzE", ChannelType: 2},
				{ChannelID: "alpha", ChannelType: 2, Ban: 1, SubscriberMutationVersion: 3},
				{ChannelID: "alpha____cmd", ChannelType: 2},
			},
			cursor: metadb.ChannelCursor{ChannelID: "alpha____cmd", ChannelType: 2},
			done:   true,
		},
	}
	reader.slotPages[2] = map[metadb.ChannelCursor]fakeChannelBusinessPage{
		{}: {
			items: []metadb.Channel{
				{ChannelID: "alpha-remote", ChannelType: 2, SendBan: 1},
				{ChannelID: "beta", ChannelType: 3},
			},
			cursor: metadb.ChannelCursor{ChannelID: "beta", ChannelType: 3},
			done:   true,
		},
	}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotIDs:        []multiraft.SlotID{2, 1},
			slotForKey:     map[string]multiraft.SlotID{"alpha": 1, "alpha-remote": 2},
			hashSlotForKey: map[string]uint16{"alpha": 7, "alpha-remote": 8},
		},
		ChannelBusinessReader: reader,
	})

	got, err := app.ListBusinessChannels(context.Background(), ListBusinessChannelsRequest{Limit: 10, TypeFilter: 2, Keyword: " alpha "})

	require.NoError(t, err)
	require.Equal(t, []BusinessChannelListItem{
		{ChannelID: "alpha", ChannelType: 2, SlotID: 1, HashSlot: 7, Ban: true, SubscriberMutationVersion: 3},
		{ChannelID: "alpha-remote", ChannelType: 2, SlotID: 2, HashSlot: 8, SendBan: true},
	}, got.Items)
	require.False(t, got.HasMore)
}

func TestListBusinessChannelsReturnsCursorAndRejectsFilterMismatch(t *testing.T) {
	reader := newFakeChannelBusinessReader()
	reader.slotPages[1] = map[metadb.ChannelCursor]fakeChannelBusinessPage{
		{}: {
			items:  []metadb.Channel{{ChannelID: "a", ChannelType: 2}, {ChannelID: "b", ChannelType: 2}},
			cursor: metadb.ChannelCursor{ChannelID: "b", ChannelType: 2},
			done:   true,
		},
	}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotIDs:        []multiraft.SlotID{1},
			slotForKey:     map[string]multiraft.SlotID{"a": 1, "b": 1},
			hashSlotForKey: map[string]uint16{"a": 1, "b": 2},
		},
		ChannelBusinessReader: reader,
	})

	got, err := app.ListBusinessChannels(context.Background(), ListBusinessChannelsRequest{Limit: 1})
	require.NoError(t, err)
	require.True(t, got.HasMore)
	require.Equal(t, []BusinessChannelListItem{{ChannelID: "a", ChannelType: 2, SlotID: 1, HashSlot: 1}}, got.Items)
	require.Equal(t, ChannelListCursor{SlotID: 1, ChannelID: "a", ChannelType: 2, KeywordHash: crc32.ChecksumIEEE(nil)}, got.NextCursor)

	_, err = app.ListBusinessChannels(context.Background(), ListBusinessChannelsRequest{Limit: 1, Keyword: "b", Cursor: got.NextCursor})
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestGetBusinessChannelReturnsDetailFlags(t *testing.T) {
	reader := newFakeChannelBusinessReader()
	reader.channels["g1:2"] = metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, Disband: 1, SubscriberMutationVersion: 9}
	reader.hasSubscribers["g1:2"] = true
	reader.hasSubscribers[channelmembers.DenylistChannelID(channelmembers.ChannelKey{ChannelID: "g1", ChannelType: 2})+":2"] = true
	app := New(Options{
		Cluster: fakeClusterReader{
			slotForKey:     map[string]multiraft.SlotID{"g1": 3},
			hashSlotForKey: map[string]uint16{"g1": 11},
		},
		ChannelBusinessReader: reader,
	})

	got, err := app.GetBusinessChannel(context.Background(), "g1", 2)

	require.NoError(t, err)
	require.Equal(t, BusinessChannelDetail{
		BusinessChannelListItem: BusinessChannelListItem{ChannelID: "g1", ChannelType: 2, SlotID: 3, HashSlot: 11, Ban: true, Disband: true, SubscriberMutationVersion: 9},
		HasSubscribers:          true,
		HasDenylist:             true,
	}, got)
}

func TestGetBusinessChannelRejectsInternalChannelID(t *testing.T) {
	app := New(Options{Cluster: fakeClusterReader{}, ChannelBusinessReader: newFakeChannelBusinessReader()})
	_, err := app.GetBusinessChannel(context.Background(), "__wk_internal_memberlist__/allow/2/ZzE", 2)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)

	_, err = app.GetBusinessChannel(context.Background(), "g1____cmd", 2)
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestUpsertBusinessChannelDelegatesAndReturnsDetail(t *testing.T) {
	reader := newFakeChannelBusinessReader()
	reader.channels["g1:2"] = metadb.Channel{ChannelID: "g1", ChannelType: 2, Ban: 1, SendBan: 1, SubscriberMutationVersion: 5}
	operator := &fakeChannelBusinessOperator{}
	app := New(Options{
		Cluster: fakeClusterReader{
			slotForKey:     map[string]multiraft.SlotID{"g1": 1},
			hashSlotForKey: map[string]uint16{"g1": 4},
		},
		ChannelBusinessReader:   reader,
		ChannelBusinessOperator: operator,
	})

	got, err := app.UpsertBusinessChannel(context.Background(), UpsertBusinessChannelRequest{ChannelID: "g1", ChannelType: 2, Ban: true, SendBan: true})

	require.NoError(t, err)
	require.Equal(t, []channelusecase.Info{{ChannelID: "g1", ChannelType: 2, Ban: true, SendBan: true}}, operator.updateInfoCalls)
	require.Equal(t, BusinessChannelListItem{ChannelID: "g1", ChannelType: 2, SlotID: 1, HashSlot: 4, Ban: true, SendBan: true, SubscriberMutationVersion: 5}, got.BusinessChannelListItem)
}

func TestListBusinessChannelMembersDelegatesAndBindsCursor(t *testing.T) {
	operator := &fakeChannelBusinessOperator{
		allowlistPage: channelusecase.MemberListPageResult{Members: []channelusecase.Member{{UID: "u2"}}, NextCursor: "u2", HasMore: true},
	}
	app := New(Options{ChannelBusinessOperator: operator})

	got, err := app.ListBusinessChannelMembers(context.Background(), ListBusinessChannelMembersRequest{
		ChannelID:   "g1",
		ChannelType: 2,
		ListKind:    "allowlist",
		Limit:       10,
	})

	require.NoError(t, err)
	require.Equal(t, []channelusecase.MemberListPageRequest{{ChannelKey: channelusecase.ChannelKey{ChannelID: "g1", ChannelType: 2}, Limit: 10}}, operator.listAllowlistCalls)
	require.Equal(t, ListBusinessChannelMembersResponse{
		Items:      []BusinessChannelMember{{UID: "u2"}},
		HasMore:    true,
		NextCursor: ChannelMemberCursor{ChannelIDHash: crc32.ChecksumIEEE([]byte("g1")), ChannelType: 2, ListKind: channelMemberListKindAllowlist, UID: "u2"},
	}, got)

	_, err = app.ListBusinessChannelMembers(context.Background(), ListBusinessChannelMembersRequest{
		ChannelID:   "g1",
		ChannelType: 2,
		ListKind:    "denylist",
		Limit:       10,
		Cursor:      got.NextCursor,
	})
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
}

func TestMutateBusinessChannelMembersNormalizesUIDsAndRejectsPersonSubscribers(t *testing.T) {
	operator := &fakeChannelBusinessOperator{}
	app := New(Options{ChannelBusinessOperator: operator})

	got, err := app.MutateBusinessChannelMembers(context.Background(), MutateBusinessChannelMembersRequest{
		ChannelID:   "g1",
		ChannelType: 2,
		ListKind:    "subscribers",
		UIDs:        []string{" u1 ", "u1", "", "u2"},
		Add:         true,
	})
	require.NoError(t, err)
	require.Equal(t, MutateBusinessChannelMembersResponse{ChannelID: "g1", ChannelType: 2, ListKind: "subscribers", Changed: true}, got)
	require.Equal(t, []channelusecase.SubscriberCommand{{ChannelID: "g1", ChannelType: 2, Subscribers: []string{"u1", "u2"}}}, operator.addSubscriberCalls)

	_, err = app.MutateBusinessChannelMembers(context.Background(), MutateBusinessChannelMembersRequest{
		ChannelID:   "p1",
		ChannelType: int64(frame.ChannelTypePerson),
		ListKind:    "subscribers",
		UIDs:        []string{"u1"},
		Add:         false,
	})
	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	require.Empty(t, operator.removeSubscriberCalls)

	_, err = app.MutateBusinessChannelMembers(context.Background(), MutateBusinessChannelMembersRequest{
		ChannelID:   "p1",
		ChannelType: int64(frame.ChannelTypePerson),
		ListKind:    "allowlist",
		UIDs:        []string{"u1"},
		Add:         true,
	})
	require.NoError(t, err)
	require.Equal(t, []channelusecase.MemberCommand{{ChannelKey: channelusecase.ChannelKey{ChannelID: "p1", ChannelType: frame.ChannelTypePerson}, UIDs: []string{"u1"}}}, operator.addAllowlistCalls)
}

type fakeChannelBusinessReader struct {
	slotPages      map[multiraft.SlotID]map[metadb.ChannelCursor]fakeChannelBusinessPage
	channels       map[string]metadb.Channel
	hasSubscribers map[string]bool
}

type fakeChannelBusinessPage struct {
	items  []metadb.Channel
	cursor metadb.ChannelCursor
	done   bool
}

func newFakeChannelBusinessReader() *fakeChannelBusinessReader {
	return &fakeChannelBusinessReader{
		slotPages:      make(map[multiraft.SlotID]map[metadb.ChannelCursor]fakeChannelBusinessPage),
		channels:       make(map[string]metadb.Channel),
		hasSubscribers: make(map[string]bool),
	}
}

func (f *fakeChannelBusinessReader) ScanChannelsSlotPage(_ context.Context, slotID multiraft.SlotID, after metadb.ChannelCursor, _ int) ([]metadb.Channel, metadb.ChannelCursor, bool, error) {
	if pages := f.slotPages[slotID]; pages != nil {
		if page, ok := pages[after]; ok {
			return append([]metadb.Channel(nil), page.items...), page.cursor, page.done, nil
		}
	}
	return nil, after, true, nil
}

func (f *fakeChannelBusinessReader) GetChannelForPermission(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	ch, ok := f.channels[channelBusinessTestKey(channelID, channelType)]
	if !ok {
		return metadb.Channel{}, metadb.ErrNotFound
	}
	return ch, nil
}

func (f *fakeChannelBusinessReader) HasChannelSubscribers(_ context.Context, channelID string, channelType int64) (bool, error) {
	return f.hasSubscribers[channelBusinessTestKey(channelID, channelType)], nil
}

type fakeChannelBusinessOperator struct {
	updateInfoCalls       []channelusecase.Info
	addSubscriberCalls    []channelusecase.SubscriberCommand
	removeSubscriberCalls []channelusecase.SubscriberCommand
	addAllowlistCalls     []channelusecase.MemberCommand
	removeAllowlistCalls  []channelusecase.MemberCommand
	addDenylistCalls      []channelusecase.MemberCommand
	removeDenylistCalls   []channelusecase.MemberCommand
	listSubscribersCalls  []channelusecase.MemberListPageRequest
	listAllowlistCalls    []channelusecase.MemberListPageRequest
	listDenylistCalls     []channelusecase.MemberListPageRequest
	subscribersPage       channelusecase.MemberListPageResult
	allowlistPage         channelusecase.MemberListPageResult
	denylistPage          channelusecase.MemberListPageResult
}

func (f *fakeChannelBusinessOperator) UpdateInfo(_ context.Context, info channelusecase.Info) error {
	f.updateInfoCalls = append(f.updateInfoCalls, info)
	return nil
}

func (f *fakeChannelBusinessOperator) AddSubscribers(_ context.Context, cmd channelusecase.SubscriberCommand) error {
	f.addSubscriberCalls = append(f.addSubscriberCalls, cmd)
	return nil
}

func (f *fakeChannelBusinessOperator) RemoveSubscribers(_ context.Context, cmd channelusecase.SubscriberCommand) error {
	f.removeSubscriberCalls = append(f.removeSubscriberCalls, cmd)
	return nil
}

func (f *fakeChannelBusinessOperator) AddAllowlist(_ context.Context, cmd channelusecase.MemberCommand) error {
	f.addAllowlistCalls = append(f.addAllowlistCalls, cmd)
	return nil
}

func (f *fakeChannelBusinessOperator) RemoveAllowlist(_ context.Context, cmd channelusecase.MemberCommand) error {
	f.removeAllowlistCalls = append(f.removeAllowlistCalls, cmd)
	return nil
}

func (f *fakeChannelBusinessOperator) AddDenylist(_ context.Context, cmd channelusecase.MemberCommand) error {
	f.addDenylistCalls = append(f.addDenylistCalls, cmd)
	return nil
}

func (f *fakeChannelBusinessOperator) RemoveDenylist(_ context.Context, cmd channelusecase.MemberCommand) error {
	f.removeDenylistCalls = append(f.removeDenylistCalls, cmd)
	return nil
}

func (f *fakeChannelBusinessOperator) ListSubscribersPage(_ context.Context, req channelusecase.MemberListPageRequest) (channelusecase.MemberListPageResult, error) {
	f.listSubscribersCalls = append(f.listSubscribersCalls, req)
	return f.subscribersPage, nil
}

func (f *fakeChannelBusinessOperator) ListAllowlistPage(_ context.Context, req channelusecase.MemberListPageRequest) (channelusecase.MemberListPageResult, error) {
	f.listAllowlistCalls = append(f.listAllowlistCalls, req)
	return f.allowlistPage, nil
}

func (f *fakeChannelBusinessOperator) ListDenylistPage(_ context.Context, req channelusecase.MemberListPageRequest) (channelusecase.MemberListPageResult, error) {
	f.listDenylistCalls = append(f.listDenylistCalls, req)
	return f.denylistPage, nil
}

func channelBusinessTestKey(channelID string, channelType int64) string {
	return channelID + ":" + strconv.FormatInt(channelType, 10)
}
