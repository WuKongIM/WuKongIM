package message

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func replayRetirementProposal(t *testing.T, previous DurableProposalManifest, retirement quorumlog.MQTTReplayRetirement, command byte) (DurableProposalManifest, []channel.Record) {
	t.Helper()
	body, err := retirement.MarshalBinary()
	require.NoError(t, err)
	r, err := compatibilityRecordFromRow(messageRow{MessageID: 1000 + uint64(command), ChannelID: "activation", ChannelType: 1, FramerFlags: 4, ServerTimestampMS: 6000 + int64(command), Payload: body})
	require.NoError(t, err)
	r.Epoch = 1
	m := DurableProposalManifest{Version: quorumlog.MQTTReplayRetirementProposalManifestVersion, ChannelEpoch: 1, LeaderTerm: previous.LeaderTerm, FenceVersion: previous.FenceVersion, CommandID: quorumlog.CommandID{command}, BaseOffset: previous.LastOffset, LastOffset: previous.LastOffset + 1, PreviousIndex: previous.LastOffset, PreviousTerm: previous.LeaderTerm, PreviousDigest: previous.Digest}
	return sealCompatProposalManifest(t, m, []channel.Record{r}), []channel.Record{r}
}

func TestMQTTRetirementJournalPendingTrimRestartAndBackup(t *testing.T) {
	ctx := context.Background()
	f := newReplayTransferFixture(t)
	anchor, rows, a := replayAnchorProposal(t, f, 3)
	appendMQTTActivation(t, f.target, anchor, rows, 5)
	r := quorumlog.MQTTReplayRetirement{Anchor: a, AnchorPosition: 5, AnchorDigest: anchor.Digest}
	m, records := replayRetirementProposal(t, anchor, r, 4)
	before, sourcePresent, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	require.True(t, sourcePresent)
	appendMQTTActivation(t, f.target, m, records, 5)
	_, found, err := f.target.LoadMQTTReplayRetirement(ctx, 6)
	require.NoError(t, err)
	require.False(t, found)
	cut := BackupChannelCut{Key: f.target.log.key, ID: f.target.log.id, Checkpoint: Checkpoint{HW: 5}}
	pending := readBackupSnapshot(t, f.target.log.db, BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{cut}})
	restore := openTestMessageStore(t)
	defer restore.close(t)
	_, err = restore.db.ImportBackupSnapshot(ctx, pending)
	require.NoError(t, err)
	_, found, err = restore.db.engine.Get(mqttReplayRetirementKey(f.target.log.key, 6))
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, f.target.Truncate(5))
	_, found, err = f.targetEngine.engine.Get(mqttReplayRetirementKey(f.target.log.key, 6))
	require.NoError(t, err)
	require.False(t, found)
	appendMQTTActivation(t, f.target, m, records, 6)
	appendMQTTActivation(t, f.target, m, records, 6)
	proof, found, err := f.target.LoadMQTTReplayRetirement(ctx, 6)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, r, proof.Retirement)
	require.Equal(t, m, proof.Manifest)
	after, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
	_, shared, err := f.target.log.LoadMQTTReplayState(ctx)
	require.NoError(t, err)
	require.False(t, shared, "retirement journal alone must not fabricate local content coverage")
	// Controlled source release only permits testing journal independence from
	// ordinary history. Physical shared-content reclamation is not invoked here.
	after.Revision++
	after.CopiedThrough = 6
	after.ReceiptDigest = [32]byte{9}
	require.NoError(t, f.target.log.ApplyMQTTSourceState(ctx, before.Revision, after))
	trim, err := f.target.log.TrimPrefixThrough(ctx, 6)
	require.NoError(t, err)
	require.Equal(t, 6, trim.Deleted)
	require.NoError(t, f.target.Close())
	require.NoError(t, f.targetEngine.Close())
	f.targetEngine, err = Open(f.targetPath)
	require.NoError(t, err)
	f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	got, found, err := f.target.LoadMQTTReplayRetirement(ctx, 6)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, proof, got)
	cut.Checkpoint.HW = 6
	body := readBackupSnapshot(t, f.target.log.db, BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{cut}})
	restored := openTestMessageStore(t)
	defer restored.close(t)
	_, err = restored.db.ImportBackupSnapshot(ctx, body)
	require.NoError(t, err)
	log := mustAcquireChannel(t, restored.db, f.target.log.key, f.target.log.id)
	defer log.Close()
	got, found, err = log.LoadMQTTReplayRetirement(ctx, 6)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, proof, got)
}

