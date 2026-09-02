package message

import (
	"context"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func TestReplaceRecoverySuffixAtomicallyPublishesVerifiedReplacement(t *testing.T) {
	engine := openCompatEngine(t)
	store, first, oldTail := appendRecoveryReplacementFixture(t, engine)
	defer store.Close()
	expected, err := store.LoadDurableFrontier(context.Background())
	if err != nil {
		t.Fatalf("LoadDurableFrontier() error = %v", err)
	}
	newRecords := []channel.Record{
		compatExactTestRecord(t, 3, 9203, "recovery-replace", "replacement-3"),
		compatExactTestRecord(t, 3, 9204, "recovery-replace", "replacement-4"),
	}
	replacement := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 6, FenceVersion: 8,
		CommandID: quorumlog.CommandID{31: 9}, BaseOffset: 2, LastOffset: 4,
		PreviousTerm: first.LeaderTerm, PreviousIndex: first.LastOffset, PreviousDigest: first.Digest,
	}, newRecords)

	result, err := store.ReplaceRecoverySuffix(context.Background(), ReplaceRecoverySuffixRequest{
		Expected: expected, KeepThrough: 2,
		Proposals: []RecoveryProposal{{Manifest: replacement, Records: newRecords}}, Committed: 4,
	})
	if err != nil {
		t.Fatalf("ReplaceRecoverySuffix() error = %v", err)
	}
	if result.Outcome != quorumlog.AppendOutcomeDurable || result.LastOffset != 4 {
		t.Fatalf("ReplaceRecoverySuffix() result = %#v", result)
	}
	if _, present, err := store.GetMessageByMessageID(9199); err != nil || present {
		t.Fatalf("replaced message present = %v, error = %v", present, err)
	}
	for seq, messageID := range map[uint64]uint64{1: 9201, 2: 9202, 3: 9203, 4: 9204} {
		message, present, err := store.GetMessageBySeq(seq)
		if err != nil || !present || message.MessageID != messageID {
			t.Fatalf("message[%d] = %#v, present %v, error %v", seq, message, present, err)
		}
	}
	checkpoint, err := store.LoadCheckpoint()
	if err != nil || checkpoint.HW != 4 || checkpoint.Epoch != 3 {
		t.Fatalf("checkpoint = %#v, error = %v", checkpoint, err)
	}
	if _, present, err := store.LoadDurableProposal(context.Background(), oldTail.CommandID, 1, 1<<20); err != nil || present {
		t.Fatalf("old proposal present = %v, error = %v", present, err)
	}
	proposal, present, err := store.LoadDurableProposal(context.Background(), replacement.CommandID, 2, 1<<20)
	if err != nil || !present || proposal.Manifest != replacement || len(proposal.Records) != 2 {
		t.Fatalf("replacement proposal = %#v, present %v, error %v", proposal, present, err)
	}
	frontier, err := store.LoadDurableFrontier(context.Background())
	if err != nil || frontier.LEO != 4 || frontier.Committed != 4 || frontier.Manifest.CommandID != replacement.CommandID {
		t.Fatalf("replacement frontier = %#v, error = %v", frontier, err)
	}
}

func TestReplaceRecoverySuffixCanRollBackOnlyUncommittedCompleteProposal(t *testing.T) {
	engine := openCompatEngine(t)
	store, first, _ := appendRecoveryReplacementFixture(t, engine)
	defer store.Close()
	expected, err := store.LoadDurableFrontier(context.Background())
	if err != nil {
		t.Fatalf("LoadDurableFrontier() error = %v", err)
	}
	result, err := store.ReplaceRecoverySuffix(context.Background(), ReplaceRecoverySuffixRequest{
		Expected: expected, KeepThrough: 2, Committed: 2,
	})
	if err != nil {
		t.Fatalf("ReplaceRecoverySuffix(truncate) error = %v", err)
	}
	if result.Outcome != quorumlog.AppendOutcomeDurable || result.LastOffset != 2 {
		t.Fatalf("ReplaceRecoverySuffix(truncate) result = %#v", result)
	}
	if _, present, err := store.GetMessageBySeq(3); err != nil || present {
		t.Fatalf("uncommitted suffix present = %v, error = %v", present, err)
	}
	frontier, err := store.LoadDurableFrontier(context.Background())
	if err != nil || frontier.LEO != 2 || frontier.Committed != 2 || frontier.Manifest.CommandID != first.CommandID {
		t.Fatalf("retained frontier = %#v, error = %v", frontier, err)
	}
}

func appendRecoveryReplacementFixture(t *testing.T, engine *Engine) (*ChannelStore, DurableProposalManifest, DurableProposalManifest) {
	t.Helper()
	id := channel.ChannelID{ID: "recovery-replace", Type: 1}
	store := mustForChannel(t, engine, "recovery-replace", id)
	firstRecords := []channel.Record{
		compatExactTestRecord(t, 3, 9201, id.ID, "retained-1"),
		compatExactTestRecord(t, 3, 9202, id.ID, "retained-2"),
	}
	first := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: quorumlog.CommandID{31: 1}, BaseOffset: 0, LastOffset: 2,
	}, firstRecords)
	oldRecords := []channel.Record{compatExactTestRecord(t, 3, 9199, id.ID, "old-tail")}
	oldTail := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: quorumlog.CommandID{31: 2}, BaseOffset: 2, LastOffset: 3,
		PreviousTerm: first.LeaderTerm, PreviousIndex: first.LastOffset, PreviousDigest: first.Digest,
	}, oldRecords)
	for index, item := range []AppendBatchItem{
		{Store: store, Records: firstRecords, ExactBaseOffset: true, ExpectedBaseOffset: 0, Proposal: first},
		{Store: store, Records: oldRecords, ExactBaseOffset: true, ExpectedBaseOffset: 2, Proposal: oldTail},
	} {
		results := StoreAppendBatch(context.Background(), []AppendBatchItem{item})
		if len(results) != 1 || results[0].Err != nil || results[0].Outcome != quorumlog.AppendOutcomeDurable {
			store.Close()
			t.Fatalf("StoreAppendBatch()[%d] = %#v", index, results)
		}
	}
	if err := store.StoreCheckpoint(channel.Checkpoint{Epoch: 3, HW: 2}); err != nil {
		store.Close()
		t.Fatalf("StoreCheckpoint() error = %v", err)
	}
	return store, first, oldTail
}
