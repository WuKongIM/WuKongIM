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
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	signer := restoreInspectorSigner()
	publishDirectRestorePoint(t, primary, secondary, signer, "restore-retained", 1710000000000, 10)
	publishDirectRestorePoint(t, primary, secondary, signer, "restore-expired", 1710000001000, 11)

	retained := backupusecase.RestorePoint{ID: "restore-retained", ManifestSHA256: repositoryChecksum(t, primary, "restore-points/restore-retained/manifest.json")}
	pending := backupusecase.RestorePoint{ID: "restore-expired", ManifestSHA256: repositoryChecksum(t, primary, "restore-points/restore-expired/manifest.json")}
	for _, repository := range []backupartifact.Repository{primary, secondary} {
		putGarbageTestObject(t, repository, "objects/orphan-job/00000/metadata-000000.bin", []byte("orphan"))
		putGarbageTestObject(t, repository, "objects/active-job/00000/metadata-000000.bin", []byte("active"))
	}
	collector, err := backupinfra.NewRestorePointGarbageCollector(backupinfra.RestorePointGarbageCollectorOptions{
		Primary: primary, Secondary: secondary, Signer: signer,
		MinimumAge: 7 * 24 * time.Hour, MaxDeletesPerRepository: 64,
		Now: func() time.Time { return time.Now().UTC().Add(8 * 24 * time.Hour) },
	})
	require.NoError(t, err)

	result, err := collector.Collect(context.Background(), []backupusecase.RestorePoint{retained}, []backupusecase.RestorePoint{pending}, &backupusecase.Job{ID: "active-job"})
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
	}
}

func TestRestorePointGarbageCollectorFailsClosedWhenRetainedGraphIsMissing(t *testing.T) {
	primary, err := backupinfra.NewFileRepository("primary", t.TempDir())
	require.NoError(t, err)
	secondary, err := backupinfra.NewFileRepository("secondary", t.TempDir())
	require.NoError(t, err)
	collector, err := backupinfra.NewRestorePointGarbageCollector(backupinfra.RestorePointGarbageCollectorOptions{
		Primary: primary, Secondary: secondary, Signer: restoreInspectorSigner(), MinimumAge: 7 * 24 * time.Hour,
	})
	require.NoError(t, err)
	_, err = collector.Collect(context.Background(), []backupusecase.RestorePoint{{ID: "missing", ManifestSHA256: hex.EncodeToString(make([]byte, sha256.Size))}}, nil, nil)
	require.Error(t, err)
	require.False(t, errors.Is(err, context.Canceled))
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
