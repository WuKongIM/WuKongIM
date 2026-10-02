package message

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/rowcodec"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
	"github.com/stretchr/testify/require"
)

func replayAnchorProposal(t *testing.T, f *replayTransferFixture, command byte) (DurableProposalManifest, []channel.Record, quorumlog.MQTTReplayAnchor) {
	t.Helper()
	p := f.all.After
	a := quorumlog.MQTTReplayAnchor{SourceCommand: f.activation.CommandID, StartAfter: p.StartAfter, Through: p.Through, TotalBytes: p.TotalBytes, TotalStoredBytes: p.TotalStoredBytes, Digest: p.Digest}
	body, err := a.MarshalBinary()
	require.NoError(t, err)
	r, err := compatibilityRecordFromRow(messageRow{MessageID: 900 + uint64(command), ChannelID: "activation", ChannelType: 1, FramerFlags: 4, ServerTimestampMS: 5000 + int64(command), Payload: body})
	require.NoError(t, err)
	r.Epoch = 1
	m := DurableProposalManifest{Version: quorumlog.MQTTReplayAnchorProposalManifestVersion, ChannelEpoch: 1, LeaderTerm: 2, FenceVersion: 2, CommandID: quorumlog.CommandID{command}, BaseOffset: 4, LastOffset: 5, PreviousIndex: 4, PreviousTerm: 2, PreviousDigest: f.business.Digest}
	m = sealCompatProposalManifest(t, m, []channel.Record{r})
	return m, []channel.Record{r}, a
}

func TestMQTTReplayAnchorPendingCommittedTrimRestartAndBackup(t *testing.T) {
	ctx := context.Background()
	f := newReplayTransferFixture(t)
	m, records, a := replayAnchorProposal(t, f, 3)
	before, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	appendMQTTActivation(t, f.target, m, records, 4)
	_, found, err := f.target.log.LoadMQTTReplayAnchor(ctx, 5)
	require.NoError(t, err)
	require.False(t, found)
	cut := BackupChannelCut{Key: f.target.log.key, ID: f.target.log.id, Checkpoint: Checkpoint{HW: 4}}
	pending := readBackupSnapshot(t, f.target.log.db, BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{cut}})
	restore := openTestMessageStore(t)
	defer restore.close(t)
	_, err = restore.db.ImportBackupSnapshot(ctx, pending)
	require.NoError(t, err)
	_, found, err = restore.db.engine.Get(mqttReplayAnchorKey(f.target.log.key, 5))
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, f.target.StoreCheckpointHWMonotonic(ctx, 5))
	proof, found, err := f.target.log.LoadMQTTReplayAnchor(ctx, 5)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, a, proof.Anchor)
	require.Equal(t, m, proof.Manifest)
	after, _, err := f.target.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "anchor must not release the source")
	// A test-controlled external release allows original-body trimming; the
	// locally committed anchor remains independent of any donor transfer page.
	release := after
	release.Revision++
	release.CopiedThrough = 4
	release.ReceiptDigest = [32]byte{9}
	require.NoError(t, f.target.log.ApplyMQTTSourceState(ctx, after.Revision, release))
	trim, err := f.target.log.TrimPrefixThrough(ctx, 4)
	require.NoError(t, err)
	require.Equal(t, 4, trim.Deleted)
	require.NoError(t, f.target.Close())
	require.NoError(t, f.targetEngine.Close())
	f.targetEngine, err = Open(f.targetPath)
	require.NoError(t, err)
	f.target = mustForChannel(t, f.targetEngine, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	got, found, err := f.target.log.LoadMQTTReplayAnchor(ctx, 5)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, proof, got)
	expected := MQTTReplayState{Generation: quorumlog.MQTTSourceGeneration(got.Anchor.SourceCommand), StartAfter: got.Anchor.StartAfter, Through: got.Anchor.Through, TotalBytes: got.Anchor.TotalBytes, TotalStoredBytes: got.Anchor.TotalStoredBytes, Digest: got.Anchor.Digest}
	_, err = f.target.log.ImportMQTTReplay(ctx, expected, f.all)
	require.NoError(t, err)
	// The anchor control itself is copied before releasing its ordinary row.
	_, err = f.target.log.CopyMQTTReplaySource(ctx, f.generation, 5, 5, replayTransferBudget)
	require.NoError(t, err)
	next := release
	next.Revision++
	next.CopiedThrough = 5
	require.NoError(t, f.target.log.ApplyMQTTSourceState(ctx, release.Revision, next))
	trim, err = f.target.log.TrimPrefixThrough(ctx, 5)
	require.NoError(t, err)
	require.Equal(t, 1, trim.Deleted)
	_, originalPresent, err := f.targetEngine.engine.Get(encodeMessageRowKey(f.target.log.key, 5, 0))
	require.NoError(t, err)
	require.False(t, originalPresent)
	trimmed, found, err := f.target.log.LoadMQTTReplayAnchor(ctx, 5)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, proof, trimmed)
	cut.Checkpoint.HW = 5
	body := readBackupSnapshot(t, f.target.log.db, BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{cut}})
	restored := openTestMessageStore(t)
	defer restored.close(t)
	_, err = restored.db.ImportBackupSnapshot(ctx, body)
	require.NoError(t, err)
	log := mustAcquireChannel(t, restored.db, f.target.log.key, f.target.log.id)
	defer log.Close()
	actual, found, err := log.LoadMQTTReplayAnchor(ctx, 5)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, proof, actual)
}

