package channelplane

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/stretchr/testify/require"
)

func TestSchedulerDeduplicatesReadyKeys(t *testing.T) {
	var s scheduler
	key := channel.ChannelID{ID: "g1", Type: 1}

	s.markReady(key)
	s.markReady(key)

	got, ok := s.pop()
	require.True(t, ok)
	require.Equal(t, key, got)
	_, ok = s.pop()
	require.False(t, ok)
}

func TestSchedulerPopsReadyKeysInOrderAndAllowsRequeue(t *testing.T) {
	var s scheduler
	first := channel.ChannelID{ID: "g1", Type: 1}
	second := channel.ChannelID{ID: "g2", Type: 1}

	s.markReady(first)
	s.markReady(second)

	got, ok := s.pop()
	require.True(t, ok)
	require.Equal(t, first, got)

	s.markReady(first)

	got, ok = s.pop()
	require.True(t, ok)
	require.Equal(t, second, got)
	got, ok = s.pop()
	require.True(t, ok)
	require.Equal(t, first, got)
	_, ok = s.pop()
	require.False(t, ok)
}
