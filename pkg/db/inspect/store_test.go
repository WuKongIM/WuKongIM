package inspect

import (
	"errors"
	"testing"

	db "github.com/WuKongIM/WuKongIM/pkg/db"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

func TestOpenStoreRequiresPath(t *testing.T) {
	store, err := OpenStore(Options{})
	if store != nil {
		t.Fatalf("OpenStore() store = %#v, want nil", store)
	}
	if !errors.Is(err, db.ErrInvalidArgument) {
		t.Fatalf("OpenStore() err = %v, want ErrInvalidArgument", err)
	}
}

func TestOpenStoreReadOnlyDoesNotAllowWrites(t *testing.T) {
	path := seedEngine(t)

	store, err := OpenStore(Options{MetaPath: path})
	if err != nil {
		t.Fatalf("OpenStore() err = %v", err)
	}
	defer store.Close()

	batch := store.metaEngine.NewBatch()
	defer batch.Close()
	if err := batch.Set([]byte("readonly-key"), []byte("readonly-value")); err != nil {
		t.Fatalf("Set() err = %v", err)
	}
	if err := batch.Commit(false); err == nil {
		t.Fatal("Commit() err = nil, want read-only failure")
	}
}

func TestOpenStoreClosesPartialOpenOnMessageFailure(t *testing.T) {
	metaPath := seedEngine(t)

	store, err := OpenStore(Options{
		MetaPath:    metaPath,
		MessagePath: t.TempDir() + "/missing-message",
	})
	if store != nil {
		t.Fatalf("OpenStore() store = %#v, want nil", store)
	}
	if err == nil {
		t.Fatal("OpenStore() err = nil, want message open failure")
	}

	reopened, err := engine.Open(metaPath, engine.Options{})
	if err != nil {
		t.Fatalf("engine.Open() after failed OpenStore err = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("Close() reopened err = %v", err)
	}
}

func seedEngine(t *testing.T) string {
	t.Helper()

	path := t.TempDir()
	eng, err := engine.Open(path, engine.Options{})
	if err != nil {
		t.Fatalf("engine.Open() err = %v", err)
	}
	batch := eng.NewBatch()
	if err := batch.Set([]byte("seed-key"), []byte("seed-value")); err != nil {
		t.Fatalf("Set() err = %v", err)
	}
	if err := batch.Commit(false); err != nil {
		t.Fatalf("Commit() err = %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("Batch Close() err = %v", err)
	}
	if err := eng.Close(); err != nil {
		t.Fatalf("Engine Close() err = %v", err)
	}
	return path
}
