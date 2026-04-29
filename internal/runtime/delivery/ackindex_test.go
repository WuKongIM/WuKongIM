package delivery

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestAckIndexBindsLooksUpAndRemovesRoutes(t *testing.T) {
	index := NewAckIndex()
	binding := AckBinding{
		SessionID:   2,
		MessageID:   101,
		ChannelID:   "u1@u2",
		ChannelType: frame.ChannelTypePerson,
		Route:       testRoute("u2", 1, 11, 2),
	}

	index.Bind(binding)

	got, ok := index.Lookup(2, 101)
	require.True(t, ok)
	require.Equal(t, binding, got)
	require.Equal(t, []AckBinding{binding}, index.LookupSession(2))

	index.Remove(2, 101)

	_, ok = index.Lookup(2, 101)
	require.False(t, ok)
	require.Empty(t, index.LookupSession(2))
}

func TestAckIndexTakeSessionRemovesAllSessionBindings(t *testing.T) {
	index := NewAckIndex()
	sessionBinding1 := AckBinding{
		SessionID:   2,
		MessageID:   101,
		ChannelID:   "u1@u2",
		ChannelType: frame.ChannelTypePerson,
		Route:       testRoute("u2", 1, 11, 2),
	}
	sessionBinding2 := AckBinding{
		SessionID:   2,
		MessageID:   102,
		ChannelID:   "g1",
		ChannelType: frame.ChannelTypeGroup,
		Route:       testRoute("u2", 1, 11, 2),
	}
	otherSessionBinding := AckBinding{
		SessionID:   3,
		MessageID:   201,
		ChannelID:   "u1@u3",
		ChannelType: frame.ChannelTypePerson,
		Route:       testRoute("u3", 1, 11, 3),
	}
	index.Bind(sessionBinding1)
	index.Bind(sessionBinding2)
	index.Bind(otherSessionBinding)

	taken := index.TakeSession(2)

	require.ElementsMatch(t, []AckBinding{sessionBinding1, sessionBinding2}, taken)
	require.Empty(t, index.LookupSession(2))
	_, ok := index.Lookup(2, 101)
	require.False(t, ok)
	_, ok = index.Lookup(2, 102)
	require.False(t, ok)
	require.Equal(t, []AckBinding{otherSessionBinding}, index.LookupSession(3))
}
