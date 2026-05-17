package app

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/stretchr/testify/require"
)

func TestDeliveryPresenceCacheReusesBatchRoutesWithinTTL(t *testing.T) {
	now := time.Unix(100, 0)
	authority := &recordingAuthoritative{batches: map[string][]presence.Route{
		"u1": {{UID: "u1", NodeID: 1, BootID: 11, SessionID: 101}},
	}}
	cache := newDeliveryPresenceCache(authority, time.Second, 1024, func() time.Time {
		return now
	})

	first, err := cache.EndpointsByUIDs(context.Background(), []string{"u1"})
	require.NoError(t, err)
	second, err := cache.EndpointsByUIDs(context.Background(), []string{"u1"})
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, [][]string{{"u1"}}, authority.uidBatches)
}

func TestDeliveryPresenceCacheExpiresBatchRoutes(t *testing.T) {
	now := time.Unix(100, 0)
	authority := &recordingAuthoritative{batches: map[string][]presence.Route{
		"u1": {{UID: "u1", NodeID: 1, BootID: 11, SessionID: 101}},
	}}
	cache := newDeliveryPresenceCache(authority, time.Second, 1024, func() time.Time {
		return now
	})

	_, err := cache.EndpointsByUIDs(context.Background(), []string{"u1"})
	require.NoError(t, err)
	now = now.Add(2 * time.Second)
	_, err = cache.EndpointsByUIDs(context.Background(), []string{"u1"})
	require.NoError(t, err)

	require.Equal(t, [][]string{{"u1"}, {"u1"}}, authority.uidBatches)
}
