package chatlifecycle

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Failure inventory: private diagnostics escape active snapshots, malformed
// optional data poisons native evidence, or fixed retention exceeds terminal
// budgets. Existing Evidence v3 and its five-field samples must not change.
func driverValidSnapshotFixture() WorkerSnapshot {
	return WorkerSnapshot{RunID: "driver-run", AssignmentID: "driver-assignment", Phase: WorkerPhaseFinal, Generation: 1, WorkerID: 0, WorkerCount: 3, DriverDiagnostics: &DriverDiagnosticsSnapshot{Generation: 1, Records: []DriverDeadlineMiss{{Turn: 1, Operation: "advance", Code: FailureCode(38), ClockValid: true, DeadlineAfterDue: 100 * time.Millisecond, FreshAfterDue: 101 * time.Millisecond}}}}
}
func TestDriverDiagnosticsPrivateProtocolRoundTripAndSanitization(t *testing.T) {
	if workerProtocolVersion != 15 {
		t.Fatalf("private protocol=%d want15", workerProtocolVersion)
	}
	s := driverValidSnapshotFixture()
	body, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WorkerSnapshot
	if !decodeStrictWorkerResponse(body, &decoded) || !validTypedWorkerResponse(&decoded) || decoded.DriverDiagnostics == nil {
		t.Fatal("valid final diagnostics did not cross strict private protocol")
	}
	for _, mutate := range []func(*WorkerSnapshot){
		func(s *WorkerSnapshot) { s.Phase = WorkerPhaseRunning },
		func(s *WorkerSnapshot) { s.DriverDiagnostics.Generation++ },
		func(s *WorkerSnapshot) { s.DriverDiagnostics.Records[0].Code = 37 },
		func(s *WorkerSnapshot) { s.DriverDiagnostics.Records[0].Operation = "raw-private-identity" },
		func(s *WorkerSnapshot) { s.DriverDiagnostics.Records[0].Turn = 0 },
		func(s *WorkerSnapshot) { s.DriverDiagnostics.Records = make([]DriverDeadlineMiss, 17) },
	} {
		s := driverValidSnapshotFixture()
		mutate(&s)
		if !validTypedWorkerResponse(&s) || s.DriverDiagnostics != nil {
			t.Fatalf("malformed optional diagnostics changed native outcome or were retained: %+v", s)
		}
	}
}
func TestDriverDiagnosticsTerminalSidecarKeepsOriginalEvidence(t *testing.T) {
	directory := t.TempDir()
	at := time.Unix(1700000000, 0)
	recorder := newLiveDiagnosticRecorder(directory, "driver-run", at, nil, LatencyLimit{})
	workers := make([]WorkerSnapshot, 3)
	for i := range workers {
		workers[i] = driverValidSnapshotFixture()
		workers[i].WorkerID = uint64(i)
	}
	if err := recorder.writeTerminalWorkerEvidence(at, workers); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(directory, "worker-evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(original), "driver_diagnostics") || !strings.Contains(string(original), "wukongim/chat-lifecycle-worker-evidence/v3") {
		t.Fatalf("native evidence changed: %s", original)
	}
	sidecar, err := os.ReadFile(filepath.Join(directory, "driver-diagnostics.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sidecar) > 64<<10 || !strings.Contains(string(sidecar), "wukongim/chat-lifecycle-driver-diagnostics/v1") {
		t.Fatalf("invalid sidecar bytes=%d", len(sidecar))
	}
	for _, private := range []string{"driver-private-channel", "client_msg_no", "sender", "payload", "raw_error"} {
		if strings.Contains(string(sidecar), private) {
			t.Fatalf("private field %q leaked", private)
		}
	}
	// Optional diagnostic write failure cannot replace native terminal outcome.
	if err := os.Remove(filepath.Join(directory, "driver-diagnostics.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "driver-diagnostics.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := recorder.writeTerminalWorkerEvidence(at, workers); err != nil {
		t.Fatalf("optional diagnostic I/O altered native terminal outcome: %v", err)
	}
}
func TestDriverDiagnosticsMaximumEnvelopeIsBounded(t *testing.T) {
	directory := t.TempDir()
	at := time.Unix(1700000000, 0)
	recorder := newLiveDiagnosticRecorder(directory, "driver-run", at, nil, LatencyLimit{})
	workers := make([]WorkerSnapshot, 3)
	for i := range workers {
		s := driverValidSnapshotFixture()
		s.WorkerID = uint64(i)
		s.DriverDiagnostics.Dropped = math.MaxUint64
		s.DriverDiagnostics.Records = make([]DriverDeadlineMiss, 16)
		for j := range s.DriverDiagnostics.Records {
			r := &s.DriverDiagnostics.Records[j]
			fillEvidenceBudgetScalars(reflect.ValueOf(r).Elem())
			r.Turn = uint64(j + 1)
			r.Operation = "advance"
			r.Code = 38
			r.ClockValid = false
		}
		workers[i] = s
		body, _ := json.Marshal(s)
		if len(body) > 256<<10 {
			t.Fatalf("worker bytes=%d", len(body))
		}
	}
	if err := recorder.writeTerminalWorkerEvidence(at, workers); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(directory, "driver-diagnostics.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > 64<<10 {
		t.Fatalf("three-worker bounded sidecar=%d bytes", len(body))
	}
	t.Logf("maximum-width diagnostic sidecar=%d bytes", len(body))
}

// Review regression: disabled workers must preserve terminal behavior exactly;
// a null-only sidecar is still extra allocation and filesystem activity.
func TestDriverDiagnosticsDisabledTerminalExportHasNoSideEffect(t *testing.T) {
	at := time.Unix(1700000000, 0)
	workers := make([]WorkerSnapshot, 3)
	for i := range workers {
		workers[i] = driverValidSnapshotFixture()
		workers[i].WorkerID = uint64(i)
		workers[i].DriverDiagnostics = nil
	}
	directory := t.TempDir()
	recorder := newLiveDiagnosticRecorder(directory, "driver-run", at, nil, LatencyLimit{})
	if err := recorder.writeTerminalWorkerEvidence(at, workers); err != nil {
		t.Fatal(err)
	}
	native, err := os.ReadFile(filepath.Join(directory, "worker-evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(native), "wukongim/chat-lifecycle-worker-evidence/v3") || strings.Contains(string(native), "driver_diagnostics") {
		t.Fatalf("disabled native evidence changed: %s", native)
	}
	if _, err := os.Stat(filepath.Join(directory, "driver-diagnostics.json")); !os.IsNotExist(err) {
		t.Fatalf("disabled terminal export added sidecar: stat=%v", err)
	}
	// The nonexistent parent makes any attempted writer call observable as an
	// error; no global writer callback/test hook is introduced.
	blocked := newLiveDiagnosticRecorder(filepath.Join(directory, "absent"), "driver-run", at, nil, LatencyLimit{})
	if err := blocked.writeTerminalDriverDiagnostics(at, workers); err != nil {
		t.Fatalf("disabled optional export attempted filesystem activity: %v", err)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		if err := blocked.writeTerminalDriverDiagnostics(at, workers); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatalf("disabled optional export allocated %v objects", allocations)
	}
}
