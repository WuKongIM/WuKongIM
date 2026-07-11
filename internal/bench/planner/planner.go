package planner

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
)

// Build validates a wkbench scenario and returns a deterministic per-worker shard plan.
func Build(s model.Scenario, workers []model.Worker) (model.Plan, error) {
	if err := validateWorkers(workers); err != nil {
		return model.Plan{}, err
	}
	if s.Online.TotalUsers < 0 {
		return model.Plan{}, fmt.Errorf("online.total_users must not be negative")
	}
	profilesByName, profileOrder, personParticipants, err := validateProfiles(s.Channels.Profiles)
	if err != nil {
		return model.Plan{}, err
	}
	if personParticipants > s.Online.TotalUsers {
		return model.Plan{}, fmt.Errorf("person profiles require %d distinct participants, but online.total_users is %d", personParticipants, s.Online.TotalUsers)
	}
	identityRanges, err := profileIdentityRanges(profileOrder, profilesByName, s.Online.TotalUsers, personParticipants)
	if err != nil {
		return model.Plan{}, err
	}

	globalRates, err := ratesByProfile(s.Messages.Traffic, profilesByName)
	if err != nil {
		return model.Plan{}, err
	}
	plan := model.Plan{
		RunID:         s.Run.ID,
		Workers:       make(map[string]model.WorkerPlan, len(workers)),
		WorkerOrder:   make([]string, 0, len(workers)),
		ProfileOrder:  profileOrder,
		IdentityPool:  model.Range{Start: 0, End: s.Online.TotalUsers},
		ChannelOwners: make(map[string]map[int]string),
	}
	for idx, worker := range workers {
		workerID := strings.TrimSpace(worker.ID)
		plan.WorkerOrder = append(plan.WorkerOrder, workerID)
		plan.Workers[workerID] = model.WorkerPlan{
			WorkerID:      workerID,
			IdentityRange: weightedRange(s.Online.TotalUsers, workers, idx),
			Profiles:      make(map[string]model.ProfileShard, len(s.Channels.Profiles)),
		}
	}
	for _, worker := range workers {
		if worker.TCPSource == nil {
			continue
		}
		workerID := strings.TrimSpace(worker.ID)
		if err := model.ValidateTCPSourceConfig(worker.TCPSource); err != nil {
			return model.Plan{}, fmt.Errorf("worker %q tcp source: %w", workerID, err)
		}
		capacity := model.TCPSourceCapacity(worker.TCPSource)
		identityCount := plan.Workers[workerID].IdentityRange.Len()
		if capacity < int64(identityCount) {
			return model.Plan{}, fmt.Errorf("worker %q tcp source capacity %d is smaller than identity range %d", workerID, capacity, identityCount)
		}
	}

	for _, profileName := range profileOrder {
		profile := profilesByName[profileName]
		globalRate := globalRates[profileName]
		identityRange := identityRanges[profileName]
		if profile.ChannelType == model.ChannelTypeGroup {
			plan.ChannelOwners[profileName] = channelOwners(s.Run.ID, profileName, profile.Count, workers)
		}

		for idx, worker := range workers {
			workerID := strings.TrimSpace(worker.ID)
			shard := model.ProfileShard{
				Name:        profile.Name,
				ChannelType: profile.ChannelType,
				GlobalRate:  globalRate,
				LocalRate:   globalRate,
			}

			switch profile.ChannelType {
			case model.ChannelTypePerson:
				channelRange := weightedRange(profile.Count, workers, idx)
				shard.ChannelRange = channelRange
				shard.ParticipantRange = model.Range{
					Start: identityRange.Participant.Start + channelRange.Start*2,
					End:   identityRange.Participant.Start + channelRange.End*2,
				}
			case model.ChannelTypeGroup:
				shard.MemberReusePolicy = groupMemberReusePolicy(profile)
				if profile.Shard.Mode == model.ShardModeSplitMembersAndTraffic {
					if profile.Count != 1 {
						return model.Plan{}, fmt.Errorf("profile %q uses %s and must have exactly one channel", profile.Name, model.ShardModeSplitMembersAndTraffic)
					}
					memberRange := weightedRange(profile.Members.Count, workers, idx)
					memberRange.Start += identityRange.Members.Start
					memberRange.End += identityRange.Members.Start
					totalPartitions := trafficPartitionTotal(workers)
					partitionRange := weightedRange(totalPartitions, workers, idx)
					shard.ChannelRange = model.Range{Start: 0, End: profile.Count}
					shard.MemberRange = memberRange
					shard.TrafficPartitionCount = totalPartitions
					shard.OwnedTrafficPartitions = indexesInRange(partitionRange)
					shard.LocalRate = model.Rate{PerSecond: globalRate.PerSecond * float64(partitionRange.Len()) / float64(totalPartitions)}
				} else {
					shard.ChannelRange = weightedRange(profile.Count, workers, idx)
					if groupMembersRequireNoReuse(profile) {
						shard.MemberRange = model.Range{
							Start: identityRange.Members.Start + shard.ChannelRange.Start*profile.Members.Count,
							End:   identityRange.Members.Start + shard.ChannelRange.End*profile.Members.Count,
						}
					} else {
						shard.MemberRange = identityRange.Members
					}
				}
			}

			workerPlan := plan.Workers[workerID]
			workerPlan.Profiles[profile.Name] = shard
			plan.Workers[workerID] = workerPlan
		}

	}

	return plan, nil
}

