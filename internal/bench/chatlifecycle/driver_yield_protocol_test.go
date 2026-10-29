package chatlifecycle

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The failure inventory predates these tests. JSON and reflection keep the old
// source executable: missing optional evidence is a diagnostic contract RED,
// never a claim that a new product failure was reproduced.
type driverYieldTestWall struct {
	Count      uint64 `json:"count"`
	WallNanos  int64  `json:"wall_nanos"`
	ClockValid bool   `json:"clock_valid"`
}

type driverYieldTestRecord struct {
	Turn       uint64               `json:"turn"`
	Processed  uint64               `json:"processed"`
	ClockValid bool                 `json:"clock_valid"`
	Residual   time.Duration        `json:"residual"`
	YieldWall  *driverYieldTestWall `json:"yield_wall"`
}

type driverYieldTestSnapshot struct {
	Generation uint64                  `json:"generation"`
	Dropped    uint64                  `json:"dropped"`
	Records    []driverYieldTestRecord `json:"records"`
}

func driverYieldTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func driverYieldTestWire(t *testing.T, snapshot WorkerSnapshot, child any) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(driverYieldTestJSON(t, snapshot), &fields); err != nil {
		t.Fatal(err)
	}
	var driver map[string]json.RawMessage
	if err := json.Unmarshal(fields["driver_diagnostics"], &driver); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(driver["records"], &rows); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		row["yield_wall"] = driverYieldTestJSON(t, child)
	}
	driver["records"] = driverYieldTestJSON(t, rows)
	fields["driver_diagnostics"] = driverYieldTestJSON(t, driver)
	return driverYieldTestJSON(t, fields)
}

func driverYieldTestDecode(t *testing.T, snapshot *DriverDiagnosticsSnapshot) driverYieldTestSnapshot {
	t.Helper()
	var out driverYieldTestSnapshot
	if snapshot == nil || json.Unmarshal(driverYieldTestJSON(t, snapshot), &out) != nil {
		t.Fatal("joined original driver diagnostics are absent")
	}
	return out
}

func TestDriverYieldProtocol15LegacyAbsenceAndKnownOptionalRoundTrip(t *testing.T) {
	if workerProtocolVersion != 15 || ReportSchemaVersion != "wukongim/chat-lifecycle-report/v7" {
		t.Fatal("yield observation changed the native protocol/report version")
	}
	legacy := driverValidSnapshotFixture()
	var absent WorkerSnapshot
	if !decodeStrictWorkerResponse(driverYieldTestJSON(t, legacy), &absent) || !validTypedWorkerResponse(&absent) || absent.DriverDiagnostics == nil {
		t.Fatal("new reader stopped accepting legacy absent yield diagnostics")
	}
	if got := driverYieldTestDecode(t, absent.DriverDiagnostics).Records[0].YieldWall; got != nil {
		t.Fatalf("legacy absence was invented as known zero: %+v", got)
	}
	legacy.DriverDiagnostics.Records[0].Processed = 40
	want := driverYieldTestWall{Count: 1, WallNanos: 45_863_372, ClockValid: true}
	body := driverYieldTestWire(t, legacy, want)
	var got WorkerSnapshot
	if !decodeStrictWorkerResponse(body, &got) || !validTypedWorkerResponse(&got) || got.DriverDiagnostics == nil {
		t.Fatal("diagnostic contract RED: same-source v15 reader rejected a valid optional yield_wall")
	}
	actual := driverYieldTestDecode(t, got.DriverDiagnostics).Records[0].YieldWall
	if actual == nil || *actual != want {
		t.Fatalf("valid yield wall disappeared or changed: got=%+v want=%+v", actual, want)
	}
	if !reflect.DeepEqual(got.Messages, legacy.Messages) || !reflect.DeepEqual(got.Evidence, legacy.Evidence) {
		t.Fatal("optional yield changed original native counters/evidence")
	}
}

