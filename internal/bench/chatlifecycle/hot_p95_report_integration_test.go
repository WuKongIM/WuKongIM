//go:build integration

package chatlifecycle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// F6/F7: the real checkpoint/report reader path must declare a new P95 contract,
// preserve legacy P99, and reject forged/missing/aliased quantile evidence.
func p95NativeGateBody(t *testing.T, bad bool) []byte {
	t.Helper()
	var gate map[string]json.RawMessage
	if err := json.Unmarshal(m01ValidPersistedGate(t), &gate); err != nil {
		t.Fatal(err)
	}
	gate["schema_version"] = json.RawMessage(`"wukongim/chat-lifecycle-latency-gate/v2"`)
	gate["p95_fraction_denominator"] = json.RawMessage(`20`)
	rows := m01ClassMaps(t, gate)
	for _, suffix := range []string{"limit", "count", "above", "samples"} {
		rows[0]["p95_"+suffix] = rows[0]["p99_"+suffix]
		delete(rows[0], "p99_"+suffix)
	}
	rows[0]["p95_limit"] = json.RawMessage(`600000000`)
	rows[0]["p95_above"] = json.RawMessage(`50`)
	rows[0]["p999_above"] = json.RawMessage(`1`)
	if bad {
		rows[0]["p95_above"] = json.RawMessage(`51`)
		rows[0]["breached"] = json.RawMessage(`true`)
		rows[0]["breach_since_elapsed"] = json.RawMessage(`86100000000000`)
		rows[0]["breach_attribution"] = json.RawMessage(`"product_failure"`)
	}
	b, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	gate["classes"] = b
	b, err = json.Marshal(gate)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func p95PersistedReportBody(t *testing.T) []byte {
	t.Helper()
	r := reportFixture(t)
	r.Profile = ProfileLocal
	r.Stage = StageShakeout
	r.Qualification = reportQualification(r.Profile, r.Stage)
	r.SchemaVersion = "wukongim/chat-lifecycle-report/v9"
	r.ThresholdVersion = "wukongim/chat-lifecycle-thresholds/v3"
	r.Thresholds.Latency.HotSendACK = p95LimitFixture(t)
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return m01InsertGate(t, b, p95NativeGateBody(t, false))
}

func TestHotP95NativeReportRoundTripAndLegacyMeaning(t *testing.T) {
	body := p95PersistedReportBody(t)
	r, err := m01ReadBody(t, body)
	if err != nil {
		t.Fatalf("explicit local P95 report rejected: %v", err)
	}
	for _, format := range []ReportFormat{ReportFormatJSON, ReportFormatMarkdown} {
		b, err := MarshalReport(r, format)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"p95_limit"`) || !strings.Contains(string(b), `"p95":`) {
			t.Fatalf("P95 result mislabeled: %s", b)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCurrentReport(path); err != nil {
		t.Fatalf("current local P95 result unreadable: %v", err)
	}
	legacy := reportFixture(t)
	b, err := MarshalReport(legacy, ReportFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.SchemaVersion != "wukongim/chat-lifecycle-report/v8" || strings.Contains(string(b), `"p95"`) {
		t.Fatal("legacy P99 report reheadered/reinterpreted")
	}
	if _, err := m01ReadBody(t, b); err != nil {
		t.Fatal(err)
	}
	// Exercise the actual producer, not only a hand-authored report.
	cfg := LocalConfig()
	cfg.Thresholds.Latency.HotSendACK = p95LimitFixture(t)
	start := time.Unix(2100000000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "p95-report", Generation: 1}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	got := recorder.buildReport(CheckpointQualification, start.Add(time.Minute), time.Minute, CoordinatorSnapshot{Generation: 1}, checkpointEvidenceFixture(false))
	if got.SchemaVersion != "wukongim/chat-lifecycle-report/v9" || got.ThresholdVersion != "wukongim/chat-lifecycle-thresholds/v3" {
		t.Fatalf("native P95 header wrong: %s/%s", got.SchemaVersion, got.ThresholdVersion)
	}
}

func TestHotP95StrictReportProofAndScopeRefusesTransplant(t *testing.T) {
	base := p95PersistedReportBody(t)
	for _, mode := range []string{"missing95", "null95", "wrong20", "zero20", "unknown_quantile", "mixed99", "cold95", "false_breach", "null_samples", "duplicate95", "case_alias", "old_report", "old_gate", "formal", "old_threshold", "unknown_field"} {
		t.Run(mode, func(t *testing.T) {
			var root, verdict, gate map[string]json.RawMessage
			_ = json.Unmarshal(base, &root)
			_ = json.Unmarshal(root["verdict"], &verdict)
			_ = json.Unmarshal(verdict["latency_gate"], &gate)
			rows := m01ClassMaps(t, gate)
			switch mode {
			case "missing95":
				delete(rows[0], "p95_limit")
			case "null95":
				rows[0]["p95_limit"] = json.RawMessage(`null`)
			case "wrong20":
				gate["p95_fraction_denominator"] = json.RawMessage(`100`)
			case "zero20":
				gate["p95_fraction_denominator"] = json.RawMessage(`0`)
			case "unknown_quantile":
				rows[0]["p90_limit"] = json.RawMessage(`600000000`)
			case "mixed99":
				rows[0]["p99_limit"] = json.RawMessage(`600000000`)
			case "cold95":
				rows[1]["p95_limit"] = rows[1]["p99_limit"]
				delete(rows[1], "p99_limit")
			case "false_breach":
				rows[0]["p95_above"] = json.RawMessage(`51`)
			case "null_samples":
				rows[0]["p95_samples"] = json.RawMessage(`null`)
			case "case_alias":
				rows[0]["P95_limit"] = rows[0]["p95_limit"]
				delete(rows[0], "p95_limit")
			case "old_report":
				root["schema_version"] = json.RawMessage(`"wukongim/chat-lifecycle-report/v8"`)
			case "old_gate":
				gate["schema_version"] = json.RawMessage(`"wukongim/chat-lifecycle-latency-gate/v1"`)
			case "formal":
				root["profile"] = json.RawMessage(`"formal"`)
			case "old_threshold":
				root["threshold_version"] = json.RawMessage(`"wukongim/chat-lifecycle-thresholds/v2"`)
			case "unknown_field":
				gate["allow_pass"] = json.RawMessage(`true`)
			}
			b, _ := json.Marshal(rows)
			gate["classes"] = b
			b, _ = json.Marshal(gate)
			if mode == "duplicate95" {
				b = []byte(strings.Replace(string(b), `"p95_limit":600000000`, `"p95_limit":600000000,"p95_limit":600000000`, 1))
			}
			verdict["latency_gate"] = b
			b, _ = json.Marshal(verdict)
			root["verdict"] = b
			b, _ = json.Marshal(root)
			if _, err := m01ReadBody(t, b); err == nil {
				t.Fatal("malformed/transplanted P95 report accepted")
			}
		})
	}
}

func TestHotP95NativeGateNamesAndFrozenIndependentTail(t *testing.T) {
	cfg := LocalConfig()
	cfg.Thresholds.Latency.HotSendACK = p95LimitFixture(t)
	start := time.Unix(2100000000, 0)
	v, err := NewVerdictEvaluator(start, cfg.Thresholds)
	if err != nil {
		t.Fatal(err)
	}
	c := LatencyCountersForThresholds(cfg.Thresholds.Latency)
	for i := 0; i <= 61; i++ {
		if i > 0 {
			c.Hot = p95CounterFixture(t, uint64(i)*1000, uint64(i)*2, uint64(i)*2)
			c.Cold.Count = uint64(i) * 1000
			c.Sync.Count = uint64(i) * 1000
		}
		m01Observe(t, v, start.Add(cfg.Thresholds.Timeline.LatencyWarmup+time.Duration(i)*5*time.Second), &c, CapacityAttributionProduct)
	}
	if got := v.Snapshot(); !got.Terminal || got.Cause != VerdictCauseHotLatency {
		t.Fatalf("independent1s tail did not stop: %+v", got)
	}
	b := m01GateJSON(t, projectReportVerdict(v.Snapshot()))
	var gate map[string]json.RawMessage
	if err := json.Unmarshal(b, &gate); err != nil {
		t.Fatal(err)
	}
	rows := m01ClassMaps(t, gate)
	if string(gate["p95_fraction_denominator"]) != "20" || string(gate["p999_fraction_denominator"]) != "1000" || len(rows[0]["p95_above"]) == 0 || len(rows[0]["p99_above"]) != 0 || len(rows[1]["p99_above"]) == 0 {
		t.Fatalf("native mixed quantile proof mislabeled: %s", b)
	}
	if err := v.Observe(VerdictObservation{At: start.Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	after := m01GateJSON(t, projectReportVerdict(v.Snapshot()))
	if string(after) != string(b) {
		t.Fatal("terminal gate refreshed after tailfailure")
	}
}
