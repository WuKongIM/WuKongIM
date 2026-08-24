package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	productmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

func TestPreflightValidFormalAndLocalGateTraffic(t *testing.T) {
	for _, cfg := range []Config{FormalConfig(), LocalConfig()} {
		fixture := newPreflightFixture(cfg)
		result := fixture.preflight.Check(context.Background(), cfg)
		if !result.Passed() || !result.TrafficAllowed() || result.Outcome != PreflightPass {
			t.Fatalf("profile %s result = %+v, want pass/traffic allowed", cfg.Profile, result)
		}
		if fixture.safeStops != 0 {
			t.Fatalf("profile %s safe stops = %d, want 0", cfg.Profile, fixture.safeStops)
		}
	}
}

func TestWorkerPreflightRejectsProtocolV1BeforeCoordinatorMutation(t *testing.T) {
	serverForVersion := func(version uint64) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/healthz":
				_ = json.NewEncoder(response).Encode(WorkerHealth{OK: true, Phase: WorkerPhaseUnassigned})
			case "/v1/info":
				_ = json.NewEncoder(response).Encode(WorkerInfo{
					ProtocolVersion: version, MaxRequestBytes: workerMaxRequestBytes, MaxResponseBytes: workerMaxResponseBytes,
				})
			default:
				response.WriteHeader(http.StatusNotFound)
			}
		}))
	}
	for _, testCase := range []struct {
		version uint64
		wantErr bool
	}{{version: 1, wantErr: true}, {version: workerProtocolVersion, wantErr: false}} {
		server := serverForVersion(testCase.version)
		client, err := NewWorkerClient(WorkerClientConfig{
			BaseURL: server.URL, ControlToken: "control-secret", HTTPClient: server.Client(),
		})
		if err != nil {
			server.Close()
			t.Fatalf("NewWorkerClient(version=%d): %v", testCase.version, err)
		}
		err = (workerPreflightClient{client: client}).Check(context.Background())
		server.Close()
		if (err != nil) != testCase.wantErr {
			t.Fatalf("protocol version %d error = %v, wantErr=%v", testCase.version, err, testCase.wantErr)
		}
	}
}

func TestPreflightPassesFreshZeroEventProductMetrics(t *testing.T) {
	registry := productmetrics.New(1, "node-1")
	registry.RuntimePressure.SetQueueDepth("channel", "meta", "worker", "none", 0)
	registry.RuntimePressure.SetQueueCapacity("channel", "meta", "worker", "none", 64)
	registry.RuntimePressure.SetPoolInflight("channel", "meta", 0)
	registry.ChannelRuntime.SetWorkerQueueDepth("meta", 0)
	registry.ChannelRuntime.SetWorkerQueueCapacity("meta", 64)
	productHandler := registry.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		plainRequest := r.Clone(r.Context())
		plainRequest.Header = r.Header.Clone()
		plainRequest.Header.Del("Accept-Encoding")
		productHandler.ServeHTTP(recorder, plainRequest)
		scrape := recorder.Body.String()
		_, _ = w.Write([]byte(scrape))
		// The Go process collector does not expose RSS on every development OS.
		// Production Linux does; keep this target-client test portable without
		// teaching the parser to accept a missing required family.
		if !strings.Contains(scrape, "\nprocess_resident_memory_bytes ") {
			_, _ = fmt.Fprintln(w, "process_resident_memory_bytes 0")
		}
	}))
	defer server.Close()
	metricsClient := target.NewClient(target.Config{APIAddrs: []string{server.URL}})
	snapshot, err := metricsClient.Metrics(context.Background())
	if err != nil {
		t.Fatalf("Metrics() error = %v", err)
	}
	if err := snapshot.ValidateRequired(); err != nil {
		t.Fatalf("fresh Metrics() snapshot = %+v: %v", snapshot, err)
	}

	fixture := newPreflightFixture(LocalConfig())
	for _, observed := range fixture.targets {
		observed.metricsClient = metricsClient
	}
	result := fixture.preflight.Check(context.Background(), fixture.cfg)
	if !result.Passed() || !result.TrafficAllowed() {
		t.Fatalf("result = %+v, want fresh zero-event product metrics to pass", result)
	}
}

