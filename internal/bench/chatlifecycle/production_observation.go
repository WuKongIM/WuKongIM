package chatlifecycle

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

var errProductionObservation = errors.New("chat lifecycle production observation failed")

// ProductionObservationTarget is the protected service-node resource boundary.
type ProductionObservationTarget interface {
	ForceGC(context.Context) error
	Metrics(context.Context) (target.MetricsSnapshot, error)
}

// ProductionObservationOptions binds one source to the reviewed three-node topology.
type ProductionObservationOptions struct {
	Config     Config
	BenchToken string
	HTTPClient *http.Client

	TargetFactory func(int, EndpointDeclaration, string) ProductionObservationTarget
	DiskFactory   func(int, EndpointDeclaration) DiskReader
}

// ProductionObservationSnapshot is the latest complete round plus bounded
// cumulative report evidence. Metrics and slices are returned as deep copies.
type ProductionObservationSnapshot struct {
	// Sequence advances once for each atomically committed Observer round.
	Sequence uint64
	// At is the source-owned verdict timestamp. Forced-GC rounds use the exact
	// run-start-aligned hour even when collection completes within bounded slack.
	At        time.Time
	Resources []NodeResourceSample
	// Metrics contains the exact same-round service snapshots used by Resources.
	Metrics [coordinatorWorkerCount]target.MetricsSnapshot
	// ActivationRejections is the exact monotonic sum across those three snapshots.
	ActivationRejections uint64
	Signals              []VerdictSignal
	ResourceEvidence     ReportResourceEvidence
	ClusterEvidence      ReportClusterEvidence
}

// ProductionObservationSource enriches validated Observer samples with one
// same-round service-metrics and host-filesystem collection.
type ProductionObservationSource struct {
	cfg     Config
	targets [coordinatorWorkerCount]ProductionObservationTarget
	disks   [coordinatorWorkerCount]DiskReader

	// observeMu prevents overlapping external rounds; mu protects snapshots and
	// the monotonic baselines read concurrently by Snapshot.
	observeMu sync.Mutex
	mu        sync.RWMutex
	begun     bool
	start     time.Time
	nextGC    time.Time
	lastAt    time.Time
	nodeIDs   [coordinatorWorkerCount]uint64
	lastRej   [coordinatorWorkerCount]uint64
	rejSeen   bool
	snapshot  ProductionObservationSnapshot
}

var _ ObserverSampleSink = (*ProductionObservationSource)(nil)

// NewProductionObservationSource constructs production adapters without network I/O.
func NewProductionObservationSource(options ProductionObservationOptions) (*ProductionObservationSource, error) {
	if options.Config.Validate() != nil || strings.TrimSpace(options.BenchToken) == "" ||
		len(options.Config.Observation.ServiceNodes) != coordinatorWorkerCount ||
		len(options.Config.Observation.HostMetrics) != coordinatorWorkerCount {
		return nil, errProductionObservation
	}
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if options.TargetFactory == nil {
		options.TargetFactory = func(_ int, endpoint EndpointDeclaration, token string) ProductionObservationTarget {
			client := target.NewClient(target.Config{
				APIAddrs: []string{endpoint.Address}, Token: token, HTTPClient: options.HTTPClient,
			})
			return productionObservationTargetClient{client: client}
		}
	}
	if options.DiskFactory == nil {
		options.DiskFactory = func(_ int, endpoint EndpointDeclaration) DiskReader {
			return newNodeExporterDiskReader(endpoint, options.HTTPClient)
		}
	}
	cfg := options.Config
	cfg.Observation.ServiceNodes = append([]EndpointDeclaration(nil), options.Config.Observation.ServiceNodes...)
	cfg.Observation.HostMetrics = append([]EndpointDeclaration(nil), options.Config.Observation.HostMetrics...)
	source := &ProductionObservationSource{cfg: cfg}
	for index := 0; index < coordinatorWorkerCount; index++ {
		source.targets[index] = options.TargetFactory(index, cfg.Observation.ServiceNodes[index], options.BenchToken)
		source.disks[index] = options.DiskFactory(index, cfg.Observation.HostMetrics[index])
		if source.targets[index] == nil || source.disks[index] == nil {
			return nil, errProductionObservation
		}
	}
	return source, nil
}

