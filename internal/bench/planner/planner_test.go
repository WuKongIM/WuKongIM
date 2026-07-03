package planner

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/stretchr/testify/require"
)

func TestBuildRejectsPersonProfilesExceedingIdentityPool(t *testing.T) {
	scenario := scenarioWithPersonCount(51)
	scenario.Online.TotalUsers = 100
	workers := []model.Worker{{ID: "a", Weight: 1}}

	_, err := Build(scenario, workers)

	require.ErrorContains(t, err, "person profiles require 102 distinct participants")
}

func TestBuildAllowsDefaultGroupMemberOverlapWithinSharedIdentityPool(t *testing.T) {
	scenario := scenarioWithManyGroup(2, 60, "1/s")
	scenario.Online.TotalUsers = 100
	workers := []model.Worker{{ID: "a", Weight: 1}}

	plan, err := Build(scenario, workers)

	require.NoError(t, err)
	shard := plan.Workers["a"].Profiles["many-group"]
	require.Equal(t, model.Range{Start: 0, End: 100}, shard.MemberRange)
	require.Equal(t, "allowed", shard.MemberReusePolicy)
}

func TestBuildHandlesZeroCountGroupWithMembersWithoutPanic(t *testing.T) {
	scenario := scenarioWithManyGroup(0, 60, "1/s")
	scenario.Online.TotalUsers = 100
	workers := []model.Worker{{ID: "a", Weight: 1}}

	require.NotPanics(t, func() {
		plan, err := Build(scenario, workers)
		require.NoError(t, err)
		require.Equal(t, model.Range{}, plan.Workers["a"].Profiles["many-group"].ChannelRange)
	})
}

func TestPlanIdentityRangeByWorkerWeight(t *testing.T) {
	scenario := scenarioWithManyGroup(1, 10, "1/s")
	scenario.Online.TotalUsers = 100
	workers := []model.Worker{{ID: "a", Weight: 1}, {ID: "b", Weight: 1}, {ID: "c", Weight: 2}}

	plan, err := Build(scenario, workers)

	require.NoError(t, err)
	require.Equal(t, model.Range{Start: 0, End: 25}, plan.Workers["a"].IdentityRange)
	require.Equal(t, model.Range{Start: 25, End: 50}, plan.Workers["b"].IdentityRange)
	require.Equal(t, model.Range{Start: 50, End: 100}, plan.Workers["c"].IdentityRange)
}

func TestBuildRejectsDisallowedOverlapGroupsWhenSharedIdentityPoolTooSmall(t *testing.T) {
	small := channelProfile("small-groups", "group", 50)
	small.Members.Count = 100
	small.Members.Overlap = "disallowed"
	huge := channelProfile("huge-group", "group", 1)
	huge.Members.Count = 10000
	huge.Members.Overlap = "disallowed"
	huge.Shard.Mode = "split_members_and_traffic"
	scenario := scenarioWithProfiles(small, huge)
	scenario.Online.TotalUsers = 14999
	workers := []model.Worker{{ID: "a", Weight: 1}, {ID: "b", Weight: 1}, {ID: "c", Weight: 2}}

	_, err := Build(scenario, workers)

	require.ErrorContains(t, err, "group profiles require 15000 distinct members")
}

func TestBuildRejectsPersonAndGroupReservationsExceedingSharedIdentityPool(t *testing.T) {
	person := channelProfile("person-chat", "person", 100)
	group := channelProfile("group-chat", "group", 1)
	group.Members.Count = 100
	group.Members.Overlap = "disallowed"
	scenario := scenarioWithProfiles(person, group)
	scenario.Online.TotalUsers = 299
	workers := []model.Worker{{ID: "a", Weight: 1}}

	_, err := Build(scenario, workers)

	require.ErrorContains(t, err, "channel profiles require 300 distinct generated users")
}

