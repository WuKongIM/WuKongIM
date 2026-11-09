package replication

import (
	"context"
	"errors"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
)

// After Controller activation the distributed log sequence is message_seq:
// a leader change must not write the in-log authority barrier. The durable
// authority promise replaces it (docs/superpowers/specs/
// 2026-11-09-log-sequence-is-message-seq-design.md). Failure cases:
//   - a leader change consumes a log index (the next message is not N+1);
//   - a stale old leader still reaches a write quorum after the new leader's
//     promise is durable on a quorum, with or without an explicit writer;
//   - an install proceeds although a voter already holds a higher promise;
//   - a voter that missed the promising probe is never fenced;
//   - the current leader cannot repair an old-authority entry on a promised voter;
//   - the wire leaks the new fields to peers before activation (legacy kinds
//     must stay byte-identical when no promise or writer is set);
//   - a stale promise reply fails sibling items or is mistaken for a tail.

func promiseTestLog(t *testing.T, harness *replicaHarness, local ch.NodeID, active bool) *quorumLog {
	t.Helper()
	log, err := newQuorumLog(quorumLogConfig{
		Local: local, Store: harness.stores[local], Recovery: harness, Durability: harness.as(local),
		RecoveryTimeout: time.Minute, RecoveryPageBytes: 64 << 10,
		MaxChannels: 8, MaxVoters: 3, MaxProposalRecords: 256, MaxProposalBytes: 64 << 10, MaxRetainedCommands: 16,
		PromiseActive: func() bool { return active },
	})
	if err != nil {
		t.Fatalf("newQuorumLog() error = %v", err)
	}
	return log
}

func promiseTestProposal(authority Authority, marker byte) Proposal {
	payload := []byte{marker}
	return Proposal{
		Key: authority.Key, Expected: authority.ID, CommandID: ch.CommandID{31: marker},
		Records: []ch.Record{{
			ID: uint64(marker), Epoch: authority.ID.ChannelEpoch, FromUID: "sender", ClientMsgNo: string(rune('a' + marker)),
			Payload: payload, SizeBytes: len(payload), ServerTimestampMS: int64(marker),
		}},
	}
}

