package sim_test

import (
	"math"
	"testing"
	"time"

	benchconfig "github.com/WuKongIM/WuKongIM/internal/bench/config"
	"github.com/WuKongIM/WuKongIM/internal/bench/planner"
	"github.com/WuKongIM/WuKongIM/internal/bench/workload"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/hashslot"
	"github.com/stretchr/testify/require"
)

func TestSmallStabilityPresetMatchesReviewedScale(t *testing.T) {
	scenario, err := benchconfig.LoadScenario("cloud-small.yaml")
	require.NoError(t, err)

	require.Equal(t, "small", scenario.Objectives.Scale)
	require.True(t, scenario.Objectives.Standard)
	require.Equal(t, 10_000, scenario.Identity.TotalUsers)
	require.Equal(t, 1_000, scenario.Online.TotalUsers)
	require.Equal(t, 48*time.Hour, scenario.Run.Duration)
	require.Equal(t, 10*time.Minute, scenario.Run.Warmup)
	require.Equal(t, 500.0, scenario.Objectives.IngressQPS.PerSecond)
	require.Equal(t, 5_000.0, scenario.Objectives.OnlineFanoutQPS.PerSecond)
	require.Equal(t, 0.1, scenario.Objectives.ToleranceRatio)
	require.Equal(t, 5*time.Minute, scenario.Objectives.ActiveChannelWindow)
	require.True(t, scenario.Objectives.RequireAllChannelsActive)
	require.Equal(t, 256, scenario.Messages.Payload.SizeBytes)
	requireScheduledChurn(t, scenario)

	personChannels, groupChannels := channelCounts(scenario)
	require.Equal(t, 500, personChannels)
	require.Equal(t, 500, groupChannels)
	require.InDelta(t, 500, configuredIngressQPS(scenario), 0.001)
	requireGroupTraffic(t, scenario, "ordinary-small-group", 171, 10, 10, 75)
	requireGroupTraffic(t, scenario, "ordinary-medium-group", 61, 30, 30, 52.5)
	requireGroupTraffic(t, scenario, "ordinary-large-group", 12, 60, 60, 22.5)
	requireGroupTraffic(t, scenario, "max-group", 256, 100, 10, 100)

	plan, err := planner.Build(scenario, []model.Worker{{ID: "simulator-worker", Weight: 1}})
	require.NoError(t, err)
	require.Equal(t, model.Range{Start: 0, End: 10_000}, plan.IdentityPool)
	require.Equal(t, model.Range{Start: 0, End: 1_000}, plan.OnlineIdentityPool)
}

func TestMediumStabilityPresetMatchesReviewedScale(t *testing.T) {
	scenario, err := benchconfig.LoadScenario("cloud-medium.yaml")
	require.NoError(t, err)

	require.Equal(t, "medium", scenario.Objectives.Scale)
	require.True(t, scenario.Objectives.Standard)
	require.Equal(t, 100_000, scenario.Identity.TotalUsers)
	require.Equal(t, 10_000, scenario.Online.TotalUsers)
	require.Equal(t, 5_000.0, scenario.Objectives.IngressQPS.PerSecond)
	require.Equal(t, 50_000.0, scenario.Objectives.OnlineFanoutQPS.PerSecond)
	require.Equal(t, 48*time.Hour, scenario.Run.Duration)
	require.Equal(t, 10*time.Minute, scenario.Run.Warmup)
	require.Equal(t, 256, scenario.Messages.Payload.SizeBytes)
	requireScheduledChurn(t, scenario)
	personChannels, groupChannels := channelCounts(scenario)
	require.Equal(t, 5_000, personChannels)
	require.Equal(t, 5_000, groupChannels)
	require.InDelta(t, 5_000, configuredIngressQPS(scenario), 0.001)
	requireGroupTraffic(t, scenario, "ordinary-small-group", 3_321, 20, 5, 1_200)
	requireGroupTraffic(t, scenario, "ordinary-medium-group", 1_186, 100, 15, 840)
	requireGroupTraffic(t, scenario, "ordinary-large-group", 237, 500, 55, 360)
	requireGroupTraffic(t, scenario, "max-group", 256, 1_000, 100, 100)

	plan, err := planner.Build(scenario, []model.Worker{{ID: "simulator-worker", Weight: 1}})
	require.NoError(t, err)
	require.Equal(t, model.Range{Start: 0, End: 100_000}, plan.IdentityPool)
	require.Equal(t, model.Range{Start: 0, End: 10_000}, plan.OnlineIdentityPool)
}

