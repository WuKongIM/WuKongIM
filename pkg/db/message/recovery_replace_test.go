package message

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func TestReplaceRecoverySuffixRejectsCorruptRetainedProposalTail(t *testing.T) {
	engine, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	const channelKey = channel.ChannelKey("recovery-boundary")
	store, err := engine.ForChannel(channelKey, channel.ChannelID{ID: "recovery-boundary", Type: 1})
	if err != nil {
		t.Fatalf("ForChannel(): %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	first, firstTail := recoveryProposalFixture(t, 1, 0, quorumlog.EntryIdentity{})
	second, _ := recoveryProposalFixture(t, 2, 1, firstTail)
	for _, proposal := range []RecoveryProposal{first, second} {
		results := StoreAppendBatch(context.Background(), []AppendBatchItem{{
			Store:              store,
			Records:            proposal.Records,
			ExactBaseOffset:    true,
			ExpectedBaseOffset: proposal.Manifest.BaseOffset,
			Proposal:           proposal.Manifest,
		}})
		if len(results) != 1 || results[0].Err != nil || results[0].Outcome != quorumlog.AppendOutcomeDurable {
			t.Fatalf("StoreAppendBatch(base=%d) = %+v, want durable", proposal.Manifest.BaseOffset, results)
		}
	}
	frontier, err := store.LoadDurableFrontier(context.Background())
	if err != nil {
		t.Fatalf("LoadDurableFrontier(): %v", err)
	}

	corruptTail := firstTail
	corruptTail.Digest[0] ^= 0xff
	batch := engine.engine.NewBatch()
	if err := batch.Set(encodeEntryIdentityKey(ChannelKey(channelKey), 1), encodeDurableEntryIdentity(corruptTail)); err != nil {
		batch.Close()
		t.Fatalf("corrupt retained tail: %v", err)
	}
	if err := batch.Commit(true); err != nil {
		batch.Close()
		t.Fatalf("commit corrupt retained tail: %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("close corruption batch: %v", err)
	}

	result, err := store.ReplaceRecoverySuffix(context.Background(), ReplaceRecoverySuffixRequest{
		Expected: frontier, KeepThrough: 1,
	})
	if !errors.Is(err, channel.ErrCorruptState) || result.Outcome != quorumlog.AppendOutcomeConflict {
		t.Fatalf("ReplaceRecoverySuffix() = %+v, %v; want corrupt-state conflict", result, err)
	}
	if _, present, err := store.GetMessageBySeq(2); err != nil || !present {
		t.Fatalf("GetMessageBySeq(2) after rejected replacement = present %v, error %v; want unchanged suffix", present, err)
	}
}

func TestRecoverySuffixChainAcceptsAdjacentCompleteProposals(t *testing.T) {
	first, firstTail := recoveryProposalFixture(t, 11, 0, quorumlog.EntryIdentity{})
	second, _ := recoveryProposalFixture(t, 12, 1, firstTail)
	chain, err := newRecoverySuffixChain(0, durableProposalRecord{}, quorumlog.EntryIdentity{}, 2)
	if err != nil {
		t.Fatalf("newRecoverySuffixChain(): %v", err)
	}
	for _, proposal := range []RecoveryProposal{first, second} {
		if err := chain.admit(proposal.Manifest, len(proposal.Records)); err != nil {
			t.Fatalf("admit(base=%d): %v", proposal.Manifest.BaseOffset, err)
		}
	}
	if chain.lastOffset != 2 {
		t.Fatalf("last offset = %d, want 2", chain.lastOffset)
	}
}

func TestRecoverySuffixChainRejectsBrokenTopology(t *testing.T) {
	first, firstTail := recoveryProposalFixture(t, 21, 0, quorumlog.EntryIdentity{})
	skippedPrevious := firstTail
	skippedPrevious.Index = 2
	skipped, _ := recoveryProposalFixture(t, 22, 2, skippedPrevious)
	forgedPrevious := firstTail
	forgedPrevious.Digest[0] ^= 0xff
	forged, _ := recoveryProposalFixture(t, 23, 1, forgedPrevious)
	reusedCommand, _ := recoveryProposalFixtureWithCommand(t, 24, 1, firstTail, first.Manifest.CommandID)

	tests := []struct {
		name      string
		candidate RecoveryProposal
		wantError error
	}{
		{
			name:      "skipped offset",
			candidate: skipped,
			wantError: channel.ErrInvalidArgument,
		},
		{
			name:      "forged predecessor",
			candidate: forged,
			wantError: dberrors.ErrConflict,
		},
		{
			name:      "reused command identity",
			candidate: reusedCommand,
			wantError: dberrors.ErrConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chain, err := newRecoverySuffixChain(0, durableProposalRecord{}, quorumlog.EntryIdentity{}, 2)
			if err != nil {
				t.Fatalf("newRecoverySuffixChain(): %v", err)
			}
			if err := chain.admit(first.Manifest, len(first.Records)); err != nil {
				t.Fatalf("admit(first): %v", err)
			}
			if err := chain.admit(test.candidate.Manifest, len(test.candidate.Records)); !errors.Is(err, test.wantError) {
				t.Fatalf("admit(second) error = %v, want %v", err, test.wantError)
			}
		})
	}
}

func recoveryProposalFixture(t *testing.T, marker byte, base uint64, previous quorumlog.EntryIdentity) (RecoveryProposal, quorumlog.EntryIdentity) {
	t.Helper()
	return recoveryProposalFixtureWithCommand(t, marker, base, previous, quorumlog.CommandID{31: marker})
}

func recoveryProposalFixtureWithCommand(t *testing.T, marker byte, base uint64, previous quorumlog.EntryIdentity, commandID quorumlog.CommandID) (RecoveryProposal, quorumlog.EntryIdentity) {
	t.Helper()
	row := messageRow{
		MessageID: uint64(8_000) + uint64(marker), ClientMsgNo: fmt.Sprintf("recovery-%d", marker),
		ChannelID: "recovery-boundary", ChannelType: 1, FromUID: "sender",
		ServerTimestampMS: 1_700_000_000_000 + int64(marker), Payload: []byte{marker},
	}
	record, err := compatibilityRecordFromRow(row)
	if err != nil {
		t.Fatalf("compatibilityRecordFromRow(): %v", err)
	}
	record.Epoch = 3
	manifest := DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: commandID, BaseOffset: base, LastOffset: base + 1,
		PreviousTerm: previous.LeaderTerm, PreviousIndex: base, PreviousDigest: previous.Digest,
	}
	rows, err := compatibilityRowsFromRecords(base+1, []channel.Record{record})
	if err != nil {
		t.Fatalf("compatibilityRowsFromRecords(): %v", err)
	}
	entries, ok := deriveDurableProposalEntries(manifest, []channel.Record{record}, rows)
	if !ok || len(entries) != 1 {
		t.Fatal("deriveDurableProposalEntries() failed")
	}
	manifest.Digest = entries[0].Digest
	return RecoveryProposal{Manifest: manifest, Records: []channel.Record{record}}, entries[0]
}