func TestPreflightClassifiesInvalidHarnessBeforeTraffic(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*preflightFixture)
		code   PreflightCode
	}{
		{"wrong slot count", func(f *preflightFixture) { f.targets[0].config.InitialSlotCount = 11 }, PreflightCodeTargetConfig},
		{"wrong hash slot count", func(f *preflightFixture) { f.targets[0].config.HashSlotCount = 255 }, PreflightCodeTargetConfig},
		{"replica mismatch", func(f *preflightFixture) { f.targets[0].config.SlotReplicaCount = 2 }, PreflightCodeTargetConfig},
		{"max channels above exact config", func(f *preflightFixture) { f.targets[0].config.MaxChannels = 50_001 }, PreflightCodeTargetConfig},
		{"missing bench capability", func(f *preflightFixture) { f.targets[0].capabilities.Supports.ChannelRuntimeProbe = false }, PreflightCodeBenchCapability},
		{"unreachable worker", func(f *preflightFixture) { f.workers[1].err = errors.New("unreachable") }, PreflightCodeWorkerUnavailable},
		{"unauthorized debug", func(f *preflightFixture) {
			f.targets[0].configErr = errors.New("GET /debug/config returned status 401")
		}, PreflightCodeUnauthorized},
		{"unauthorized bench", func(f *preflightFixture) {
			f.targets[0].capabilitiesErr = errors.New("GET /bench/v1/capabilities returned status 401")
		}, PreflightCodeUnauthorized},
		{"disk ambiguity", func(f *preflightFixture) { f.disks[0].err = ErrDiskAmbiguous }, PreflightCodeDiskAmbiguous},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newPreflightFixture(FormalConfig())
			tt.mutate(fixture)
			result := fixture.preflight.Check(context.Background(), fixture.cfg)
			if result.Outcome != PreflightHarnessInvalid || result.Code != tt.code || result.TrafficAllowed() {
				t.Fatalf("result = %+v, want harness_invalid/%s/traffic denied", result, tt.code)
			}
			if fixture.safeStops != 0 {
				t.Fatalf("safe stops = %d, want 0 for harness invalid", fixture.safeStops)
			}
		})
	}
}

func TestPreflightDiskLowSignalsCoordinatedSafeStop(t *testing.T) {
	fixture := newPreflightFixture(FormalConfig())
	fixture.disks[2].filesystem.AvailableBytes = 49_999_999_999
	fixture.disks[2].filesystem.SizeBytes = 1_000_000_000_000

	result := fixture.preflight.Check(context.Background(), fixture.cfg)
	if result.Outcome != PreflightInfrastructureFailure || result.Code != PreflightCodeDiskFree || result.TrafficAllowed() {
		t.Fatalf("result = %+v, want infrastructure_failure/disk_free", result)
	}
	if fixture.safeStops != 1 {
		t.Fatalf("safe stops = %d, want 1", fixture.safeStops)
	}
}

func TestPreflightDiskCapacityFailsWithoutStopSignal(t *testing.T) {
	fixture := newPreflightFixture(FormalConfig())
	fixture.disks[0].filesystem.SizeBytes = 499_999_999_999
	fixture.disks[0].filesystem.AvailableBytes = 499_999_999_999

	result := fixture.preflight.Check(context.Background(), fixture.cfg)
	if result.Outcome != PreflightInfrastructureFailure || result.Code != PreflightCodeDiskCapacity || result.TrafficAllowed() {
		t.Fatalf("result = %+v, want infrastructure_failure/disk_capacity", result)
	}
	if fixture.safeStops != 0 {
		t.Fatalf("safe stops = %d, want 0", fixture.safeStops)
	}
}

func TestLocalPreflightAcceptsBoundedDevelopmentHostCapacity(t *testing.T) {
	fixture := newPreflightFixture(LocalConfig())
	const sizeBytes = int64(151_263_856 * 1024)
	const availableBytes = int64(88_001_148 * 1024)
	for _, disk := range fixture.disks {
		disk.filesystem.SizeBytes = sizeBytes
		disk.filesystem.AvailableBytes = availableBytes
		disk.filesystem.SystemSizeBytes = sizeBytes
		disk.filesystem.SystemAvailableBytes = availableBytes
	}

	result := fixture.preflight.Check(context.Background(), fixture.cfg)
	if !result.Passed() || !result.TrafficAllowed() {
		t.Fatalf("result = %+v, want bounded local host capacity to pass", result)
	}
}

func TestPreflightChecksEveryDeclaredAPIEndpoint(t *testing.T) {
	fixture := newPreflightFixture(FormalConfig())
	result := fixture.preflight.Check(context.Background(), fixture.cfg)
	if !result.Passed() {
		t.Fatalf("result = %+v, want pass", result)
	}
	serviceCount := len(fixture.cfg.Observation.ServiceNodes)
	if len(fixture.targetEndpoints) != serviceCount+len(fixture.cfg.Observation.APIAddrs) {
		t.Fatalf("target endpoints = %d, want all service and API endpoints", len(fixture.targetEndpoints))
	}
	for index, address := range fixture.cfg.Observation.APIAddrs {
		if got := fixture.targetEndpoints[serviceCount+index].Address; got != address {
			t.Fatalf("API endpoint %d = %q, want %q", index, got, address)
		}
	}
}

