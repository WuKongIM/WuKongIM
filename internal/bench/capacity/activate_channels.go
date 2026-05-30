package capacity

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"github.com/WuKongIM/WuKongIM/internal/bench/report"
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
	// ActivationRejectedDelta is the increase in rejected activation requests during the run.
	ActivationRejectedDelta uint64 `json:"activation_rejected_delta"`
	// ProbeMissingAllNodes lists probed channels absent from every responding target node.
	ProbeMissingAllNodes []string `json:"probe_missing_all_nodes,omitempty"`
}

// DefaultActivateChannelsConfig returns defaults for a 10k group activation run.
func DefaultActivateChannelsConfig() ActivateChannelsConfig {
	return ActivateChannelsConfig{
		RunID:                 "activate-channels-10k",
		Channels:              10000,
		Users:                 20000,
		GroupMembers:          10,
		ActivationConcurrency: 2000,
		ActivationWindow:      10 * time.Second,
		Hold:                  60 * time.Second,
		HoldProbeInterval:     10 * time.Second,
		ProbeBatchSize:        1000,
		StableP99:             200 * time.Millisecond,
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
		Prepare: model.PrepareConfig{Concurrency: 8, RateLimit: model.Rate{PerSecond: 1000}},
		Identity: model.IdentityConfig{
			UIDPrefix:       "activate-u",
			DevicePrefix:    "activate-d",
			ClientMsgPrefix: "activate-msg",
			Token:           model.TokenConfig{Mode: "bench_api"},
		},
		Online: model.OnlineConfig{
			TotalUsers:     cfg.Users,
			ConnectRate:    model.Rate{PerSecond: 1000},
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
	rejectedDelta := activationRejectedDelta(cold, active)
	missingEverywhere := probeMissingEverywhere(probes)

	got := ActivateChannelsEvaluation{
		ActivationSuccess:       send.SendSuccess,
		ActivationErrors:        send.SendErrors,
		ActivationBacklog:       activationBacklog(targetChannels, send.SendSuccess, send.SendErrors),
		SendackP50:              send.SendackP50,
		SendackP95:              send.SendackP95,
		SendackP99:              send.SendackP99,
		ActiveLeaderTotal:       activeLeaderTotal,
		ActivationRejectedDelta: rejectedDelta,
		ProbeMissingAllNodes:    missingEverywhere,
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
