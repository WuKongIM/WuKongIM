package channelid

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsCommandChannel(t *testing.T) {
	require.True(t, IsCommandChannel("g1____cmd"))
	require.False(t, IsCommandChannel("g1"))
}

func TestToCommandChannel(t *testing.T) {
	require.Equal(t, "g1____cmd", ToCommandChannel("g1"))
	require.Equal(t, "g1____cmd", ToCommandChannel("g1____cmd"))
}

func TestFromCommandChannel(t *testing.T) {
	channelID, ok := FromCommandChannel("g1____cmd")
	require.True(t, ok)
	require.Equal(t, "g1", channelID)

	channelID, ok = FromCommandChannel("g1")
	require.False(t, ok)
	require.Equal(t, "g1", channelID)

	channelID, ok = FromCommandChannel("u2@u1____cmd")
	require.True(t, ok)
	require.Equal(t, "u2@u1", channelID)
}
