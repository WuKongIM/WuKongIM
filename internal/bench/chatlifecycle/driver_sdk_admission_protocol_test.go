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

// The frozen failure inventory and root's design selection predate this file.
// Missing typed DTOs are an explicit API RED, not a new native product failure.
func sdkAdmissionValidChild() *DriverSDKAdmissionSnapshot {
	return &DriverSDKAdmissionSnapshot{Version: 1, Emitter: "sdk_start", Code: 38,
		Records: []DriverSDKAdmissionMiss{{Ordinal: 1, KnownMask: 15,
			SampledAfterDue: 20 * time.Millisecond, DeadlineAfterDue: 100 * time.Millisecond,
			FreshAfterDue: 101 * time.Millisecond, QueuedToPickup: 11, PickupToOffer: 12, OfferToOwner: 13}}}
}

func sdkAdmissionWire(t *testing.T, snapshot WorkerSnapshot, child any) []byte {
	t.Helper()
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(driverYieldTestJSON(t, snapshot), &outer); err != nil {
		t.Fatal(err)
	}
	var driver map[string]json.RawMessage
	if err := json.Unmarshal(outer["driver_diagnostics"], &driver); err != nil {
		t.Fatal(err)
	}
	driver["sdk_admission"] = driverYieldTestJSON(t, child)
	outer["driver_diagnostics"] = driverYieldTestJSON(t, driver)
	return driverYieldTestJSON(t, outer)
}

func TestDriverSDKAdmissionProtocolLegacyAndExactEmitter(t *testing.T) {
	if workerProtocolVersion != 15 || ReportSchemaVersion != "wukongim/chat-lifecycle-report/v7" {
		t.Fatal("observation changed native protocol/report version")
	}
	legacy := driverValidSnapshotFixture()
	var absent WorkerSnapshot
	if !decodeStrictWorkerResponse(driverYieldTestJSON(t, legacy), &absent) || !validTypedWorkerResponse(&absent) || absent.DriverDiagnostics == nil || absent.DriverDiagnostics.SDKAdmission != nil {
		t.Fatal("legacy absence is not an unknown optional SDK child")
	}
	want := sdkAdmissionValidChild()
	var got WorkerSnapshot
	if !decodeStrictWorkerResponse(sdkAdmissionWire(t, legacy, want), &got) || !validTypedWorkerResponse(&got) || got.DriverDiagnostics == nil {
		t.Fatal("semantic RED: strict reader rejected the independently named SDK observation")
	}
	if !reflect.DeepEqual(got.DriverDiagnostics.SDKAdmission, want) || !reflect.DeepEqual(got.DriverDiagnostics.Records, legacy.DriverDiagnostics.Records) || !reflect.DeepEqual(got.Messages, legacy.Messages) || !reflect.DeepEqual(got.Evidence, legacy.Evidence) {
		t.Fatal("SDK emitter altered, invented an owner prefix, or changed native evidence")
	}
}

