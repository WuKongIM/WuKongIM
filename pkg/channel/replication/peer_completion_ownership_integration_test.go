//go:build integration

package replication

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

// Returned envelopes still retain their request/result storage while a callback
// is held. Both item and byte admission must cover that final ownership phase.
func TestPeerCompletionRetainsItemAndByteCredit(t *testing.T) {
	for _, limit := range []string{"target_items", "target_bytes", "global_items"} {
		t.Run(limit, func(t *testing.T) {
			a := testReplicateRequest(t, "1:retained-A", "retained-A", 1, []byte("a"))
			b := testReplicateRequest(t, "1:retained-B", "retained-B", 2, []byte("b"))
			cfg := peerCompletionTestConfig()
			if limit == "target_items" || limit == "global_items" {
				cfg.MaxTargetQueuedItems, cfg.MaxQueuedItems = 2, 4
			}
			if limit == "target_bytes" {
				bytes := estimateReplicateRequestBytes(a) + estimateReplicateRequestBytes(b)
				cfg.MaxBatchBytes, cfg.MaxTargetQueuedBytes, cfg.MaxQueuedBytes = bytes, bytes, 2*bytes
			}
			f := newPeerCompletionFixture(t, cfg, nil, nil)
			entered := make(chan peerCompletionObservation, 4)
			require.NoError(t, f.b.submit(f.owner, 2, a, f.held(entered)))
			require.NoError(t, f.b.submit(f.owner, 2, b, f.held(entered)))
			if limit == "global_items" {
				otherA := testReplicateRequest(t, "1:retained-C", "retained-C", 3, []byte("c"))
				otherB := testReplicateRequest(t, "1:retained-D", "retained-D", 4, []byte("d"))
				otherA.Follower, otherB.Follower = 3, 3
				require.NoError(t, f.b.submit(f.owner, 3, otherA, f.held(entered)))
				require.NoError(t, f.b.submit(f.owner, 3, otherB, f.held(entered)))
			}
			f.activate()
			first := receivePeerCompletion(t, entered)
			require.NoError(t, first.err)
			require.Equal(t, ReplicateDurable, first.result.Status)
			if limit == "global_items" {
				// The two fixed workers enter distinct target owners in the old
				// implementation, so both returned batches are held concurrently.
				_ = receivePeerCompletion(t, entered)
			}
			extra := testReplicateRequest(t, "1:retained-extra", "retained-extra", 5, []byte("extra"))
			node := ch.NodeID(2)
			if limit == "global_items" {
				node, extra.Follower = 4, 4
			}
			err := f.b.submit(f.owner, node, extra, func(ReplicateResult, error) {})
			require.ErrorIs(t, err, ch.ErrBackpressured, "held returned envelopes must not free retained admission credit")
			f.b.mu.Lock()
			owned, bytes := f.b.ownedItems, f.b.ownedBytes
			f.b.mu.Unlock()
			want := 2
			if limit == "global_items" {
				want = 4
			}
			require.Equal(t, want, owned)
			require.Positive(t, bytes)
		})
	}
}

// MaxTargetFlight bounds network ownership, not held callback concurrency.
// Available executor capacity must still run independent accepted I/O.
func TestPeerHeldCallbackDoesNotConsumeSingleIOFlight(t *testing.T) {
	for _, prequeued := range []bool{true, false} {
		name := "prequeued"
		if !prequeued {
			name = "new_arrival"
		}
		t.Run(name, func(t *testing.T) {
			cfg := peerCompletionTestConfig()
			cfg.MaxBatchItems, cfg.MaxTargetFlight = 1, 1
			link := &sharedCallbackBatchLink{batches: make(chan []ch.ChannelKey, 16)}
			f := newPeerCompletionFixture(t, cfg, link, nil)
			entered := make(chan peerCompletionObservation, 1)
			a := testReplicateRequest(t, "1:single-io-held", "single-io-held", 1, []byte("a"))
			b := testReplicateRequest(t, "1:single-io-ready", "single-io-ready", 2, []byte("b"))
			require.NoError(t, f.b.submit(f.owner, 2, a, f.held(entered)))
			if prequeued {
				require.NoError(t, f.b.submit(f.owner, 2, b, func(ReplicateResult, error) {}))
			}
			f.activate()
			_ = receivePeerCompletion(t, entered)
			require.Equal(t, []ch.ChannelKey{a.ChannelKey}, receivePeerBatch(t, link.batches))
			if !prequeued {
				require.NoError(t, f.b.submit(f.owner, 2, b, func(ReplicateResult, error) {}))
			}
			require.Equal(t, []ch.ChannelKey{b.ChannelKey}, receivePeerBatch(t, link.batches), "a held callback must not occupy the sole I/O flight")
		})
	}
}

