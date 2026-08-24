package localbaseline

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQueryFirstMeasuredProfileThresholdUsesTypedActualOfferedInterval(t *testing.T) {
	start := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	captures := []LifecycleCapture{
		profileLifecycleCapture(start, "warmup", "run-a", profileTraffic(0, 0, 0, 0)),
		profileLifecycleCapture(start.Add(time.Second), "run", "run-a", profileTraffic(100, 90, 0, 0)),
		profileLifecycleCapture(start.Add(2*time.Second), "run", "run-a", profileTraffic(200, 175, 0, 0)),
		profileLifecycleCapture(start.Add(3*time.Second), "run", "run-a", profileTraffic(300, 275, 0, 0)),
	}

	query := QueryFirstMeasuredProfileThreshold(captures, "run-a", 100, 90)
	if !query.EvidenceComplete || !query.Triggered || query.Trigger.Kind != ProfileTriggerActualOfferedRatio {
		t.Fatalf("query = %+v", query)
	}
	if !query.Trigger.PreviousAt.Equal(start.Add(time.Second)) || !query.Trigger.CurrentAt.Equal(start.Add(2*time.Second)) {
		t.Fatalf("first threshold bracket = %s -> %s", query.Trigger.PreviousAt, query.Trigger.CurrentAt)
	}
	if query.Trigger.AcknowledgedDelta != 85 || query.Trigger.ActualOfferedPercent != 85 {
		t.Fatalf("first threshold evidence = %+v", query.Trigger)
	}
	if query.LivePhase != ProfilePhaseMeasurement {
		t.Fatalf("live phase = %q", query.LivePhase)
	}
}

func TestQueryFirstMeasuredProfileThresholdPrefersTypedTerminalFailure(t *testing.T) {
	start := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	captures := []LifecycleCapture{
		profileLifecycleCapture(start, "run", "run-a", profileTraffic(0, 0, 0, 0)),
		profileLifecycleCapture(start.Add(time.Second), "run", "run-a", profileTraffic(100, 10, 1, 0)),
	}

	query := QueryFirstMeasuredProfileThreshold(captures, "run-a", 100, 90)
	if !query.EvidenceComplete || !query.Triggered || query.Trigger.Kind != ProfileTriggerTerminalProductFailure ||
		query.Trigger.TerminalFailureDelta != 1 {
		t.Fatalf("terminal query = %+v", query)
	}
}

func TestQueryFirstMeasuredProfileThresholdDoesNotUseWarmupOrHumanErrorText(t *testing.T) {
	start := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	first := profileLifecycleCapture(start, "warmup", "run-a", profileTraffic(100, 0, 0, 0))
	first.Status.LastError = "terminal product failure actual/offered ratio"
	second := profileLifecycleCapture(start.Add(time.Second), "run", "run-a", profileTraffic(100, 0, 0, 0))
	third := profileLifecycleCapture(start.Add(2*time.Second), "cooldown", "run-a", profileTraffic(100, 0, 1, 0))

	query := QueryFirstMeasuredProfileThreshold([]LifecycleCapture{first, second, third}, "run-a", 100, 90)
	if !query.EvidenceComplete || query.Triggered {
		t.Fatalf("warmup/text/cooldown must not trigger profiling: %+v", query)
	}
	if query.LivePhase != ProfilePhaseDrain {
		t.Fatalf("live phase = %q", query.LivePhase)
	}
}

func TestQueryFirstMeasuredProfileThresholdFailsClosedOnCounterReset(t *testing.T) {
	start := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	captures := []LifecycleCapture{
		profileLifecycleCapture(start, "run", "run-a", profileTraffic(100, 90, 0, 0)),
		profileLifecycleCapture(start.Add(time.Second), "run", "run-a", profileTraffic(99, 80, 0, 0)),
	}

	query := QueryFirstMeasuredProfileThreshold(captures, "run-a", 100, 90)
	if query.EvidenceComplete || query.Triggered || query.Reason != "counter_reset" {
		t.Fatalf("reset query = %+v", query)
	}
}

