package message

import (
	"bytes"
	"context"
	"testing"

	channel "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

func mqttActivationProposal(t *testing.T, previous DurableProposalManifest, command byte) (DurableProposalManifest, []channel.Record) {
	t.Helper()
	r, err := compatibilityRecordFromRow(messageRow{MessageID: uint64(500 + int(command)), ChannelID: "activation", ChannelType: 1,
		ServerTimestampMS: 1000 + int64(command), FramerFlags: 4, Payload: []byte(quorumlog.MQTTSourceActivationPayload)})
	if err != nil {
		t.Fatal(err)
	}
	r.Epoch = 1
	m := DurableProposalManifest{Version: quorumlog.MQTTSourceProposalManifestVersion, ChannelEpoch: 1, LeaderTerm: 1, FenceVersion: 1,
		CommandID: quorumlog.CommandID{command}, BaseOffset: previous.LastOffset, LastOffset: previous.LastOffset + 1,
		PreviousIndex: previous.LastOffset, PreviousDigest: previous.Digest, PreviousTerm: previous.LeaderTerm}
	return sealCompatProposalManifest(t, m, []channel.Record{r}), []channel.Record{r}
}

func appendMQTTActivation(t *testing.T, s *ChannelStore, m DurableProposalManifest, records []channel.Record, committed uint64) {
	t.Helper()
	result := StoreAppendBatch(context.Background(), []AppendBatchItem{{Store: s, Records: records, ExpectedBaseOffset: m.BaseOffset, ExactBaseOffset: true, Proposal: m, Committed: committed}})
	if len(result) != 1 || result[0].Err != nil || !result[0].Outcome.Durable() {
		t.Fatalf("activation append: %+v", result)
	}
}

func TestMQTTLogActivationPendingCommitDuplicateAndBackup(t *testing.T) {
	ctx := context.Background()
	e := openCompatEngine(t)
	s := mustForChannel(t, e, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
	defer s.Close()
	m, records := mqttActivationProposal(t, DurableProposalManifest{}, 1)
	appendMQTTActivation(t, s, m, records, 0)
	appendMQTTActivation(t, s, m, records, 0)
	if _, ok, err := s.log.LoadMQTTSourceState(ctx); err != nil || ok {
		t.Fatalf("uncommitted activation became source: %v %v", ok, err)
	}
	if cp, ok, err := s.log.LoadCheckpoint(ctx); err != nil || !ok || cp.HW != 0 {
		t.Fatalf("pending checkpoint: %+v %v %v", cp, ok, err)
	}
	trim, err := s.log.TrimPrefixThrough(ctx, 1)
	if err != nil || trim.Deleted != 0 {
		t.Fatalf("pending activation lost to trim: %+v %v", trim, err)
	}
	// A backup below the pending control must not turn it into a restored source.
	cut := BackupChannelCut{Key: s.log.key, ID: s.log.id, Checkpoint: Checkpoint{}}
	empty := readBackupSnapshot(t, s.log.db, BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{cut}})
	target := openTestMessageStore(t)
	defer target.close(t)
	if _, err := target.db.ImportBackupSnapshot(ctx, empty); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := target.db.engine.Get(mqttActivationKey(s.log.key)); err != nil || ok {
		t.Fatal("pending marker leaked into committed backup")
	}
	if err := s.StoreCheckpointHWMonotonic(ctx, 1); err != nil {
		t.Fatal(err)
	}
	want := MQTTSourceState{Generation: quorumlog.MQTTSourceGeneration(m.CommandID), Revision: 1}
	if got, ok, err := s.log.LoadMQTTSourceState(ctx); err != nil || !ok || got != want {
		t.Fatalf("committed source: %+v %v %v", got, ok, err)
	}
	second, secondRecords := mqttActivationProposal(t, m, 2)
	appendMQTTActivation(t, s, second, secondRecords, 2)
	if got, _, err := s.log.LoadMQTTSourceState(ctx); err != nil || got != want {
		t.Fatalf("duplicate changed incarnation: %+v %v", got, err)
	}
	if err := s.Truncate(0); err == nil {
		t.Fatal("committed activation was truncated")
	}
	cut.Checkpoint.HW = 2
	body := readBackupSnapshot(t, s.log.db, BackupSnapshotRequest{HashSlot: 1, Channels: []BackupChannelCut{cut}})
	restored := openTestMessageStore(t)
	defer restored.close(t)
	if _, err := restored.db.ImportBackupSnapshot(ctx, body); err != nil {
		t.Fatal(err)
	}
	l := mustAcquireChannel(t, restored.db, s.log.key, s.log.id)
	defer l.Close()
	if got, ok, err := l.LoadMQTTSourceState(ctx); err != nil || !ok || got != want {
		t.Fatalf("restored source: %+v %v %v", got, ok, err)
	}
	if trim, err := l.TrimPrefixThrough(ctx, 2); err != nil || trim.Deleted != 0 {
		t.Fatalf("restore lost protection: %+v %v", trim, err)
	}
	rows, err := l.ReadMQTTProtectedSource(ctx, want.Generation, 1, 2, ReadOptions{Limit: 2, MaxBytes: 1024})
	if err != nil || len(rows) != 2 || !bytes.Equal(rows[0].Payload, []byte(quorumlog.MQTTSourceActivationPayload)) {
		t.Fatalf("restored records: %+v %v", rows, err)
	}
}

func TestMQTTLogActivationPendingSuffixCanBeReplaced(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		t.Run(map[bool]string{false: "truncate", true: "recovery"}[recovery], func(t *testing.T) {
			ctx := context.Background()
			e := openCompatEngine(t)
			s := mustForChannel(t, e, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
			defer s.Close()
			old, oldRecords := mqttActivationProposal(t, DurableProposalManifest{}, 1)
			appendMQTTActivation(t, s, old, oldRecords, 0)
			next, nextRecords := mqttActivationProposal(t, DurableProposalManifest{}, 2)
			if recovery {
				state, err := s.LoadDurableRecovery(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.ReplaceRecoverySuffix(ctx, ReplaceRecoverySuffixRequest{Expected: state.DurableFrontier, Proposals: []RecoveryProposal{{Manifest: next, Records: nextRecords}}, Committed: 1}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.Truncate(0); err != nil {
					t.Fatal(err)
				}
				appendMQTTActivation(t, s, next, nextRecords, 1)
			}
			got, ok, err := s.log.LoadMQTTSourceState(ctx)
			if err != nil || !ok || got.Generation != quorumlog.MQTTSourceGeneration(next.CommandID) {
				t.Fatalf("wrong recovered source: %+v %v %v", got, ok, err)
			}
		})
	}
}

func TestMQTTLogActivationMissingOrCorruptProjectionFailsClosed(t *testing.T) {
	for _, damage := range []string{"checkpoint", "source", "marker", "marker_value"} {
		t.Run(damage, func(t *testing.T) {
			e := openCompatEngine(t)
			s := mustForChannel(t, e, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
			defer s.Close()
			m, records := mqttActivationProposal(t, DurableProposalManifest{}, 1)
			appendMQTTActivation(t, s, m, records, 1)
			switch damage {
			case "checkpoint":
				deletePhysicalTestKey(t, e, encodeCheckpointKey(s.log.key))
			case "source":
				deletePhysicalTestKey(t, e, mqttSourceKey(s.log.key))
			case "marker":
				deletePhysicalTestKey(t, e, mqttActivationKey(s.log.key))
			case "marker_value":
				setPhysicalTestValue(t, e, mqttActivationKey(s.log.key), []byte{1})
			}
			if _, _, err := s.log.LoadCheckpoint(context.Background()); err == nil {
				t.Fatal("corrupt activation evidence accepted")
			}
			if err := s.StoreCheckpointHWMonotonic(context.Background(), 1); err == nil {
				t.Fatal("no-op repaired corrupt activation")
			}
			if _, err := s.log.TrimPrefixThrough(context.Background(), 1); err == nil {
				t.Fatal("trim accepted corrupt activation")
			}
		})
	}
}

// A log-derived generation must originate in the exact control commit. The
// replica CAS port must not install one without its durable activation marker.
func TestMQTTLogActivationRejectsUnprovenGenerationCAS(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "reserved_without_control", true: "pending_with_unrelated_source"}[pending], func(t *testing.T) {
			e := openCompatEngine(t)
			s := mustForChannel(t, e, "activation:1", channel.ChannelID{ID: "activation", Type: 1})
			defer s.Close()
			generation := quorumlog.MQTTSourceGeneration(quorumlog.CommandID{1})
			if pending {
				m, records := mqttActivationProposal(t, DurableProposalManifest{}, 1)
				appendMQTTActivation(t, s, m, records, 0)
				generation = "unrelated-local-state"
			}
			err := s.log.ApplyMQTTSourceState(context.Background(), 0, MQTTSourceState{Generation: generation, Revision: 1})
			if err == nil {
				t.Fatal("CAS fabricated or bypassed a log activation")
			}
			if _, ok, err := s.log.LoadMQTTSourceState(context.Background()); err != nil || ok {
				t.Fatalf("rejected activation changed storage: %v %v", ok, err)
			}
		})
	}
}
