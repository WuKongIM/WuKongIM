package chatlifecycle

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// F1 exercises real YAML loading before any worker or network can start.
func TestLocalOneSecondP95PairedPolicyYAMLAndScope(t *testing.T) {
	for _, mode := range []string{"paired", "hot_only", "sync_only", "mixed_sync", "cold95", "missing_attempt", "formal", "capacity", "negative_sync", "sync_order"} {
		t.Run(mode, func(t *testing.T) {
			cfg := LocalConfig()
			if mode == "formal" {
				cfg = FormalConfig()
			}
			raw, err := yaml.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := yaml.Unmarshal(raw, &root); err != nil {
				t.Fatal(err)
			}
			lat := root["thresholds"].(map[string]any)["latency"].(map[string]any)
			hot := map[string]any{"p95": "600ms"}
			sync := map[string]any{"p95": "1s", "p999": "3s"}
			lat["hot_sendack"], lat["sync"], lat["hot_attempt_timeout"] = hot, sync, "2s"
			switch mode {
			case "hot_only":
				lat["sync"] = map[string]any{"p99": "1s", "p999": "3s"}
			case "sync_only":
				hot["p999"] = "1s"
			case "mixed_sync":
				sync["p99"] = "1s"
			case "cold95":
				lat["cold"] = map[string]any{"p95": "2s", "p999": "5s"}
			case "missing_attempt":
				delete(lat, "hot_attempt_timeout")
			case "capacity":
				root["mode"] = "capacity"
			case "negative_sync":
				sync["p95"] = "-1ns"
			case "sync_order":
				sync["p95"] = "4s"
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
			if mode != "paired" {
				if err == nil {
					t.Fatal("unrequested/ambiguous policy accepted")
				}
				return
			}
			if err != nil {
				t.Fatalf("authorized paired local P95 rejected: %v", err)
			}
			if got.Thresholds.Latency.HotSendACK.P95 != 600*time.Millisecond || got.Thresholds.Latency.HotSendACK.P99 != 0 || got.Thresholds.Latency.HotSendACK.P999 != 0 || got.Thresholds.Latency.Sync.P95 != time.Second || got.Thresholds.Latency.Sync.P99 != 0 || got.Thresholds.Latency.Sync.P999 != 3*time.Second {
				t.Fatal("paired policy changed or retained old quantile")
			}
			if got.Thresholds.Latency.Cold != cfg.Thresholds.Latency.Cold || got.Thresholds.Latency.hotAttemptTimeout() != 2*time.Second {
				t.Fatal("unrequested cold/first-attempt change")
			}
			encoded, err := json.Marshal(got.Thresholds.Latency.HotSendACK)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != `{"p95":600000000}` {
				t.Fatalf("inactive hot P999 must be absent, got %s", encoded)
			}
		})
	}
}

// F2 consumes registered positive ACKs through the real verifier callback.
func TestLocalOneSecondP95ActualACKBoundaries(t *testing.T) {
	model, _ := newTestVerifier(t, 64, 64, 64, time.Minute)
	evidence, _ := NewEvidenceRecorder(4, 4)
	v, err := NewVerifier(model, VerifierConfig{PendingCapacity: 64, SequenceCapacity: 64, CorrelationCapacity: 64, CorrelationDeadline: time.Minute, HotLatency: LatencyLimit{P95: 600 * time.Millisecond}}, evidence)
	if err != nil {
		t.Fatal(err)
	}
	for i, duration := range []time.Duration{600 * time.Millisecond, 600*time.Millisecond + 1, time.Second + 1, 11 * time.Second} {
		meterAck(t, model, v, uint64(i+1), duration, false)
	}
	got := v.Snapshot().HotSendackThresholds
	if got.P95Limit != 600*time.Millisecond || got.P99Limit != 0 || got.P999Limit != 0 || got.Count != 4 || got.AboveP95 != 3 || got.AboveP99 != 0 || got.AboveP999 != 0 || got.Above10Seconds != 1 {
		t.Fatalf("actual ACK lost P95-only/disabled-tail/anomaly semantics: %+v", got)
	}
}

// F2 reaches successful full-sync settlement through the public Login seam.
func TestLocalOneSecondP95ActualSyncBoundaries(t *testing.T) {
	f := newSessionTestFixture(t)
	pool, err := NewSessionPool(SessionPoolConfig{Identity: f.identity, Schedule: f.schedule, Catalog: f.catalog, Factory: f.factory, Syncer: f.syncer, Verifier: f.verifier, Clock: f.clock, DeviceID: "wkbench-lifecycle", StartingCapacity: 128, SyncLatency: LatencyLimit{P95: time.Second, P999: 3 * time.Second}, SingleAnomaly: 10 * time.Second})
	if err != nil {
		t.Fatalf("authorized P95 sync pool rejected: %v", err)
	}
	t.Cleanup(func() {
		if err := pool.CloseAll(); err != nil {
			t.Error(err)
		}
	})
	for i, duration := range []time.Duration{time.Second, time.Second + 1, 3 * time.Second, 3*time.Second + 1} {
		f.syncer.onSync = func() { f.clock.Set(f.clock.Now().Add(duration)) }
		index := uint64(80 + i)
		uid := f.identity.UID(index)
		if _, err := pool.Login(context.Background(), SessionLogin{UID: uid, UserIndex: index, LoginOrdinal: uint64(i + 1)}); err != nil {
			t.Fatal(err)
		}
		if err := pool.Logout(uid); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := pool.Snapshot()
	got := snapshot.ConversationSyncThresholds
	if snapshot.ConversationSyncLatency.Count != 4 || got.P95Limit != time.Second || got.P99Limit != 0 || got.P999Limit != 3*time.Second || got.Count != 4 || got.AboveP95 != 3 || got.AboveP99 != 0 || got.AboveP999 != 1 || got.Above10Seconds != 0 {
		t.Fatalf("actual sync completion lost exact1s/3s boundaries: %+v", got)
	}
}

func pairedLocalP95Fixture() Config {
	cfg := LocalConfig()
	cfg.Thresholds.Latency.HotSendACK = LatencyLimit{P95: 600 * time.Millisecond}
	cfg.Thresholds.Latency.Sync = LatencyLimit{P95: time.Second, P999: 3 * time.Second}
	cfg.Thresholds.Latency.HotAttemptTimeout = 2 * time.Second
	return cfg
}

// Twenty actual-duration-shaped observations have exactly one strictly over1s.
// The literal5% example is independent of the production aggregation algorithm.
func pairedP95Snapshots(fence WorkerFence, sequence uint64, uptime time.Duration) []WorkerSnapshot {
	snapshots := coordinatorSnapshotFixture(fence, sequence, uptime, 10)
	for i := range snapshots {
		snapshots[i].HotSendackThresholds = LatencyThresholdCounters{P95Limit: 600 * time.Millisecond, Count: uint64(10 + i)}
		histogram := newWorkerHistogramSnapshot()
		histogram.Count = 20
		histogram.SumNanos = uint64(20*time.Second + 1)
		histogram.MaxNanos = uint64(time.Second + 1)
		histogram.Buckets[10] = 19
		histogram.Buckets[11] = 1
		snapshots[i].Sync.Latency = histogram
		snapshots[i].Sync.Thresholds = LatencyThresholdCounters{P95Limit: time.Second, P999Limit: 3 * time.Second, Count: 20, AboveP95: 1}
	}
	return snapshots
}

// F3 drives the public coordinator admission/reducer and native projection.
func TestLocalOneSecondP95CoordinatorAggregationAndSchemaFences(t *testing.T) {
	cfg := pairedLocalP95Fixture()
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "paired-p95", Generation: 1}
	control, err := NewCoordinatorSnapshotAggregator(fence)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Aggregate(coordinatorSnapshotFixture(fence, 1, time.Second, 10)); err != nil {
		t.Fatalf("same-fence legacy control failed: %v", err)
	}
	t.Log("same-fence legacy snapshot control accepted")
	for _, mutation := range []string{"valid", "mixed_worker", "hot_tail_reactivated", "inactive_sync99", "sync_partial_count", "sync95_regression", "all_sync_limit_drift"} {
		t.Run(mutation, func(t *testing.T) {
			aggregator, err := NewCoordinatorSnapshotAggregator(fence)
			if err != nil {
				t.Fatal(err)
			}
			first := pairedP95Snapshots(fence, 1, time.Second)
			aggregate, err := aggregator.Aggregate(first)
			if err != nil {
				t.Fatalf("authorized paired P95 snapshot rejected: %v", err)
			}
			if aggregate.HotSendackLatency.Count != 33 || aggregate.Sync.Thresholds.Count != 60 || aggregate.Sync.Thresholds.AboveP95 != 3 {
				t.Fatalf("lost exact public sums: hot=%+v sync=%+v", aggregate.HotSendackLatency, aggregate.Sync.Thresholds)
			}
			_, latency, _, err := projectWorkerVerdictEvidence(cfg, first, LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()})
			if err != nil || latency.Hot.Count != 33 || latency.Hot.P999Limit != 0 || latency.Hot.AboveP999 != 0 || latency.Sync.P95Limit != time.Second || latency.Sync.P99Limit != 0 || latency.Sync.Count != 60 || latency.Sync.AboveP95 != 3 {
				t.Fatalf("native policy projection lost P95: %+v err=%v", latency, err)
			}
			if mutation == "valid" {
				return
			}
			next := pairedP95Snapshots(fence, 2, 2*time.Second)
			switch mutation {
			case "mixed_worker":
				next[1].Sync.Thresholds.P95Limit = 500 * time.Millisecond
			case "hot_tail_reactivated":
				next[0].HotSendackThresholds.P999Limit = time.Second
			case "inactive_sync99":
				next[0].Sync.Thresholds.AboveP99 = 1
			case "sync_partial_count":
				next[0].Sync.Thresholds.Count++
			case "sync95_regression":
				next[0].Sync.Thresholds.AboveP95 = 0
			case "all_sync_limit_drift":
				for i := range next {
					next[i].Sync.Thresholds.P95Limit = 500 * time.Millisecond
				}
			}
			if _, err := aggregator.Aggregate(next); err == nil {
				t.Fatal("changed/mixed/partial paired P95 evidence accepted")
			}
		})
	}
}

