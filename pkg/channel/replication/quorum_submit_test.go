package replication

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func continuationLog(t *testing.T, w durabilityDispatcher) (*quorumLog, []Proposal) {
	t.Helper()
	h := newReplicaHarness(t, 1, 2, 3)
	l, err := newQuorumLog(quorumLogConfig{Local: 1, Store: h.stores[1], Recovery: h, Durability: w, RecoveryTimeout: time.Minute, RecoveryPageBytes: 64 << 10, MaxChannels: 8, MaxVoters: 3, MaxProposalRecords: 256, MaxProposalBytes: 64 << 10, MaxRetainedCommands: 16})
	if err != nil {
		t.Fatal(err)
	}
	var proposals []Proposal
	for i, key := range []string{"submit-a", "submit-b"} {
		authority := Authority{Key: ch.ChannelKey("1:" + key), ChannelID: ch.ChannelID{ID: key, Type: 1}, ID: AuthorityID{ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1}, Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2}
		if _, err := l.Install(context.Background(), authority); err != nil {
			t.Fatal(err)
		}
		proposals = append(proposals, Proposal{Key: authority.Key, Expected: authority.ID, CommandID: ch.CommandID{31: byte(i + 1)}, Records: []ch.Record{{ID: uint64(i + 1), Epoch: 1, FromUID: "sender", ClientMsgNo: key, Payload: []byte("owned"), SizeBytes: 5, ServerTimestampMS: 1}}})
	}
	return l, proposals
}
func proveContinuation(w *continuationWriter, p Proposal) {
	key := string(p.Key)
	peer := []ch.NodeID{2, 3}[preferredFollowerIndex(p.Key, 2)]
	w.callback(key, 1)(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
	w.callback(key, peer)(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
}
func TestQuorumSubmitIndependentAndReentrantDurableRetry(t *testing.T) {
	w := &continuationWriter{}
	l, ps := continuationLog(t, w)
	done := 0
	for _, proposal := range ps {
		if err := l.SubmitCommit(context.Background(), proposal, func(receipt Receipt, err error) {
			if err != nil || receipt.HW != 1 || receipt.CommandID != proposal.CommandID {
				t.Error(receipt, err)
			}
			// A user callback must not still own the exact Channel mutex.
			retried, e := l.Commit(context.Background(), proposal)
			if e != nil || retried != receipt {
				t.Error(retried, e)
			}
			done++
		}); err != nil {
			t.Fatal(err)
		}
	}
	if done != 0 {
		t.Fatal("completed before proof")
	}
	for _, p := range ps {
		if w.callback(string(p.Key), 1) == nil {
			t.Fatal("independent start blocked")
		}
		proveContinuation(w, p)
	}
	if done != 2 {
		t.Fatal(done)
	}
}
func TestQuorumSubmitCancellationRetainsPendingUntilExactRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &continuationWriter{}
		l, ps := continuationLog(t, w)
		p := ps[0]
		ctx, cancel := context.WithCancel(context.Background())
		results := make(chan error, 2)
		if err := l.SubmitCommit(ctx, p, func(_ Receipt, e error) { results <- e }); err != nil {
			t.Fatal(err)
		}
		oldLocal := w.callback(string(p.Key), 1)
		oldPeer := w.callback(string(p.Key), []ch.NodeID{2, 3}[preferredFollowerIndex(p.Key, 2)])
		cancel()
		synctest.Wait()
		if e := <-results; !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
		other := p
		other.CommandID = ch.CommandID{31: 9}
		if e := l.SubmitCommit(context.Background(), other, func(Receipt, error) { t.Fatal("rejected callback") }); !errors.Is(e, ch.ErrBackpressured) {
			t.Fatal(e)
		}
		if e := l.SubmitCommit(context.Background(), p, func(r Receipt, e error) {
			if r.HW != 1 {
				t.Error(r)
			}
			results <- e
		}); e != nil {
			t.Fatal(e)
		}
		proveContinuation(w, p)
		if e := <-results; e != nil {
			t.Fatal(e)
		}
		oldLocal(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
		oldPeer(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
		select {
		case e := <-results:
			t.Fatal("late old round repeated callback", e)
		default:
		}
		if r, e := l.Commit(context.Background(), p); e != nil || r.HW != 1 {
			t.Fatal(r, e)
		}
	})
}
func TestQuorumSubmitRejectsWithoutCallback(t *testing.T) {
	w := &continuationWriter{}
	l, ps := continuationLog(t, w)
	p := ps[0]
	if e := l.SubmitCommit(context.Background(), p, nil); !errors.Is(e, ch.ErrInvalidConfig) {
		t.Fatal(e)
	}
	p.Expected.FenceVersion++
	if e := l.SubmitCommit(context.Background(), p, func(Receipt, error) { t.Fatal("rejected callback") }); !errors.Is(e, ch.ErrStaleMeta) {
		t.Fatal(e)
	}
	if len(w.calls) != 0 {
		t.Fatal("rejected dispatch")
	}
}

type panicContinuationWriter struct{ *continuationWriter }

func (w panicContinuationWriter) submitLocal(context.Context, durableProposal, func(durabilityCompletion)) error {
	panic("ambiguous submit")
}
func TestQuorumSubmitPanicDoesNotLeakSequencerOrForgeLocalProof(t *testing.T) {
	w := panicContinuationWriter{&continuationWriter{}}
	l, ps := continuationLog(t, w)
	p := ps[0]
	done := make(chan error, 1)
	if e := l.SubmitCommit(context.Background(), p, func(_ Receipt, e error) { done <- e }); e != nil {
		t.Fatal(e)
	}
	for _, peer := range []ch.NodeID{2, 3} {
		w.callback(string(p.Key), peer)(durabilityCompletion{outcome: ch.AppendOutcomeDurable})
	}
	if e := <-done; e == nil {
		t.Fatal("success without local proof")
	}
	other := p
	other.CommandID = ch.CommandID{31: 9}
	if e := l.SubmitCommit(context.Background(), other, func(Receipt, error) { t.Fatal("rejected callback") }); !errors.Is(e, ch.ErrBackpressured) {
		t.Fatal(e)
	}
}
