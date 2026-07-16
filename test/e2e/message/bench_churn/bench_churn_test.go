//go:build e2e

package bench_churn

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/planner"
	"github.com/WuKongIM/WuKongIM/internal/bench/worker"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestIdentitySwapKeepsReplacementUserInGroup(t *testing.T) {
	node := suite.New(t).StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, map[string]string{
		"WK_BENCH_API_ENABLE":         "true",
		"WK_BENCH_API_MAX_BATCH_SIZE": "100",
	}))
	scenario := model.Scenario{
		Version: "wkbench/v1",
		Run: model.RunConfig{
			ID: "e2e-bench-churn", Duration: 2 * time.Second, FailFast: true,
		},
		Identity: model.IdentityConfig{
			TotalUsers: 4, UIDPrefix: "churn-u", DevicePrefix: "churn-d", ClientMsgPrefix: "churn-msg",
			Token: model.TokenConfig{Mode: "bench_api"},
		},
		Online: model.OnlineConfig{
			TotalUsers: 2, ConnectRate: model.Rate{PerSecond: 100}, GatewayBalance: "round_robin",
			Churn: model.ChurnConfig{
				Enabled: true, Interval: time.Second, Ratio: 0.5, SameUserRatio: 0, IdentitySwapRatio: 1,
			},
		},
		Channels: model.ChannelsConfig{Profiles: []model.ChannelProfile{{
			Name: "group-a", ChannelType: model.ChannelTypeGroup, Count: 1,
			Members: model.MembersConfig{Count: 2, Pick: "deterministic_hash", Overlap: "disallowed"},
			Online:  model.ChannelOnlineConfig{MemberRatio: 1},
			Shard:   model.ShardConfig{Mode: "hash"},
			Prepare: model.ChannelPrepareConfig{SubscribersBatchSize: 100},
		}}},
		Messages: model.MessagesConfig{
			Payload: model.PayloadConfig{SizeBytes: 32, Mode: "deterministic"},
			Traffic: []model.TrafficConfig{{
				Name: "group-send", ChannelRef: "group-a", RatePerChannel: model.Rate{PerSecond: 10},
				Concurrency: 4, AckTimeout: 3 * time.Second, SenderPick: "round_robin",
				Verify: model.VerifyConfig{Recv: model.RecvVerifyConfig{Mode: "none"}},
			}},
		},
	}
	workerConfig := model.Worker{ID: "worker-a", Weight: 1}
	plan, err := planner.Build(scenario, []model.Worker{workerConfig})
	require.NoError(t, err)
	assignment := worker.Assignment{
		RunID: scenario.Run.ID, WorkerID: workerConfig.ID,
		Target: model.Target{
			API:      model.TargetAPIConfig{Addrs: []string{"http://" + node.APIAddr()}},
			BenchAPI: model.BenchAPIConfig{Enabled: true, Addrs: []string{"http://" + node.APIAddr()}},
			Gateway:  model.TargetGatewayConfig{TCP: model.TargetGatewayTCPConfig{Addrs: []string{node.GatewayAddr()}}},
		},
		Scenario: scenario,
		Plan:     plan.Workers[workerConfig.ID],
	}

	runner := worker.NewDefaultWorkloadRunner(nil)
	runner.(worker.AssignmentStarter).BeginAssignment(assignment)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, runner.Prepare(ctx, assignment))
	require.NoError(t, runner.Connect(ctx, assignment))
	require.NoError(t, runner.Run(ctx, assignment))
	require.Equal(t, uint64(1), runner.(worker.MetricsReporter).MetricsSnapshot().Counters["churn_window_total"])
	require.NoError(t, runner.Cooldown(ctx, assignment))
}
