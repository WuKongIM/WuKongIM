package replication

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestRecoveryProbeOwnerSelectsQuorumPrefixInsteadOfMinorityTail(t *testing.T) {
	prefix := recoveryIdentity(5, 5)
	minorityTail := recoveryIdentityAfter(prefix, 6, 6)
	dispatcher := &scriptedRecoveryProbeDispatcher{results: map[ch.NodeID]ProbeResult{
		1: recoveryReport(1, 5, 5, []EntryProbe{{Index: 5, Present: true, Identity: prefix}}).Result,
		2: recoveryReport(2, 5, 5, []EntryProbe{{Index: 5, Present: true, Identity: prefix}}).Result,
		3: recoveryReport(3, 6, 0, []EntryProbe{{Index: 6, Present: true, Identity: minorityTail}, {Index: 5, Present: true, Identity: prefix}}).Result,
	}}

	selected, err := recoverQuorumPrefix(context.Background(), recoveryProbeRequest{
		ChannelKey: "1:owner", ChannelID: ch.ChannelID{ID: "owner", Type: 1},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Quorum: 2, Timeout: time.Minute,
	}, dispatcher)
	if err != nil {
		t.Fatalf("recoverQuorumPrefix() error = %v", err)
	}
	if selected.Index != 5 || selected.Identity != prefix || selected.CertifiedCommitted != 5 {
		t.Fatalf("recoverQuorumPrefix() = %+v, want certified prefix 5", selected)
	}
	wantIndexes := [][]uint64{nil, {5}}
	for voter := ch.NodeID(1); voter <= 3; voter++ {
		if got := dispatcher.indexes[voter]; !reflect.DeepEqual(got, wantIndexes) {
			t.Fatalf("voter %d probe indexes = %v, want frontier then bounded identity page %v", voter, got, wantIndexes)
		}
	}
}

func TestRecoveryProbeOwnerFailsClosedWhenIdentityPageLosesQuorum(t *testing.T) {
	prefix := recoveryIdentity(5, 5)
	dispatcher := &scriptedRecoveryProbeDispatcher{
		results: map[ch.NodeID]ProbeResult{
			1: recoveryReport(1, 5, 5, []EntryProbe{{Index: 5, Present: true, Identity: prefix}}).Result,
			2: recoveryReport(2, 5, 5, []EntryProbe{{Index: 5, Present: true, Identity: prefix}}).Result,
			3: recoveryReport(3, 5, 0, []EntryProbe{{Index: 5, Present: true, Identity: prefix}}).Result,
		},
		failPage: map[ch.NodeID]bool{2: true, 3: true},
	}

	_, err := recoverQuorumPrefix(context.Background(), recoveryProbeRequest{
		ChannelKey: "1:owner", ChannelID: ch.ChannelID{ID: "owner", Type: 1},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Quorum: 2, Timeout: time.Minute,
	}, dispatcher)
	if !errors.Is(err, errRecoveryProbeIncomplete) {
		t.Fatalf("recoverQuorumPrefix() error = %v, want probe incomplete", err)
	}
}

func TestRecoveryProbeOwnerStreamsCompleteChainWithoutPermanentDistanceLimit(t *testing.T) {
	entries := makeRecoveryChain(257)
	dispatcher := &scriptedRecoveryProbeDispatcher{results: map[ch.NodeID]ProbeResult{
		1: recoveryReport(1, 257, 1, entries).Result,
		2: recoveryReport(2, 257, 1, entries).Result,
		3: recoveryReport(3, 257, 1, entries).Result,
	}}
	request := recoveryProbeRequest{
		ChannelKey: "1:paged", ChannelID: ch.ChannelID{ID: "paged", Type: 1},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Quorum: 2, Timeout: time.Minute,
	}

	selected, err := recoverQuorumPrefix(context.Background(), request, dispatcher)
	if err != nil {
		t.Fatalf("recoverQuorumPrefix() error = %v", err)
	}
	if selected.Index != 257 || selected.Identity != entries[0].Identity {
		t.Fatalf("recoverQuorumPrefix() = %+v, want complete tail 257", selected)
	}
	if got := dispatcher.indexes[1]; len(got) != 3 || len(got[0]) != 0 || len(got[1]) != 256 || !reflect.DeepEqual(got[2], []uint64{257}) {
		t.Fatalf("paged indexes = %v, want frontier + ascending 256 + 1", got)
	}
}