// F4 uses real native reducers with deterministic clocked cumulative cuts.
func TestLocalOneSecondP95NativeRollingGates(t *testing.T) {
	for _, tc := range []struct {
		name            string
		hot, sync, tail uint64
		cold            uint64
		failure         bool
		cause           VerdictCause
		anomaly         bool
	}{
		{name: "both_exact_five_percent", hot: 50, sync: 50, tail: 1},
		{name: "hot_above_five_percent", hot: 51, sync: 50, tail: 1, failure: true, cause: VerdictCauseHotLatency},
		{name: "sync_above_five_percent", hot: 50, sync: 51, tail: 1, failure: true, cause: VerdictCauseSyncLatency},
		{name: "sync_tail_retained", hot: 16, sync: 2, tail: 2, failure: true, cause: VerdictCauseSyncLatency},
		{name: "cold99_retained", hot: 16, sync: 50, tail: 1, cold: 11, failure: true, cause: VerdictCauseColdLatency},
		{name: "hot_no_tail_anomaly_retained", hot: 16, sync: 50, tail: 1, anomaly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := pairedLocalP95Fixture()
			start := time.Unix(2100000000, 0)
			evaluator, err := NewVerdictEvaluator(start, cfg.Thresholds)
			if err != nil {
				t.Fatal(err)
			}
			counters := LatencyCountersForThresholds(cfg.Thresholds.Latency)
			if counters.Sync.P95Limit != time.Second || counters.Sync.P99Limit != 0 || counters.Hot.P999Limit != 0 {
				t.Fatalf("native paired schema lost: %+v", counters)
			}
			for i := 0; i <= 61; i++ {
				n := uint64(i)
				counters.Hot.Count = n * 1000
				counters.Hot.AboveP95 = n * tc.hot
				counters.Cold.Count = n * 1000
				counters.Cold.AboveP99 = n * tc.cold
				counters.Sync.Count = n * 1000
				counters.Sync.AboveP95 = n * tc.sync
				counters.Sync.AboveP999 = n * tc.tail
				if tc.anomaly {
					counters.Hot.Above10Seconds = n
				}
				if err := evaluator.Observe(VerdictObservation{At: start.Add(cfg.Thresholds.Timeline.LatencyWarmup + time.Duration(i)*5*time.Second), Latency: &counters}); err != nil {
					t.Fatal(err)
				}
			}
			got := evaluator.Snapshot()
			if got.Terminal != tc.failure || (tc.failure && (got.Outcome != VerdictProductFailure || got.Cause != tc.cause)) || (!tc.failure && got.Outcome == VerdictHarnessInvalid) {
				t.Fatalf("paired native gate=%+v expected failure=%v cause=%v", got, tc.failure, tc.cause)
			}
			if tc.anomaly && got.LatencyAnomalyCount != 61 {
				t.Fatalf("hot no-tail suppressed ten-second anomaly: %+v", got)
			}
			if evaluator.LatencyWindowEvidence().Qualified == ([3]bool{true, true, true}) {
				t.Fatal("305seconds became qualified600seconds")
			}
		})
	}
}

