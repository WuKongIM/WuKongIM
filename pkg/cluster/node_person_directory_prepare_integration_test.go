//go:build integration

package cluster

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

// Failure inventory: a three-ingress burst must queue behind eight executions;
// canceled/expired waiters must not execute; retained RPCs must stay bounded;
// releasing execution must drain the queue without inventing directory Ready.
func TestPersonDirectoryPreparationReceiverQueuesBoundedBurst(t *testing.T) {
	node, err := New(Config{NodeID: 1, ListenAddr: freeTCPAddr(t), DataDir: t.TempDir(),
		Control: ControlConfig{ClusterID: "person-prepare-admission"},
		Slots:   SlotConfig{InitialSlotCount: 1, HashSlotCount: 256, ReplicaCount: 1}})
	require.NoError(t, err)
	entered := make(chan struct{}, 32)
	release := make(chan struct{})
	var once sync.Once
	var workers sync.WaitGroup
	var active, peak atomic.Int32
	node.SetPersonDirectoryPreparation(func(ctx context.Context, items []metadb.PersonDirectoryTaskEntry) []error {
		current := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
		}
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		// Deliberately leave the task Pending. Receiver admission is not Ready.
		return make([]error, len(items))
	})
	startNode(t, node)
	t.Cleanup(func() { once.Do(func() { close(release) }); workers.Wait(); stopNodes(t, node) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	id := "prepare-left@prepare-right"
	waitNodeWriteReady(t, node)
	results := node.AdmitPersonDirectoryTasks(ctx, []metadb.PersonDirectoryTask{{ChannelID: id, ChannelType: 1, CreatedAt: 1}})
	require.Len(t, results, 1)
	require.NoError(t, results[0])
	route := waitRouteKeyLeaderReady(t, node, id)
	payload, err := json.Marshal(personPrepareRequest{Version: 1, Tasks: []metadb.PersonDirectoryTaskLocation{{HashSlot: metadb.HashSlot(route.HashSlot), ChannelID: id, ChannelType: 1}}})
	require.NoError(t, err)
	handler := node.pendingRPCHandlers[clusternet.RPCPersonDirectoryPrepare]
	call := func(parent context.Context, done chan error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			body, callErr := handler.HandleRPC(parent, payload)
			if callErr == nil {
				var reply personPrepareResponse
				if decodeErr := json.Unmarshal(body, &reply); decodeErr != nil {
					callErr = decodeErr
				} else if len(reply.Ready) != 1 || reply.Ready[0].Valid() {
					callErr = metadb.ErrInvalidArgument
				}
			}
			done <- callErr
		}()
	}
	completed := make(chan error, 32)
	for range 8 {
		call(ctx, completed)
	}
	for range 8 {
		select {
		case <-entered:
		case err := <-completed:
			t.Fatalf("execution failed before barrier: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	waiting, cancelWaiting := context.WithCancel(ctx)
	canceled := make(chan error, 1)
	call(waiting, canceled)
	select {
	case err := <-canceled:
		t.Fatalf("ninth receiver rejected instead of waiting: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancelWaiting()
	require.ErrorIs(t, <-canceled, context.Canceled)
	expired, cancelExpired := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancelExpired()
	_, err = handler.HandleRPC(expired, payload)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	// Three ingress nodes can each have eight outstanding preparation batches.
	// Keep 24 waiters plus the eight executions; the next request must reject.
	for range 24 {
		call(ctx, completed)
	}
	select {
	case err := <-completed:
		t.Fatalf("bounded burst rejected before execution was released: %v", err)
	case <-entered:
		t.Fatal("receiver exceeded eight concurrent executions")
	case <-time.After(100 * time.Millisecond):
	}
	_, err = handler.HandleRPC(ctx, payload)
	require.ErrorIs(t, err, ErrRouteNotReady)
	once.Do(func() { close(release) })
	for range 32 {
		select {
		case err := <-completed:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	require.Equal(t, int32(8), peak.Load())
	require.Len(t, entered, 24, "canceled and expired waiters must not execute")
}
