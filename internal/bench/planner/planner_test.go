package planner

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"github.com/stretchr/testify/require"
)

func TestBuildRejectsPersonProfilesExceedingIdentityPool(t *testing.T) {
	scenario := scenarioWithPersonCount(51)
	scenario.Online.TotalUsers = 100
	workers := []model.Worker{{ID: "a", Weight: 1}}

	_, err := Build(scenario, workers)

	require.ErrorContains(t, err, "person profiles require 102 distinct participants")
}

func TestPlanPersonPairsByWorkerWeight(t *testing.T) {
	scenario := scenarioWithPersonCount(500000)
	workers := []model.Worker{{ID: "a", Weight: 1}, {ID: "b", Weight: 1}, {ID: "c", Weight: 2}}

	plan, err := Build(scenario, workers)

	require.NoError(t, err)
	require.Equal(t, model.Range{Start: 0, End: 125000}, plan.Workers["a"].Profiles["person-chat"].ChannelRange)
	require.Equal(t, model.Range{Start: 125000, End: 250000}, plan.Workers["b"].Profiles["person-chat"].ChannelRange)
	require.Equal(t, model.Range{Start: 250000, End: 500000}, plan.Workers["c"].Profiles["person-chat"].ChannelRange)
	require.Equal(t, model.Range{Start: 0, End: 250000}, plan.Workers["a"].Profiles["person-chat"].ParticipantRange)
	require.Equal(t, model.Range{Start: 500000, End: 1000000}, plan.Workers["c"].Profiles["person-chat"].ParticipantRange)
}

func TestPlanManyGroupChannelsByWorkerWeightAndOwnersAreDeterministic(t *testing.T) {
	scenario := scenarioWithManyGroup(10, 100, "2/s")
	workers := []model.Worker{{ID: "a", Weight: 1}, {ID: "b", Weight: 1}, {ID: "c", Weight: 2}}

	plan, err := Build(scenario, workers)
	require.NoError(t, err)
	repeat, err := Build(scenario, workers)
	require.NoError(t, err)

	require.Equal(t, []string{"many-group"}, plan.ProfileOrder)
	require.Equal(t, model.Range{Start: 0, End: 2}, plan.Workers["a"].Profiles["many-group"].ChannelRange)
	require.Equal(t, model.Range{Start: 2, End: 5}, plan.Workers["b"].Profiles["many-group"].ChannelRange)
	require.Equal(t, model.Range{Start: 5, End: 10}, plan.Workers["c"].Profiles["many-group"].ChannelRange)
	require.Equal(t, 2.0, plan.Workers["a"].Profiles["many-group"].GlobalRate.PerSecond)
	require.Equal(t, 2.0, plan.Workers["c"].Profiles["many-group"].LocalRate.PerSecond)
	require.Equal(t, plan.ChannelOwners["many-group"], repeat.ChannelOwners["many-group"])
	require.Len(t, plan.ChannelOwners["many-group"], 10)
	for channelIndex, ownerID := range plan.ChannelOwners["many-group"] {
		require.Contains(t, map[string]struct{}{"a": {}, "b": {}, "c": {}}, ownerID, "channel %d", channelIndex)
	}
}

func TestPlanHugeGroupSplitsMembersAndTraffic(t *testing.T) {
	scenario := scenarioWithHugeGroup(10000, "20/s")
	workers := []model.Worker{{ID: "a", Weight: 1}, {ID: "b", Weight: 1}, {ID: "c", Weight: 2}}

	plan, err := Build(scenario, workers)

	require.NoError(t, err)
	require.Equal(t, model.Range{Start: 0, End: 1}, plan.Workers["a"].Profiles["huge-group"].ChannelRange)
	require.Equal(t, model.Range{Start: 0, End: 2500}, plan.Workers["a"].Profiles["huge-group"].MemberRange)
	require.Equal(t, model.Range{Start: 2500, End: 5000}, plan.Workers["b"].Profiles["huge-group"].MemberRange)
	require.Equal(t, model.Range{Start: 5000, End: 10000}, plan.Workers["c"].Profiles["huge-group"].MemberRange)
	require.Equal(t, 1, plan.Workers["a"].Profiles["huge-group"].TrafficPartitionCount)
	require.Equal(t, []int{0}, plan.Workers["a"].Profiles["huge-group"].OwnedTrafficPartitions)
	require.Equal(t, []int{2, 3}, plan.Workers["c"].Profiles["huge-group"].OwnedTrafficPartitions)
	require.Equal(t, 20.0, plan.Workers["a"].Profiles["huge-group"].GlobalRate.PerSecond)
	require.InDelta(t, 5.0, plan.Workers["a"].Profiles["huge-group"].LocalRate.PerSecond, 0.001)
	require.InDelta(t, 10.0, plan.Workers["c"].Profiles["huge-group"].LocalRate.PerSecond, 0.001)
}

