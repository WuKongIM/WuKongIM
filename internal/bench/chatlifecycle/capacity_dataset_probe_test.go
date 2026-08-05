package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

func TestCapacityDatasetProbeReadsEveryDeclaredNodeAndBuildsOneAgedDigest(t *testing.T) {
	const token = "dataset-probe-secret"
	generatedAt := time.Date(2030, time.March, 17, 17, 46, 40, 0, time.UTC)
	observedAt := generatedAt.Add(time.Second)
	type nodeHits struct {
		mu      sync.Mutex
		config  int
		summary int
	}
	hits := make([]*nodeHits, coordinatorWorkerCount)
	servers := make([]*httptest.Server, 0, coordinatorWorkerCount)
	for index := 0; index < coordinatorWorkerCount; index++ {
		index := index
		hits[index] = &nodeHits{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if request.Header.Get("Authorization") != "Bearer "+token {
				t.Fatalf("node %d authorization = %q", index+1, request.Header.Get("Authorization"))
			}
			hits[index].mu.Lock()
			defer hits[index].mu.Unlock()
			switch request.URL.Path {
			case "/debug/config":
				hits[index].config++
				_ = json.NewEncoder(w).Encode(map[string]any{
					"node_id": index + 1, "node_data_dir": "/srv/wukongim/node-" + string(rune('1'+index)),
				})
			case "/debug/goroutines/summary":
				hits[index].summary++
				_ = json.NewEncoder(w).Encode(map[string]any{
					"generated_at": generatedAt, "process_started_at": generatedAt.Add(-73 * time.Hour),
					"boot_id": "process-" + string(rune('1'+index)),
				})
			default:
				http.NotFound(w, request)
			}
		}))
		servers = append(servers, server)
		t.Cleanup(server.Close)
	}

	cfg := capacityDatasetTestConfig(t)
	for index := range cfg.Observation.ServiceNodes {
		cfg.Observation.ServiceNodes[index].Address = servers[index].URL
	}
	probe := NewCapacityDatasetProbe(CapacityDatasetProbeOptions{
		BenchToken: token,
		Clock:      capacityDatasetClockFunc(func() time.Time { return observedAt }),
	})
	evidence, err := probe.ProbeCapacityDataset(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	digest := evidence.Nodes[0].DatasetDigest
	if !validReportHash(digest) {
		t.Fatalf("dataset digest = %q", digest)
	}
	for index, node := range evidence.Nodes {
		if node.NodeID != uint64(index+1) || node.DatasetDigest != digest ||
			node.ObservedAt != observedAt || node.State != CapacityDatasetLiveAged {
			t.Fatalf("node %d evidence = %+v", index+1, node)
		}
		hits[index].mu.Lock()
		if hits[index].config != 1 || hits[index].summary != 1 {
			t.Fatalf("node %d hits = %+v", index+1, hits[index])
		}
		hits[index].mu.Unlock()
	}
}

type capacityDatasetClockFunc func() time.Time

func (f capacityDatasetClockFunc) Now() time.Time { return f() }

func TestCapacityDatasetProbeDigestIsOrderIndependentAndRestartSensitive(t *testing.T) {
	generatedAt := time.Date(2030, time.March, 17, 17, 46, 40, 0, time.UTC)
	nodes := []fakeCapacityDatasetTarget{
		{config: target.DebugConfig{NodeID: 3, NodeDataDir: "/srv/wukongim/node-3"}, summary: target.DebugGoroutineSummary{GeneratedAt: generatedAt, ProcessStartedAt: generatedAt.Add(-73 * time.Hour), BootID: "process-3"}},
		{config: target.DebugConfig{NodeID: 1, NodeDataDir: "/srv/wukongim/node-1"}, summary: target.DebugGoroutineSummary{GeneratedAt: generatedAt, ProcessStartedAt: generatedAt.Add(-73 * time.Hour), BootID: "process-1"}},
		{config: target.DebugConfig{NodeID: 2, NodeDataDir: "/srv/wukongim/node-2"}, summary: target.DebugGoroutineSummary{GeneratedAt: generatedAt, ProcessStartedAt: generatedAt.Add(-73 * time.Hour), BootID: "process-2"}},
	}
	first := probeCapacityDatasetWithTargets(t, nodes, generatedAt.Add(time.Second))

	reordered := []fakeCapacityDatasetTarget{nodes[2], nodes[0], nodes[1]}
	for index := range reordered {
		reordered[index].config.NodeDataDir = " \t" + reordered[index].config.NodeDataDir + "\n"
		reordered[index].summary.BootID = " " + reordered[index].summary.BootID + " "
	}
	second := probeCapacityDatasetWithTargets(t, reordered, generatedAt.Add(2*time.Second))
	if first.Nodes[0].DatasetDigest != second.Nodes[0].DatasetDigest {
		t.Fatalf("reordered digest = %q, want %q", second.Nodes[0].DatasetDigest, first.Nodes[0].DatasetDigest)
	}

	restarted := append([]fakeCapacityDatasetTarget(nil), nodes...)
	restarted[1].summary.BootID = "process-1-restarted"
	third := probeCapacityDatasetWithTargets(t, restarted, generatedAt.Add(3*time.Second))
	if third.Nodes[0].DatasetDigest == first.Nodes[0].DatasetDigest {
		t.Fatalf("restarted digest = %q, want a new generation", third.Nodes[0].DatasetDigest)
	}
}

