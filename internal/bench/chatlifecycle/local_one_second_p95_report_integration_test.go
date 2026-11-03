//go:build integration

package chatlifecycle

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pairedP95PersistedReport(t *testing.T) []byte {
	t.Helper()
	r := reportFixture(t)
	r.Profile = ProfileLocal
	r.Stage = StageShakeout
	r.Mode = ModeSoak
	r.Qualification = reportQualification(r.Profile, r.Stage)
	r.SchemaVersion = "wukongim/chat-lifecycle-report/v10"
	r.ThresholdVersion = "wukongim/chat-lifecycle-thresholds/v4"
	r.Thresholds.Latency = pairedLocalP95Fixture().Thresholds.Latency
	r.Sync.Thresholds.P95Limit = time.Second
	r.Sync.Thresholds.P999Limit = 3 * time.Second
	r.Sync.Thresholds.P99Limit = 0
	r.Sync.Thresholds.AboveP95 = r.Sync.Thresholds.AboveP99
	r.Sync.Thresholds.AboveP99 = 0
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var gate map[string]json.RawMessage
	if err := json.Unmarshal(p95NativeGateBody(t, false), &gate); err != nil {
		t.Fatal(err)
	}
	gate["schema_version"] = json.RawMessage(`"wukongim/chat-lifecycle-latency-gate/v3"`)
	rows := m01ClassMaps(t, gate)
	for _, suffix := range []string{"limit", "count", "above", "samples"} {
		delete(rows[0], "p999_"+suffix)
		rows[2]["p95_"+suffix] = rows[2]["p99_"+suffix]
		delete(rows[2], "p99_"+suffix)
	}
	rows[2]["p95_limit"] = json.RawMessage(`1000000000`)
	rows[2]["p95_above"] = json.RawMessage(`50`)
	rows[2]["p999_above"] = json.RawMessage(`1`)
	gate["classes"], err = json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(gate)
	if err != nil {
		t.Fatal(err)
	}
	return m01InsertGate(t, raw, body)
}

// F5 reaches the real checkpoint writer, native exporter and strict reader.
func TestLocalOneSecondP95NativeVersionsAndReportRoundtrip(t *testing.T) {
	cfg := pairedLocalP95Fixture()
	start := time.Unix(2100000000, 0)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "paired-report", Generation: 1}
	recorder, err := NewCheckpointRecorder(cfg, fence, start)
	if err != nil {
		t.Fatal(err)
	}
	got := recorder.buildReport(CheckpointQualification, start.Add(time.Minute), time.Minute, CoordinatorSnapshot{Generation: 1}, checkpointEvidenceFixture(false))
	if got.SchemaVersion != "wukongim/chat-lifecycle-report/v10" || got.ThresholdVersion != "wukongim/chat-lifecycle-thresholds/v4" {
		t.Fatalf("native paired writer has old semantics: %s/%s", got.SchemaVersion, got.ThresholdVersion)
	}
}

func TestLocalOneSecondP95NativeGateWire(t *testing.T) {
	cfg := pairedLocalP95Fixture()
	start := time.Unix(2100000000, 0)
	v, err := NewVerdictEvaluator(start, cfg.Thresholds)
	if err != nil {
		t.Fatal(err)
	}
	c := LatencyCountersForThresholds(cfg.Thresholds.Latency)
	for i := 0; i <= 61; i++ {
		n := uint64(i)
		c.Hot.Count = n * 1000
		c.Hot.AboveP95 = n * 50
		c.Cold.Count = n * 1000
		c.Sync.Count = n * 1000
		c.Sync.AboveP95 = n * 50
		c.Sync.AboveP999 = n
		if err := v.Observe(VerdictObservation{At: start.Add(cfg.Thresholds.Timeline.LatencyWarmup + time.Duration(i)*5*time.Second), Latency: &c}); err != nil {
			t.Fatal(err)
		}
	}
	gate := v.Snapshot().LatencyGate
	if gate == nil || gate.SchemaVersion != "wukongim/chat-lifecycle-latency-gate/v3" {
		t.Fatalf("native paired gate wrong version: %+v", gate)
	}
	raw, err := json.Marshal(gate)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	rows := m01ClassMaps(t, obj)
	if len(rows[0]["p999_limit"]) != 0 || len(rows[0]["p999_count"]) != 0 || len(rows[0]["p99_limit"]) != 0 || len(rows[2]["p99_limit"]) != 0 || string(rows[2]["p95_limit"]) != "1000000000" || string(rows[2]["p999_limit"]) != "3000000000" {
		t.Fatalf("native active/inactive quantile labels wrong: %s", raw)
	}
	var decoded VerdictLatencyGateSnapshot
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("native v3 gate cannot read its own output: %v", err)
	}
}

