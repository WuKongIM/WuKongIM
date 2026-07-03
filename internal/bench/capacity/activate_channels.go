package capacity

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/coordinator"
	"github.com/WuKongIM/WuKongIM/internal/bench/report"
	targetapi "github.com/WuKongIM/WuKongIM/internal/bench/target"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

const (
	activateChannelsProfileName = "activate-groups"
	activateChannelsTrafficName = "activate-send"
)

// ActivateChannelsConfig controls the fixed 10k-channel activation scenario.
type ActivateChannelsConfig struct {
	// APIAddrs are HTTP API base addresses for already-running target nodes.
	APIAddrs []string
	// GatewayTCPAddrs optionally overrides discovered WKProto TCP gateway addresses.
	GatewayTCPAddrs []string
	// BenchToken is an optional bearer token for bench API routes.
	BenchToken string
	// RunID is the stable benchmark run identifier.
	RunID string
	// Channels is the number of group channels to activate.
	Channels int
	// Users is the number of online users prepared for the activation run.
	Users int
	// GroupMembers is the number of members per generated group channel.
	GroupMembers int
	// PrepareRatePerSecond limits bench API data preparation operations per second.
	PrepareRatePerSecond float64
	// ConnectRatePerSecond limits gateway connect attempts per second.
	ConnectRatePerSecond float64
	// ActivationConcurrency is the maximum in-flight send operations during activation.
	ActivationConcurrency int
	// ActivationWindow is the active send window used to schedule one send per channel.
	ActivationWindow time.Duration
	// Hold is the optional post-activation observation duration.
	Hold time.Duration
	// HoldProbeInterval is the interval between post-activation stability probes.
	HoldProbeInterval time.Duration
	// ProbeBatchSize is the number of channels sampled per post-activation probe.
	ProbeBatchSize int
	// StableP99 is the maximum desired sendack p99 latency.
	StableP99 time.Duration
	// MaxSendackErrorRate is the maximum allowed sendack error rate.
	MaxSendackErrorRate float64
	// MaxConnectErrorRate is the maximum allowed gateway connect error rate.
	MaxConnectErrorRate float64
	// EvictAfter controls whether activated channel runtime state is evicted after probing.
	EvictAfter bool
	// ReportDir is the directory where activation reports should be written.
	ReportDir string
}

// ActivateChannelsEvaluation is the final verdict for one activation run.
type ActivateChannelsEvaluation struct {
	// Passed is true when every activation-specific success condition passed.
	Passed bool `json:"passed"`
	// FailureReasons lists stable machine-readable failed checks.
	FailureReasons []string `json:"failure_reasons,omitempty"`
	// ActivationSuccess is the successful run-phase sendack count.
	ActivationSuccess uint64 `json:"activation_success"`
	// ActivationErrors is the failed run-phase send/sendack count.
	ActivationErrors uint64 `json:"activation_errors"`
	// ActivationBacklog is the number of channels that did not reach a terminal send result.
	ActivationBacklog uint64 `json:"activation_backlog"`
	// SendackP50 is the maximum worker-local run-phase sendack p50 latency.
	SendackP50 time.Duration `json:"sendack_p50"`
	// SendackP95 is the maximum worker-local run-phase sendack p95 latency.
	SendackP95 time.Duration `json:"sendack_p95"`
	// SendackP99 is the maximum worker-local run-phase sendack p99 latency.
	SendackP99 time.Duration `json:"sendack_p99"`
	// ActiveLeaderTotal is the total active leader runtime count across target nodes.
	ActiveLeaderTotal int `json:"active_leader_total"`
	// ActiveLeaderNodeCount is the number of target nodes with at least one active leader runtime.
	ActiveLeaderNodeCount int `json:"active_leader_node_count"`
	// ActiveLeaderMaxNodeID is the node id with the largest active leader runtime count.
	ActiveLeaderMaxNodeID uint64 `json:"active_leader_max_node_id,omitempty"`
	// ActiveLeaderMaxNodeShare is the largest single-node share of active leader runtimes.
	ActiveLeaderMaxNodeShare float64 `json:"active_leader_max_node_share"`
	// ActiveNodes contains per-node active runtime distribution captured after activation.
	ActiveNodes []ActivateChannelsNodeRuntime `json:"active_nodes,omitempty"`
	// ActivationRejectedDelta is the increase in rejected activation requests during the run.
	ActivationRejectedDelta uint64 `json:"activation_rejected_delta"`
	// ProbeMissingAllNodes lists probed channels absent from every responding target node.
	ProbeMissingAllNodes []string `json:"probe_missing_all_nodes,omitempty"`
}

