package message

import (
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

func TestCommitObserverAdapterPreservesNativeStatsValue(t *testing.T) {
	observer := &recordingCommitObserver{}
	adapter := commitObserverAdapter{observer: observer}
	failure := errors.New("publication error")
	event := commit.BatchEvent{Requests: 3, Records: 4, Bytes: 80, TotalDuration: 10 * time.Millisecond, Err: failure,
		Native: engine.NativeCommitStats{Available: true, TotalDuration: 70 * time.Millisecond, SemaphoreWaitDuration: time.Millisecond, WALQueueWaitDuration: 2 * time.Millisecond, MemTableWriteStallDuration: 3 * time.Millisecond, L0ReadAmpWriteStallDuration: 4 * time.Millisecond, WALRotationDuration: 5 * time.Millisecond, CommitWaitDuration: 6 * time.Millisecond},
	}
	adapter.ObserveBatch(event)
	want := NativeCommitStats{Available: true, TotalDuration: 70 * time.Millisecond, SemaphoreWaitDuration: time.Millisecond, WALQueueWaitDuration: 2 * time.Millisecond, MemTableWriteStallDuration: 3 * time.Millisecond, L0ReadAmpWriteStallDuration: 4 * time.Millisecond, WALRotationDuration: 5 * time.Millisecond, CommitWaitDuration: 6 * time.Millisecond}
	if observer.batch.Native != want || observer.batch.TotalDuration != 10*time.Millisecond || !errors.Is(observer.batch.Err, failure) {
		t.Fatalf("adapter changed observation: %+v", observer.batch)
	}
	event.Native = engine.NativeCommitStats{}
	if observer.batch.Native != want {
		t.Fatal("published native snapshot aliased source event")
	}
	adapter.ObserveBatch(event)
	if observer.batch.Native.Available {
		t.Fatal("unavailable event borrowed previous native stats")
	}
}
