package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
	"github.com/WuKongIM/WuKongIM/internal/usecase/chatlifecyclerun"
)

func TestCurrentReportRehearsalCommandRequiresV8(t *testing.T) {
	report, receipt := currentRehearsalTerminalFixture(t)
	for _, schema := range []string{chatlifecycle.ReportSchemaVersion, "wukongim/chat-lifecycle-report/v7"} {
		t.Run(schema, func(t *testing.T) {
			candidate := report
			candidate.SchemaVersion = schema
			directory := t.TempDir()
			var wire any = candidate
			if schema == "wukongim/chat-lifecycle-report/v7" {
				encoded, err := json.Marshal(candidate)
				if err != nil {
					t.Fatal(err)
				}
				wire = json.RawMessage(currentReportLegacyHotBudgetJSON(t, encoded))
			}
			reportPath := writeJSONFile(t, directory, "final.json", wire)
			startPath := writeJSONFile(t, directory, "run-start.json", receipt)
			// The archived decoder remains deliberately readable for diagnostics.
			if decoded, err := chatlifecycle.ReadReport(reportPath); err != nil || decoded.SchemaVersion != schema || decoded.Verdict.LatencyGate != nil {
				t.Fatalf("valid wire fixture lost its original unknown gate: %q/%v", decoded.SchemaVersion, err)
			}
			var output bytes.Buffer
			command := newRootCommand(&output)
			command.SetArgs([]string{"validate-rehearsal-report", "--report", reportPath, "--run-start", startPath})
			err := command.Execute()
			if schema == chatlifecycle.ReportSchemaVersion {
				if err != nil {
					t.Fatalf("current terminal command rejected: %v", err)
				}
				var result reportResult
				if json.Unmarshal(output.Bytes(), &result) != nil || result.Outcome != report.Verdict.Outcome || result.Cause != report.Verdict.Cause {
					t.Fatalf("current terminal outcome changed: %s", output.Bytes())
				}
			} else if !errors.Is(err, chatlifecyclerun.ErrInvalidInput) || output.Len() != 0 {
				t.Fatalf("legacy diagnostic became a current terminal result: error=%v output=%q", err, output.String())
			}
		})
	}
}

// Historicalv7 never contained the new attempt-budget field. Keep every old
// case/assertion while removing only that pending key from its wire fixture.
func currentReportLegacyHotBudgetJSON(t *testing.T, body []byte) []byte {
	t.Helper()
	var root, thresholds, latency map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(root["thresholds"], &thresholds); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(thresholds["latency"], &latency); err != nil {
		t.Fatal(err)
	}
	delete(latency, "hot_attempt_timeout")
	thresholds["latency"], _ = json.Marshal(latency)
	root["thresholds"], _ = json.Marshal(thresholds)
	result, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCurrentReportFormalChainRequiresV8ForEveryCut(t *testing.T) {
	formal, capacity := currentFormalChainShapeFixture()
	for _, test := range []struct {
		name, formal, capacity string
		accept                 bool
	}{
		{"current", chatlifecycle.ReportSchemaVersion, chatlifecycle.ReportSchemaVersion, true},
		{"legacy_formal", "wukongim/chat-lifecycle-report/v7", chatlifecycle.ReportSchemaVersion, false},
		{"legacy_capacity", chatlifecycle.ReportSchemaVersion, "wukongim/chat-lifecycle-report/v7", false},
		{"both_legacy", "wukongim/chat-lifecycle-report/v7", "wukongim/chat-lifecycle-report/v7", false},
		{"missing_formal", "", chatlifecycle.ReportSchemaVersion, false},
		{"missing_capacity", chatlifecycle.ReportSchemaVersion, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			soak, next := formal, capacity
			soak.SchemaVersion, next.SchemaVersion = test.formal, test.capacity
			result, err := formalChainResultFromReports(soak, &next)
			if test.accept {
				if err != nil || result.Outcome != capacity.Verdict.Outcome {
					t.Fatalf("current continuous chain rejected: %+v/%v", result, err)
				}
			} else if !errors.Is(err, chatlifecyclerun.ErrInvalidInput) {
				t.Fatalf("noncurrent report chain granted current terminal authority: %+v/%v", result, err)
			}
		})
	}
	formal.Verdict.Outcome, formal.Verdict.Cause = chatlifecycle.VerdictProductFailure, chatlifecycle.VerdictCauseMessageLoss
	for _, schema := range []string{chatlifecycle.ReportSchemaVersion, "wukongim/chat-lifecycle-report/v7"} {
		t.Run("early_failure_"+schema, func(t *testing.T) {
			formal.SchemaVersion = schema
			result, err := formalChainResultFromReports(formal, nil)
			if schema == chatlifecycle.ReportSchemaVersion {
				if err != nil || result.Outcome != formal.Verdict.Outcome {
					t.Fatalf("current early failure lost: %+v/%v", result, err)
				}
			} else if !errors.Is(err, chatlifecyclerun.ErrInvalidInput) {
				t.Fatalf("legacy early report emitted current chain result: %+v/%v", result, err)
			}
		})
	}
}