func validateWorkers(workers []model.Worker) error {
	if len(workers) == 0 {
		return fmt.Errorf("at least one worker is required")
	}
	seen := make(map[string]struct{}, len(workers))
	for _, worker := range workers {
		workerID := strings.TrimSpace(worker.ID)
		if workerID == "" {
			return fmt.Errorf("worker id is required")
		}
		if _, ok := seen[workerID]; ok {
			return fmt.Errorf("duplicate worker id %q", workerID)
		}
		seen[workerID] = struct{}{}
		if worker.Weight <= 0 || math.IsNaN(worker.Weight) || math.IsInf(worker.Weight, 0) {
			return fmt.Errorf("worker %q weight must be greater than zero", workerID)
		}
	}
	return nil
}

func validateProfiles(profiles []model.ChannelProfile) (map[string]model.ChannelProfile, []string, int, error) {
	byName := make(map[string]model.ChannelProfile, len(profiles))
	order := make([]string, 0, len(profiles))
	personParticipants := 0
	for _, profile := range profiles {
		profileName := strings.TrimSpace(profile.Name)
		if profileName == "" {
			return nil, nil, 0, fmt.Errorf("channel profile name is required")
		}
		if _, ok := byName[profileName]; ok {
			return nil, nil, 0, fmt.Errorf("duplicate channel profile name %q", profileName)
		}
		profile.Name = profileName
		if profile.Count < 0 {
			return nil, nil, 0, fmt.Errorf("profile %q count must not be negative", profile.Name)
		}
		if profile.Members.Count < 0 {
			return nil, nil, 0, fmt.Errorf("profile %q members.count must not be negative", profile.Name)
		}
		overlap := strings.TrimSpace(profile.Members.Overlap)
		if overlap != "" && overlap != "allowed" && overlap != "disallowed" {
			return nil, nil, 0, fmt.Errorf("profile %q members.overlap must be allowed or disallowed", profile.Name)
		}

		switch profile.ChannelType {
		case model.ChannelTypePerson:
			if profile.Count > (math.MaxInt-personParticipants)/2 {
				return nil, nil, 0, fmt.Errorf("profile %q person participant count overflows int", profile.Name)
			}
			personParticipants += profile.Count * 2
		case model.ChannelTypeGroup:
			if profile.Count > 0 && profile.Members.Count > 0 && profile.Members.Count > math.MaxInt/profile.Count {
				return nil, nil, 0, fmt.Errorf("profile %q group member count overflows int", profile.Name)
			}
		default:
			return nil, nil, 0, fmt.Errorf("profile %q uses unsupported channel type %q", profile.Name, profile.ChannelType)
		}
		byName[profileName] = profile
		order = append(order, profileName)
	}
	return byName, order, personParticipants, nil
}

type profileIdentityRange struct {
	Participant model.Range
	Members     model.Range
}

func profileIdentityRanges(profileOrder []string, profiles map[string]model.ChannelProfile, totalUsers, personParticipants int) (map[string]profileIdentityRange, error) {
	ranges := make(map[string]profileIdentityRange, len(profileOrder))
	cursor := 0
	for _, profileName := range profileOrder {
		profile := profiles[profileName]
		switch profile.ChannelType {
		case model.ChannelTypePerson:
			participantSpan := profile.Count * 2
			ranges[profile.Name] = profileIdentityRange{
				Participant: model.Range{Start: cursor, End: cursor + participantSpan},
			}
			cursor += participantSpan
		case model.ChannelTypeGroup:
			memberSpan, err := groupMemberSpan(profile)
			if err != nil {
				return nil, err
			}
			if profile.Members.Count > totalUsers {
				return nil, fmt.Errorf("group profile %q requires %d members per channel, but online.total_users is %d", profile.Name, profile.Members.Count, totalUsers)
			}
			memberBase := 0
			if groupMembersRequireNoReuse(profile) {
				memberBase = cursor
				cursor += memberSpan
			}
			ranges[profile.Name] = profileIdentityRange{
				Members: model.Range{Start: memberBase, End: memberBase + groupMemberPoolSpan(profile, memberSpan, totalUsers)},
			}
		}
	}
	if cursor > totalUsers {
		if personParticipants > 0 {
			return nil, fmt.Errorf("channel profiles require %d distinct generated users, but online.total_users is %d", cursor, totalUsers)
		}
		return nil, fmt.Errorf("group profiles require %d distinct members, but online.total_users is %d", cursor, totalUsers)
	}
	return ranges, nil
}