func TestLocalOneSecondP95StrictPersistedReportAndLegacyMeaning(t *testing.T) {
	body := pairedP95PersistedReport(t)
	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := ReadCurrentReport(path)
	if err != nil {
		t.Fatalf("authorized paired v10 report rejected: %v", err)
	}
	for _, format := range []ReportFormat{ReportFormatJSON, ReportFormatMarkdown} {
		raw, err := MarshalReport(r, format)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), `"schema_version": "wukongim/chat-lifecycle-report/v10"`) && !strings.Contains(string(raw), `"schema_version":"wukongim/chat-lifecycle-report/v10"`) {
			t.Fatalf("paired report version absent: %s", raw)
		}
	}
	for _, mode := range []string{"old_report", "old_threshold", "old_gate", "formal", "capacity", "hot_tail_zero", "hot_tail_reactivated", "sync99", "missing_sync95", "null_sync95", "wrong20", "zero20", "null_samples", "false_breach", "duplicate95", "case_alias"} {
		t.Run(mode, func(t *testing.T) {
			var root, verdict, gate map[string]json.RawMessage
			_ = json.Unmarshal(body, &root)
			_ = json.Unmarshal(root["verdict"], &verdict)
			_ = json.Unmarshal(verdict["latency_gate"], &gate)
			rows := m01ClassMaps(t, gate)
			switch mode {
			case "old_report":
				root["schema_version"] = json.RawMessage(`"wukongim/chat-lifecycle-report/v9"`)
			case "old_threshold":
				root["threshold_version"] = json.RawMessage(`"wukongim/chat-lifecycle-thresholds/v3"`)
			case "old_gate":
				gate["schema_version"] = json.RawMessage(`"wukongim/chat-lifecycle-latency-gate/v2"`)
			case "formal":
				root["profile"] = json.RawMessage(`"formal"`)
			case "capacity":
				root["mode"] = json.RawMessage(`"capacity"`)
			case "hot_tail_zero":
				rows[0]["p999_limit"] = json.RawMessage(`0`)
			case "hot_tail_reactivated":
				rows[0]["p999_limit"] = json.RawMessage(`1000000000`)
			case "sync99":
				rows[2]["p99_limit"] = json.RawMessage(`1000000000`)
			case "missing_sync95":
				delete(rows[2], "p95_limit")
			case "null_sync95":
				rows[2]["p95_limit"] = json.RawMessage(`null`)
			case "wrong20":
				gate["p95_fraction_denominator"] = json.RawMessage(`100`)
			case "zero20":
				gate["p95_fraction_denominator"] = json.RawMessage(`0`)
			case "null_samples":
				rows[2]["p95_samples"] = json.RawMessage(`null`)
			case "false_breach":
				rows[2]["p95_above"] = json.RawMessage(`51`)
			case "case_alias":
				rows[2]["P95_limit"] = rows[2]["p95_limit"]
				delete(rows[2], "p95_limit")
			}
			gate["classes"], _ = json.Marshal(rows)
			raw, _ := json.Marshal(gate)
			if mode == "duplicate95" {
				raw = []byte(strings.Replace(string(raw), `"p95_limit":1000000000`, `"p95_limit":1000000000,"p95_limit":1000000000`, 1))
			}
			verdict["latency_gate"] = raw
			root["verdict"], _ = json.Marshal(verdict)
			raw, _ = json.Marshal(root)
			if _, err := m01ReadBody(t, raw); err == nil {
				t.Fatal("malformed/transplanted paired report accepted")
			}
		})
	}
	if _, err := m01ReadBody(t, p95PersistedReportBody(t)); err != nil {
		t.Fatalf("legacy v9 meaning broken: %v", err)
	}
	legacy, err := MarshalReport(reportFixture(t), ReportFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m01ReadBody(t, legacy); err != nil {
		t.Fatalf("legacy v8 meaning broken: %v", err)
	}
}