// ActivateChannelsNodeRuntime summarizes one node's active ChannelV2 runtime distribution.
type ActivateChannelsNodeRuntime struct {
	// NodeID identifies the target node that produced the runtime snapshot.
	NodeID uint64 `json:"node_id"`
	// ActiveTotal is the node-local active runtime count.
	ActiveTotal int `json:"active_total"`
	// ActiveLeader is the node-local active leader runtime count.
	ActiveLeader int `json:"active_leader"`
	// ActiveFollower is the node-local active follower runtime count.
	ActiveFollower int `json:"active_follower"`
	// FollowerParked is the node-local parked follower runtime count.
	FollowerParked int `json:"follower_parked"`
}

// ActivateChannelsRunner drives a fixed-size live-channel activation experiment.
type ActivateChannelsRunner struct {
	cfg        ActivateChannelsConfig
	discovered DiscoveredTarget
	base       *Runner
	target     activateTargetClient
	run        func(context.Context, model.Scenario, []model.Worker, model.Target) (coordinator.RunResult, error)
	now        func() time.Time
}

type activateTargetClient interface {
	Capabilities(context.Context) (model.BenchCapabilities, error)
	ChannelRuntimeSnapshots(context.Context, model.ChannelRuntimeQuery) ([]model.ChannelRuntimeSnapshot, error)
	ProbeChannelRuntimeAll(context.Context, model.ChannelRuntimeProbeRequest) ([]model.ChannelRuntimeProbeResult, error)
	EvictChannelRuntimeAll(context.Context, model.ChannelRuntimeEvictRequest) ([]model.ChannelRuntimeEvictResult, error)
}

type activateTargetCapabilitiesAll interface {
	CapabilitiesAll(context.Context) (map[string]model.BenchCapabilities, error)
}

type activateRunnerTarget struct {
	cfg    targetapi.Config
	client *targetapi.Client
}

// NewActivateChannelsRunner creates a runner using one temporary local worker by default.
func NewActivateChannelsRunner(cfg ActivateChannelsConfig, discovered DiscoveredTarget) *ActivateChannelsRunner {
	r := &ActivateChannelsRunner{
		cfg:        cfg,
		discovered: discovered,
		base:       &Runner{discovered: discovered},
		target:     newActivateRunnerTarget(cfg),
		now:        time.Now,
	}
	r.run = func(ctx context.Context, scenario model.Scenario, workers []model.Worker, target model.Target) (coordinator.RunResult, error) {
		return coordinator.New(coordinator.CoordinatorConfig{Workers: workers, Target: target}).Run(ctx, scenario)
	}
	return r
}

