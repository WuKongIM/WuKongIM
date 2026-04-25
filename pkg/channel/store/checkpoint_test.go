package store

import (
	"bytes"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

func TestChannelStoreCheckpointRoundTrip(t *testing.T) {
	st := newTestChannelStore(t)
	want := channel.Checkpoint{Epoch: 3, LogStartOffset: 4, HW: 9}

	if err := st.StoreCheckpoint(want); err != nil {
		t.Fatalf("StoreCheckpoint() error = %v", err)
	}
	got, err := st.LoadCheckpoint()
	if err != nil {
		t.Fatalf("LoadCheckpoint() error = %v", err)
	}
	if got != want {
		t.Fatalf("checkpoint = %+v, want %+v", got, want)
	}
}

func TestSystemStateRoundTripUsesSystemKeyspace(t *testing.T) {
	st := newTestChannelStore(t)

	requireKeyspaceWriteCount := func(expected int) {
		t.Helper()

		prefix := encodeKeyspacePrefix(keyspaceTableSystem, st.key)
		iter, err := st.engine.db.NewIter(&pebble.IterOptions{
			LowerBound: prefix,
			UpperBound: keyUpperBound(prefix),
		})
		if err != nil {
			t.Fatalf("NewIter() error = %v", err)
		}
		defer iter.Close()

		count := 0
		for valid := iter.First(); valid; valid = iter.Next() {
			if !bytes.HasPrefix(iter.Key(), prefix) {
				t.Fatalf("unexpected key outside system keyspace: %x", iter.Key())
			}
			count++
		}
		if err := iter.Error(); err != nil {
			t.Fatalf("iter.Error() = %v", err)
		}
		if count != expected {
			t.Fatalf("system key count = %d, want %d", count, expected)
		}
	}

	checkpoint := channel.Checkpoint{Epoch: 2, LogStartOffset: 3, HW: 7}
	if err := st.StoreCheckpoint(checkpoint); err != nil {
		t.Fatalf("StoreCheckpoint() error = %v", err)
	}
	if err := st.AppendHistory(channel.EpochPoint{Epoch: 2, StartOffset: 3}); err != nil {
		t.Fatalf("AppendHistory() error = %v", err)
	}
	if err := st.StoreSnapshotPayload([]byte("snapshot")); err != nil {
		t.Fatalf("StoreSnapshotPayload() error = %v", err)
	}

	gotCheckpoint, err := st.LoadCheckpoint()
	if err != nil {
		t.Fatalf("LoadCheckpoint() error = %v", err)
	}
	if gotCheckpoint != checkpoint {
		t.Fatalf("checkpoint = %+v, want %+v", gotCheckpoint, checkpoint)
	}

	points, err := st.LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory() error = %v", err)
	}
	if len(points) != 1 || points[0] != (channel.EpochPoint{Epoch: 2, StartOffset: 3}) {
		t.Fatalf("history = %+v", points)
	}

	payload, err := st.LoadSnapshotPayload()
	if err != nil {
		t.Fatalf("LoadSnapshotPayload() error = %v", err)
	}
	if string(payload) != "snapshot" {
		t.Fatalf("payload = %q, want snapshot", payload)
	}

	requireKeyspaceWriteCount(3)
}
