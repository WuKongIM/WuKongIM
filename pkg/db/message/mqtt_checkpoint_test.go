package message

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

func mqttCheckpointFixture(t *testing.T) (*Engine, *ChannelStore, DurableProposalManifest, []channel.Record) {
	t.Helper()
	e := openCompatEngine(t)
	s := mustForChannel(t, e, "mqtt-checkpoint:1", channel.ChannelID{ID: "mqtt-checkpoint", Type: 1})
	t.Cleanup(func() { _ = s.Close() })
	records := []channel.Record{
		compatExactTestRecord(t, 1, 101, "mqtt-checkpoint", "1"),
		compatExactTestRecord(t, 1, 102, "mqtt-checkpoint", "2"),
		compatExactTestRecord(t, 1, 103, "mqtt-checkpoint", "3"),
	}
	m := sealCompatProposalManifest(t, DurableProposalManifest{
		Version: DurableProposalManifestVersion, ChannelEpoch: 1, LeaderTerm: 1,
		FenceVersion: 1, CommandID: [32]byte{1}, LastOffset: 3,
	}, records)
	r := StoreAppendBatch(context.Background(), []AppendBatchItem{{Store: s, Records: records, ExactBaseOffset: true, Proposal: m, Committed: 3}})
	if len(r) != 1 || r[0].Err != nil {
		t.Fatalf("seed exact proposal: %+v", r)
	}
	if err := s.log.ApplyMQTTSourceState(context.Background(), 0, MQTTSourceState{
		Generation: "source", Revision: 1, StartAfter: 1, CopiedThrough: 1,
	}); err != nil {
		t.Fatal(err)
	}
	return e, s, m, records
}

