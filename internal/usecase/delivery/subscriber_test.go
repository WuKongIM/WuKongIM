package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/runtime/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestPersonChannelCodecRoundTripsCanonicalIDs(t *testing.T) {
	channelID := EncodePersonChannel("u1", "u2")
	require.Equal(t, "u2@u1", channelID)

	left, right, err := DecodePersonChannel(channelID)
	require.NoError(t, err)
	require.Equal(t, "u2", left)
	require.Equal(t, "u1", right)
}

func TestSubscriberResolverReturnsTwoUIDsForPersonChannel(t *testing.T) {
	resolver := NewSubscriberResolver(SubscriberResolverOptions{})

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   EncodePersonChannel("u1", "u2"),
		Type: frame.ChannelTypePerson,
	})
	require.NoError(t, err)

	page1, cursor, done, err := resolver.NextPage(context.Background(), token, "", 1)
	require.NoError(t, err)
	require.Equal(t, []string{"u2"}, page1)
	require.Equal(t, "u2", cursor)
	require.False(t, done)

	page2, cursor, done, err := resolver.NextPage(context.Background(), token, cursor, 1)
	require.NoError(t, err)
	require.Equal(t, []string{"u1"}, page2)
	require.Equal(t, "u1", cursor)
	require.True(t, done)
}

func TestSubscriberResolverStaticSnapshotPageAvoidsAllocation(t *testing.T) {
	resolver := NewSubscriberResolver(SubscriberResolverOptions{})
	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   EncodePersonChannel("u1", "u2"),
		Type: frame.ChannelTypePerson,
	})
	require.NoError(t, err)

	var (
		page   []string
		cursor string
		done   bool
	)
	allocs := testing.AllocsPerRun(1000, func() {
		var err error
		page, cursor, done, err = resolver.NextPage(context.Background(), token, "", 10)
		if err != nil {
			panic(err)
		}
		if len(page) != 2 || cursor != "u1" || !done {
			panic("unexpected page")
		}
	})
	if allocs != 0 {
		t.Fatalf("static snapshot page allocations = %.1f/op, want 0/op", allocs)
	}
}

func TestSubscriberResolverSkipsMetadataForDerivedPersonChannel(t *testing.T) {
	store := &fakeAuthoritativeSubscriberStore{}
	resolver := NewSubscriberResolver(SubscriberResolverOptions{Store: store})

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   EncodePersonChannel("u1", "u2"),
		Type: frame.ChannelTypePerson,
	})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, SubscriberSourceKindDerived, source.Kind)
	require.Zero(t, source.SubscriberMutationVersion)
	require.Zero(t, source.SourceSubscriberMutationVersion)
	require.Empty(t, store.getChannelCalls)
	require.Empty(t, store.permissionCalls)
}

func TestSubscriberResolverPagesGroupSubscribersFromMetastore(t *testing.T) {
	store := &fakeSubscriberStore{
		pageResults: []subscriberListResult{
			{uids: []string{"u2", "u3"}, cursor: "u3", done: false},
			{uids: []string{"u4"}, cursor: "u4", done: true},
		},
	}
	resolver := NewSubscriberResolver(SubscriberResolverOptions{Store: store})

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   "g1",
		Type: frame.ChannelTypeGroup,
	})
	require.NoError(t, err)

	page1, cursor, done, err := resolver.NextPage(context.Background(), token, "", 2)
	require.NoError(t, err)
	require.Equal(t, []string{"u2", "u3"}, page1)
	require.Equal(t, "u3", cursor)
	require.False(t, done)

	page2, cursor, done, err := resolver.NextPage(context.Background(), token, cursor, 2)
	require.NoError(t, err)
	require.Equal(t, []string{"u4"}, page2)
	require.Equal(t, "u4", cursor)
	require.True(t, done)

	require.Empty(t, store.snapshotCalls)
	require.Equal(t, []subscriberListCall{
		{channelID: "g1", channelType: int64(frame.ChannelTypeGroup), afterUID: "", limit: 2},
		{channelID: "g1", channelType: int64(frame.ChannelTypeGroup), afterUID: "u3", limit: 2},
	}, store.pageCalls)
}

