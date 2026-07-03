package messageevents

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/stretchr/testify/require"
)

func TestMessageCommittedCloneDeepCopiesPayload(t *testing.T) {
	event := MessageCommitted{
		Message: channel.Message{
			MessageID:  1,
			MessageSeq: 2,
			Payload:    []byte("original"),
		},
		SenderSessionID: 99,
	}

	clone := event.Clone()
	clone.Message.Payload[0] = 'O'

	require.Equal(t, []byte("original"), event.Message.Payload)
	require.Equal(t, []byte("Original"), clone.Message.Payload)
	require.Equal(t, event.Message.MessageID, clone.Message.MessageID)
	require.Equal(t, event.SenderSessionID, clone.SenderSessionID)
}

func TestMessageCommittedCloneDeepCopiesMessageScopedUIDs(t *testing.T) {
	event := MessageCommitted{MessageScopedUIDs: []string{"u1", "u2"}}

	clone := event.Clone()
	clone.MessageScopedUIDs[0] = "changed"

	require.Equal(t, []string{"u1", "u2"}, event.MessageScopedUIDs)
	require.Equal(t, []string{"changed", "u2"}, clone.MessageScopedUIDs)
}

func TestMessageCommittedClonePreservesCMDConversationIntentSubmitted(t *testing.T) {
	event := MessageCommitted{CMDConversationIntentSubmitted: true}

	clone := event.Clone()

	require.True(t, clone.CMDConversationIntentSubmitted)
}