// A real authenticated worker boundary must advertise the new same-source epoch.
func TestLocalOneSecondP95WorkerHTTPProtocolAndAssignment(t *testing.T) {
	cfg := pairedLocalP95Fixture()
	var installed Config
	server, err := NewWorkerServer(WorkerServerConfig{ControlToken: "paired-fixture", Factory: WorkerGenerationFactoryFunc(func(a WorkerAssignment) (WorkerGeneration, error) {
		installed = a.Config
		return newFakeWorkerGeneration(), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	client, err := NewWorkerClient(WorkerClientConfig{BaseURL: httpServer.URL, ControlToken: "paired-fixture", HTTPClient: httpServer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	info, err := client.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.ProtocolVersion != 19 {
		t.Errorf("new paired live counters require protocol19, got%d", info.ProtocolVersion)
	}
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "paired-http", Generation: 1}
	_, err = client.Assign(context.Background(), WorkerAssignment{WorkerFence: fence, WorkerID: 0, WorkerCount: uint64(cfg.Workload.Workers), Config: cfg})
	if err != nil {
		t.Fatalf("actual paired worker assignment: %v", err)
	}
	if installed.Thresholds.Latency.HotSendACK != cfg.Thresholds.Latency.HotSendACK || installed.Thresholds.Latency.Sync != cfg.Thresholds.Latency.Sync {
		t.Fatal("HTTP assignment changed paired thresholds")
	}
}

// Review regression: persisted native counters must retain the paired schema.
func TestLocalOneSecondP95PersistedSyncCountersStayBound(t *testing.T) {
	body := pairedP95PersistedReport(t)
	for _, mode := range []string{"legacy99", "inactive99", "count_mismatch", "limit_drift", "tail_drift", "missing95", "null95", "duplicate95", "alias95"} {
		t.Run(mode, func(t *testing.T) {
			var root, sync, thresholds map[string]json.RawMessage
			_ = json.Unmarshal(body, &root)
			_ = json.Unmarshal(root["sync"], &sync)
			_ = json.Unmarshal(sync["thresholds"], &thresholds)
			switch mode {
			case "legacy99":
				delete(thresholds, "P95Limit")
				thresholds["P99Limit"] = json.RawMessage(`1000000000`)
			case "inactive99":
				thresholds["AboveP99"] = json.RawMessage(`1`)
			case "count_mismatch":
				thresholds["Count"] = json.RawMessage(`999`)
			case "limit_drift":
				thresholds["P95Limit"] = json.RawMessage(`2000000000`)
			case "tail_drift":
				thresholds["P999Limit"] = json.RawMessage(`4000000000`)
			case "missing95":
				delete(thresholds, "P95Limit")
			case "null95":
				thresholds["P95Limit"] = json.RawMessage(`null`)
			case "alias95":
				thresholds["p95limit"] = thresholds["P95Limit"]
				delete(thresholds, "P95Limit")
			}
			raw, _ := json.Marshal(thresholds)
			if mode == "duplicate95" {
				raw = []byte(strings.Replace(string(raw), `"P95Limit":1000000000`, `"P95Limit":1000000000,"P95Limit":1000000000`, 1))
			}
			sync["thresholds"] = raw
			root["sync"], _ = json.Marshal(sync)
			bad, _ := json.Marshal(root)
			path := filepath.Join(t.TempDir(), "report.json")
			if err := os.WriteFile(path, bad, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadCurrentReport(path); err == nil {
				t.Fatal("unbound persisted sync counters accepted")
			}
		})
	}
}