func groupMemberSpan(profile model.ChannelProfile) (int, error) {
	if profile.Shard.Mode == model.ShardModeSplitMembersAndTraffic {
		if profile.Count != 1 {
			return 0, nil
		}
		return profile.Members.Count, nil
	}
	if profile.Members.Count == 0 || profile.Count == 0 {
		return 0, nil
	}
	if profile.Count > math.MaxInt/profile.Members.Count {
		return 0, fmt.Errorf("profile %q group member count overflows int", profile.Name)
	}
	return profile.Count * profile.Members.Count, nil
}

func groupMembersRequireNoReuse(profile model.ChannelProfile) bool {
	return strings.TrimSpace(profile.Members.Overlap) == "disallowed"
}

func groupMemberPoolSpan(profile model.ChannelProfile, reservedSpan, totalUsers int) int {
	if groupMembersRequireNoReuse(profile) {
		return reservedSpan
	}
	if profile.Members.Count == 0 {
		return 0
	}
	return totalUsers
}

func groupMemberReusePolicy(profile model.ChannelProfile) string {
	if groupMembersRequireNoReuse(profile) {
		return "disallowed"
	}
	return "allowed"
}

func ratesByProfile(traffic []model.TrafficConfig, profiles map[string]model.ChannelProfile) (map[string]model.Rate, error) {
	rates := make(map[string]model.Rate, len(traffic))
	for idx, item := range traffic {
		channelRef := strings.TrimSpace(item.ChannelRef)
		if channelRef == "" {
			return nil, fmt.Errorf("messages.traffic[%d].channel_ref is required", idx)
		}
		if _, ok := profiles[channelRef]; !ok {
			return nil, fmt.Errorf("messages.traffic[%d].channel_ref %q does not match a channel profile", idx, channelRef)
		}
		current := rates[channelRef]
		current.PerSecond += item.RatePerChannel.PerSecond
		rates[channelRef] = current
	}
	return rates, nil
}

func weightedRange(count int, workers []model.Worker, idx int) model.Range {
	allocations := weightedAllocations(count, workers)
	start := 0
	for i := 0; i < idx; i++ {
		start += allocations[i]
	}
	return model.Range{Start: start, End: start + allocations[idx]}
}

func weightedAllocations(count int, workers []model.Worker) []int {
	allocations := make([]int, len(workers))
	if count <= 0 {
		return allocations
	}
	totalWeight := 0.0
	for _, worker := range workers {
		totalWeight += worker.Weight
	}
	type remainder struct {
		idx   int
		value float64
	}
	remainders := make([]remainder, 0, len(workers))
	allocated := 0
	for idx, worker := range workers {
		exact := float64(count) * worker.Weight / totalWeight
		base := int(math.Floor(exact))
		allocations[idx] = base
		allocated += base
		remainders = append(remainders, remainder{idx: idx, value: exact - float64(base)})
	}
	sort.SliceStable(remainders, func(i, j int) bool {
		return remainders[i].value > remainders[j].value
	})
	for remaining := count - allocated; remaining > 0; remaining-- {
		allocations[remainders[0].idx]++
		remainders = remainders[1:]
	}
	return allocations
}

func trafficPartitionTotal(workers []model.Worker) int {
	best := len(workers)
	for partitions := len(workers); partitions <= len(workers)*1000; partitions++ {
		allocations := weightedAllocations(partitions, workers)
		allPositive := true
		for _, allocation := range allocations {
			if allocation == 0 {
				allPositive = false
				break
			}
		}
		if !allPositive {
			continue
		}
		if partitions > best {
			best = partitions
		}
		if allocationsApproximateWeights(workers, allocations) {
			return partitions
		}
	}
	return best
}

func allocationsApproximateWeights(workers []model.Worker, allocations []int) bool {
	totalWeight := 0.0
	totalAllocations := 0
	for idx, worker := range workers {
		totalWeight += worker.Weight
		totalAllocations += allocations[idx]
	}
	for idx, worker := range workers {
		if math.Abs(float64(totalAllocations)*worker.Weight/totalWeight-float64(allocations[idx])) > 1e-9 {
			return false
		}
	}
	return true
}

func indexesInRange(r model.Range) []int {
	indexes := make([]int, 0, r.Len())
	for i := r.Start; i < r.End; i++ {
		indexes = append(indexes, i)
	}
	return indexes
}

func channelOwners(runID, profileName string, count int, workers []model.Worker) map[int]string {
	owners := make(map[int]string, count)
	if count <= 0 {
		return owners
	}
	totalWeight := 0.0
	for _, worker := range workers {
		totalWeight += worker.Weight
	}
	for channelIndex := 0; channelIndex < count; channelIndex++ {
		owners[channelIndex] = ownerForChannel(runID, profileName, channelIndex, workers, totalWeight)
	}
	return owners
}

func ownerForChannel(runID, profileName string, channelIndex int, workers []model.Worker, totalWeight float64) string {
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%s/%s/%d", runID, profileName, channelIndex)
	point := float64(h.Sum32()) / float64(math.MaxUint32) * totalWeight
	cumulative := 0.0
	for _, worker := range workers {
		cumulative += worker.Weight
		if point < cumulative {
			return strings.TrimSpace(worker.ID)
		}
	}
	return strings.TrimSpace(workers[len(workers)-1].ID)
}
