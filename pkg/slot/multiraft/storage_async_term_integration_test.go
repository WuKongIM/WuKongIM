//go:build integration

package multiraft

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
)

// An old pending append must not stop higher-term leadership loss or consume a
// new leader's submitted Future when it finally completes. Separate public
// scenarios keep both proofs within the unchanged four-Ready ownership limit:
// a higher-term leader heartbeat, and same-term CheckQuorum loss followed by a
// real election. The latter avoids an unrelated follower HardState write.
func TestRuntimeStorageOldTermAppendCannotResolveNewTermFuture(t *testing.T) {
	t.Run("higher_term_leader_message", func(t *testing.T) {
		storageTermFutureScenario(t, false)
	})
	t.Run("quorum_loss_then_new_term_future", func(t *testing.T) {
		storageTermFutureScenario(t, true)
	})
}

// storageTermFutureScenario uses only Runtime, Storage, Transport and Future
// contracts. Two remote votes are needed because the candidate's self-vote
// response is itself waiting for durable storage.
func storageTermFutureScenario(t *testing.T, quorumLoss bool) {
	t.Helper()
	store := newStorageFenceGate()
	fsm := &storageAsyncFSM{internalFakeStateMachine: &internalFakeStateMachine{}, store: store}
	transport := &storageTermTransport{sent: make(chan raftpb.Message, 128)}
	rt, err := New(Options{
		NodeID: 1, Workers: 1, TickInterval: 10 * time.Millisecond, Transport: transport,
		Raft: RaftOptions{ElectionTick: 10, HeartbeatTick: 1, PreVote: false, CheckQuorum: quorumLoss},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(func() {
		store.release()
		cancel()
		if err := rt.Close(); err != nil {
			t.Errorf("Runtime.Close: %v", err)
		}
		t.Log("term/Future cleanup: held append released and Runtime joined")
	})
	const slotID SlotID = 314
	if err := rt.BootstrapSlot(ctx, BootstrapSlotRequest{
		Slot: SlotOptions{ID: slotID, Storage: store, StateMachine: fsm}, Voters: []NodeID{1, 2, 3},
	}); err != nil {
		t.Fatal(err)
	}
	var baseline Status
	storageAsyncWait(t, ctx, func() bool {
		s, e := rt.Status(slotID)
		if e != nil || s.CommitIndex == 0 || s.AppliedIndex != s.CommitIndex || len(s.CurrentVoters) != 3 {
			return false
		}
		baseline = s
		return true
	})
	entries, err := store.Entries(ctx, 1, baseline.CommitIndex+1, ^uint64(0))
	if err != nil || len(entries) != int(baseline.CommitIndex) {
		t.Fatalf("initial peer history: entries=%d commit=%d err=%v", len(entries), baseline.CommitIndex, err)
	}
	peers := map[uint64]*storageTermPeer{}
	for _, id := range []uint64{2, 3} {
		peer := &storageTermPeer{id: id, term: baseline.Term, commit: baseline.CommitIndex, terms: map[uint64]uint64{0: 0}}
		for _, entry := range entries {
			peer.terms[entry.Index] = entry.Term
			peer.last = entry.Index
		}
		peers[id] = peer
	}
	allowVotes := true
	dropPeerReplies := false
	var droppedPeerReplies [4]int
	holdPeer3AppendReplies := false
	var heldPeer3Reply *raftpb.Message
	votes := make(map[uint64]raftpb.Message, 2)
	var newEntryIndex uint64
	var newTerm, newLeaderNoopIndex, peer2AckTerm, peer2AckIndex uint64
	newPayload := proposalString("new-term-result")
	var recent [32]storageTermProtocolSummary
	var observed uint64
	newStage := &storageTermStageObserver{}
	// Each reply follows a real outbound request and the simulated peer's
	// durable log/vote state; no fabricated completion is sent to RawNode.
	drive := func(m raftpb.Message) {
		t.Helper()
		peer := peers[m.To]
		if peer == nil || m.From != 1 {
			t.Fatalf("unexpected outbound protocol endpoints: from=%d to=%d", m.From, m.To)
		}
		if m.Type == raftpb.MsgVote && !allowVotes {
			votes[m.To] = m
			return
		}
		for _, entry := range m.Entries {
			if m.To == 2 && newTerm != 0 && entry.Term == newTerm && entry.Index > store.target.Load() && len(entry.Data) == 0 {
				newLeaderNoopIndex = entry.Index
			}
			if m.To == 2 && bytes.Equal(entry.Data, newPayload) {
				if newEntryIndex != 0 && newEntryIndex != entry.Index {
					t.Fatal("new proposal appeared at multiple indexes")
				}
				newEntryIndex = entry.Index
			}
		}
		reply, ok := peer.reply(t, m)
		if ok {
			if dropPeerReplies && (reply.Type == raftpb.MsgAppResp || reply.Type == raftpb.MsgHeartbeatResp) {
				droppedPeerReplies[reply.From]++
				return
			}
			if reply.From == 2 && reply.Type == raftpb.MsgAppResp && !reply.Reject {
				peer2AckTerm, peer2AckIndex = reply.Term, reply.Index
			}
			if holdPeer3AppendReplies && reply.From == 3 && reply.Type == raftpb.MsgAppResp {
				if heldPeer3Reply == nil || reply.Term > heldPeer3Reply.Term || (reply.Term == heldPeer3Reply.Term && reply.Index >= heldPeer3Reply.Index) {
					copy := reply
					heldPeer3Reply = &copy
				}
				return
			}
			if err := rt.Step(ctx, Envelope{SlotID: slotID, Message: reply}); err != nil {
				t.Fatal(err)
			}
		}
	}
	until := func(label string, done func() bool) {
		t.Helper()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for !done() {
			select {
			case m := <-transport.sent:
				recent[observed%uint64(len(recent))] = storageTermProtocolSummary{
					Kind: m.Type.String(), From: m.From, To: m.To, Term: m.Term, Index: m.Index, Entries: len(m.Entries),
				}
				observed++
				drive(m)
			case <-tick.C:
			case <-ctx.Done():
				status, statusErr := rt.Status(slotID)
				summaries := make([]storageTermProtocolSummary, 0, len(recent))
				start := uint64(0)
				if observed > uint64(len(recent)) {
					start = observed - uint64(len(recent))
				}
				for i := start; i < observed; i++ {
					summaries = append(summaries, recent[i%uint64(len(recent))])
				}
				t.Logf("bounded term protocol: observed=%d recent=%+v", observed, summaries)
				t.Logf("public term status: role=%v term=%d leader=%d commit=%d applied=%d error=%v", status.Role, status.Term, status.LeaderID, status.CommitIndex, status.AppliedIndex, statusErr)
				t.Logf("new proposal pre-RawNode owner-control stage observed=%v", newStage.ownerControl.Load())
				t.Fatalf("%s did not complete through public protocol: %v", label, ctx.Err())
			}
		}
	}
	until("initial current-term leader commit", func() bool {
		s, e := rt.Status(slotID)
		if e != nil || s.Role != RoleLeader || s.CommitIndex == 0 || s.AppliedIndex != s.CommitIndex {
			return false
		}
		if peers[2].commit < s.CommitIndex || peers[3].commit < s.CommitIndex {
			return false
		}
		term, e := store.Term(ctx, s.CommitIndex)
		if e != nil || term != s.Term {
			return false
		}
		baseline = s
		return true
	})
	store.target.Store(baseline.CommitIndex + 1)
	store.armed.Store(true)
	old, err := rt.Propose(ctx, slotID, proposalString("old-term-result"))
	if err != nil {
		t.Fatal(err)
	}
	oldResult := storageTermObserve(t, old)
	storageFenceEntered(t, ctx, store.entered)
	if store.durable.Load() {
		t.Fatal("old append was not held before durability")
	}
	t.Log("term/Future setup: current-term leader and controlled old append confirmed")

	allowVotes = false
	if quorumLoss {
		// The real ticker detects the loss of both peers' acknowledgements.
		// No local MsgCheckQuorum or MsgHup is injected. Stepping down in the
		// same term needs no additional HardState storage owner.
		dropPeerReplies = true
	} else {
		// Peers 2 and 3 elect peer 2 using their durable shared prefix. Node
		// 1's uncommitted tail does not prevent that two-voter majority.
		higherTerm := baseline.Term + 1
		for _, peer := range peers {
			peer.term, peer.voted = higherTerm, 2
		}
		if err := rt.Step(ctx, Envelope{SlotID: slotID, Message: raftpb.Message{
			Type: raftpb.MsgHeartbeat, From: 2, To: 1, Term: higherTerm, Commit: baseline.CommitIndex,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	// Both remote votes remain available, but only peer 2 returns append ACKs
	// until release. Peer 3 persists its entries while its ACK is delayed. With
	// the local append also held, the new leader cannot commit its noop and
	// consume another Ready owner merely to manufacture this test interleaving.
	holdPeer3AppendReplies = true
	var oldDone storageTermResult
	until("old Future leadership failure before append release", func() bool {
		select {
		case oldDone = <-oldResult:
			return true
		default:
			return false
		}
	})
	if !errors.Is(oldDone.err, ErrNotLeader) || store.durable.Load() {
		t.Fatalf("old Future must fail leadership while append remains held: err=%v durable=%v", oldDone.err, store.durable.Load())
	}
	if !quorumLoss {
		store.release()
		until("old append durability after higher-term failure", store.durable.Load)
		if _, err := old.Wait(ctx); !errors.Is(err, ErrNotLeader) {
			t.Fatalf("higher-term Future was rewritten by late completion: %v", err)
		}
		if fsm.earlyApply.Load() || transport.overflow.Load() {
			t.Fatal("higher-term durability, apply or bounded transport invariant violated")
		}
		return
	}
	lost, err := rt.Status(slotID)
	if err != nil || lost.Term != baseline.Term || lost.Role != RoleFollower || droppedPeerReplies[2] == 0 || droppedPeerReplies[3] == 0 {
		t.Fatalf("leadership loss lacked actual same-term quorum-loss witness: role=%v term=%d baseline=%d dropped=%v err=%v", lost.Role, lost.Term, baseline.Term, droppedPeerReplies, err)
	}
	dropPeerReplies = false
	until("next-term candidate and both actual vote requests", func() bool {
		s, e := rt.Status(slotID)
		if e != nil || s.Role != RoleCandidate || s.Term <= baseline.Term || votes[2].Term != s.Term || votes[3].Term != s.Term {
			return false
		}
		newTerm = s.Term
		return true
	})
	allowVotes = true
	for _, id := range []uint64{2, 3} {
		drive(votes[id])
	}
	until("new leader before old append release", func() bool {
		s, e := rt.Status(slotID)
		return e == nil && s.Role == RoleLeader && s.Term == newTerm
	})
	// Finish peer 2's real log-prefix probe before submitting another entry.
	// Otherwise its pending probe may suppress the actual payload witness.
	until("peer 2 durable noop acknowledgement", func() bool {
		return newLeaderNoopIndex != 0 && peer2AckTerm == newTerm && peer2AckIndex >= newLeaderNoopIndex
	})
	fresh, err := rt.FreshStatus(ctx, slotID)
	if err != nil || fresh.Term != newTerm || fresh.Role != RoleLeader || fresh.CommitIndex != baseline.CommitIndex || fresh.Progress[2].Match < newLeaderNoopIndex || fresh.Progress[2].State != "StateReplicate" {
		t.Fatalf("peer 2 did not finish its public replication probe before new proposal: %+v err=%v", fresh, err)
	}
	current, err := rt.Propose(WithProposalStageObserver(ctx, newStage), slotID, newPayload)
	if err != nil {
		t.Fatal(err)
	}
	currentResult := storageTermObserve(t, current)
	until("new proposal replication before old append release", func() bool { return newEntryIndex != 0 })
	if store.durable.Load() || newEntryIndex <= store.target.Load() {
		t.Fatalf("new proposal lacked distinct pre-release Ready witness: index=%d old=%d durable=%v", newEntryIndex, store.target.Load(), store.durable.Load())
	}
	preRelease, err := rt.Status(slotID)
	if err != nil || preRelease.CommitIndex != baseline.CommitIndex || preRelease.Term != newTerm {
		t.Fatalf("pre-release quorum advanced without local/second-peer ACK: %+v err=%v", preRelease, err)
	}
	t.Log("term/Future witness: new-term payload replicated while old append still undurable")
	store.release()
	holdPeer3AppendReplies = false
	if heldPeer3Reply != nil {
		if err := rt.Step(ctx, Envelope{SlotID: slotID, Message: *heldPeer3Reply}); err != nil {
			t.Fatal(err)
		}
	}
	var completed storageTermResult
	until("new Future durable result", func() bool {
		select {
		case completed = <-currentResult:
			return true
		default:
			return false
		}
	})
	if completed.err != nil || completed.result.Index != newEntryIndex || completed.result.Term != newTerm || string(completed.result.Data) != "ok:new-term-result" {
		t.Fatalf("new Future bound to wrong entry: index=%d want=%d term=%d want=%d data=%q err=%v", completed.result.Index, newEntryIndex, completed.result.Term, newTerm, completed.result.Data, completed.err)
	}
	if _, err := old.Wait(ctx); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("old Future was rewritten by late completion: %v", err)
	}
	if !store.durable.Load() || fsm.earlyApply.Load() || transport.overflow.Load() {
		t.Fatal("durability, apply or bounded transport invariant violated")
	}
}

type storageTermResult struct {
	result Result
	err    error
}

type storageTermProtocolSummary struct {
	Kind                  string
	From, To, Term, Index uint64
	Entries               int
}

type storageTermStageObserver struct{ ownerControl atomic.Bool }

func (p *storageTermStageObserver) ObserveProposalStage(stage string, _ string, _ time.Duration) {
	if stage == "meta_create_slot_control_wait" {
		p.ownerControl.Store(true)
	}
}

type storageTermObserver chan storageTermResult

func (o storageTermObserver) ObserveFutureCompletion(result Result, err error) {
	o <- storageTermResult{result, err}
}

func storageTermObserve(t *testing.T, f Future) <-chan storageTermResult {
	t.Helper()
	ch := make(storageTermObserver, 1)
	observable, ok := f.(CompletionFuture)
	if !ok || !observable.ObserveCompletion(ch) {
		t.Fatal("public completion observer unavailable")
	}
	return ch
}

// storageTermTransport copies only small, bounded messages owned by this test.
type storageTermTransport struct {
	sent     chan raftpb.Message
	overflow atomic.Bool
}

func (p *storageTermTransport) Send(_ context.Context, batch []Envelope) error {
	for _, env := range batch {
		m := env.Message
		if len(m.Entries) > 32 || len(m.Context) > 4096 || m.Snapshot != nil || len(m.Responses) != 0 {
			p.overflow.Store(true)
			return errors.New("term protocol witness exceeded fixed bounds")
		}
		m.Context = append([]byte(nil), m.Context...)
		m.Entries = append([]raftpb.Entry(nil), m.Entries...)
		for i := range m.Entries {
			if len(m.Entries[i].Data) > 4096 {
				p.overflow.Store(true)
				return errors.New("term protocol entry exceeded fixed bounds")
			}
			m.Entries[i].Data = append([]byte(nil), m.Entries[i].Data...)
		}
		select {
		case p.sent <- m:
		default:
			p.overflow.Store(true)
			return errors.New("term protocol witness queue full")
		}
	}
	return nil
}

// storageTermPeer is a bounded external Storage/Transport peer: its votes and
// append acknowledgements follow persistent term, vote and log-prefix rules.
type storageTermPeer struct {
	id, term, voted, last, commit uint64
	terms                         map[uint64]uint64
}

func (p *storageTermPeer) reply(t *testing.T, m raftpb.Message) (raftpb.Message, bool) {
	t.Helper()
	if m.Term > p.term {
		p.term, p.voted = m.Term, 0
	}
	r := raftpb.Message{From: p.id, To: m.From, Term: p.term}
	switch m.Type {
	case raftpb.MsgVote:
		r.Type = raftpb.MsgVoteResp
		upToDate := m.LogTerm > p.terms[p.last] || (m.LogTerm == p.terms[p.last] && m.Index >= p.last)
		r.Reject = m.Term != p.term || (p.voted != 0 && p.voted != m.From) || !upToDate
		if !r.Reject {
			p.voted = m.From
		}
	case raftpb.MsgApp:
		r.Type, r.Index = raftpb.MsgAppResp, m.Index
		term, found := p.terms[m.Index]
		if m.Term != p.term || !found || term != m.LogTerm {
			r.Reject, r.RejectHint, r.LogTerm = true, p.last, p.terms[p.last]
			return r, true
		}
		for i, entry := range m.Entries {
			if entry.Index != m.Index+uint64(i)+1 || entry.Index > 64 || entry.Term == 0 {
				t.Fatal("unexpected finite peer log sequence")
			}
			if old, exists := p.terms[entry.Index]; exists && old != entry.Term {
				if entry.Index <= p.commit {
					t.Fatal("protocol attempted to overwrite committed peer history")
				}
				for index := entry.Index; index <= p.last; index++ {
					delete(p.terms, index)
				}
				p.last = entry.Index - 1
			}
			p.terms[entry.Index] = entry.Term
			p.last = max(p.last, entry.Index)
		}
		p.commit = max(p.commit, min(m.Commit, p.last))
		r.Index = m.Index + uint64(len(m.Entries))
	case raftpb.MsgHeartbeat:
		r.Type, r.Context = raftpb.MsgHeartbeatResp, m.Context
		if m.Term == p.term {
			p.commit = max(p.commit, min(m.Commit, p.last))
		}
	case raftpb.MsgHeartbeatResp, raftpb.MsgAppResp, raftpb.MsgVoteResp:
		return raftpb.Message{}, false
	default:
		t.Fatalf("unexpected external protocol kind %s", m.Type)
	}
	return r, true
}
