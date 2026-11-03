package chatlifecycle

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// Optional metadata must be checked before zero-value decoding can erase it.
// The public child decoder must preserve the original confirmed-late prefix.
func TestSlowHotSuccessRejectsNullMetadataBeforeNormalization(t *testing.T) {
	model, v := slowHotSuccessFixture(t, true)
	at := time.Unix(123456, 0)
	slowHotSuccessACK(t, model, v, 1, at, 2*time.Second, SendLatencyHot)
	slowHotSuccessACK(t, model, v, 2, at.Add(3*time.Second), 700*time.Millisecond, SendLatencyHot)
	body, err := json.Marshal(v.AttemptLinkDiagnostics(1))
	if err != nil {
		t.Fatal(err)
	}
	var document, child map[string]json.RawMessage
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(document["slow_hot_success"], &child); err != nil {
		t.Fatal(err)
	}
	delete(child, "retention_policy")
	delete(child, "tail_limit_nanos")
	for _, test := range []struct {
		name   string
		policy string
		tail   string
		valid  bool
	}{
		{"legacy_absent", "", "", true},
		{"legacy_explicit_empty", `""`, "", true},
		{"null_policy", "null", "", false},
		{"legacy_null_tail", "", "null", false},
		{"legacy_zero_tail", "", "0", false},
		{"empty_policy_null_tail", `""`, "null", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			delete(child, "retention_policy")
			delete(child, "tail_limit_nanos")
			if test.policy != "" {
				child["retention_policy"] = json.RawMessage(test.policy)
			}
			if test.tail != "" {
				child["tail_limit_nanos"] = json.RawMessage(test.tail)
			}
			document["slow_hot_success"], err = json.Marshal(child)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			var decoded AttemptLinkDiagnosticsSnapshot
			if err := json.Unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Records) != 1 || decoded.Records[0].Key.ClientSeq != 1 {
				t.Fatal("malformed optional child erased original confirmed-late evidence")
			}
			if (decoded.SlowHotSuccess != nil) != test.valid {
				t.Fatalf("metadata normalized into legacy evidence: retained=%t want=%t", decoded.SlowHotSuccess != nil, test.valid)
			}
		})
	}
}

func TestSlowHotSuccessRejectsMetadataAliasesAndDuplicates(t *testing.T) {
	model, v := slowHotSuccessFixture(t, true)
	at := time.Unix(123456, 0)
	slowHotSuccessACK(t, model, v, 1, at, 2*time.Second, SendLatencyHot)
	slowHotSuccessACK(t, model, v, 2, at.Add(3*time.Second), 700*time.Millisecond, SendLatencyHot)
	body, err := json.Marshal(v.AttemptLinkDiagnostics(1))
	if err != nil {
		t.Fatal(err)
	}
	var document, child map[string]json.RawMessage
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	priority := document["slow_hot_success"]
	if err := json.Unmarshal(priority, &child); err != nil {
		t.Fatal(err)
	}
	delete(child, "retention_policy")
	delete(child, "tail_limit_nanos")
	legacy, err := json.Marshal(child)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		priority bool
		suffix   string
	}{
		{"canonical_priority_control", true, ""},
		{"upper_policy_null", false, `"RETENTION_POLICY":null`},
		{"mixed_policy_null", false, `"ReTeNtIoN_PoLiCy":null`},
		{"upper_tail_null", false, `"TAIL_LIMIT_NANOS":null`},
		{"upper_tail_zero", false, `"TAIL_LIMIT_NANOS":0`},
		{"duplicate_legacy_policy", false, `"retention_policy":null,"retention_policy":""`},
		{"duplicate_priority_policy", true, `"retention_policy":"unknown","retention_policy":"p999_reserve8/v1"`},
		{"duplicate_priority_tail", true, `"tail_limit_nanos":null,"tail_limit_nanos":1000000000`},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := legacy
			if test.priority {
				raw = priority
			}
			if test.suffix != "" {
				raw = append(append([]byte{}, raw[:len(raw)-1]...), []byte(","+test.suffix+"}")...)
			}
			document["slow_hot_success"] = raw
			wire, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			var decoded AttemptLinkDiagnosticsSnapshot
			if err := json.Unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded.Records) != 1 || decoded.Records[0].Key.ClientSeq != 1 {
				t.Fatal("metadata rejection erased original confirmed-late evidence")
			}
			if (decoded.SlowHotSuccess != nil) != (test.suffix == "") {
				t.Fatalf("noncanonical or duplicate metadata survived normalization: retained=%t", decoded.SlowHotSuccess != nil)
			}
		})
	}
}

// The independent P999 failure needs original ACK keys even when more frequent
// primary-only successes arrive afterward. This tests retention, not the cause
// of physical server latency, through the existing public verifier boundary.
func TestSlowHotSuccessRetainsIndependentTailAcrossPrimaryChurn(t *testing.T) {
	var native [2]VerifierSnapshot
	for mode, enabled := range []bool{false, true} {
		model := newTestTrafficModel(t, FormalConfig())
		evidence, err := NewEvidenceRecorder(2, 2)
		if err != nil {
			t.Fatal(err)
		}
		v, err := NewVerifier(model, VerifierConfig{
			PendingCapacity: 64, SequenceCapacity: 64, CorrelationCapacity: 64,
			CorrelationDeadline: 10 * time.Second, EnableAttemptLinkDiagnostics: enabled,
			HotLatency: LatencyLimit{P95: 600 * time.Millisecond, P999: time.Second},
		}, evidence)
		if err != nil {
			t.Fatal(err)
		}
		at := time.Unix(123456, 0)
		slowHotSuccessACK(t, model, v, 1, at, 1100*time.Millisecond, SendLatencyHot)
		for index := uint64(2); index <= 17; index++ {
			slowHotSuccessACK(t, model, v, index, at.Add(time.Duration(index)*3*time.Second), 700*time.Millisecond, SendLatencyHot)
		}
		native[mode] = v.Snapshot()
		if !enabled {
			if v.AttemptLinkDiagnostics(1) != nil {
				t.Fatal("disabled diagnostics retained attempt state")
			}
			continue
		}
		cut := slowHotSuccessCut(t, v, 1)
		if cut.Seen != 17 || len(cut.Records) != 16 || cut.Overwritten != 1 {
			t.Fatalf("bounded retention changed: seen=%d records=%d overwritten=%d", cut.Seen, len(cut.Records), cut.Overwritten)
		}
		var tail, newest bool
		for _, row := range cut.Records {
			tail = tail || row.Key.ClientSeq == 1 && row.ObservedAfterRegistration == 1100*time.Millisecond
			newest = newest || row.Key.ClientSeq == 17 && row.ObservedAfterRegistration == 700*time.Millisecond
		}
		if !tail || !newest {
			t.Fatalf("independent P999 sample lost to primary-only churn: tail=%t newest=%t", tail, newest)
		}
	}
	if !reflect.DeepEqual(native[0], native[1]) {
		t.Fatal("diagnostic retention changed native latency, deadline or correctness evidence")
	}
}