func TestSubscriberResolverResolvesCommandGroupFromSourceChannel(t *testing.T) {
	store := &fakeSubscriberStore{
		pageResults: []subscriberListResult{
			{uids: []string{"u2", "u3"}, cursor: "u3", done: true},
		},
	}
	resolver := NewSubscriberResolver(SubscriberResolverOptions{Store: store})

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   channelid.ToCommandChannel("g1"),
		Type: frame.ChannelTypeGroup,
	})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, channelid.ToCommandChannel("g1"), source.ChannelID)
	require.Equal(t, frame.ChannelTypeGroup, source.ChannelType)
	require.Equal(t, "g1", source.SourceChannelID)
	require.Equal(t, frame.ChannelTypeGroup, source.SourceChannelType)

	page, cursor, done, err := resolver.NextPage(context.Background(), token, "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"u2", "u3"}, page)
	require.Equal(t, "u3", cursor)
	require.True(t, done)
	require.Equal(t, []subscriberListCall{
		{channelID: "g1", channelType: int64(frame.ChannelTypeGroup), afterUID: "", limit: 10},
	}, store.pageCalls)
}

func TestSubscriberResolverResolvesCommandPersonFromOriginalChannel(t *testing.T) {
	resolver := NewSubscriberResolver(SubscriberResolverOptions{})
	requestedID := channelid.ToCommandChannel(EncodePersonChannel("u1", "u2"))

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   requestedID,
		Type: frame.ChannelTypePerson,
	})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, requestedID, source.ChannelID)
	require.Equal(t, frame.ChannelTypePerson, source.ChannelType)
	require.Equal(t, "u2@u1", source.SourceChannelID)
	require.Equal(t, frame.ChannelTypePerson, source.SourceChannelType)

	page, cursor, done, err := resolver.NextPage(context.Background(), token, "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"u2", "u1"}, page)
	require.Equal(t, "u1", cursor)
	require.True(t, done)
}

func TestSubscriberResolverResolvesCommandAgentFromSourceChannel(t *testing.T) {
	resolver := NewSubscriberResolver(SubscriberResolverOptions{})
	requestedID := channelid.ToCommandChannel("userA@agentB")

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   requestedID,
		Type: frame.ChannelTypeAgent,
	})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, SubscriberSourceKindDerived, source.Kind)
	require.Equal(t, requestedID, source.ChannelID)
	require.Equal(t, frame.ChannelTypeAgent, source.ChannelType)
	require.Equal(t, "userA@agentB", source.SourceChannelID)
	require.Equal(t, frame.ChannelTypeAgent, source.SourceChannelType)

	page, cursor, done, err := resolver.NextPage(context.Background(), token, "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"userA", "agentB"}, page)
	require.Equal(t, "agentB", cursor)
	require.True(t, done)
}

func TestSubscriberResolverUsesCustomerServiceSourceVersionForCommandVisitors(t *testing.T) {
	store := &fakeAuthoritativeSubscriberStore{
		fakeSubscriberStore: fakeSubscriberStore{
			pageResults: []subscriberListResult{{uids: []string{"agent1"}, cursor: "agent1", done: true}},
		},
		getChannelVersions: map[subscriberSnapshotCall]uint64{
			{channelID: channelid.ToCommandChannel("visitor1"), channelType: int64(frame.ChannelTypeVisitors)}: 4,
			{channelID: "visitor1", channelType: int64(frame.ChannelTypeCustomerService)}:                      5,
		},
		permissionVersions: map[subscriberSnapshotCall]uint64{
			{channelID: "visitor1", channelType: int64(frame.ChannelTypeCustomerService)}: 9,
		},
	}
	resolver := NewSubscriberResolver(SubscriberResolverOptions{Store: store})

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   channelid.ToCommandChannel("visitor1"),
		Type: frame.ChannelTypeVisitors,
	})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, SubscriberSourceKindOverlayStore, source.Kind)
	require.Equal(t, channelid.ToCommandChannel("visitor1"), source.ChannelID)
	require.Equal(t, frame.ChannelTypeVisitors, source.ChannelType)
	require.Equal(t, "visitor1", source.SourceChannelID)
	require.Equal(t, frame.ChannelTypeCustomerService, source.SourceChannelType)
	require.Equal(t, uint64(4), source.SubscriberMutationVersion)
	require.Equal(t, uint64(9), source.SourceSubscriberMutationVersion)
	require.True(t, source.ReusableTagState)
	require.Contains(t, store.permissionCalls, subscriberSnapshotCall{channelID: "visitor1", channelType: int64(frame.ChannelTypeCustomerService)})
	require.NotContains(t, store.permissionCalls, subscriberSnapshotCall{channelID: "visitor1", channelType: int64(frame.ChannelTypeVisitors)})

	page, _, done, err := resolver.NextPage(context.Background(), token, "", 10)
	require.NoError(t, err)
	require.True(t, done)
	require.ElementsMatch(t, []string{"visitor1", "agent1"}, page)
	require.Equal(t, []subscriberListCall{{channelID: "visitor1", channelType: int64(frame.ChannelTypeCustomerService), afterUID: "", limit: 9}}, store.pageCalls)
}

