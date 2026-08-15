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

type gatedDurabilityWriter struct {
	localStarted chan struct{}
	localRelease chan struct{}

	mu              sync.Mutex
	replicaStarted  map[ch.NodeID]chan struct{}
	replicaRelease  map[ch.NodeID]chan struct{}
	replicaReturned map[ch.NodeID]chan struct{}
}

func newGatedDurabilityWriter(nodes ...ch.NodeID) *gatedDurabilityWriter {
	w := &gatedDurabilityWriter{
		localStarted:    make(chan struct{}),
		localRelease:    make(chan struct{}),
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

func (w *gatedDurabilityWriter) submitLocal(ctx context.Context, _ durableProposal, complete func(error)) error {
	close(w.localStarted)
	go func() {
		select {
		case <-w.localRelease:
			complete(nil)
		case <-ctx.Done():
			complete(ctx.Err())
		}
	}()
	return nil
}

func (w *gatedDurabilityWriter) submitReplica(ctx context.Context, node ch.NodeID, _ durableProposal, complete func(error)) error {
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
			complete(nil)
		case <-ctx.Done():
			complete(ctx.Err())
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

func (d immediateDurabilityDispatcher) submitLocal(_ context.Context, _ durableProposal, complete func(error)) error {
	complete(d.localErr)
	return nil
}

func (immediateDurabilityDispatcher) submitReplica(_ context.Context, _ ch.NodeID, _ durableProposal, complete func(error)) error {
	complete(nil)
	return nil
}

type countingDurabilityDispatcher struct {
	submissions int
}

func (d *countingDurabilityDispatcher) submitLocal(_ context.Context, _ durableProposal, _ func(error)) error {
	d.submissions++
	return nil
}

func (d *countingDurabilityDispatcher) submitReplica(_ context.Context, _ ch.NodeID, _ durableProposal, _ func(error)) error {
	d.submissions++
	return nil
}