func TestMQTTReplayAnchorEvidenceRejectsCorruption(t *testing.T) {
	for _, mode := range []string{"missing_journal", "wrong_key", "changed_payload", "missing_entry", "missing_proposal", "foreign_source"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newReplayTransferFixture(t)
			m, records, _ := replayAnchorProposal(t, f, 3)
			appendMQTTActivation(t, f.target, m, records, 5)
			key := mqttReplayAnchorKey(f.target.log.key, 5)
			b, found, err := f.targetEngine.engine.Get(key)
			require.NoError(t, err)
			require.True(t, found)
			batch := f.targetEngine.engine.NewBatch()
			defer batch.Close()
			switch mode {
			case "missing_journal":
				require.NoError(t, batch.Delete(key))
			case "wrong_key":
				require.NoError(t, batch.Set(mqttReplayAnchorKey(f.target.log.key, 6), b))
			case "changed_payload":
				env, e := rowcodec.UnwrapBorrowed(key, b)
				require.NoError(t, e)
				changed := append([]byte(nil), env.Payload...)
				changed[len(changed)-1] ^= 1
				require.NoError(t, batch.Set(key, rowcodec.Wrap(key, 1, rowcodec.CodecFixed, rowcodec.FlagChecksum, changed)))
			case "missing_entry":
				require.NoError(t, batch.Delete(encodeEntryIdentityKey(f.target.log.key, 5)))
			case "missing_proposal":
				require.NoError(t, batch.Delete(encodeProposalByCommandKey(f.target.log.key, m.CommandID)))
			case "foreign_source":
				require.NoError(t, batch.Delete(mqttActivationKey(f.target.log.key)))
			}
			require.NoError(t, batch.Commit(true))
			position := uint64(5)
			if mode == "wrong_key" {
				position = 6
			}
			_, _, err = f.target.log.LoadMQTTReplayAnchor(ctx, position)
			require.Error(t, err)
		})
	}
}

func TestMQTTReplayAnchorPendingSuffixReplacementAndBackupValidation(t *testing.T) {
	f := newReplayTransferFixture(t)
	ctx := context.Background()
	m, records, _ := replayAnchorProposal(t, f, 3)
	appendMQTTActivation(t, f.target, m, records, 4)
	require.NoError(t, f.target.Truncate(4))
	_, found, err := f.targetEngine.engine.Get(mqttReplayAnchorKey(f.target.log.key, 5))
	require.NoError(t, err)
	require.False(t, found)
	appendMQTTActivation(t, f.target, m, records, 5)
	cut := BackupChannelCut{Key: f.target.log.key, ID: f.target.log.id, Checkpoint: Checkpoint{HW: 5}}
	snap, err := f.target.log.db.engine.NewSnapshot()
	require.NoError(t, err)
	defer snap.Close()
	entries, err := snapshotBackupSystemEntries(ctx, snap, cut.Key, 5)
	require.NoError(t, err)
	var missing []backupRawEntry
	for _, entry := range entries {
		if string(entry.Key) != string(mqttReplayAnchorKey(cut.Key, 5)) {
			missing = append(missing, entry)
		}
	}
	require.Error(t, validateBackupProposalSystemEntries(cut.Key, 5, missing), "backup must require every committed anchor journal")
}