func promiseOf(t *testing.T, store ReplicaStore, key ch.ChannelKey, id ch.ChannelID) (channelstore.AuthorityFence, bool) {
	t.Helper()
	adapter := store.(*storeAdapter)
	channel, err := adapter.cfg.Factory.ChannelStore(key, id)
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	promise, ok, err := channel.(channelstore.AuthorityPromiser).AuthorityPromise(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return promise, ok
}

func fenceOf(id AuthorityID) channelstore.AuthorityFence {
	return channelstore.AuthorityFence{ChannelEpoch: id.ChannelEpoch, LeaderTerm: id.LeaderTerm, FenceVersion: id.FenceVersion}
}

func promiseTestAuthority(name string, leader ch.NodeID) Authority {
	return Authority{
		Key: ch.ChannelKey("1:" + name), ChannelID: ch.ChannelID{ID: name, Type: 1},
		ID:     AuthorityID{ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7},
		Leader: leader, Voters: []ch.NodeID{1, 2, 3}, WriteQuorum: 2,
	}
}

func TestPromiseInstallConsumesNoLogIndexAcrossLeaderChange(t *testing.T) {
	harness := newReplicaHarness(t, 1, 2, 3)
	authority := promiseTestAuthority("promise-no-index", 1)
	if _, err := promiseTestLog(t, harness, 1, true).Install(context.Background(), authority); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	first := promiseTestLog(t, harness, 1, true)
	if _, err := first.Install(context.Background(), authority); err != nil {
		t.Fatal(err)
	}
	if receipt, err := first.Commit(context.Background(), promiseTestProposal(authority, 1)); err != nil || receipt.First != 1 {
		t.Fatalf("Commit(first) = %+v, %v", receipt, err)
	}

	next := authority
	next.ID.LeaderTerm++
	before := harness.syncCalls
	log := promiseTestLog(t, harness, 1, true)
	installed, err := log.Install(context.Background(), next)
	if err != nil {
		t.Fatalf("Install(new authority) error = %v", err)
	}
	if installed.LEO != 1 || installed.HW != 1 || harness.syncCalls != before {
		t.Fatalf("Install(new authority) = %+v with %d writes, want frontier 1 and no log write", installed, harness.syncCalls-before)
	}
	for voter, store := range harness.stores {
		if promise, ok := promiseOf(t, store, next.Key, next.ChannelID); !ok || promise != fenceOf(next.ID) {
			t.Fatalf("voter %d promise = %+v/%v, want %+v", voter, promise, ok, next.ID)
		}
	}
	receipt, err := log.Commit(context.Background(), promiseTestProposal(next, 2))
	if err != nil || receipt.First != 2 || receipt.Last != 2 {
		t.Fatalf("Commit(after leader change) = %+v, %v; want message_seq 2", receipt, err)
	}
	// Reinstalling the same authority is idempotent and still writes nothing.
	before = harness.syncCalls
	if installed, err := promiseTestLog(t, harness, 1, true).Install(context.Background(), next); err != nil || installed.LEO != 2 || harness.syncCalls != before {
		t.Fatalf("reinstall = %+v, %v with %d writes", installed, err, harness.syncCalls-before)
	}
}

func TestBarrierModeStillWritesBarrierBeforeActivation(t *testing.T) {
	harness := newReplicaHarness(t, 1, 2, 3)
	authority := promiseTestAuthority("promise-inactive", 1)
	log := promiseTestLog(t, harness, 1, false)
	if _, err := log.Install(context.Background(), authority); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Commit(context.Background(), promiseTestProposal(authority, 1)); err != nil {
		t.Fatal(err)
	}
	next := authority
	next.ID.LeaderTerm++
	installed, err := promiseTestLog(t, harness, 1, false).Install(context.Background(), next)
	if err != nil || installed.LEO != 2 {
		t.Fatalf("inactive Install = %+v, %v; want barrier at 2", installed, err)
	}
	for voter, store := range harness.stores {
		if _, ok := promiseOf(t, store, next.Key, next.ChannelID); ok {
			t.Fatalf("voter %d holds a promise before activation", voter)
		}
	}
}

func TestPromiseInstallFailsClosedOnHigherPromise(t *testing.T) {
	harness := newReplicaHarness(t, 1, 2, 3)
	authority := promiseTestAuthority("promise-higher", 1)
	higher := authority.ID
	higher.LeaderTerm += 10
	for _, voter := range []ch.NodeID{2, 3} {
		channel, err := harness.stores[voter].(*storeAdapter).cfg.Factory.ChannelStore(authority.Key, authority.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		if err := channel.(channelstore.AuthorityPromiser).PromiseAuthority(context.Background(), fenceOf(higher)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := promiseTestLog(t, harness, 1, true).Install(context.Background(), authority); !errors.Is(err, ch.ErrStaleMeta) {
		t.Fatalf("Install(below a promise) error = %v, want ErrStaleMeta", err)
	}
}

func TestStaleLeaderCannotCommitAfterPromiseQuorum(t *testing.T) {
	for _, staleActive := range []bool{false, true} {
		harness := newReplicaHarness(t, 1, 2, 3)
		old := promiseTestAuthority("promise-stale", 1)
		stale := promiseTestLog(t, harness, 1, staleActive)
		if _, err := stale.Install(context.Background(), old); err != nil {
			t.Fatal(err)
		}
		if _, err := stale.Commit(context.Background(), promiseTestProposal(old, 1)); err != nil {
			t.Fatal(err)
		}
		next := old
		next.ID.LeaderTerm++
		next.Leader = 2
		if installed, err := promiseTestLog(t, harness, 2, true).Install(context.Background(), next); err != nil || installed.LEO != 1 {
			t.Fatalf("Install(new leader) = %+v, %v", installed, err)
		}
		if receipt, err := stale.Commit(context.Background(), promiseTestProposal(old, 2)); err == nil {
			t.Fatalf("stale leader (promise mode %v) committed %+v after the new promise quorum", staleActive, receipt)
		}
		for voter, store := range harness.stores {
			loaded, err := store.Load(context.Background(), LoadBatch{Items: []LoadRequest{{ChannelKey: old.Key, ChannelID: old.ChannelID}}})
			if err != nil || loaded.Items[0].State.LEO != 1 {
				t.Fatalf("voter %d frontier = %+v, %v; stale write must not land", voter, loaded, err)
			}
		}
	}
}

func TestMissedVoterIsPromisedByFirstFencedWrite(t *testing.T) {
	harness := newReplicaHarness(t, 1, 2, 3)
	authority := promiseTestAuthority("promise-missed", 1)
	log := promiseTestLog(t, harness, 1, true)
	if _, err := log.Install(context.Background(), authority); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Commit(context.Background(), promiseTestProposal(authority, 1)); err != nil {
		t.Fatal(err)
	}
	next := authority
	next.ID.LeaderTerm++
	harness.probeDown = map[ch.NodeID]bool{3: true}
	log = promiseTestLog(t, harness, 1, true)
	if _, err := log.Install(context.Background(), next); err != nil {
		t.Fatalf("Install(with one voter missing) error = %v", err)
	}
	if promise, _ := promiseOf(t, harness.stores[3], next.Key, next.ChannelID); promise == fenceOf(next.ID) {
		t.Fatal("unreachable voter received the promise")
	}
	if _, err := log.Commit(context.Background(), promiseTestProposal(next, 2)); err != nil {
		t.Fatal(err)
	}
	if promise, ok := promiseOf(t, harness.stores[3], next.Key, next.ChannelID); !ok || promise != fenceOf(next.ID) {
		t.Fatalf("missed voter promise after first write = %+v/%v, want %+v", promise, ok, next.ID)
	}
}

func TestExchangeServerPromisesBeforeReportingTail(t *testing.T) {
	store, err := NewStoreAdapter(StoreAdapterConfig{Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 8, MaxBatchBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewExchangeServer(ExchangeServerConfig{LocalNode: 2, Store: store, MaxBatchItems: 8, MaxBatchBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	x := promiseTestAuthority("promise-server-x", 1)
	y := promiseTestAuthority("promise-server-y", 1)
	probe := func(authority Authority, promise AuthorityID) ExchangeItem {
		return ExchangeItem{Kind: ExchangeProbe, Probe: &ProbeRequest{
			ChannelKey: authority.Key, ChannelID: authority.ChannelID, Leader: 1, Follower: 2, Promise: promise,
		}}
	}
	first := probe(x, x.ID)
	first.RequestID = 1
	result, err := server.Handle(context.Background(), 1, ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{first}})
	if err != nil || result.Items[0].Replicate != (ReplicateResult{}) || !sameProbeProof(result.Items[0].Probe.Proof, probeProofFor(*first.Probe)) {
		t.Fatalf("promise probe = %+v, %v", result, err)
	}
	if promise, ok := promiseOf(t, store, x.Key, x.ChannelID); !ok || promise != fenceOf(x.ID) {
		t.Fatalf("promise after probe = %+v/%v", promise, ok)
	}
	lower := x.ID
	lower.FenceVersion--
	staleItem, sibling := probe(x, lower), probe(y, AuthorityID{})
	staleItem.RequestID, sibling.RequestID = 1, 2
	result, err = server.Handle(context.Background(), 1, ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{staleItem, sibling}})
	if err != nil {
		t.Fatalf("stale promise failed the batch: %v", err)
	}
	if result.Items[0].Replicate != (ReplicateResult{Status: ReplicateStaleFence}) || !zeroProbeResult(result.Items[0].Probe) {
		t.Fatalf("stale promise reply = %+v, want ReplicateStaleFence and no tail", result.Items[0])
	}
	if !sameProbeProof(result.Items[1].Probe.Proof, probeProofFor(*sibling.Probe)) {
		t.Fatalf("sibling probe = %+v", result.Items[1])
	}
	if !promiseProbeStale(*staleItem.Probe, result.Items[0]) || promiseProbeStale(*sibling.Probe, result.Items[1]) {
		t.Fatal("peer batcher must recognize exactly the stale promise reply")
	}
	if _, ok := promiseOf(t, store, y.Key, y.ChannelID); ok {
		t.Fatal("a plain probe created a promise")
	}
}

func TestExchangeServerFencesReplicateByWriterOrEntryAuthority(t *testing.T) {
	store, err := NewStoreAdapter(StoreAdapterConfig{Factory: channelstore.NewMemoryFactory(), MaxBatchItems: 8, MaxBatchBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewExchangeServer(ExchangeServerConfig{LocalNode: 2, Store: store, MaxBatchItems: 8, MaxBatchBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	key, id := ch.ChannelKey("1:promise-repair"), ch.ChannelID{ID: "promise-repair", Type: 1}
	// recoveryMutationAfter seals entries under authority (3,5,7).
	entry, _ := recoveryMutationAfter(t, key, id, 1, 0, ch.EntryIdentity{})
	promise := AuthorityID{ChannelEpoch: 3, LeaderTerm: 6, FenceVersion: 7}
	channel, err := store.(*storeAdapter).cfg.Factory.ChannelStore(key, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := channel.(channelstore.AuthorityPromiser).PromiseAuthority(context.Background(), fenceOf(promise)); err != nil {
		t.Fatal(err)
	}
	replicate := func(writer AuthorityID) ReplicateResult {
		request := ReplicateRequest{ChannelKey: key, ChannelID: id, Leader: 1, Follower: 2, Manifest: entry.Manifest, Records: entry.Records, Writer: writer}
		result, err := server.Handle(context.Background(), 1, ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{{RequestID: 1, Kind: ExchangeReplicate, Replicate: &request}}})
		if err != nil {
			t.Fatal(err)
		}
		return result.Items[0].Replicate
	}
	if got := replicate(AuthorityID{}); got.Status != ReplicateStaleFence {
		t.Fatalf("unfenced old-authority append = %+v, want stale fence", got)
	}
	if got := replicate(AuthorityID{ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 9}); got.Status != ReplicateStaleFence {
		t.Fatalf("lower writer = %+v, want stale fence", got)
	}
	if got := replicate(promise); !got.Status.Durable() {
		t.Fatalf("current writer repairing an old-authority entry = %+v, want durable", got)
	}
}

func TestExchangeCodecKeepsLegacyKindsForUnfencedItems(t *testing.T) {
	key, id := ch.ChannelKey("1:promise-codec"), ch.ChannelID{ID: "promise-codec", Type: 1}
	entry, _ := recoveryMutationAfter(t, key, id, 1, 0, ch.EntryIdentity{})
	authority := AuthorityID{ChannelEpoch: 3, LeaderTerm: 6, FenceVersion: 7}
	const kindOffset = 4 // version, priority, count, request ID: one byte each.
	for _, tc := range []struct {
		name string
		item ExchangeItem
		wire byte
	}{
		{"replicate", ExchangeItem{RequestID: 1, Kind: ExchangeReplicate, Replicate: &ReplicateRequest{ChannelKey: key, ChannelID: id, Leader: 1, Follower: 2, Manifest: entry.Manifest, Records: entry.Records}}, byte(ExchangeReplicate)},
		{"fenced replicate", ExchangeItem{RequestID: 1, Kind: ExchangeReplicate, Replicate: &ReplicateRequest{ChannelKey: key, ChannelID: id, Leader: 1, Follower: 2, Manifest: entry.Manifest, Records: entry.Records, Writer: authority}}, exchangeWireFencedReplicate},
		{"probe", ExchangeItem{RequestID: 1, Kind: ExchangeProbe, Probe: &ProbeRequest{ChannelKey: key, ChannelID: id, Leader: 1, Follower: 2, Indexes: []uint64{1}}}, byte(ExchangeProbe)},
		{"promise probe", ExchangeItem{RequestID: 1, Kind: ExchangeProbe, Probe: &ProbeRequest{ChannelKey: key, ChannelID: id, Leader: 1, Follower: 2, Indexes: []uint64{1}, Promise: authority}}, exchangeWirePromiseProbe},
	} {
		encoded, err := EncodeExchangeBatch(ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{tc.item}})
		if err != nil {
			t.Fatalf("%s: encode error = %v", tc.name, err)
		}
		if encoded[kindOffset] != tc.wire {
			t.Fatalf("%s: wire kind = %d, want %d", tc.name, encoded[kindOffset], tc.wire)
		}
		decoded, err := DecodeExchangeBatch(encoded)
		if err != nil {
			t.Fatalf("%s: decode error = %v", tc.name, err)
		}
		got := decoded.Items[0]
		if got.Kind != tc.item.Kind {
			t.Fatalf("%s: decoded kind = %d", tc.name, got.Kind)
		}
		if tc.item.Replicate != nil && got.Replicate.Writer != tc.item.Replicate.Writer {
			t.Fatalf("%s: writer = %+v", tc.name, got.Replicate.Writer)
		}
		if tc.item.Probe != nil && got.Probe.Promise != tc.item.Probe.Promise {
			t.Fatalf("%s: promise = %+v", tc.name, got.Probe.Promise)
		}
	}
	funding := ExchangeItem{RequestID: 1, Kind: ExchangeStoragePrepare, FundingNonce: 9, Replicate: &ReplicateRequest{ChannelKey: key, ChannelID: id, Leader: 1, Follower: 2, Manifest: entry.Manifest, Records: entry.Records, Writer: authority}}
	if _, err := EncodeExchangeBatch(ExchangeBatch{Version: ExchangeVersion, Items: []ExchangeItem{funding}}); err == nil {
		t.Fatal("a storage reservation must not carry a writer authority")
	}
}

// A leader failover runs through a write-fenced install on the new leader,
// then a fence-clearing reinstall, while the old leader is unreachable.
func TestPromiseFailoverThroughFencedInstallAndFenceClear(t *testing.T) {
	harness := newReplicaHarness(t, 1, 2, 3)
	old := promiseTestAuthority("promise-failover", 1)
	log1 := promiseTestLog(t, harness, 1, true)
	if _, err := log1.Install(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	for marker := byte(1); marker <= 2; marker++ {
		if _, err := log1.Commit(context.Background(), promiseTestProposal(old, marker)); err != nil {
			t.Fatal(err)
		}
	}
	harness.probeDown = map[ch.NodeID]bool{1: true}
	harness.syncDown = map[ch.NodeID]bool{1: true}
	fenced := old
	fenced.Leader = 2
	fenced.ID.LeaderTerm++
	fenced.ID.FenceVersion++
	fenced.WriteFence = ch.WriteFence{Token: "failover", Version: fenced.ID.FenceVersion, Reason: ch.WriteFenceReasonLeaderTransfer}
	log2 := promiseTestLog(t, harness, 2, true)
	installed, err := log2.Install(context.Background(), fenced)
	if err != nil || installed.LEO != 2 || installed.HW != 2 {
		t.Fatalf("fenced Install = %+v, %v", installed, err)
	}
	cleared := fenced
	cleared.ID.FenceVersion++
	cleared.WriteFence = ch.WriteFence{}
	installed, err = log2.Install(context.Background(), cleared)
	if err != nil || installed.LEO != 2 || installed.HW != 2 {
		t.Fatalf("fence-clearing Install = %+v, %v", installed, err)
	}
	if receipt, err := log2.Commit(context.Background(), promiseTestProposal(cleared, 3)); err != nil || receipt.First != 3 {
		t.Fatalf("Commit after failover = %+v, %v", receipt, err)
	}
}

// A promise-mode install keeps an old-authority tail (no barrier), yet the
// recovered prefix is proven by the promise quorum. Committed replica refresh
// must replay that tail fenced by the current writer, while a barrier-mode
// install over a foreign tail still refuses it.
func TestCommittedRefreshReplaysForeignTailOnlyAfterPromiseInstall(t *testing.T) {
	for _, active := range []bool{true, false} {
		harness := newReplicaHarness(t, 1, 2, 3)
		authority := promiseTestAuthority("promise-refresh", 1)
		first := promiseTestLog(t, harness, 1, active)
		if _, err := first.Install(context.Background(), authority); err != nil {
			t.Fatal(err)
		}
		if _, err := first.Commit(context.Background(), promiseTestProposal(authority, 1)); err != nil {
			t.Fatal(err)
		}
		next := authority
		next.ID.LeaderTerm++
		sink := &committedRefreshSink{}
		log, err := newQuorumLog(quorumLogConfig{
			Local: 1, Store: harness.stores[1], Recovery: harness, Durability: harness, RepairAuthorities: sink,
			RecoveryTimeout: time.Minute, RecoveryPageBytes: 64 << 10, MaxChannels: 8, MaxVoters: 3,
			MaxProposalRecords: 256, MaxProposalBytes: 64 << 10, MaxRetainedCommands: 16,
			PromiseActive: func() bool { return active },
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := log.Install(context.Background(), next); err != nil {
			t.Fatal(err)
		}
		err = log.RequestCommittedReplicaRefresh(context.Background(), next)
		if !active {
			// The barrier tail belongs to the new authority, so refresh is allowed.
			if err != nil {
				t.Fatalf("barrier-mode refresh error = %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("promise-mode refresh over a foreign tail error = %v", err)
		}
		if len(sink.repairs) != 2 {
			t.Fatalf("repairs = %+v, want both followers", sink.repairs)
		}
		for _, repair := range sink.repairs {
			if repair.writer != next.ID || repair.committed != 1 {
				t.Fatalf("repair = %+v, want writer %+v and committed 1", repair, next.ID)
			}
		}
	}
}