func TestSubscriberResolverMarksCommandInfoWithTemporaryOverlayNonReusable(t *testing.T) {
	store := &fakeAuthoritativeSubscriberStore{
		fakeSubscriberStore: fakeSubscriberStore{
			temporaryUIDs: []string{"temp1"},
			pageResults:   []subscriberListResult{{uids: []string{"info1"}, cursor: "info1", done: true}},
		},
		getChannelVersions: map[subscriberSnapshotCall]uint64{
			{channelID: channelid.ToCommandChannel("infoA"), channelType: int64(frame.ChannelTypeInfo)}: 6,
			{channelID: "infoA", channelType: int64(frame.ChannelTypeInfo)}:                             7,
		},
		permissionVersions: map[subscriberSnapshotCall]uint64{
			{channelID: "infoA", channelType: int64(frame.ChannelTypeInfo)}: 8,
		},
	}
	resolver := NewSubscriberResolver(SubscriberResolverOptions{Store: store})

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   channelid.ToCommandChannel("infoA"),
		Type: frame.ChannelTypeInfo,
	})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, SubscriberSourceKindOverlayStore, source.Kind)
	require.Equal(t, "infoA", source.SourceChannelID)
	require.Equal(t, frame.ChannelTypeInfo, source.SourceChannelType)
	require.Equal(t, uint64(8), source.SourceSubscriberMutationVersion)
	require.False(t, source.ReusableTagState)
	require.Equal(t, []subscriberSnapshotCall{{channelID: "infoA", channelType: int64(frame.ChannelTypeInfo)}}, store.temporaryCalls)

	page, _, done, err := resolver.NextPage(context.Background(), token, "", 10)
	require.NoError(t, err)
	require.True(t, done)
	require.ElementsMatch(t, []string{"temp1", "info1"}, page)
}

func TestSubscriberResolverKeepsCommandInfoWithoutTemporaryOverlayReusable(t *testing.T) {
	store := &fakeAuthoritativeSubscriberStore{
		fakeSubscriberStore: fakeSubscriberStore{
			pageResults: []subscriberListResult{{uids: []string{"info1"}, cursor: "info1", done: true}},
		},
		getChannelVersions: map[subscriberSnapshotCall]uint64{
			{channelID: channelid.ToCommandChannel("infoA"), channelType: int64(frame.ChannelTypeInfo)}: 6,
		},
		permissionVersions: map[subscriberSnapshotCall]uint64{
			{channelID: "infoA", channelType: int64(frame.ChannelTypeInfo)}: 8,
		},
	}
	resolver := NewSubscriberResolver(SubscriberResolverOptions{Store: store})

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   channelid.ToCommandChannel("infoA"),
		Type: frame.ChannelTypeInfo,
	})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, SubscriberSourceKindPagedStore, source.Kind)
	require.True(t, source.ReusableTagState)
	require.Equal(t, uint64(8), source.SourceSubscriberMutationVersion)
}

