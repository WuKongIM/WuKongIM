package channels

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

// Concurrent MQTT requests on one Channel must wait for the shard lock within
// their own deadline instead of failing as not ready.
func TestRequestMetaApplyWaitsForContentionWithinDeadline(t *testing.T) {
	id := ch.ChannelID{ID: "request-apply", Type: 2}
	meta := ch.Meta{ID: id, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2}, ISR: []ch.NodeID{1, 2}, MinISR: 2, Status: ch.StatusActive}
	runtime := &repairActivationRuntime{}
	svc, err := NewService(Config{LocalNode: 2, Runtime: runtime})
	require.NoError(t, err)
	lock := &svc.metaApplyLocks[channelMetaApplyLockIndex(id)]

	lock.Lock()
	time.AfterFunc(30*time.Millisecond, lock.Unlock)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, svc.applyRequestMetaContext(ctx, meta))
	require.Equal(t, 1, runtime.activations)

	// A holder that outlives the deadline yields the caller's deadline error.
	lock.Lock()
	short, done := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer done()
	require.ErrorIs(t, svc.applyRequestMetaContext(short, meta), context.DeadlineExceeded)
	lock.Unlock()
	require.Equal(t, 1, runtime.activations)

	// Without a deadline the request cannot wait unboundedly: it still yields.
	lock.Lock()
	require.ErrorIs(t, svc.applyRequestMetaContext(context.Background(), meta), ch.ErrNotReady)
	lock.Unlock()
}