// A spare executor worker may deliver callbacks, but it cannot create a
// second simultaneous Exchange when the original target I/O bound is one.
func TestPeerSingleIOFlightBoundIsUnchanged(t *testing.T) {
	firstEntered, releaseFirst, spareRan := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(releaseFirst) }) }
	var active, maximum atomic.Int32
	link := readinessPeerLinkFunc(func(_ context.Context, _ ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for previous := maximum.Load(); current > previous; previous = maximum.Load() {
			if maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		if batch.Items[0].Replicate.ChannelKey == "1:bounded-io-A" {
			close(firstEntered)
			<-releaseFirst
		}
		out := ExchangeBatchResult{Version: ExchangeVersion}
		for _, item := range batch.Items {
			out.Items = append(out.Items, durableExchangeResult(item))
		}
		return out, nil
	})
	cfg := peerCompletionTestConfig()
	cfg.MaxBatchItems, cfg.MaxTargetFlight = 1, 1
	f := newPeerCompletionFixture(t, cfg, link, nil)
	t.Cleanup(unblock)
	done := make(chan peerCompletionObservation, 2)
	complete := func(result ReplicateResult, err error) { done <- peerCompletionObservation{result: result, err: err} }
	a := testReplicateRequest(t, "1:bounded-io-A", "bounded-io-A", 1, []byte("a"))
	b := testReplicateRequest(t, "1:bounded-io-B", "bounded-io-B", 2, []byte("b"))
	require.NoError(t, f.b.submit(f.owner, 2, a, complete))
	f.activate()
	select {
	case <-firstEntered:
	case <-time.After(time.Second):
		t.Fatal("first bounded Exchange did not enter")
	}
	require.NoError(t, f.b.submit(f.owner, 2, b, complete))
	// This externally admitted test task proves the second fixed executor
	// worker can run while A owns I/O; no sleep supplies the negative check.
	require.NoError(t, f.executor.Submit(func() { close(spareRan) }))
	select {
	case <-spareRan:
	case <-time.After(time.Second):
		t.Fatal("spare test executor worker did not run")
	}
	require.Equal(t, int32(1), maximum.Load())
	unblock()
	for i := 0; i < 2; i++ {
		_ = receivePeerCompletion(t, done)
	}
	require.Equal(t, int32(1), maximum.Load())
}

// Network progress and callback FIFO are independent: the next same-Channel
// Exchange may finish, but its callback cannot overtake an earlier held one.
func TestPeerCompletionFIFOAllowsSameChannelIOAndIndependentCallback(t *testing.T) {
	link := &sharedCallbackBatchLink{batches: make(chan []ch.ChannelKey, 16)}
	f := newPeerCompletionFixture(t, peerCompletionTestConfig(), link, nil)
	entered := make(chan peerCompletionObservation, 1)
	first := testReplicateRequest(t, "1:callback-fifo", "callback-fifo", 1, []byte("one"))
	second := testReplicateRequest(t, first.ChannelKey, "callback-fifo", 2, []byte("two"))
	independent := testReplicateRequest(t, "1:callback-fifo-independent", "callback-fifo-independent", 3, []byte("other"))
	var firstReturned atomic.Bool
	secondCompleted := make(chan bool, 1)
	independentCompleted := make(chan peerCompletionObservation, 1)
	require.NoError(t, f.b.submit(f.owner, 2, first, func(result ReplicateResult, err error) {
		entered <- peerCompletionObservation{result: result, err: err}
		<-f.release
		firstReturned.Store(true)
	}))
	f.activate()
	_ = receivePeerCompletion(t, entered)
	_ = receivePeerBatch(t, link.batches)
	require.NoError(t, f.b.submit(f.owner, 2, second, func(ReplicateResult, error) {
		secondCompleted <- firstReturned.Load()
	}))
	require.NoError(t, f.b.submit(f.owner, 2, independent, func(result ReplicateResult, err error) {
		independentCompleted <- peerCompletionObservation{result: result, err: err}
	}))
	// Observe the actual second same-key wire request, not a fabricated local
	// scheduling signal. An unrelated callback supplies the progress barrier.
	seenSecond := false
	for i := 0; i < 2 && !seenSecond; i++ {
		for _, key := range receivePeerBatch(t, link.batches) {
			seenSecond = seenSecond || key == second.ChannelKey
		}
	}
	require.True(t, seenSecond, "the next same-Channel I/O must not wait for callback return")
	_ = receivePeerCompletion(t, independentCompleted)
	select {
	case <-secondCompleted:
		t.Fatal("later same-Channel callback overtook the held original callback")
	default:
	}
	f.unblock()
	select {
	case after := <-secondCompleted:
		require.True(t, after)
	case <-time.After(time.Second):
		t.Fatal("same-Channel callback did not complete after its predecessor returned")
	}
}

