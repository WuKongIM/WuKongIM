package reactor

import (
	"testing"
	"time"

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

func TestMailboxDrainIntoReusesProvidedBuffer(t *testing.T) {
	mailbox := NewMailbox(MailboxConfig{HighSize: 2, NormalSize: 2, LowSize: 2})
	backing := make([]Event, 2)
	buf := backing[:0]

	require.NoError(t, mailbox.Submit(PriorityHigh, Event{Kind: EventApplyMeta, Key: ch.ChannelKey("high")}))
	require.NoError(t, mailbox.Submit(PriorityNormal, Event{Kind: EventAppend, Key: ch.ChannelKey("normal")}))

	events := mailbox.DrainInto(buf, 2)
	require.Len(t, events, 2)
	require.Equal(t, &backing[0], &events[0])
	require.Equal(t, ch.ChannelKey("high"), events[0].Key)
	require.Equal(t, ch.ChannelKey("normal"), events[1].Key)
}

func TestMailboxWaitOneWakesOnSubmit(t *testing.T) {
	mailbox := NewMailbox(MailboxConfig{HighSize: 1, NormalSize: 1, LowSize: 1})
	stop := make(chan struct{})
	got := make(chan Event, 1)

	go func() {
		event, ok := mailbox.WaitOne(stop, nil)
		if ok {
			got <- event
		}
	}()

	require.NoError(t, mailbox.Submit(PriorityNormal, Event{Kind: EventAppend, Key: ch.ChannelKey("wake")}))
	select {
	case event := <-got:
		require.Equal(t, ch.ChannelKey("wake"), event.Key)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("mailbox wait did not wake after submit")
	}
	close(stop)
}