func TestPlanMixedNoOverlapGroupsWhenIdentityPoolIsSufficient(t *testing.T) {
	small := channelProfile("small-groups", "group", 50)
	small.Members.Count = 100
	small.Members.Overlap = "disallowed"
	huge := channelProfile("huge-group", "group", 1)
	huge.Members.Count = 10000
	huge.Members.Overlap = "disallowed"
	huge.Shard.Mode = "split_members_and_traffic"
	scenario := scenarioWithProfiles(small, huge)
	scenario.Online.TotalUsers = 15000
	workers := []model.Worker{{ID: "a", Weight: 1}, {ID: "b", Weight: 1}, {ID: "c", Weight: 2}}

	plan, err := Build(scenario, workers)

	require.NoError(t, err)
	require.Equal(t, model.Range{Start: 0, End: 1300}, plan.Workers["a"].Profiles["small-groups"].MemberRange)
	require.Equal(t, model.Range{Start: 1300, End: 2500}, plan.Workers["b"].Profiles["small-groups"].MemberRange)
	require.Equal(t, model.Range{Start: 2500, End: 5000}, plan.Workers["c"].Profiles["small-groups"].MemberRange)
	require.Equal(t, model.Range{Start: 5000, End: 7500}, plan.Workers["a"].Profiles["huge-group"].MemberRange)
	require.Equal(t, model.Range{Start: 7500, End: 10000}, plan.Workers["b"].Profiles["huge-group"].MemberRange)
	require.Equal(t, model.Range{Start: 10000, End: 15000}, plan.Workers["c"].Profiles["huge-group"].MemberRange)
}

func TestBuildAllowsPersonAndDefaultGroupOverlapAtPoolBoundary(t *testing.T) {
	person := channelProfile("person-chat", "person", 100)
	group := channelProfile("group-chat", "group", 1)
	group.Members.Count = 100
	scenario := scenarioWithProfiles(person, group)
	scenario.Online.TotalUsers = 200
	workers := []model.Worker{{ID: "a", Weight: 1}}

	plan, err := Build(scenario, workers)

	require.NoError(t, err)
	require.Equal(t, model.Range{Start: 0, End: 200}, plan.Workers["a"].Profiles["person-chat"].ParticipantRange)
	require.Equal(t, model.Range{Start: 0, End: 200}, plan.Workers["a"].Profiles["group-chat"].MemberRange)
	require.Equal(t, "allowed", plan.Workers["a"].Profiles["group-chat"].MemberReusePolicy)
}

func TestBuildRejectsAllowedGroupMembersExceedingPool(t *testing.T) {
	group := channelProfile("group-chat", "group", 1)
	group.Members.Count = 101
	group.Members.Overlap = "allowed"
	scenario := scenarioWithProfiles(group)
	scenario.Online.TotalUsers = 100
	workers := []model.Worker{{ID: "a", Weight: 1}}

	_, err := Build(scenario, workers)

	require.ErrorContains(t, err, "requires 101 members per channel")
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
	require.Equal(t, model.Range{Start: 0, End: 3}, plan.Workers["a"].Profiles["many-group"].ChannelRange)
	require.Equal(t, model.Range{Start: 3, End: 5}, plan.Workers["b"].Profiles["many-group"].ChannelRange)
	require.Equal(t, model.Range{Start: 5, End: 10}, plan.Workers["c"].Profiles["many-group"].ChannelRange)
	require.Equal(t, 2.0, plan.Workers["a"].Profiles["many-group"].GlobalRate.PerSecond)
	require.Equal(t, 2.0, plan.Workers["c"].Profiles["many-group"].LocalRate.PerSecond)
	require.Equal(t, plan.ChannelOwners["many-group"], repeat.ChannelOwners["many-group"])
	require.Len(t, plan.ChannelOwners["many-group"], 10)
	for channelIndex, ownerID := range plan.ChannelOwners["many-group"] {
		require.Contains(t, map[string]struct{}{"a": {}, "b": {}, "c": {}}, ownerID, "channel %d", channelIndex)
	}
}

func TestPlanManyGroupUsesLargestRemainderWeightedRanges(t *testing.T) {
	scenario := scenarioWithManyGroup(50, 100, "2/s")
	workers := []model.Worker{{ID: "a", Weight: 1}, {ID: "b", Weight: 1}, {ID: "c", Weight: 2}}

	plan, err := Build(scenario, workers)

	require.NoError(t, err)
	require.Equal(t, model.Range{Start: 0, End: 13}, plan.Workers["a"].Profiles["many-group"].ChannelRange)
	require.Equal(t, model.Range{Start: 13, End: 25}, plan.Workers["b"].Profiles["many-group"].ChannelRange)
	require.Equal(t, model.Range{Start: 25, End: 50}, plan.Workers["c"].Profiles["many-group"].ChannelRange)
}