func TestDriverSDKAdmissionMalformedChildCannotPoisonBase(t *testing.T) {
	cases := []any{nil, "private-error", []int{1}, map[string]any{}}
	mutations := []func(*DriverSDKAdmissionSnapshot){
		func(c *DriverSDKAdmissionSnapshot) { c.Version++ },
		func(c *DriverSDKAdmissionSnapshot) { c.Emitter = "advance" },
		func(c *DriverSDKAdmissionSnapshot) { c.Code = 37 },
		func(c *DriverSDKAdmissionSnapshot) { c.Records = make([]DriverSDKAdmissionMiss, 17) },
		func(c *DriverSDKAdmissionSnapshot) { c.Records[0].Ordinal = 0 },
		func(c *DriverSDKAdmissionSnapshot) { c.Records[0].Ordinal = 17 },
		func(c *DriverSDKAdmissionSnapshot) { c.Records = append(c.Records, c.Records[0]) },
		func(c *DriverSDKAdmissionSnapshot) { c.Records[0].KnownMask = 16 },
		func(c *DriverSDKAdmissionSnapshot) { c.Records[0].KnownMask = 0 },
		func(c *DriverSDKAdmissionSnapshot) { c.Records[0].QueuedToPickup = -1 },
		func(c *DriverSDKAdmissionSnapshot) { c.Records[0].DeadlineAfterDue++ },
		func(c *DriverSDKAdmissionSnapshot) { c.Records[0].FreshAfterDue = 100 * time.Millisecond },
		func(c *DriverSDKAdmissionSnapshot) { c.Records[0].SampledAfterDue = 101 * time.Millisecond },
	}
	for _, mutate := range mutations {
		child := sdkAdmissionValidChild()
		mutate(child)
		cases = append(cases, child)
	}
	var object map[string]any
	_ = json.Unmarshal(driverYieldTestJSON(t, sdkAdmissionValidChild()), &object)
	object["unexpected"] = "secret-must-not-survive"
	cases = append(cases, object)
	for _, malformed := range []string{"records_null", "records_missing", "row_unknown", "row_missing_zero_field"} {
		var child map[string]any
		_ = json.Unmarshal(driverYieldTestJSON(t, sdkAdmissionValidChild()), &child)
		switch malformed {
		case "records_null":
			child["records"] = nil
		case "records_missing":
			delete(child, "records")
		case "row_unknown":
			child["records"].([]any)[0].(map[string]any)["unknown_row"] = 0
		case "row_missing_zero_field":
			delete(child["records"].([]any)[0].(map[string]any), "queued_to_pickup")
		}
		cases = append(cases, child)
	}
	for _, name := range []string{"version", "emitter", "code", "dropped"} {
		var child map[string]any
		_ = json.Unmarshal(driverYieldTestJSON(t, sdkAdmissionValidChild()), &child)
		child[name] = nil
		cases = append(cases, child)
	}
	for _, name := range []string{"ordinal", "sampled_after_due", "deadline_after_due", "fresh_after_due", "known_mask", "queued_to_pickup", "pickup_to_offer", "offer_to_owner"} {
		for _, bad := range []any{nil, "1"} {
			var child map[string]any
			_ = json.Unmarshal(driverYieldTestJSON(t, sdkAdmissionValidChild()), &child)
			child["records"].([]any)[0].(map[string]any)[name] = bad
			cases = append(cases, child)
		}
	}
	for i, child := range cases {
		base := driverValidSnapshotFixture()
		var got WorkerSnapshot
		if !decodeStrictWorkerResponse(sdkAdmissionWire(t, base, child), &got) || !validTypedWorkerResponse(&got) || got.DriverDiagnostics == nil || got.DriverDiagnostics.SDKAdmission != nil || !reflect.DeepEqual(got.DriverDiagnostics.Records, base.DriverDiagnostics.Records) || !reflect.DeepEqual(got.Evidence, base.Evidence) {
			t.Fatalf("malformed SDK case%d erased base/native evidence or survived", i)
		}
	}
}

func TestDriverSDKAdmissionUnknownBaseRemainsStrict(t *testing.T) {
	var outer map[string]json.RawMessage
	_ = json.Unmarshal(sdkAdmissionWire(t, driverValidSnapshotFixture(), sdkAdmissionValidChild()), &outer)
	var driver map[string]json.RawMessage
	_ = json.Unmarshal(outer["driver_diagnostics"], &driver)
	driver["unknown_base"] = json.RawMessage(`1`)
	outer["driver_diagnostics"] = driverYieldTestJSON(t, driver)
	var got WorkerSnapshot
	if decodeStrictWorkerResponse(driverYieldTestJSON(t, outer), &got) {
		t.Fatal("named optional isolation weakened unknown original base fields")
	}
}

func TestDriverSDKAdmissionUnknownAndMeasuredZeroAreDistinct(t *testing.T) {
	for _, mask := range []uint8{0, 7, 8, 15} {
		child := sdkAdmissionValidChild()
		row := &child.Records[0]
		row.KnownMask = mask
		row.QueuedToPickup, row.PickupToOffer, row.OfferToOwner = 0, 0, 0
		if mask&8 == 0 {
			row.SampledAfterDue, row.DeadlineAfterDue, row.FreshAfterDue = 0, 0, 0
		}
		var got WorkerSnapshot
		if !decodeStrictWorkerResponse(sdkAdmissionWire(t, driverValidSnapshotFixture(), child), &got) || !validTypedWorkerResponse(&got) || got.DriverDiagnostics == nil || !reflect.DeepEqual(got.DriverDiagnostics.SDKAdmission, child) {
			t.Fatalf("unknown and measured-zero observation collapsed for mask%d", mask)
		}
	}
}

