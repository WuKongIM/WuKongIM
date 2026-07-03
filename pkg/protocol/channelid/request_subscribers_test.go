package channelid

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestNormalizeRequestSubscribersTrimsAndDeduplicates(t *testing.T) {
	require.Equal(t, []string{"u1", "u2", "u3"}, NormalizeRequestSubscribers([]string{" u1 ", "u2", "u1", "", " u3 "}))
}

func TestRequestSubscriberChannelForDerivesTempCommandChannel(t *testing.T) {
	ch, err := RequestSubscriberChannelFor([]string{"u1", "u2"})
	require.NoError(t, err)
	require.Equal(t, frame.ChannelTypeTemp, ch.ChannelType)
	require.Equal(t, []string{"u1", "u2"}, ch.Subscribers)
	require.Equal(t, ToCommandChannel(ch.SourceChannelID), ch.CommandChannelID)
	require.False(t, IsCommandChannel(ch.SourceChannelID))
	require.True(t, IsCommandChannel(ch.CommandChannelID))

	again, err := RequestSubscriberChannelFor([]string{" u1 ", "u2", "u1"})
	require.NoError(t, err)
	require.Equal(t, ch.SourceChannelID, again.SourceChannelID)
	require.Equal(t, ch.CommandChannelID, again.CommandChannelID)
}

func TestRequestSubscriberChannelForRejectsEmptySubscribers(t *testing.T) {
	_, err := RequestSubscriberChannelFor([]string{"", " "})
	require.ErrorIs(t, err, ErrRequestSubscribersRequired)
}
