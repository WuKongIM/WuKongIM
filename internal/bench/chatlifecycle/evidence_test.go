package chatlifecycle

import (
	"reflect"
	"sync"
	"testing"
)

func TestEvidenceRecorderRetainsBoundedRedactedFirstAndLastExamples(t *testing.T) {
	recorder, err := NewEvidenceRecorder(2, 2)
	if err != nil {
		t.Fatalf("NewEvidenceRecorder() error = %v", err)
	}
	for index := uint64(0); index < 10; index++ {
		err := recorder.Record(EvidenceEvent{
			Class:       FailureClassReceive,
			Stage:       EvidenceStageReceive,
			Code:        FailureCodeReceivePayload,
			SampleIndex: index,
			Fingerprint: [16]byte{byte(index + 1)},
			Value:       index * 10,
		})
		if err != nil {
			t.Fatalf("Record(%d) error = %v", index, err)
		}
	}

	snapshot := recorder.Snapshot()
	if snapshot.Classification != SyncClassificationProductFailure {
		t.Fatalf("classification = %q, want product_failure", snapshot.Classification)
	}
	if len(snapshot.Classes) != 1 {
		t.Fatalf("classes = %d, want 1", len(snapshot.Classes))
	}
	class := snapshot.Classes[0]
	if class.Class != FailureClassReceive || class.Count != 10 {
		t.Fatalf("class = %+v", class)
	}
	if got, want := sampleIndexes(class.First), []uint64{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first indexes = %v, want %v", got, want)
	}
	if got, want := sampleIndexes(class.Last), []uint64{8, 9}; !reflect.DeepEqual(got, want) {
		t.Fatalf("last indexes = %v, want %v", got, want)
	}

	// A snapshot owns its examples; caller mutation cannot corrupt recorder state.
	snapshot.Classes[0].First[0].Value = 999
	again := recorder.Snapshot()
	if again.Classes[0].First[0].Value != 0 {
		t.Fatalf("snapshot mutation reached recorder: %+v", again.Classes[0].First[0])
	}
}

func TestEvidenceRecorderVerdictPrecedenceAndStableOrdering(t *testing.T) {
	recorder, err := NewEvidenceRecorder(1, 1)
	if err != nil {
		t.Fatalf("NewEvidenceRecorder() error = %v", err)
	}
	events := []EvidenceEvent{
		{Class: FailureClassHarness, Stage: EvidenceStageCapacity, Code: FailureCodePendingCapacity, SampleIndex: 7},
		{Class: FailureClassCorrelation, Stage: EvidenceStageCorrelation, Code: FailureCodeCorrelationExpired, SampleIndex: 9},
		{Class: FailureClassSend, Stage: EvidenceStageSendack, Code: FailureCodeUnknownSendack, SampleIndex: 3},
	}
	for _, event := range events {
		if err := recorder.Record(event); err != nil {
			t.Fatalf("Record(%+v) error = %v", event, err)
		}
	}
	snapshot := recorder.Snapshot()
	if snapshot.Classification != SyncClassificationProductFailure {
		t.Fatalf("classification = %q, want product_failure", snapshot.Classification)
	}
	want := []FailureClass{FailureClassSend, FailureClassCorrelation, FailureClassHarness}
	got := make([]FailureClass, 0, len(snapshot.Classes))
	for _, class := range snapshot.Classes {
		got = append(got, class.Class)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("class order = %v, want %v", got, want)
	}

	// Success-like activity has no API that can clear a sticky failure verdict.
	if recorder.Snapshot().Classification != SyncClassificationProductFailure {
		t.Fatal("product failure verdict was not sticky")
	}
	if err := recorder.Record(EvidenceEvent{
		Class: FailureClassHarness,
		Stage: EvidenceStageCapacity,
		Code:  FailureCodeSequenceCapacity,
	}); err != nil {
		t.Fatalf("Record(harness after product) error = %v", err)
	}
	if recorder.Snapshot().Classification != SyncClassificationProductFailure {
		t.Fatal("later harness evidence overrode product_failure")
	}
}

func TestEvidenceRecorderRejectsInvalidBoundsAndClosedVocabulary(t *testing.T) {
	for _, bounds := range [][2]int{{0, 1}, {1, 0}, {-1, 1}, {1, -1}, {65, 1}, {1, 65}} {
		if _, err := NewEvidenceRecorder(bounds[0], bounds[1]); err == nil {
			t.Fatalf("NewEvidenceRecorder(%d, %d) succeeded", bounds[0], bounds[1])
		}
	}
	recorder, err := NewEvidenceRecorder(1, 1)
	if err != nil {
		t.Fatalf("NewEvidenceRecorder() error = %v", err)
	}
	bad := []EvidenceEvent{
		{},
		{Class: FailureClass(255), Stage: EvidenceStageReceive, Code: FailureCodeReceivePayload},
		{Class: FailureClassReceive, Stage: EvidenceStage(255), Code: FailureCodeReceivePayload},
		{Class: FailureClassReceive, Stage: EvidenceStageReceive, Code: FailureCode(255)},
		{Class: FailureClassHarness, Stage: EvidenceStageCapacity, Code: FailureCodeReceivePayload},
		{Class: FailureClassSend, Stage: EvidenceStageReceive, Code: FailureCodeUnknownSendack},
		{Class: FailureClassReceive, Stage: EvidenceStageSendack, Code: FailureCodeReceivePayload},
		{Class: FailureClassCorrelation, Stage: EvidenceStageCapacity, Code: FailureCodeCorrelationExpired},
		{Class: FailureClassHarness, Stage: EvidenceStageSend, Code: FailureCodeRecvackCanceled},
		{Class: FailureClassHarness, Stage: EvidenceStageCapacity, Code: FailureCodeRecvackDeadline},
		{Class: FailureClassHarness, Stage: EvidenceStageRecvack, Code: FailureCodePendingCapacity},
	}
	for _, event := range bad {
		if err := recorder.Record(event); err == nil {
			t.Fatalf("Record(%+v) succeeded", event)
		}
	}
	if snapshot := recorder.Snapshot(); len(snapshot.Classes) != 0 || snapshot.Classification != "" {
		t.Fatalf("invalid events mutated recorder: %+v", snapshot)
	}
}

func TestEvidenceRecorderConcurrentRecordAndSnapshotIsRaceSafe(t *testing.T) {
	recorder, err := NewEvidenceRecorder(2, 2)
	if err != nil {
		t.Fatalf("NewEvidenceRecorder() error = %v", err)
	}
	const records = 200
	var writers sync.WaitGroup
	for index := 0; index < records; index++ {
		writers.Add(1)
		go func(index int) {
			defer writers.Done()
			if err := recorder.Record(EvidenceEvent{
				Class:       FailureClassReceive,
				Stage:       EvidenceStageReceive,
				Code:        FailureCodeReceiveSequence,
				SampleIndex: uint64(index),
			}); err != nil {
				t.Errorf("Record(%d) error = %v", index, err)
			}
		}(index)
	}
	for index := 0; index < records; index++ {
		_ = recorder.Snapshot()
	}
	writers.Wait()
	snapshot := recorder.Snapshot()
	if len(snapshot.Classes) != 1 || snapshot.Classes[0].Count != records || len(snapshot.Classes[0].First) != 2 || len(snapshot.Classes[0].Last) != 2 {
		t.Fatalf("concurrent snapshot = %+v", snapshot)
	}
}

func sampleIndexes(examples []EvidenceExample) []uint64 {
	indexes := make([]uint64, len(examples))
	for index := range examples {
		indexes[index] = examples[index].SampleIndex
	}
	return indexes
}