func TestMQTTCheckpointRawSetterCannotLowerProtectedCommittedFrontier(t *testing.T) {
	for _, compat := range []bool{false, true} {
		t.Run(map[bool]string{false: "typed", true: "compatibility"}[compat], func(t *testing.T) {
			_, s, _, _ := mqttCheckpointFixture(t)
			ctx := context.Background()
			var err error
			if compat {
				err = s.StoreCheckpoint(channel.Checkpoint{HW: 2})
			} else {
				err = s.log.StoreCheckpoint(ctx, Checkpoint{HW: 2})
			}
			if !errors.Is(err, dberrors.ErrConflict) && !errors.Is(err, channel.ErrCorruptState) {
				t.Fatalf("raw setter lowered protected HW: %v", err)
			}
			cp, present, err := s.log.LoadCheckpoint(ctx)
			if err != nil || !present || cp.HW != 3 {
				t.Fatalf("failed write changed checkpoint: %+v %v %v", cp, present, err)
			}
			if err := s.log.TruncateFrom(ctx, 3); !errors.Is(err, dberrors.ErrConflict) {
				t.Fatalf("protected committed suffix cut: %v", err)
			}
			rows, err := s.log.ReadMQTTProtectedSource(ctx, "source", 2, 3, ReadOptions{Limit: 2, MaxBytes: 1024})
			if err != nil || len(rows) != 2 {
				t.Fatalf("lost original protected content: %v %v", rows, err)
			}
			if err := s.log.StoreCheckpoint(ctx, cp); err != nil {
				t.Fatalf("exact checkpoint retry: %v", err)
			}
		})
	}
	// The legacy raw setter remains intentionally non-monotonic for native logs.
	e := openCompatEngine(t)
	s := mustForChannel(t, e, "native:1", channel.ChannelID{ID: "native", Type: 1})
	defer s.Close()
	for _, hw := range []uint64{3, 2} {
		if err := s.StoreCheckpoint(channel.Checkpoint{HW: hw}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMQTTCheckpointMissingEvidenceCannotBeRecreatedByMutation(t *testing.T) {
	type mutate func(*testing.T, *ChannelStore, DurableProposalManifest, []channel.Record) error
	cases := map[string]mutate{
		"raw_typed": func(_ *testing.T, s *ChannelStore, _ DurableProposalManifest, _ []channel.Record) error {
			return s.log.StoreCheckpoint(context.Background(), Checkpoint{HW: 3})
		},
		"raw_compat": func(_ *testing.T, s *ChannelStore, _ DurableProposalManifest, _ []channel.Record) error {
			return s.StoreCheckpoint(channel.Checkpoint{HW: 3})
		},
		"monotonic": func(_ *testing.T, s *ChannelStore, _ DurableProposalManifest, _ []channel.Record) error {
			return s.StoreCheckpointMonotonic(context.Background(), channel.Checkpoint{HW: 3}, 3, 3)
		},
		"hw_only": func(_ *testing.T, s *ChannelStore, _ DurableProposalManifest, _ []channel.Record) error {
			return s.StoreCheckpointHWMonotonic(context.Background(), 3)
		},
		"hw_zero": func(_ *testing.T, s *ChannelStore, _ DurableProposalManifest, _ []channel.Record) error {
			return s.StoreCheckpointHWMonotonic(context.Background(), 0)
		},
		"batch_hw": func(_ *testing.T, s *ChannelStore, _ DurableProposalManifest, _ []channel.Record) error {
			return StoreCheckpointHWMonotonicBatch(context.Background(), []CheckpointHWBatchItem{{Store: s, HW: 3}})[0].Err
		},
		"typed_fetch": func(_ *testing.T, s *ChannelStore, _ DurableProposalManifest, _ []channel.Record) error {
			_, err := s.log.ApplyFetch(context.Background(), ApplyFetchRequest{BaseSeq: 4, Records: []Record{{ID: 104, Payload: []byte("new")}}, Checkpoint: &Checkpoint{HW: 4}})
			return err
		},
		"snapshot": func(_ *testing.T, s *ChannelStore, _ DurableProposalManifest, _ []channel.Record) error {
			_, err := s.log.InstallSnapshot(context.Background(), Snapshot{Epoch: 1, EndOffset: 3, Payload: []byte("snapshot")}, Checkpoint{Epoch: 1, LogStartOffset: 3, HW: 3}, EpochPoint{Epoch: 1})
			return err
		},
		"exact_retry": func(_ *testing.T, s *ChannelStore, m DurableProposalManifest, records []channel.Record) error {
			return StoreAppendBatch(context.Background(), []AppendBatchItem{{Store: s, Records: records, ExactBaseOffset: true, Proposal: m, Committed: 3}})[0].Err
		},
	}
	for name, apply := range cases {
		t.Run(name, func(t *testing.T) {
			e, s, manifest, records := mqttCheckpointFixture(t)
			deletePhysicalTestKey(t, e, encodeCheckpointKey(s.log.key))
			err := apply(t, s, manifest, records)
			if !errors.Is(err, dberrors.ErrCorruptState) && !errors.Is(err, channel.ErrCorruptState) {
				t.Fatalf("missing protected checkpoint accepted: %v", err)
			}
			if _, ok, err := e.engine.Get(encodeCheckpointKey(s.log.key)); err != nil || ok {
				t.Fatalf("recreated lost evidence: present=%v err=%v", ok, err)
			}
			if leo, err := s.log.loadLEOLocked(context.Background()); err != nil || leo != 3 {
				t.Fatalf("partial append: leo=%d err=%v", leo, err)
			}
			if _, ok, err := s.log.LoadSnapshotPayload(context.Background()); err != nil || ok {
				t.Fatalf("partial snapshot: present=%v err=%v", ok, err)
			}
			if !s.log.appendMu.TryLock() {
				t.Fatal("append lock leaked")
			}
			s.log.appendMu.Unlock()
			if !s.log.checkpointMu.TryLock() {
				t.Fatal("checkpoint lock leaked")
			}
			s.log.checkpointMu.Unlock()
		})
	}
}

func TestMQTTCheckpointCorruptionCannotBecomeProgress(t *testing.T) {
	for _, damage := range []string{"missing", "below_copy", "bad_shape", "bad_encoding", "bad_source"} {
		t.Run(damage, func(t *testing.T) {
			e, s, _, _ := mqttCheckpointFixture(t)
			key := encodeCheckpointKey(s.log.key)
			switch damage {
			case "missing":
				deletePhysicalTestKey(t, e, key)
			case "below_copy":
				setPhysicalTestValue(t, e, key, encodeCheckpoint(Checkpoint{}))
			case "bad_shape":
				setPhysicalTestValue(t, e, key, encodeCheckpoint(Checkpoint{HW: 3, LogStartOffset: 4}))
			case "bad_encoding":
				setPhysicalTestValue(t, e, key, []byte{1})
			case "bad_source":
				setPhysicalTestValue(t, e, mqttSourceKey(s.log.key), []byte{1})
			}
			before, beforeOK, err := e.engine.Get(key)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if _, _, err := s.log.LoadCheckpoint(ctx); err == nil {
				t.Fatal("checkpoint read accepted corrupt protected evidence")
			}
			if err := s.StoreCheckpointHWMonotonic(ctx, 0); err == nil {
				t.Fatal("no-op HW write hid corrupt evidence")
			}
			if err := StoreCheckpointHWMonotonicBatch(ctx, []CheckpointHWBatchItem{{Store: s, HW: 0}})[0].Err; err == nil {
				t.Fatal("no-op batch hid corrupt evidence")
			}
			if err := s.log.StoreCheckpoint(ctx, Checkpoint{HW: 3}); err == nil {
				t.Fatal("raw write repaired corrupt evidence")
			}
			if err := s.log.ApplyMQTTSourceState(ctx, 1, MQTTSourceState{Generation: "source", Revision: 2, StartAfter: 1, CopiedThrough: 2, ReceiptDigest: [32]byte{1}}); err == nil {
				t.Fatal("source advancement accepted corrupt evidence")
			}
			after, afterOK, err := e.engine.Get(key)
			if err != nil || beforeOK != afterOK || !bytes.Equal(before, after) {
				t.Fatalf("changed corrupt evidence: %v", err)
			}
		})
	}
}
