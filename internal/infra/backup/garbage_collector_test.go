package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestRestorePointGarbageCollectorMarksRetainedGraphsAndSweepsBothRepositories(t *testing.T) {
	primaryDirectory := t.TempDir()
	secondaryDirectory := t.TempDir()
	primary, err := backupinfra.NewFileRepository("primary", primaryDirectory)
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", secondaryDirectory)
	require.NoError(t, err)
	primaryGarbage, err := backupinfra.NewFileRepository("primary", primaryDirectory)
	require.NoError(t, err)
	secondaryGarbage, err := backupinfra.NewFileRepository("secondary", secondaryDirectory)
	require.NoError(t, err)
	signer := restoreInspectorSigner()
	codec := restoreInspectorCodec()
	publishDirectRestorePoint(t, primary, secondary, signer, "restore-retained", 1710000000000, 10)
	publishDirectRestorePoint(t, primary, secondary, signer, "restore-expired", 1710000001000, 11)

	retained := backupusecase.RestorePoint{ID: "restore-retained", ManifestSHA256: repositoryChecksum(t, primary, "restore-points/restore-retained/manifest.json")}
	pending := backupusecase.RestorePoint{ID: "restore-expired", ManifestSHA256: repositoryChecksum(t, primary, "restore-points/restore-expired/manifest.json")}
	for _, repository := range []backupartifact.Repository{primary, secondary} {
		putGarbageTestObject(t, repository, "objects/orphan-job/00000/metadata-000000.bin", []byte("orphan"))
		putGarbageTestObject(t, repository, "objects/active-job/00000/metadata-000000.bin", []byte("active"))
	}
	coordination, err := backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: 1, Store: &erasureLedgerStateStore{}, Publisher: erasureLedgerNoopPublisher{},
		Now: func() time.Time { return time.UnixMilli(1710000002000) }, NewJobID: func() string { return "unused" },
	})
	require.NoError(t, err)
	erasureLedger, err := backupinfra.NewPermanentErasureLedger(backupinfra.PermanentErasureLedgerOptions{
		Primary: primary, Secondary: secondary, Codec: codec, Coordinator: coordination, Signer: signer,
		SigningKeyID: "signing-key", KMSKeyID: "kms-backup", RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: "generation-1", HashSlotCount: 1,
		Now: func() time.Time { return time.UnixMilli(1710000002000) }, NewAttemptID: func() string { return "gc-ledger" },
	})
	require.NoError(t, err)
	_, err = erasureLedger.RecordPermanentMessageErasure(context.Background(), backupinfra.PermanentMessageErasure{ChannelID: "room", ChannelType: 2, ThroughSeq: 3, RequestedAtUnixMillis: 1710000002000})
	require.NoError(t, err)
	loader, err := backupinfra.NewErasureLedgerLoader(backupinfra.ErasureLedgerLoaderOptions{
		Primary: primary, Secondary: secondary, Signer: signer, Codec: codec, RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: "generation-1", HashSlotCount: 1,
	})
	require.NoError(t, err)
	ledgerSnapshot, err := loader.LoadDualSnapshot(context.Background())
	require.NoError(t, err)
	collector, err := backupinfra.NewRestorePointGarbageCollector(backupinfra.RestorePointGarbageCollectorOptions{
		Primary: primaryGarbage, Secondary: secondaryGarbage, PrimaryRepository: "primary", SecondaryRepository: "secondary", Signer: signer, Codec: codec,
		RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: "generation-1", HashSlotCount: 1,
		MinimumAge: 7 * 24 * time.Hour, MaxDeletesPerRepository: 64,
		Now: func() time.Time { return time.Now().UTC().Add(8 * 24 * time.Hour) },
	})
	require.NoError(t, err)

	result, err := collector.Collect(context.Background(), []backupusecase.RestorePoint{retained}, []backupusecase.RestorePoint{pending}, &backupusecase.Job{ID: "active-job"}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"restore-expired"}, result.CompletedRestorePointIDs)
	require.GreaterOrEqual(t, result.DeletedObjects, 6)
	for _, repository := range []backupartifact.Repository{primary, secondary} {
		_, err = backupartifact.LoadRestorePoint(context.Background(), repository, retained.ID, signer)
		require.NoError(t, err)
		_, err = repository.Stat(context.Background(), "restore-points/restore-expired/manifest.json")
		require.ErrorIs(t, err, backupartifact.ErrObjectNotFound)
		_, err = repository.Stat(context.Background(), "objects/orphan-job/00000/metadata-000000.bin")
		require.ErrorIs(t, err, backupartifact.ErrObjectNotFound)
		_, err = repository.Stat(context.Background(), "objects/active-job/00000/metadata-000000.bin")
		require.NoError(t, err)
		for _, key := range ledgerSnapshot.Keys {
			_, err = repository.Stat(context.Background(), key)
			require.NoError(t, err, key)
		}
	}
}