func TestDriverYieldOptionalMalformedChildCannotPoisonNativeEvidence(t *testing.T) {
	for _, child := range []any{
		nil, "raw-private-error", []int{1},
		map[string]any{},
		map[string]any{"count": 1, "wall_nanos": 4},
		map[string]any{"count": nil, "wall_nanos": 0, "clock_valid": true},
		map[string]any{"count": 1, "wall_nanos": nil, "clock_valid": true},
		map[string]any{"count": 1, "wall_nanos": 0, "clock_valid": nil},
		map[string]any{"count": 1, "wall_nanos": 0, "clock_valid": "true"},
		map[string]any{"count": -1, "wall_nanos": 4, "clock_valid": true},
		map[string]any{"count": json.Number("18446744073709551616"), "wall_nanos": 4, "clock_valid": true},
		map[string]any{"count": 1.5, "wall_nanos": 4, "clock_valid": true},
		map[string]any{"count": "1", "wall_nanos": 4, "clock_valid": true},
		map[string]any{"count": 1, "wall_nanos": -1, "clock_valid": true},
		map[string]any{"count": 1, "wall_nanos": uint64(math.MaxInt64) + 1, "clock_valid": true},
		map[string]any{"count": 1, "wall_nanos": "4", "clock_valid": true},
		map[string]any{"count": 0, "wall_nanos": 4, "clock_valid": true},
		map[string]any{"count": 2, "wall_nanos": 4, "clock_valid": true},
		map[string]any{"count": 1, "wall_nanos": 4, "clock_valid": false},
		map[string]any{"count": 1, "wall_nanos": 4, "clock_valid": true, "actor_cpu": 4},
	} {
		t.Run(string(driverYieldTestJSON(t, child)), func(t *testing.T) {
			s := driverValidSnapshotFixture()
			s.DriverDiagnostics.Records[0].Processed = 40
			var got WorkerSnapshot
			if !decodeStrictWorkerResponse(driverYieldTestWire(t, s, child), &got) || !validTypedWorkerResponse(&got) || got.DriverDiagnostics == nil {
				t.Fatal("malformed optional yield poisoned original native/driver evidence")
			}
			if row := driverYieldTestDecode(t, got.DriverDiagnostics).Records[0]; row.YieldWall != nil || row.Turn != 1 || row.Processed != 40 {
				t.Fatalf("malformed yield was retained or erased original prefix: %+v", row)
			}
			if !reflect.DeepEqual(got.Messages, s.Messages) || !reflect.DeepEqual(got.Evidence, s.Evidence) {
				t.Fatal("invalid optional child altered native counters")
			}
		})
	}
	// Native strictness must remain independent of named optional sanitization.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(driverYieldTestJSON(t, driverValidSnapshotFixture()), &fields); err != nil {
		t.Fatal(err)
	}
	fields["unreviewed_native_field"] = json.RawMessage(`1`)
	var got WorkerSnapshot
	if decodeStrictWorkerResponse(driverYieldTestJSON(t, fields), &got) {
		t.Fatal("optional support weakened unknown native field rejection")
	}
	// Isolating the one named child cannot loosen the surrounding driver body.
	delete(fields, "unreviewed_native_field")
	var driver map[string]json.RawMessage
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(fields["driver_diagnostics"], &driver); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(driver["records"], &rows); err != nil {
		t.Fatal(err)
	}
	rows[0]["unexpected_base_key"] = json.RawMessage(`1`)
	driver["records"] = driverYieldTestJSON(t, rows)
	fields["driver_diagnostics"] = driverYieldTestJSON(t, driver)
	if decodeStrictWorkerResponse(driverYieldTestJSON(t, fields), &got) {
		t.Fatal("named yield support weakened unknown base-driver field rejection")
	}
}

func TestDriverYieldOptionalUnknownIsDistinctFromObservedZero(t *testing.T) {
	for _, wall := range []driverYieldTestWall{{Count: 0, ClockValid: true}, {Count: 1, ClockValid: true}, {Count: 1, ClockValid: false}} {
		s := driverValidSnapshotFixture()
		s.DriverDiagnostics.Records[0].Processed = 40
		s.DriverDiagnostics.Records[0].ClockValid = false // Independent clock quality.
		var got WorkerSnapshot
		if !decodeStrictWorkerResponse(driverYieldTestWire(t, s, wall), &got) || !validTypedWorkerResponse(&got) || got.DriverDiagnostics == nil {
			t.Fatal("bounded observed-zero/unknown yield did not round trip")
		}
		row := driverYieldTestDecode(t, got.DriverDiagnostics).Records[0]
		if row.YieldWall == nil || *row.YieldWall != wall || row.ClockValid {
			t.Fatalf("independent yield quality was conflated with original clock quality: %+v", row)
		}
	}
}