func TestMQTTRetirementJournalRejectsMissingOrChangedProof(t *testing.T) {
	for _, mode := range []string{"missing_journal", "changed_journal", "missing_anchor", "missing_entry", "missing_pair", "missing_source"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newReplayTransferFixture(t)
			anchor, rows, a := replayAnchorProposal(t, f, 3)
			appendMQTTActivation(t, f.target, anchor, rows, 5)
			r := quorumlog.MQTTReplayRetirement{Anchor: a, AnchorPosition: 5, AnchorDigest: anchor.Digest}
			m, records := replayRetirementProposal(t, anchor, r, 4)
			appendMQTTActivation(t, f.target, m, records, 6)
			key := mqttReplayRetirementKey(f.target.log.key, 6)
			batch := f.targetEngine.engine.NewBatch()
			defer batch.Close()
			switch mode {
			case "missing_journal":
				require.NoError(t, batch.Delete(key))
			case "changed_journal":
				value, _, err := f.targetEngine.engine.Get(key)
				require.NoError(t, err)
				env, err := rowcodec.UnwrapBorrowed(key, value)
				require.NoError(t, err)
				changed := append([]byte(nil), env.Payload...)
				changed[len(changed)-1] ^= 1
				require.NoError(t, batch.Set(key, rowcodec.Wrap(key, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, changed)))
			case "missing_anchor":
				require.NoError(t, batch.Delete(mqttReplayAnchorKey(f.target.log.key, 5)))
			case "missing_entry":
				require.NoError(t, batch.Delete(encodeEntryIdentityKey(f.target.log.key, 6)))
			case "missing_pair":
				require.NoError(t, batch.Delete(encodeProposalByCommandKey(f.target.log.key, m.CommandID)))
			case "missing_source":
				require.NoError(t, batch.Delete(mqttSourceKey(f.target.log.key)))
			}
			require.NoError(t, batch.Commit(true))
			_, _, err := f.target.LoadMQTTReplayRetirement(ctx, 6)
			require.Error(t, err)
		})
	}
}

func TestMQTTRetirementRejectsUnprovedReferencesAndBackupOmission(t *testing.T) {
	for _, mode := range []string{"digest", "prefix", "uncommitted", "missing", "absent_activation"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newReplayTransferFixture(t)
			anchor, rows, a := replayAnchorProposal(t, f, 3)
			hw := uint64(5)
			if mode == "uncommitted" {
				hw = 4
			}
			appendMQTTActivation(t, f.target, anchor, rows, hw)
			r := quorumlog.MQTTReplayRetirement{Anchor: a, AnchorPosition: 5, AnchorDigest: anchor.Digest}
			if mode == "digest" {
				r.AnchorDigest[0] ^= 1
			}
			if mode == "prefix" {
				r.Anchor.TotalStoredBytes++
			}
			if mode == "missing" {
				b := f.targetEngine.engine.NewBatch()
				defer b.Close()
				require.NoError(t, b.Delete(mqttReplayAnchorKey(f.target.log.key, 5)))
				require.NoError(t, b.Commit(true))
			}
			if mode == "absent_activation" {
				b := f.targetEngine.engine.NewBatch()
				defer b.Close()
				require.NoError(t, b.Delete(mqttActivationKey(f.target.log.key)))
				require.NoError(t, b.Delete(mqttSourceKey(f.target.log.key)))
				require.NoError(t, b.Commit(true))
			}
			m, records := replayRetirementProposal(t, anchor, r, 4)
			results := StoreAppendBatch(ctx, []AppendBatchItem{{Store: f.target, Records: records, ExpectedBaseOffset: 5, ExactBaseOffset: true, Proposal: m, Committed: hw}})
			require.Len(t, results, 1)
			require.Error(t, results[0].Err)
			require.False(t, results[0].Outcome.Durable())
		})
	}
	f := newReplayTransferFixture(t)
	ctx := context.Background()
	anchor, rows, a := replayAnchorProposal(t, f, 3)
	appendMQTTActivation(t, f.target, anchor, rows, 5)
	m, records := replayRetirementProposal(t, anchor, quorumlog.MQTTReplayRetirement{Anchor: a, AnchorPosition: 5, AnchorDigest: anchor.Digest}, 4)
	appendMQTTActivation(t, f.target, m, records, 6)
	snapshot, err := f.targetEngine.engine.NewSnapshot()
	require.NoError(t, err)
	defer snapshot.Close()
	entries, err := snapshotBackupSystemEntries(ctx, snapshot, f.target.log.key, 6)
	require.NoError(t, err)
	for _, missingKey := range [][]byte{mqttReplayRetirementKey(f.target.log.key, 6), mqttReplayAnchorKey(f.target.log.key, 5)} {
		var missing []backupRawEntry
		for _, entry := range entries {
			if string(entry.Key) != string(missingKey) {
				missing = append(missing, entry)
			}
		}
		require.Error(t, validateBackupProposalSystemEntries(f.target.log.key, 6, missing))
	}
}

