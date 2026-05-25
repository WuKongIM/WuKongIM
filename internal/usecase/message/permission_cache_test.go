package message

import (
	"context"
	"errors"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestPermissionCacheReusesSuccessfulReadsWithinTTL(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := &countingPermissionStore{
		channel: metadb.Channel{ChannelID: "g1", ChannelType: 2},
		contains: map[string]bool{
			"g1#2#u1": true,
		},
		hasAny: map[string]bool{
			"g1#2": true,
		},
	}
	cache := newPermissionCache(store, time.Minute, func() time.Time { return now })

	for i := 0; i < 2; i++ {
		ch, err := cache.GetChannelForPermission(context.Background(), "g1", 2)
		require.NoError(t, err)
		require.Equal(t, "g1", ch.ChannelID)
		contains, err := cache.ContainsChannelSubscriber(context.Background(), "g1", 2, "u1")
		require.NoError(t, err)
		require.True(t, contains)
		hasAny, err := cache.HasChannelSubscribers(context.Background(), "g1", 2)
		require.NoError(t, err)
		require.True(t, hasAny)
	}

	require.Equal(t, 1, store.channelReads)
	require.Equal(t, 1, store.containsReads)
	require.Equal(t, 1, store.hasAnyReads)
}

func TestPermissionCacheExpiresEntries(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := &countingPermissionStore{channel: metadb.Channel{ChannelID: "g1", ChannelType: 2}}
	cache := newPermissionCache(store, 10*time.Millisecond, func() time.Time { return now })

	_, err := cache.GetChannelForPermission(context.Background(), "g1", 2)
	require.NoError(t, err)
	now = now.Add(11 * time.Millisecond)
	_, err = cache.GetChannelForPermission(context.Background(), "g1", 2)
	require.NoError(t, err)

	require.Equal(t, 2, store.channelReads)
}

func TestPermissionCacheReusesNotFoundChannelReadsWithinTTL(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := &countingPermissionStore{channelErr: metadb.ErrNotFound}
	cache := newPermissionCache(store, time.Minute, func() time.Time { return now })

	for i := 0; i < 2; i++ {
		_, err := cache.GetChannelForPermission(context.Background(), "missing", 1)
		require.ErrorIs(t, err, metadb.ErrNotFound)
	}

	require.Equal(t, 1, store.channelReads)
}

func TestPermissionCacheDoesNotReuseTransientChannelReadErrors(t *testing.T) {
	now := time.Unix(1700000000, 0)
	store := &countingPermissionStore{channelErr: errors.New("temporary failure")}
	cache := newPermissionCache(store, time.Minute, func() time.Time { return now })

	for i := 0; i < 2; i++ {
		_, err := cache.GetChannelForPermission(context.Background(), "g1", 2)
		require.ErrorContains(t, err, "temporary failure")
	}

	require.Equal(t, 2, store.channelReads)
}

type countingPermissionStore struct {
	channel       metadb.Channel
	channelErr    error
	contains      map[string]bool
	hasAny        map[string]bool
	channelReads  int
	containsReads int
	hasAnyReads   int
}

func (s *countingPermissionStore) GetChannelForPermission(context.Context, string, int64) (metadb.Channel, error) {
	s.channelReads++
	if s.channelErr != nil {
		return metadb.Channel{}, s.channelErr
	}
	return s.channel, nil
}

func (s *countingPermissionStore) ContainsChannelSubscriber(_ context.Context, channelID string, channelType int64, uid string) (bool, error) {
	s.containsReads++
	return s.contains[permissionCacheContainsKey{channelID: channelID, channelType: channelType, uid: uid}.String()], nil
}

func (s *countingPermissionStore) HasChannelSubscribers(_ context.Context, channelID string, channelType int64) (bool, error) {
	s.hasAnyReads++
	return s.hasAny[permissionCacheChannelKey{channelID: channelID, channelType: channelType}.String()], nil
}
