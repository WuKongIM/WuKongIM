package benchmark

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/chatlifecycle"
)

func TestCurrentReportCapacityCLIParsingRequiresV8(t *testing.T) {
	report := currentCapacityCLIReportFixture(t)
	for _, schema := range []string{chatlifecycle.ReportSchemaVersion, "wukongim/chat-lifecycle-report/v7"} {
		t.Run(schema, func(t *testing.T) {
			directory := t.TempDir()
			reportPath := filepath.Join(directory, "formal.json")
			candidate := report
			candidate.SchemaVersion = schema
			body, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if schema == "wukongim/chat-lifecycle-report/v7" {
				body = currentReportLegacyHotBudgetJSON(t, body)
			}
			if err := os.WriteFile(reportPath, body, 0o600); err != nil {
				t.Fatal(err)
			}
			if decoded, err := chatlifecycle.ReadReport(reportPath); err != nil || decoded.SchemaVersion != schema || decoded.Verdict.LatencyGate != nil {
				t.Fatalf("capacity wire fixture lost diagnostic readability/unknown proof: %q/%v", decoded.SchemaVersion, err)
			}
			cfg := chatlifecycle.FormalConfig()
			cfg.Mode = chatlifecycle.ModeCapacity
			cfg.Capacity.AgedCheckpoint = chatlifecycle.AgedCheckpoint{Reference: reportPath, Completed: true, Passed: true, Duration: 72 * time.Hour}
			configPath := filepath.Join(directory, "capacity.yaml")
			// Use the same reviewed YAML writer as the real formal-to-capacity
			// path; JSON duration integers are not this configuration contract.
			if err := writeFormalChainConfig(configPath, cfg); err != nil {
				t.Fatal(err)
			}
			if _, err := chatlifecycle.LoadConfig(configPath); err != nil {
				t.Fatalf("capacity config fixture is invalid before schema eligibility: %v", err)
			}
			cli := chatLifecycleCLIConfig{configPath: configPath, checkpointPath: reportPath, outputDir: filepath.Join(directory, "not-created")}
			err = loadCapacityChatLifecycleConfig(&cli)
			if schema == chatlifecycle.ReportSchemaVersion {
				if err != nil || cli.checkpoint.SchemaVersion != schema {
					t.Fatalf("current capacity parse rejected: %q/%v", cli.checkpoint.SchemaVersion, err)
				}
			} else if err == nil {
				t.Fatal("legacy diagnostic report admitted a current capacity configuration")
			}
			if _, err := os.Stat(cli.outputDir); !os.IsNotExist(err) {
				t.Fatalf("parser created output or obscured its absence: %v", err)
			}
		})
	}
}

// Strip only the field that real historicalv7 writers could not emit.
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