func TestBuildValidatesProfileAndWorkerInputs(t *testing.T) {
	tests := []struct {
		name     string
		scenario model.Scenario
		workers  []model.Worker
		wantErr  string
	}{
		{
			name:     "duplicate profile names",
			scenario: scenarioWithProfiles(channelProfile("dup", "group", 1), channelProfile("dup", "group", 1)),
			workers:  []model.Worker{{ID: "a", Weight: 1}},
			wantErr:  "duplicate channel profile name",
		},
		{
			name:     "unsupported channel type",
			scenario: scenarioWithProfiles(channelProfile("agent", "agent", 1)),
			workers:  []model.Worker{{ID: "a", Weight: 1}},
			wantErr:  "unsupported channel type",
		},
		{
			name:     "duplicate worker id",
			scenario: scenarioWithManyGroup(1, 10, "1/s"),
			workers:  []model.Worker{{ID: "a", Weight: 1}, {ID: "a", Weight: 1}},
			wantErr:  "duplicate worker id",
		},
		{
			name:     "non-positive worker weight",
			scenario: scenarioWithManyGroup(1, 10, "1/s"),
			workers:  []model.Worker{{ID: "a", Weight: 0}},
			wantErr:  "weight must be greater than zero",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Build(tt.scenario, tt.workers)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func scenarioWithPersonCount(count int) model.Scenario {
	scenario := scenarioWithProfiles(channelProfile("person-chat", "person", count))
	scenario.Online.TotalUsers = count * 2
	return scenario
}

func scenarioWithManyGroup(count, members int, rate string) model.Scenario {
	profile := channelProfile("many-group", "group", count)
	profile.Members.Count = members
	profile.Shard.Mode = "hash"
	return scenarioWithProfilesAndTraffic(profile, traffic("many-group-send", "many-group", rate))
}

func scenarioWithHugeGroup(members int, rate string) model.Scenario {
	profile := channelProfile("huge-group", "group", 1)
	profile.Members.Count = members
	profile.Shard.Mode = "split_members_and_traffic"
	return scenarioWithProfilesAndTraffic(profile, traffic("huge-group-send", "huge-group", rate))
}

func scenarioWithProfiles(profiles ...model.ChannelProfile) model.Scenario {
	return scenarioWithProfilesAndTraffic(profiles[0], nilTraffic(), profiles[1:]...)
}

func scenarioWithProfilesAndTraffic(profile model.ChannelProfile, firstTraffic model.TrafficConfig, extraProfiles ...model.ChannelProfile) model.Scenario {
	profiles := append([]model.ChannelProfile{profile}, extraProfiles...)
	scenario := model.Scenario{
		Version:  "wkbench/v1",
		Run:      model.RunConfig{ID: "run-1"},
		Online:   model.OnlineConfig{TotalUsers: 1000000},
		Channels: model.ChannelsConfig{Profiles: profiles},
	}
	if firstTraffic.Name != "" {
		scenario.Messages.Traffic = []model.TrafficConfig{firstTraffic}
	}
	return scenario
}

func channelProfile(name, channelType string, count int) model.ChannelProfile {
	return model.ChannelProfile{Name: name, ChannelType: channelType, Count: count}
}

func traffic(name, channelRef, rate string) model.TrafficConfig {
	parsed, err := model.ParseRate(rate)
	if err != nil {
		panic(err)
	}
	return model.TrafficConfig{Name: name, ChannelRef: channelRef, RatePerChannel: parsed}
}

func nilTraffic() model.TrafficConfig { return model.TrafficConfig{} }
