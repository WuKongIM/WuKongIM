package chatlifecycle

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveDiagnosticRecorderPersistsCurrentWorkersAndBoundedChangeLog(t *testing.T) {
	outputDir := t.TempDir()
	start := time.Unix(1_965_000_000, 0).UTC()
	fence := WorkerFence{RunID: "live-run", AssignmentID: "live-assignment", Generation: 1}
	var diagnosticLog bytes.Buffer
	recorder := newLiveDiagnosticRecorder(outputDir, fence.RunID, start, &diagnosticLog)
	snapshots := coordinatorSnapshotFixture(fence, 1, time.Minute, 1)
	for index := range snapshots {
		snapshots[index].Phase = WorkerPhaseRunning
		snapshots[index].Sessions.Target = 1
		snapshots[index].Sessions.Online = 1
		snapshots[index].Sessions.TrafficReady = 1
		snapshots[index].Sessions.CloseReasons = SessionCloseReasonSnapshot{}
	}
	if err := recorder.Observe(start.Add(5*time.Second), CoordinatorCutPeriodic, snapshots); err != nil {
		t.Fatalf("first Observe() error = %v", err)
	}
	snapshots[2].SnapshotSequence++
	snapshots[2].Sessions.Online = 0
	snapshots[2].Sessions.TrafficReady = 0
	snapshots[2].Sessions.CloseReasons.HeartbeatFailed = 1
	if err := recorder.Observe(start.Add(10*time.Second), CoordinatorCutPeriodic, snapshots); err != nil {
		t.Fatalf("second Observe() error = %v", err)
	}

	body, err := os.ReadFile(filepath.Join(outputDir, LiveDiagnosticStatusFile))
	if err != nil {
		t.Fatal(err)
	}
	var document liveDiagnosticStatus
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	if document.Schema != LiveDiagnosticStatusSchemaV1 || document.RunID != fence.RunID || document.State != liveDiagnosticRunning ||
		document.Stage != liveDiagnosticMeasured || document.Totals.Online != 2 || document.CloseReasons.HeartbeatFailed != 1 ||
		len(document.Workers) != coordinatorWorkerCount || len(document.RecentEvents) != 5 {
		t.Fatalf("diagnostic status = %+v", document)
	}
	last := document.RecentEvents[len(document.RecentEvents)-1]
	if last.WorkerID != 2 || last.Kind != liveDiagnosticCloseReasonsChanged || last.CloseReasons.HeartbeatFailed != 1 {
		t.Fatalf("last diagnostic event = %+v", last)
	}
	if len(body) > maxLiveDiagnosticStatusBytes {
		t.Fatalf("diagnostic status bytes = %d", len(body))
	}
	if logBody := diagnosticLog.String(); !strings.Contains(logBody, `"event":"wkbench.chat_lifecycle.worker_status_cut"`) ||
		!strings.Contains(logBody, `"heartbeat_failed":1`) || strings.Contains(logBody, `"uid"`) {
		t.Fatalf("diagnostic log = %q", logBody)
	}
}
