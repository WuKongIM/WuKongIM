package chatlifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// F1-F5: explicit local P95, exact scalar ACK counting and unchanged P999,
// authority, first-deadline, rolling-window and legacy P99 contracts.
// JSON deliberately drives the old production API into semantic RED first.
func p95LimitFixture(t *testing.T) LatencyLimit {
	t.Helper()
	var limit LatencyLimit
	if err := json.Unmarshal([]byte(`{"p95":600000000,"p999":1000000000}`), &limit); err != nil {
		t.Fatal(err)
	}
	return limit
}

func p95CounterFixture(t *testing.T, count, above95, above999 uint64) LatencyThresholdCounters {
	t.Helper()
	var counters LatencyThresholdCounters
	raw := fmt.Sprintf(`{"P95Limit":600000000,"P99Limit":0,"P999Limit":1000000000,"Count":%d,"AboveP95":%d,"AboveP99":0,"AboveP999":%d}`, count, above95, above999)
	if err := json.Unmarshal([]byte(raw), &counters); err != nil {
		t.Fatal(err)
	}
	return counters
}

func p95Wire(t *testing.T, value any) map[string]json.RawMessage {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestHotP95RealYAMLConfigAndScopeBoundaries(t *testing.T) {
	for _, mode := range []string{"local95", "both", "neither", "negative", "ordered", "cold95", "sync95", "formal95", "capacity95"} {
		t.Run(mode, func(t *testing.T) {
			cfg := LocalConfig()
			if mode == "formal95" {
				cfg = FormalConfig()
			}
			b, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := yaml.Unmarshal(b, &root); err != nil {
				t.Fatal(err)
			}
			lat := root["thresholds"].(map[string]any)["latency"].(map[string]any)
			hot := lat["hot_sendack"].(map[string]any)
			delete(hot, "p99")
			hot["p95"] = "600ms"
			hot["p999"] = "1s"
			switch mode {
			case "both":
				hot["p99"] = "600ms"
			case "neither":
				delete(hot, "p95")
			case "negative":
				hot["p95"] = "-1ns"
			case "ordered":
				hot["p95"] = "2s"
			case "cold95", "sync95":
				key := strings.TrimSuffix(mode, "95")
				row := lat[key].(map[string]any)
				row["p95"] = row["p99"]
				delete(row, "p99")
			case "capacity95":
				root["mode"] = "capacity"
			}
			body, err := yaml.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, body, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadConfig(path)
			if mode != "local95" {
				if err == nil {
					t.Fatal("out-of-scope/ambiguous P95 config accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("authorized local P95 rejected before runtime: %v", err)
			}
			wire := p95Wire(t, got.Thresholds.Latency.HotSendACK)
			if string(wire["p95"]) != "600000000" || (len(wire["p99"]) != 0 && string(wire["p99"]) != "0") || got.Thresholds.Latency.HotSendACK.P999 != time.Second {
				t.Fatalf("wrong P95 config: %s", body)
			}
			if got.Thresholds.Latency.Cold != cfg.Thresholds.Latency.Cold || got.Thresholds.Latency.Sync != cfg.Thresholds.Latency.Sync || got.Thresholds.Latency.hotAttemptTimeout() != cfg.Thresholds.Latency.hotAttemptTimeout() {
				t.Fatal("unrequested gate/budget changed")
			}
		})
	}
	legacy := LocalConfig()
	before, err := json.Marshal(legacy.Thresholds.Latency.HotSendACK)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != `{"p99":400000000,"p999":1000000000}` {
		t.Fatalf("legacy threshold bytes changed: %s", before)
	}
	changed := legacy
	changed.Thresholds.Latency.HotSendACK = p95LimitFixture(t)
	a, _ := digestCheckpointConfig(legacy)
	b, _ := digestCheckpointConfig(changed)
	if a == b {
		t.Fatal("P95 config not digest-bound")
	}
}

func TestHotP95RealACKBoundariesResetAndProductionEmptyCut(t *testing.T) {
	model, _ := newTestVerifier(t, 64, 64, 64, time.Minute)
	evidence, _ := NewEvidenceRecorder(4, 4)
	v, err := NewVerifier(model, VerifierConfig{PendingCapacity: 64, SequenceCapacity: 64, CorrelationCapacity: 64, CorrelationDeadline: time.Minute, HotLatency: p95LimitFixture(t)}, evidence)
	if err != nil {
		t.Fatal(err)
	}
	initial := p95Wire(t, v.Snapshot().HotSendackThresholds)
	if string(initial["P95Limit"]) != "600000000" {
		t.Fatalf("P95 producer missing/wrong schema: %+v", initial)
	}
	for i, d := range []time.Duration{599 * time.Millisecond, 600 * time.Millisecond, 600*time.Millisecond + 1, time.Second, time.Second + 1, 11 * time.Second} {
		meterAck(t, model, v, uint64(i+1), d, false)
	}
	c := v.Snapshot().HotSendackThresholds
	wire := p95Wire(t, c)
	if c.Count != 6 || string(wire["AboveP95"]) != "4" || c.AboveP99 != 0 || c.AboveP999 != 2 || c.Above10Seconds != 1 {
		t.Fatalf("wrong exact selected counts: %+v / %+v", c, wire)
	}
	meterAck(t, model, v, 7, -time.Nanosecond, false)
	meterAck(t, model, v, 8, 0, true)
	if v.Snapshot().HotSendackThresholds != c {
		t.Fatal("unknown/negative clock invented latency")
	}
	v.resetRuntime()
	reset := v.Snapshot().HotSendackThresholds
	if reset.Count != 0 || string(p95Wire(t, reset)["P95Limit"]) != "600000000" || string(p95Wire(t, reset)["AboveP95"]) != "" {
		t.Fatalf("reset lost fixed P95 limits/counts: %+v", reset)
	}
	cfg := LocalConfig()
	cfg.Thresholds.Latency.HotSendACK = p95LimitFixture(t)
	snapshots := make([]WorkerSnapshot, 3)
	for worker := uint64(0); worker < 3; worker++ {
		g, err := NewEngineWorkerGenerationFactory("p95-fixture").New(WorkerAssignment{WorkerFence: WorkerFence{RunID: cfg.RunID, AssignmentID: fmt.Sprint("p95-", worker), Generation: 1}, WorkerID: worker, WorkerCount: 3, Config: cfg})
		if err != nil {
			t.Fatal(err)
		}
		e := g.(*engineWorkerGeneration).engine
		if err = e.StartGeneration(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := e.Stop(); err != nil {
				t.Error(err)
			}
		})
		snapshots[worker], err = g.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
	}
	_, lat, _, err := projectWorkerVerdictEvidence(cfg, snapshots, LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()})
	if err != nil {
		t.Fatalf("accepted local600ms P95 factory cut invalid: %v", err)
	}
	if string(p95Wire(t, lat.Hot)["P95Limit"]) != "600000000" || lat.Hot.Count != 0 {
		t.Fatal("factory wiring lost P95 schema")
	}
	for _, mutation := range []string{"missing", "wrong_limit", "inactive99", "overflow"} {
		t.Run(mutation, func(t *testing.T) {
			copy := append([]WorkerSnapshot(nil), snapshots...)
			c := copy[0].HotSendackThresholds
			switch mutation {
			case "missing":
				c = LatencyThresholdCounters{}
			case "wrong_limit":
				c.P999Limit = 2 * time.Second
			case "inactive99":
				c.AboveP99 = 1
			case "overflow":
				c.Count = math.MaxUint64
			}
			copy[0].HotSendackThresholds = c
			if _, _, _, err := projectWorkerVerdictEvidence(cfg, copy, LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}); err == nil {
				t.Fatal("malformed P95 evidence accepted")
			}
		})
	}
}