func TestMQTTReplayAnchorBackupRejectsEntryFormatDowngrade(t *testing.T) {
	f := newReplayTransferFixture(t)
	ctx := context.Background()
	m, records, _ := replayAnchorProposal(t, f, 3)
	appendMQTTActivation(t, f.target, m, records, 5)
	snapshot, err := f.target.log.db.engine.NewSnapshot()
	require.NoError(t, err)
	defer snapshot.Close()
	entries, err := snapshotBackupSystemEntries(ctx, snapshot, f.target.log.key, 5)
	require.NoError(t, err)
	// Re-seal the same row under business format 1, then falsely label only its
	// enclosing manifest as control format 5. A matching hash alone is insufficient.
	native := m
	native.Version = 1
	native = sealCompatProposalManifest(t, native, records)
	body, _ := (quorumlog.MQTTReplayAnchor{SourceCommand: f.activation.CommandID, Through: f.all.After.Through, TotalBytes: f.all.After.TotalBytes, TotalStoredBytes: f.all.After.TotalStoredBytes, Digest: f.all.After.Digest}).MarshalBinary()
	_, proofs, ok := quorumlog.SealProposalManifest(native, []quorumlog.Record{{ID: 903, Index: 5, Epoch: 1, ServerTimestampMS: 5003, SyncOnce: true, Payload: body}})
	require.True(t, ok)
	forged := native
	forged.Version = quorumlog.MQTTReplayAnchorProposalManifestVersion
	for i := range entries {
		k := entries[i].Key
		if string(k) == string(encodeProposalByLastKey(f.target.log.key, 5)) || string(k) == string(encodeProposalByCommandKey(f.target.log.key, m.CommandID)) {
			entries[i].Value = encodeDurableProposalRecord(durableProposalRecord{manifest: forged})
		}
		if string(k) == string(encodeEntryIdentityKey(f.target.log.key, 5)) {
			entries[i].Value = encodeDurableEntryIdentity(proofs[0])
		}
	}
	require.Error(t, validateBackupProposalSystemEntries(f.target.log.key, 5, entries))
}

func TestMQTTReplayAnchorRecoveryReplacesPendingSourceAtomically(t *testing.T) {
	ctx := context.Background()
	db := openCompatEngine(t)
	st := mustForChannel(t, db, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	defer st.Close()
	old, oldRows := mqttActivationProposal(t, DurableProposalManifest{}, 1)
	appendMQTTActivation(t, st, old, oldRows, 0)
	next, nextRows := mqttActivationProposal(t, DurableProposalManifest{}, 2)
	anchor := quorumlog.MQTTReplayAnchor{SourceCommand: next.CommandID, Through: 1, TotalBytes: 40, TotalStoredBytes: 200, Digest: quorumlog.EntryDigest{7}}
	body, err := anchor.MarshalBinary()
	require.NoError(t, err)
	row, err := compatibilityRecordFromRow(messageRow{MessageID: 999, ChannelID: "activation", ChannelType: 1, FramerFlags: 4, ServerTimestampMS: 9999, Payload: body})
	require.NoError(t, err)
	row.Epoch = 1
	m := sealCompatProposalManifest(t, DurableProposalManifest{Version: quorumlog.MQTTReplayAnchorProposalManifestVersion, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1, CommandID: quorumlog.CommandID{3}, BaseOffset: 1, LastOffset: 2, PreviousIndex: 1, PreviousTerm: 1, PreviousDigest: next.Digest}, []channel.Record{row})
	state, err := st.LoadDurableRecovery(ctx, nil)
	require.NoError(t, err)
	result, err := st.ReplaceRecoverySuffix(ctx, ReplaceRecoverySuffixRequest{Expected: state.DurableFrontier, Proposals: []RecoveryProposal{{Manifest: next, Records: nextRows}, {Manifest: m, Records: []channel.Record{row}}}, Committed: 2})
	require.NoError(t, err)
	require.True(t, result.Outcome.Durable())
	proof, found, err := st.log.LoadMQTTReplayAnchor(ctx, 2)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, anchor, proof.Anchor)
	source, found, err := st.log.LoadMQTTSourceState(ctx)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, quorumlog.MQTTSourceGeneration(next.CommandID), source.Generation)
}
