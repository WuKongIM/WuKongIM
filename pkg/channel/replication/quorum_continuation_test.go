package replication

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// continuationWriter transfers callbacks without allocating execution goroutines.
type continuationWriter struct {
	mu          sync.Mutex
	calls       map[string]map[ch.NodeID]func(durabilityCompletion)
	inlineLocal *durabilityCompletion
}

func (w *continuationWriter) submit(ctx context.Context, n ch.NodeID, p durableProposal, done func(durabilityCompletion)) error {
	if ctx.Done() != nil {
		return errors.New("durability inherited caller cancellation")
	}
	w.mu.Lock()
	if w.calls == nil {
		w.calls = make(map[string]map[ch.NodeID]func(durabilityCompletion))
	}
	key := string(p.channelKey)
	if w.calls[key] == nil {
		w.calls[key] = make(map[ch.NodeID]func(durabilityCompletion))
	}
	w.calls[key][n] = done
	local := w.inlineLocal
	w.mu.Unlock()
	if n == 1 && local != nil {
		done(*local)
	}
	return nil
}
func (w *continuationWriter) submitLocal(c context.Context, p durableProposal, f func(durabilityCompletion)) error {
	return w.submit(c, 1, p, f)
}
func (w *continuationWriter) submitReplica(c context.Context, n ch.NodeID, p durableProposal, f func(durabilityCompletion)) error {
	return w.submit(c, n, p, f)
}
func (w *continuationWriter) callback(key string, n ch.NodeID) func(durabilityCompletion) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls[key][n]
}
func TestDurableContinuationStartsIndependentRoundsWithoutWaiting(t *testing.T) {
	w := &continuationWriter{}
	completed := 0
	for i := 0; i < 8; i++ {
		key := fmt.Sprintf("round-%d", i)
		if err := startDurableRound(context.Background(), 1, []ch.NodeID{1, 2, 3}, 2, durableProposal{channelKey: ch.ChannelKey(key)}, w, func(r durableRoundResult, e error) {
			if e != nil || !r.localDurable || r.durableVotes < 2 {
				t.Error(r, e)
			}
			completed++
		}); err != nil {
			t.Fatal(err)
		}
	}
	if completed != 0 {
		t.Fatal("acknowledged without durability")
	}
	for i := 0; i < 8; i++ {
		key := fmt.Sprintf("round-%d", i)
		peer := []ch.NodeID{2, 3}[preferredFollowerIndex(ch.ChannelKey(key), 2)]
		local, remote := w.callback(key, 1), w.callback(key, peer)
		if local == nil || remote == nil {
			t.Fatal("independent dispatch blocked")
		}
		remote(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
		remote(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
		if completed != i {
			t.Fatal("duplicate follower replaced local proof")
		}
		local(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
		if completed != i+1 {
			t.Fatal("missing completion")
		}
		local(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
		if completed != i+1 {
			t.Fatal("double completion")
		}
	}
}
func TestDurableContinuationInlineConflictWaitsForInitialSubmissionTransfer(t *testing.T) {
	conflict := durabilityCompletion{outcome: ch.AppendOutcomeConflict, err: ch.ErrLogConflict}
	w := &continuationWriter{inlineLocal: &conflict}
	completed := 0
	err := startDurableRound(context.Background(), 1, []ch.NodeID{1, 2}, 2, durableProposal{channelKey: "inline"}, w, func(r durableRoundResult, e error) {
		if w.callback("inline", 2) == nil {
			t.Fatal("completion escaped before initial transfer")
		}
		if !errors.Is(e, ch.ErrLogConflict) || r.outcome != ch.AppendOutcomeConflict {
			t.Fatal(r, e)
		}
		completed++
	})
	if err != nil || completed != 1 {
		t.Fatal(err, completed)
	}
	w.callback("inline", 2)(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
	if completed != 1 {
		t.Fatal("late peer completed twice")
	}
}
func TestDurableContinuationCancelOwnsRemainingWritesAndFinishesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		w := &continuationWriter{}
		done := make(chan durableRoundResult, 2)
		if err := startDurableRound(ctx, 1, []ch.NodeID{1, 2, 3}, 2, durableProposal{channelKey: "cancel"}, w, func(r durableRoundResult, e error) {
			if !errors.Is(e, context.Canceled) {
				t.Error(e)
			}
			done <- r
		}); err != nil {
			t.Fatal(err)
		}
		cancel()
		synctest.Wait()
		select {
		case r := <-done:
			if r.outcome != ch.AppendOutcomeUnknown {
				t.Fatal(r)
			}
		default:
			t.Fatal("cancellation waited for durable result")
		}
		for _, n := range []ch.NodeID{1, 2, 3} {
			f := w.callback("cancel", n)
			if f == nil {
				t.Fatal("remaining write not transferred", n)
			}
			f(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
		}
		select {
		case <-done:
			t.Fatal("late completion repeated result")
		default:
		}
	})
}
func TestDurableContinuationRejectsBeforeOwnership(t *testing.T) {
	w := &continuationWriter{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []context.Context{nil, ctx} {
		if err := startDurableRound(c, 1, []ch.NodeID{1, 2}, 2, durableProposal{}, w, func(durableRoundResult, error) { t.Fatal("rejected callback") }); err == nil {
			t.Fatal("accepted invalid context")
		}
	}
	if len(w.calls) != 0 {
		t.Fatal("rejected work dispatched")
	}
}
