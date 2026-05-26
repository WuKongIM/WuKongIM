package message

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestCheckpointLoadStore(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	_, ok, err := log.LoadCheckpoint(context.Background())
	if err != nil {
		t.Fatalf("LoadCheckpoint() empty: %v", err)
	}
	if ok {
		t.Fatal("LoadCheckpoint() empty ok = true, want false")
	}

	want := Checkpoint{Epoch: 3, LogStartOffset: 4, HW: 9}
	if err := log.StoreCheckpoint(context.Background(), want); err != nil {
		t.Fatalf("StoreCheckpoint(): %v", err)
	}
	got, ok, err := log.LoadCheckpoint(context.Background())
	if err != nil {
		t.Fatalf("LoadCheckpoint(): %v", err)
	}
	if !ok || got != want {
		t.Fatalf("LoadCheckpoint() = (%+v, %v), want (%+v, true)", got, ok, want)
	}
}

func TestCheckpointMonotonicRejectsRegression(t *testing.T) {
	tests := []struct {
		name      string
		current   Checkpoint
		next      Checkpoint
		visibleHW uint64
		leo       uint64
	}{
		{
			name:      "high watermark regression",
			current:   Checkpoint{Epoch: 7, LogStartOffset: 1, HW: 4},
			next:      Checkpoint{Epoch: 7, LogStartOffset: 1, HW: 3},
			visibleHW: 4,
			leo:       4,
		},
		{
			name:      "epoch regression",
			current:   Checkpoint{Epoch: 8, LogStartOffset: 1, HW: 4},
			next:      Checkpoint{Epoch: 7, LogStartOffset: 1, HW: 5},
			visibleHW: 5,
			leo:       5,
		},
		{
			name:      "above visible high watermark",
			current:   Checkpoint{Epoch: 7, LogStartOffset: 1, HW: 4},
			next:      Checkpoint{Epoch: 7, LogStartOffset: 1, HW: 6},
			visibleHW: 5,
			leo:       6,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := openTestMessageStore(t)
			defer store.close(t)

			log := testChannelLog(store)
			if err := log.StoreCheckpoint(context.Background(), tt.current); err != nil {
				t.Fatalf("StoreCheckpoint(): %v", err)
			}
			err := log.StoreCheckpointMonotonic(context.Background(), tt.next, tt.visibleHW, tt.leo)
			if !errors.Is(err, dberrors.ErrCorruptState) {
				t.Fatalf("StoreCheckpointMonotonic() err = %v, want corrupt state", err)
			}
			got, ok, err := log.LoadCheckpoint(context.Background())
			if err != nil {
				t.Fatalf("LoadCheckpoint(): %v", err)
			}
			if !ok || got != tt.current {
				t.Fatalf("checkpoint after rejected store = (%+v, %v), want (%+v, true)", got, ok, tt.current)
			}
		})
	}
}