func TestSubscriberResolverMessageScopedTempCommandSource(t *testing.T) {
	resolver := NewSubscriberResolver(SubscriberResolverOptions{})
	requestedID := channelid.ToCommandChannel("tmp1")

	token, err := resolver.BeginSnapshotWithRequest(context.Background(), channel.ChannelID{
		ID:   requestedID,
		Type: frame.ChannelTypeTemp,
	}, SubscriberSnapshotRequest{MessageScopedUIDs: []string{"u1", "u2", "u1", ""}})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, SubscriberSourceKindMessageScoped, source.Kind)
	require.Equal(t, requestedID, source.ChannelID)
	require.Equal(t, frame.ChannelTypeTemp, source.ChannelType)
	require.Equal(t, "tmp1", source.SourceChannelID)
	require.Equal(t, frame.ChannelTypeTemp, source.SourceChannelType)
	require.False(t, source.ReusableTagState)

	page, cursor, done, err := resolver.NextPage(context.Background(), token, "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"u1", "u2"}, page)
	require.Equal(t, "u2", cursor)
	require.True(t, done)
}

func TestSubscriberResolverUsesAuthoritativeMetadataForCommandSourceVersion(t *testing.T) {
	store := &fakeAuthoritativeSubscriberStore{
		fakeSubscriberStore: fakeSubscriberStore{
			pageResults: []subscriberListResult{
				{uids: []string{"u2"}, cursor: "u2", done: true},
			},
		},
		getChannelVersions: map[subscriberSnapshotCall]uint64{
			{channelID: channelid.ToCommandChannel("g1"), channelType: int64(frame.ChannelTypeGroup)}: 4,
			{channelID: "g1", channelType: int64(frame.ChannelTypeGroup)}:                             3,
		},
		permissionVersions: map[subscriberSnapshotCall]uint64{
			{channelID: "g1", channelType: int64(frame.ChannelTypeGroup)}: 9,
		},
	}
	resolver := NewSubscriberResolver(SubscriberResolverOptions{Store: store})

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   channelid.ToCommandChannel("g1"),
		Type: frame.ChannelTypeGroup,
	})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, channelid.ToCommandChannel("g1"), source.ChannelID)
	require.Equal(t, "g1", source.SourceChannelID)
	require.Equal(t, uint64(4), source.SubscriberMutationVersion)
	require.Equal(t, uint64(9), source.SourceSubscriberMutationVersion)
	require.Contains(t, store.permissionCalls, subscriberSnapshotCall{
		channelID:   "g1",
		channelType: int64(frame.ChannelTypeGroup),
	})
	require.Contains(t, store.getChannelCalls, subscriberSnapshotCall{
		channelID:   channelid.ToCommandChannel("g1"),
		channelType: int64(frame.ChannelTypeGroup),
	})
	require.NotContains(t, store.getChannelCalls, subscriberSnapshotCall{
		channelID:   "g1",
		channelType: int64(frame.ChannelTypeGroup),
	})
}

func TestSubscriberResolverUsesOrdinaryMetadataForNonCommandChannels(t *testing.T) {
	store := &fakeAuthoritativeSubscriberStore{
		fakeSubscriberStore: fakeSubscriberStore{
			pageResults: []subscriberListResult{
				{uids: []string{"u2"}, cursor: "u2", done: true},
			},
		},
		getChannelVersions: map[subscriberSnapshotCall]uint64{
			{channelID: "g1", channelType: int64(frame.ChannelTypeGroup)}: 3,
		},
		permissionVersions: map[subscriberSnapshotCall]uint64{
			{channelID: "g1", channelType: int64(frame.ChannelTypeGroup)}: 9,
		},
	}
	resolver := NewSubscriberResolver(SubscriberResolverOptions{Store: store})

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   "g1",
		Type: frame.ChannelTypeGroup,
	})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, uint64(3), source.SubscriberMutationVersion)
	require.Equal(t, uint64(3), source.SourceSubscriberMutationVersion)
	require.Equal(t, []subscriberSnapshotCall{
		{channelID: "g1", channelType: int64(frame.ChannelTypeGroup)},
	}, store.getChannelCalls)
	require.Empty(t, store.permissionCalls)
}

