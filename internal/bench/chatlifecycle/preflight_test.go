package chatlifecycle

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
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

func TestPreflightClassifiesInvalidHarnessBeforeTraffic(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*preflightFixture)
		code   PreflightCode
	}{
		{"wrong slot count", func(f *preflightFixture) { f.targets[0].config.InitialSlotCount = 11 }, PreflightCodeTargetConfig},
		{"wrong hash slot count", func(f *preflightFixture) { f.targets[0].config.HashSlotCount = 255 }, PreflightCodeTargetConfig},
		{"replica mismatch", func(f *preflightFixture) { f.targets[0].config.SlotReplicaCount = 2 }, PreflightCodeTargetConfig},
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
	fixture.disks[0].filesystem.SizeBytes = 999_999_999_999
	fixture.disks[0].filesystem.AvailableBytes = 999_999_999_999

	result := fixture.preflight.Check(context.Background(), fixture.cfg)
	if result.Outcome != PreflightInfrastructureFailure || result.Code != PreflightCodeDiskCapacity || result.TrafficAllowed() {
		t.Fatalf("result = %+v, want infrastructure_failure/disk_capacity", result)
	}
	if fixture.safeStops != 0 {
		t.Fatalf("safe stops = %d, want 0", fixture.safeStops)
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
				SlotReplicaCount: 3, ChannelReplicaCount: 3, MaxChannels: 50_000,
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
	fixture.disks = make([]*fakeDiskReader, len(cfg.Observation.HostMetrics))
	for index := range fixture.disks {
		size := cfg.Thresholds.MinimumDataFilesystemBytes
		fixture.disks[index] = &fakeDiskReader{filesystem: DataFilesystem{SizeBytes: size, AvailableBytes: size}}
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
func (f *fakePreflightTarget) CheckMetrics(context.Context) error { return f.observeErr }
func (f *fakePreflightTarget) ForceGC(context.Context) error      { return f.observeErr }
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