func TestPlanGroupOwnersRespectWorkerWeights(t *testing.T) {
	scenario := scenarioWithManyGroup(400, 100, "2/s")
	workers := []model.Worker{{ID: "low", Weight: 1}, {ID: "high", Weight: 3}}

	plan, err := Build(scenario, workers)

	require.NoError(t, err)
	counts := ownerCounts(plan.ChannelOwners["many-group"])
	require.Greater(t, counts["high"], counts["low"]*2)
}

func TestPlanGroupOwnersIncludeRunIDInStableHash(t *testing.T) {
	scenario := scenarioWithManyGroup(20, 100, "2/s")
	workers := []model.Worker{{ID: "a", Weight: 1}, {ID: "b", Weight: 1}}

	first, err := Build(scenario, workers)
	require.NoError(t, err)
	scenario.Run.ID = "run-2"
	second, err := Build(scenario, workers)
	require.NoError(t, err)

	require.NotEqual(t, first.ChannelOwners["many-group"], second.ChannelOwners["many-group"])
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
	require.Equal(t, 4, plan.Workers["a"].Profiles["huge-group"].TrafficPartitionCount)
	require.Equal(t, 4, plan.Workers["c"].Profiles["huge-group"].TrafficPartitionCount)
	require.Equal(t, []int{0}, plan.Workers["a"].Profiles["huge-group"].OwnedTrafficPartitions)
	require.Equal(t, []int{2, 3}, plan.Workers["c"].Profiles["huge-group"].OwnedTrafficPartitions)
	require.Equal(t, 20.0, plan.Workers["a"].Profiles["huge-group"].GlobalRate.PerSecond)
	require.InDelta(t, 5.0, plan.Workers["a"].Profiles["huge-group"].LocalRate.PerSecond, 0.001)
	require.InDelta(t, 10.0, plan.Workers["c"].Profiles["huge-group"].LocalRate.PerSecond, 0.001)
}

func TestPlanHugeGroupScaledWeightsKeepSameTrafficPartitions(t *testing.T) {
	scenario := scenarioWithHugeGroup(10000, "20/s")
	workers := []model.Worker{{ID: "a", Weight: 0.25}, {ID: "b", Weight: 0.25}, {ID: "c", Weight: 0.5}}

	plan, err := Build(scenario, workers)

	require.NoError(t, err)
	require.Equal(t, 4, plan.Workers["a"].Profiles["huge-group"].TrafficPartitionCount)
	require.Equal(t, []int{0}, plan.Workers["a"].Profiles["huge-group"].OwnedTrafficPartitions)
	require.Equal(t, []int{2, 3}, plan.Workers["c"].Profiles["huge-group"].OwnedTrafficPartitions)
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
		{
			name: "negative online users",
			scenario: func() model.Scenario {
				scenario := scenarioWithManyGroup(1, 10, "1/s")
				scenario.Online.TotalUsers = -1
				return scenario
			}(),
			workers: []model.Worker{{ID: "a", Weight: 1}},
			wantErr: "online.total_users must not be negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Build(tt.scenario, tt.workers)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestBuildValidatesTrafficRefs(t *testing.T) {
	tests := []struct {
		name    string
		traffic model.TrafficConfig
		wantErr string
	}{
		{
			name:    "empty channel ref",
			traffic: traffic("bad", "", "1/s"),
			wantErr: "messages.traffic[0].channel_ref is required",
		},
		{
			name:    "unknown channel ref",
			traffic: traffic("bad", "missing", "1/s"),
			wantErr: "messages.traffic[0].channel_ref \"missing\" does not match a channel profile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scenario := scenarioWithManyGroup(1, 10, "1/s")
			scenario.Messages.Traffic = []model.TrafficConfig{tt.traffic}

			_, err := Build(scenario, []model.Worker{{ID: "a", Weight: 1}})

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

func ownerCounts(owners map[int]string) map[string]int {
	counts := make(map[string]int)
	for _, owner := range owners {
		counts[owner]++
	}
	return counts
}
