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

// Failure cases: legal retained evidence exceeds a collector's smaller cap;
// full fixed counters or JSON indentation defeat an evidence-only size estimate;
// a version change adds fields, or one worker's budget exceeds the terminal cap.
func TestWorkerEvidenceMaximumRetainedShapeFitsConsumerBudgets(t *testing.T) {
	var snapshot WorkerSnapshot
	fillEvidenceBudgetScalars(reflect.ValueOf(&snapshot).Elem())
	snapshot.RunID = strings.Repeat("\x01", 128) // Maximum escaped valid fence width.
	snapshot.AssignmentID = snapshot.RunID
	snapshot.Phase = WorkerPhaseFinal
	snapshot.WorkerID, snapshot.WorkerCount = 0, 3
	snapshot.Harness.Classification = SyncClassificationHarnessInvalid
	snapshot.Evidence.Classification = SyncClassificationHarnessInvalid
	snapshot.Messages.TerminalReasons = TerminalSendSnapshot{}
	snapshot.Messages.Terminal = 0
	// This conservative maximum-width fixed-counter envelope need not be
	// reachable in one run. Its evidence, fences and validated subsets are legal.
	for class := FailureClassSend; class <= FailureClassHarness; class++ {
		stage, code := EvidenceStageSendack, FailureCodeInvalidSendack
		switch class {
		case FailureClassReceive:
			stage, code = EvidenceStageSync, FailureCodeSessionSyncValidation
		case FailureClassCorrelation:
			stage, code = EvidenceStageCorrelation, FailureCodeCorrelationSequenceConflict
		case FailureClassHarness:
			stage, code = EvidenceStageCapacity, FailureCode(46)
		}
		item := EvidenceExample{Stage: stage, Code: code, SampleIndex: math.MaxUint64, Value: math.MaxUint64}
		for i := range item.Fingerprint {
			item.Fingerprint[i] = 255
		}
		retained := EvidenceClassSnapshot{Class: class, Count: math.MaxUint64}
		for i := 0; i < maxEvidenceExamplesPerSide; i++ {
			retained.First = append(retained.First, item)
			retained.Last = append(retained.Last, item)
		}
		snapshot.Evidence.Classes = append(snapshot.Evidence.Classes, retained)
	}
	if !validWorkerFence(WorkerFence{RunID: snapshot.RunID, AssignmentID: snapshot.AssignmentID, Generation: 1}) || !validWorkerSnapshot(snapshot) {
		t.Fatal("budget fixture violates snapshot bounds")
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(body)+1 > 256<<10 || int64(len(body)+1) > workerMaxResponseBytes {
		t.Fatalf("full worker snapshot exceeds finite consumer/API budget: %d", len(body)+1)
	}
	var shape map[string]json.RawMessage
	exampleBody, _ := json.Marshal(snapshot.Evidence.Classes[3].First[0])
	_ = json.Unmarshal(exampleBody, &shape)
	if len(shape) != 5 {
		t.Fatalf("evidence example grew beyond five fields: %s", exampleBody)
	}
	for _, key := range []string{"stage", "code", "sample_index", "fingerprint", "value"} {
		if _, ok := shape[key]; !ok {
			t.Fatalf("missing evidence key %q", key)
		}
	}
	directory := t.TempDir()
	recorder := newLiveDiagnosticRecorder(directory, snapshot.RunID, time.Now(), nil, LatencyLimit{})
	snapshots := make([]WorkerSnapshot, 3)
	for i := range snapshots {
		snapshots[i] = snapshot
		snapshots[i].WorkerID = uint64(i)
	}
	if err := recorder.writeTerminalWorkerEvidence(time.Now(), snapshots); err != nil {
		t.Fatalf("three-worker maximum retention exceeds terminal document budget: %v", err)
	}
	terminal, err := os.ReadFile(filepath.Join(directory, "worker-evidence.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(terminal) > 1<<20 {
		t.Fatalf("terminal document exceeds finite budget: %d", len(terminal))
	}
	t.Logf("maximum-width fixed envelope: snapshot=%d bytes, three-worker indented evidence=%d bytes", len(body)+1, len(terminal))
}

func fillEvidenceBudgetScalars(value reflect.Value) {
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			fillEvidenceBudgetScalars(value.Field(i))
		}
	case reflect.Array:
		for i := 0; i < value.Len(); i++ {
			fillEvidenceBudgetScalars(value.Index(i))
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value.SetUint(uint64(1)<<value.Type().Bits() - 1)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(int64(uint64(1)<<(value.Type().Bits()-1) - 1))
	case reflect.Bool:
		value.SetBool(true)
	}
}