func TestHotP95ExactFivePercentAndIndependentP999SustainedGate(t *testing.T) {
	for _, tc := range []struct {
		name              string
		above95, above999 uint64
		operation         int
		failure           bool
	}{
		{"at_five_percent", 50, 1, 0, false}, {"above_five_percent", 51, 1, 0, true}, {"P999_still_fails", 2, 2, 0, true}, {"cold_keeps99", 0, 0, 1, true}, {"sync_keeps99", 0, 0, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
					c.Hot = p95CounterFixture(t, uint64(i)*1000, uint64(i)*tc.above95, uint64(i)*tc.above999)
					c.Cold.Count = uint64(i) * 1000
					c.Sync.Count = uint64(i) * 1000
					if tc.operation == 1 {
						c.Cold.AboveP99 = uint64(i) * 11
					}
					if tc.operation == 2 {
						c.Sync.AboveP99 = uint64(i) * 11
					}
				}
				if err := v.Observe(VerdictObservation{At: start.Add(cfg.Thresholds.Timeline.LatencyWarmup + time.Duration(i)*5*time.Second), Latency: &c}); err != nil {
					t.Fatal(err)
				}
			}
			got := v.Snapshot()
			if got.Terminal != tc.failure || (tc.failure && got.Outcome != VerdictProductFailure) {
				t.Fatalf("selected percentile gate=%+v expected failure%v", got, tc.failure)
			}
			if !tc.failure && v.LatencyWindowEvidence().Qualified == ([3]bool{true, true, true}) {
				t.Fatal("short window acquired positive qualification")
			}
		})
	}
}

