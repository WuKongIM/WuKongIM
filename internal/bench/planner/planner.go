package planner

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
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
	for _, worker := range workers {
		workerID := strings.TrimSpace(worker.ID)
		plan.WorkerOrder = append(plan.WorkerOrder, workerID)
		plan.Workers[workerID] = model.WorkerPlan{
			WorkerID: workerID,
			Profiles: make(map[string]model.ProfileShard, len(s.Channels.Profiles)),
		}
	}

	personParticipantOffset := 0
	for _, profileName := range profileOrder {
		profile := profilesByName[profileName]
		globalRate := globalRates[profileName]
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
					Start: personParticipantOffset + channelRange.Start*2,
					End:   personParticipantOffset + channelRange.End*2,
				}
			case model.ChannelTypeGroup:
				if profile.Shard.Mode == model.ShardModeSplitMembersAndTraffic {
					if profile.Count != 1 {
						return model.Plan{}, fmt.Errorf("profile %q uses %s and must have exactly one channel", profile.Name, model.ShardModeSplitMembersAndTraffic)
					}
					memberRange := weightedRange(profile.Members.Count, workers, idx)
					totalPartitions := trafficPartitionTotal(workers)
					partitionRange := weightedRange(totalPartitions, workers, idx)
					shard.ChannelRange = model.Range{Start: 0, End: profile.Count}
					shard.MemberRange = memberRange
					shard.TrafficPartitionCount = totalPartitions
					shard.OwnedTrafficPartitions = indexesInRange(partitionRange)
					shard.LocalRate = model.Rate{PerSecond: globalRate.PerSecond * float64(partitionRange.Len()) / float64(totalPartitions)}
				} else {
					shard.ChannelRange = weightedRange(profile.Count, workers, idx)
				}
			}

			workerPlan := plan.Workers[workerID]
			workerPlan.Profiles[profile.Name] = shard
			plan.Workers[workerID] = workerPlan
		}

		if profile.ChannelType == model.ChannelTypePerson {
			personParticipantOffset += profile.Count * 2
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

		switch profile.ChannelType {
		case model.ChannelTypePerson:
			if profile.Count > (math.MaxInt-personParticipants)/2 {
				return nil, nil, 0, fmt.Errorf("profile %q person participant count overflows int", profile.Name)
			}
			personParticipants += profile.Count * 2
		case model.ChannelTypeGroup:
		default:
			return nil, nil, 0, fmt.Errorf("profile %q uses unsupported channel type %q", profile.Name, profile.ChannelType)
		}
		byName[profileName] = profile
		order = append(order, profileName)
	}
	return byName, order, personParticipants, nil
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
