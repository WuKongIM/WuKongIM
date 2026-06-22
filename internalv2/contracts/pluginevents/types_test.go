package pluginevents

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPersistAfterCommittedCloneCopiesMutableFields(t *testing.T) {
	event := PersistAfterCommitted{
		MessageID:         10,
		MessageSeq:        3,
		ChannelID:         "room",
		ChannelType:       2,
		FromUID:           "u1",
		ClientMsgNo:       "c1",
		ServerTimestampMS: 1713859200123,
		Payload:           []byte("hello"),
		MessageScopedUIDs: []string{"u2"},
	}
	clone := event.Clone()
	event.Payload[0] = 'H'
	event.MessageScopedUIDs[0] = "changed"

	require.Equal(t, []byte("hello"), clone.Payload)
	require.Equal(t, []string{"u2"}, clone.MessageScopedUIDs)
}
