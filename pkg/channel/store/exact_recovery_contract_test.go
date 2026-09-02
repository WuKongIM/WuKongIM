package store

import (
	"context"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestExactRecoveryStoreContract(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		testExactRecoveryStoreContract(t, NewMemoryFactory(), "memory")
	})
	t.Run("message_db", func(t *testing.T) {
		factory := NewMessageDBFactory(t.TempDir())
		t.Cleanup(func() { require.NoError(t, factory.Close()) })
		testExactRecoveryStoreContract(t, factory, "message-db")
	})
}

func testExactRecoveryStoreContract(t *testing.T, factory Factory, suffix string) {
	t.Helper()
	ctx := context.Background()
	id := ch.ChannelID{ID: "exact-recovery-" + suffix, Type: 1}
	channelStore, err := factory.ChannelStore(ch.ChannelKeyForID(id), id)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, channelStore.Close()) })

	stateLoader, ok := channelStore.(ExactStateLoader)
	require.True(t, ok)
	recoveryLoader, ok := channelStore.(ExactRecoveryStateLoader)
	require.True(t, ok)
	pageReader, ok := channelStore.(ExactRecoveryPageReader)
	require.True(t, ok)
	proposalLookup, ok := channelStore.(ExactProposalLookup)
	require.True(t, ok)
	suffixReplacer, ok := channelStore.(RecoverySuffixReplacer)
	require.True(t, ok)

	empty, err := stateLoader.LoadExactState(ctx)
	require.NoError(t, err)
	require.Equal(t, ExactState{}, empty)

	firstRecords := []ch.Record{
		{ID: 101, Epoch: 3, FromUID: "alice", ClientMsgNo: "first-1", ServerTimestampMS: 1_700_000_000_001, Payload: []byte("one"), SizeBytes: 3},
		{ID: 102, Epoch: 3, FromUID: "alice", ClientMsgNo: "first-2", ServerTimestampMS: 1_700_000_000_002, Payload: []byte("two"), SizeBytes: 3},
	}
	first := sealRecoveryProposal(t, 1, 0, ch.ProposalManifest{}, firstRecords)
	appendResult, err := channelStore.AppendLeader(ctx, AppendLeaderRequest{
		Records: first.Records, ExactBaseOffset: true, ExpectedBaseOffset: 0,
		Proposal: first.Manifest, Committed: 1,
	})
	require.NoError(t, err)
	require.Equal(t, AppendOutcomeDurable, appendResult.Outcome)

	secondRecords := []ch.Record{{
		ID: 103, Epoch: 3, FromUID: "bob", ClientMsgNo: "second-1",
		ServerTimestampMS: 1_700_000_000_003, Payload: []byte("old-tail"), SizeBytes: len("old-tail"),
	}}
	second := sealRecoveryProposal(t, 2, 2, first.Manifest, secondRecords)
	appendResult, err = channelStore.AppendLeader(ctx, AppendLeaderRequest{
		Records: second.Records, ExactBaseOffset: true, ExpectedBaseOffset: 2,
		Proposal: second.Manifest, Committed: 1,
	})
	require.NoError(t, err)
	require.Equal(t, AppendOutcomeDurable, appendResult.Outcome)

	secondEntries, ok := ch.DeriveProposalEntries(second.Manifest, len(second.Records), func(index int) ch.Record {
		return second.Records[index]
	})
	require.True(t, ok)
	wantState := ExactState{
		InitialState: InitialState{LEO: 3, HW: 1, CheckpointHW: 1},
		Manifest:     second.Manifest,
		TailIdentity: secondEntries[0],
	}
	loaded, err := stateLoader.LoadExactState(ctx)
	require.NoError(t, err)
	require.Equal(t, wantState, loaded)

	recovery, err := recoveryLoader.LoadExactRecoveryState(ctx, []uint64{1, 3, 4})
	require.NoError(t, err)
	require.Equal(t, wantState, recovery.ExactState)
	require.Len(t, recovery.Entries, 3)
	require.True(t, recovery.Entries[0].Present)
	require.Equal(t, uint64(1), recovery.Entries[0].Identity.Index)
	require.Equal(t, secondEntries[0], recovery.Entries[1].Identity)
	require.Equal(t, ExactEntryProbe{Index: 4}, recovery.Entries[2])

	page, err := pageReader.ReadExactRecoveryPage(ctx, ExactRecoveryPageRequest{
		From: 1, Through: 3, MaxBytes: 64 << 10,
	})
	require.NoError(t, err)
	require.Equal(t, wantState, page.ExactState)
	require.Equal(t, []uint64{101, 102, 103}, recordIDs(page.Records))
	require.Len(t, page.Entries, 3)
	require.Equal(t, []byte("one"), page.Records[0].Payload)
	page.Records[0].Payload[0] = 'x'
	reread, err := pageReader.ReadExactRecoveryPage(ctx, ExactRecoveryPageRequest{
		From: 1, Through: 3, MaxBytes: 64 << 10,
	})
	require.NoError(t, err)
	require.Equal(t, []byte("one"), reread.Records[0].Payload)

	proposal, found, err := proposalLookup.LoadExactProposal(ctx, ExactProposalRequest{
		CommandID: first.Manifest.CommandID, MaxRecords: 2, MaxBytes: 64 << 10,
	})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, first.Manifest, proposal.Manifest)
	require.Equal(t, []uint64{101, 102}, recordIDs(proposal.Records))
	proposal.Records[0].Payload[0] = 'x'
	proposal, found, err = proposalLookup.LoadExactProposal(ctx, ExactProposalRequest{
		CommandID: first.Manifest.CommandID, MaxRecords: 2, MaxBytes: 64 << 10,
	})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("one"), proposal.Records[0].Payload)

	_, found, err = proposalLookup.LoadExactProposal(ctx, ExactProposalRequest{
		CommandID: ch.CommandID{31: 99}, MaxRecords: 2, MaxBytes: 64 << 10,
	})
	require.NoError(t, err)
	require.False(t, found)
	_, _, err = proposalLookup.LoadExactProposal(ctx, ExactProposalRequest{
		CommandID: first.Manifest.CommandID, MaxRecords: 1, MaxBytes: 64 << 10,
	})
	require.ErrorIs(t, err, ch.ErrBackpressured)
	_, _, err = proposalLookup.LoadExactProposal(ctx, ExactProposalRequest{
		CommandID: first.Manifest.CommandID, MaxRecords: 2, MaxBytes: 1,
	})
	require.ErrorIs(t, err, ch.ErrBackpressured)

	_, err = pageReader.ReadExactRecoveryPage(ctx, ExactRecoveryPageRequest{
		From: 1, Through: 1, MaxBytes: 64 << 10,
	})
	require.ErrorIs(t, err, ch.ErrBackpressured, "a recovery page must not split a proposal")
	_, err = pageReader.ReadExactRecoveryPage(ctx, ExactRecoveryPageRequest{
		From: 2, Through: 3, MaxBytes: 64 << 10,
	})
	require.ErrorIs(t, err, ch.ErrLogConflict, "a recovery page must start on a proposal boundary")
	_, err = pageReader.ReadExactRecoveryPage(ctx, ExactRecoveryPageRequest{
		From: 1, Through: 3, MaxBytes: 1,
	})
	require.ErrorIs(t, err, ch.ErrBackpressured)

	replacementRecords := []ch.Record{{
		ID: 104, Epoch: 3, FromUID: "carol", ClientMsgNo: "replacement-1",
		ServerTimestampMS: 1_700_000_000_004, Payload: []byte("new-tail"), SizeBytes: len("new-tail"),
	}}
	replacement := sealRecoveryProposal(t, 3, 2, first.Manifest, replacementRecords)
	replaced, err := suffixReplacer.ReplaceRecoverySuffix(ctx, ReplaceRecoverySuffixRequest{
		Expected: wantState, KeepThrough: 2,
		Proposals: []RecoveryProposal{replacement}, Committed: 2,
	})
	require.NoError(t, err)
	require.Equal(t, ReplaceRecoverySuffixResult{LastOffset: 3, Outcome: AppendOutcomeDurable}, replaced)

	replacementEntries, ok := ch.DeriveProposalEntries(replacement.Manifest, len(replacement.Records), func(index int) ch.Record {
		return replacement.Records[index]
	})
	require.True(t, ok)
	wantReplacedState := ExactState{
		InitialState: InitialState{LEO: 3, HW: 2, CheckpointHW: 2},
		Manifest:     replacement.Manifest,
		TailIdentity: replacementEntries[0],
	}
	loaded, err = stateLoader.LoadExactState(ctx)
	require.NoError(t, err)
	require.Equal(t, wantReplacedState, loaded)
	oldProposal, found, err := proposalLookup.LoadExactProposal(ctx, ExactProposalRequest{
		CommandID: second.Manifest.CommandID, MaxRecords: 1, MaxBytes: 64 << 10,
	})
	require.NoError(t, err)
	require.False(t, found)
	require.Empty(t, oldProposal)
	newProposal, found, err := proposalLookup.LoadExactProposal(ctx, ExactProposalRequest{
		CommandID: replacement.Manifest.CommandID, MaxRecords: 1, MaxBytes: 64 << 10,
	})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte("new-tail"), newProposal.Records[0].Payload)

	rejected, err := suffixReplacer.ReplaceRecoverySuffix(ctx, ReplaceRecoverySuffixRequest{
		Expected: wantState, KeepThrough: 2, Committed: 2,
	})
	require.ErrorIs(t, err, ch.ErrLogConflict)
	require.Equal(t, AppendOutcomeConflict, rejected.Outcome)
	loaded, err = stateLoader.LoadExactState(ctx)
	require.NoError(t, err)
	require.Equal(t, wantReplacedState, loaded, "a rejected replacement must leave the durable state unchanged")
}

func sealRecoveryProposal(t *testing.T, commandByte byte, base uint64, previous ch.ProposalManifest, records []ch.Record) RecoveryProposal {
	t.Helper()
	manifest := ch.ProposalManifest{
		Version: ch.ProposalManifestVersion, ChannelEpoch: 3, LeaderTerm: 5, FenceVersion: 7,
		CommandID: ch.CommandID{31: commandByte}, BaseOffset: base,
		LastOffset: base + uint64(len(records)), PreviousIndex: base,
	}
	if base > 0 {
		manifest.PreviousTerm = previous.LeaderTerm
		manifest.PreviousDigest = previous.Digest
	}
	sealed, _, ok := ch.SealProposalManifest(manifest, records)
	require.True(t, ok)
	return RecoveryProposal{Manifest: sealed, Records: records}
}

func recordIDs(records []ch.Record) []uint64 {
	ids := make([]uint64, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	return ids
}