// Run executes the activation scenario and gathers runtime evidence.
func (r *ActivateChannelsRunner) Run(ctx context.Context) (ActivateChannelsResult, error) {
	if r == nil {
		return ActivateChannelsResult{Status: StatusFailed}, fmt.Errorf("activate channels runner is required")
	}
	r.setDefaults()
	result := ActivateChannelsResult{
		Status:    StatusFailed,
		Config:    activateChannelsReportConfigFromConfig(r.cfg),
		ReportDir: r.cfg.ReportDir,
	}
	if err := r.cfg.Validate(); err != nil {
		return result, err
	}
	if err := r.validateCapabilities(ctx); err != nil {
		result.Evaluation.FailureReasons = []string{"capabilities_missing"}
		return result, err
	}
	startedWorker, err := r.ensureWorker()
	if err != nil {
		return result, err
	}
	if startedWorker {
		defer r.base.close()
	}

	fullRange := model.ChannelRuntimeRange{Start: 0, End: r.cfg.Channels}
	fullQuery := activateChannelsRuntimeQuery(r.cfg, fullRange)
	cold, err := r.target.ChannelRuntimeSnapshots(ctx, fullQuery)
	if err != nil {
		result.Cold = cold
		return result, err
	}
	result.Cold = cold

	runResult, runErr := r.run(ctx, BuildActivateChannelsScenario(r.cfg), r.base.workers, r.discovered.Target)
	if runErr != nil && !hasAttemptReport(runResult.Report) {
		return result, runErr
	}

	active, err := r.target.ChannelRuntimeSnapshots(ctx, fullQuery)
	if err != nil {
		result.Active = active
		return result, err
	}
	result.Active = active

	holdSamples, err := r.holdSnapshots(ctx, fullQuery)
	if err != nil {
		result.HoldSamples = holdSamples
		return result, err
	}
	result.HoldSamples = holdSamples

	probes, err := r.probeBatches(ctx)
	if err != nil {
		result.ProbeBatches = probes
		return result, err
	}
	result.ProbeBatches = probes

	result.Evaluation = EvaluateActivateChannels(r.cfg, runResult.Report, cold, active, probes)
	if runErr != nil && runResult.Status != coordinator.StatusHardLimitFailed {
		reason := string(runResult.Status)
		if reason == "" {
			reason = "coordinator_failed"
		}
		addActivateChannelsFailure(&result.Evaluation, reason)
	}
	if result.Evaluation.Passed {
		result.Status = StatusPassed
	}

	if r.cfg.EvictAfter {
		evicts, err := r.evictBatches(ctx)
		result.EvictBatches = evicts
		if err != nil {
			addActivateChannelsFailure(&result.Evaluation, "evict_failed")
			result.Status = StatusFailed
			return result, err
		}
	}
	return result, nil
}

// DefaultActivateChannelsConfig returns defaults for a 10k group activation run.
func DefaultActivateChannelsConfig() ActivateChannelsConfig {
	return ActivateChannelsConfig{
		RunID:                 "activate-channels-10k",
		Channels:              10000,
		Users:                 1000,
		GroupMembers:          10,
		PrepareRatePerSecond:  1000,
		ConnectRatePerSecond:  500,
		ActivationConcurrency: 512,
		ActivationWindow:      120 * time.Second,
		Hold:                  60 * time.Second,
		HoldProbeInterval:     10 * time.Second,
		ProbeBatchSize:        1000,
		StableP99:             2 * time.Second,
		MaxSendackErrorRate:   0,
		MaxConnectErrorRate:   0,
		ReportDir:             "./tmp/wkbench-activate-channels",
	}
}

// Validate checks static activation config before discovery or execution.
func (c ActivateChannelsConfig) Validate() error {
	if len(nonEmptyStrings(c.APIAddrs)) == 0 {
		return fmt.Errorf("api addresses are required")
	}
	if trimmedRunID := strings.TrimSpace(c.RunID); trimmedRunID == "" || trimmedRunID != c.RunID {
		return fmt.Errorf("run-id must be non-empty and must not contain leading or trailing whitespace")
	}
	if c.Channels <= 0 {
		return fmt.Errorf("channels must be greater than zero")
	}
	if c.Users <= 0 {
		return fmt.Errorf("users must be greater than zero")
	}
	if c.GroupMembers <= 0 {
		return fmt.Errorf("group-members must be greater than zero")
	}
	if c.Users < c.GroupMembers {
		return fmt.Errorf("users must be greater than or equal to group-members")
	}
	if c.PrepareRatePerSecond < 0 || math.IsNaN(c.PrepareRatePerSecond) || math.IsInf(c.PrepareRatePerSecond, 0) {
		return fmt.Errorf("prepare-rate must not be negative")
	}
	if c.ConnectRatePerSecond < 0 || math.IsNaN(c.ConnectRatePerSecond) || math.IsInf(c.ConnectRatePerSecond, 0) {
		return fmt.Errorf("connect-rate must not be negative")
	}
	if c.ActivationConcurrency <= 0 {
		return fmt.Errorf("activation-concurrency must be greater than zero")
	}
	if c.ProbeBatchSize <= 0 {
		return fmt.Errorf("probe-batch-size must be greater than zero")
	}
	if c.ActivationWindow <= 0 {
		return fmt.Errorf("activation-window must be greater than zero")
	}
	if c.Hold < 0 {
		return fmt.Errorf("hold must not be negative")
	}
	if c.HoldProbeInterval <= 0 {
		return fmt.Errorf("hold-probe-interval must be greater than zero")
	}
	if c.StableP99 <= 0 {
		return fmt.Errorf("stable-p99 must be greater than zero")
	}
	if c.MaxSendackErrorRate < 0 || math.IsNaN(c.MaxSendackErrorRate) || math.IsInf(c.MaxSendackErrorRate, 0) {
		return fmt.Errorf("max-sendack-error-rate must not be negative")
	}
	if c.MaxConnectErrorRate < 0 || math.IsNaN(c.MaxConnectErrorRate) || math.IsInf(c.MaxConnectErrorRate, 0) {
		return fmt.Errorf("max-connect-error-rate must not be negative")
	}
	return nil
}

