//go:build integration

package replication

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

// Independent Channels must not inherit a held callback from an already
// returned shared Exchange. The second case controls for ordinary ordering.
func TestPeerSharedBatchCallbackDoesNotBlockIndependentChannel(t *testing.T) {
	for _, heldFirst := range []bool{true, false} {
		name := "held_first"
		if !heldFirst {
			name = "held_second_control"
		}
		t.Run(name, func(t *testing.T) {
			owner, cancel := context.WithCancel(context.Background())
			executor := newSharedCallbackTestExecutor()
			callbacks := newSharedCallbackTestExecutor()
			link := &sharedCallbackBatchLink{batches: make(chan []ch.ChannelKey, 2)}
			b, err := newPeerBatcher(peerBatcherConfig{
				Link: link, Executor: executor, CallbackExecutor: callbacks, CallbackWorkers: 2, OwnerContext: owner,
				ExchangeTimeout: time.Second, MaxTargetFlight: 16,
				MaxBatchItems: 2, MaxBatchBytes: 4096,
				MaxQueuedItems: 8, MaxQueuedBytes: 32768,
				MaxTargetQueuedItems: 4, MaxTargetQueuedBytes: 16384,
			})
			require.NoError(t, err)
			type completion struct {
				result ReplicateResult
				err    error
			}
			heldEntered := make(chan completion, 1)
			independentCompleted := make(chan completion, 1)
			heldReturned, releaseHeld := make(chan struct{}), make(chan struct{})
			var release sync.Once
			unblock := func() { release.Do(func() { close(releaseHeld) }) }
			var heldCalls, independentCalls atomic.Int32
			t.Cleanup(func() {
				unblock()
				executor.activate()
				callbacks.activate()
				executor.closeAndJoin(t)
				callbacks.closeAndJoin(t)
				cancel()
				b.mu.Lock()
				defer b.mu.Unlock()
				require.Zero(t, b.ownedItems)
				require.Zero(t, b.ownedBytes)
				for _, target := range b.targets {
					require.Zero(t, target.workerCount())
					require.Empty(t, target.inflight)
				}
			})
			held := testReplicateRequest(t, "1:shared-batch-held", "shared-batch-held", 1, []byte("held"))
			independent := testReplicateRequest(t, "1:shared-batch-independent", "shared-batch-independent", 2, []byte("independent"))
			submitHeld := func() {
				require.NoError(t, b.submit(owner, 2, held, func(result ReplicateResult, err error) {
					heldCalls.Add(1)
					heldEntered <- completion{result: result, err: err}
					<-releaseHeld
					close(heldReturned)
				}))
			}
			submitIndependent := func() {
				require.NoError(t, b.submit(owner, 2, independent, func(result ReplicateResult, err error) {
					independentCalls.Add(1)
					independentCompleted <- completion{result: result, err: err}
				}))
			}
			wantBatch := []ch.ChannelKey{held.ChannelKey, independent.ChannelKey}
			if heldFirst {
				submitHeld()
				submitIndependent()
			} else {
				submitIndependent()
				submitHeld()
				wantBatch = []ch.ChannelKey{independent.ChannelKey, held.ChannelKey}
			}
			// Both submissions precede execution, preserving the original batch
			// collection policy instead of issuing a later independent RPC.
			executor.activate()
			callbacks.activate()
			select {
			case got := <-link.batches:
				require.Equal(t, wantBatch, got, "the failure requires both Channels in the same returned Exchange")
			case <-time.After(time.Second):
				t.Fatal("shared Exchange did not return")
			}
			select {
			case c := <-heldEntered:
				require.NoError(t, c.err)
				require.Equal(t, ReplicateDurable, c.result.Status)
			case <-time.After(time.Second):
				t.Fatal("held callback did not enter after its shared Exchange returned")
			}
			select {
			case c := <-independentCompleted:
				require.NoError(t, c.err)
				require.Equal(t, ReplicateDurable, c.result.Status)
			case <-time.After(time.Second):
				t.Fatal("independent Channel result from the same Exchange could not publish while its sibling callback was held")
			}
			select {
			case <-heldReturned:
				t.Fatal("held callback escaped its explicit release barrier")
			default:
			}
			unblock()
			select {
			case <-heldReturned:
			case <-time.After(time.Second):
				t.Fatal("held callback did not return after release")
			}
			require.Equal(t, int32(1), heldCalls.Load())
			require.Equal(t, int32(1), independentCalls.Load())
			select {
			case <-link.batches:
				t.Fatal("two accepted results unexpectedly required another Exchange")
			default:
			}
		})
	}
}

// sharedCallbackTestExecutor gates initial execution and supplies a fixed two
// workers. It can run independent accepted owner tasks, never one goroutine per
// item, and does not implement the callback behavior under test.
type sharedCallbackTestExecutor struct {
	start, stop chan struct{}
	tasks       chan func()
	startOnce   sync.Once
	pending     sync.WaitGroup
	workers     sync.WaitGroup
}

func newSharedCallbackTestExecutor() *sharedCallbackTestExecutor {
	return &sharedCallbackTestExecutor{start: make(chan struct{}), stop: make(chan struct{}), tasks: make(chan func(), 16)}
}

func (e *sharedCallbackTestExecutor) Submit(task func()) error {
	e.pending.Add(1)
	select {
	case e.tasks <- task:
		return nil
	default:
		e.pending.Done()
		return ch.ErrBackpressured
	}
}

func (e *sharedCallbackTestExecutor) activate() {
	e.startOnce.Do(func() {
		for i := 0; i < 2; i++ {
			e.workers.Add(1)
			go func() {
				defer e.workers.Done()
				<-e.start
				for {
					select {
					case task := <-e.tasks:
						func() { defer e.pending.Done(); task() }()
					case <-e.stop:
						return
					}
				}
			}()
		}
		close(e.start)
	})
}

func (e *sharedCallbackTestExecutor) closeAndJoin(t *testing.T) {
	t.Helper()
	joined := make(chan struct{})
	go func() { e.pending.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Error("accepted shared-batch owner tasks did not join after callback release")
		return
	}
	close(e.stop)
	workersJoined := make(chan struct{})
	go func() { e.workers.Wait(); close(workersJoined) }()
	select {
	case <-workersJoined:
	case <-time.After(time.Second):
		t.Error("fixed shared-batch executor workers did not join")
	}
}

// The only replaced boundary is the external peer transport. Every response
// carries the real request proof and returns both accepted results together.
type sharedCallbackBatchLink struct{ batches chan []ch.ChannelKey }

func (l *sharedCallbackBatchLink) Exchange(_ context.Context, _ ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
	keys := make([]ch.ChannelKey, len(batch.Items))
	results := make([]ExchangeItemResult, len(batch.Items))
	for i, item := range batch.Items {
		keys[i] = item.Replicate.ChannelKey
		results[i] = durableExchangeResult(item)
	}
	l.batches <- keys
	return ExchangeBatchResult{Version: ExchangeVersion, Items: results}, nil
}