// This cancellation occurs only after a valid durable Exchange result has
// reached an original callback. Earlier cancellation retains its old rules.
func TestPeerReturnedCompletionSurvivesCallerCancellation(t *testing.T) {
	f := newPeerCompletionFixture(t, peerCompletionTestConfig(), nil, nil)
	caller, cancelCaller := context.WithCancel(context.Background())
	defer cancelCaller()
	entered := make(chan peerCompletionObservation, 1)
	done := make(chan peerCompletionObservation, 1)
	a := testReplicateRequest(t, "1:cancel-returned-A", "cancel-returned-A", 1, []byte("a"))
	b := testReplicateRequest(t, "1:cancel-returned-B", "cancel-returned-B", 2, []byte("b"))
	require.NoError(t, f.b.submit(caller, 2, a, f.held(entered)))
	require.NoError(t, f.b.submit(caller, 2, b, func(result ReplicateResult, err error) {
		<-caller.Done()
		done <- peerCompletionObservation{result: result, err: err}
	}))
	f.activate()
	first := receivePeerCompletion(t, entered)
	require.NoError(t, first.err)
	require.Equal(t, ReplicateDurable, first.result.Status)
	cancelCaller()
	result := receivePeerCompletion(t, done)
	require.NoError(t, result.err)
	require.Equal(t, ReplicateDurable, result.result.Status)
}

func TestPeerCompletionCancellationBeforeAndDuringIOKeepsOldOutcomes(t *testing.T) {
	t.Run("caller_canceled_before_admission", func(t *testing.T) {
		link := &sharedCallbackBatchLink{batches: make(chan []ch.ChannelKey, 4)}
		f := newPeerCompletionFixture(t, peerCompletionTestConfig(), link, nil)
		caller, cancel := context.WithCancel(context.Background())
		cancel()
		var callbacks atomic.Int32
		request := testReplicateRequest(t, "1:pre-admission-cancel", "pre-admission-cancel", 1, []byte("a"))
		require.ErrorIs(t, f.b.submit(caller, 2, request, func(ReplicateResult, error) { callbacks.Add(1) }), context.Canceled)
		require.Zero(t, callbacks.Load())
		select {
		case <-link.batches:
			t.Fatal("canceled admission reached the peer transport")
		default:
		}
	})
	t.Run("owner_canceled_during_io", func(t *testing.T) {
		ioEntered := make(chan struct{}, 1)
		link := readinessPeerLinkFunc(func(ctx context.Context, _ ch.NodeID, _ ExchangeBatch) (ExchangeBatchResult, error) {
			ioEntered <- struct{}{}
			<-ctx.Done()
			return ExchangeBatchResult{}, ctx.Err()
		})
		f := newPeerCompletionFixture(t, peerCompletionTestConfig(), link, nil)
		entered, done := make(chan peerCompletionObservation, 1), make(chan peerCompletionObservation, 1)
		a := testReplicateRequest(t, "1:io-cancel-A", "io-cancel-A", 1, []byte("a"))
		b := testReplicateRequest(t, "1:io-cancel-B", "io-cancel-B", 2, []byte("b"))
		require.NoError(t, f.b.submit(f.owner, 2, a, f.held(entered)))
		require.NoError(t, f.b.submit(f.owner, 2, b, func(result ReplicateResult, err error) { done <- peerCompletionObservation{result: result, err: err} }))
		f.activate()
		select {
		case <-ioEntered:
		case <-time.After(time.Second):
			t.Fatal("accepted I/O did not enter before owner cancellation")
		}
		f.cancelOwner()
		for _, got := range []peerCompletionObservation{receivePeerCompletion(t, entered), receivePeerCompletion(t, done)} {
			require.ErrorIs(t, got.err, context.Canceled)
			require.Equal(t, ReplicateOutcomeUnknown, got.result.Status, "I/O cancellation is not a durable proof")
		}
	})
}

