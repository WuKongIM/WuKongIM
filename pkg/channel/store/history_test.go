package store

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func TestChannelStoreHistoryAppendAndLoad(t *testing.T) {
	st := newTestChannelStore(t)
	if err := st.AppendHistory(channel.EpochPoint{Epoch: 7, StartOffset: 10}); err != nil {
		t.Fatalf("AppendHistory() error = %v", err)
	}
	points, err := st.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory() error = %v", err)
	}
	if len(points) != 1 || points[0].Epoch != 7 || points[0].StartOffset != 10 {
		t.Fatalf("points = %+v", points)
	}
}

func TestChannelStoreHistoryPreservesSameOffsetEpochBoundaries(t *testing.T) {
	st := newTestChannelStore(t)
	points := []channel.EpochPoint{
		{Epoch: 7, StartOffset: 0},
		{Epoch: 8, StartOffset: 0},
	}
	for _, point := range points {
		if err := st.AppendHistory(point); err != nil {
			t.Fatalf("AppendHistory(%+v) error = %v", point, err)
		}
	}

	got, err := st.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory() error = %v", err)
	}
	if len(got) != len(points) || got[0] != points[0] || got[1] != points[1] {
		t.Fatalf("points = %+v, want %+v", got, points)
	}
}
