//go:build integration

package meta

import (
	"context"
	"io"
	"runtime"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

// Closing a snapshot stream must complete ownership of its pinned view before
// an offline caller closes the read-only database, including an unread stream.
func TestHashSlotSnapshotStreamCloseReleasesOwnedView(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for _, backup := range []bool{false, true} {
		mode := "ordinary"
		if backup {
			mode = "backup"
		}
		for _, boundary := range []string{"unread", "blocked_header", "eof", "eof_before_close", "canceled"} {
			t.Run(mode+"/"+boundary, func(t *testing.T) {
				db, physical := openSnapshotLifecycleDB(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				reader := openLifecycleSnapshot(t, db, ctx, backup)
				defer reader.Close()
				switch boundary {
				case "blocked_header", "canceled":
					// A one-byte read leaves the producer's multi-byte header write
					// blocked, proving that it still owns the pinned snapshot.
					var first [1]byte
					if _, err := io.ReadFull(reader, first[:]); err != nil {
						t.Fatalf("ReadFull(header byte): %v", err)
					}
					if boundary == "canceled" {
						cancel()
					}
				case "eof", "eof_before_close":
					if _, err := io.Copy(io.Discard, reader); err != nil {
						t.Fatalf("Copy(snapshot): %v", err)
					}
				}
				if boundary == "eof_before_close" {
					// EOF itself must follow view release, independently of
					// whether the consumer has called stream Close yet.
					if err := physical.Close(); err != nil {
						t.Errorf("Close(database) after stream EOF before stream Close: %v", err)
					}
					return
				}
				if err := reader.Close(); err != nil {
					t.Fatalf("Close(snapshot): %v", err)
				}
				if err := physical.Close(); err != nil {
					t.Errorf("Close(database) after completed stream Close: %v", err)
				}
			})
		}
	}
}

// Every concurrent closer must join the same owner completion, and a later
// repeated Close must not attempt to close the physical snapshot again.
func TestHashSlotSnapshotStreamConcurrentCloseReleasesOwnedView(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for _, backup := range []bool{false, true} {
		mode := "ordinary"
		if backup {
			mode = "backup"
		}
		t.Run(mode, func(t *testing.T) {
			db, physical := openSnapshotLifecycleDB(t)
			reader := openLifecycleSnapshot(t, db, context.Background(), backup)
			defer reader.Close()
			var first [1]byte
			if _, err := io.ReadFull(reader, first[:]); err != nil {
				t.Fatalf("ReadFull(header byte): %v", err)
			}
			var callers sync.WaitGroup
			errors := make(chan error, 8)
			for range 8 {
				callers.Go(func() { errors <- reader.Close() })
			}
			callers.Wait()
			close(errors)
			for err := range errors {
				if err != nil {
					t.Errorf("concurrent Close(snapshot): %v", err)
				}
			}
			if err := reader.Close(); err != nil {
				t.Errorf("repeated Close(snapshot): %v", err)
			}
			if err := physical.Close(); err != nil {
				t.Errorf("Close(database) after all stream closers: %v", err)
			}
		})
	}
}

func openSnapshotLifecycleDB(t *testing.T) (*MetaDB, *engine.DB) {
	t.Helper()
	dir := t.TempDir()
	initial, err := engine.Open(dir, engine.Options{})
	if err != nil {
		t.Fatalf("Open(initial database): %v", err)
	}
	if err := initial.Close(); err != nil {
		t.Fatalf("Close(initial database): %v", err)
	}
	physical, err := engine.Open(dir, engine.Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("Open(read-only database): %v", err)
	}
	db := NewDB(physical)
	t.Cleanup(func() {
		db.close()
		if err := physical.Close(); err != nil {
			t.Errorf("cleanup Close(database): %v", err)
		}
	})
	return db, physical
}

func openLifecycleSnapshot(t *testing.T, db *MetaDB, ctx context.Context, backup bool) io.ReadCloser {
	t.Helper()
	var reader io.ReadCloser
	var err error
	if backup {
		reader, err = db.OpenBackupHashSlotSnapshot(ctx, []uint16{5})
	} else {
		reader, err = db.OpenHashSlotSnapshot(ctx, []uint16{5})
	}
	if err != nil {
		t.Fatalf("Open(snapshot): %v", err)
	}
	return reader
}