func TestDriverSDKAdmissionTypedMaximumEnvelopeKeepsAllYieldEvidence(t *testing.T) {
	runID := strings.Repeat("x", 128)
	workers := make([]WorkerSnapshot, 3)
	for i := range workers {
		s := driverValidSnapshotFixture()
		s.RunID = runID
		s.WorkerID = uint64(i)
		s.DriverDiagnostics.Dropped = math.MaxUint64
		s.DriverDiagnostics.Records = make([]DriverDeadlineMiss, 16)
		for j := range s.DriverDiagnostics.Records {
			r := &s.DriverDiagnostics.Records[j]
			fillEvidenceBudgetScalars(reflect.ValueOf(r).Elem())
			r.Turn, r.Processed = uint64(j+1), math.MaxUint64
			r.Code, r.Operation, r.ClockValid = 38, "advance", false
			r.YieldWall = &DriverYieldWall{Count: math.MaxUint64 / 32, WallNanos: math.MaxInt64, ClockValid: true}
		}
		child := sdkAdmissionValidChild()
		child.Dropped = math.MaxUint64
		child.Records = make([]DriverSDKAdmissionMiss, 16)
		for j := range child.Records {
			child.Records[j] = DriverSDKAdmissionMiss{Ordinal: uint64(j + 1), KnownMask: 15,
				SampledAfterDue: 100 * time.Millisecond, DeadlineAfterDue: 100 * time.Millisecond,
				FreshAfterDue: math.MaxInt64, QueuedToPickup: math.MaxInt64, PickupToOffer: math.MaxInt64, OfferToOwner: math.MaxInt64}
		}
		s.DriverDiagnostics.SDKAdmission = child
		body := driverYieldTestJSON(t, s)
		if len(body) > 256<<10 || int64(len(body)) > workerMaxResponseBytes || workerMaxResponseBytes != 4<<20 {
			t.Fatalf("unchanged worker envelope exceeded: %d", len(body))
		}
		if !decodeStrictWorkerResponse(body, &workers[i]) || !validTypedWorkerResponse(&workers[i]) || workers[i].DriverDiagnostics == nil || workers[i].DriverDiagnostics.SDKAdmission == nil || len(workers[i].DriverDiagnostics.Records) != 16 || len(workers[i].DriverDiagnostics.SDKAdmission.Records) != 16 {
			t.Fatal("actual typed maximum was silently shortened or discarded")
		}
	}
	dir := t.TempDir()
	at := time.Unix(1700000000, 0)
	recorder := newLiveDiagnosticRecorder(dir, runID, at, nil, LatencyLimit{})
	if err := recorder.writeTerminalWorkerEvidence(at, workers); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "driver-diagnostics.json"))
	if err != nil || len(body) > 64<<10 || bytes.Count(body, []byte(`"yield_wall"`)) != 48 || bytes.Count(body, []byte(`"sdk_admission"`)) != 51 || bytes.Count(body, []byte(`"ordinal"`)) != 48 {
		t.Fatalf("typed sidecar lost old yield/SDK rows or exceeded original64KiB: bytes=%d err=%v", len(body), err)
	}
	// The original48 scalar sdk_admission fields must coexist with3 named children.
	var detached struct {
		Workers []struct {
			Diagnostics *DriverDiagnosticsSnapshot `json:"driver_diagnostics"`
		} `json:"workers"`
	}
	if err := json.Unmarshal(body, &detached); err != nil || len(detached.Workers) != 3 {
		t.Fatal("typed sidecar lost its three-worker structure")
	}
	for _, worker := range detached.Workers {
		d := worker.Diagnostics
		if d == nil || len(d.Records) != 16 || d.SDKAdmission == nil || len(d.SDKAdmission.Records) != 16 {
			t.Fatal("named child changed the retained original or SDK rows")
		}
		for _, record := range d.Records {
			if record.SDKAdmission != math.MaxInt64 {
				t.Fatal("new child deleted or reinterpreted the original scalar sdk_admission")
			}
		}
	}
	native, err := os.ReadFile(filepath.Join(dir, "worker-evidence.json"))
	if err != nil || strings.Contains(string(native), "sdk_admission") || strings.Contains(string(native), "yield_wall") || !strings.Contains(string(native), "wukongim/chat-lifecycle-worker-evidence/v3") {
		t.Fatal("private observations escaped into native evidence")
	}
	for _, identity := range []string{"client_msg_no", "sender", "packet", "uid", "raw_error", "unexpected"} {
		if bytes.Contains(body, []byte(`"`+identity+`"`)) {
			t.Fatalf("unbounded/private field %q escaped", identity)
		}
	}
	t.Logf("actual typed three-worker16base+48yield+48SDK sidecar=%d bytes", len(body))
}

func TestDriverSDKAdmissionChildCopiesAndActiveSnapshotSanitization(t *testing.T) {
	s := driverValidSnapshotFixture()
	s.DriverDiagnostics.SDKAdmission = sdkAdmissionValidChild()
	copy := copyDriverDiagnostics(s.DriverDiagnostics)
	copy.SDKAdmission.Records[0].QueuedToPickup = 999
	if s.DriverDiagnostics.SDKAdmission.Records[0].QueuedToPickup == 999 {
		t.Fatal("final caller mutated retained SDK prefix")
	}
	s.Phase = WorkerPhaseRunning
	if !validTypedWorkerResponse(&s) || s.DriverDiagnostics != nil {
		t.Fatal("new child made running diagnostics final")
	}
}