func TestPreflightAllowsHealthyInitialLeaderImbalanceForObserverWindow(t *testing.T) {
	fixture := newPreflightFixture(FormalConfig())
	for _, observed := range fixture.targets {
		observed.cluster.Slots[2].LeaderID = 1
		observed.cluster.Slots[2].ReplicaProgress = nil
		if observed.cluster.NodeID == 1 {
			observed.cluster.Slots[2].ReplicaProgress = []target.ReplicaProgress{
				{NodeID: 1, MatchIndex: 100, State: "StateReplicate"},
				{NodeID: 2, MatchIndex: 100, State: "StateReplicate"},
				{NodeID: 3, MatchIndex: 100, State: "StateReplicate"},
			}
		}
	}

	result := fixture.preflight.Check(context.Background(), fixture.cfg)
	if !result.Passed() || !result.TrafficAllowed() {
		t.Fatalf("result = %+v, want pass so observer owns the continuous imbalance window", result)
	}
}

func TestPreflightRejectsCopiedAPIGatewayTopologyWithoutIO(t *testing.T) {
	fixture := newPreflightFixture(LocalConfig())
	for index, api := range fixture.cfg.Observation.APIAddrs {
		fixture.cfg.Observation.GatewayTCPAddrs[index] = api[len("http://"):]
	}
	result := fixture.preflight.Check(context.Background(), fixture.cfg)
	if result.Outcome != PreflightHarnessInvalid || result.Code != PreflightCodeTopology || result.TrafficAllowed() {
		t.Fatalf("result = %+v, want topology harness_invalid", result)
	}
	if fixture.targetCalls != 0 || fixture.workerCalls != 0 || fixture.diskCalls != 0 {
		t.Fatalf("I/O calls target/worker/disk = %d/%d/%d, want zero", fixture.targetCalls, fixture.workerCalls, fixture.diskCalls)
	}
}

type preflightFixture struct {
	cfg             Config
	preflight       *Preflight
	targets         []*fakePreflightTarget
	workers         []*fakePreflightWorker
	disks           []*fakeDiskReader
	targetCalls     int
	workerCalls     int
	diskCalls       int
	safeStops       int
	targetEndpoints []EndpointDeclaration
}

func newPreflightFixture(cfg Config) *preflightFixture {
	fixture := &preflightFixture{cfg: cfg}
	fixture.targets = make([]*fakePreflightTarget, len(cfg.Observation.ServiceNodes))
	for index := range fixture.targets {
		fixture.targets[index] = &fakePreflightTarget{
			config: target.DebugConfig{
				NodeID: uint64(index + 1), InitialSlotCount: 12, HashSlotCount: 256,
				SlotReplicaCount: 3, ChannelReplicaCount: 3, MaxChannels: cfg.Workload.MaxChannelsPerNode,
			},
			cluster:      healthyPreflightCluster(uint64(index + 1)),
			capabilities: requiredPreflightCapabilities(),
		}
	}
	fixture.workers = make([]*fakePreflightWorker, len(cfg.Observation.Workers))
	workerTokens := make(map[string]string, len(fixture.workers))
	for index := range fixture.workers {
		fixture.workers[index] = &fakePreflightWorker{}
		workerTokens[cfg.Observation.Workers[index].Name] = fmt.Sprintf("worker-token-%d", index+1)
	}
	fixture.disks = make([]*fakeDiskReader, productionHostCount)
	for index := range fixture.disks {
		size := cfg.Thresholds.MinimumDataFilesystemBytes
		if index == coordinatorWorkerCount {
			size = cfg.Thresholds.Resource.MinimumLoadFilesystemBytes
		}
		fixture.disks[index] = &fakeDiskReader{filesystem: DataFilesystem{
			SizeBytes: size, AvailableBytes: size, SystemSizeBytes: 40_000_000_000,
			SystemAvailableBytes: 20_000_000_000, HostResourcesObserved: true,
			WatchedDirectoryObserved: index == coordinatorWorkerCount,
			NetworkTransmitObserved:  index == coordinatorWorkerCount,
		}}
		prepareProductionProcessEvidence(&fixture.disks[index].filesystem, index, cfg.Stage)
	}
	fixture.preflight = NewPreflight(PreflightOptions{
		BenchToken:   "bench-token",
		WorkerTokens: workerTokens,
		TargetFactory: func(index int, endpoint EndpointDeclaration, _ string) preflightTarget {
			fixture.targetEndpoints = append(fixture.targetEndpoints, endpoint)
			fixture.targetCalls++
			return fixture.targets[index]
		},
		WorkerFactory: func(index int, _ EndpointDeclaration, _ string) preflightWorker {
			fixture.workerCalls++
			return fixture.workers[index]
		},
		DiskFactory: func(index int, _ EndpointDeclaration) diskReader {
			fixture.diskCalls++
			return fixture.disks[index]
		},
		GatewayChecker: GatewayCheckerFunc(func(context.Context, []string) error { return nil }),
		SafeStop: CoordinatedStopFunc(func(context.Context, PreflightResult) error {
			fixture.safeStops++
			return nil
		}),
	})
	return fixture
}

