package message

import (
	"context"
	"errors"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func TestStoreAppendBatchCommitsAdjacentProposalChainAndCoalescesStagedReplay(t *testing.T) {
	engine := openCompatEngine(t)
	store := mustForChannel(t, engine, "exact-batch-chain:1", channel.ChannelID{ID: "exact-batch-chain", Type: 1})
	defer store.Close()

	firstRecord := compatExactTestRecord(t, 5, 8_101, "exact-batch-chain", "client-1")
	first := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: [32]byte{1}, BaseOffset: 0, LastOffset: 1,
	}, []channel.Record{firstRecord})
	secondRecord := compatExactTestRecord(t, 5, 8_102, "exact-batch-chain", "client-2")
	second := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 5, LeaderTerm: 7, FenceVersion: 9,
		CommandID: [32]byte{2}, BaseOffset: 1, LastOffset: 2,
		PreviousTerm: first.LeaderTerm, PreviousIndex: first.LastOffset, PreviousDigest: first.Digest,
	}, []channel.Record{secondRecord})

	results := StoreAppendBatch(context.Background(), []AppendBatchItem{
		{
			Store: store, Records: []channel.Record{firstRecord}, ExactBaseOffset: true,
			ExpectedBaseOffset: 0, Committed: 1, Proposal: first,
		},
		{
			Store: store, Records: []channel.Record{secondRecord}, ExactBaseOffset: true,
			ExpectedBaseOffset: 1, Committed: 2, Proposal: second,
		},
		{
			Store: store, Records: []channel.Record{firstRecord}, ExactBaseOffset: true,
			ExpectedBaseOffset: 0, Committed: 1, Proposal: first,
		},
	})
	if len(results) != 3 {
		t.Fatalf("results len = %d, want 3", len(results))
	}
	for index, result := range results {
		if result.Err != nil {
			t.Fatalf("result[%d] = %+v", index, result)
		}
	}
	if results[0].Outcome != quorumlog.AppendOutcomeDurable || results[0].BaseOffset != 0 || results[0].LastOffset != 1 {
		t.Fatalf("first result = %+v", results[0])
	}
	if results[1].Outcome != quorumlog.AppendOutcomeDurable || results[1].BaseOffset != 1 || results[1].LastOffset != 2 {
		t.Fatalf("second result = %+v", results[1])
	}
	if results[2].Outcome != quorumlog.AppendOutcomeAlreadyDurable || results[2].BaseOffset != 0 || results[2].LastOffset != 1 {
		t.Fatalf("staged replay result = %+v", results[2])
	}
	if got := store.LEO(); got != 2 {
		t.Fatalf("LEO = %d, want 2", got)
	}
	checkpoint, err := store.LoadCheckpoint()
	if err != nil {
		t.Fatalf("LoadCheckpoint(): %v", err)
	}
	if checkpoint.HW != 2 {
		t.Fatalf("checkpoint = %+v, want HW 2", checkpoint)
	}
	records, err := store.Read(0, 1<<20)
	if err != nil {
		t.Fatalf("Read(): %v", err)
	}
	if len(records) != 2 || records[0].ID != 8_101 || records[1].ID != 8_102 {
		t.Fatalf("records = %+v", records)
	}
	for _, manifest := range []DurableProposalManifest{first, second} {
		proposal, ok, err := store.LoadDurableProposal(context.Background(), manifest.CommandID, 16, 1<<20)
		if err != nil || !ok || proposal.Manifest != manifest {
			t.Fatalf("LoadDurableProposal(%x) = ok %v err %v proposal %+v", manifest.CommandID[:1], ok, err, proposal)
		}
	}
	recovery, err := store.LoadDurableRecovery(context.Background(), []uint64{1, 2, 3})
	if err != nil {
		t.Fatalf("LoadDurableRecovery(): %v", err)
	}
	if recovery.LEO != 2 || recovery.Committed != 2 || recovery.Manifest != second || recovery.TailIdentity.Index != 2 {
		t.Fatalf("recovery frontier = %+v", recovery.DurableFrontier)
	}
	if len(recovery.Entries) != 3 || !recovery.Entries[0].Present || !recovery.Entries[1].Present || recovery.Entries[2].Present || recovery.Entries[2].Index != 3 {
		t.Fatalf("recovery probes = %+v", recovery.Entries)
	}
	frontier, err := store.LoadDurableFrontier(context.Background())
	if err != nil || frontier != recovery.DurableFrontier {
		t.Fatalf("LoadDurableFrontier() = (%+v, %v), want %+v", frontier, err, recovery.DurableFrontier)
	}
	if _, err := store.LoadDurableRecovery(nil, nil); !errors.Is(err, channel.ErrInvalidArgument) {
		t.Fatalf("nil-context recovery error = %v", err)
	}
	if _, err := store.LoadDurableRecovery(context.Background(), []uint64{0}); !errors.Is(err, channel.ErrInvalidArgument) {
		t.Fatalf("zero-index recovery error = %v", err)
	}
}

