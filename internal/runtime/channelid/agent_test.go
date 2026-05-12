package channelid

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodeAgentChannel(t *testing.T) {
	require.Equal(t, "u1@agent-a", EncodeAgentChannel("u1", "agent-a"))
}

func TestDecodeAgentChannel(t *testing.T) {
	left, right, err := DecodeAgentChannel("u1@agent-a")
	require.NoError(t, err)
	require.Equal(t, "u1", left)
	require.Equal(t, "agent-a", right)
}

func TestDecodeAgentChannelRejectsInvalidShape(t *testing.T) {
	_, _, err := DecodeAgentChannel("u1")
	require.ErrorIs(t, err, ErrInvalidAgentChannel)

	_, _, err = DecodeAgentChannel("u1@")
	require.ErrorIs(t, err, ErrInvalidAgentChannel)

	_, _, err = DecodeAgentChannel("u1@agent-a@extra")
	require.ErrorIs(t, err, ErrInvalidAgentChannel)
}