func TestRecoveryProbeOwnerStreamsBeyondFormerSixtyFourPageCeiling(t *testing.T) {
	const last = uint64(16_385)
	dispatcher := &generatedRecoveryProbeDispatcher{last: last}
	selected, err := recoverQuorumPrefix(context.Background(), recoveryProbeRequest{
		ChannelKey: "1:long", ChannelID: ch.ChannelID{ID: "long", Type: 1},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Quorum: 2, Timeout: time.Minute,
	}, dispatcher)
	if err != nil {
		t.Fatalf("recoverQuorumPrefix() error = %v", err)
	}
	if selected.Index != last || selected.Identity != generatedRecoveryIdentity(last) {
		t.Fatalf("recoverQuorumPrefix() = %+v, want tail %d", selected, last)
	}
	if dispatcher.rounds != 1+65 {
		t.Fatalf("probe rounds = %d, want frontier plus 65 bounded pages", dispatcher.rounds)
	}
}

func TestRecoveryProbeOwnerRejectsOversizedVoterSetBeforeDispatch(t *testing.T) {
	voters := make([]ch.NodeID, maxRecoveryProbeVoters+1)
	for index := range voters {
		voters[index] = ch.NodeID(index + 1)
	}
	dispatcher := &generatedRecoveryProbeDispatcher{last: 1}
	_, err := recoverQuorumPrefix(context.Background(), recoveryProbeRequest{
		ChannelKey: "1:many-voters", ChannelID: ch.ChannelID{ID: "many-voters", Type: 1},
		Leader: 1, Voters: voters, Quorum: len(voters)/2 + 1, Timeout: time.Minute,
	}, dispatcher)
	if !errors.Is(err, ch.ErrInvalidConfig) {
		t.Fatalf("recoverQuorumPrefix() error = %v, want invalid voter bound", err)
	}
	if dispatcher.rounds != 0 {
		t.Fatalf("dispatch rounds = %d, want zero before bounded validation", dispatcher.rounds)
	}
}

func TestRecoveryProbeOwnerRequiresBoundedOperationBeforeDispatch(t *testing.T) {
	dispatcher := &generatedRecoveryProbeDispatcher{last: 1}
	_, err := recoverQuorumPrefix(context.Background(), recoveryProbeRequest{
		ChannelKey: "1:no-timeout", ChannelID: ch.ChannelID{ID: "no-timeout", Type: 1},
		Leader: 1, Voters: []ch.NodeID{1}, Quorum: 1,
	}, dispatcher)
	if !errors.Is(err, ch.ErrInvalidConfig) {
		t.Fatalf("recoverQuorumPrefix() error = %v, want missing operation timeout rejected", err)
	}
	if dispatcher.rounds != 0 {
		t.Fatalf("dispatch rounds = %d, want zero before timeout validation", dispatcher.rounds)
	}
}

func TestRecoveryProbeOwnerPropagatesBoundedAttemptDeadlineToEveryRound(t *testing.T) {
	dispatcher := &generatedRecoveryProbeDispatcher{last: 1}
	selected, err := recoverQuorumPrefix(context.Background(), recoveryProbeRequest{
		ChannelKey: "1:deadline", ChannelID: ch.ChannelID{ID: "deadline", Type: 1},
		Leader: 1, Voters: []ch.NodeID{1}, Quorum: 1, Timeout: time.Minute,
	}, dispatcher)
	if err != nil || selected.Index != 1 {
		t.Fatalf("recoverQuorumPrefix() = %+v, %v; want complete single-voter prefix", selected, err)
	}
	if !dispatcher.sawDeadline {
		t.Fatal("recovery dispatcher did not receive the owner attempt deadline")
	}
}