func TestLocalOneSecondP95QualificationFreshnessAndInactiveTail(t *testing.T) {
	for _, name := range []string{"qualified", "sync999_samples", "inactive_hot_tail"} {
		t.Run(name, func(t *testing.T) {
			cfg := pairedLocalP95Fixture()
			start := time.Unix(2100000000, 0)
			v, err := NewVerdictEvaluator(start, cfg.Thresholds)
			if err != nil {
				t.Fatal(err)
			}
			c := LatencyCountersForThresholds(cfg.Thresholds.Latency)
			if c.Sync.P95Limit != time.Second {
				t.Fatal("sync P95 native initializer missing")
			}
			for i := 0; i <= 120; i++ {
				n := uint64(i)
				c.Hot.Count = n * 1000
				c.Cold.Count = n * 1000
				c.Sync.Count = n * 1000
				if name == "sync999_samples" {
					c.Sync.Count = n * 999 / 120
				}
				if name == "inactive_hot_tail" && i > 0 {
					c.Hot.AboveP999 = 1
				}
				if err := v.Observe(VerdictObservation{At: start.Add(cfg.Thresholds.Timeline.LatencyWarmup + time.Duration(i)*5*time.Second), Latency: &c}); err != nil {
					t.Fatal(err)
				}
				if name == "inactive_hot_tail" && i > 0 {
					if v.Snapshot().Outcome != VerdictHarnessInvalid {
						t.Fatal("inactive hot tail count silently accepted")
					}
					return
				}
			}
			want := [3]bool{true, true, true}
			if name == "sync999_samples" {
				want[2] = false
			}
			if got := v.LatencyWindowEvidence(); got.Qualified != want || got.Duration != 10*time.Minute {
				t.Fatalf("paired qualification=%+v want=%v", got, want)
			}
			if err := v.Observe(VerdictObservation{At: start.Add(cfg.Thresholds.Timeline.LatencyWarmup + 606*time.Second)}); err != nil {
				t.Fatal(err)
			}
			if v.LatencyWindowEvidence().Qualified != ([3]bool{}) {
				t.Fatal("stale paired P95 evidence remained qualified")
			}
		})
	}
}

