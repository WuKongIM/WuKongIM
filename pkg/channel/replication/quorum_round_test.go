package replication

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestDurableRoundStartsLocalAndFollowerDurabilityBeforeEitherCompletes(t *testing.T) {
	writer := newGatedDurabilityWriter(2, 3)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	type roundResult struct {
		result durableRoundResult
		err    error
	}
	done := make(chan roundResult, 1)
	go func() {
		result, err := runDurableRound(ctx, 1, []ch.NodeID{1, 2, 3}, 2, durableProposal{first: 1, last: 1}, writer)
		done <- roundResult{result: result, err: err}
	}()

	waitForSignal(t, writer.localStarted, "local durability")
	waitForSignal(t, writer.replicaStarted[2], "follower durability before local completion")
	select {
	case <-writer.replicaStarted[3]:
		t.Fatal("trailing follower durability started before the foreground write quorum completed")
	default:
	}

	close(writer.replicaRelease[2])
	waitForSignal(t, writer.replicaReturned[2], "follower durable result")
	select {
	case got := <-done:
		t.Fatalf("round completed from follower durability without local durability: %+v", got)
	default:
	}

	close(writer.localRelease)
	got := <-done
	if got.err != nil {
		t.Fatalf("runDurableRound() error = %v", got.err)
	}
	if !got.result.localDurable || got.result.durableVotes < 2 {
		t.Fatalf("runDurableRound() = %+v, want local plus write quorum durable", got.result)
	}
	close(writer.replicaRelease[3])
	waitForSignal(t, writer.replicaReturned[3], "owned trailing follower durability")
}

