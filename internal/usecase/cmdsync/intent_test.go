package cmdsync

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

func TestBuildConversationIntentUsesScopedUIDsAndSenderReadSeq(t *testing.T) {
	now := time.Unix(123, 0)
	msg := channel.Message{
		ChannelID:   "g1____cmd",
		ChannelType: frame.ChannelTypeGroup,
		MessageSeq:  9,
		FromUID:     "u1",
		Timestamp:   int32(now.Unix()),
		Framer:      frame.Framer{SyncOnce: true},
	}

	intent, ok := BuildConversationIntent(msg, []string{"u2", "u1", "u2", ""}, func() time.Time { return time.Unix(999, 0) })

	require.True(t, ok)
	require.Equal(t, ConversationIntent{
		CommandChannelID: "g1____cmd",
		ChannelType:      frame.ChannelTypeGroup,
		MessageSeq:       9,
		ActiveAt:         now.UnixNano(),
		SenderUID:        "u1",
		UserReadSeqs: map[string]uint64{
			"u1": 9,
			"u2": 0,
		},
	}, intent)
}

func TestBuildConversationIntentNormalizesSyncOnceSourceChannel(t *testing.T) {
	msg := channel.Message{ChannelID: "g1", ChannelType: frame.ChannelTypeGroup, MessageSeq: 3, Framer: frame.Framer{SyncOnce: true}}
	intent, ok := BuildConversationIntent(msg, []string{"u1"}, func() time.Time { return time.Unix(100, 0) })
	require.True(t, ok)
	require.Equal(t, "g1____cmd", intent.CommandChannelID)
}

func TestBuildConversationIntentRejectsNoPersistZeroSeqOrEmptyRecipients(t *testing.T) {
	_, ok := BuildConversationIntent(channel.Message{ChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 0}, []string{"u1"}, time.Now)
	require.False(t, ok)

	_, ok = BuildConversationIntent(channel.Message{ChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 1, Framer: frame.Framer{NoPersist: true}}, []string{"u1"}, time.Now)
	require.False(t, ok)

	_, ok = BuildConversationIntent(channel.Message{ChannelID: "g1____cmd", ChannelType: 2, MessageSeq: 1}, []string{"", " "}, time.Now)
	require.False(t, ok)
}