func TestRestorePointGarbageCollectorFailsClosedWhenRetainedGraphIsMissing(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	collector, err := backupinfra.NewRestorePointGarbageCollector(backupinfra.RestorePointGarbageCollectorOptions{
		Primary: primary, Secondary: secondary, Signer: restoreInspectorSigner(), Codec: restoreInspectorCodec(), MinimumAge: 7 * 24 * time.Hour,
		RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: "generation-1", HashSlotCount: 1,
	})
	require.NoError(t, err)
	_, err = collector.Collect(context.Background(), []backupusecase.RestorePoint{{ID: "missing", ManifestSHA256: hex.EncodeToString(make([]byte, sha256.Size))}}, nil, nil, nil)
	require.Error(t, err)
	require.False(t, errors.Is(err, context.Canceled))
}

func TestRestorePointGarbageCollectorProtectsAuthenticatedPendingErasure(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	signer := restoreInspectorSigner()
	codec := restoreInspectorCodec()
	store := &erasureLedgerStateStore{}
	coordination, err := backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: 1, Store: store, Publisher: erasureLedgerNoopPublisher{},
		Now: func() time.Time { return time.UnixMilli(1710000002000) }, NewJobID: func() string { return "unused" },
	})
	require.NoError(t, err)
	ledger, err := backupinfra.NewPermanentErasureLedger(backupinfra.PermanentErasureLedgerOptions{
		Primary: primary, Secondary: secondary, Codec: codec, Coordinator: coordination, Signer: signer,
		SigningKeyID: "signing-key", KMSKeyID: "kms-backup", RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: "generation-1", HashSlotCount: 1,
		Now: func() time.Time { return time.UnixMilli(1710000002000) }, NewAttemptID: func() string { return "pending-ledger" },
	})
	require.NoError(t, err)
	receipt, err := ledger.RecordPermanentMessageErasure(context.Background(), backupinfra.PermanentMessageErasure{
		ChannelID: "pending-room", ChannelType: 2, ThroughSeq: 3, RequestedAtUnixMillis: 1710000002000,
	})
	require.NoError(t, err)
	commitBody := readRepositoryBody(t, primary, backupartifact.ErasureLedgerCommitKey(receipt.Sequence))
	commit, err := backupartifact.LoadErasureLedgerCommit(context.Background(), commitBody, signer)
	require.NoError(t, err)
	recordBody := readRepositoryBody(t, primary, commit.RecordKey)
	record, err := backupartifact.LoadErasureLedgerRecord(context.Background(), recordBody, signer)
	require.NoError(t, err)
	pending := backupusecase.ErasureLedgerRecordReference{
		Sequence: commit.Sequence, EventID: commit.EventID, RecordKey: commit.RecordKey, RecordSHA256: commit.RecordSHA256,
	}
	for _, repository := range []*backupinfra.FileRepository{primary, secondary} {
		require.NoError(t, repository.DeleteGarbageObject(context.Background(), backupartifact.ErasureLedgerCommitKey(commit.Sequence)))
		require.NoError(t, repository.DeleteGarbageObject(context.Background(), backupartifact.ErasureLedgerReceiptKey(commit.EventID)))
		putGarbageTestObject(t, repository, "objects/uncommitted-orphan/old.wkb", []byte("orphan"))
	}
	collector, err := backupinfra.NewRestorePointGarbageCollector(backupinfra.RestorePointGarbageCollectorOptions{
		Primary: primary, Secondary: secondary, Signer: signer, Codec: codec,
		RepositoryID: "repo-prod", SourceClusterID: "cluster-a", SourceGeneration: "generation-1", HashSlotCount: 1,
		MinimumAge: 7 * 24 * time.Hour, Now: func() time.Time { return time.Now().UTC().Add(8 * 24 * time.Hour) },
	})
	require.NoError(t, err)
	_, err = collector.Collect(context.Background(), nil, nil, nil, &pending)
	require.NoError(t, err)
	for _, repository := range []backupartifact.Repository{primary, secondary} {
		_, err = repository.Stat(context.Background(), commit.RecordKey)
		require.NoError(t, err)
		_, err = repository.Stat(context.Background(), record.Object.Key)
		require.NoError(t, err)
		_, err = repository.Stat(context.Background(), "objects/uncommitted-orphan/old.wkb")
		require.ErrorIs(t, err, backupartifact.ErrObjectNotFound)
	}
}

func repositoryChecksum(t *testing.T, repository backupartifact.Repository, key string) string {
	t.Helper()
	object, err := repository.Stat(context.Background(), key)
	require.NoError(t, err)
	return object.SHA256
}

func putGarbageTestObject(t *testing.T, repository backupartifact.Repository, key string, body []byte) {
	t.Helper()
	hash := sha256.Sum256(body)
	require.NoError(t, repository.PutImmutable(context.Background(), key, int64(len(body)), hex.EncodeToString(hash[:]), bytes.NewReader(body)))
}