func TestRecoveryProbeOwnerContinuesAfterBoundedAttemptWithoutRescanningProvenPages(t *testing.T) {
	const last = uint64(600)
	firstContext, cancelFirst := context.WithCancel(context.Background())
	firstDispatcher := &generatedRecoveryProbeDispatcher{last: last, cancel: cancelFirst, cancelRound: 3}
	request := recoveryProbeRequest{
		ChannelKey: "1:continue", ChannelID: ch.ChannelID{ID: "continue", Type: 1},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Quorum: 2, Timeout: time.Minute,
	}
	partial, err := recoverQuorumPrefix(firstContext, request, firstDispatcher)
	firstDispatcher.completePending(context.Canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("first recoverQuorumPrefix() error = %v, want canceled bounded attempt", err)
	}
	if partial.Index != 256 || partial.Continuation == nil || partial.Continuation.NextIndex != 257 {
		t.Fatalf("partial recovery = %+v, want continuation at 257", partial)
	}

	request.Continuation = partial.Continuation
	changedDispatcher := &generatedRecoveryProbeDispatcher{last: last + 1}
	if _, err := recoverQuorumPrefix(context.Background(), request, changedDispatcher); !errors.Is(err, errRecoveryProbeIncomplete) {
		t.Fatalf("changed-frontier continuation error = %v, want incomplete", err)
	}
	if len(changedDispatcher.pageStarts) != 0 {
		t.Fatalf("changed-frontier page starts = %v, want no identity page before rejection", changedDispatcher.pageStarts)
	}

	secondDispatcher := &generatedRecoveryProbeDispatcher{last: last, failVoters: map[ch.NodeID]bool{3: true}}
	selected, err := recoverQuorumPrefix(context.Background(), request, secondDispatcher)
	if err != nil {
		t.Fatalf("continued recoverQuorumPrefix() error = %v", err)
	}
	if selected.Index != last || selected.Continuation != nil {
		t.Fatalf("continued recovery = %+v, want complete tail without continuation", selected)
	}
	if len(secondDispatcher.pageStarts) != 2 || secondDispatcher.pageStarts[0] != 257 || secondDispatcher.pageStarts[1] != 513 {
		t.Fatalf("continued page starts = %v, want [257 513] without rescanning page 1", secondDispatcher.pageStarts)
	}
	if secondDispatcher.callsByVoter[3] != 1 {
		t.Fatalf("unavailable continuation voter calls = %d, want frontier only", secondDispatcher.callsByVoter[3])
	}
}

func TestRecoveryProbeOwnerStopsProbingVoterRemovedFromStableQuorum(t *testing.T) {
	entries := makeRecoveryChain(257)
	dispatcher := &scriptedRecoveryProbeDispatcher{
		results: map[ch.NodeID]ProbeResult{
			1: recoveryReport(1, 257, 1, entries).Result,
			2: recoveryReport(2, 257, 1, entries).Result,
			3: recoveryReport(3, 257, 1, entries).Result,
		},
		failPage: map[ch.NodeID]bool{3: true},
	}
	selected, err := recoverQuorumPrefix(context.Background(), recoveryProbeRequest{
		ChannelKey: "1:stable", ChannelID: ch.ChannelID{ID: "stable", Type: 1},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Quorum: 2, Timeout: time.Minute,
	}, dispatcher)
	if err != nil || selected.Index != 257 {
		t.Fatalf("recoverQuorumPrefix() = %+v, %v; want complete quorum prefix", selected, err)
	}
	if len(dispatcher.indexes[3]) != 2 {
		t.Fatalf("removed voter calls = %v, want frontier and first failed page only", dispatcher.indexes[3])
	}
}

func TestRecoveryProbeOwnerPreservesPreviousContinuationWhenLaterPageLosesQuorum(t *testing.T) {
	entries := makeRecoveryChain(257)
	results := map[ch.NodeID]ProbeResult{
		1: recoveryReport(1, 257, 1, entries).Result,
		2: recoveryReport(2, 257, 1, entries).Result,
		3: recoveryReport(3, 257, 1, entries).Result,
	}
	request := recoveryProbeRequest{
		ChannelKey: "1:late-loss", ChannelID: ch.ChannelID{ID: "late-loss", Type: 1},
		Leader: 1, Voters: []ch.NodeID{1, 2, 3}, Quorum: 2, Timeout: time.Minute,
	}
	partial, err := recoverQuorumPrefix(context.Background(), request, &scriptedRecoveryProbeDispatcher{
		results: results, failPageAt: map[ch.NodeID]int{2: 2, 3: 2},
	})
	if !errors.Is(err, errRecoveryProbeIncomplete) {
		t.Fatalf("recoverQuorumPrefix() error = %v, want later page incomplete", err)
	}
	if partial.Index != 256 || partial.Continuation == nil || partial.Continuation.NextIndex != 257 {
		t.Fatalf("partial recovery = %+v, want preserved continuation at 257", partial)
	}

	request.Continuation = partial.Continuation
	resumed := &scriptedRecoveryProbeDispatcher{results: results}
	selected, err := recoverQuorumPrefix(context.Background(), request, resumed)
	if err != nil || selected.Index != 257 {
		t.Fatalf("resumed recovery = %+v, %v; want complete tail", selected, err)
	}
	if got := resumed.indexes[1]; len(got) != 2 || !reflect.DeepEqual(got[1], []uint64{257}) {
		t.Fatalf("resumed indexes = %v, want frontier then only unproved page 257", got)
	}
}

