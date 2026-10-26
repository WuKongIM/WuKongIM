//go:build integration

package message

import (
	"context"
	"io"
	"runtime"
	"testing"
)

// A completed backup stream Close must release the owned snapshot before the
// caller closes its MessageDB, including a producer blocked on its header.
func TestMessageBackupStreamCloseReleasesOwnedView(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for _, withStats := range []bool{false, true} {
		mode := "ordinary"
		if withStats {
			mode = "with_stats"
		}
		for _, boundary := range []string{"blocked_header", "eof", "eof_before_close"} {
			t.Run(mode+"/"+boundary, func(t *testing.T) {
				store := openTestMessageStore(t)
				defer store.close(t)
				request := BackupSnapshotRequest{HashSlot: 5}
				var reader io.ReadCloser
				var err error
				if withStats {
					reader, _, err = store.db.OpenBackupSnapshotWithStats(context.Background(), request)
				} else {
					reader, err = store.db.OpenBackupSnapshot(context.Background(), request)
				}
				if err != nil {
					t.Fatalf("Open(backup snapshot): %v", err)
				}
				defer reader.Close()
				if boundary == "blocked_header" {
					// The header is longer than one byte, so the producer still
					// owns its view while its pipe Write waits for more reads.
					var first [1]byte
					if _, err := io.ReadFull(reader, first[:]); err != nil {
						t.Fatalf("ReadFull(header byte): %v", err)
					}
				} else if _, err := io.Copy(io.Discard, reader); err != nil {
					t.Fatalf("Copy(backup snapshot): %v", err)
				}
				if boundary != "eof_before_close" {
					if err := reader.Close(); err != nil {
						t.Fatalf("Close(backup snapshot): %v", err)
					}
				}
				err = store.db.Close()
				// Close is terminal even when its native leak check rejects;
				// avoid reporting that same preserved result twice in cleanup.
				store.db, store.engine = nil, nil
				if err != nil {
					t.Errorf("Close(MessageDB) at %s boundary: %v", boundary, err)
				}
			})
		}
	}
}
