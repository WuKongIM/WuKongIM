package capacity

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

const capacityRatePerChannel = 1.0

const (
	capacityHeartbeatInterval = 30 * time.Second
	capacityHeartbeatTimeout  = 5 * time.Second
)

// Attempt identifies one offered-QPS attempt in the capacity search.
type Attempt struct {
	// Index is the zero-based attempt index.
	Index int `json:"index"`
	// OfferedQPS is the target ingress QPS for this attempt.
	OfferedQPS float64 `json:"offered_qps"`
}

// BuildScenario creates a normal wkbench scenario for one capacity attempt.
func BuildScenario(cfg Config, attempt Attempt) model.Scenario {
	profile := strings.TrimSpace(cfg.Profile)
	if profile == "" {
		profile = ProfileMixed
	}
	personQPS, groupQPS := profileQPS(profile, attempt.OfferedQPS)
	personChannels := channelCountForQPS(personQPS)
	groupChannels := channelCountForQPS(groupQPS)
	runID := fmt.Sprintf("capacity-send-%s-%03d-%gqps", profile, attempt.Index, attempt.OfferedQPS)
	scenario := model.Scenario{
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
			UIDPrefix:       "capacity-u",
			DevicePrefix:    "capacity-d",
			ClientMsgPrefix: "capacity-msg",
			Token:           model.TokenConfig{Mode: "bench_api"},
		},
		Online: model.OnlineConfig{
			TotalUsers:     totalUsersForScenario(personChannels, groupChannels, cfg.GroupMembers),
			ConnectRate:    model.Rate{PerSecond: 1000},
			GatewayBalance: "round_robin",
			Heartbeat: model.HeartbeatConfig{
				Enabled:  true,
				Interval: capacityHeartbeatInterval,
				Timeout:  capacityHeartbeatTimeout,
			},
		},
		Messages: model.MessagesConfig{
			Payload: model.PayloadConfig{SizeBytes: 128, Mode: "deterministic"},
		},
		Cleanup: model.CleanupConfig{Enabled: false},
	}
	if personChannels > 0 {
		scenario.Channels.Profiles = append(scenario.Channels.Profiles, model.ChannelProfile{
			Name:        "person-chat",
			ChannelType: model.ChannelTypePerson,
			Count:       personChannels,
			Online:      model.ChannelOnlineConfig{SenderRatio: 1, RecipientRatio: 1},
		})
		scenario.Messages.Traffic = append(scenario.Messages.Traffic, model.TrafficConfig{
			Name:           "person-send",
			ChannelRef:     "person-chat",
			RatePerChannel: model.Rate{PerSecond: capacityRatePerChannel},
			Concurrency:    concurrencyForQPS(personQPS),
			SenderPick:     "round_robin",
			Verify:         model.VerifyConfig{Recv: model.RecvVerifyConfig{Mode: "none"}},
		})
	}
	if groupChannels > 0 {
		scenario.Channels.Profiles = append(scenario.Channels.Profiles, model.ChannelProfile{
			Name:        "small-group",
			ChannelType: model.ChannelTypeGroup,
			Count:       groupChannels,
			Members:     model.MembersConfig{Count: cfg.GroupMembers, Overlap: "disallowed"},
			Online:      model.ChannelOnlineConfig{MemberRatio: 1},
			Shard:       model.ShardConfig{Mode: "hash"},
			Prepare:     model.ChannelPrepareConfig{SubscribersBatchSize: 1000},
		})
		scenario.Messages.Traffic = append(scenario.Messages.Traffic, model.TrafficConfig{
			Name:           "group-send",
			ChannelRef:     "small-group",
			RatePerChannel: model.Rate{PerSecond: capacityRatePerChannel},
			Concurrency:    concurrencyForQPS(groupQPS),
			SenderPick:     "first_online",
			Verify:         model.VerifyConfig{Recv: model.RecvVerifyConfig{Mode: "none"}},
		})
	}
	return scenario
}

func profileQPS(profile string, offered float64) (float64, float64) {
	switch profile {
	case ProfilePerson:
		return offered, 0
	case ProfileGroup:
		return 0, offered
	default:
		return offered / 2, offered / 2
	}
}

func channelCountForQPS(qps float64) int {
	if qps <= 0 {
		return 0
	}
	return int(math.Ceil(qps / capacityRatePerChannel))
}

func totalUsersForScenario(personChannels, groupChannels, groupMembers int) int {
	users := personChannels*2 + groupChannels*groupMembers
	if users < groupMembers {
		users = groupMembers
	}
	if users < 1 {
		users = 1
	}
	return users
}

func concurrencyForQPS(qps float64) int {
	if qps <= 0 {
		return 0
	}
	concurrency := int(math.Ceil(qps / 10))
	if concurrency < 1 {
		return 1
	}
	return concurrency
}

func attemptReportDir(root string, attempt Attempt) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	return filepath.Join(root, "attempts", fmt.Sprintf("%06.0f-qps", attempt.OfferedQPS))
}