func TestBatchingRecoveryProbeDispatcherRoutesLocalStoreAndRemotePeer(t *testing.T) {
	identity := recoveryIdentity(1, 1)
	probe := recoveryReport(1, 1, 1, []EntryProbe{{Index: 1, Present: true, Identity: identity}}).Result
	executor := &manualPeerExecutor{}
	link := &recordingPeerLink{probes: map[ch.ChannelKey]ProbeResult{"1:adapter": probe}}
	batcher, err := newPeerBatcher(peerBatcherConfig{
		Link: link, Executor: executor,
		OwnerContext: context.Background(), ExchangeTimeout: time.Minute,
		MaxBatchItems: 2, MaxBatchBytes: 4096,
		MaxQueuedItems: 4, MaxQueuedBytes: 8192, MaxTargetQueuedItems: 2, MaxTargetQueuedBytes: 4096,
	})
	if err != nil {
		t.Fatalf("newPeerBatcher() error = %v", err)
	}
	store := &recordingReplicaStore{loadResult: LoadBatchResult{Items: []LoadResult{{State: probe.State, Entries: probe.Entries}}}}
	dispatcher := &batchingRecoveryProbeDispatcher{
		local: 1, ownerContext: context.Background(), localTimeout: time.Minute,
		store: store, peers: batcher, executor: executor,
	}

	query := recoveryProbeQuery{
		ChannelKey: "1:adapter", ChannelID: ch.ChannelID{ID: "adapter", Type: 1},
		Leader: 1, Voter: 1, Indexes: []uint64{1},
	}
	localDone := make(chan ProbeResult, 1)
	if err := dispatcher.submitRecoveryProbe(context.Background(), query, func(result ProbeResult, err error) {
		if err != nil {
			t.Errorf("local probe error = %v", err)
		}
		localDone <- result
	}); err != nil {
		t.Fatalf("submit local probe error = %v", err)
	}
	executor.RunNext()
	wantLocal := probe
	wantLocal.Proof = probeProofFor(ProbeRequest{
		ChannelKey: query.ChannelKey, ChannelID: query.ChannelID,
		Leader: query.Leader, Follower: query.Voter, Indexes: query.Indexes,
	})
	if got := <-localDone; !reflect.DeepEqual(got, wantLocal) {
		t.Fatalf("local probe = %+v, want %+v", got, wantLocal)
	}
	if len(store.loadBatches) != 1 || !reflect.DeepEqual(store.loadBatches[0].Items[0].ProbeIndexes, []uint64{1}) {
		t.Fatalf("local load batches = %+v, want exact index 1", store.loadBatches)
	}

	query.Voter = 2
	remoteDone := make(chan ProbeResult, 1)
	if err := dispatcher.submitRecoveryProbe(context.Background(), query, func(result ProbeResult, err error) {
		if err != nil {
			t.Errorf("remote probe error = %v", err)
		}
		remoteDone <- result
	}); err != nil {
		t.Fatalf("submit remote probe error = %v", err)
	}
	executor.RunNext()
	wantRemote := probe
	wantRemote.Proof = probeProofFor(ProbeRequest{
		ChannelKey: query.ChannelKey, ChannelID: query.ChannelID,
		Leader: query.Leader, Follower: query.Voter, Indexes: query.Indexes,
	})
	if got := <-remoteDone; !reflect.DeepEqual(got, wantRemote) {
		t.Fatalf("remote probe = %+v, want %+v", got, wantRemote)
	}
	if len(link.batches) != 1 || link.batches[0].Items[0].Kind != ExchangeProbe {
		t.Fatalf("remote batches = %+v, want one probe exchange", link.batches)
	}
}

