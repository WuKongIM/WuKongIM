package message

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
)

func TestHistoryAppendLoadAndTruncate(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	points := []EpochPoint{
		{Epoch: 7, StartOffset: 0},
		{Epoch: 8, StartOffset: 5},
		{Epoch: 9, StartOffset: 10},
	}
	for _, point := range points {
		if err := log.AppendHistory(context.Background(), point); err != nil {
			t.Fatalf("AppendHistory(%+v): %v", point, err)
		}
	}
	if err := log.AppendHistory(context.Background(), points[2]); err != nil {
		t.Fatalf("AppendHistory() duplicate boundary: %v", err)
	}

	got, ok, err := log.LoadHistory(context.Background())
	if err != nil {
		t.Fatalf("LoadHistory(): %v", err)
	}
	if !ok || len(got) != 3 || got[0] != points[0] || got[1] != points[1] || got[2] != points[2] {
		t.Fatalf("history = (%+v, %v), want %+v", got, ok, points)
	}

	if err := log.TruncateHistoryTo(context.Background(), 5); err != nil {
		t.Fatalf("TruncateHistoryTo(): %v", err)
	}
	got, ok, err = log.LoadHistory(context.Background())
	if err != nil {
		t.Fatalf("LoadHistory() after truncate: %v", err)
	}
	want := points[:2]
	if !ok || len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("history after truncate = (%+v, %v), want %+v", got, ok, want)
	}
}

func TestHistoryRejectsRegression(t *testing.T) {
	store := openTestMessageStore(t)
	defer store.close(t)

	log := testChannelLog(store)
	if err := log.AppendHistory(context.Background(), EpochPoint{Epoch: 8, StartOffset: 5}); err != nil {
		t.Fatalf("AppendHistory(): %v", err)
	}
	err := log.AppendHistory(context.Background(), EpochPoint{Epoch: 7, StartOffset: 6})
	if !errors.Is(err, dberrors.ErrCorruptState) {
		t.Fatalf("AppendHistory() err = %v, want corrupt state", err)
	}
}