// The typed reducer expects reports already checked by the wire reader. This
// fixture isolates that reducer's additional current-schema eligibility bound.
func currentFormalChainShapeFixture() (chatlifecycle.Report, chatlifecycle.Report) {
	end := time.Unix(1_990_000_000, 0)
	formal := chatlifecycle.Report{
		SchemaVersion: chatlifecycle.ReportSchemaVersion,
		Profile:       chatlifecycle.ProfileFormal, Mode: chatlifecycle.ModeSoak, Stage: chatlifecycle.StageFormal,
		Kind: chatlifecycle.CheckpointFinal, Final: true, Continuous: true,
		DatasetDigest: "sha256:" + strings.Repeat("a", 64),
		Fence:         chatlifecycle.ReportFence{RunHash: "sha256:" + strings.Repeat("b", 64), AssignmentHash: "sha256:" + strings.Repeat("c", 64), Generation: 1},
		Window:        chatlifecycle.ReportTimeWindow{End: end},
		Verdict:       chatlifecycle.ReportVerdictEvidence{Terminal: true, Outcome: chatlifecycle.VerdictPass, Cause: chatlifecycle.VerdictCauseCompleted},
	}
	capacity := formal
	capacity.Mode = chatlifecycle.ModeCapacity
	capacity.Window = chatlifecycle.ReportTimeWindow{Start: end.Add(time.Second), End: end.Add(time.Hour)}
	capacity.Capacity = chatlifecycle.ReportCapacityEvidence{Attempted: true, Completed: true, MaximumPassingRate: 2000, RecoveryPassed: true}
	return formal, capacity
}

// Capture an actual versioned terminal wire report using existing public
// producer APIs. There is no sleep, process, network, workload or cloud action.
func currentRehearsalTerminalFixture(t *testing.T) (chatlifecycle.Report, chatlifecycle.RunStartReceipt) {
	t.Helper()
	cfg := chatlifecycle.FormalConfig()
	cfg.Stage, cfg.RunID = chatlifecycle.StageRehearsal, "current-schema-rehearsal"
	start := time.Unix(1_990_000_000, 0)
	fence := chatlifecycle.WorkerFence{RunID: cfg.RunID, AssignmentID: "schema-assignment", Generation: 1}
	recorder, err := chatlifecycle.NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	histogram := chatlifecycle.WorkerHistogramSnapshot{BucketUpper: [16]uint64{0, 1_000_000, 2_000_000, 5_000_000, 10_000_000, 20_000_000, 50_000_000, 100_000_000, 200_000_000, 400_000_000, 1_000_000_000, 2_000_000_000, 5_000_000_000, 10_000_000_000, 30_000_000_000, 60_000_000_000}}
	snapshots := make([]chatlifecycle.WorkerSnapshot, 3)
	for index := range snapshots {
		snapshots[index] = chatlifecycle.WorkerSnapshot{
			RunID: fence.RunID, AssignmentID: fence.AssignmentID, Generation: fence.Generation, WorkerID: uint64(index), WorkerCount: 3, Phase: chatlifecycle.WorkerPhaseFinal, Uptime: time.Second, SnapshotSequence: 1,
			Sync:              chatlifecycle.WorkerSyncSnapshot{ConnectLatency: histogram, Latency: histogram},
			HotSendackLatency: histogram, ColdFirstCreateSendackLatency: histogram, LifecycleReheatSendackLatency: histogram, RecvackLatency: histogram, SendPendingToWriteLatency: histogram, SendWriteToAckLatency: histogram,
		}
	}
	evidence := chatlifecycle.CheckpointEvidence{
		DatasetDigest: "sha256:" + strings.Repeat("d", 64), TopologyValidated: true,
		MetaCreate: chatlifecycle.MetaCreateAccountingSnapshot{Checkpoints: 1},
		Lifecycle:  chatlifecycle.LifecycleProofSnapshot{ReheatLatency: histogram},
		Verdict:    chatlifecycle.VerdictSnapshot{Terminal: true, Outcome: chatlifecycle.VerdictProductFailure, Cause: chatlifecycle.VerdictCauseMessageLoss},
	}
	for index := range evidence.Resources.Nodes {
		evidence.Resources.Nodes[index].DataFilesystemBytes = 1_000_000_000_000
	}
	directory := t.TempDir()
	report, err := recorder.CaptureAndWrite(start.Add(time.Second), snapshots, evidence, chatlifecycle.CheckpointOutputPaths{JSON: filepath.Join(directory, "producer.json"), Markdown: filepath.Join(directory, "producer.md")})
	if err != nil {
		t.Fatalf("existing producer fixture rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "producer.md")); err != nil {
		t.Fatal(err)
	}
	return report, chatlifecycle.RunStartReceipt{Schema: chatlifecycle.RunStartReceiptSchemaV1, Stage: cfg.Stage, StartedAt: start, ExpectedEndAt: start.Add(2 * time.Hour), RunHash: report.Fence.RunHash, AssignmentHash: report.Fence.AssignmentHash, Generation: 1}
}
