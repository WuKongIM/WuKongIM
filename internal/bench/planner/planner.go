package planner

import (
	"fmt"
	"hash/fnv"
	"math"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/model"
)

// Build validates a wkbench scenario and returns a deterministic per-worker shard plan.
func Build(s model.Scenario, workers []model.Worker) (model.Plan, error) {
	if err := validateWorkers(workers); err != nil {
		return model.Plan{}, err
	}
	profilesByName, profileOrder, personParticipants, err := validateProfiles(s.Channels.Profiles)
	if err != nil {
		return model.Plan{}, err
	}
	if personParticipants > s.Online.TotalUsers {
		return model.Plan{}, fmt.Errorf("person profiles require %d distinct participants, but online.total_users is %d", personParticipants, s.Online.TotalUsers)
	}

	globalRates := ratesByProfile(s.Messages.Traffic)
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
			plan.ChannelOwners[profileName] = channelOwners(profileName, profile.Count, workers)
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
					shard.TrafficPartitionCount = partitionRange.Len()
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
		switch profile.ChannelType {
		case model.ChannelTypePerson:
			personParticipants += profile.Count * 2
		case model.ChannelTypeGroup:
		default:
			return nil, nil, 0, fmt.Errorf("profile %q uses unsupported channel type %q", profile.Name, profile.ChannelType)
		}
		if profile.Count < 0 {
			return nil, nil, 0, fmt.Errorf("profile %q count must not be negative", profile.Name)
		}
		if profile.Members.Count < 0 {
			return nil, nil, 0, fmt.Errorf("profile %q members.count must not be negative", profile.Name)
		}
		byName[profileName] = profile
		order = append(order, profileName)
	}
	return byName, order, personParticipants, nil
}

func ratesByProfile(traffic []model.TrafficConfig) map[string]model.Rate {
	rates := make(map[string]model.Rate, len(traffic))
	for _, item := range traffic {
		if item.ChannelRef == "" {
			continue
		}
		current := rates[item.ChannelRef]
		current.PerSecond += item.RatePerChannel.PerSecond
		rates[item.ChannelRef] = current
	}
	return rates
}

func weightedRange(count int, workers []model.Worker, idx int) model.Range {
	if count <= 0 {
		return model.Range{}
	}
	totalWeight := 0.0
	for _, worker := range workers {
		totalWeight += worker.Weight
	}
	prefixWeight := 0.0
	for i := 0; i < idx; i++ {
		prefixWeight += workers[i].Weight
	}
	start := int(math.Floor(float64(count) * prefixWeight / totalWeight))
	end := count
	if idx < len(workers)-1 {
		end = int(math.Floor(float64(count) * (prefixWeight + workers[idx].Weight) / totalWeight))
	}
	return model.Range{Start: start, End: end}
}

func trafficPartitionTotal(workers []model.Worker) int {
	totalWeight := 0.0
	for _, worker := range workers {
		totalWeight += worker.Weight
	}
	partitions := int(math.Round(totalWeight))
	if partitions < len(workers) {
		return len(workers)
	}
	return partitions
}

func indexesInRange(r model.Range) []int {
	indexes := make([]int, 0, r.Len())
	for i := r.Start; i < r.End; i++ {
		indexes = append(indexes, i)
	}
	return indexes
}

func channelOwners(profileName string, count int, workers []model.Worker) map[int]string {
	owners := make(map[int]string, count)
	if count <= 0 {
		return owners
	}
	for channelIndex := 0; channelIndex < count; channelIndex++ {
		owners[channelIndex] = ownerForChannel(profileName, channelIndex, workers)
	}
	return owners
}

func ownerForChannel(profileName string, channelIndex int, workers []model.Worker) string {
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%s/%d", profileName, channelIndex)
	return strings.TrimSpace(workers[int(h.Sum32())%len(workers)].ID)
}