// No active hot P999 means optional samples retain primary recency only.
func TestLocalOneSecondP95OptionalAnchorsDoNotInventTail(t *testing.T) {
	model, v := slowHotSuccessFixture(t, true)
	v.config.HotLatency = pairedLocalP95Fixture().Thresholds.Latency.HotSendACK
	at := time.Unix(123456, 0)
	for i := uint64(1); i <= 20; i++ {
		slowHotSuccessACK(t, model, v, i, at.Add(time.Duration(i)*time.Second), 700*time.Millisecond, SendLatencyHot)
	}
	got := v.AttemptLinkDiagnostics(1).SlowHotSuccess
	if got == nil || !validSlowHotSuccess(got) || got.Percentile != 95 || got.RetentionPolicy != "" || got.TailLimitNanos != 0 || len(got.Records) != 16 || got.Records[0].Ordinal != 5 || got.Records[15].Ordinal != 20 {
		t.Fatalf("disabled hot tail fabricated P999 retention or lost primary samples: %+v", got)
	}
	body, err := json.Marshal(v.AttemptLinkDiagnostics(1))
	if err != nil {
		t.Fatal(err)
	}
	var decoded AttemptLinkDiagnosticsSnapshot
	if err = json.Unmarshal(body, &decoded); err != nil || decoded.SlowHotSuccess == nil {
		t.Fatalf("own optional wire dropped primary evidence: %v", err)
	}
}