func TestBatchingRecoveryProbeDispatcherSupportsSingleNodeClusterWithoutPeerOwner(t *testing.T) {
	identity := recoveryIdentity(1, 1)
	probe := recoveryReport(1, 1, 1, []EntryProbe{{Index: 1, Present: true, Identity: identity}}).Result
	executor := &manualPeerExecutor{}
	dispatcher := &batchingRecoveryProbeDispatcher{
		local: 1, ownerContext: context.Background(), localTimeout: time.Minute,
		store:    &recordingReplicaStore{loadResult: LoadBatchResult{Items: []LoadResult{{State: probe.State, Entries: probe.Entries}}}},
		executor: executor,
	}
	done := make(chan error, 1)
	if err := dispatcher.submitRecoveryProbe(context.Background(), recoveryProbeQuery{
		ChannelKey: "1:single", ChannelID: ch.ChannelID{ID: "single", Type: 1},
		Leader: 1, Voter: 1, Indexes: []uint64{1},
	}, func(_ ProbeResult, err error) { done <- err }); err != nil {
		t.Fatalf("submitRecoveryProbe() error = %v", err)
	}
	executor.RunNext()
	if err := <-done; err != nil {
		t.Fatalf("single-node local probe error = %v", err)
	}
}

func TestBatchingRecoveryProbeDispatcherBoundsAcceptedLocalReadByOwnerTimeout(t *testing.T) {
	executor := &manualPeerExecutor{}
	store := &deadlineRecoveryReplicaStore{}
	dispatcher := &batchingRecoveryProbeDispatcher{
		local: 1, ownerContext: context.Background(), localTimeout: 20 * time.Millisecond,
		store: store, executor: executor,
	}
	done := make(chan error, 1)
	if err := dispatcher.submitRecoveryProbe(context.Background(), recoveryProbeQuery{
		ChannelKey: "1:timeout", ChannelID: ch.ChannelID{ID: "timeout", Type: 1},
		Leader: 1, Voter: 1,
	}, func(_ ProbeResult, err error) { done <- err }); err != nil {
		t.Fatalf("submitRecoveryProbe() error = %v", err)
	}
	executor.RunNext()
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("local probe error = %v, want owner deadline", err)
	}
	if !store.sawDeadline {
		t.Fatal("local ReplicaStore.Load did not receive a finite owner deadline")
	}
}

type scriptedRecoveryProbeDispatcher struct {
	results    map[ch.NodeID]ProbeResult
	indexes    map[ch.NodeID][][]uint64
	failPage   map[ch.NodeID]bool
	failPageAt map[ch.NodeID]int
	pageCalls  map[ch.NodeID]int
}

type generatedRecoveryProbeDispatcher struct {
	last         uint64
	rounds       int
	calls        int
	cancel       context.CancelFunc
	cancelRound  int
	pageStarts   []uint64
	sawDeadline  bool
	failVoters   map[ch.NodeID]bool
	callsByVoter map[ch.NodeID]int
	pending      []func(error)
}

func (d *generatedRecoveryProbeDispatcher) submitRecoveryProbe(ctx context.Context, query recoveryProbeQuery, complete func(ProbeResult, error)) error {
	_, d.sawDeadline = ctx.Deadline()
	d.calls++
	if d.callsByVoter == nil {
		d.callsByVoter = make(map[ch.NodeID]int)
	}
	d.callsByVoter[query.Voter]++
	if query.Voter == 1 {
		d.rounds++
		if len(query.Indexes) > 0 {
			d.pageStarts = append(d.pageStarts, query.Indexes[0])
		}
	}
	if d.cancelRound > 0 && d.rounds == d.cancelRound {
		d.pending = append(d.pending, func(err error) { complete(ProbeResult{}, err) })
		if query.Voter == 3 {
			d.cancel()
		}
		return nil
	}
	if d.failVoters[query.Voter] {
		complete(ProbeResult{}, errPeerOutcomeUnknown)
		return nil
	}
	tail := generatedRecoveryIdentity(d.last)
	result := ProbeResult{State: recoveryReport(query.Voter, d.last, 1, []EntryProbe{{Index: d.last, Present: true, Identity: tail}}).Result.State}
	result.Entries = make([]EntryProbe, len(query.Indexes))
	for index, requested := range query.Indexes {
		result.Entries[index] = EntryProbe{Index: requested, Present: true, Identity: generatedRecoveryIdentity(requested)}
	}
	complete(result, nil)
	return nil
}

