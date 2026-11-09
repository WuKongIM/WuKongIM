package message

import (
	"context"
	"errors"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// The authority promise replaces the in-log current-term barrier: the
// distributed log sequence is message_seq, so the fence lives in the Channel's
// system space. Failure cases this storage contract must prevent
// (docs/superpowers/specs/2026-11-09-log-sequence-is-message-seq-design.md):
//   - a lower authority overwriting a higher promise;
//   - a promise lost across reopen (it must be durable before success);
//   - a stale writer's append accepted after a newer promise;
//   - a legitimate repair (old-authority entries sent by the current writer)
//     rejected because of the entries' own authority;
//   - any promise state consuming a message sequence.
func TestAuthorityPromiseIsDurableMonotonicAndOutsideTheLog(t *testing.T) {
	dir := t.TempDir()
	eng, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	id := channel.ChannelID{ID: "promise", Type: 2}
	store := mustForChannel(t, eng, "promise:2", id)

	if _, ok, err := store.AuthorityPromise(context.Background()); err != nil || ok {
		t.Fatalf("fresh promise = ok %v err %v, want absent", ok, err)
	}
	b := AuthorityPromise{ChannelEpoch: 1, LeaderTerm: 4, FenceVersion: 2}
	if err := store.PromiseAuthority(context.Background(), b); err != nil {
		t.Fatalf("PromiseAuthority(b): %v", err)
	}
	if err := store.PromiseAuthority(context.Background(), b); err != nil {
		t.Fatalf("PromiseAuthority(b) again must be idempotent: %v", err)
	}
	for _, lower := range []AuthorityPromise{
		{ChannelEpoch: 1, LeaderTerm: 3, FenceVersion: 9},
		{ChannelEpoch: 1, LeaderTerm: 4, FenceVersion: 1},
		{ChannelEpoch: 0, LeaderTerm: 9, FenceVersion: 9},
	} {
		if err := store.PromiseAuthority(context.Background(), lower); !errors.Is(err, channel.ErrStaleAuthority) {
			t.Fatalf("PromiseAuthority(%+v) error = %v, want stale meta", lower, err)
		}
	}
	if store.LEO() != 0 {
		t.Fatalf("a promise consumed a log sequence: LEO = %d", store.LEO())
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Engine.Close(): %v", err)
	}

	reopened, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	store = mustForChannel(t, reopened, "promise:2", id)
	defer store.Close()
	got, ok, err := store.AuthorityPromise(context.Background())
	if err != nil || !ok || got != b {
		t.Fatalf("reopened promise = %+v ok %v err %v, want %+v", got, ok, err, b)
	}
	higher := AuthorityPromise{ChannelEpoch: 2, LeaderTerm: 1, FenceVersion: 1}
	if err := store.PromiseAuthority(context.Background(), higher); err != nil {
		t.Fatalf("PromiseAuthority(higher): %v", err)
	}
	if got, _, _ := store.AuthorityPromise(context.Background()); got != higher {
		t.Fatalf("promise = %+v, want %+v", got, higher)
	}
}

func TestExactAppendFencesTheWriterAuthorityAgainstThePromise(t *testing.T) {
	eng := openCompatEngine(t)
	store := mustForChannel(t, eng, "promise-fence:1", channel.ChannelID{ID: "promise-fence", Type: 1})
	defer store.Close()

	oldTerm := AuthorityPromise{ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9}
	newTerm := AuthorityPromise{ChannelEpoch: 5, LeaderTerm: 8, FenceVersion: 9}
	// Entry 1 was written under the old authority before the leader change.
	record1 := compatExactTestRecord(t, 5, 9_001, "promise-fence", "c1")
	entry1 := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: [32]byte{1}, BaseOffset: 0, LastOffset: 1,
	}, []channel.Record{record1})

	if err := store.PromiseAuthority(context.Background(), newTerm); err != nil {
		t.Fatalf("PromiseAuthority(new): %v", err)
	}
	// A stale old leader writing entry 1 as its own new proposal is fenced and
	// writes nothing.
	stale := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store: store, Records: []channel.Record{record1}, ExactBaseOffset: true,
		ExpectedBaseOffset: 0, Committed: 1, Proposal: entry1, WriterAuthority: &oldTerm,
	}})
	if !errors.Is(stale[0].Err, channel.ErrStaleAuthority) || stale[0].Outcome != quorumlog.AppendOutcomeDefinitelyNotWritten {
		t.Fatalf("stale writer result = %+v, want definitely-not-written stale meta", stale[0])
	}
	if store.LEO() != 0 {
		t.Fatalf("fenced append wrote rows: LEO = %d", store.LEO())
	}
	// The current leader repairing the same old-authority entry is accepted:
	// the fence binds the writer, not the copied entry's original authority.
	repair := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store: store, Records: []channel.Record{record1}, ExactBaseOffset: true,
		ExpectedBaseOffset: 0, Committed: 1, Proposal: entry1, WriterAuthority: &newTerm,
	}})
	if repair[0].Err != nil || repair[0].Outcome != quorumlog.AppendOutcomeDurable || store.LEO() != 1 {
		t.Fatalf("repair by the current writer = %+v LEO %d", repair[0], store.LEO())
	}

	// A higher writer that this replica never saw a promise for raises the
	// promise atomically with its accepted append (a voter that missed the
	// promising probe catches up through the first new-authority write).
	nextTerm := AuthorityPromise{ChannelEpoch: 5, LeaderTerm: 9, FenceVersion: 9}
	record2 := compatExactTestRecord(t, 5, 9_002, "promise-fence", "c2")
	entry2 := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 9, FenceVersion: 9,
		CommandID: [32]byte{2}, BaseOffset: 1, LastOffset: 2,
		PreviousTerm: entry1.LeaderTerm, PreviousIndex: entry1.LastOffset, PreviousDigest: entry1.Digest,
	}, []channel.Record{record2})
	raised := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store: store, Records: []channel.Record{record2}, ExactBaseOffset: true,
		ExpectedBaseOffset: 1, Committed: 2, Proposal: entry2, WriterAuthority: &nextTerm,
	}})
	if raised[0].Err != nil || store.LEO() != 2 {
		t.Fatalf("higher writer append = %+v LEO %d", raised[0], store.LEO())
	}
	if got, ok, err := store.AuthorityPromise(context.Background()); err != nil || !ok || got != nextTerm {
		t.Fatalf("promise after higher writer = %+v ok %v err %v, want %+v", got, ok, err, nextTerm)
	}
	if err := store.PromiseAuthority(context.Background(), newTerm); !errors.Is(err, channel.ErrStaleAuthority) {
		t.Fatalf("lower promise after a raised writer error = %v, want stale meta", err)
	}

	// Without a writer authority (barrier mode, or a stale leader that has not
	// observed Controller activation) the entry's own authority is checked
	// against the promise but never raises it: otherwise a stale barrier-mode
	// leader could still reach a quorum through promised voters.
	record3 := compatExactTestRecord(t, 5, 9_003, "promise-fence", "c3")
	entry3 := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: [32]byte{3}, BaseOffset: 2, LastOffset: 3,
		PreviousTerm: entry2.LeaderTerm, PreviousIndex: entry2.LastOffset, PreviousDigest: entry2.Digest,
	}, []channel.Record{record3})
	legacyStale := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store: store, Records: []channel.Record{record3}, ExactBaseOffset: true,
		ExpectedBaseOffset: 2, Committed: 3, Proposal: entry3,
	}})
	if !errors.Is(legacyStale[0].Err, channel.ErrStaleAuthority) || legacyStale[0].Outcome != quorumlog.AppendOutcomeDefinitelyNotWritten || store.LEO() != 2 {
		t.Fatalf("unfenced append below the promise = %+v LEO %d, want stale meta", legacyStale[0], store.LEO())
	}
	higherTerm := AuthorityPromise{ChannelEpoch: 5, LeaderTerm: 10, FenceVersion: 9}
	record4 := compatExactTestRecord(t, 5, 9_004, "promise-fence", "c4")
	entry4 := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 10, FenceVersion: 9,
		CommandID: [32]byte{4}, BaseOffset: 2, LastOffset: 3,
		PreviousTerm: entry2.LeaderTerm, PreviousIndex: entry2.LastOffset, PreviousDigest: entry2.Digest,
	}, []channel.Record{record4})
	legacy := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store: store, Records: []channel.Record{record4}, ExactBaseOffset: true,
		ExpectedBaseOffset: 2, Committed: 3, Proposal: entry4,
	}})
	if legacy[0].Err != nil || store.LEO() != 3 {
		t.Fatalf("unfenced append at or above the promise = %+v LEO %d", legacy[0], store.LEO())
	}
	if got, _, _ := store.AuthorityPromise(context.Background()); got != nextTerm || got == higherTerm {
		t.Fatalf("unfenced append changed the promise to %+v", got)
	}
}

// Before Controller activation no promise exists, and unfenced appends must
// never create one: older binaries must be able to roll back over this data.
func TestUnfencedAppendNeverCreatesAPromise(t *testing.T) {
	eng := openCompatEngine(t)
	store := mustForChannel(t, eng, "promise-absent:1", channel.ChannelID{ID: "promise-absent", Type: 1})
	defer store.Close()
	record := compatExactTestRecord(t, 5, 9_101, "promise-absent", "c1")
	entry := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: [32]byte{1}, BaseOffset: 0, LastOffset: 1,
	}, []channel.Record{record})
	result := StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store: store, Records: []channel.Record{record}, ExactBaseOffset: true,
		ExpectedBaseOffset: 0, Committed: 1, Proposal: entry,
	}})
	if result[0].Err != nil || store.LEO() != 1 {
		t.Fatalf("unfenced append = %+v LEO %d", result[0], store.LEO())
	}
	if _, ok, err := store.AuthorityPromise(context.Background()); err != nil || ok {
		t.Fatalf("unfenced append created a promise: ok %v err %v", ok, err)
	}
}
