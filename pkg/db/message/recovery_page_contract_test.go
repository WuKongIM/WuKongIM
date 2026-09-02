package message

import (
	"context"
	"errors"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func TestDurableRecoveryPageReturnsOnlyCompleteVerifiedProposals(t *testing.T) {
	engine := openCompatEngine(t)
	store, firstManifest, secondManifest := appendRecoveryPageFixture(t, engine)
	defer store.Close()

	page, err := store.ReadDurableRecoveryPage(context.Background(), DurableRecoveryPageRequest{
		From: 1, Through: 3, MaxBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("ReadDurableRecoveryPage() error = %v", err)
	}
	if page.LEO != 3 || page.Committed != 3 || page.Manifest.CommandID != secondManifest.CommandID ||
		len(page.Records) != 3 || len(page.Entries) != 3 {
		t.Fatalf("recovery page = %#v", page)
	}
	for index := range page.Records {
		wantIndex := uint64(index + 1)
		if page.Records[index].Index != wantIndex || page.Records[index].Epoch != 3 ||
			!page.Entries[index].Present || page.Entries[index].Index != wantIndex || page.Entries[index].Identity.Index != wantIndex {
			t.Fatalf("page item[%d] = record %#v entry %#v", index, page.Records[index], page.Entries[index])
		}
	}
	if page.Entries[0].Identity.CommandID != firstManifest.CommandID || page.Entries[1].Identity.CommandID != firstManifest.CommandID ||
		page.Entries[2].Identity.CommandID != secondManifest.CommandID {
		t.Fatalf("proposal identity grouping = %#v", page.Entries)
	}
}

func TestDurableRecoveryPageRefusesPartialOrUnprovenRanges(t *testing.T) {
	engine := openCompatEngine(t)
	store, _, _ := appendRecoveryPageFixture(t, engine)
	defer store.Close()
	tests := []struct {
		name string
		req  DurableRecoveryPageRequest
		want error
	}{
		{name: "split first proposal", req: DurableRecoveryPageRequest{From: 1, Through: 1, MaxBytes: 1 << 20}, want: channel.ErrBackpressured},
		{name: "start inside proposal", req: DurableRecoveryPageRequest{From: 2, Through: 3, MaxBytes: 1 << 20}, want: channel.ErrCorruptState},
		{name: "past durable frontier", req: DurableRecoveryPageRequest{From: 1, Through: 4, MaxBytes: 1 << 20}, want: channel.ErrCorruptState},
		{name: "proposal exceeds byte budget", req: DurableRecoveryPageRequest{From: 1, Through: 3, MaxBytes: 1}, want: channel.ErrBackpressured},
		{name: "zero start", req: DurableRecoveryPageRequest{From: 0, Through: 1, MaxBytes: 1 << 20}, want: channel.ErrInvalidArgument},
		{name: "reversed range", req: DurableRecoveryPageRequest{From: 3, Through: 2, MaxBytes: 1 << 20}, want: channel.ErrInvalidArgument},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := store.ReadDurableRecoveryPage(context.Background(), test.req); !errors.Is(err, test.want) {
				t.Fatalf("ReadDurableRecoveryPage(%#v) error = %v, want %v", test.req, err, test.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.ReadDurableRecoveryPage(ctx, DurableRecoveryPageRequest{From: 1, Through: 3, MaxBytes: 1 << 20}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadDurableRecoveryPage(canceled) error = %v", err)
	}
}

func TestLoadDurableProposalReturnsExactRowsWithinCallerBounds(t *testing.T) {
	engine := openCompatEngine(t)
	store, firstManifest, _ := appendRecoveryPageFixture(t, engine)
	defer store.Close()
	proposal, present, err := store.LoadDurableProposal(context.Background(), firstManifest.CommandID, 2, 1<<20)
	if err != nil || !present {
		t.Fatalf("LoadDurableProposal() = present %v, error %v", present, err)
	}
	if proposal.Manifest != firstManifest || len(proposal.Records) != 2 || proposal.Records[0].Index != 1 ||
		proposal.Records[1].Index != 2 || proposal.Records[0].Epoch != 3 || proposal.Records[1].Epoch != 3 {
		t.Fatalf("durable proposal = %#v", proposal)
	}
	if _, present, err := store.LoadDurableProposal(context.Background(), quorumlog.CommandID{31: 99}, 2, 1<<20); err != nil || present {
		t.Fatalf("LoadDurableProposal(missing) = present %v, error %v", present, err)
	}
	if _, _, err := store.LoadDurableProposal(context.Background(), firstManifest.CommandID, 1, 1<<20); !errors.Is(err, channel.ErrBackpressured) {
		t.Fatalf("LoadDurableProposal(record bound) error = %v", err)
	}
	if _, _, err := store.LoadDurableProposal(context.Background(), firstManifest.CommandID, 2, 1); !errors.Is(err, channel.ErrBackpressured) {
		t.Fatalf("LoadDurableProposal(byte bound) error = %v", err)
	}
	if _, _, err := store.LoadDurableProposal(nil, firstManifest.CommandID, 2, 1<<20); !errors.Is(err, channel.ErrInvalidArgument) {
		t.Fatalf("LoadDurableProposal(nil context) error = %v", err)
	}
}

func appendRecoveryPageFixture(t *testing.T, engine *Engine) (*ChannelStore, DurableProposalManifest, DurableProposalManifest) {
	t.Helper()
	id := channel.ChannelID{ID: "recovery-page", Type: 1}
	store := mustForChannel(t, engine, "recovery-page", id)
	firstRecords := []channel.Record{
		compatExactTestRecord(t, 3, 9101, id.ID, "recovery-page-1"),
		compatExactTestRecord(t, 3, 9102, id.ID, "recovery-page-2"),
	}
	first := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: quorumlog.CommandID{31: 1}, BaseOffset: 0, LastOffset: 2,
	}, firstRecords)
	secondRecords := []channel.Record{compatExactTestRecord(t, 3, 9103, id.ID, "recovery-page-3")}
	second := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: quorumlog.CommandID{31: 2}, BaseOffset: 2, LastOffset: 3,
		PreviousTerm: first.LeaderTerm, PreviousIndex: first.LastOffset, PreviousDigest: first.Digest,
	}, secondRecords)
	for index, item := range []AppendBatchItem{
		{Store: store, Records: firstRecords, ExactBaseOffset: true, ExpectedBaseOffset: 0, Proposal: first},
		{Store: store, Records: secondRecords, ExactBaseOffset: true, ExpectedBaseOffset: 2, Proposal: second},
	} {
		results := StoreAppendBatch(context.Background(), []AppendBatchItem{item})
		if len(results) != 1 || results[0].Err != nil || results[0].Outcome != quorumlog.AppendOutcomeDurable {
			store.Close()
			t.Fatalf("StoreAppendBatch()[%d] = %#v", index, results)
		}
	}
	if err := store.StoreCheckpoint(channel.Checkpoint{Epoch: 3, HW: 3}); err != nil {
		store.Close()
		t.Fatalf("StoreCheckpoint() error = %v", err)
	}
	return store, first, second
}