func TestDriverYieldMaximumEnvelopeAndTerminalExportKeepExistingCaps(t *testing.T) {
	workers := make([]WorkerSnapshot, 3)
	for i := range workers {
		s := driverValidSnapshotFixture()
		s.WorkerID = uint64(i)
		s.DriverDiagnostics.Dropped = math.MaxUint64
		s.DriverDiagnostics.Records = make([]DriverDeadlineMiss, 16)
		for j := range s.DriverDiagnostics.Records {
			r := &s.DriverDiagnostics.Records[j]
			fillEvidenceBudgetScalars(reflect.ValueOf(r).Elem())
			r.Turn, r.Processed = uint64(j+1), math.MaxUint64
			r.Code, r.Operation, r.ClockValid = 38, "advance", false
		}
		child := driverYieldTestWall{Count: math.MaxUint64 / 32, WallNanos: math.MaxInt64, ClockValid: true}
		body := driverYieldTestWire(t, s, child)
		if len(body) > 256<<10 || int64(len(body)) > workerMaxResponseBytes || workerMaxResponseBytes != 4<<20 {
			t.Fatalf("optional yield requires expanded worker caps: bytes=%d", len(body))
		}
		if !decodeStrictWorkerResponse(body, &workers[i]) || !validTypedWorkerResponse(&workers[i]) || workers[i].DriverDiagnostics == nil {
			t.Fatal("maximum-width valid yield did not cross unchanged worker envelope")
		}
		if driverYieldTestDecode(t, workers[i].DriverDiagnostics).Records[0].YieldWall == nil {
			t.Fatal("maximum-width test silently omitted the proposed optional pointer")
		}
	}
	at := time.Unix(1700000000, 0)
	directory := t.TempDir()
	recorder := newLiveDiagnosticRecorder(directory, "driver-run", at, nil, LatencyLimit{})
	if err := recorder.writeTerminalWorkerEvidence(at, workers); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(directory, "driver-diagnostics.json"))
	if err != nil || len(body) > 64<<10 || bytes.Count(body, []byte(`"yield_wall"`)) != 48 {
		t.Fatalf("terminal sidecar cap/present48 changed: bytes=%d err=%v", len(body), err)
	}
	native, err := os.ReadFile(filepath.Join(directory, "worker-evidence.json"))
	if err != nil || strings.Contains(string(native), "yield_wall") || !strings.Contains(string(native), "wukongim/chat-lifecycle-worker-evidence/v3") {
		t.Fatal("yield escaped the optional terminal sidecar")
	}
}

func TestDriverYieldTypedAndTerminalSanitizeOnlyInvalidChild(t *testing.T) {
	valid := func() WorkerSnapshot {
		s := driverValidSnapshotFixture()
		s.DriverDiagnostics.Records[0].Processed = 40
		var out WorkerSnapshot
		if !decodeStrictWorkerResponse(driverYieldTestWire(t, s, driverYieldTestWall{Count: 1, WallNanos: 50, ClockValid: true}), &out) || out.DriverDiagnostics == nil {
			t.Fatal("diagnostic contract RED: valid named child unavailable")
		}
		return out
	}
	mutate := func(snapshot *WorkerSnapshot, field string, value int64) {
		yield := reflect.ValueOf(snapshot.DriverDiagnostics).Elem().FieldByName("Records").Index(0).FieldByName("YieldWall")
		if !yield.IsValid() || yield.IsNil() {
			t.Fatal("optional YieldWall absent from valid decoded record")
		}
		target := yield.Elem().FieldByName(field)
		if target.Kind() == reflect.Uint64 {
			target.SetUint(uint64(value))
		} else {
			target.SetInt(value)
		}
	}
	bad := valid()
	mutate(&bad, "Count", 2) // Cannot occur within processed40.
	nativeBefore := driverYieldTestJSON(t, bad.Messages)
	if !validTypedWorkerResponse(&bad) || bad.DriverDiagnostics == nil {
		t.Fatal("typed invalid child erased original driver/native evidence")
	}
	if row := driverYieldTestDecode(t, bad.DriverDiagnostics).Records[0]; row.YieldWall != nil || row.Turn != 1 || row.Processed != 40 {
		t.Fatalf("typed sanitizer retained bad child or erased original row: %+v", row)
	}
	if !bytes.Equal(nativeBefore, driverYieldTestJSON(t, bad.Messages)) {
		t.Fatal("typed child sanitizer altered native counters")
	}
	workers := []WorkerSnapshot{valid(), valid(), valid()}
	for i := range workers {
		workers[i].WorkerID = uint64(i)
	}
	mutate(&workers[0], "WallNanos", -1)
	before := driverYieldTestJSON(t, workers)
	directory := t.TempDir()
	at := time.Unix(1700000000, 0)
	recorder := newLiveDiagnosticRecorder(directory, "driver-run", at, nil, LatencyLimit{})
	if err := recorder.writeTerminalWorkerEvidence(at, workers); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, driverYieldTestJSON(t, workers)) {
		t.Fatal("terminal sanitization mutated the supplied detached worker snapshots")
	}
	body, err := os.ReadFile(filepath.Join(directory, "driver-diagnostics.json"))
	if err != nil {
		t.Fatal(err)
	}
	var terminal struct {
		Workers []struct {
			Driver *driverYieldTestSnapshot `json:"driver_diagnostics"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(body, &terminal); err != nil || len(terminal.Workers) != 3 {
		t.Fatal("terminal diagnostic envelope malformed")
	}
	for i, worker := range terminal.Workers {
		if worker.Driver == nil || len(worker.Driver.Records) != 1 || worker.Driver.Records[0].Turn != 1 {
			t.Fatal("terminal invalid child erased original driver row")
		}
		child := worker.Driver.Records[0].YieldWall
		if (child == nil) != (i == 0) || (child != nil && child.Count != 1) {
			t.Fatalf("terminal sanitizer poisoned sibling or retained invalid child: worker%d %+v", i, child)
		}
	}
}
