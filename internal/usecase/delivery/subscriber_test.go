package delivery

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
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

func TestSubscriberResolverPagesGroupSubscribersFromMetastore(t *testing.T) {
	store := &fakeSubscriberStore{
		snapshotUIDs: []string{"u2", "u3", "u4"},
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

	require.Equal(t, []subscriberSnapshotCall{
		{channelID: "g1", channelType: int64(frame.ChannelTypeGroup)},
	}, store.snapshotCalls)
	require.Empty(t, store.pageCalls)
}

type fakeSubscriberStore struct {
	snapshotCalls []subscriberSnapshotCall
	pageCalls     []subscriberListCall
	snapshotUIDs  []string
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

type subscriberPage struct {
	uids       []string
	nextCursor string
	done       bool
}

func (f *fakeSubscriberStore) SnapshotChannelSubscribers(_ context.Context, channelID string, channelType int64) ([]string, error) {
	f.snapshotCalls = append(f.snapshotCalls, subscriberSnapshotCall{
		channelID:   channelID,
		channelType: channelType,
	})
	return append([]string(nil), f.snapshotUIDs...), nil
}

func (f *fakeSubscriberStore) ListChannelSubscribers(_ context.Context, channelID string, channelType int64, afterUID string, limit int) ([]string, string, bool, error) {
	f.pageCalls = append(f.pageCalls, subscriberListCall{
		channelID:   channelID,
		channelType: channelType,
		afterUID:    afterUID,
		limit:       limit,
	})
	return nil, "", true, nil
}
