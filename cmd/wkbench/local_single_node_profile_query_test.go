package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/localbaseline"
)

func TestLocalSingleNodeProfileThresholdQueryWritesFirstTypedBreach(t *testing.T) {
	dir := t.TempDir()
	lifecyclePath := filepath.Join(dir, "lifecycle.jsonl")
	outputPath := filepath.Join(dir, "query.json")
	start := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	writeLocalProfileQueryCaptures(t, lifecyclePath, []localbaseline.LifecycleCapture{
		localProfileQueryCapture(start, "run", 0, 0, 0),
		localProfileQueryCapture(start.Add(time.Second), "run", 100, 95, 0),
		localProfileQueryCapture(start.Add(2*time.Second), "run", 200, 180, 0),
	})
	var stderr bytes.Buffer

	code := runWithStderr([]string{
		"report", "local-single-node-profile-threshold",
		"--lifecycle", lifecyclePath,
		"--run-id", "profile-query-run",
		"--offered-qps", "100",
		"--minimum-throughput-percent", "90",
		"--output", outputPath,
	}, &stderr)
	if code != 0 {
		t.Fatalf("query exit = %d: %s", code, stderr.String())
	}
	var query localbaseline.ProfileThresholdQuery
	decodeLocalSingleNodeJSON(t, outputPath, &query)
	if !query.EvidenceComplete || !query.Triggered || query.Trigger.Kind != localbaseline.ProfileTriggerActualOfferedRatio ||
		query.Trigger.AcknowledgedDelta != 85 || !query.Trigger.PreviousAt.Equal(start.Add(time.Second)) ||
		!query.Trigger.CurrentAt.Equal(start.Add(2*time.Second)) {
		t.Fatalf("query = %+v", query)
	}
}

func TestLocalSingleNodeProfileThresholdQueryDoesNotParseErrorText(t *testing.T) {
	dir := t.TempDir()
	lifecyclePath := filepath.Join(dir, "lifecycle.jsonl")
	outputPath := filepath.Join(dir, "query.json")
	at := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	capture := localProfileQueryCapture(at, "warmup", 100, 0, 0)
	capture.Status.LastError = "actual_offered_ratio terminal_product_failure"
	writeLocalProfileQueryCaptures(t, lifecyclePath, []localbaseline.LifecycleCapture{capture})
	var stderr bytes.Buffer

	code := runWithStderr([]string{
		"report", "local-single-node-profile-threshold",
		"--lifecycle", lifecyclePath,
		"--run-id", "profile-query-run",
		"--offered-qps", "100",
		"--minimum-throughput-percent", "90",
		"--output", outputPath,
	}, &stderr)
	if code != 0 {
		t.Fatalf("query exit = %d: %s", code, stderr.String())
	}
	var query localbaseline.ProfileThresholdQuery
	decodeLocalSingleNodeJSON(t, outputPath, &query)
	if !query.EvidenceComplete || query.Triggered {
		t.Fatalf("human-readable error text triggered profiling: %+v", query)
	}
}

func localProfileQueryCapture(at time.Time, activePhase string, sent, acknowledged, terminal uint64) localbaseline.LifecycleCapture {
	phase := activePhase
	if activePhase == "run" {
		phase = "warmup"
	}
	traffic := localbaseline.TrafficEvidence{
		Planned: sent, Dispatched: sent, LogicalSent: sent, SendAttempts: sent,
		SendACKs: acknowledged, TerminalErrors: terminal, Remaining: sent - acknowledged - terminal,
		StableClientMsgNo: true, RetryEvidenceComplete: true, MaximumRetriesPerMessage: 3,
	}
	return localbaseline.LifecycleCapture{
		Schema: localbaseline.LifecycleCaptureSchema, SampledAt: at,
		Status: &localbaseline.CapturedStatus{
			Phase: phase, ActivePhase: activePhase, ObservedAt: at,
			Lifecycle:  &localbaseline.CapturedLifecycleStatus{ActiveConnections: 2500, Traffic: traffic},
			Assignment: localbaseline.CapturedAssignment{RunID: "profile-query-run", AssignmentID: "assignment-1"},
		},
		Server: localbaseline.ProcessEvidence{PID: 1, StartToken: "server", Alive: true},
		Worker: localbaseline.ProcessEvidence{PID: 2, StartToken: "worker", Alive: true},
	}
}

func writeLocalProfileQueryCaptures(t *testing.T, path string, captures []localbaseline.LifecycleCapture) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for _, capture := range captures {
		if err := encoder.Encode(capture); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