// The fixture starts at the existing public producer boundary. Its test-only
// final-cut fields are then validated by the existing MarshalReport contract;
// no real workload, process, elapsed wait, target, or cloud action is involved.
func currentCapacityCLIReportFixture(t *testing.T) chatlifecycle.Report {
	t.Helper()
	cfg := chatlifecycle.FormalConfig()
	cfg.RunID = "current-capacity-schema"
	start := time.Unix(1_990_000_000, 0)
	fence := chatlifecycle.WorkerFence{RunID: cfg.RunID, AssignmentID: "current-schema", Generation: 1}
	recorder, err := chatlifecycle.NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	empty := chatlifecycle.WorkerHistogramSnapshot{BucketUpper: [16]uint64{0, 1_000_000, 2_000_000, 5_000_000, 10_000_000, 20_000_000, 50_000_000, 100_000_000, 200_000_000, 400_000_000, 1_000_000_000, 2_000_000_000, 5_000_000_000, 10_000_000_000, 30_000_000_000, 60_000_000_000}}
	snapshots := make([]chatlifecycle.WorkerSnapshot, 3)
	for index := range snapshots {
		snapshots[index] = chatlifecycle.WorkerSnapshot{
			RunID: fence.RunID, AssignmentID: fence.AssignmentID, Generation: 1, WorkerID: uint64(index), WorkerCount: 3, Phase: chatlifecycle.WorkerPhaseFinal, Uptime: time.Second, SnapshotSequence: 1,
			Sync: chatlifecycle.WorkerSyncSnapshot{ConnectLatency: empty, Latency: empty}, HotSendackLatency: empty, ColdFirstCreateSendackLatency: empty, LifecycleReheatSendackLatency: empty, RecvackLatency: empty, SendPendingToWriteLatency: empty, SendWriteToAckLatency: empty,
		}
	}
	evidence := chatlifecycle.CheckpointEvidence{DatasetDigest: "sha256:" + strings.Repeat("d", 64), TopologyValidated: true, MetaCreate: chatlifecycle.MetaCreateAccountingSnapshot{Checkpoints: 1}, Lifecycle: chatlifecycle.LifecycleProofSnapshot{ReheatLatency: empty}, Verdict: chatlifecycle.VerdictSnapshot{Terminal: true, Outcome: chatlifecycle.VerdictProductFailure, Cause: chatlifecycle.VerdictCauseMessageLoss}}
	for index := range evidence.Resources.Nodes {
		evidence.Resources.Nodes[index].DataFilesystemBytes = 1_000_000_000_000
	}
	directory := t.TempDir()
	report, err := recorder.CaptureAndWrite(start.Add(time.Second), snapshots, evidence, chatlifecycle.CheckpointOutputPaths{JSON: filepath.Join(directory, "producer.json"), Markdown: filepath.Join(directory, "producer.md")})
	if err != nil {
		t.Fatalf("producer fixture invalid: %v", err)
	}
	report.Continuous = true
	report.Window.End = start.Add(72 * time.Hour)
	report.Window.Elapsed = 72 * time.Hour
	report.MinimumWorkerUptime = 72 * time.Hour
	for index := range report.Workers {
		report.Workers[index].Phase = chatlifecycle.WorkerPhaseRunning
	}
	report.Verdict.Outcome, report.Verdict.Cause = chatlifecycle.VerdictPass, chatlifecycle.VerdictCauseCompleted
	histogram := empty
	histogram.Count, histogram.Buckets[1] = 3_000, 3_000
	histogram.SumNanos, histogram.MaxNanos = uint64(3_000*time.Millisecond), uint64(time.Millisecond)
	report.Latency.HotSendACK, report.Latency.ColdFirstCreateSendACK, report.Latency.FullSync = histogram, histogram, histogram
	report.Sync.Latency = histogram
	report.Sync.Thresholds = chatlifecycle.LatencyThresholdCounters{P99Limit: cfg.Thresholds.Latency.Sync.P99, P999Limit: cfg.Thresholds.Latency.Sync.P999, Count: 3_000}
	report.Latency.QualificationWindow = chatlifecycle.ReportLatencyQualificationWindow{
		WarmupEnd: start.Add(10 * time.Minute), BaselineAt: start.Add(10 * time.Minute), LastAt: report.Window.End, WindowStart: report.Window.End.Add(-10 * time.Minute), WindowEnd: report.Window.End,
		BaselineElapsed: 10 * time.Minute, WindowStartElapsed: 72*time.Hour - 10*time.Minute, WindowEndElapsed: 72 * time.Hour, Duration: 10 * time.Minute, RequiredDuration: 10 * time.Minute, MaxAge: 5 * time.Second, MinimumSamples: 1_000, Samples: [3]uint64{1_000, 1_000, 1_000}, Qualified: [3]bool{true, true, true}, Status: chatlifecycle.ReportLatencySufficient,
	}
	if _, err := chatlifecycle.MarshalReport(report, chatlifecycle.ReportFormatJSON); err != nil {
		t.Fatalf("passing current wire fixture invalid: %v", err)
	}
	return report
}