func TestQueryFirstMeasuredProfileThresholdRequiresAdjacentMeasuredCuts(t *testing.T) {
	start := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	captures := []LifecycleCapture{
		profileLifecycleCapture(start, "warmup", "run-a", profileTraffic(0, 0, 0, 0)),
		profileLifecycleCapture(start.Add(time.Second), "run", "run-a", profileTraffic(100, 10, 1, 0)),
	}

	query := QueryFirstMeasuredProfileThreshold(captures, "run-a", 100, 90)
	if !query.EvidenceComplete || query.Triggered {
		t.Fatalf("phase-boundary counters triggered profiling: %+v", query)
	}
}

func TestQueryFirstMeasuredProfileThresholdIgnoresClosedPreviousAssignment(t *testing.T) {
	start := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	closed := profileLifecycleCapture(start, "", "previous-run", profileTraffic(100, 100, 0, 0))
	closed.Status.Phase = "stopped"
	captures := []LifecycleCapture{
		closed,
		profileLifecycleCapture(start.Add(time.Second), "run", "run-a", profileTraffic(0, 0, 0, 0)),
		profileLifecycleCapture(start.Add(2*time.Second), "run", "run-a", profileTraffic(100, 80, 0, 0)),
	}

	query := QueryFirstMeasuredProfileThreshold(captures, "run-a", 100, 90)
	if !query.EvidenceComplete || !query.Triggered || query.AssignmentID != "assignment-1" ||
		query.Trigger.Kind != ProfileTriggerActualOfferedRatio {
		t.Fatalf("query after closed previous assignment = %+v", query)
	}
}