func TestLargeStabilityPresetMatchesReviewedScale(t *testing.T) {
	scenario, err := benchconfig.LoadScenario("cloud-large.yaml")
	require.NoError(t, err)

	require.Equal(t, "large", scenario.Objectives.Scale)
	require.True(t, scenario.Objectives.Standard)
	require.Equal(t, 1_000_000, scenario.Identity.TotalUsers)
	require.Equal(t, 100_000, scenario.Online.TotalUsers)
	require.Equal(t, 20_000.0, scenario.Objectives.IngressQPS.PerSecond)
	require.Equal(t, 200_000.0, scenario.Objectives.OnlineFanoutQPS.PerSecond)
	require.Equal(t, 48*time.Hour, scenario.Run.Duration)
	require.Equal(t, 10*time.Minute, scenario.Run.Warmup)
	require.Equal(t, 256, scenario.Messages.Payload.SizeBytes)
	requireScheduledChurn(t, scenario)
	personChannels, groupChannels := channelCounts(scenario)
	require.Equal(t, 50_000, personChannels)
	require.Equal(t, 50_000, groupChannels)
	require.InDelta(t, 20_000, configuredIngressQPS(scenario), 0.001)
	requireGroupTraffic(t, scenario, "ordinary-small-group", 34_821, 50, 5, 4_980)
	requireGroupTraffic(t, scenario, "ordinary-medium-group", 12_436, 500, 15, 3_486)
	requireGroupTraffic(t, scenario, "ordinary-large-group", 2_487, 2_000, 50, 1_494)
	requireGroupTraffic(t, scenario, "max-group", 256, 10_000, 1_000, 40)

	plan, err := planner.Build(scenario, []model.Worker{{ID: "simulator-worker", Weight: 1}})
	require.NoError(t, err)
	require.Equal(t, model.Range{Start: 0, End: 1_000_000}, plan.IdentityPool)
	require.Equal(t, model.Range{Start: 0, End: 100_000}, plan.OnlineIdentityPool)
}

func requireScheduledChurn(t *testing.T, scenario model.Scenario) {
	t.Helper()
	require.True(t, scenario.Online.Churn.Enabled)
	require.Equal(t, 5*time.Minute, scenario.Online.Churn.Interval)
	require.Equal(t, 0.01, scenario.Online.Churn.Ratio)
	require.Equal(t, 0.5, scenario.Online.Churn.SameUserRatio)
	require.Equal(t, 0.5, scenario.Online.Churn.IdentitySwapRatio)
	require.False(t, scenario.Online.Churn.HistorySync)
}

func channelCounts(scenario model.Scenario) (person int, group int) {
	for _, profile := range scenario.Channels.Profiles {
		switch profile.ChannelType {
		case model.ChannelTypePerson:
			person += profile.Count
		case model.ChannelTypeGroup:
			group += profile.Count
		}
	}
	return person, group
}

func configuredIngressQPS(scenario model.Scenario) float64 {
	counts := make(map[string]int, len(scenario.Channels.Profiles))
	for _, profile := range scenario.Channels.Profiles {
		counts[profile.Name] = profile.Count
	}
	var total float64
	for _, traffic := range scenario.Messages.Traffic {
		total += float64(counts[traffic.ChannelRef]) * traffic.RatePerChannel.PerSecond
	}
	return total
}

func requireGroupTraffic(t *testing.T, scenario model.Scenario, name string, count, members, onlineMembers int, ingressQPS float64) {
	t.Helper()
	profileFound := false
	for _, profile := range scenario.Channels.Profiles {
		if profile.Name != name {
			continue
		}
		profileFound = true
		require.Equal(t, model.ChannelTypeGroup, profile.ChannelType)
		require.Equal(t, count, profile.Count)
		require.Equal(t, members, profile.Members.Count)
		require.True(t, math.Abs(profile.Online.MemberRatio-float64(onlineMembers)/float64(members)) < 1e-9)
		if name == "max-group" {
			require.True(t, profile.Shard.HashSlotSpread)
			require.Equal(t, uint16(256), profile.Shard.HashSlotCount)
			for channelIndex := 0; channelIndex < count; channelIndex++ {
				channelID := workload.GroupChannelIDForHashSlot(scenario.Run.ID, name, channelIndex, 256)
				require.Equal(t, uint16(channelIndex), hashslot.HashSlotForKey(channelID, 256))
			}
		}
	}
	require.True(t, profileFound, "group profile %q not found", name)
	trafficFound := false
	for _, traffic := range scenario.Messages.Traffic {
		if traffic.ChannelRef != name {
			continue
		}
		trafficFound = true
		require.Equal(t, "weighted_80_20", traffic.SenderPick)
		require.InDelta(t, ingressQPS, float64(count)*traffic.RatePerChannel.PerSecond, 0.001)
	}
	require.True(t, trafficFound, "group traffic %q not found", name)
}