// BuildActivateChannelsScenario creates a fixed one-send-per-group activation scenario.
func BuildActivateChannelsScenario(cfg ActivateChannelsConfig) model.Scenario {
	return model.Scenario{
		Version: "wkbench/v1",
		Run: model.RunConfig{
			ID:        cfg.RunID,
			Duration:  cfg.ActivationWindow,
			Warmup:    0,
			Cooldown:  0,
			FailFast:  true,
			ReportDir: cfg.ReportDir,
		},
		Limits: model.LimitsConfig{
			FailOnSoft: true,
			Hard: model.HardLimitsConfig{
				MaxWorkerFailed:        0,
				MaxConnectErrorRate:    cfg.MaxConnectErrorRate,
				MaxSendackErrorRate:    cfg.MaxSendackErrorRate,
				MaxRecvVerifyErrorRate: 0,
			},
			Soft: model.SoftLimitsConfig{MaxSendackP99: cfg.StableP99},
		},
		Prepare: model.PrepareConfig{Concurrency: 8, RateLimit: model.Rate{PerSecond: cfg.PrepareRatePerSecond}},
		Identity: model.IdentityConfig{
			UIDPrefix:       "activate-u",
			DevicePrefix:    "activate-d",
			ClientMsgPrefix: "activate-msg",
			Token:           model.TokenConfig{Mode: "bench_api"},
		},
		Online: model.OnlineConfig{
			TotalUsers:     cfg.Users,
			ConnectRate:    model.Rate{PerSecond: cfg.ConnectRatePerSecond},
			GatewayBalance: "round_robin",
			Heartbeat: model.HeartbeatConfig{
				Enabled:  true,
				Interval: capacityHeartbeatInterval,
				Timeout:  capacityHeartbeatTimeout,
			},
		},
		Channels: model.ChannelsConfig{Profiles: []model.ChannelProfile{{
			Name:        activateChannelsProfileName,
			ChannelType: model.ChannelTypeGroup,
			Count:       cfg.Channels,
			Members:     model.MembersConfig{Count: cfg.GroupMembers, Overlap: "allowed"},
			Online:      model.ChannelOnlineConfig{MemberRatio: 1},
			Shard:       model.ShardConfig{Mode: "hash"},
			Prepare:     model.ChannelPrepareConfig{SubscribersBatchSize: 1000},
		}}},
		Messages: model.MessagesConfig{
			Payload: model.PayloadConfig{SizeBytes: 128, Mode: "deterministic"},
			Traffic: []model.TrafficConfig{{
				Name:           activateChannelsTrafficName,
				ChannelRef:     activateChannelsProfileName,
				RatePerChannel: model.Rate{PerSecond: activateChannelsRatePerChannel(cfg.ActivationWindow)},
				Concurrency:    cfg.ActivationConcurrency,
				SenderPick:     "round_robin",
				Verify:         model.VerifyConfig{Recv: model.RecvVerifyConfig{Mode: "none"}},
			}},
		},
		Cleanup: model.CleanupConfig{Enabled: false},
	}
}

