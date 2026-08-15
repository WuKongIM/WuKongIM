package replication

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

func TestQuorumLogInstallRecoversBarrierThenCommitsAndReplaysExactProposal(t *testing.T) {
	harness := newReplicaHarness(t, 1, 2, 3)
	log, err := newQuorumLog(quorumLogConfig{
		Local: 1, Store: harness.stores[1], Recovery: harness, Durability: harness,
		RecoveryTimeout: time.Minute, RecoveryPageBytes: 64 << 10,
		MaxChannels: 8, MaxProposalRecords: 256, MaxProposalBytes: 64 << 10, MaxRetainedCommands: 16,
	})
	if err != nil {
		t.Fatalf("newQuorumLog() error = %v", err)
	}
	authority := Authority{
		Key: "1:quorum-log", ChannelID: ch.ChannelID{ID: "quorum-log", Type: 1},
		ID:     AuthorityID{ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
	}
	installed, err := log.Install(context.Background(), authority)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if installed.Authority != authority.ID || installed.LEO != 1 || installed.HW != 1 {
		t.Fatalf("Install() = %+v, want current-term barrier at 1", installed)
	}
	proposal := Proposal{
		Key: authority.Key, Expected: authority.ID, CommandID: ch.CommandID{31: 9},
		Records: []ch.Record{{
			ID: 91, Epoch: 3, FromUID: "sender", ClientMsgNo: "client-91",
			Payload: []byte("payload"), SizeBytes: len("payload"), ServerTimestampMS: 91,
		}},
	}
	receipt, err := log.Commit(context.Background(), proposal)
	if err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	wantReceipt := Receipt{Authority: authority.ID, CommandID: proposal.CommandID, First: 2, Last: 2, HW: 2}
	if receipt != wantReceipt {
		t.Fatalf("Commit() = %+v, want %+v", receipt, wantReceipt)
	}
	beforeReplay := harness.syncCalls
	replayed, err := log.Commit(context.Background(), proposal)
	if err != nil || replayed != receipt {
		t.Fatalf("Commit(exact retry) = %+v, %v; want %+v", replayed, err, receipt)
	}
	if harness.syncCalls != beforeReplay {
		t.Fatalf("exact retry issued %d additional store writes, want zero", harness.syncCalls-beforeReplay)
	}
	changed := proposal
	changed.Records = cloneRecords(proposal.Records)
	changed.Records[0].Payload = []byte("changed")
	changed.Records[0].SizeBytes = len(changed.Records[0].Payload)
	if _, err := log.Commit(context.Background(), changed); !errors.Is(err, ch.ErrLogConflict) {
		t.Fatalf("Commit(command reuse with changed content) error = %v, want %v", err, ch.ErrLogConflict)
	}
	if harness.syncCalls != beforeReplay {
		t.Fatalf("conflicting retry issued %d additional store writes, want zero", harness.syncCalls-beforeReplay)
	}
	for _, voter := range authority.Voters {
		loaded, loadErr := harness.stores[voter].Load(context.Background(), LoadBatch{Items: []LoadRequest{{ChannelKey: authority.Key, ChannelID: authority.ChannelID}}})
		if loadErr != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil || loaded.Items[0].State.LEO != 2 ||
			loaded.Items[0].State.Committed != 1 || loaded.Items[0].State.Manifest.CommandID != proposal.CommandID {
			t.Fatalf("voter %d state = %+v, error %v; want proposal at 2 with persisted prior HW 1", voter, loaded, loadErr)
		}
	}

	restarted, err := newQuorumLog(quorumLogConfig{
		Local: 1, Store: harness.stores[1], Recovery: harness, Durability: harness,
		RecoveryTimeout: time.Minute, RecoveryPageBytes: 64 << 10,
		MaxChannels: 8, MaxProposalRecords: 256, MaxProposalBytes: 64 << 10, MaxRetainedCommands: 16,
	})
	if err != nil {
		t.Fatalf("newQuorumLog(restart) error = %v", err)
	}
	beforeRestart := harness.syncCalls
	reinstalled, err := restarted.Install(context.Background(), authority)
	if err != nil {
		t.Fatalf("Install(restart) error = %v", err)
	}
	if reinstalled.Authority != authority.ID || reinstalled.LEO != 2 || reinstalled.HW != 2 {
		t.Fatalf("Install(restart) = %+v, want recovered current-authority prefix at 2", reinstalled)
	}
	if harness.syncCalls != beforeRestart {
		t.Fatalf("Install(restart) issued %d barrier writes, want zero", harness.syncCalls-beforeRestart)
	}
	restartedReceipt, err := restarted.Commit(context.Background(), proposal)
	if err != nil || restartedReceipt != receipt {
		t.Fatalf("Commit(restart exact retry) = %+v, %v; want %+v", restartedReceipt, err, receipt)
	}
	if harness.syncCalls != beforeRestart+len(authority.Voters) {
		t.Fatalf("restart exact retry issued %d store attempts, want one conflict proof per voter", harness.syncCalls-beforeRestart)
	}
	for _, voter := range authority.Voters {
		loaded, loadErr := harness.stores[voter].Load(context.Background(), LoadBatch{Items: []LoadRequest{{ChannelKey: authority.Key, ChannelID: authority.ChannelID}}})
		if loadErr != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil || loaded.Items[0].State.LEO != 2 {
			t.Fatalf("voter %d frontier after restart retry = %+v, error %v; want no new row above 2", voter, loaded, loadErr)
		}
	}
}

func TestQuorumLogRetriesSameImmutableRangeAfterLostDurabilityResponses(t *testing.T) {
	harness := newReplicaHarness(t, 1, 2, 3)
	log, err := newQuorumLog(quorumLogConfig{
		Local: 1, Store: harness.stores[1], Recovery: harness, Durability: harness,
		RecoveryTimeout: time.Minute, RecoveryPageBytes: 64 << 10,
		MaxChannels: 8, MaxProposalRecords: 256, MaxProposalBytes: 64 << 10, MaxRetainedCommands: 16,
	})
	if err != nil {
		t.Fatalf("newQuorumLog() error = %v", err)
	}
	authority := Authority{
		Key: "1:lost-response", ChannelID: ch.ChannelID{ID: "lost-response", Type: 1},
		ID:     AuthorityID{ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
	}
	if _, err := log.Install(context.Background(), authority); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	proposal := Proposal{
		Key: authority.Key, Expected: authority.ID, CommandID: ch.CommandID{31: 19},
		Records: []ch.Record{{
			ID: 101, Epoch: 3, FromUID: "sender", ClientMsgNo: "client-101",
			Payload: []byte("payload"), SizeBytes: len("payload"), ServerTimestampMS: 101,
		}},
	}
	harness.loseResponses = len(authority.Voters)
	if _, err := log.Commit(context.Background(), proposal); err == nil {
		t.Fatal("Commit() error = nil after every durability response was lost")
	}
	writesAfterLostResponse := harness.syncCalls
	other := proposal
	other.CommandID[30] = 1
	if _, err := log.Commit(context.Background(), other); !errors.Is(err, ch.ErrBackpressured) {
		t.Fatalf("Commit(other while proposal ambiguous) error = %v, want %v", err, ch.ErrBackpressured)
	}
	if harness.syncCalls != writesAfterLostResponse {
		t.Fatalf("other command issued %d writes while exact proposal was pending", harness.syncCalls-writesAfterLostResponse)
	}
	receipt, err := log.Commit(context.Background(), proposal)
	if err != nil {
		t.Fatalf("Commit(exact retry) error = %v", err)
	}
	if receipt.First != 2 || receipt.Last != 2 || receipt.HW != 2 {
		t.Fatalf("Commit(exact retry) = %+v, want original range 2", receipt)
	}
}

func TestQuorumLogHigherFencedAuthorityPermanentlyClosesOldAdmission(t *testing.T) {
	harness := newReplicaHarness(t, 1, 2, 3)
	log, err := newQuorumLog(quorumLogConfig{
		Local: 1, Store: harness.stores[1], Recovery: harness, Durability: harness,
		RecoveryTimeout: time.Minute, RecoveryPageBytes: 64 << 10,
		MaxChannels: 8, MaxProposalRecords: 256, MaxProposalBytes: 64 << 10, MaxRetainedCommands: 16,
	})
	if err != nil {
		t.Fatalf("newQuorumLog() error = %v", err)
	}
	authority := Authority{
		Key: "1:fenced-authority", ChannelID: ch.ChannelID{ID: "fenced-authority", Type: 1},
		ID:     AuthorityID{ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
	}
	if _, err := log.Install(context.Background(), authority); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	fenced := authority
	fenced.ID.LeaderTerm++
	fenced.ID.FenceVersion++
	fenced.WriteFence = ch.WriteFence{Token: "transfer", Version: fenced.ID.FenceVersion, Reason: ch.WriteFenceReasonLeaderTransfer}
	if _, err := log.Install(context.Background(), fenced); !errors.Is(err, ch.ErrWriteFenced) {
		t.Fatalf("Install(fenced authority) error = %v, want %v", err, ch.ErrWriteFenced)
	}
	before := harness.syncCalls
	proposal := Proposal{
		Key: authority.Key, Expected: authority.ID, CommandID: ch.CommandID{31: 23},
		Records: []ch.Record{{
			ID: 111, Epoch: authority.ID.ChannelEpoch, Payload: []byte("payload"),
			SizeBytes: len("payload"), ServerTimestampMS: 111,
		}},
	}
	if _, err := log.Commit(context.Background(), proposal); !errors.Is(err, ch.ErrNotReady) && !errors.Is(err, ch.ErrStaleMeta) {
		t.Fatalf("Commit(old authority after fence) error = %v, want fail-closed admission", err)
	}
	if harness.syncCalls != before {
		t.Fatalf("old authority issued %d writes after higher fence", harness.syncCalls-before)
	}
}

type replicaHarness struct {
	stores        map[ch.NodeID]ReplicaStore
	syncCalls     int
	loseResponses int
}

func newReplicaHarness(t *testing.T, voters ...ch.NodeID) *replicaHarness {
	t.Helper()
	harness := &replicaHarness{stores: make(map[ch.NodeID]ReplicaStore, len(voters))}
	for _, voter := range voters {
		store, err := NewStoreAdapter(StoreAdapterConfig{
			Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 4, MaxBatchBytes: 1 << 20,
		})
		if err != nil {
			t.Fatalf("NewStoreAdapter(voter=%d) error = %v", voter, err)
		}
		harness.stores[voter] = store
	}
	return harness
}

func (h *replicaHarness) submitRecoveryProbe(_ context.Context, query recoveryProbeQuery, complete func(ProbeResult, error)) error {
	loaded, err := h.stores[query.Voter].Load(context.Background(), LoadBatch{Items: []LoadRequest{{
		ChannelKey: query.ChannelKey, ChannelID: query.ChannelID, ProbeIndexes: query.Indexes,
	}}})
	if err != nil || len(loaded.Items) != 1 || loaded.Items[0].Err != nil {
		if err == nil && len(loaded.Items) == 1 {
			err = loaded.Items[0].Err
		}
		complete(ProbeResult{}, err)
		return nil
	}
	request := ProbeRequest{
		ChannelKey: query.ChannelKey, ChannelID: query.ChannelID,
		Leader: query.Leader, Follower: query.Voter, Indexes: query.Indexes,
	}
	complete(ProbeResult{Proof: probeProofFor(request), State: loaded.Items[0].State, Entries: loaded.Items[0].Entries}, nil)
	return nil
}

func (h *replicaHarness) submitRecoveryFetch(_ context.Context, query recoveryFetchQuery, complete func(FetchResult, error)) error {
	fetched := h.stores[query.Donor].Fetch(context.Background(), []FetchRange{{
		ChannelKey: query.ChannelKey, ChannelID: query.ChannelID, Expected: query.Expected,
		From: query.From, Through: query.Through, Previous: query.Previous, MaxBytes: query.MaxBytes,
	}})
	if len(fetched) != 1 || fetched[0].Err != nil {
		var err error
		if len(fetched) == 1 {
			err = fetched[0].Err
		}
		complete(FetchResult{}, err)
		return nil
	}
	request := FetchRequest{
		ChannelKey: query.ChannelKey, ChannelID: query.ChannelID,
		Leader: query.Leader, Follower: query.Donor, Expected: query.Expected,
		From: query.From, Through: query.Through, Previous: query.Previous, MaxBytes: query.MaxBytes,
	}
	complete(FetchResult{Proof: fetchProofFor(request), State: fetched[0].State, Proposals: fetched[0].Proposals}, nil)
	return nil
}

func (h *replicaHarness) submitLocal(_ context.Context, proposal durableProposal, complete func(durabilityCompletion)) error {
	h.submit(1, proposal, complete)
	return nil
}

func (h *replicaHarness) submitReplica(_ context.Context, voter ch.NodeID, proposal durableProposal, complete func(durabilityCompletion)) error {
	h.submit(voter, proposal, complete)
	return nil
}

func (h *replicaHarness) submit(voter ch.NodeID, proposal durableProposal, complete func(durabilityCompletion)) {
	h.syncCalls++
	results := h.stores[voter].Sync(context.Background(), []Mutation{{
		ChannelKey: proposal.channelKey, ChannelID: proposal.channelID,
		Manifest: proposal.manifest, Records: proposal.records, Committed: proposal.committed,
	}})
	if len(results) != 1 {
		complete(durabilityCompletion{outcome: ch.AppendOutcomeUnknown, err: ch.ErrLogConflict})
		return
	}
	if h.loseResponses > 0 {
		h.loseResponses--
		complete(durabilityCompletion{outcome: ch.AppendOutcomeUnknown, err: errors.New("durability response lost")})
		return
	}
	complete(durabilityCompletion{outcome: results[0].Outcome, err: results[0].Err})
}

var _ DurableQuorumLog = (*quorumLog)(nil)
var _ recoveryProbeDispatcher = (*replicaHarness)(nil)
var _ recoveryFetchDispatcher = (*replicaHarness)(nil)
var _ durabilityDispatcher = (*replicaHarness)(nil)
