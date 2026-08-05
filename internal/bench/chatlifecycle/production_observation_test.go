package chatlifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

func TestProductionObservationCollectsOneCompleteForcedGCRound(t *testing.T) {
	cfg := LocalConfig()
	start := time.Date(2030, time.March, 17, 17, 0, 0, 0, time.UTC)
	targets := make([]*fakeProductionObservationTarget, coordinatorWorkerCount)
	disks := make([]*fakeProductionObservationDisk, coordinatorWorkerCount)
	for index := 0; index < coordinatorWorkerCount; index++ {
		targets[index] = &fakeProductionObservationTarget{metrics: target.MetricsSnapshot{
			GoGoroutines: 10 + float64(index), GoHeapAllocBytes: 100 + float64(index),
			RuntimeQueueDepth: 5, ChannelWorkerQueueDepth: 7, RuntimeInflight: 3,
			ActivationRejectedTotal: float64(index + 1),
			MetaCreatedTotal:        map[string]float64{"created": float64(index + 1)},
		}}
		disks[index] = &fakeProductionObservationDisk{filesystem: DataFilesystem{
			SizeBytes: 10_000_000_000, AvailableBytes: 9_000_000_000 - int64(index),
		}}
	}
	source, err := NewProductionObservationSource(ProductionObservationOptions{
		Config: cfg, BenchToken: "bench-token",
		TargetFactory: func(index int, _ EndpointDeclaration, token string) ProductionObservationTarget {
			if token != "bench-token" {
				t.Fatalf("target %d token = %q", index, token)
			}
			return targets[index]
		},
		DiskFactory: func(index int, _ EndpointDeclaration) DiskReader { return disks[index] },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Begin(start); err != nil {
		t.Fatal(err)
	}
	if err := source.Observe(context.Background(), healthyProductionObserverSample(start)); err != nil {
		t.Fatal(err)
	}

	snapshot := source.Snapshot()
	if snapshot.Sequence != 1 || snapshot.At != start || snapshot.ActivationRejections != 6 || len(snapshot.Resources) != coordinatorWorkerCount {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	for index, resource := range snapshot.Resources {
		if resource.NodeID != uint64(index+1) || !resource.ForcedGC || resource.HeapBytes != 100+float64(index) ||
			resource.Goroutines != 10+float64(index) || resource.QueueDepth != 12 || resource.Inflight != 3 {
			t.Fatalf("resource %d = %+v", index, resource)
		}
		if targets[index].forced != 1 || targets[index].scraped != 1 || disks[index].reads != 1 {
			t.Fatalf("node %d calls force/metrics/disk = %d/%d/%d", index, targets[index].forced, targets[index].scraped, disks[index].reads)
		}
		node := snapshot.ResourceEvidence.Nodes[index]
		if node.DataFilesystemBytes != 10_000_000_000 || node.DataFilesystemAvailableBytes != uint64(9_000_000_000-int64(index)) ||
			node.ForcedGCSamples != 1 || node.HeapStartBytes != uint64(100+index) || node.HeapEndBytes != uint64(100+index) ||
			node.GoroutineStart != uint64(10+index) || node.GoroutineEnd != uint64(10+index) ||
			node.QueueBaseline != 12 || node.QueueCurrent != 12 || node.InflightBaseline != 3 || node.InflightCurrent != 3 {
			t.Fatalf("resource evidence %d = %+v", index, node)
		}
	}
	if snapshot.ClusterEvidence != (ReportClusterEvidence{
		HealthySamples: 1, LogicalSlotGroups: 12, LeaderGroups: 12, FullReplicaGroups: 12,
	}) {
		t.Fatalf("cluster evidence = %+v", snapshot.ClusterEvidence)
	}
	if len(snapshot.Signals) != 0 {
		t.Fatalf("signals = %+v", snapshot.Signals)
	}
	snapshot.Metrics[0].MetaCreatedTotal["created"] = 99
	if got := source.Snapshot().Metrics[0].MetaCreatedTotal["created"]; got != 1 {
		t.Fatalf("snapshot metrics map retained caller mutation: %v", got)
	}
}

func TestProductionObservationIgnoresPreBeginAndRejectsActivationRegressionAtomically(t *testing.T) {
	cfg := LocalConfig()
	start := time.Date(2030, time.March, 17, 17, 0, 0, 0, time.UTC)
	targets := make([]*fakeProductionObservationTarget, coordinatorWorkerCount)
	disks := make([]*fakeProductionObservationDisk, coordinatorWorkerCount)
	for index := range targets {
		targets[index] = &fakeProductionObservationTarget{metrics: target.MetricsSnapshot{
			GoGoroutines: 10, GoHeapAllocBytes: 100, RuntimeQueueDepth: 5,
			ChannelWorkerQueueDepth: 7, RuntimeInflight: 3, ActivationRejectedTotal: float64(index + 1),
		}}
		disks[index] = &fakeProductionObservationDisk{filesystem: DataFilesystem{
			SizeBytes: 10_000_000_000, AvailableBytes: 9_000_000_000,
		}}
	}
	source, err := NewProductionObservationSource(ProductionObservationOptions{
		Config: cfg, BenchToken: "bench-token",
		TargetFactory: func(index int, _ EndpointDeclaration, _ string) ProductionObservationTarget { return targets[index] },
		DiskFactory:   func(index int, _ EndpointDeclaration) DiskReader { return disks[index] },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Observe(context.Background(), healthyProductionObserverSample(start)); err != nil {
		t.Fatalf("pre-Begin Observe = %v", err)
	}
	if snapshot := source.Snapshot(); snapshot.Sequence != 0 || len(snapshot.Resources) != 0 {
		t.Fatalf("pre-Begin snapshot = %+v", snapshot)
	}
	for index := range targets {
		if targets[index].forced != 0 || targets[index].scraped != 0 || disks[index].reads != 0 {
			t.Fatalf("pre-Begin node %d performed I/O", index)
		}
	}

	if err := source.Begin(start); err != nil {
		t.Fatal(err)
	}
	if err := source.Observe(context.Background(), healthyProductionObserverSample(start)); err != nil {
		t.Fatal(err)
	}
	targets[0].metrics.ActivationRejectedTotal = 2
	targets[1].metrics.ActivationRejectedTotal = 1
	targets[2].metrics.ActivationRejectedTotal = 4
	if err := source.Observe(context.Background(), healthyProductionObserverSample(start.Add(cfg.Observation.Cadence))); !errors.Is(err, errProductionObservation) {
		t.Fatalf("regressing activation error = %v", err)
	}
	for index := range targets {
		targets[index].metrics.ActivationRejectedTotal = float64(index + 1)
	}
	if err := source.Observe(context.Background(), healthyProductionObserverSample(start.Add(2*cfg.Observation.Cadence))); err != nil {
		t.Fatalf("valid round after rejected regression = %v", err)
	}
	snapshot := source.Snapshot()
	if snapshot.Sequence != 2 || snapshot.ActivationRejections != 6 || snapshot.At != start.Add(2*cfg.Observation.Cadence) ||
		len(snapshot.Resources) != coordinatorWorkerCount {
		t.Fatalf("snapshot after rejected round = %+v", snapshot)
	}
	for _, resource := range snapshot.Resources {
		if resource.ForcedGC || resource.HeapBytes != 0 || resource.Goroutines != 0 {
			t.Fatalf("non-hour resource = %+v", resource)
		}
	}
	if snapshot.ClusterEvidence.HealthySamples != 2 {
		t.Fatalf("healthy samples = %d, want only committed rounds", snapshot.ClusterEvidence.HealthySamples)
	}
}

func TestProductionObservationSignalsLowDiskAndKeepsHourlyEvidenceAligned(t *testing.T) {
	cfg := LocalConfig()
	start := time.Date(2030, time.March, 17, 17, 3, 7, 0, time.UTC)
	targets, disks := validProductionObservationFakes(cfg)
	disks[2].filesystem.AvailableBytes = 499_999_999
	source := newProductionObservationFakeSource(t, cfg, targets, disks)
	if err := source.Begin(start); err != nil {
		t.Fatal(err)
	}
	firstAt := start.Add(cfg.Observation.Cadence)
	if err := source.Observe(context.Background(), healthyProductionObserverSample(firstAt)); err != nil {
		t.Fatal(err)
	}
	snapshot := source.Snapshot()
	if snapshot.At != start || snapshot.Sequence != 1 || len(snapshot.Signals) != 1 ||
		snapshot.Signals[0] != (VerdictSignal{Outcome: VerdictInfrastructureFailure, Cause: VerdictCauseDiskExhausted}) {
		t.Fatalf("first low-disk snapshot = %+v", snapshot)
	}

	disks[2].filesystem.AvailableBytes = 9_000_000_000
	secondAt := start.Add(time.Hour + cfg.Observation.Cadence)
	if err := source.Observe(context.Background(), healthyProductionObserverSample(secondAt)); err != nil {
		t.Fatal(err)
	}
	snapshot = source.Snapshot()
	if snapshot.At != start.Add(time.Hour) || snapshot.Sequence != 2 || len(snapshot.Signals) != 1 {
		t.Fatalf("second hourly snapshot = %+v", snapshot)
	}
	for index, resource := range snapshot.Resources {
		if !resource.ForcedGC || targets[index].forced != 2 || snapshot.ResourceEvidence.Nodes[index].ForcedGCSamples != 2 {
			t.Fatalf("node %d hourly resource/evidence = %+v / %+v", index, resource, snapshot.ResourceEvidence.Nodes[index])
		}
	}
}

func TestProductionObservationAcceptsObserverPhaseAndRoundLatencyAtHourlyBoundary(t *testing.T) {
	cfg := LocalConfig()
	start := time.Date(2030, time.March, 17, 17, 0, 0, 0, time.UTC)
	targets, disks := validProductionObservationFakes(cfg)
	source := newProductionObservationFakeSource(t, cfg, targets, disks)
	if err := source.Begin(start); err != nil {
		t.Fatal(err)
	}
	latestValid := start.Add(cfg.Observation.Cadence + observerMaxRoundTimeout)
	if err := source.Observe(context.Background(), healthyProductionObserverSample(latestValid)); err != nil {
		t.Fatalf("phase plus round-latency sample = %v", err)
	}
	if snapshot := source.Snapshot(); snapshot.At != start || snapshot.Sequence != 1 || !snapshot.Resources[0].ForcedGC {
		t.Fatalf("aligned delayed snapshot = %+v", snapshot)
	}
}

func TestProductionObservationCancellationJoinsAllExternalReads(t *testing.T) {
	cfg := LocalConfig()
	start := time.Date(2030, time.March, 17, 17, 0, 0, 0, time.UTC)
	started := make(chan struct{}, coordinatorWorkerCount*2)
	returned := make(chan struct{}, coordinatorWorkerCount*2)
	source, err := NewProductionObservationSource(ProductionObservationOptions{
		Config: cfg, BenchToken: "bench-token",
		TargetFactory: func(int, EndpointDeclaration, string) ProductionObservationTarget {
			return blockingProductionObservationTarget{started: started, returned: returned}
		},
		DiskFactory: func(int, EndpointDeclaration) DiskReader {
			return blockingProductionObservationDisk{started: started, returned: returned}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Begin(start); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- source.Observe(ctx, healthyProductionObserverSample(start)) }()
	for index := 0; index < coordinatorWorkerCount*2; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("external reads did not all start")
		}
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled observation error = %v", err)
	}
	if len(returned) != coordinatorWorkerCount*2 {
		t.Fatalf("joined reads = %d, want %d", len(returned), coordinatorWorkerCount*2)
	}
	if snapshot := source.Snapshot(); snapshot.Sequence != 0 {
		t.Fatalf("canceled observation committed snapshot = %+v", snapshot)
	}
}

func TestProductionObservationPreCanceledContextPerformsNoIO(t *testing.T) {
	cfg := LocalConfig()
	start := time.Date(2030, time.March, 17, 17, 0, 0, 0, time.UTC)
	targets, disks := validProductionObservationFakes(cfg)
	source := newProductionObservationFakeSource(t, cfg, targets, disks)
	if err := source.Begin(start); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := source.Observe(ctx, healthyProductionObserverSample(start)); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled observation error = %v", err)
	}
	for index := range targets {
		if targets[index].forced != 0 || targets[index].scraped != 0 || disks[index].reads != 0 {
			t.Fatalf("pre-canceled node %d performed I/O", index)
		}
	}
	if snapshot := source.Snapshot(); snapshot.Sequence != 0 {
		t.Fatalf("pre-canceled observation committed = %+v", snapshot)
	}
}

func TestProductionObservationDoesNotExposeTargetErrors(t *testing.T) {
	const secret = "production-observation-secret"
	cfg := LocalConfig()
	start := time.Date(2030, time.March, 17, 17, 0, 0, 0, time.UTC)
	targets, disks := validProductionObservationFakes(cfg)
	targets[0].err = errors.New("remote response leaked " + secret)
	source := newProductionObservationFakeSource(t, cfg, targets, disks)
	if err := source.Begin(start); err != nil {
		t.Fatal(err)
	}
	err := source.Observe(context.Background(), healthyProductionObserverSample(start))
	if !errors.Is(err, errProductionObservation) || strings.Contains(err.Error(), secret) {
		t.Fatalf("observation error = %q", err)
	}
}

func healthyProductionObserverSample(at time.Time) ObserverSample {
	var nodes [coordinatorWorkerCount]target.DebugCluster
	for index := range nodes {
		nodes[index] = healthyPreflightCluster(uint64(index + 1))
	}
	return ObserverSample{
		At: at, Nodes: nodes, ServiceHealthy: true, ClusterHealthy: true,
		LogicalSlotGroups: 12, LeaderGroups: 12, FullReplicaGroups: 12,
	}
}

type fakeProductionObservationTarget struct {
	metrics target.MetricsSnapshot
	err     error
	forced  int
	scraped int
}

func (f *fakeProductionObservationTarget) ForceGC(context.Context) error {
	f.forced++
	return f.err
}

func (f *fakeProductionObservationTarget) Metrics(context.Context) (target.MetricsSnapshot, error) {
	f.scraped++
	return f.metrics, f.err
}

type fakeProductionObservationDisk struct {
	filesystem DataFilesystem
	err        error
	reads      int
}

func (f *fakeProductionObservationDisk) Filesystem(context.Context) (DataFilesystem, error) {
	f.reads++
	return f.filesystem, f.err
}

func validProductionObservationFakes(cfg Config) ([]*fakeProductionObservationTarget, []*fakeProductionObservationDisk) {
	targets := make([]*fakeProductionObservationTarget, coordinatorWorkerCount)
	disks := make([]*fakeProductionObservationDisk, coordinatorWorkerCount)
	for index := range targets {
		targets[index] = &fakeProductionObservationTarget{metrics: target.MetricsSnapshot{
			GoGoroutines: 10 + float64(index), GoHeapAllocBytes: 100 + float64(index),
			RuntimeQueueDepth: 5, ChannelWorkerQueueDepth: 7, RuntimeInflight: 3,
			ActivationRejectedTotal: float64(index + 1),
		}}
		disks[index] = &fakeProductionObservationDisk{filesystem: DataFilesystem{
			SizeBytes:      cfg.Thresholds.MinimumDataFilesystemBytes,
			AvailableBytes: cfg.Thresholds.MinimumDataFilesystemBytes * 9 / 10,
		}}
	}
	return targets, disks
}

func newProductionObservationFakeSource(
	t *testing.T,
	cfg Config,
	targets []*fakeProductionObservationTarget,
	disks []*fakeProductionObservationDisk,
) *ProductionObservationSource {
	t.Helper()
	source, err := NewProductionObservationSource(ProductionObservationOptions{
		Config: cfg, BenchToken: "bench-token",
		TargetFactory: func(index int, _ EndpointDeclaration, _ string) ProductionObservationTarget { return targets[index] },
		DiskFactory:   func(index int, _ EndpointDeclaration) DiskReader { return disks[index] },
	})
	if err != nil {
		t.Fatal(err)
	}
	return source
}

type blockingProductionObservationTarget struct {
	started  chan<- struct{}
	returned chan<- struct{}
}

func (t blockingProductionObservationTarget) ForceGC(ctx context.Context) error {
	t.started <- struct{}{}
	<-ctx.Done()
	t.returned <- struct{}{}
	return ctx.Err()
}

func (blockingProductionObservationTarget) Metrics(context.Context) (target.MetricsSnapshot, error) {
	panic("metrics must not run after canceled forced GC")
}

type blockingProductionObservationDisk struct {
	started  chan<- struct{}
	returned chan<- struct{}
}

func (d blockingProductionObservationDisk) Filesystem(ctx context.Context) (DataFilesystem, error) {
	d.started <- struct{}{}
	<-ctx.Done()
	d.returned <- struct{}{}
	return DataFilesystem{}, ctx.Err()
}
