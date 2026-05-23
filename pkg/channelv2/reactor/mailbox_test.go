package reactor

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/stretchr/testify/require"
)

func TestMailboxDrainsHighBeforeNormal(t *testing.T) {
	mailbox := NewMailbox(MailboxConfig{HighSize: 2, NormalSize: 2, LowSize: 2})
	require.NoError(t, mailbox.Submit(PriorityNormal, Event{Kind: EventAppend, Key: ch.ChannelKey("normal")}))
	require.NoError(t, mailbox.Submit(PriorityHigh, Event{Kind: EventApplyMeta, Key: ch.ChannelKey("high")}))
	events := mailbox.Drain(2)
	require.Equal(t, ch.ChannelKey("high"), events[0].Key)
	require.Equal(t, ch.ChannelKey("normal"), events[1].Key)
}

func TestMailboxNormalBackpressure(t *testing.T) {
	mailbox := NewMailbox(MailboxConfig{HighSize: 1, NormalSize: 1, LowSize: 1})
	require.NoError(t, mailbox.Submit(PriorityNormal, Event{Kind: EventAppend, Key: ch.ChannelKey("a")}))
	err := mailbox.Submit(PriorityNormal, Event{Kind: EventAppend, Key: ch.ChannelKey("b")})
	require.ErrorIs(t, err, ch.ErrBackpressured)
}