// EvaluateActivateChannels classifies the activation run from send metrics and runtime evidence.
func EvaluateActivateChannels(cfg ActivateChannelsConfig, rep report.Report, cold []model.ChannelRuntimeSnapshot, active []model.ChannelRuntimeSnapshot, probes [][]model.ChannelRuntimeProbeResult) ActivateChannelsEvaluation {
	send := report.SendRunSummaryFromMetrics(rep.Metrics, cfg.ActivationWindow)
	targetChannels := positiveUint64(cfg.Channels)
	activeLeaderTotal := sumActiveLeader(active)
	activeNodes := activeNodeRuntimeDistribution(active)
	activeLeaderNodeCount, activeLeaderMaxNodeID, activeLeaderMaxNodeShare := activeLeaderDistribution(activeNodes)
	rejectedDelta := activationRejectedDelta(cold, active)
	missingEverywhere := probeMissingEverywhere(probes)

	got := ActivateChannelsEvaluation{
		ActivationSuccess:        send.SendSuccess,
		ActivationErrors:         send.SendErrors,
		ActivationBacklog:        activationBacklog(targetChannels, send.SendSuccess, send.SendErrors),
		SendackP50:               send.SendackP50,
		SendackP95:               send.SendackP95,
		SendackP99:               send.SendackP99,
		ActiveLeaderTotal:        activeLeaderTotal,
		ActiveLeaderNodeCount:    activeLeaderNodeCount,
		ActiveLeaderMaxNodeID:    activeLeaderMaxNodeID,
		ActiveLeaderMaxNodeShare: activeLeaderMaxNodeShare,
		ActiveNodes:              activeNodes,
		ActivationRejectedDelta:  rejectedDelta,
		ProbeMissingAllNodes:     missingEverywhere,
	}
	if send.SendSuccess != targetChannels {
		got.FailureReasons = append(got.FailureReasons, "activation_success_mismatch")
	}
	if send.SendErrors > 0 {
		got.FailureReasons = append(got.FailureReasons, "activation_errors")
	}
	if rep.Summary.WorkerFailed > 0 {
		got.FailureReasons = append(got.FailureReasons, "worker_failed")
	}
	if rep.Summary.ConnectErrorRate > cfg.MaxConnectErrorRate {
		got.FailureReasons = append(got.FailureReasons, "connect_error_rate_exceeded")
	}
	if rep.Summary.SendackErrorRate > cfg.MaxSendackErrorRate {
		got.FailureReasons = append(got.FailureReasons, "sendack_error_rate_exceeded")
	}
	if cfg.StableP99 > 0 && send.SendackP99 > cfg.StableP99 {
		got.FailureReasons = append(got.FailureReasons, "sendack_p99_exceeded")
	}
	if activeLeaderTotal < cfg.Channels {
		got.FailureReasons = append(got.FailureReasons, "active_leader_below_channels")
	}
	if len(nonEmptyStrings(cfg.APIAddrs)) > 1 && activeLeaderTotal > 0 && activeLeaderNodeCount == 1 {
		got.FailureReasons = append(got.FailureReasons, "active_leader_single_node")
	}
	if rejectedDelta > 0 {
		got.FailureReasons = append(got.FailureReasons, "activation_rejected_delta")
	}
	if len(missingEverywhere) > 0 {
		got.FailureReasons = append(got.FailureReasons, "probe_missing_all_nodes")
	}
	got.Passed = len(got.FailureReasons) == 0
	return got
}

func activateChannelsRatePerChannel(window time.Duration) float64 {
	if window <= 0 {
		return 0
	}
	return math.Nextafter(1.0/window.Seconds(), math.Inf(1))
}

func positiveUint64(value int) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

func activationBacklog(channels, success, errors uint64) uint64 {
	if success >= channels {
		return 0
	}
	remaining := channels - success
	if errors >= remaining {
		return 0
	}
	return remaining - errors
}

func sumActiveLeader(snapshots []model.ChannelRuntimeSnapshot) int {
	total := 0
	for _, snapshot := range snapshots {
		total += snapshot.ActiveLeader
	}
	return total
}

