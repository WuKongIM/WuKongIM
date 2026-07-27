package backup_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/stretchr/testify/require"
)

func TestReplicatedErasureIntegrityAuditorValidatesAndRepairsFullEvent(t *testing.T) {
	ctx := context.Background()
	primaryDir := t.TempDir()
	secondaryDir := t.TempDir()
	primary, err := backupinfra.NewFileRepository("primary", primaryDir)
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", secondaryDir)
	require.NoError(t, err)
	keys := testWrappingKeyManager{mask: 0x5a}
	codec := backupartifact.NewObjectCodec(
		keys, bytes.NewReader(bytes.Repeat([]byte{0x71}, 64)),
	)
	seed := sha256.Sum256([]byte("erasure-integrity-signing-key"))
	signer := testEd25519Signer{
		privateKey: ed25519.NewKeyFromSeed(seed[:]),
	}
	const (
		repositoryID     = "repository-prod"
		sourceClusterID  = "cluster-source"
		sourceGeneration = "source-generation-1"
		hashSlotCount    = uint16(256)
	)
	channelID := "channel-erasure-audit"
	hashSlot := routing.HashSlotForKey(channelID, hashSlotCount)
	eventID := backupartifact.ComputeErasureEventID(
		repositoryID, sourceClusterID, sourceGeneration,
		channelID, 2, 17,
	)
	event := backupartifact.ErasureLedgerEvent{
		Format:       backupartifact.ErasureLedgerEventFormat,
		Version:      backupartifact.ErasureLedgerEventVersion,
		RepositoryID: repositoryID, SourceClusterID: sourceClusterID,
		SourceGeneration: sourceGeneration, EventID: eventID,
		HashSlot: hashSlot, ChannelID: channelID, ChannelType: 2,
		ThroughSeq: 17, RequestedAtUnixMillis: 1_753_400_200_000,
	}
	plaintext, err := backupartifact.MarshalErasureLedgerEvent(event)
	require.NoError(t, err)
	sealed, err := codec.Seal(ctx, backupartifact.ObjectDescriptor{
		Key:      "objects/erasure-ledger/" + eventID + "/attempt-1.wkb",
		Kind:     backupartifact.ObjectKindErasureLedger,
		HashSlot: hashSlot,
	}, plaintext)
	require.NoError(t, err)
	record, err := backupartifact.SignErasureLedgerRecord(
		ctx, backupartifact.ErasureLedgerRecord{
			Format:       backupartifact.ErasureLedgerRecordFormat,
			Version:      backupartifact.ErasureLedgerRecordVersion,
			RepositoryID: repositoryID, SourceClusterID: sourceClusterID,
			SourceGeneration: sourceGeneration, EventID: eventID,
			HashSlot: hashSlot, CreatedAtUnixMillis: 1_753_400_200_000,
			Object: sealed.Entry,
		},
		signer)
	require.NoError(t, err)
	recordBody, err := backupartifact.MarshalErasureLedgerRecord(record)
	require.NoError(t, err)
	recordDigest := sha256.Sum256(recordBody)
	recordSHA := hex.EncodeToString(recordDigest[:])
	namespace := backupartifact.ComputeErasureLedgerStreamNamespace(
		repositoryID, sourceClusterID, sourceGeneration,
	)
	commitKey := backupartifact.ErasureLedgerCommitKey(
		namespace, hashSlot, 1,
	)
	commit, err := backupartifact.SignErasureLedgerCommit(
		ctx, backupartifact.ErasureLedgerCommit{
			Format:       backupartifact.ErasureLedgerCommitFormat,
			Version:      backupartifact.ErasureLedgerCommitVersion,
			RepositoryID: repositoryID, SourceClusterID: sourceClusterID,
			SourceGeneration: sourceGeneration, HashSlot: hashSlot,
			Sequence: 1, EventID: eventID,
			RecordKey: backupartifact.ErasureLedgerRecordKey(
				hashSlot, eventID,
			),
			RecordSHA256:        recordSHA,
			CreatedAtUnixMillis: 1_753_400_200_000,
			PrimaryRepository:   "primary", SecondaryRepository: "secondary",
		},
		signer)
	require.NoError(t, err)
	commitBody, err := backupartifact.MarshalErasureLedgerCommit(commit)
	require.NoError(t, err)
	commitDigest := sha256.Sum256(commitBody)
	commitSHA := hex.EncodeToString(commitDigest[:])
	for _, repository := range []backupartifact.Repository{primary, secondary} {
		putErasureAuditObject(
			t, repository, sealed.Entry.Key,
			sealed.Entry.CiphertextSHA256, sealed.Ciphertext,
		)
		putErasureAuditObject(
			t, repository, commit.RecordKey, recordSHA, recordBody,
		)
		putErasureAuditObject(
			t, repository, commitKey, commitSHA, commitBody,
		)
		putErasureAuditObject(
			t, repository,
			backupartifact.ErasureLedgerReceiptKey(eventID),
			commitSHA, commitBody,
		)
	}
	auditor, err := backupinfra.NewReplicatedErasureIntegrityAuditor(
		backupinfra.ReplicatedErasureIntegrityAuditorOptions{
			Primary: primary, Secondary: secondary,
			PrimaryRepair: primary, SecondaryRepair: secondary,
			Codec: codec, Signer: signer,
			RepositoryID:     repositoryID,
			SourceClusterID:  sourceClusterID,
			SourceGeneration: sourceGeneration,
			HashSlotCount:    hashSlotCount,
		},
	)
	require.NoError(t, err)
	targets := []backupinfra.ErasureIntegrityAuditTarget{
		{
			Kind:     backupinfra.ErasureIntegrityArtifactCommit,
			HashSlot: hashSlot, Sequence: 1,
			CommitKey: commitKey, ExpectedCommitSHA256: commitSHA,
		},
		{
			Kind:     backupinfra.ErasureIntegrityArtifactReceipt,
			HashSlot: hashSlot, Sequence: 1,
			CommitKey: commitKey, ExpectedCommitSHA256: commitSHA,
			EventID: eventID,
		},
		{
			Kind:     backupinfra.ErasureIntegrityArtifactRecord,
			HashSlot: hashSlot, Sequence: 1,
			CommitKey: commitKey, ExpectedCommitSHA256: commitSHA,
			EventID: eventID, RecordKey: commit.RecordKey,
			RecordSHA256: recordSHA,
		},
		{
			Kind:     backupinfra.ErasureIntegrityArtifactEvent,
			HashSlot: hashSlot, Sequence: 1,
			CommitKey: commitKey, ExpectedCommitSHA256: commitSHA,
			EventID: eventID, RecordKey: commit.RecordKey,
			RecordSHA256: recordSHA,
		},
	}
	for _, target := range targets {
		report, inspectErr := auditor.InspectErasureArtifactCopies(ctx, target)
		require.NoError(t, inspectErr)
		require.True(t, report.Copies[0].Healthy)
		require.True(t, report.Copies[1].Healthy)
	}

	receiptKey := backupartifact.ErasureLedgerReceiptKey(eventID)
	require.NoError(t, os.Remove(
		filepath.Join(secondaryDir, filepath.FromSlash(receiptKey)),
	))
	report, err := auditor.InspectErasureArtifactCopies(ctx, targets[1])
	require.NoError(t, err)
	require.True(t, report.Copies[0].Healthy)
	require.False(t, report.Copies[1].Healthy)
	require.Equal(
		t, backupartifact.SegmentCorruptionMissing,
		report.Copies[1].Category,
	)
	repairedBytes, err := auditor.RepairErasureArtifactCopy(
		ctx, targets[1], "secondary",
	)
	require.NoError(t, err)
	require.Equal(t, int64(len(commitBody)), repairedBytes)
	report, err = auditor.InspectErasureArtifactCopies(ctx, targets[1])
	require.NoError(t, err)
	require.True(t, report.Copies[0].Healthy)
	require.True(t, report.Copies[1].Healthy)

	require.NoError(t, os.Remove(
		filepath.Join(
			secondaryDir, filepath.FromSlash(sealed.Entry.Key),
		),
	))
	report, err = auditor.InspectErasureArtifactCopies(ctx, targets[3])
	require.NoError(t, err)
	require.True(t, report.Copies[0].Healthy)
	require.False(t, report.Copies[1].Healthy)
	repairedBytes, err = auditor.RepairErasureArtifactCopy(
		ctx, targets[3], "secondary",
	)
	require.NoError(t, err)
	require.Equal(t, int64(len(sealed.Ciphertext)), repairedBytes)

	require.NoError(t, os.Remove(
		filepath.Join(
			secondaryDir, filepath.FromSlash(commit.RecordKey),
		),
	))
	repairedBytes, err = auditor.RepairErasureArtifactCopy(
		ctx, targets[3], "secondary",
	)
	require.NoError(t, err)
	require.Equal(t, int64(len(recordBody)), repairedBytes)
	report, err = auditor.InspectErasureArtifactCopies(ctx, targets[3])
	require.NoError(t, err)
	require.True(t, report.Copies[0].Healthy)
	require.True(t, report.Copies[1].Healthy)
}

func putErasureAuditObject(
	t *testing.T,
	repository backupartifact.Repository,
	key, checksum string,
	body []byte,
) {
	t.Helper()
	require.NoError(t, repository.PutImmutable(
		context.Background(), key, int64(len(body)), checksum,
		bytes.NewReader(body),
	))
}