func TestCapacityDatasetProbeMarksYoungNodeUnavailableAndRejectsInvalidIdentity(t *testing.T) {
	generatedAt := time.Date(2030, time.March, 17, 17, 46, 40, 0, time.UTC)
	nodes := []fakeCapacityDatasetTarget{
		{config: target.DebugConfig{NodeID: 1, NodeDataDir: "/srv/wukongim/node-1"}, summary: target.DebugGoroutineSummary{GeneratedAt: generatedAt, ProcessStartedAt: generatedAt.Add(-73 * time.Hour), BootID: "process-1"}},
		{config: target.DebugConfig{NodeID: 2, NodeDataDir: "/srv/wukongim/node-2"}, summary: target.DebugGoroutineSummary{GeneratedAt: generatedAt, ProcessStartedAt: generatedAt.Add(-72*time.Hour + time.Nanosecond), BootID: "process-2"}},
		{config: target.DebugConfig{NodeID: 3, NodeDataDir: "/srv/wukongim/node-3"}, summary: target.DebugGoroutineSummary{GeneratedAt: generatedAt, ProcessStartedAt: generatedAt.Add(-73 * time.Hour), BootID: "process-3"}},
	}
	evidence := probeCapacityDatasetWithTargets(t, nodes, generatedAt.Add(time.Second))
	if evidence.Nodes[0].State != CapacityDatasetLiveAged || evidence.Nodes[1].State != CapacityDatasetUnavailable || evidence.Nodes[2].State != CapacityDatasetLiveAged {
		t.Fatalf("dataset states = (%q, %q, %q)", evidence.Nodes[0].State, evidence.Nodes[1].State, evidence.Nodes[2].State)
	}

	cfg := capacityDatasetTestConfig(t)
	invalid := append([]fakeCapacityDatasetTarget(nil), nodes...)
	invalid[1].config.NodeID = invalid[0].config.NodeID
	probe := newCapacityDatasetFakeProbe(invalid, generatedAt.Add(2*time.Second))
	_, err := probe.ProbeCapacityDataset(context.Background(), cfg)
	if !errors.Is(err, errCapacityDatasetProbe) {
		t.Fatalf("duplicate node error = %v", err)
	}
}