func (d *generatedRecoveryProbeDispatcher) completePending(err error) {
	for _, complete := range d.pending {
		complete(err)
	}
	d.pending = nil
}

func generatedRecoveryIdentity(index uint64) ch.EntryIdentity {
	identity := ch.EntryIdentity{
		Version: ch.ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		Index: index, CommandID: ch.CommandID{0: 1}, Digest: ch.EntryDigest{0: 1},
	}
	for shift := 0; shift < 8; shift++ {
		identity.CommandID[shift+1] = byte(index >> (shift * 8))
		identity.Digest[shift+1] = byte(index >> (shift * 8))
	}
	if index > 1 {
		identity.PreviousTerm = 5
		identity.PreviousIndex = index - 1
		identity.PreviousDigest = generatedRecoveryDigest(index - 1)
	}
	return identity
}

func generatedRecoveryDigest(index uint64) ch.EntryDigest {
	digest := ch.EntryDigest{0: 1}
	for shift := 0; shift < 8; shift++ {
		digest[shift+1] = byte(index >> (shift * 8))
	}
	return digest
}

type deadlineRecoveryReplicaStore struct {
	sawDeadline bool
}

func (s *deadlineRecoveryReplicaStore) Load(ctx context.Context, _ LoadBatch) (LoadBatchResult, error) {
	_, s.sawDeadline = ctx.Deadline()
	return LoadBatchResult{}, context.DeadlineExceeded
}

func (*deadlineRecoveryReplicaStore) Sync(context.Context, []Mutation) []MutationResult { return nil }

func makeRecoveryChain(last uint64) []EntryProbe {
	identities := make([]ch.EntryIdentity, last)
	for index := uint64(1); index <= last; index++ {
		identity := ch.EntryIdentity{
			Version: ch.ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
			Index: index, CommandID: ch.CommandID{0: byte(index>>8) + 1, 1: byte(index)},
			Digest: ch.EntryDigest{0: byte(index>>8) + 1, 1: byte(index)},
		}
		if index > 1 {
			previous := identities[index-2]
			identity.PreviousTerm = previous.LeaderTerm
			identity.PreviousIndex = previous.Index
			identity.PreviousDigest = previous.Digest
		}
		identities[index-1] = identity
	}
	entries := make([]EntryProbe, 0, last)
	for index := last; index > 0; index-- {
		entries = append(entries, EntryProbe{Index: index, Present: true, Identity: identities[index-1]})
	}
	return entries
}

func (d *scriptedRecoveryProbeDispatcher) submitRecoveryProbe(_ context.Context, query recoveryProbeQuery, complete func(ProbeResult, error)) error {
	if d.indexes == nil {
		d.indexes = make(map[ch.NodeID][][]uint64)
	}
	d.indexes[query.Voter] = append(d.indexes[query.Voter], append([]uint64(nil), query.Indexes...))
	result := d.results[query.Voter]
	if len(query.Indexes) == 0 {
		result.Entries = nil
		complete(result, nil)
		return nil
	}
	if d.pageCalls == nil {
		d.pageCalls = make(map[ch.NodeID]int)
	}
	d.pageCalls[query.Voter]++
	if d.failPage[query.Voter] || d.failPageAt[query.Voter] > 0 && d.pageCalls[query.Voter] >= d.failPageAt[query.Voter] {
		complete(ProbeResult{}, errPeerOutcomeUnknown)
		return nil
	}
	byIndex := make(map[uint64]EntryProbe, len(result.Entries))
	for _, entry := range result.Entries {
		byIndex[entry.Index] = entry
	}
	result.Entries = make([]EntryProbe, len(query.Indexes))
	for index, requested := range query.Indexes {
		entry, ok := byIndex[requested]
		if !ok {
			entry = EntryProbe{Index: requested}
		}
		result.Entries[index] = entry
	}
	complete(result, nil)
	return nil
}
