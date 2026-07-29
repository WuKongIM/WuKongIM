package backup_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestRepositoryProviderResolvesSharedFileRootAliasOnce(t *testing.T) {
	dataDir := t.TempDir()
	sharedRoot := t.TempDir()
	requireNoError(t, os.Symlink(
		sharedRoot, filepath.Join(dataDir, "backup-repository"),
	))
	provider, err := backupinfra.NewRepositoryProvider(dataDir, nil)
	requireNoError(t, err)
	store, err := provider.Open(
		context.Background(),
		backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
	)
	requireNoError(t, err)
	body := []byte("shared")
	requireNoError(t, store.Put(context.Background(), backupartifact.PutObject{
		Key: "probes/shared", Body: bytes.NewReader(body),
		ExpectedBytes: uint64(len(body)), IfAbsent: true,
	}))
	requireNoError(t, os.Remove(filepath.Join(dataDir, "backup-repository")))
	reader, _, err := store.Open(context.Background(), "probes/shared")
	requireNoError(t, err)
	requireNoError(t, reader.Close())
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