func activeNodeRuntimeDistribution(snapshots []model.ChannelRuntimeSnapshot) []ActivateChannelsNodeRuntime {
	if len(snapshots) == 0 {
		return nil
	}
	out := make([]ActivateChannelsNodeRuntime, 0, len(snapshots))
	for _, snapshot := range snapshots {
		out = append(out, ActivateChannelsNodeRuntime{
			NodeID:         snapshot.NodeID,
			ActiveTotal:    snapshot.ActiveTotal,
			ActiveLeader:   snapshot.ActiveLeader,
			ActiveFollower: snapshot.ActiveFollower,
			FollowerParked: snapshot.FollowerParked,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].NodeID < out[j].NodeID
	})
	return out
}

func activeLeaderDistribution(nodes []ActivateChannelsNodeRuntime) (leaderNodeCount int, maxNodeID uint64, maxShare float64) {
	total := 0
	maxLeaders := 0
	for _, node := range nodes {
		if node.ActiveLeader > 0 {
			leaderNodeCount++
			total += node.ActiveLeader
		}
		if node.ActiveLeader > maxLeaders {
			maxLeaders = node.ActiveLeader
			maxNodeID = node.NodeID
		}
	}
	if total > 0 {
		maxShare = float64(maxLeaders) / float64(total)
	}
	return leaderNodeCount, maxNodeID, maxShare
}

func activationRejectedDelta(cold []model.ChannelRuntimeSnapshot, active []model.ChannelRuntimeSnapshot) uint64 {
	coldTotal := sumActivationRejected(cold)
	activeTotal := sumActivationRejected(active)
	if activeTotal <= coldTotal {
		return 0
	}
	return activeTotal - coldTotal
}

func sumActivationRejected(snapshots []model.ChannelRuntimeSnapshot) uint64 {
	var total uint64
	for _, snapshot := range snapshots {
		total += snapshot.ActivationRejectedTotal
	}
	return total
}

func probeMissingEverywhere(probes [][]model.ChannelRuntimeProbeResult) []string {
	all := make(map[string]struct{})
	for _, batch := range probes {
		for channelID := range probeBatchMissingEverywhere(batch) {
			all[channelID] = struct{}{}
		}
	}
	out := make([]string, 0, len(all))
	for channelID := range all {
		out = append(out, channelID)
	}
	sort.Strings(out)
	return out
}

func probeBatchMissingEverywhere(batch []model.ChannelRuntimeProbeResult) map[string]struct{} {
	if len(batch) == 0 {
		return nil
	}
	var intersection map[string]struct{}
	for i, result := range batch {
		current := stringSet(result.Missing)
		if i == 0 {
			intersection = current
			continue
		}
		for channelID := range intersection {
			if _, ok := current[channelID]; !ok {
				delete(intersection, channelID)
			}
		}
		if len(intersection) == 0 {
			break
		}
	}
	return intersection
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func (r *ActivateChannelsRunner) setDefaults() {
	if r.base == nil {
		r.base = &Runner{discovered: r.discovered}
	}
	if r.target == nil {
		r.target = newActivateRunnerTarget(r.cfg)
	}
	if r.run == nil {
		r.run = func(ctx context.Context, scenario model.Scenario, workers []model.Worker, target model.Target) (coordinator.RunResult, error) {
			return coordinator.New(coordinator.CoordinatorConfig{Workers: workers, Target: target}).Run(ctx, scenario)
		}
	}
	if r.now == nil {
		r.now = time.Now
	}
}

func (r *ActivateChannelsRunner) ensureWorker() (bool, error) {
	if len(r.base.workers) > 0 {
		return false, nil
	}
	started := r.base.server == nil
	if err := r.base.startWorker(); err != nil {
		return false, err
	}
	if len(r.base.workers) == 0 {
		return false, fmt.Errorf("activate channels worker unavailable")
	}
	return started, nil
}

func newActivateRunnerTarget(cfg ActivateChannelsConfig) *activateRunnerTarget {
	targetCfg := targetapi.Config{APIAddrs: cfg.APIAddrs, Token: cfg.BenchToken}
	return &activateRunnerTarget{
		cfg:    targetCfg,
		client: targetapi.NewClient(targetCfg),
	}
}

func (t *activateRunnerTarget) Capabilities(ctx context.Context) (model.BenchCapabilities, error) {
	return t.client.Capabilities(ctx)
}

func (t *activateRunnerTarget) CapabilitiesAll(ctx context.Context) (map[string]model.BenchCapabilities, error) {
	addrs := nonEmptyStrings(t.cfg.APIAddrs)
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no target api addresses configured")
	}
	out := make(map[string]model.BenchCapabilities, len(addrs))
	for _, addr := range addrs {
		caps, err := targetapi.NewClient(targetapi.Config{APIAddrs: []string{addr}, Token: t.cfg.Token, HTTPClient: t.cfg.HTTPClient}).Capabilities(ctx)
		if err != nil {
			return out, fmt.Errorf("target %s capabilities failed: %w", addr, err)
		}
		out[addr] = caps
	}
	return out, nil
}