func TestPeerSharedBatchCompletionPreservesErrors(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "transport_error"
		if malformed {
			name = "invalid_result_cardinality"
		}
		t.Run(name, func(t *testing.T) {
			transportErr := errors.New("test peer transport failure")
			link := readinessPeerLinkFunc(func(_ context.Context, _ ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
				if !malformed {
					return ExchangeBatchResult{}, transportErr
				}
				return ExchangeBatchResult{Version: ExchangeVersion, Items: []ExchangeItemResult{durableExchangeResult(batch.Items[0])}}, nil
			})
			f := newPeerCompletionFixture(t, peerCompletionTestConfig(), link, nil)
			entered, done := make(chan peerCompletionObservation, 1), make(chan peerCompletionObservation, 1)
			a := testReplicateRequest(t, "1:error-held", "error-held", 1, []byte("a"))
			b := testReplicateRequest(t, "1:error-independent", "error-independent", 2, []byte("b"))
			require.NoError(t, f.b.submit(f.owner, 2, a, f.held(entered)))
			require.NoError(t, f.b.submit(f.owner, 2, b, func(result ReplicateResult, err error) { done <- peerCompletionObservation{result: result, err: err} }))
			f.activate()
			first, independent := receivePeerCompletion(t, entered), receivePeerCompletion(t, done)
			for _, got := range []peerCompletionObservation{first, independent} {
				require.Error(t, got.err)
				require.Equal(t, ReplicateOutcomeUnknown, got.result.Status)
				if !malformed {
					require.ErrorIs(t, got.err, transportErr)
				}
			}
		})
	}
}

// Rejected executor admission must keep returned results owned. The existing
// supervised flush is driven explicitly here, without a new business submit.
func TestPeerCompletionExecutorRejectionKeepsOwnedResultForRetry(t *testing.T) {
	var rejector *completionRejectingExecutor
	link := readinessPeerLinkFunc(func(_ context.Context, _ ch.NodeID, batch ExchangeBatch) (ExchangeBatchResult, error) {
		rejector.refuse.Store(true)
		out := ExchangeBatchResult{Version: ExchangeVersion}
		for _, item := range batch.Items {
			out.Items = append(out.Items, durableExchangeResult(item))
		}
		return out, nil
	})
	f := newPeerCompletionFixture(t, peerCompletionTestConfig(), link, func(base peerExecutor) peerExecutor {
		rejector = &completionRejectingExecutor{next: base, rejected: make(chan struct{}, 1)}
		return rejector
	})
	defer rejector.refuse.Store(false)
	entered, done := make(chan peerCompletionObservation, 1), make(chan peerCompletionObservation, 1)
	a := testReplicateRequest(t, "1:reject-held", "reject-held", 1, []byte("a"))
	b := testReplicateRequest(t, "1:reject-independent", "reject-independent", 2, []byte("b"))
	require.NoError(t, f.b.submit(f.owner, 2, a, f.held(entered)))
	require.NoError(t, f.b.submit(f.owner, 2, b, func(result ReplicateResult, err error) { done <- peerCompletionObservation{result: result, err: err} }))
	f.activate()
	select {
	case <-rejector.rejected:
	case <-time.After(time.Second):
		t.Fatal("returned completion did not exercise the injected executor rejection boundary")
	}
	f.b.mu.Lock()
	owned := f.b.ownedItems
	f.b.mu.Unlock()
	require.Equal(t, 2, owned, "executor refusal cannot release accepted terminal debt")
	rejector.refuse.Store(false)
	require.NoError(t, f.b.flushDeferred())
	_ = receivePeerCompletion(t, entered)
	_ = receivePeerCompletion(t, done)
}

