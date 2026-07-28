package backup_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	"github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestFileArchiveStorePublishesListsAndDeletesBoundedPrefix(t *testing.T) {
	ctx := context.Background()
	store, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	objects := map[string][]byte{
		"backups/bk-1/manifest.json":             []byte("manifest"),
		"backups/bk-1/slots/000/meta-000001.zst": []byte("chunk"),
		"backups/bk-2/manifest.json":             []byte("other"),
	}
	for key, body := range objects {
		if err := store.Put(ctx, backup.PutObject{
			Key:           key,
			Body:          bytes.NewReader(body),
			ExpectedBytes: uint64(len(body)),
		}); err != nil {
			t.Fatalf("Put(%q): %v", key, err)
		}
	}

	reader, info, err := store.Open(ctx, "backups/bk-1/manifest.json")
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	body, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read/close = %v / %v", readErr, closeErr)
	}
	if string(body) != "manifest" || info.Bytes != uint64(len(body)) {
		t.Fatalf("object = %q, %#v", body, info)
	}

	items, err := store.List(ctx, "backups/bk-1")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(items) != 2 ||
		items[0].Key != "backups/bk-1/manifest.json" ||
		items[1].Key != "backups/bk-1/slots/000/meta-000001.zst" {
		t.Fatalf("items = %#v", items)
	}

	if err := store.DeletePrefix(ctx, "backups/bk-1"); err != nil {
		t.Fatalf("DeletePrefix(): %v", err)
	}
	if _, _, err := store.Open(ctx, "backups/bk-1/manifest.json"); !errors.Is(err, backup.ErrObjectNotFound) {
		t.Fatalf("Open(deleted) error = %v", err)
	}
	if _, _, err := store.Open(ctx, "backups/bk-2/manifest.json"); err != nil {
		t.Fatalf("Open(unrelated): %v", err)
	}
}

func TestFileArchiveStoreRejectsUnsafeKeysAndIfAbsentOverwrite(t *testing.T) {
	ctx := context.Background()
	store, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	if err := store.Put(ctx, backup.PutObject{
		Key: "../escape", Body: bytes.NewReader([]byte("x")), ExpectedBytes: 1,
	}); err == nil {
		t.Fatal("Put() accepted traversal")
	}

	request := backup.PutObject{
		Key: "repository.json", Body: bytes.NewReader([]byte("first")),
		ExpectedBytes: 5, IfAbsent: true,
	}
	if err := store.Put(ctx, request); err != nil {
		t.Fatalf("Put(first): %v", err)
	}
	request.Body = bytes.NewReader([]byte("other"))
	if err := store.Put(ctx, request); !errors.Is(err, backup.ErrObjectExists) {
		t.Fatalf("Put(overwrite) error = %v", err)
	}
}

func TestFileArchiveStoreRejectsIntermediateSymlinkEscape(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	outside := t.TempDir()
	store, err := backupinfra.NewFileArchiveStore(root)
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	outsideObject := filepath.Join(outside, "manifest.json")
	if err := os.WriteFile(outsideObject, []byte("outside"), 0o600); err != nil {
		t.Fatalf("WriteFile(outside): %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "backups")); err != nil {
		t.Fatalf("Symlink(): %v", err)
	}

	if err := store.Put(ctx, backup.PutObject{
		Key: "backups/new.json", Body: bytes.NewReader([]byte("new")),
		ExpectedBytes: 3,
	}); err == nil {
		t.Fatal("Put() followed an intermediate symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "new.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside write exists or stat failed: %v", err)
	}
	if _, _, err := store.Open(ctx, "backups/manifest.json"); err == nil {
		t.Fatal("Open() followed an intermediate symlink")
	}
	if err := store.Delete(ctx, "backups/manifest.json"); err == nil {
		t.Fatal("Delete() followed an intermediate symlink")
	}
	body, err := os.ReadFile(outsideObject)
	if err != nil || string(body) != "outside" {
		t.Fatalf("outside object changed: body=%q error=%v", body, err)
	}
	if _, err := store.List(ctx, "backups"); err == nil {
		t.Fatal("List() followed an intermediate symlink")
	}
}