func (t *activateRunnerTarget) ChannelRuntimeSnapshots(ctx context.Context, query model.ChannelRuntimeQuery) ([]model.ChannelRuntimeSnapshot, error) {
	return t.client.ChannelRuntimeSnapshots(ctx, query)
}

func (t *activateRunnerTarget) ProbeChannelRuntimeAll(ctx context.Context, req model.ChannelRuntimeProbeRequest) ([]model.ChannelRuntimeProbeResult, error) {
	return t.client.ProbeChannelRuntimeAll(ctx, req)
}

func (t *activateRunnerTarget) EvictChannelRuntimeAll(ctx context.Context, req model.ChannelRuntimeEvictRequest) ([]model.ChannelRuntimeEvictResult, error) {
	return t.client.EvictChannelRuntimeAll(ctx, req)
}

func (r *ActivateChannelsRunner) validateCapabilities(ctx context.Context) error {
	if all, ok := r.target.(activateTargetCapabilitiesAll); ok {
		capsByAddr, err := all.CapabilitiesAll(ctx)
		if err != nil {
			return err
		}
		return validateActivateChannelsCapabilitiesAll(r.cfg.APIAddrs, capsByAddr, r.cfg.EvictAfter)
	}
	caps, err := r.target.Capabilities(ctx)
	if err != nil {
		return err
	}
	return validateActivateChannelsCapabilitiesForAddr("", caps, r.cfg.EvictAfter)
}

func validateActivateChannelsCapabilitiesAll(apiAddrs []string, capsByAddr map[string]model.BenchCapabilities, requireEvict bool) error {
	for _, addr := range nonEmptyStrings(apiAddrs) {
		caps, ok := capsByAddr[addr]
		if !ok {
			return fmt.Errorf("target %s capabilities missing", addr)
		}
		if err := validateActivateChannelsCapabilitiesForAddr(addr, caps, requireEvict); err != nil {
			return err
		}
	}
	return nil
}

func validateActivateChannelsCapabilitiesForAddr(addr string, caps model.BenchCapabilities, requireEvict bool) error {
	if err := validateActivateChannelsCapabilities(caps, requireEvict); err != nil {
		if strings.TrimSpace(addr) == "" {
			return err
		}
		return fmt.Errorf("target %s capabilities invalid: %w", addr, err)
	}
	return nil
}

func validateActivateChannelsCapabilities(caps model.BenchCapabilities, requireEvict bool) error {
	if !caps.Enabled {
		return fmt.Errorf("bench api is not enabled")
	}
	if caps.Version != "bench/v1" {
		return fmt.Errorf("bench api version = %q, want bench/v1", caps.Version)
	}
	if !caps.Supports.ChannelRuntimeSnapshot {
		return fmt.Errorf("bench api capability channel_runtime_snapshot is required")
	}
	if !caps.Supports.ChannelRuntimeProbe {
		return fmt.Errorf("bench api capability channel_runtime_probe is required")
	}
	if requireEvict && !caps.Supports.ChannelRuntimeEvict {
		return fmt.Errorf("bench api capability channel_runtime_evict is required")
	}
	return nil
}

func activateChannelsRuntimeQuery(cfg ActivateChannelsConfig, rng model.ChannelRuntimeRange) model.ChannelRuntimeQuery {
	return model.ChannelRuntimeQuery{
		RunID:       cfg.RunID,
		Profile:     activateChannelsProfileName,
		ChannelType: frame.ChannelTypeGroup,
		Range:       rng,
	}
}