func TestPeerCompletionPanicDoesNotLoseSharedBatchSibling(t *testing.T) {
	caught := make(chan any, 2)
	f := newPeerCompletionFixture(t, peerCompletionTestConfig(), nil, func(base peerExecutor) peerExecutor {
		return completionTrapExecutor{next: base, caught: caught}
	})
	// Capture the same executor-boundary panic policy while the baseline still
	// delivers callbacks on the I/O task, as well as after resource isolation.
	f.b.cfg.Executor = completionTrapExecutor{next: f.executor, caught: caught}
	entered, done := make(chan peerCompletionObservation, 1), make(chan peerCompletionObservation, 1)
	panicToken := &struct{}{}
	var calls atomic.Int32
	a := testReplicateRequest(t, "1:panic-held", "panic-held", 1, []byte("a"))
	b := testReplicateRequest(t, "1:panic-sibling", "panic-sibling", 2, []byte("b"))
	require.NoError(t, f.b.submit(f.owner, 2, a, func(result ReplicateResult, err error) {
		entered <- peerCompletionObservation{result: result, err: err}
		<-f.release
		panic(panicToken)
	}))
	require.NoError(t, f.b.submit(f.owner, 2, b, func(result ReplicateResult, err error) {
		calls.Add(1)
		done <- peerCompletionObservation{result: result, err: err}
	}))
	f.activate()
	_ = receivePeerCompletion(t, entered)
	f.unblock()
	select {
	case got := <-caught:
		require.Same(t, panicToken, got, "the existing executor-boundary panic policy must remain visible")
	case <-time.After(time.Second):
		t.Fatal("callback panic did not reach its owning executor")
	}
	result := receivePeerCompletion(t, done)
	require.NoError(t, result.err)
	require.Equal(t, ReplicateDurable, result.result.Status)
	require.Equal(t, int32(1), calls.Load())
}

func TestPeerHeldFetchCompletionRetainsReplyByteBudget(t *testing.T) {
	replicate := testReplicateRequest(t, "1:held-fetch", "held-fetch", 1, make([]byte, 512))
	identities, ok := ch.DeriveProposalEntries(replicate.Manifest, len(replicate.Records), func(i int) ch.Record { return replicate.Records[i] })
	require.True(t, ok)
	state := ReplicaState{LEO: 1, Committed: 1, Manifest: replicate.Manifest, TailIdentity: identities[0]}
	request := FetchRequest{ChannelKey: replicate.ChannelKey, ChannelID: replicate.ChannelID, Leader: 1, Follower: 2, Expected: state, From: 1, Through: 1, MaxBytes: 2048}
	want := FetchResult{Proof: fetchProofFor(request), State: state, Proposals: []RecoveryProposal{{Manifest: replicate.Manifest, Records: replicate.Records}}}
	link := &recordingPeerLink{fetches: map[ch.ChannelKey]FetchResult{request.ChannelKey: want}}
	cfg := peerCompletionTestConfig()
	cfg.MaxBatchItems = 1
	cfg.MaxBatchBytes = estimateFetchRequestBytes(request)
	cfg.MaxTargetQueuedBytes, cfg.MaxQueuedBytes = cfg.MaxBatchBytes, 2*cfg.MaxBatchBytes
	f := newPeerCompletionFixture(t, cfg, link, nil)
	entered := make(chan error, 1)
	require.NoError(t, f.b.submitFetch(f.owner, 2, request, func(result FetchResult, err error) {
		if err == nil && len(result.Proposals) != 1 {
			err = errors.New("fetch result lost its original bounded proposal")
		}
		entered <- err
		<-f.release
	}))
	f.activate()
	select {
	case err := <-entered:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("fetch callback did not enter")
	}
	extra := testReplicateRequest(t, "1:held-fetch-extra", "held-fetch-extra", 2, []byte("extra"))
	err := f.b.submit(f.owner, 2, extra, func(ReplicateResult, error) {})
	require.ErrorIs(t, err, ch.ErrBackpressured, "a held Fetch reply retains its declared MaxBytes budget")
}