func prepareProductionProcessEvidence(filesystem *DataFilesystem, host int, stage Stage) {
	filesystem.ProcessResourcesObserved = true
	required := []int{1, 11, 12}
	if host < coordinatorWorkerCount {
		required = append(required, 0)
	} else {
		required = append(required, 2, 3, 4, 8, 9, 10)
		if stage == StageRehearsal {
			required = append(required, 7)
		} else {
			required = append(required, 6)
		}
	}
	for _, process := range required {
		filesystem.ProcessUp[process] = true
		filesystem.ProcessCPUJiffies[process] = uint64(process + 1)
		filesystem.ProcessResidentMemoryBytes[process] = uint64(process+1) * 1_000_000
	}
}

func requiredPreflightCapabilities() model.BenchCapabilities {
	return model.BenchCapabilities{
		Enabled: true, Version: "bench/v1",
		Supports: model.BenchCapabilitiesSupports{
			UsersTokensBatch: true, ChannelsBatch: true, ChannelSubscribersBatch: true,
			Snapshot: true, PresenceSnapshot: true, ChannelRuntimeSnapshot: true,
			ChannelRuntimeProbe: true, ChannelTypes: []string{"person", "group"},
		},
	}
}

func healthyPreflightCluster(nodeID uint64) target.DebugCluster {
	snapshot := target.DebugCluster{NodeID: nodeID, StateRevision: 1, Slots: make([]target.ClusterSlot, 12)}
	for index := range snapshot.Slots {
		leader := uint64(index%3 + 1)
		slot := target.ClusterSlot{
			SlotID: uint32(index + 1), LeaderID: leader, Replicas: []uint64{1, 2, 3},
			Voters: []uint64{1, 2, 3}, Term: 1, CommitIndex: 100, AppliedIndex: 100,
		}
		if leader == nodeID {
			slot.ReplicaProgress = []target.ReplicaProgress{
				{NodeID: 1, MatchIndex: 100, State: "StateReplicate"},
				{NodeID: 2, MatchIndex: 100, State: "StateReplicate"},
				{NodeID: 3, MatchIndex: 100, State: "StateReplicate"},
			}
		}
		snapshot.Slots[index] = slot
	}
	return snapshot
}

type fakePreflightTarget struct {
	config          target.DebugConfig
	configErr       error
	cluster         target.DebugCluster
	clusterErr      error
	capabilities    model.BenchCapabilities
	capabilitiesErr error
	metricsClient   *target.Client
	observeErr      error
}

func (f *fakePreflightTarget) Healthz(context.Context) error { return f.observeErr }
func (f *fakePreflightTarget) Readyz(context.Context) error  { return f.observeErr }
func (f *fakePreflightTarget) DebugConfig(context.Context) (target.DebugConfig, error) {
	return f.config, f.configErr
}
func (f *fakePreflightTarget) DebugCluster(context.Context) (target.DebugCluster, error) {
	return f.cluster, f.clusterErr
}
func (f *fakePreflightTarget) CheckMetrics(ctx context.Context) error {
	if f.observeErr != nil || f.metricsClient == nil {
		return f.observeErr
	}
	snapshot, err := f.metricsClient.Metrics(ctx)
	if err != nil {
		return err
	}
	return snapshot.ValidateRequired()
}
func (f *fakePreflightTarget) ForceGC(context.Context) error { return f.observeErr }
func (f *fakePreflightTarget) Capabilities(context.Context) (model.BenchCapabilities, error) {
	return f.capabilities, f.capabilitiesErr
}

type fakePreflightWorker struct{ err error }

func (f *fakePreflightWorker) Check(context.Context) error { return f.err }

type fakeDiskReader struct {
	filesystem DataFilesystem
	err        error
}

func (f *fakeDiskReader) Filesystem(context.Context) (DataFilesystem, error) {
	return f.filesystem, f.err
}
