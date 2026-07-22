package backup_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestFileRepositoryPutImmutableAndOpen(t *testing.T) {
	repository, err := backupinfra.NewFileRepository("primary-dev", t.TempDir())
	require.NoError(t, err)
	body := []byte("encrypted-backup-object")
	hash := sha256.Sum256(body)
	checksum := hex.EncodeToString(hash[:])

	err = repository.PutImmutable(context.Background(), "objects/job-1/00001/metadata-000000.bin", int64(len(body)), checksum, bytes.NewReader(body))
	require.NoError(t, err)
	err = repository.PutImmutable(context.Background(), "objects/job-1/00001/metadata-000000.bin", int64(len(body)), checksum, bytes.NewReader(body))
	require.ErrorIs(t, err, backupartifact.ErrObjectExists)

	reader, object, err := repository.Open(context.Background(), "objects/job-1/00001/metadata-000000.bin")
	require.NoError(t, err)
	defer reader.Close()
	loaded, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, body, loaded)
	require.Equal(t, int64(len(body)), object.Size)
	require.Equal(t, checksum, object.SHA256)
}

func TestFileRepositoryRejectsMismatchWithoutPublishing(t *testing.T) {
	repository, err := backupinfra.NewFileRepository("primary-dev", t.TempDir())
	require.NoError(t, err)
	key := "objects/job-1/00001/metadata-000000.bin"

	err = repository.PutImmutable(context.Background(), key, 4, "bad", bytes.NewReader([]byte("data")))
	require.Error(t, err)
	_, err = repository.Stat(context.Background(), key)
	require.True(t, errors.Is(err, backupartifact.ErrObjectNotFound))
}

func TestFileRepositoryListsOnlyCommittedRestorePoints(t *testing.T) {
	repository, err := backupinfra.NewFileRepository("primary-dev", t.TempDir())
	require.NoError(t, err)
	body := []byte("staged")
	hash := sha256.Sum256(body)
	checksum := hex.EncodeToString(hash[:])
	require.NoError(t, repository.PutImmutable(context.Background(), "restore-points/rp-staged/manifest.json", int64(len(body)), checksum, bytes.NewReader(body)))

	ids, err := repository.ListRestorePointIDs(context.Background())
	require.NoError(t, err)
	require.Empty(t, ids)

	require.NoError(t, repository.PutImmutable(context.Background(), "restore-points/rp-staged/published.json", int64(len(body)), checksum, bytes.NewReader(body)))
	ids, err = repository.ListRestorePointIDs(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"rp-staged"}, ids)
}

func TestFileRepositoryDeletesOnlyExplicitGarbageObject(t *testing.T) {
	repository, err := backupinfra.NewFileRepository("primary-dev", t.TempDir())
	require.NoError(t, err)
	body := []byte("expired-object")
	hash := sha256.Sum256(body)
	key := "objects/expired/00001.bin"
	require.NoError(t, repository.PutImmutable(context.Background(), key, int64(len(body)), hex.EncodeToString(hash[:]), bytes.NewReader(body)))
	require.NoError(t, repository.DeleteGarbageObject(context.Background(), key))
	_, err = repository.Stat(context.Background(), key)
	require.ErrorIs(t, err, backupartifact.ErrObjectNotFound)
	require.NoError(t, repository.DeleteGarbageObject(context.Background(), key))
}

func TestFileRepositoryCheckProvesWritableRootWithoutLeavingProbe(t *testing.T) {
	root := t.TempDir()
	repository, err := backupinfra.NewFileRepository("primary-dev", root)
	require.NoError(t, err)

	require.NoError(t, repository.Check(context.Background()))
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, repository.Check(ctx), context.Canceled)
}