func TestCapacityDatasetProbeJoinsEveryNodeAfterCancellation(t *testing.T) {
	started := make(chan struct{}, coordinatorWorkerCount)
	returned := make(chan struct{}, coordinatorWorkerCount)
	targets := make([]cancelCapacityDatasetTarget, coordinatorWorkerCount)
	for index := range targets {
		targets[index] = cancelCapacityDatasetTarget{started: started, returned: returned}
	}
	cfg := capacityDatasetTestConfig(t)
	probe := NewCapacityDatasetProbe(CapacityDatasetProbeOptions{
		BenchToken: "dataset-probe-secret",
		TargetFactory: func(index int, _ EndpointDeclaration, _ string) CapacityDatasetProbeTarget {
			return targets[index]
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := probe.ProbeCapacityDataset(ctx, cfg)
		result <- err
	}()
	for range coordinatorWorkerCount {
		<-started
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("probe error = %v, want context canceled", err)
	}
	if len(returned) != coordinatorWorkerCount {
		t.Fatalf("returned probes = %d, want %d", len(returned), coordinatorWorkerCount)
	}
}

func TestCapacityDatasetProbeDoesNotExposeTargetErrors(t *testing.T) {
	const secret = "raw-target-secret"
	generatedAt := time.Date(2030, time.March, 17, 17, 46, 40, 0, time.UTC)
	nodes := []fakeCapacityDatasetTarget{
		{configErr: errors.New("remote response contained " + secret)},
		{config: target.DebugConfig{NodeID: 2, NodeDataDir: "/srv/wukongim/node-2"}, summary: target.DebugGoroutineSummary{GeneratedAt: generatedAt, ProcessStartedAt: generatedAt.Add(-73 * time.Hour), BootID: "process-2"}},
		{config: target.DebugConfig{NodeID: 3, NodeDataDir: "/srv/wukongim/node-3"}, summary: target.DebugGoroutineSummary{GeneratedAt: generatedAt, ProcessStartedAt: generatedAt.Add(-73 * time.Hour), BootID: "process-3"}},
	}
	cfg := capacityDatasetTestConfig(t)
	probe := newCapacityDatasetFakeProbe(nodes, generatedAt.Add(time.Second))
	_, err := probe.ProbeCapacityDataset(context.Background(), cfg)
	if !errors.Is(err, errCapacityDatasetProbe) || strings.Contains(err.Error(), secret) {
		t.Fatalf("probe error = %q", err)
	}
}

func TestCapacityDatasetProbeReadsSoakDigestWithoutAgeAdmission(t *testing.T) {
	generatedAt := time.Date(2030, time.March, 17, 17, 46, 40, 0, time.UTC)
	targets := []fakeCapacityDatasetTarget{
		{config: target.DebugConfig{NodeID: 1, NodeDataDir: "/srv/wukongim/node-1"}, summary: target.DebugGoroutineSummary{GeneratedAt: generatedAt, ProcessStartedAt: generatedAt.Add(-time.Hour), BootID: "process-1"}},
		{config: target.DebugConfig{NodeID: 2, NodeDataDir: "/srv/wukongim/node-2"}, summary: target.DebugGoroutineSummary{GeneratedAt: generatedAt, ProcessStartedAt: generatedAt.Add(-time.Hour), BootID: "process-2"}},
		{config: target.DebugConfig{NodeID: 3, NodeDataDir: "/srv/wukongim/node-3"}, summary: target.DebugGoroutineSummary{GeneratedAt: generatedAt, ProcessStartedAt: generatedAt.Add(-time.Hour), BootID: "process-3"}},
	}
	probe := newCapacityDatasetFakeProbe(targets, generatedAt.Add(time.Second))
	digest, err := probe.ProbeDatasetDigest(context.Background(), FormalConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !validReportHash(digest) {
		t.Fatalf("dataset digest = %q", digest)
	}
}

type fakeCapacityDatasetTarget struct {
	config     target.DebugConfig
	summary    target.DebugGoroutineSummary
	configErr  error
	summaryErr error
}

func (f fakeCapacityDatasetTarget) DebugConfig(context.Context) (target.DebugConfig, error) {
	return f.config, f.configErr
}

func (f fakeCapacityDatasetTarget) DebugGoroutineSummary(context.Context) (target.DebugGoroutineSummary, error) {
	return f.summary, f.summaryErr
}

func probeCapacityDatasetWithTargets(t *testing.T, targets []fakeCapacityDatasetTarget, observedAt time.Time) CapacityLiveDatasetEvidence {
	t.Helper()
	cfg := capacityDatasetTestConfig(t)
	probe := newCapacityDatasetFakeProbe(targets, observedAt)
	evidence, err := probe.ProbeCapacityDataset(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func newCapacityDatasetFakeProbe(targets []fakeCapacityDatasetTarget, observedAt time.Time) *CapacityDatasetProbe {
	return NewCapacityDatasetProbe(CapacityDatasetProbeOptions{
		BenchToken: "dataset-probe-secret",
		Clock:      capacityDatasetClockFunc(func() time.Time { return observedAt }),
		TargetFactory: func(index int, _ EndpointDeclaration, _ string) CapacityDatasetProbeTarget {
			return targets[index]
		},
	})
}

func capacityDatasetTestConfig(t *testing.T) Config {
	t.Helper()
	cfg := FormalConfig()
	cfg.RunID = "capacity-dataset-probe-test"
	cfg.Mode = ModeCapacity
	cfg.Capacity.AgedCheckpoint = AgedCheckpoint{
		Reference: "reports/formal-72h", Completed: true, Passed: true, Duration: 72 * time.Hour,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

type cancelCapacityDatasetTarget struct {
	started  chan<- struct{}
	returned chan<- struct{}
}

func (t cancelCapacityDatasetTarget) DebugConfig(ctx context.Context) (target.DebugConfig, error) {
	t.started <- struct{}{}
	<-ctx.Done()
	t.returned <- struct{}{}
	return target.DebugConfig{}, ctx.Err()
}

func (cancelCapacityDatasetTarget) DebugGoroutineSummary(context.Context) (target.DebugGoroutineSummary, error) {
	panic("summary must not be called after canceled config")
}
