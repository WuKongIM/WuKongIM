package backup_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestArchiveFinalizerPrunesExpiredIncompleteBackup(t *testing.T) {
	root := t.TempDir()
	store, err := backupinfra.NewFileArchiveStore(root)
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	for key, body := range map[string][]byte{
		"pending/orphan":                   []byte("1"),
		"backups/orphan/slots/000/partial": []byte("partial"),
	} {
		if err := store.Put(context.Background(), backupartifact.PutObject{
			Key: key, Body: bytes.NewReader(body), ExpectedBytes: uint64(len(body)),
		}); err != nil {
			t.Fatalf("Put(%s): %v", key, err)
		}
	}
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-73 * time.Hour)
	if err := os.Chtimes(
		filepath.Join(root, "pending", "orphan"), expired, expired,
	); err != nil {
		t.Fatalf("Chtimes(): %v", err)
	}
	finalizer, err := backupinfra.NewArchiveFinalizer(
		backupinfra.ArchiveFinalizerOptions{
			ClusterID: "cluster-a", Application: "test",
			Now: func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatalf("NewArchiveFinalizer(): %v", err)
	}
	if err := finalizer.ApplyRetention(
		context.Background(), store, 7,
	); err != nil {
		t.Fatalf("ApplyRetention(): %v", err)
	}
	for _, prefix := range []string{"pending", "backups/orphan"} {
		objects, err := store.List(context.Background(), prefix)
		if err != nil {
			t.Fatalf("List(%s): %v", prefix, err)
		}
		if len(objects) != 0 {
			t.Fatalf("%s objects remain: %#v", prefix, objects)
		}
	}
}