func activateChannelsProbeRequest(cfg ActivateChannelsConfig, rng model.ChannelRuntimeRange) model.ChannelRuntimeProbeRequest {
	return model.ChannelRuntimeProbeRequest{
		RunID:       cfg.RunID,
		Profile:     activateChannelsProfileName,
		ChannelType: frame.ChannelTypeGroup,
		Range:       rng,
	}
}

func activateChannelsEvictRequest(cfg ActivateChannelsConfig, rng model.ChannelRuntimeRange) model.ChannelRuntimeEvictRequest {
	return model.ChannelRuntimeEvictRequest{
		RunID:       cfg.RunID,
		Profile:     activateChannelsProfileName,
		ChannelType: frame.ChannelTypeGroup,
		Range:       rng,
	}
}

func (r *ActivateChannelsRunner) holdSnapshots(ctx context.Context, query model.ChannelRuntimeQuery) ([][]model.ChannelRuntimeSnapshot, error) {
	if r.cfg.Hold <= 0 {
		return nil, nil
	}
	maxSamples := holdSampleCount(r.cfg.Hold, r.cfg.HoldProbeInterval)
	samples := make([][]model.ChannelRuntimeSnapshot, 0, maxSamples)
	deadline := r.now().Add(r.cfg.Hold)
	for len(samples) < maxSamples {
		remaining := deadline.Sub(r.now())
		wait := r.cfg.HoldProbeInterval
		if wait <= 0 {
			wait = remaining
		}
		if remaining > 0 && wait > remaining {
			wait = remaining
		}
		if wait > 0 {
			if err := sleepContext(ctx, wait); err != nil {
				return samples, err
			}
		} else if err := ctx.Err(); err != nil {
			return samples, err
		}
		snapshot, err := r.target.ChannelRuntimeSnapshots(ctx, query)
		if err != nil {
			return samples, err
		}
		samples = append(samples, snapshot)
		if !r.now().Before(deadline) {
			break
		}
	}
	return samples, nil
}

func holdSampleCount(hold time.Duration, interval time.Duration) int {
	if hold <= 0 {
		return 0
	}
	if interval <= 0 {
		return 1
	}
	return int(math.Ceil(float64(hold) / float64(interval)))
}

func (r *ActivateChannelsRunner) probeBatches(ctx context.Context) ([][]model.ChannelRuntimeProbeResult, error) {
	ranges := activateChannelsRuntimeRanges(r.cfg.Channels, r.cfg.ProbeBatchSize)
	out := make([][]model.ChannelRuntimeProbeResult, 0, len(ranges))
	for _, rng := range ranges {
		result, err := r.target.ProbeChannelRuntimeAll(ctx, activateChannelsProbeRequest(r.cfg, rng))
		if len(result) > 0 || err == nil {
			out = append(out, result)
		}
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func (r *ActivateChannelsRunner) evictBatches(ctx context.Context) ([][]model.ChannelRuntimeEvictResult, error) {
	ranges := activateChannelsRuntimeRanges(r.cfg.Channels, r.cfg.ProbeBatchSize)
	out := make([][]model.ChannelRuntimeEvictResult, 0, len(ranges))
	for _, rng := range ranges {
		result, err := r.target.EvictChannelRuntimeAll(ctx, activateChannelsEvictRequest(r.cfg, rng))
		if len(result) > 0 || err == nil {
			out = append(out, result)
		}
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func activateChannelsRuntimeRanges(channels int, batchSize int) []model.ChannelRuntimeRange {
	if channels <= 0 || batchSize <= 0 {
		return nil
	}
	ranges := make([]model.ChannelRuntimeRange, 0, (channels+batchSize-1)/batchSize)
	for start := 0; start < channels; start += batchSize {
		end := start + batchSize
		if end > channels {
			end = channels
		}
		ranges = append(ranges, model.ChannelRuntimeRange{Start: start, End: end})
	}
	return ranges
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func addActivateChannelsFailure(eval *ActivateChannelsEvaluation, reason string) {
	if eval == nil || reason == "" {
		return
	}
	for _, existing := range eval.FailureReasons {
		if existing == reason {
			eval.Passed = len(eval.FailureReasons) == 0
			return
		}
	}
	eval.FailureReasons = append(eval.FailureReasons, reason)
	eval.Passed = false
}