func TestMQTTRetirementRecoveryCarriesActivationAnchorAndDecision(t *testing.T) {
	ctx := context.Background()
	db := openCompatEngine(t)
	st := mustForChannel(t, db, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	defer st.Close()
	activation, activationRows := mqttActivationProposal(t, DurableProposalManifest{}, 1)
	a := quorumlog.MQTTReplayAnchor{SourceCommand: activation.CommandID, Through: 1, TotalStoredBytes: 200, Digest: quorumlog.EntryDigest{7}}
	body, err := a.MarshalBinary()
	require.NoError(t, err)
	row, err := compatibilityRecordFromRow(messageRow{MessageID: 999, ChannelID: "activation", ChannelType: 1, FramerFlags: 4, ServerTimestampMS: 9999, Payload: body})
	require.NoError(t, err)
	row.Epoch = 1
	anchor := sealCompatProposalManifest(t, DurableProposalManifest{Version: 5, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: quorumlog.CommandID{2}, BaseOffset: 1, LastOffset: 2, PreviousIndex: 1, PreviousTerm: 1, PreviousDigest: activation.Digest}, []channel.Record{row})
	m, records := replayRetirementProposal(t, anchor, quorumlog.MQTTReplayRetirement{Anchor: a, AnchorPosition: 2, AnchorDigest: anchor.Digest}, 3)
	state, err := st.LoadDurableRecovery(ctx, nil)
	require.NoError(t, err)
	result, err := st.ReplaceRecoverySuffix(ctx, ReplaceRecoverySuffixRequest{Expected: state.DurableFrontier, Proposals: []RecoveryProposal{{Manifest: activation, Records: activationRows}, {Manifest: anchor, Records: []channel.Record{row}}, {Manifest: m, Records: records}}, Committed: 3})
	require.NoError(t, err)
	require.True(t, result.Outcome.Durable())
	proof, found, err := st.LoadMQTTReplayRetirement(ctx, 3)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, m, proof.Manifest)
}

func TestMQTTRetirementPrefixCannotRegressOrChangeEqualReference(t *testing.T) {
	for _, mode := range []string{"regress", "changed_reference", "same"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newReplayTransferFixture(t)
			first, rows, a := replayAnchorProposal(t, f, 3)
			appendMQTTActivation(t, f.target, first, rows, 5)
			other := a
			if mode == "regress" {
				other.Through = 1
				other.TotalBytes = 0
				other.TotalStoredBytes = 100
				other.Digest = quorumlog.EntryDigest{9}
			}
			body, err := other.MarshalBinary()
			require.NoError(t, err)
			row, err := compatibilityRecordFromRow(messageRow{MessageID: 998, ChannelID: "activation", ChannelType: 1, FramerFlags: 4, ServerTimestampMS: 9998, Payload: body})
			require.NoError(t, err)
			row.Epoch = 1
			second := first
			second.CommandID = quorumlog.CommandID{8}
			second.BaseOffset, second.LastOffset, second.PreviousIndex, second.PreviousDigest = 5, 6, 5, first.Digest
			second = sealCompatProposalManifest(t, second, []channel.Record{row})
			appendMQTTActivation(t, f.target, second, []channel.Record{row}, 6)
			decision := quorumlog.MQTTReplayRetirement{Anchor: a, AnchorPosition: 5, AnchorDigest: first.Digest}
			previous, records := replayRetirementProposal(t, second, decision, 4)
			appendMQTTActivation(t, f.target, previous, records, 7)
			if mode != "same" {
				decision.Anchor, decision.AnchorPosition, decision.AnchorDigest = other, 6, second.Digest
			}
			next, records := replayRetirementProposal(t, previous, decision, 5)
			results := StoreAppendBatch(ctx, []AppendBatchItem{{Store: f.target, Records: records, ExpectedBaseOffset: 7, ExactBaseOffset: true, Proposal: next, Committed: 8}})
			require.Len(t, results, 1)
			if mode == "same" {
				require.NoError(t, results[0].Err)
				require.True(t, results[0].Outcome.Durable())
			} else {
				require.Error(t, results[0].Err)
				require.False(t, results[0].Outcome.Durable())
			}
		})
	}
}