func TestHotP95CheckedAggregationRegressionAndPositiveQualification(t *testing.T) {
	limits := p95LimitFixture(t)
	hist := newWorkerHistogramSnapshot()
	hist.Count = 1000
	hist.Buckets[9] = 950
	hist.Buckets[10] = 49
	hist.Buckets[11] = 1
	hist.SumNanos = uint64(430*time.Second) + 1
	hist.MaxNanos = uint64(time.Second) + 1
	s := WorkerSnapshot{HotSendackLatency: hist, HotSendackThresholds: p95CounterFixture(t, 1000, 50, 1)}
	got, err := aggregateWorkerHotThresholds([]WorkerSnapshot{s, s, s}, limits)
	if err != nil || got.Count != 3000 || string(p95Wire(t, got)["AboveP95"]) != "150" {
		t.Fatalf("checked P95 aggregation=%+v/%v", got, err)
	}
	for _, key := range []string{"P95Limit", "AboveP95", "P99Limit", "AboveP99"} {
		t.Run(key, func(t *testing.T) {
			wire := p95Wire(t, s.HotSendackThresholds)
			switch key {
			case "P95Limit":
				wire[key] = json.RawMessage(`500000000`)
			case "AboveP95":
				wire[key] = json.RawMessage(`1001`)
			case "P99Limit":
				wire[key] = json.RawMessage(`600000000`)
			case "AboveP99":
				wire[key] = json.RawMessage(`1`)
			}
			raw, _ := json.Marshal(wire)
			bad := s
			_ = json.Unmarshal(raw, &bad.HotSendackThresholds)
			if _, err := aggregateWorkerHotThresholds([]WorkerSnapshot{bad, s, s}, limits); err == nil {
				t.Fatal("bad quantile counters accepted")
			}
		})
	}
	large := WorkerSnapshot{HotSendackLatency: newWorkerHistogramSnapshot(), HotSendackThresholds: p95CounterFixture(t, math.MaxUint64, 0, 0)}
	large.HotSendackLatency.Count = math.MaxUint64
	large.HotSendackLatency.Buckets[0] = math.MaxUint64
	if _, err := aggregateWorkerHotThresholds([]WorkerSnapshot{large, s}, limits); err == nil {
		t.Fatal("P95 sum overflow wrapped")
	}
	previous := p95CounterFixture(t, 1000, 50, 1)
	current := p95CounterFixture(t, 2000, 49, 1)
	if !hotThresholdsRegressed(current, previous) {
		t.Fatal("P95 regression missed")
	}
	cfg := LocalConfig()
	cfg.Thresholds.Latency.HotSendACK = limits
	start := time.Unix(2100000000, 0)
	v, err := NewVerdictEvaluator(start, cfg.Thresholds)
	if err != nil {
		t.Fatal(err)
	}
	c := LatencyCountersForThresholds(cfg.Thresholds.Latency)
	for i := 0; i <= 120; i++ {
		c.Hot = p95CounterFixture(t, uint64(i)*1000, 0, 0)
		c.Cold.Count = uint64(i) * 1000
		c.Sync.Count = uint64(i) * 1000
		if err := v.Observe(VerdictObservation{At: start.Add(cfg.Thresholds.Timeline.LatencyWarmup + time.Duration(i)*5*time.Second), Latency: &c}); err != nil {
			t.Fatal(err)
		}
	}
	if v.LatencyWindowEvidence().Qualified != ([3]bool{true, true, true}) {
		t.Fatal("fresh complete600s/1000each qualification failed")
	}
	if err := v.Observe(VerdictObservation{At: start.Add(cfg.Thresholds.Timeline.LatencyWarmup + 606*time.Second)}); err != nil {
		t.Fatal(err)
	}
	if v.LatencyWindowEvidence().Qualified != ([3]bool{}) {
		t.Fatal("stale P95 evidence remained qualified")
	}
}

func TestHotP95PassiveDiagnosticSelectionAndProtocolBound(t *testing.T) {
	if workerProtocolVersion != 18 {
		t.Fatalf("new P95 live counters need exactsame-source protocol18, got%d", workerProtocolVersion)
	}
	model := newTestTrafficModel(t, FormalConfig())
	evidence, _ := NewEvidenceRecorder(2, 2)
	v, err := NewVerifier(model, VerifierConfig{PendingCapacity: 64, SequenceCapacity: 64, CorrelationCapacity: 64, CorrelationDeadline: 10 * time.Second, EnableAttemptLinkDiagnostics: true, HotLatency: p95LimitFixture(t)}, evidence)
	if err != nil {
		t.Fatal(err)
	}
	slowHotSuccessACK(t, model, v, 1, time.Unix(123456, 0), 600*time.Millisecond, SendLatencyHot)
	slowHotSuccessACK(t, model, v, 2, time.Unix(123457, 0), 600*time.Millisecond+1, SendLatencyHot)
	d := v.AttemptLinkDiagnostics(1).SlowHotSuccess
	wire := p95Wire(t, d)
	if d.Seen != 1 || d.LimitNanos != 600*time.Millisecond || string(wire["percentile"]) != "95" {
		t.Fatalf("P95 diagnostic tail semantics=%+v", wire)
	}
	snapshots := []WorkerSnapshot{{HotSendackLatency: v.Snapshot().HotSendackLatency, HotSendackThresholds: v.Snapshot().HotSendackThresholds}}
	r := newLiveDiagnosticRecorder(t.TempDir(), "p95", time.Unix(1, 0), nil, p95LimitFixture(t))
	log := r.hotLatencyCounters(snapshots)
	if log == nil || string(p95Wire(t, log)["percentile"]) != "95" || log.AboveLimit != 1 {
		t.Fatalf("P95 live log lost selected quantile: %+v", log)
	}
}
