package reactor

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
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
