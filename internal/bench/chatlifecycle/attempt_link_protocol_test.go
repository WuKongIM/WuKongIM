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

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

func attemptLinkSnapshotFixture() WorkerSnapshot {
	return WorkerSnapshot{RunID: "attempt-run", AssignmentID: "attempt-assignment", Phase: WorkerPhaseRunning, Generation: 1, WorkerID: 0, WorkerCount: 3,
		AttemptLinkDiagnostics: &AttemptLinkDiagnosticsSnapshot{Generation: 1, Records: []AttemptLinkRecord{{Ordinal: 1, Key: sendtrace.AttemptKey{MessageFingerprint: [16]byte{1}, ClientSeq: 1, ConnectFingerprint: [16]byte{2}}, Kind: "sendack_success", Attribution: "candidate", DeadlineAfterRegistration: time.Second, ObservedAfterRegistration: time.Second}}}}
}
func TestAttemptLinkV15OptionalLiveAndFinalDiagnosticSanitizesIndependently(t *testing.T) {
	if ReportSchemaVersion != "wukongim/chat-lifecycle-report/v8" {
		t.Fatal("attempt diagnostics changed native report schema")
	}
	if workerProtocolVersion != 15 {
		t.Fatalf("private protocol=%d want15", workerProtocolVersion)
	}
	for _, phase := range []WorkerPhase{WorkerPhaseRunning, WorkerPhaseFinal} {
		s := attemptLinkSnapshotFixture()
		s.Phase = phase
		body, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var got WorkerSnapshot
		if !decodeStrictWorkerResponse(body, &got) || !validTypedWorkerResponse(&got) || got.AttemptLinkDiagnostics == nil {
			t.Fatalf("valid %s prefix did not round trip", phase)
		}
	}
	for _, mutate := range []func(*WorkerSnapshot){
		func(s *WorkerSnapshot) { s.AttemptLinkDiagnostics.Generation++ },
		func(s *WorkerSnapshot) { s.AttemptLinkDiagnostics.Records[0].Ordinal = 0 },
		func(s *WorkerSnapshot) { s.AttemptLinkDiagnostics.Records[0].Kind = "private-raw-error" },
		func(s *WorkerSnapshot) { s.AttemptLinkDiagnostics.Records[0].Key.ClientSeq = 0 },
		func(s *WorkerSnapshot) { s.AttemptLinkDiagnostics.Records[0].ObservedAfterRegistration = -1 },
		func(s *WorkerSnapshot) { s.AttemptLinkDiagnostics.Records = make([]AttemptLinkRecord, 17) },
	} {
		s := attemptLinkSnapshotFixture()
		native := s.Messages
		mutate(&s)
		if !validTypedWorkerResponse(&s) || s.AttemptLinkDiagnostics != nil || !reflect.DeepEqual(native, s.Messages) {
			t.Fatalf("invalid optional evidence poisoned native outcome: %+v", s)
		}
	}
}
func TestAttemptLinkMaximumEncodingFitsEveryExistingEnvelope(t *testing.T) {
	s := attemptLinkSnapshotFixture()
	fillEvidenceBudgetScalars(reflect.ValueOf(&s).Elem())
	s.RunID = strings.Repeat("\x01", 128)
	s.AssignmentID = s.RunID
	s.Generation = math.MaxUint64
	s.Phase = WorkerPhaseFinal
	s.WorkerID = 0
	s.WorkerCount = 3
	s.Messages.Terminal = 0
	s.Messages.TerminalReasons = TerminalSendSnapshot{}
	s.AttemptLinkDiagnostics = &AttemptLinkDiagnosticsSnapshot{Generation: s.Generation, Dropped: math.MaxUint64, Records: make([]AttemptLinkRecord, 16)}
	for i := range s.AttemptLinkDiagnostics.Records {
		r := &s.AttemptLinkDiagnostics.Records[i]
		fillEvidenceBudgetScalars(reflect.ValueOf(r).Elem())
		r.Ordinal = uint64(i + 1)
		r.Kind = "transport_error"
		r.Attribution = "connection_unknown"
		r.TimingMask = 3 // Largest valid two-bit diagnostic timing mask.
	}
	body, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(body)+1 > 256<<10 || int64(len(body)+1) > workerMaxResponseBytes || workerMaxResponseBytes != 4<<20 {
		t.Fatalf("new diagnostics expanded native caps: %d/%d", len(body)+1, workerMaxResponseBytes)
	}
	directory := t.TempDir()
	at := time.Unix(1700000000, 0)
	recorder := newLiveDiagnosticRecorder(directory, s.RunID, at, nil, LatencyLimit{})
	workers := make([]WorkerSnapshot, 3)
	for i := range workers {
		workers[i] = s
		workers[i].WorkerID = uint64(i)
	}
	if err = recorder.writeTerminalWorkerEvidence(at, workers); err != nil {
		t.Fatal(err)
	}
	native, err := os.ReadFile(filepath.Join(directory, "worker-evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(native) > 1<<20 || !strings.Contains(string(native), "wukongim/chat-lifecycle-worker-evidence/v3") || strings.Contains(string(native), "attempt_link_diagnostics") {
		t.Fatalf("native Evidencev3 contaminated/over cap: %d", len(native))
	}
	if err = recorder.writeAttemptLinkDiagnostics(at, workers, false); err != nil {
		t.Fatal(err)
	}
	sidecar, err := os.ReadFile(filepath.Join(directory, "attempt-link-diagnostics.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sidecar) > 64<<10 {
		t.Fatalf("shrink optional diagnostic, do not expand 64KiB cap: %d", len(sidecar))
	}
	for _, raw := range []string{"client_msg_no", "uid", "channel_id", "payload", "token", "raw_error"} {
		if strings.Contains(string(sidecar), raw) {
			t.Fatalf("raw identity leaked: %s", raw)
		}
	}
}
func TestAttemptLinkLiveSidecarAvailableBeforeStopAndNativeEvidenceUnchanged(t *testing.T) {
	directory := t.TempDir()
	at := time.Unix(1700000000, 0)
	recorder := newLiveDiagnosticRecorder(directory, "attempt-run", at, nil, LatencyLimit{})
	workers := make([]WorkerSnapshot, 3)
	for i := range workers {
		workers[i] = attemptLinkSnapshotFixture()
		workers[i].WorkerID = uint64(i)
	}
	before, _ := json.Marshal(workers[0].Messages)
	if err := recorder.writeAttemptLinkDiagnostics(at, workers, true); err != nil {
		t.Fatal(err)
	}
	live, err := os.ReadFile(filepath.Join(directory, "attempt-link-diagnostics.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(live), "sendack_success") {
		t.Fatal("terminal-only prefix cannot trigger early exact lookup")
	}
	after, _ := json.Marshal(workers[0].Messages)
	if string(before) != string(after) {
		t.Fatal("sidecar changed native counters")
	}
}

func TestAttemptLinkMalformedOptionalWireIsDiscardedWithoutWeakeningNativeStrictness(t *testing.T) {
	valid := attemptLinkSnapshotFixture()
	valid.Messages.FirstAttempts = 7
	base, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	for _, optional := range []string{
		`"not-an-object"`,
		`{"generation":1,"records":"not-an-array"}`,
		`{"generation":1,"records":[],"private_unknown_field":"must-not-escape"}`,
		`{"generation":1,"records":[{"ordinal":1,"key":{"message_fingerprint":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17],"client_seq":1,"connect_packet_fingerprint":[1]},"kind":"sendack_success","attribution":"candidate"}]}`,
	} {
		var shape map[string]json.RawMessage
		if err = json.Unmarshal(base, &shape); err != nil {
			t.Fatal(err)
		}
		shape["attempt_link_diagnostics"] = json.RawMessage(optional)
		body, _ := json.Marshal(shape)
		var got WorkerSnapshot
		if !decodeStrictWorkerResponse(body, &got) || !validTypedWorkerResponse(&got) || got.AttemptLinkDiagnostics != nil || got.Messages.FirstAttempts != 7 {
			t.Fatalf("malformed optional field changed native result: %s", body)
		}
	}
	// Only the new named optional field is isolated. Unknown native fields
	// remain a private-protocol violation rather than a silent compatibility mode.
	var shape map[string]json.RawMessage
	_ = json.Unmarshal(base, &shape)
	shape["unknown_native_field"] = json.RawMessage(`true`)
	body, _ := json.Marshal(shape)
	var got WorkerSnapshot
	if decodeStrictWorkerResponse(body, &got) {
		t.Fatal("optional isolation weakened native strict decoding")
	}
}