func TestDurableRoundDefersTrailingFollowerEvenWhenImmediateHedgeIsAvailable(t *testing.T) {
	dispatcher := newDeferredRecordingDurabilityDispatcher()
	done := make(chan error, 1)
	go func() {
		_, err := runDurableRound(context.Background(), 1, []ch.NodeID{1, 2, 3}, 2, durableProposal{
			first: 1, last: 1, channelKey: "1:deferred-trailing",
		}, dispatcher)
		done <- err
	}()

	waitForSignal(t, dispatcher.localStarted, "local durability")
	primary := []ch.NodeID{2, 3}[preferredFollowerIndex("1:deferred-trailing", 2)]
	waitForSignal(t, dispatcher.urgentStarted[primary], "foreground follower durability")
	select {
	case follower := <-dispatcher.hedged:
		t.Fatalf("follower %d was admitted as an eager hedge before quorum", follower)
	default:
	}
	close(dispatcher.urgentRelease[primary])
	close(dispatcher.localRelease)
	if err := <-done; err != nil {
		t.Fatalf("runDurableRound() error = %v", err)
	}

	trailing := ch.NodeID(2)
	if primary == trailing {
		trailing = 3
	}
	select {
	case got := <-dispatcher.deferred:
		if got != trailing {
			t.Fatalf("deferred follower = %d, want %d", got, trailing)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("timed out waiting for trailing follower deferred admission")
	}
	select {
	case <-dispatcher.urgentStarted[trailing]:
		t.Fatalf("trailing follower %d was admitted through the urgent path", trailing)
	default:
	}
}

func TestDurableRoundDoesNotSubstituteTwoFollowersForLocalDurability(t *testing.T) {
	localErr := errors.New("local sync failed")
	dispatcher := immediateDurabilityDispatcher{localErr: localErr}

	result, err := runDurableRound(context.Background(), 1, []ch.NodeID{1, 2, 3}, 2, durableProposal{first: 1, last: 1}, dispatcher)
	if !errors.Is(err, errDurableQuorumUnavailable) {
		t.Fatalf("runDurableRound() error = %v, want durable quorum unavailable", err)
	}
	if result.localDurable {
		t.Fatalf("runDurableRound() = %+v, want local durability false", result)
	}
	if result.durableVotes != 2 {
		t.Fatalf("runDurableRound() durable votes = %d, want both follower votes retained for recovery evidence", result.durableVotes)
	}
}

func TestDurableRoundRejectsDuplicateVotersBeforeDispatch(t *testing.T) {
	dispatcher := &countingDurabilityDispatcher{}

	_, err := runDurableRound(context.Background(), 1, []ch.NodeID{1, 2, 2}, 2, durableProposal{first: 1, last: 1}, dispatcher)
	if !errors.Is(err, ch.ErrInvalidConfig) {
		t.Fatalf("runDurableRound() error = %v, want invalid config", err)
	}
	if dispatcher.submissions != 0 {
		t.Fatalf("durability submissions = %d, want zero before invalid topology rejection", dispatcher.submissions)
	}
}

func TestDurableRoundPreservesOutcomeUnknown(t *testing.T) {
	dispatcher := immediateDurabilityDispatcher{localErr: errPeerOutcomeUnknown}

	result, err := runDurableRound(context.Background(), 1, []ch.NodeID{1, 2}, 2, durableProposal{first: 1, last: 1}, dispatcher)

	if !errors.Is(err, errDurableQuorumUnavailable) {
		t.Fatalf("runDurableRound() error = %v, want quorum unavailable", err)
	}
	if result.outcome != ch.AppendOutcomeUnknown {
		t.Fatalf("runDurableRound() outcome = %v, want outcome unknown", result.outcome)
	}
}

func TestDurableRoundCallerCancellationStopsWaitingButNotOwnedDurability(t *testing.T) {
	dispatcher := newGatedDurabilityWriter(2)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct {
		result durableRoundResult
		err    error
	}, 1)
	go func() {
		result, err := runDurableRound(ctx, 1, []ch.NodeID{1, 2}, 2, durableProposal{first: 1, last: 1}, dispatcher)
		done <- struct {
			result durableRoundResult
			err    error
		}{result: result, err: err}
	}()
	waitForSignal(t, dispatcher.localStarted, "owned local durability")
	waitForSignal(t, dispatcher.replicaStarted[2], "owned follower durability")
	cancel()
	got := <-done
	if !errors.Is(got.err, context.Canceled) || got.result.outcome != ch.AppendOutcomeUnknown {
		t.Fatalf("canceled wait = %+v, %v, want typed outcome unknown", got.result, got.err)
	}

	close(dispatcher.localRelease)
	close(dispatcher.replicaRelease[2])
	waitForSignal(t, dispatcher.localReturned, "local durability after caller cancellation")
	waitForSignal(t, dispatcher.replicaReturned[2], "follower durability after caller cancellation")
}

type gatedDurabilityWriter struct {
	localStarted  chan struct{}
	localRelease  chan struct{}
	localReturned chan struct{}

	mu              sync.Mutex
	replicaStarted  map[ch.NodeID]chan struct{}
	replicaRelease  map[ch.NodeID]chan struct{}
	replicaReturned map[ch.NodeID]chan struct{}
}

func newGatedDurabilityWriter(nodes ...ch.NodeID) *gatedDurabilityWriter {
	w := &gatedDurabilityWriter{
		localStarted:    make(chan struct{}),
		localRelease:    make(chan struct{}),
		localReturned:   make(chan struct{}),
		replicaStarted:  make(map[ch.NodeID]chan struct{}, len(nodes)),
		replicaRelease:  make(map[ch.NodeID]chan struct{}, len(nodes)),
		replicaReturned: make(map[ch.NodeID]chan struct{}, len(nodes)),
	}
	for _, node := range nodes {
		w.replicaStarted[node] = make(chan struct{})
		w.replicaRelease[node] = make(chan struct{})
		w.replicaReturned[node] = make(chan struct{})
	}
	return w
}

