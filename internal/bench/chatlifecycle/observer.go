package chatlifecycle

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

const observerMaxRoundTimeout = 5 * time.Second

// ObserverOutcome is the closed terminal class for continuous target observation.
type ObserverOutcome string

const (
	ObserverStopped        ObserverOutcome = "stopped"
	ObserverProductFailure ObserverOutcome = "product_failure"
	ObserverHarnessInvalid ObserverOutcome = "harness_invalid"
)

// ObserverCode is a bounded, non-secret reason for observer termination.
type ObserverCode string

const (
	ObserverCodeStopped         ObserverCode = "stopped"
	ObserverCodeTopology        ObserverCode = "topology"
	ObserverCodeServiceHealth   ObserverCode = "service_health"
	ObserverCodeClusterHealth   ObserverCode = "cluster_health"
	ObserverCodeLeaderImbalance ObserverCode = "leader_imbalance"
	ObserverCodeEvidence        ObserverCode = "evidence"
)

// ObserverResult contains no raw endpoint response, identity, or credential value.
type ObserverResult struct {
	Outcome ObserverOutcome `json:"outcome"`
	Code    ObserverCode    `json:"code"`
}

// ObserverSample is one complete, same-round, three-node cluster projection.
// Nodes are deep copies and may be retained by a read-only consumer.
type ObserverSample struct {
	At                    time.Time
	Nodes                 [coordinatorWorkerCount]target.DebugCluster
	ServiceHealthy        bool
	ClusterHealthy        bool
	LeaderImbalanced      bool
	LogicalSlotGroups     uint64
	LeaderGroups          uint64
	FullReplicaGroups     uint64
	HotReplicaLagBreaches uint64
}

// ObserverSampleSink consumes validated observer rounds without owning polling.
type ObserverSampleSink interface {
	Observe(context.Context, ObserverSample) error
}

// ObserverSampleSinkFunc adapts a function to ObserverSampleSink.
type ObserverSampleSinkFunc func(context.Context, ObserverSample) error

func (f ObserverSampleSinkFunc) Observe(ctx context.Context, sample ObserverSample) error {
	return f(ctx, sample)
}

// ClusterHealthTarget is the node-local service and Slot observation boundary.
type ClusterHealthTarget interface {
	Healthz(context.Context) error
	Readyz(context.Context) error
	DebugCluster(context.Context) (target.DebugCluster, error)
}

type clusterHealthTarget = ClusterHealthTarget

// ObserverClock makes all continuous-failure windows deterministic in unit tests.
type ObserverClock interface {
	Now() time.Time
	NewTicker(time.Duration) ObserverTicker
}

// ObserverTicker is the minimal ticker lifetime used by Observer.
type ObserverTicker interface {
	C() <-chan time.Time
	Stop()
}

// ObserverOptions supplies authenticated observation and bounded hot Slot declarations.
type ObserverOptions struct {
	BenchToken string
	HTTPClient *http.Client
	Clock      ObserverClock
	// RoundContext bounds all service-node requests in one observation round.
	// Production caps it at five seconds independently of a longer cadence.
	RoundContext func(context.Context, time.Duration) (context.Context, context.CancelFunc)
	// HotSlotGroups identifies the bounded logical Slot groups whose leader progress
	// is health-critical. Empty means every configured logical Slot group is hot.
	HotSlotGroups []uint32
	TargetFactory func(int, EndpointDeclaration, string) ClusterHealthTarget
	// SampleSink receives only complete three-node rounds after cluster merging.
	SampleSink ObserverSampleSink
}

// Observer polls all declared service nodes and owns only continuous-window state.
type Observer struct {
	options ObserverOptions
}