type peerCompletionObservation struct {
	result ReplicateResult
	err    error
}

// This fixture observes only original callback results and declared ownership
// bounds. The two-worker executor gates setup, never implements dispatch/FIFO.
type peerCompletionFixture struct {
	owner       context.Context
	cancelOwner context.CancelFunc
	b           *peerBatcher
	executor    *sharedCallbackTestExecutor
	callbacks   *sharedCallbackTestExecutor
	release     chan struct{}
	once        sync.Once
}

func peerCompletionTestConfig() peerBatcherConfig {
	return peerBatcherConfig{ExchangeTimeout: time.Second, MaxTargetFlight: 16, MaxBatchItems: 2, MaxBatchBytes: 4096, MaxQueuedItems: 8, MaxQueuedBytes: 32768, MaxTargetQueuedItems: 4, MaxTargetQueuedBytes: 16384}
}

func newPeerCompletionFixture(t *testing.T, cfg peerBatcherConfig, link PeerLink, wrap func(peerExecutor) peerExecutor) *peerCompletionFixture {
	t.Helper()
	owner, cancel := context.WithCancel(context.Background())
	f := &peerCompletionFixture{owner: owner, cancelOwner: cancel, executor: newSharedCallbackTestExecutor(), callbacks: newSharedCallbackTestExecutor(), release: make(chan struct{})}
	cfg.OwnerContext, cfg.Executor = owner, f.executor
	cfg.CallbackExecutor, cfg.CallbackWorkers = f.callbacks, 2
	if wrap != nil {
		cfg.CallbackExecutor = wrap(cfg.CallbackExecutor)
	}
	if link == nil {
		link = &sharedCallbackBatchLink{batches: make(chan []ch.ChannelKey, 32)}
	}
	cfg.Link = link
	var err error
	f.b, err = newPeerBatcher(cfg)
	require.NoError(t, err)
	t.Cleanup(func() {
		f.unblock()
		f.executor.activate()
		f.callbacks.activate()
		f.executor.closeAndJoin(t)
		f.callbacks.closeAndJoin(t)
		cancel()
		f.b.mu.Lock()
		defer f.b.mu.Unlock()
		require.Zero(t, f.b.ownedItems)
		require.Zero(t, f.b.ownedBytes)
		for _, target := range f.b.targets {
			require.Zero(t, target.workerCount())
			require.Empty(t, target.inflight)
		}
	})
	return f
}

func (f *peerCompletionFixture) unblock() { f.once.Do(func() { close(f.release) }) }

func (f *peerCompletionFixture) activate() {
	f.executor.activate()
	f.callbacks.activate()
}

func (f *peerCompletionFixture) held(entered chan<- peerCompletionObservation) func(ReplicateResult, error) {
	return func(result ReplicateResult, err error) {
		entered <- peerCompletionObservation{result: result, err: err}
		<-f.release
	}
}

func receivePeerCompletion(t *testing.T, result <-chan peerCompletionObservation) peerCompletionObservation {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(time.Second):
		t.Fatal("accepted independent completion did not progress")
		return peerCompletionObservation{}
	}
}

func receivePeerBatch(t *testing.T, batches <-chan []ch.ChannelKey) []ch.ChannelKey {
	t.Helper()
	select {
	case got := <-batches:
		return got
	case <-time.After(time.Second):
		t.Fatal("accepted independent Exchange did not progress")
		return nil
	}
}

type completionRejectingExecutor struct {
	next     peerExecutor
	refuse   atomic.Bool
	rejected chan struct{}
}

func (e *completionRejectingExecutor) Submit(task func()) error {
	if e.refuse.Load() {
		select {
		case e.rejected <- struct{}{}:
		default:
		}
		return ch.ErrBackpressured
	}
	return e.next.Submit(task)
}

type completionTrapExecutor struct {
	next   peerExecutor
	caught chan<- any
}

func (e completionTrapExecutor) Submit(task func()) error {
	return e.next.Submit(func() {
		defer func() {
			if value := recover(); value != nil {
				e.caught <- value
			}
		}()
		task()
	})
}