// Begin starts the one non-resumable measured generation and aligns forced-GC samples.
func (s *ProductionObservationSource) Begin(start time.Time) error {
	if s == nil || start.IsZero() {
		return errProductionObservation
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.begun {
		return errProductionObservation
	}
	s.begun = true
	s.start = start
	s.nextGC = start
	s.lastAt = time.Time{}
	s.nodeIDs = [coordinatorWorkerCount]uint64{}
	s.lastRej = [coordinatorWorkerCount]uint64{}
	s.rejSeen = false
	s.snapshot = ProductionObservationSnapshot{}
	return nil
}

type productionObservationRound struct {
	metrics    [coordinatorWorkerCount]target.MetricsSnapshot
	metricErrs [coordinatorWorkerCount]error
	disks      [coordinatorWorkerCount]DataFilesystem
	diskErrs   [coordinatorWorkerCount]error
}

// Observe implements ObserverSampleSink and joins every attempted external read.
func (s *ProductionObservationSource) Observe(ctx context.Context, sample ObserverSample) error {
	if s == nil || ctx == nil {
		return errProductionObservation
	}
	s.observeMu.Lock()
	defer s.observeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.RLock()
	begun, start, nextGC, lastAt := s.begun, s.start, s.nextGC, s.lastAt
	s.mu.RUnlock()
	if !begun {
		return nil
	}
	if !validProductionObserverSample(sample, s.cfg) || sample.At.Before(start) ||
		(!lastAt.IsZero() && !sample.At.After(lastAt)) {
		return errProductionObservation
	}
	forcedGC := !sample.At.Before(nextGC)
	observationAt := sample.At
	if forcedGC {
		alignmentTolerance := s.cfg.Observation.Cadence + min(s.cfg.Observation.Cadence, observerMaxRoundTimeout)
		if sample.At.Sub(nextGC) > alignmentTolerance || !sample.At.Before(nextGC.Add(time.Hour)) {
			return errProductionObservation
		}
		observationAt = nextGC
	}

	var round productionObservationRound
	var joined sync.WaitGroup
	joined.Add(coordinatorWorkerCount * 2)
	for index := 0; index < coordinatorWorkerCount; index++ {
		index := index
		go func() {
			defer joined.Done()
			if forcedGC {
				if err := s.targets[index].ForceGC(ctx); err != nil {
					round.metricErrs[index] = err
					return
				}
			}
			round.metrics[index], round.metricErrs[index] = s.targets[index].Metrics(ctx)
		}()
		go func() {
			defer joined.Done()
			round.disks[index], round.diskErrs[index] = s.disks[index].Filesystem(ctx)
		}()
	}
	joined.Wait()
	if err := productionObservationRoundError(ctx, round); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.commitRound(sample, observationAt, forcedGC, round)
}

func (s *ProductionObservationSource) commitRound(
	sample ObserverSample,
	observationAt time.Time,
	forcedGC bool,
	round productionObservationRound,
) error {
	var resources [coordinatorWorkerCount]NodeResourceSample
	var rejected [coordinatorWorkerCount]uint64
	for index := 0; index < coordinatorWorkerCount; index++ {
		metrics := round.metrics[index]
		queue, queueOK := addProductionGauges(metrics.RuntimeQueueDepth, metrics.ChannelWorkerQueueDepth)
		inflight, inflightOK := productionGauge(metrics.RuntimeInflight)
		activation, activationOK := productionGauge(metrics.ActivationRejectedTotal)
		heap, heapOK := productionGauge(metrics.GoHeapAllocBytes)
		goroutines, goroutinesOK := productionGauge(metrics.GoGoroutines)
		filesystem := round.disks[index]
		if !queueOK || !inflightOK || !activationOK || !heapOK || !goroutinesOK ||
			filesystem.SizeBytes < s.cfg.Thresholds.MinimumDataFilesystemBytes || filesystem.SizeBytes <= 0 ||
			filesystem.AvailableBytes < 0 || filesystem.AvailableBytes > filesystem.SizeBytes {
			return errProductionObservation
		}
		rejected[index] = activation
		resources[index] = NodeResourceSample{
			NodeID: sample.Nodes[index].NodeID, ForcedGC: forcedGC,
			QueueDepth: float64(queue), Inflight: float64(inflight),
		}
		if forcedGC {
			resources[index].HeapBytes = float64(heap)
			resources[index].Goroutines = float64(goroutines)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.begun || s.start.IsZero() || (!s.lastAt.IsZero() && !sample.At.After(s.lastAt)) {
		return errProductionObservation
	}
	next := cloneProductionObservationSnapshot(s.snapshot)
	if next.Sequence == math.MaxUint64 {
		return errProductionObservation
	}
	next.Sequence++
	next.At = observationAt
	next.Resources = append(next.Resources[:0], resources[:]...)
	next.Metrics = cloneProductionMetrics(round.metrics)
	var activationTotal uint64
	for index := 0; index < coordinatorWorkerCount; index++ {
		nodeID := sample.Nodes[index].NodeID
		if s.nodeIDs[index] != 0 && s.nodeIDs[index] != nodeID || s.rejSeen && rejected[index] < s.lastRej[index] ||
			math.MaxUint64-activationTotal < rejected[index] {
			return errProductionObservation
		}
		activationTotal += rejected[index]
	}
	next.ActivationRejections = activationTotal

	for index := 0; index < coordinatorWorkerCount; index++ {
		resource := resources[index]
		filesystem := round.disks[index]
		node := &next.ResourceEvidence.Nodes[index]
		node.DataFilesystemBytes = uint64(filesystem.SizeBytes)
		node.DataFilesystemAvailableBytes = uint64(filesystem.AvailableBytes)
		node.QueueCurrent = uint64(resource.QueueDepth)
		node.InflightCurrent = uint64(resource.Inflight)
		if !sample.At.After(s.start.Add(s.cfg.Thresholds.Timeline.Warmup)) {
			node.QueueBaseline = max(node.QueueBaseline, node.QueueCurrent)
			node.InflightBaseline = max(node.InflightBaseline, node.InflightCurrent)
		}
		if forcedGC {
			if node.ForcedGCSamples == math.MaxUint64 {
				return errProductionObservation
			}
			node.ForcedGCSamples++
			heap, goroutines := uint64(resource.HeapBytes), uint64(resource.Goroutines)
			if node.ForcedGCSamples == 1 {
				node.HeapStartBytes, node.GoroutineStart = heap, goroutines
			}
			node.HeapEndBytes, node.GoroutineEnd = heap, goroutines
		}
	}
	cluster := &next.ClusterEvidence
	if cluster.HealthySamples == math.MaxUint64 || cluster.UnhealthySamples == math.MaxUint64 ||
		math.MaxUint64-cluster.HotReplicaLagBreaches < sample.HotReplicaLagBreaches ||
		sample.LeaderImbalanced && cluster.LeaderImbalanceWarnings == math.MaxUint64 {
		return errProductionObservation
	}
	if sample.ServiceHealthy && sample.ClusterHealthy {
		cluster.HealthySamples++
	} else {
		cluster.UnhealthySamples++
	}
	cluster.LogicalSlotGroups = sample.LogicalSlotGroups
	cluster.LeaderGroups = sample.LeaderGroups
	cluster.FullReplicaGroups = sample.FullReplicaGroups
	cluster.HotReplicaLagBreaches += sample.HotReplicaLagBreaches
	if sample.LeaderImbalanced {
		cluster.LeaderImbalanceWarnings++
	}
	for _, filesystem := range round.disks {
		if diskFreeBelow(filesystem, s.cfg.Thresholds.DiskSafeStopFreePercent) {
			next.Signals = []VerdictSignal{{Outcome: VerdictInfrastructureFailure, Cause: VerdictCauseDiskExhausted}}
			break
		}
	}
	s.lastAt = sample.At
	if forcedGC {
		s.nextGC = s.nextGC.Add(time.Hour)
	}
	for index := 0; index < coordinatorWorkerCount; index++ {
		s.nodeIDs[index] = sample.Nodes[index].NodeID
		s.lastRej[index] = rejected[index]
	}
	s.rejSeen = true
	s.snapshot = next
	return nil
}

// Snapshot returns a concurrent-safe deep copy of the latest committed evidence.
func (s *ProductionObservationSource) Snapshot() ProductionObservationSnapshot {
	if s == nil {
		return ProductionObservationSnapshot{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneProductionObservationSnapshot(s.snapshot)
}

type productionObservationTargetClient struct{ client *target.Client }

func (c productionObservationTargetClient) ForceGC(ctx context.Context) error {
	return c.client.ForceGC(ctx)
}

func (c productionObservationTargetClient) Metrics(ctx context.Context) (target.MetricsSnapshot, error) {
	metrics, err := c.client.Metrics(ctx)
	if err != nil {
		return target.MetricsSnapshot{}, err
	}
	if err := metrics.ValidateRequired(); err != nil {
		return target.MetricsSnapshot{}, err
	}
	return metrics, nil
}

func validProductionObserverSample(sample ObserverSample, cfg Config) bool {
	if sample.At.IsZero() || sample.LogicalSlotGroups != uint64(cfg.Workload.Topology.LogicalSlotGroups) ||
		sample.LeaderGroups != sample.LogicalSlotGroups || sample.FullReplicaGroups != sample.LogicalSlotGroups ||
		sample.HotReplicaLagBreaches > sample.LogicalSlotGroups || sample.ClusterHealthy && sample.HotReplicaLagBreaches != 0 {
		return false
	}
	snapshots := make([]target.DebugCluster, coordinatorWorkerCount)
	for index := range sample.Nodes {
		snapshots[index] = sample.Nodes[index]
	}
	_, err := mergeClusterObservations(snapshots, cfg)
	return err == nil
}

func productionObservationRoundError(ctx context.Context, round productionObservationRound) error {
	var causal bool
	for _, roundErrs := range [2][coordinatorWorkerCount]error{round.metricErrs, round.diskErrs} {
		for _, err := range roundErrs {
			if err == nil {
				continue
			}
			if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
				causal = true
				continue
			}
			return errProductionObservation
		}
	}
	if causal {
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func productionGauge(value float64) (uint64, bool) {
	if !validPrometheusGauge(value) {
		return 0, false
	}
	return uint64(value), true
}

func addProductionGauges(left, right float64) (uint64, bool) {
	leftValue, leftOK := productionGauge(left)
	rightValue, rightOK := productionGauge(right)
	if !leftOK || !rightOK || math.MaxUint64-leftValue < rightValue {
		return 0, false
	}
	return leftValue + rightValue, true
}

func cloneProductionMetrics(source [coordinatorWorkerCount]target.MetricsSnapshot) [coordinatorWorkerCount]target.MetricsSnapshot {
	clone := source
	for index := range clone {
		clone[index].MetaCreatedTotal = cloneFloatMap(source[index].MetaCreatedTotal)
	}
	return clone
}

func cloneProductionObservationSnapshot(source ProductionObservationSnapshot) ProductionObservationSnapshot {
	clone := source
	clone.Resources = append([]NodeResourceSample(nil), source.Resources...)
	clone.Metrics = cloneProductionMetrics(source.Metrics)
	clone.Signals = append([]VerdictSignal(nil), source.Signals...)
	return clone
}

func cloneFloatMap(source map[string]float64) map[string]float64 {
	if source == nil {
		return nil
	}
	clone := make(map[string]float64, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
