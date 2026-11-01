package chatlifecycle

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// Failure inventory: configured600ms needs interpolation; exact/equal/1ns-above
// boundaries differ across recording/projection/logs; negative or unknown clocks
// invent samples; counters alias/reset incorrectly; malformed or regressing
// peer evidence is accepted; overflow wraps; legacy400ms is reinterpreted; or
// old/new worker peers silently mix. These tests drive real ACK settlement and
// existing snapshot/projection consumers. JSON intentionally exercises missing
// fields on the pre-change implementation instead of a non-compiling test API.
func configuredMeterVerifier(t *testing.T) (TrafficModel, *Verifier) {
	t.Helper()
	model, _ := newTestVerifier(t, 64, 64, 64, time.Minute)
	evidence, err := NewEvidenceRecorder(4, 4)
	if err != nil {
		t.Fatal(err)
	}
	config := VerifierConfig{PendingCapacity: 64, SequenceCapacity: 64, CorrelationCapacity: 64, CorrelationDeadline: time.Minute}
	if err := json.Unmarshal([]byte(`{"HotLatency":{"p99":600000000,"p999":1000000000}}`), &config); err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(model, config, evidence)
	if err != nil {
		t.Fatal(err)
	}
	return model, v
}
func hotCounterWire(t *testing.T, value any, field string) LatencyThresholdCounters {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err = json.Unmarshal(b, &object); err != nil {
		t.Fatal(err)
	}
	raw, ok := object[field]
	if !ok {
		t.Fatalf("configured exact hot counters absent from %s", field)
	}
	var counters LatencyThresholdCounters
	if err = json.Unmarshal(raw, &counters); err != nil {
		t.Fatal(err)
	}
	return counters
}
func meterAck(t *testing.T, model TrafficModel, v *Verifier, ordinal uint64, duration time.Duration, unknown bool) {
	t.Helper()
	logical := mustLogicalSend(t, model, 0, ordinal, TrafficPerson, "meter-sender", "meter-recipient")
	at := time.Unix(100, 0)
	if err := v.RegisterSend(logical, at, SendLatencyHot); err != nil {
		t.Fatal(err)
	}
	attempt, err := newTestRetryPolicy(t, model).Attempt(logical, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.ObserveAttempt(logical, attempt, ordinal); err != nil {
		t.Fatal(err)
	}
	ack := &frame.SendackPacket{ClientSeq: ordinal, ClientMsgNo: logical.ClientMsgNo, MessageID: int64(ordinal), MessageSeq: ordinal, ReasonCode: frame.ReasonSuccess}
	completed := at.Add(duration)
	if unknown {
		completed = time.Time{}
	}
	if err = v.HandleSendackAt(ack, completed); err != nil {
		t.Fatal(err)
	}
	if err = v.ReleaseSend(logical); err != nil {
		t.Fatal(err)
	}
}
func TestConfiguredHotMeterRecordsRealACKBoundariesAndResets(t *testing.T) {
	model, v := configuredMeterVerifier(t)
	initial := hotCounterWire(t, v.Snapshot(), "HotSendackThresholds")
	if initial.P99Limit != 600*time.Millisecond || initial.P999Limit != time.Second || initial.Count != 0 {
		t.Fatalf("initial=%+v", initial)
	}
	for i, d := range []time.Duration{599 * time.Millisecond, 600 * time.Millisecond, 600*time.Millisecond + 1, time.Second, time.Second + 1, 11 * time.Second} {
		meterAck(t, model, v, uint64(i+1), d, false)
	}
	snapshot := v.Snapshot()
	got := hotCounterWire(t, snapshot, "HotSendackThresholds")
	if got.Count != 6 || got.AboveP99 != 4 || got.AboveP999 != 2 || got.Above10Seconds != 1 || snapshot.HotSendackLatency.Count != 6 {
		t.Fatalf("exact boundary counts=%+v hist=%d", got, snapshot.HotSendackLatency.Count)
	}
	meterAck(t, model, v, 7, -time.Nanosecond, false)
	meterAck(t, model, v, 8, 0, true)
	if next := hotCounterWire(t, v.Snapshot(), "HotSendackThresholds"); next != got {
		t.Fatalf("unknown/negative clock fabricated latency: %+v", next)
	}
	meterAck(t, model, v, 9, 0, false)
	if old := hotCounterWire(t, snapshot, "HotSendackThresholds"); old != got {
		t.Fatal("detached snapshot changed")
	}
	if next := hotCounterWire(t, v.Snapshot(), "HotSendackThresholds"); next.Count != 7 {
		t.Fatal("known zero duration lost")
	}
	v.resetRuntime()
	reset := hotCounterWire(t, v.Snapshot(), "HotSendackThresholds")
	if reset.Count != 0 || reset.P99Limit != got.P99Limit || reset.P999Limit != got.P999Limit {
		t.Fatalf("generation reset lost schema: %+v", reset)
	}
	meterAck(t, model, v, 1, 600*time.Millisecond+1, false)
	if next := hotCounterWire(t, v.Snapshot(), "HotSendackThresholds"); next.Count != 1 || next.AboveP99 != 1 {
		t.Fatalf("new generation counter=%+v", next)
	}
}
func injectHotCounter(t *testing.T, s WorkerSnapshot, c LatencyThresholdCounters) WorkerSnapshot {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err = json.Unmarshal(b, &obj); err != nil {
		t.Fatal(err)
	}
	obj["hot_sendack_thresholds"], err = json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	b, err = json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}
func meteredWorkerFixture(t *testing.T) (Config, []WorkerSnapshot) {
	t.Helper()
	cfg := LocalConfig()
	cfg.Thresholds.Latency.HotSendACK = LatencyLimit{P99: 600 * time.Millisecond, P999: time.Second}
	snapshots := coordinatorSnapshotFixture(WorkerFence{RunID: cfg.RunID, AssignmentID: "exact-meter", Generation: 1}, 1, time.Minute, 0)
	for i := range snapshots {
		snapshots[i].HotSendackLatency = newWorkerHistogramSnapshot()
		snapshots[i].Sync.Thresholds = LatencyThresholdCounters{P99Limit: cfg.Thresholds.Latency.Sync.P99, P999Limit: cfg.Thresholds.Latency.Sync.P999}
		counters := LatencyThresholdCounters{P99Limit: 600 * time.Millisecond, P999Limit: time.Second}
		if i == 0 {
			for _, d := range []time.Duration{399 * time.Millisecond, 600 * time.Millisecond, 600*time.Millisecond + 1, time.Second, time.Second + 1, 11 * time.Second} {
				recordWorkerLatency(&snapshots[i].HotSendackLatency, d)
			}
			counters.Count = 6
			counters.AboveP99 = 4
			counters.AboveP999 = 2
			counters.Above10Seconds = 1
		}
		snapshots[i] = injectHotCounter(t, snapshots[i], counters)
	}
	return cfg, snapshots
}
func TestConfiguredHotMeterProjectionAndLiveLogShareExactCounters(t *testing.T) {
	cfg, s := meteredWorkerFixture(t)
	_, latency, _, err := projectWorkerVerdictEvidence(cfg, s, LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()})
	if err != nil {
		t.Fatal(err)
	}
	if latency.Hot.Count != 6 || latency.Hot.AboveP99 != 4 || latency.Hot.AboveP999 != 2 || latency.Hot.P99Limit != 600*time.Millisecond {
		t.Fatalf("projection=%+v", latency.Hot)
	}
	r := newLiveDiagnosticRecorder(t.TempDir(), cfg.RunID, time.Unix(100, 0), nil, cfg.Thresholds.Latency.HotSendACK)
	hot := r.hotLatencyCounters(s)
	if hot == nil || hot.LimitNanos != uint64(600*time.Millisecond) || hot.Count != 6 || hot.AboveLimit != 4 {
		t.Fatalf("configured diagnostic missing/wrong: %+v", hot)
	}
	if len(s[0].HotSendackLatency.BucketUpper) != 16 || ReportSchemaVersion != "wukongim/chat-lifecycle-report/v8" {
		t.Fatal("legacy histogram/report shape changed")
	}
	if workerProtocolVersion != 18 {
		t.Fatalf("new native evidence did not isolate old peers: protocol=%d", workerProtocolVersion)
	}
	round := hotCounterWire(t, s[0], "hot_sendack_thresholds")
	if round != latency.Hot {
		t.Fatalf("worker JSON lost exact evidence: %+v", round)
	}
}
func TestConfiguredHotMeterRejectsMissingMismatchAndOverflow(t *testing.T) {
	for _, name := range []string{"missing", "limits", "tail_limit", "count", "above_count", "tail_count", "anomaly_count", "overflow", "histogram_sum_overflow", "hot_under_histogram", "hot_over_histogram", "tail_histogram", "anomaly_histogram"} {
		t.Run(name, func(t *testing.T) {
			cfg, s := meteredWorkerFixture(t)
			c := LatencyThresholdCounters{P99Limit: 600 * time.Millisecond, P999Limit: time.Second, Count: 6, AboveP99: 4, AboveP999: 2, Above10Seconds: 1}
			switch name {
			case "hot_under_histogram":
				c.AboveP99 = 1
				c.AboveP999 = 0
				c.Above10Seconds = 0
			case "hot_over_histogram":
				c.AboveP99 = 6
			case "tail_histogram":
				c.AboveP999 = 3
			case "anomaly_histogram":
				c.Above10Seconds = 0
			case "missing":
				c = LatencyThresholdCounters{}
			case "limits":
				c.P99Limit = 400 * time.Millisecond
			case "tail_limit":
				c.P999Limit = 2 * time.Second
			case "count":
				c.Count = 7
			case "above_count":
				c.AboveP99 = 7
			case "tail_count":
				c.AboveP999 = 5
			case "anomaly_count":
				c.Above10Seconds = 3
			case "histogram_sum_overflow":
				for i := range s {
					s[i].HotSendackLatency.SumNanos = math.MaxUint64/2 + 1
				}
			case "overflow":
				for i := range s {
					n := uint64(math.MaxUint64/2 + 1)
					s[i].HotSendackLatency = newWorkerHistogramSnapshot()
					s[i].HotSendackLatency.Count = n
					s[i].HotSendackLatency.Buckets[0] = n
					s[i] = injectHotCounter(t, s[i], LatencyThresholdCounters{P99Limit: 600 * time.Millisecond, P999Limit: time.Second, Count: n})
				}
			}
			if name != "overflow" {
				s[0] = injectHotCounter(t, s[0], c)
			}
			if _, _, _, err := projectWorkerVerdictEvidence(cfg, s, LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()}); err == nil {
				t.Fatal("invalid exact metering accepted")
			}
		})
	}
}
func TestConfiguredHotMeterRejectsCounterRegression(t *testing.T) {
	cfg, s := meteredWorkerFixture(t)
	fence := WorkerFence{RunID: cfg.RunID, AssignmentID: "exact-meter", Generation: 1}
	a, err := NewCoordinatorSnapshotAggregator(fence)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Aggregate(s); err != nil {
		t.Fatal(err)
	}
	for i := range s {
		s[i].SnapshotSequence++
		s[i].Uptime += time.Second
	}
	s[0] = injectHotCounter(t, s[0], LatencyThresholdCounters{P99Limit: 600 * time.Millisecond, P999Limit: time.Second, Count: 6, AboveP99: 3, AboveP999: 2, Above10Seconds: 1})
	if _, err = a.Aggregate(s); err == nil {
		t.Fatal("exact threshold counter regression accepted")
	}
}
func TestConfiguredHotMeterLegacy400msStillUsesExactHistogram(t *testing.T) {
	cfg := LocalConfig()
	cfg.Thresholds.Latency.HotSendACK = LatencyLimit{P99: 400 * time.Millisecond, P999: time.Second}
	s := coordinatorSnapshotFixture(WorkerFence{RunID: cfg.RunID, AssignmentID: "legacy-meter", Generation: 1}, 1, time.Minute, 1)
	for i := range s {
		s[i].Sync.Thresholds = LatencyThresholdCounters{P99Limit: cfg.Thresholds.Latency.Sync.P99, P999Limit: cfg.Thresholds.Latency.Sync.P999}
	}
	_, latency, _, err := projectWorkerVerdictEvidence(cfg, s, LifecycleProofSnapshot{ReheatLatency: newWorkerHistogramSnapshot()})
	if err != nil {
		t.Fatal(err)
	}
	if latency.Hot.Count != 6 || latency.Hot.AboveP99 != 0 {
		t.Fatalf("legacy canonical projection=%+v", latency.Hot)
	}
}
