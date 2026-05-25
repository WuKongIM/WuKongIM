package db_test

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db"
)

func TestDefaultOptionsFillPathsAndDurability(t *testing.T) {
	opts := db.DefaultNodeStoreOptions(t.TempDir())
	if opts.MessagePath == "" || opts.MetaPath == "" {
		t.Fatalf("paths not filled: %#v", opts)
	}
	if opts.Commit.FlushWindow <= 0 {
		t.Fatalf("flush window not filled: %#v", opts.Commit)
	}
}

func TestErrorsSupportErrorsIs(t *testing.T) {
	if !errors.Is(fmt.Errorf("wrap: %w", db.ErrNotFound), db.ErrNotFound) {
		t.Fatal("ErrNotFound does not support errors.Is")
	}
}

func TestOpenNodeStoreCreatesPhysicalStores(t *testing.T) {
	opts := db.DefaultNodeStoreOptions(t.TempDir())
	store, err := db.OpenNodeStore(opts)
	if err != nil {
		t.Fatalf("OpenNodeStore(): %v", err)
	}
	defer store.Close()
	for _, path := range []string{opts.MessagePath, opts.MetaPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
	}
}

func TestOpenNodeStoreExposesMetaDomain(t *testing.T) {
	opts := db.DefaultNodeStoreOptions(t.TempDir())
	store, err := db.OpenNodeStore(opts)
	if err != nil {
		t.Fatalf("OpenNodeStore(): %v", err)
	}
	defer store.Close()
	if store.Meta() == nil {
		t.Fatal("Meta() = nil")
	}
}
