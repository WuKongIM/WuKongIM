//go:build integration

package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

// Rejecting an invalid capacity must not leave a partially opened store or FS.
func TestEngineRejectsUnrepresentableCacheBudgetBeforeCreatingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid-budget")
	db, err := Open(path, Options{CacheSize: int64(^uint(0) >> 1), MemTableSize: 1})
	if db != nil {
		_ = db.Close()
		t.Fatal("invalid budget opened an engine")
	}
	if !errors.Is(err, dberrors.ErrInvalidArgument) {
		t.Fatalf("Open error = %v, want invalid argument", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid budget created storage: %v", err)
	}
}
