package db_test

import (
	"errors"
	"fmt"
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