func TestSubscriberResolverFallsBackToOrdinaryMetadataWhenCommandSourcePermissionReadFails(t *testing.T) {
	store := &fakeAuthoritativeSubscriberStore{
		fakeSubscriberStore: fakeSubscriberStore{
			pageResults: []subscriberListResult{
				{uids: []string{"u2"}, cursor: "u2", done: true},
			},
		},
		getChannelVersions: map[subscriberSnapshotCall]uint64{
			{channelID: channelid.ToCommandChannel("g1"), channelType: int64(frame.ChannelTypeGroup)}: 4,
			{channelID: "g1", channelType: int64(frame.ChannelTypeGroup)}:                             7,
		},
		permissionErrors: map[subscriberSnapshotCall]error{
			{channelID: "g1", channelType: int64(frame.ChannelTypeGroup)}: errors.New("permission metadata unavailable"),
		},
	}
	resolver := NewSubscriberResolver(SubscriberResolverOptions{Store: store})

	token, err := resolver.BeginSnapshot(context.Background(), channel.ChannelID{
		ID:   channelid.ToCommandChannel("g1"),
		Type: frame.ChannelTypeGroup,
	})
	require.NoError(t, err)

	source := token.Source()
	require.Equal(t, uint64(4), source.SubscriberMutationVersion)
	require.Equal(t, uint64(7), source.SourceSubscriberMutationVersion)
	require.Contains(t, store.permissionCalls, subscriberSnapshotCall{
		channelID:   "g1",
		channelType: int64(frame.ChannelTypeGroup),
	})
	require.Contains(t, store.getChannelCalls, subscriberSnapshotCall{
		channelID:   "g1",
		channelType: int64(frame.ChannelTypeGroup),
	})
}

type fakeSubscriberStore struct {
	snapshotCalls  []subscriberSnapshotCall
	pageCalls      []subscriberListCall
	temporaryCalls []subscriberSnapshotCall
	snapshotUIDs   []string
	temporaryUIDs  []string
	pageResults    []subscriberListResult
}

type fakeAuthoritativeSubscriberStore struct {
	fakeSubscriberStore
	getChannelVersions map[subscriberSnapshotCall]uint64
	permissionVersions map[subscriberSnapshotCall]uint64
	permissionErrors   map[subscriberSnapshotCall]error
	getChannelCalls    []subscriberSnapshotCall
	permissionCalls    []subscriberSnapshotCall
}

type subscriberSnapshotCall struct {
	channelID   string
	channelType int64
}

type subscriberListCall struct {
	channelID   string
	channelType int64
	afterUID    string
	limit       int
}

type subscriberListResult struct {
	uids   []string
	cursor string
	done   bool
}

func (f *fakeSubscriberStore) SnapshotChannelSubscribers(_ context.Context, channelID string, channelType int64) ([]string, error) {
	f.snapshotCalls = append(f.snapshotCalls, subscriberSnapshotCall{
		channelID:   channelID,
		channelType: channelType,
	})
	return append([]string(nil), f.snapshotUIDs...), nil
}

func (f *fakeSubscriberStore) SnapshotTemporarySubscribers(_ context.Context, channelID string, channelType int64) ([]string, error) {
	f.temporaryCalls = append(f.temporaryCalls, subscriberSnapshotCall{
		channelID:   channelID,
		channelType: channelType,
	})
	return append([]string(nil), f.temporaryUIDs...), nil
}

func (f *fakeSubscriberStore) ListChannelSubscribers(_ context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	f.pageCalls = append(f.pageCalls, subscriberListCall{
		channelID:   channelID,
		channelType: channelType,
		afterUID:    afterUID,
		limit:       limit,
	})
	if len(f.pageResults) == 0 {
		return nil, afterUID, true, nil
	}
	result := f.pageResults[0]
	f.pageResults = f.pageResults[1:]
	return append([]string(nil), result.uids...), result.cursor, result.done, nil
}

func (f *fakeAuthoritativeSubscriberStore) GetChannel(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	call := subscriberSnapshotCall{channelID: channelID, channelType: channelType}
	f.getChannelCalls = append(f.getChannelCalls, call)
	return metadb.Channel{
		ChannelID:                 channelID,
		ChannelType:               channelType,
		SubscriberMutationVersion: f.getChannelVersions[call],
	}, nil
}

func (f *fakeAuthoritativeSubscriberStore) GetChannelForPermission(_ context.Context, channelID string, channelType int64) (metadb.Channel, error) {
	call := subscriberSnapshotCall{channelID: channelID, channelType: channelType}
	f.permissionCalls = append(f.permissionCalls, call)
	if err := f.permissionErrors[call]; err != nil {
		return metadb.Channel{}, err
	}
	return metadb.Channel{
		ChannelID:                 channelID,
		ChannelType:               channelType,
		SubscriberMutationVersion: f.permissionVersions[call],
	}, nil
}
