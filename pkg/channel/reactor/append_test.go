package reactor

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/stretchr/testify/require"
)

func TestValidateAppendEventRejectsWriteFencedLeader(t *testing.T) {
	meta := testMeta("write-fenced", 1, 1)
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc, err := r.lookupLoadedChannel(meta.Key)
	require.NoError(t, err)
	rc.state.WriteFence = ch.WriteFence{
		Token:   "failover-1",
		Version: 1,
		Reason:  ch.WriteFenceReasonFailover,
	}

	err = r.validateAppendEvent(context.Background(), rc, appendEvent(meta, 1, "payload"))

	require.ErrorIs(t, err, ch.ErrWriteFenced)
}

func TestValidateAppendEventReportsStaleEpochFences(t *testing.T) {
	meta := testMeta("stale-epoch", 1, 1)
	meta.Epoch = 7
	meta.LeaderEpoch = 11
	r := NewReactor(ReactorConfig{ID: 0, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 16})
	require.NoError(t, applyMetaDirect(t, r, meta))
	rc, err := r.lookupLoadedChannel(meta.Key)
	require.NoError(t, err)

	t.Run("channel epoch", func(t *testing.T) {
		event := appendEvent(meta, 1, "payload")
		event.Append.ExpectedChannelEpoch = 6
		event.Append.ExpectedLeaderEpoch = 10
		err := r.validateAppendEvent(context.Background(), rc, event)

		require.ErrorIs(t, err, ch.ErrStaleMeta)
		require.Contains(t, err.Error(), "expected channel epoch 6, actual 7")
		require.Contains(t, err.Error(), "expected leader epoch 10, actual 11")
		require.Contains(t, err.Error(), "actual leader 1")
	})

	t.Run("leader epoch", func(t *testing.T) {
		event := appendEvent(meta, 2, "payload")
		event.Append.ExpectedChannelEpoch = 7
		event.Append.ExpectedLeaderEpoch = 10
		err := r.validateAppendEvent(context.Background(), rc, event)

		require.ErrorIs(t, err, ch.ErrStaleMeta)
		require.Contains(t, err.Error(), "expected leader epoch 10, actual 11")
		require.Contains(t, err.Error(), "channel epoch 7")
		require.Contains(t, err.Error(), "actual leader 1")
	})
}