func TestExactAppendGapErrorCarriesRecoveryCursorAndCorruptStateClass(t *testing.T) {
	err := &exactAppendGapError{needFrom: 17}
	if err.Error() != "message: exact append gap" || err.needFrom != 17 || !errors.Is(err, channel.ErrCorruptState) {
		t.Fatalf("gap error = %#v (%v)", err, err)
	}
}

func TestStoreAppendBatchRejectsInvalidItemsBeforeDurability(t *testing.T) {
	engine := openCompatEngine(t)
	store := mustForChannel(t, engine, "append-batch-guards:1", channel.ChannelID{ID: "append-batch-guards", Type: 1})
	defer store.Close()
	record := compatExactTestRecord(t, 3, 8_201, "append-batch-guards", "client-1")

	if got := StoreAppendBatch(context.Background(), nil); len(got) != 0 {
		t.Fatalf("empty batch = %+v", got)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	result := StoreAppendBatch(canceled, []AppendBatchItem{{Store: store, Records: []channel.Record{record}}})
	if len(result) != 1 || !errors.Is(result[0].Err, context.Canceled) || result[0].Outcome != quorumlog.AppendOutcomeDefinitelyNotWritten {
		t.Fatalf("canceled batch = %+v", result)
	}
	result = StoreAppendBatch(context.Background(), []AppendBatchItem{{Store: nil, Records: []channel.Record{record}}})
	if len(result) != 1 || !errors.Is(result[0].Err, channel.ErrInvalidArgument) || result[0].Outcome != quorumlog.AppendOutcomeDefinitelyNotWritten {
		t.Fatalf("nil-store batch = %+v", result)
	}
	result = StoreAppendBatch(context.Background(), []AppendBatchItem{{Store: store, Records: []channel.Record{record}, Class: AppendBatchClass(255)}})
	if len(result) != 1 || !errors.Is(result[0].Err, channel.ErrInvalidArgument) {
		t.Fatalf("invalid-class batch = %+v", result)
	}
	result = StoreAppendBatch(context.Background(), []AppendBatchItem{
		{Store: store, Records: []channel.Record{record}},
		{Store: store, Records: []channel.Record{record}},
	})
	if len(result) != 2 || !errors.Is(result[0].Err, channel.ErrInvalidArgument) || !errors.Is(result[1].Err, channel.ErrInvalidArgument) {
		t.Fatalf("duplicate non-exact batch = %+v", result)
	}
	result = StoreAppendBatch(context.Background(), []AppendBatchItem{{Store: store, Records: []channel.Record{record}, Committed: 1}})
	if len(result) != 1 || !errors.Is(result[0].Err, channel.ErrInvalidArgument) || result[0].Outcome != quorumlog.AppendOutcomeDefinitelyNotWritten {
		t.Fatalf("non-exact committed batch = %+v", result)
	}

	gapped := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 4, FenceVersion: 5,
		CommandID: [32]byte{9}, BaseOffset: 2, LastOffset: 3,
		PreviousTerm: 4, PreviousIndex: 2, PreviousDigest: [32]byte{8},
	}, []channel.Record{record})
	result = StoreAppendBatch(context.Background(), []AppendBatchItem{{
		Store: store, Records: []channel.Record{record}, ExactBaseOffset: true,
		ExpectedBaseOffset: 2, Proposal: gapped,
	}})
	if len(result) != 1 || !errors.Is(result[0].Err, channel.ErrCorruptState) || result[0].NeedFrom != 1 || result[0].Outcome != quorumlog.AppendOutcomeConflict {
		t.Fatalf("gapped exact batch = %+v", result)
	}
	if got := store.LEO(); got != 0 {
		t.Fatalf("LEO after rejected batches = %d, want 0", got)
	}
}