// NewObserver constructs an observer. Static configuration remains validated by Run.
func NewObserver(options ObserverOptions) *Observer {
	if options.HTTPClient == nil {
		options.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if options.Clock == nil {
		options.Clock = realObserverClock{}
	}
	if options.RoundContext == nil {
		options.RoundContext = context.WithTimeout
	}
	if options.TargetFactory == nil {
		options.TargetFactory = func(_ int, endpoint EndpointDeclaration, token string) ClusterHealthTarget {
			return target.NewClient(target.Config{
				APIAddrs: []string{endpoint.Address}, Token: token, HTTPClient: options.HTTPClient,
			})
		}
	}
	options.HotSlotGroups = append([]uint32(nil), options.HotSlotGroups...)
	return &Observer{options: options}
}

type observerWindows struct {
	serviceUnhealthySince    *time.Time
	clusterInconsistentSince *time.Time
	hotReplicaLagSince       map[uint32]time.Time
	leaderImbalancedSince    *time.Time
}

// Run performs an immediate poll and then polls at the configured cadence.
func (o *Observer) Run(ctx context.Context, cfg Config) ObserverResult {
	if o == nil || cfg.Validate() != nil || strings.TrimSpace(o.options.BenchToken) == "" ||
		!validHotSlotDeclaration(o.options.HotSlotGroups, cfg.Workload.Topology.LogicalSlotGroups) {
		return ObserverResult{Outcome: ObserverHarnessInvalid, Code: ObserverCodeTopology}
	}
	targets := make([]clusterHealthTarget, len(cfg.Observation.ServiceNodes))
	for index, endpoint := range cfg.Observation.ServiceNodes {
		targets[index] = o.options.TargetFactory(index, endpoint, o.options.BenchToken)
		if targets[index] == nil {
			return ObserverResult{Outcome: ObserverHarnessInvalid, Code: ObserverCodeTopology}
		}
	}

	ticker := o.options.Clock.NewTicker(cfg.Observation.Cadence)
	defer ticker.Stop()
	windows := observerWindows{hotReplicaLagSince: make(map[uint32]time.Time, cfg.Workload.Topology.LogicalSlotGroups)}
	if result, terminal := o.poll(ctx, cfg, targets, &windows); terminal {
		return result
	}
	for {
		select {
		case <-ctx.Done():
			return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}
		case <-ticker.C():
			if result, terminal := o.poll(ctx, cfg, targets, &windows); terminal {
				return result
			}
		}
	}
}

func (o *Observer) poll(
	ctx context.Context,
	cfg Config,
	targets []clusterHealthTarget,
	windows *observerWindows,
) (ObserverResult, bool) {
	roundCtx, cancel := o.options.RoundContext(ctx, min(cfg.Observation.Cadence, observerMaxRoundTimeout))
	if roundCtx == nil || cancel == nil {
		return ObserverResult{Outcome: ObserverHarnessInvalid, Code: ObserverCodeTopology}, true
	}
	defer cancel()
	type nodeResult struct {
		serviceHealthy bool
		snapshot       target.DebugCluster
		hasSnapshot    bool
	}
	const serviceNodeCount = 3
	if len(targets) != serviceNodeCount {
		return ObserverResult{Outcome: ObserverHarnessInvalid, Code: ObserverCodeTopology}, true
	}
	results := make([]nodeResult, serviceNodeCount)
	var joined sync.WaitGroup
	joined.Add(serviceNodeCount)
	for index := 0; index < serviceNodeCount; index++ {
		go func() {
			defer joined.Done()
			observed := targets[index]
			result := nodeResult{serviceHealthy: true}
			if observed.Healthz(roundCtx) != nil {
				result.serviceHealthy = false
			}
			if observed.Readyz(roundCtx) != nil {
				result.serviceHealthy = false
			}
			snapshot, err := observed.DebugCluster(roundCtx)
			if err == nil {
				result.snapshot = snapshot
				result.hasSnapshot = true
			}
			results[index] = result
		}()
	}
	joined.Wait()
	now := o.options.Clock.Now()

	serviceHealthy := true
	snapshots := make([]target.DebugCluster, 0, len(targets))
	for _, result := range results {
		if !result.serviceHealthy {
			serviceHealthy = false
		}
		if result.hasSnapshot {
			snapshots = append(snapshots, result.snapshot)
		}
	}
	cluster, err := mergeClusterObservations(snapshots, cfg)
	clusterHealthy := err == nil
	var hotReplicaLagBreaches uint64
	if clusterHealthy {
		hotHealthy, valid := hotSlotProgressHealthy(cluster, o.options.HotSlotGroups)
		if !valid {
			return ObserverResult{Outcome: ObserverHarnessInvalid, Code: ObserverCodeTopology}, true
		}
		clusterHealthy = hotHealthy
		hotReplicaLagBreaches = observerHotReplicaLagBreaches(cluster, o.options.HotSlotGroups)
	}
	imbalanced := err == nil && leaderImbalanced(cluster.leaderCounts, len(cluster.slots), cfg.Thresholds.Cluster.LeaderImbalancePercent)
	if err == nil && o.options.SampleSink != nil {
		var nodes [coordinatorWorkerCount]target.DebugCluster
		for index := range results {
			nodes[index] = cloneObserverDebugCluster(results[index].snapshot)
		}
		sample := ObserverSample{
			At: now, Nodes: nodes, ServiceHealthy: serviceHealthy, ClusterHealthy: clusterHealthy,
			LeaderImbalanced: imbalanced, LogicalSlotGroups: uint64(len(cluster.slots)),
			LeaderGroups: uint64(len(cluster.slots)), FullReplicaGroups: uint64(len(cluster.slots)),
			HotReplicaLagBreaches: hotReplicaLagBreaches,
		}
		if sinkErr := o.options.SampleSink.Observe(roundCtx, sample); sinkErr != nil {
			if ctx.Err() != nil && errors.Is(sinkErr, ctx.Err()) {
				return ObserverResult{Outcome: ObserverStopped, Code: ObserverCodeStopped}, true
			}
			return ObserverResult{Outcome: ObserverHarnessInvalid, Code: ObserverCodeEvidence}, true
		}
	}
	if updateContinuousWindow(&windows.serviceUnhealthySince, !serviceHealthy, now, cfg.Thresholds.Cluster.UnhealthyFailAfter) {
		return ObserverResult{Outcome: ObserverProductFailure, Code: ObserverCodeServiceHealth}, true
	}
	if err != nil {
		clear(windows.hotReplicaLagSince)
		if updateContinuousWindow(&windows.clusterInconsistentSince, true, now, cfg.Thresholds.Cluster.UnhealthyFailAfter) {
			return ObserverResult{Outcome: ObserverProductFailure, Code: ObserverCodeClusterHealth}, true
		}
	} else {
		updateContinuousWindow(&windows.clusterInconsistentSince, false, now, cfg.Thresholds.Cluster.UnhealthyFailAfter)
		if updateHotReplicaLagWindows(
			windows.hotReplicaLagSince,
			cluster,
			o.options.HotSlotGroups,
			now,
			cfg.Thresholds.Cluster.UnhealthyFailAfter,
		) {
			return ObserverResult{Outcome: ObserverProductFailure, Code: ObserverCodeClusterHealth}, true
		}
	}

	if updateContinuousWindow(&windows.leaderImbalancedSince, imbalanced, now, cfg.Thresholds.Cluster.LeaderImbalanceFor) {
		return ObserverResult{Outcome: ObserverProductFailure, Code: ObserverCodeLeaderImbalance}, true
	}
	return ObserverResult{}, false
}

