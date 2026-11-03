//go:build integration

package replication

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestPeerRuntimeCloseIncompleteRetainsJoinOwnership(t *testing.T) {
	r := newSharedCompletionRuntime(t, &sharedCallbackBatchLink{batches: make(chan []ch.ChannelKey, 4)})
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, r.Close(ctx), "same cleanup owner must remain joinable after the first caller expired")
		assertSharedCompletionRuntimeIdle(t, r)
	})
	request := testReplicateRequest(t, "1:close-marker", "close-marker", 1, []byte("a"))
	require.NoError(t, r.peers.submit(context.Background(), 2, request, func(_ ReplicateResult, _ error) {
		close(entered)
		<-release
		close(returned)
	}))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("held callback did not enter")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err := r.Close(ctx)
	cancel()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	var incomplete interface{ StopIncomplete() bool }
	require.True(t, errors.As(err, &incomplete) && incomplete.StopIncomplete(), "caller expiry must preserve an explicit unfinished-cleanup marker")
	unblock()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("accepted callback did not return")
	}
	require.NoError(t, r.Close(context.Background()))
}

func TestPeerRuntimeJoinedCloseWinsCanceledCaller(t *testing.T) {
	r := newSharedCompletionRuntime(t, &sharedCallbackBatchLink{batches: make(chan []ch.ChannelKey, 1)})
	t.Cleanup(func() { require.NoError(t, r.Close(context.Background())) })
	require.NoError(t, r.Close(context.Background()))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, r.Close(ctx), "a joined terminal result takes precedence over a canceled waiter")
}
