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