func updateHotReplicaLagWindows(
	windows map[uint32]time.Time,
	observation mergedClusterObservation,
	declared []uint32,
	now time.Time,
	threshold time.Duration,
) bool {
	selected := make(map[uint32]struct{}, len(declared))
	for _, slotID := range declared {
		selected[slotID] = struct{}{}
	}
	for _, slot := range observation.slots {
		if len(selected) > 0 {
			if _, ok := selected[slot.slotID]; !ok {
				continue
			}
		}
		if slot.progressHealthy {
			delete(windows, slot.slotID)
			continue
		}
		started, exists := windows[slot.slotID]
		if !exists {
			windows[slot.slotID] = now
			if threshold <= 0 {
				return true
			}
			continue
		}
		if now.Sub(started) >= threshold {
			return true
		}
	}
	return false
}

func observerHotReplicaLagBreaches(observation mergedClusterObservation, declared []uint32) uint64 {
	selected := make(map[uint32]struct{}, len(declared))
	for _, slotID := range declared {
		selected[slotID] = struct{}{}
	}
	var breaches uint64
	for _, slot := range observation.slots {
		if len(selected) > 0 {
			if _, ok := selected[slot.slotID]; !ok {
				continue
			}
		}
		if !slot.progressHealthy {
			breaches++
		}
	}
	return breaches
}

func cloneObserverDebugCluster(snapshot target.DebugCluster) target.DebugCluster {
	clone := snapshot
	clone.Slots = append([]target.ClusterSlot(nil), snapshot.Slots...)
	for index := range clone.Slots {
		clone.Slots[index].Replicas = append([]uint64(nil), snapshot.Slots[index].Replicas...)
		clone.Slots[index].Voters = append([]uint64(nil), snapshot.Slots[index].Voters...)
		clone.Slots[index].ReplicaProgress = append([]target.ReplicaProgress(nil), snapshot.Slots[index].ReplicaProgress...)
	}
	return clone
}

func validHotSlotDeclaration(slotIDs []uint32, maximum int) bool {
	if len(slotIDs) > maximum {
		return false
	}
	seen := make(map[uint32]struct{}, len(slotIDs))
	for _, slotID := range slotIDs {
		if _, duplicate := seen[slotID]; duplicate {
			return false
		}
		seen[slotID] = struct{}{}
	}
	return true
}

func updateContinuousWindow(since **time.Time, unhealthy bool, now time.Time, threshold time.Duration) bool {
	if !unhealthy {
		*since = nil
		return false
	}
	if *since == nil {
		started := now
		*since = &started
		return threshold <= 0
	}
	return now.Sub(**since) >= threshold
}

type realObserverClock struct{}

func (realObserverClock) Now() time.Time { return time.Now() }
func (realObserverClock) NewTicker(period time.Duration) ObserverTicker {
	return realObserverTicker{ticker: time.NewTicker(period)}
}

type realObserverTicker struct{ ticker *time.Ticker }

func (t realObserverTicker) C() <-chan time.Time { return t.ticker.C }
func (t realObserverTicker) Stop()               { t.ticker.Stop() }
