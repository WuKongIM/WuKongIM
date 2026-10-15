package management

import (
	"context"
	"errors"
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// Failure cases: unloaded is confused with deleted, unavailable evidence is
// mistaken for inactivity/zero messages, or a foreign observation is accepted.
func TestChannelRuntimeObservationSeparatesLifecycleAndResidency(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		known, active, foreign bool
		err                    error
		want                   string
	}{
		{name: "loaded", known: true, active: true, want: "active"},
		{name: "unloaded", known: true, want: "inactive"},
		{name: "unavailable", want: "unknown"},
		{name: "transport failure", err: errors.New("peer unavailable"), want: "unknown"},
		{name: "foreign fence", known: true, foreign: true, want: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta := observationTestMeta("room")
			observer := observationTestReader{read: func(targets []ChannelObservationTarget, include bool) ([]ChannelObservation, error) {
				if len(targets) != 1 || !include {
					t.Fatalf("observation targets=%v include=%v", targets, include)
				}
				target := targets[0]
				if tc.foreign {
					target.LeaderEpoch++
				}
				seq := uint64(65)
				return []ChannelObservation{{Target: target, Known: tc.known, Active: tc.active, MaxMessageSeq: &seq}}, tc.err
			}}
			app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: observationTestSnapshot()}, ChannelRuntimeMetaPoint: &fakeChannelRuntimeMetaPointReader{metas: map[channelRuntimeMetaTestKey]metadb.ChannelRuntimeMeta{{channelID: "room", channelType: 2}: meta}}, ChannelObservations: observer})
			got, err := app.GetChannelRuntimeMeta(context.Background(), "room", 2)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != "active" || got.RuntimeStatus != tc.want || got.RuntimeNodeID != 2 {
				t.Fatalf("row = %+v", got)
			}
			if tc.want == "unknown" {
				if got.MaxMessageSeq != nil {
					t.Fatalf("unknown evidence exposed seq %d", *got.MaxMessageSeq)
				}
			} else if got.MaxMessageSeq == nil || *got.MaxMessageSeq != 65 {
				t.Fatalf("max seq = %v, want 65", got.MaxMessageSeq)
			}
		})
	}
}

// Only emitted rows may cause remote observation or storage reads, including
// when a metadata scan contains filtered rows and the pagination lookahead.
func TestChannelRuntimeListObservesOnlyReturnedRows(t *testing.T) {
	reader := newFakeChannelRuntimeMetaReader()
	reader.slotPages[1] = map[metadb.ChannelRuntimeMetaCursor]fakeChannelRuntimeMetaPage{{}: {items: []metadb.ChannelRuntimeMeta{observationTestMeta("ignored"), observationTestMeta("room-a"), observationTestMeta("room-b")}, done: true}}
	var observed []ChannelObservationTarget
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: observationTestSnapshot()}, ChannelRuntimeMeta: reader, ChannelObservations: observationTestReader{read: func(targets []ChannelObservationTarget, include bool) ([]ChannelObservation, error) {
		observed = append(observed, targets...)
		if include {
			t.Fatal("unrequested max sequence read")
		}
		out := make([]ChannelObservation, len(targets))
		for i, target := range targets {
			out[i] = ChannelObservation{Target: target, Known: true}
		}
		return out, nil
	}}})
	got, err := app.ListChannelRuntimeMeta(context.Background(), ListChannelRuntimeMetaRequest{Limit: 1, ChannelIDQuery: "room"})
	if err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 || observed[0].ChannelID != "room-a" {
		t.Fatalf("observed = %v, want only returned room-a", observed)
	}
	if len(got.Items) != 1 || got.Items[0].RuntimeStatus != "inactive" || !got.HasMore || got.NextCursor.ChannelID != "room-a" {
		t.Fatalf("page = %+v", got)
	}
}

type observationTestReader struct {
	read func([]ChannelObservationTarget, bool) ([]ChannelObservation, error)
}

func (r observationTestReader) ObserveChannels(_ context.Context, targets []ChannelObservationTarget, include bool) ([]ChannelObservation, error) {
	return r.read(targets, include)
}
func observationTestMeta(id string) metadb.ChannelRuntimeMeta {
	return metadb.ChannelRuntimeMeta{ChannelID: id, ChannelType: 2, Leader: 2, ChannelEpoch: 3, LeaderEpoch: 4, RouteGeneration: 5, Replicas: []uint64{1, 2, 3}, ISR: []uint64{1, 2, 3}, MinISR: 2, Status: uint8(ch.StatusActive)}
}
func observationTestSnapshot() control.Snapshot {
	return control.Snapshot{Slots: []control.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}}}, HashSlots: control.HashSlotTable{Count: 256, Ranges: []control.HashSlotRange{{From: 0, To: 255, SlotID: 1}}}}
}
