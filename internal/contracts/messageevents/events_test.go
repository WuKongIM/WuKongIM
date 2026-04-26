package messageevents

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
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
