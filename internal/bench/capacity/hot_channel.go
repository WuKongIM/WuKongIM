package capacity

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/coordinator"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const (
	hotChannelProfileName = "hot-group"
	hotChannelTrafficName = "hot-group-send"
)

// HotChannelConfig controls a single logical channel capacity search.
type HotChannelConfig struct {
	Config
	// Senders is the number of online group members used as sending clients.
	Senders int
}

// DefaultHotChannelConfig returns local-safe defaults for hot-channel searches.
func DefaultHotChannelConfig() HotChannelConfig {
	cfg := DefaultConfig()
	cfg.Profile = ProfileGroup
	cfg.MaxQPS = 50000
	cfg.ReportDir = "./tmp/wkbench-hot-channel"
	return HotChannelConfig{Config: cfg, Senders: 16}
}

// Validate checks static hot-channel capacity config.
func (c HotChannelConfig) Validate() error {
	cfg := c.Config
	if strings.TrimSpace(cfg.Profile) == "" {
		cfg.Profile = ProfileGroup
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if c.Senders <= 0 {
		return fmt.Errorf("senders must be greater than zero")
	}
	return nil
}

// BuildHotChannelScenario creates a one-group-channel scenario for one attempt.
func BuildHotChannelScenario(cfg HotChannelConfig, attempt Attempt) model.Scenario {
	senders := cfg.Senders
	if senders <= 0 {
		senders = 1
	}
	runID := fmt.Sprintf("capacity-hot-channel-%03d-%gqps", attempt.Index, attempt.OfferedQPS)
	return model.Scenario{
		Version: "wkbench/v1",
		Run: model.RunConfig{
			ID:        runID,
			Duration:  cfg.Duration,
			Warmup:    cfg.Warmup,
			Cooldown:  cfg.Cooldown,
			FailFast:  true,
			ReportDir: attemptReportDir(cfg.ReportDir, attempt),
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
			UIDPrefix:       "hot-channel-u",
			DevicePrefix:    "hot-channel-d",
			ClientMsgPrefix: "hot-channel-msg",
			Token:           model.TokenConfig{Mode: "bench_api"},
		},
		Online: model.OnlineConfig{
			TotalUsers:     senders,
			ConnectRate:    model.Rate{PerSecond: 1000},
			GatewayBalance: "round_robin",
			Heartbeat: model.HeartbeatConfig{
				Enabled:  true,
				Interval: capacityHeartbeatInterval,
				Timeout:  capacityHeartbeatTimeout,
			},
		},
		Channels: model.ChannelsConfig{Profiles: []model.ChannelProfile{{
			Name:        hotChannelProfileName,
			ChannelType: model.ChannelTypeGroup,
			Count:       1,
			Members:     model.MembersConfig{Count: senders, Overlap: "disallowed"},
			Online:      model.ChannelOnlineConfig{MemberRatio: 1},
			Shard:       model.ShardConfig{Mode: "hash"},
			Prepare:     model.ChannelPrepareConfig{SubscribersBatchSize: 1000},
		}}},
		Messages: model.MessagesConfig{
			Payload: model.PayloadConfig{SizeBytes: 128, Mode: "deterministic"},
			Traffic: []model.TrafficConfig{{
				Name:           hotChannelTrafficName,
				ChannelRef:     hotChannelProfileName,
				RatePerChannel: model.Rate{PerSecond: attempt.OfferedQPS},
				Concurrency:    hotChannelConcurrencyForQPS(attempt.OfferedQPS, senders),
				SenderPick:     "round_robin",
				Verify:         model.VerifyConfig{Recv: model.RecvVerifyConfig{Mode: "none"}},
			}},
		},
		Cleanup: model.CleanupConfig{Enabled: false},
	}
}

func hotChannelConcurrencyForQPS(qps float64, senders int) int {
	concurrency := concurrencyForQPS(qps)
	if concurrency < senders {
		concurrency = senders
	}
	if concurrency < 1 {
		concurrency = 1
	}
	return concurrency
}

// HotChannelRunner executes capacity attempts with a fixed one-channel scenario.
type HotChannelRunner struct {
	cfg        HotChannelConfig
	discovered DiscoveredTarget
	base       *Runner
}

// NewHotChannelRunner creates a hot-channel runner using one temporary local worker.
func NewHotChannelRunner(cfg HotChannelConfig, discovered DiscoveredTarget) *HotChannelRunner {
	return &HotChannelRunner{cfg: cfg, discovered: discovered, base: NewRunner(cfg.Config, discovered)}
}

// Run executes the configured hot-channel capacity search.
func (r *HotChannelRunner) Run(ctx context.Context) (Result, error) {
	if err := r.base.startWorker(); err != nil {
		return Result{}, err
	}
	defer r.base.close()
	result, err := Search(ctx, r.cfg.Config, hotChannelAttemptRunner{cfg: r.cfg, discovered: r.discovered, workers: r.base.workers})
	result.Profile = "hot-channel"
	result.ReportDir = r.cfg.ReportDir
	return result, err
}

type hotChannelAttemptRunner struct {
	cfg        HotChannelConfig
	discovered DiscoveredTarget
	workers    []model.Worker
}

func (r hotChannelAttemptRunner) RunAttempt(ctx context.Context, attempt Attempt) (AttemptResult, error) {
	scenario := BuildHotChannelScenario(r.cfg, attempt)
	coord := coordinator.New(coordinator.CoordinatorConfig{Workers: r.workers, Target: r.discovered.Target})
	defer func() {
		for _, w := range r.workers {
			stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = stopWorker(stopCtx, w)
			cancel()
		}
	}()
	result, err := coord.Run(ctx, scenario)
	attemptResult := EvaluateAttempt(r.cfg.Config, attempt, result.Report)
	attemptResult.ReportDir = scenario.Run.ReportDir
	if err != nil && result.Status != coordinator.StatusHardLimitFailed {
		applyCoordinatorFailure(&attemptResult, result)
	}
	if err != nil && !hasAttemptReport(result.Report) {
		return attemptResult, err
	}
	return attemptResult, nil
}
