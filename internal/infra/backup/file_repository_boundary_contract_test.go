package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestFileArchiveStoreRejectsUnsafeRootsAndPartialPublications(t *testing.T) {
	if _, err := NewFileArchiveStore(string(filepath.Separator)); err == nil {
		t.Fatal("NewFileArchiveStore(filesystem root) error = nil")
	}
	regular := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(regular, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile(root): %v", err)
	}
	if _, err := NewFileArchiveStore(regular); err == nil {
		t.Fatal("NewFileArchiveStore(regular file) error = nil")
	}
	link := filepath.Join(t.TempDir(), "repository-link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Fatalf("Symlink(root): %v", err)
	}
	if _, err := NewFileArchiveStore(link); err == nil {
		t.Fatal("NewFileArchiveStore(symlink) error = nil")
	}

	root := t.TempDir()
	store, err := NewFileArchiveStore(root)
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Put(canceled, backupartifact.PutObject{
		Key: "canceled", Body: strings.NewReader("x"), ExpectedBytes: 1,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put(canceled) error = %v", err)
	}
	if err := store.Put(context.Background(), backupartifact.PutObject{
		Key: "nil-body", ExpectedBytes: 1,
	}); !errors.Is(err, backupartifact.ErrInvalidObject) {
		t.Fatalf("Put(nil body) error = %v", err)
	}
	for _, object := range []backupartifact.PutObject{
		{Key: "short", Body: strings.NewReader("abc"), ExpectedBytes: 4},
		{Key: "long", Body: strings.NewReader("abcd"), ExpectedBytes: 3},
		{Key: "read-error", Body: &failingArchiveReader{}, ExpectedBytes: 2},
	} {
		if err := store.Put(context.Background(), object); err == nil {
			t.Fatalf("Put(%s) error = nil", object.Key)
		}
		if _, _, err := store.Open(
			context.Background(), object.Key,
		); !errors.Is(err, backupartifact.ErrObjectNotFound) {
			t.Fatalf("Open(%s) error = %v, want absent", object.Key, err)
		}
	}

	copyCtx, cancelCopy := context.WithCancel(context.Background())
	reader := &cancelingArchiveReader{cancel: cancelCopy}
	if err := store.Put(copyCtx, backupartifact.PutObject{
		Key: "mid-copy-cancel", Body: reader, ExpectedBytes: 256 << 10,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Put(mid-copy cancellation) error = %v", err)
	}
	if _, _, err := store.Open(
		context.Background(), "mid-copy-cancel",
	); !errors.Is(err, backupartifact.ErrObjectNotFound) {
		t.Fatalf("Open(mid-copy cancellation) error = %v", err)
	}
}

func TestFileArchiveStoreReadAndDeleteBoundariesFailClosed(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileArchiveStore(root)
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	if err := store.Put(context.Background(), backupartifact.PutObject{
		Key: "objects/value", Body: bytes.NewReader([]byte("value")),
		ExpectedBytes: 5,
	}); err != nil {
		t.Fatalf("Put(value): %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := store.Open(canceled, "objects/value"); !errors.Is(
		err, context.Canceled,
	) {
		t.Fatalf("Open(canceled) error = %v", err)
	}
	if err := store.Delete(canceled, "objects/value"); !errors.Is(
		err, context.Canceled,
	) {
		t.Fatalf("Delete(canceled) error = %v", err)
	}
	if err := store.Delete(context.Background(), "objects/missing"); err != nil {
		t.Fatalf("Delete(missing): %v", err)
	}
	if items, err := store.List(context.Background(), "objects/missing"); err != nil || len(items) != 0 {
		t.Fatalf("List(missing) = %+v, %v", items, err)
	}
	if _, err := store.List(context.Background(), "../escape"); !errors.Is(
		err, backupartifact.ErrInvalidObject,
	) {
		t.Fatalf("List(unsafe) error = %v", err)
	}
	if err := store.DeletePrefix(context.Background(), "../escape"); !errors.Is(
		err, backupartifact.ErrInvalidObject,
	) {
		t.Fatalf("DeletePrefix(unsafe) error = %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "objects", "directory"), 0o700); err != nil {
		t.Fatalf("MkdirAll(directory object): %v", err)
	}
	if _, _, err := store.Open(
		context.Background(), "objects/directory",
	); !errors.Is(err, backupartifact.ErrInvalidObject) {
		t.Fatalf("Open(directory object) error = %v", err)
	}
	if _, err := store.List(canceled, "objects"); !errors.Is(err, context.Canceled) {
		t.Fatalf("List(canceled) error = %v", err)
	}
}

func TestRepositoryProviderRejectsUnavailableOrUnsealedStores(t *testing.T) {
	if _, err := NewRepositoryProvider("", nil); err == nil {
		t.Fatal("NewRepositoryProvider(empty) error = nil")
	}
	dataFile := filepath.Join(t.TempDir(), "data-file")
	if err := os.WriteFile(dataFile, []byte("data"), 0o600); err != nil {
		t.Fatalf("WriteFile(data): %v", err)
	}
	if _, err := NewRepositoryProvider(dataFile, nil); err == nil {
		t.Fatal("NewRepositoryProvider(file) error = nil")
	}
	var unavailable *RepositoryProvider
	if _, err := unavailable.Open(
		context.Background(),
		backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
	); err == nil {
		t.Fatal("nil provider Open() error = nil")
	}
	provider, err := NewRepositoryProvider(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	if _, err := provider.Open(context.Background(), backupcontract.StoreConfig{
		Kind: backupcontract.StoreKindS3,
	}); err == nil {
		t.Fatal("Open(object store without cipher) error = nil")
	}
	if _, err := provider.Open(context.Background(), backupcontract.StoreConfig{
		Kind: backupcontract.StoreKind("unsupported"),
	}); err == nil {
		t.Fatal("Open(unsupported) error = nil")
	}
	if _, err := provider.SealObjectStoreCredentials("access", "secret"); err == nil {
		t.Fatal("SealObjectStoreCredentials(without cipher) error = nil")
	}
}

type failingArchiveReader struct {
	read bool
}

func (r *failingArchiveReader) Read(body []byte) (int, error) {
	if !r.read {
		r.read = true
		body[0] = 'x'
		return 1, nil
	}
	return 0, io.ErrUnexpectedEOF
}

type cancelingArchiveReader struct {
	cancel context.CancelFunc
	read   bool
}

func (r *cancelingArchiveReader) Read(body []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	body[0] = 'x'
	r.cancel()
	return 1, nil
}