func (w *gatedDurabilityWriter) submitLocal(ctx context.Context, _ durableProposal, complete func(durabilityCompletion)) error {
	close(w.localStarted)
	go func() {
		select {
		case <-w.localRelease:
			close(w.localReturned)
			complete(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
		case <-ctx.Done():
			complete(durabilityCompletion{outcome: ch.AppendOutcomeUnknown, err: ctx.Err()})
		}
	}()
	return nil
}

func (w *gatedDurabilityWriter) submitReplica(ctx context.Context, node ch.NodeID, _ durableProposal, complete func(durabilityCompletion)) error {
	w.mu.Lock()
	started := w.replicaStarted[node]
	release := w.replicaRelease[node]
	returned := w.replicaReturned[node]
	w.mu.Unlock()
	close(started)
	go func() {
		select {
		case <-release:
			close(returned)
			complete(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
		case <-ctx.Done():
			complete(durabilityCompletion{outcome: ch.AppendOutcomeUnknown, err: ctx.Err()})
		}
	}()
	return nil
}

func waitForSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("timed out waiting for %s", name)
	}
}

type immediateDurabilityDispatcher struct {
	localErr error
}

func (d immediateDurabilityDispatcher) submitLocal(_ context.Context, _ durableProposal, complete func(durabilityCompletion)) error {
	if d.localErr == nil {
		complete(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
	} else if errors.Is(d.localErr, errPeerOutcomeUnknown) {
		complete(durabilityCompletion{outcome: ch.AppendOutcomeUnknown, err: d.localErr})
	} else {
		complete(durabilityCompletion{outcome: ch.AppendOutcomeDefinitelyNotWritten, err: d.localErr})
	}
	return nil
}

func (immediateDurabilityDispatcher) submitReplica(_ context.Context, _ ch.NodeID, _ durableProposal, complete func(durabilityCompletion)) error {
	complete(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
	return nil
}

type countingDurabilityDispatcher struct {
	submissions int
}

type deferredRecordingDurabilityDispatcher struct {
	localStarted  chan struct{}
	localRelease  chan struct{}
	urgentStarted map[ch.NodeID]chan struct{}
	urgentRelease map[ch.NodeID]chan struct{}
	hedged        chan ch.NodeID
	deferred      chan ch.NodeID
}

func newDeferredRecordingDurabilityDispatcher() *deferredRecordingDurabilityDispatcher {
	return &deferredRecordingDurabilityDispatcher{
		localStarted: make(chan struct{}), localRelease: make(chan struct{}),
		urgentStarted: map[ch.NodeID]chan struct{}{2: make(chan struct{}), 3: make(chan struct{})},
		urgentRelease: map[ch.NodeID]chan struct{}{2: make(chan struct{}), 3: make(chan struct{})},
		hedged:        make(chan ch.NodeID, 1),
		deferred:      make(chan ch.NodeID, 1),
	}
}

func (d *deferredRecordingDurabilityDispatcher) submitLocal(_ context.Context, _ durableProposal, complete func(durabilityCompletion)) error {
	close(d.localStarted)
	go func() {
		<-d.localRelease
		complete(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
	}()
	return nil
}

func (d *deferredRecordingDurabilityDispatcher) submitReplica(_ context.Context, node ch.NodeID, _ durableProposal, complete func(durabilityCompletion)) error {
	close(d.urgentStarted[node])
	go func() {
		<-d.urgentRelease[node]
		complete(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
	}()
	return nil
}

func (d *deferredRecordingDurabilityDispatcher) submitReplicaHedged(_ context.Context, node ch.NodeID, _ durableProposal, _ func(durabilityCompletion)) error {
	d.hedged <- node
	return nil
}

func (d *deferredRecordingDurabilityDispatcher) submitReplicaDeferred(_ context.Context, node ch.NodeID, _ durableProposal, _ func(durabilityCompletion)) error {
	d.deferred <- node
	return nil
}

func (d *countingDurabilityDispatcher) submitLocal(_ context.Context, _ durableProposal, _ func(durabilityCompletion)) error {
	d.submissions++
	return nil
}

func (d *countingDurabilityDispatcher) submitReplica(_ context.Context, _ ch.NodeID, _ durableProposal, _ func(durabilityCompletion)) error {
	d.submissions++
	return nil
}