func TestParseProfileLifecycleSnapshotIgnoresOnlyFinalPartialLine(t *testing.T) {
	at := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	first, err := json.Marshal(profileLifecycleCapture(at, "run", "run-a", profileTraffic(0, 0, 0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(profileLifecycleCapture(at.Add(time.Second), "run", "run-a", profileTraffic(100, 80, 0, 0)))
	if err != nil {
		t.Fatal(err)
	}
	body := append(append(append([]byte(nil), first...), '\n'), second[:len(second)/2]...)

	captures, partial, err := ParseProfileLifecycleSnapshot(bytes.NewReader(body))
	if err != nil || !partial || len(captures) != 1 {
		t.Fatalf("snapshot captures=%d partial=%v err=%v", len(captures), partial, err)
	}
}

func TestReadSingleNodeProfileEvidenceAcceptsNotTriggeredWithoutBlobs(t *testing.T) {
	dir := t.TempDir()
	statusPath := filepath.Join(dir, "threshold-pprof-status.json")
	evidence := ProfileEvidence{
		Schema: ProfileEvidenceSchema, Status: "not_triggered", EvidenceComplete: true,
		CaptureValid: true, Reason: "no_measured_threshold",
	}
	writeProfileJSON(t, statusPath, evidence)

	got, err := ReadSingleNodeProfileEvidence(statusPath)
	if err != nil || got.Status != "not_triggered" || !got.EvidenceComplete {
		t.Fatalf("not-triggered evidence = %+v, %v", got, err)
	}
	if !ProfileEvidenceMatchesQuery(got, ProfileThresholdQuery{
		Schema: ProfileThresholdQuerySchema, RunID: "run-a", AssignmentID: "assignment-1",
		OfferedSendQPS: 100, MinimumThroughputPercent: 90, EvidenceComplete: true,
	}) {
		t.Fatal("not-triggered evidence did not match a complete untriggered query")
	}
}

func TestReadSingleNodeProfileEvidenceRequiresEveryTriggeredBlob(t *testing.T) {
	dir := t.TempDir()
	profileDir := filepath.Join(dir, "threshold-pprof")
	blobsDir := filepath.Join(profileDir, "profiles")
	if err := os.MkdirAll(blobsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	previous := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	current := previous.Add(time.Second)
	trigger := ProfileThresholdTrigger{Kind: ProfileTriggerActualOfferedRatio, PreviousAt: previous, CurrentAt: current}
	exitStatus := 0
	evidence := ProfileEvidence{
		Schema: ProfileEvidenceSchema, Status: "complete", EvidenceComplete: true, CaptureValid: true,
		Reason: "ok", Triggered: true, Trigger: &trigger, Metadata: "threshold-pprof/metadata.json",
		HelperExitStatus: &exitStatus,
	}
	metadata := completeSingleNodeProfileMetadata(trigger)
	writeProfileJSON(t, filepath.Join(profileDir, "metadata.json"), metadata)
	for _, name := range []string{"node-1-cpu.pb.gz", "node-1-heap.pb.gz", "node-1-goroutine.txt"} {
		if err := os.WriteFile(filepath.Join(blobsDir, name), []byte("bounded-profile"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	statusPath := filepath.Join(dir, "threshold-pprof-status.json")
	writeProfileJSON(t, statusPath, evidence)

	got, err := ReadSingleNodeProfileEvidence(statusPath)
	query := ProfileThresholdQuery{
		Schema: ProfileThresholdQuerySchema, RunID: "run-a", AssignmentID: "assignment-1",
		OfferedSendQPS: 100, MinimumThroughputPercent: 90, EvidenceComplete: true, Triggered: true, Trigger: trigger,
	}
	if err != nil || !got.EvidenceComplete || !ProfileEvidenceMatchesQuery(got, query) {
		t.Fatalf("complete evidence = %+v, %v", got, err)
	}
	query.Trigger.AcknowledgedDelta++
	if ProfileEvidenceMatchesQuery(got, query) {
		t.Fatal("profile evidence matched a numerically different typed trigger")
	}

	if err := os.Remove(filepath.Join(blobsDir, "node-1-heap.pb.gz")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSingleNodeProfileEvidence(statusPath); err == nil {
		t.Fatal("triggered profile evidence accepted a missing heap blob")
	}
}

func TestReadSingleNodeProfileEvidenceRejectsSymlinkedCaptureParents(t *testing.T) {
	for _, symlinked := range []string{"threshold-pprof", "profiles"} {
		t.Run(symlinked, func(t *testing.T) {
			evidenceDir := t.TempDir()
			external := t.TempDir()
			profileRoot := filepath.Join(evidenceDir, "threshold-pprof")
			if symlinked == "threshold-pprof" {
				profileRoot = external
				if err := os.Symlink(external, filepath.Join(evidenceDir, "threshold-pprof")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(profileRoot, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, filepath.Join(profileRoot, "profiles")); err != nil {
					t.Fatal(err)
				}
			}
			blobsDir := filepath.Join(profileRoot, "profiles")
			if symlinked == "threshold-pprof" {
				if err := os.Mkdir(blobsDir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			previous := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
			trigger := ProfileThresholdTrigger{
				Kind: ProfileTriggerActualOfferedRatio, PreviousAt: previous, CurrentAt: previous.Add(time.Second),
			}
			metadata := completeSingleNodeProfileMetadata(trigger)
			writeProfileJSON(t, filepath.Join(profileRoot, "metadata.json"), metadata)
			for _, name := range []string{"node-1-cpu.pb.gz", "node-1-heap.pb.gz", "node-1-goroutine.txt"} {
				if err := os.WriteFile(filepath.Join(blobsDir, name), []byte("external-profile"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			exitStatus := 0
			writeProfileJSON(t, filepath.Join(evidenceDir, "threshold-pprof-status.json"), ProfileEvidence{
				Schema: ProfileEvidenceSchema, Status: "complete", EvidenceComplete: true, CaptureValid: true,
				Reason: "ok", Triggered: true, Trigger: &trigger, Metadata: "threshold-pprof/metadata.json",
				HelperExitStatus: &exitStatus,
			})

			if _, err := ReadSingleNodeProfileEvidence(filepath.Join(evidenceDir, "threshold-pprof-status.json")); err == nil {
				t.Fatalf("accepted %s symlink substitution", symlinked)
			}
		})
	}
}

func TestParseSingleNodeProfileEvidenceRequiresCompleteBlobFromPartialCapture(t *testing.T) {
	previous := time.Date(2026, 8, 13, 1, 2, 3, 0, time.UTC)
	trigger := ProfileThresholdTrigger{
		Kind: ProfileTriggerActualOfferedRatio, PreviousAt: previous, CurrentAt: previous.Add(time.Second),
	}
	exitStatus := 0
	status := ProfileEvidence{
		Schema: ProfileEvidenceSchema, Status: "partial", Reason: "profile_capture_missing",
		Triggered: true, Trigger: &trigger, Metadata: "threshold-pprof/metadata.json", HelperExitStatus: &exitStatus,
	}
	metadata := completeSingleNodeProfileMetadata(trigger)
	metadata.Capture.Status = "partial"
	metadata.Capture.Valid = false
	metadata.Capture.Reason = "profile_capture_missing"
	metadata.Nodes[0].Heap = "missing"
	metadata.Nodes[0].Goroutine = "missing"
	statusData, _ := json.Marshal(status)
	metadataData, _ := json.Marshal(metadata)
	read := func(includeCPU bool) ArtifactReader {
		return func(relative string, _ int64) ([]byte, error) {
			switch relative {
			case "threshold-pprof/metadata.json":
				return metadataData, nil
			case "threshold-pprof/profiles/node-1-cpu.pb.gz":
				if includeCPU {
					return []byte("sealed-cpu-profile"), nil
				}
			}
			return nil, ErrAuthenticatedArtifactMissing
		}
	}
	if _, err := ParseSingleNodeProfileEvidence(bytes.NewReader(statusData), read(false)); err == nil {
		t.Fatal("partial capture accepted a declared-complete blob absent from the sealed manifest")
	}
	got, err := ParseSingleNodeProfileEvidence(bytes.NewReader(statusData), read(true))
	if err != nil || got.Status != "partial" || got.EvidenceComplete || got.CaptureValid {
		t.Fatalf("partial one-blob evidence = %+v, %v", got, err)
	}
}

func completeSingleNodeProfileMetadata(trigger ProfileThresholdTrigger) thresholdProfileMetadata {
	var metadata thresholdProfileMetadata
	metadata.Schema = "wukongim.local_threshold_pprof/v1"
	metadata.Trigger.Kind = trigger.Kind
	metadata.Trigger.ObservedPhase = "measurement"
	metadata.Trigger.PreviousUTC = trigger.PreviousAt
	metadata.Trigger.CurrentUTC = trigger.CurrentAt
	metadata.Capture.Status = "complete"
	metadata.Capture.Valid = true
	metadata.Capture.Reason = "ok"
	metadata.Capture.StartPhase = "measurement"
	metadata.Capture.EndPhase = "measurement"
	metadata.Capture.StartedAtUTC = trigger.CurrentAt
	metadata.Capture.CompletedAtUTC = trigger.CurrentAt.Add(time.Second)
	metadata.Capture.CPUSeconds = 1
	metadata.Nodes = append(metadata.Nodes, struct {
		Node      string `json:"node"`
		CPU       string `json:"cpu"`
		Heap      string `json:"heap"`
		Goroutine string `json:"goroutine"`
	}{Node: "node-1", CPU: "complete", Heap: "complete", Goroutine: "complete"})
	return metadata
}

func writeProfileJSON(t *testing.T, path string, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func profileLifecycleCapture(at time.Time, activePhase, runID string, traffic TrafficEvidence) LifecycleCapture {
	phase := activePhase
	if activePhase == "run" {
		phase = "warmup"
	} else if activePhase == "cooldown" {
		phase = "run"
	}
	return LifecycleCapture{
		Schema: LifecycleCaptureSchema, SampledAt: at,
		Status: &CapturedStatus{
			Phase: phase, ActivePhase: activePhase, ObservedAt: at,
			Lifecycle:  &CapturedLifecycleStatus{ActiveConnections: 2500, Traffic: traffic},
			Assignment: CapturedAssignment{RunID: runID, AssignmentID: "assignment-1"},
		},
		Server: ProcessEvidence{PID: 101, StartToken: "server", Alive: true},
		Worker: ProcessEvidence{PID: 202, StartToken: "worker", Alive: true},
	}
}

func profileTraffic(sent, sendACKs, terminal, correctness uint64) TrafficEvidence {
	return TrafficEvidence{
		Planned: sent, Dispatched: sent, LogicalSent: sent, SendAttempts: sent,
		SendACKs: sendACKs, TerminalErrors: terminal, CorrectnessErrors: correctness,
		Remaining: sent - sendACKs - terminal, StableClientMsgNo: true,
		RetryEvidenceComplete: true, MaximumRetriesPerMessage: 3,
	}
}
