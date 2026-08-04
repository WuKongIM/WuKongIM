package chatlifecycle

import (
	"fmt"
	"math"
	"math/bits"
	"time"
)

func validateWorkload(w WorkloadConfig, profile Profile) error {
	if w.Workers <= 0 {
		return fieldError("workload.workers", "must be greater than zero")
	}
	if w.OnlineUsers <= 0 {
		return fieldError("workload.online_users", "must be greater than zero")
	}
	if w.NewUsersPerDay <= 0 {
		return fieldError("workload.new_users_per_day", "must be greater than zero")
	}
	if w.SendRatePerSecond <= 0 {
		return fieldError("workload.send_rate_per_second", "must be greater than zero")
	}
	if w.HotSet.PersonChannels <= 0 {
		return fieldError("workload.hot_set.person_channels", "must be greater than zero")
	}
	if w.HotSet.GroupChannels <= 0 {
		return fieldError("workload.hot_set.group_channels", "must be greater than zero")
	}
	if w.Topology.LogicalSlotGroups <= 0 {
		return fieldError("workload.topology.logical_slot_groups", "must be greater than zero")
	}
	if w.Topology.HashSlots <= 0 {
		return fieldError("workload.topology.hash_slots", "must be greater than zero")
	}
	if w.Topology.SlotReplicas <= 0 {
		return fieldError("workload.topology.slot_replicas", "must be greater than zero")
	}
	if w.Topology.ChannelReplicas <= 0 {
		return fieldError("workload.topology.channel_replicas", "must be greater than zero")
	}
	if w.RuntimeSampling.Every <= 0 {
		return fieldError("workload.runtime_sampling.every", "must be greater than zero")
	}
	if w.RuntimeSampling.Size <= 0 {
		return fieldError("workload.runtime_sampling.size", "must be greater than zero")
	}
	if w.RuntimeSampling.Size > formalRuntimeSampleSize {
		return fieldError("workload.runtime_sampling.size", "must not exceed 1200")
	}
	if w.Sync.Limit <= 0 {
		return fieldError("workload.sync.limit", "must be greater than zero")
	}
	if w.Sync.MessageCount <= 0 {
		return fieldError("workload.sync.message_count", "must be greater than zero")
	}
	if w.Sync.Version != 0 {
		return fieldError("workload.sync.version", "must equal 0 for real sync")
	}
	if w.Sync.Limit != formalSyncLimit {
		return fieldError("workload.sync.limit", "must equal 500 for real sync")
	}
	if w.Sync.MessageCount != formalSyncMessageCount {
		return fieldError("workload.sync.message_count", "must equal 20 for real sync")
	}
	if w.BurstCredit <= 0 {
		return fieldError("workload.burst_credit", "must be greater than zero")
	}
	if w.MaxGlobalBurst <= 0 {
		return fieldError("workload.max_global_burst", "must be greater than zero")
	}
	productHigh, productLow := bits.Mul64(uint64(w.BurstCredit), uint64(w.SendRatePerSecond))
	nanosecondsPerSecond := uint64(time.Second)
	if productHigh >= nanosecondsPerSecond {
		return fieldError("workload.max_global_burst", "burst calculation exceeds supported range")
	}
	expectedBurst64, remainder := bits.Div64(productHigh, productLow, nanosecondsPerSecond)
	if remainder != 0 {
		return fieldError("workload.max_global_burst", "burst calculation must produce an integral message count")
	}
	if expectedBurst64 > uint64(math.MaxInt) {
		return fieldError("workload.max_global_burst", "burst calculation exceeds supported range")
	}
	expectedBurst := int(expectedBurst64)
	if w.MaxGlobalBurst != expectedBurst {
		return fieldError("workload.max_global_burst", "must equal burst_credit times send_rate_per_second")
	}
	if w.MaxChannelsPerNode <= 0 {
		return fieldError("workload.max_channels_per_node", "must be greater than zero")
	}
	// The current planner assigns both active person and group hot-set channels
	// against this per-node allocation bound.
	if w.HotSet.PersonChannels > w.MaxChannelsPerNode || w.HotSet.GroupChannels > w.MaxChannelsPerNode {
		return fieldError("workload.max_channels_per_node", "must cover active person and group hot-set channels")
	}
	hotSetTotal, ok := checkedAddNonnegativeInt(w.HotSet.PersonChannels, w.HotSet.GroupChannels)
	if !ok || hotSetTotal > w.MaxChannelsPerNode {
		return fieldError("workload.max_channels_per_node", "must cover active person and group hot-set channels")
	}
	if err := validatePercentPair("workload.traffic", w.Traffic.PersonPercent, w.Traffic.GroupPercent); err != nil {
		return err
	}
	if err := validatePercentPair("workload.login", w.Login.NewPercent, w.Login.ReturningPercent); err != nil {
		return err
	}
	if err := validateDurationShares("workload.sessions", w.Sessions, true); err != nil {
		return err
	}
	if err := validateLifecycle(w.Lifecycle); err != nil {
		return err
	}
	if err := validatePayloads(w.Payloads); err != nil {
		return err
	}
	if err := validatePercentPair("workload.person_direction", w.PersonDirection.AlternatingPercent, w.PersonDirection.OneWayPercent); err != nil {
		return err
	}
	if err := validateIntRange("workload.relationship.initial_messages", w.Relationship.InitialMessages); err != nil {
		return err
	}
	if err := validateDurationRange("workload.relationship.initial_message_window", w.Relationship.InitialMessageWindow); err != nil {
		return err
	}
	if err := validateIntRange("workload.relationship.returning_messages", w.Relationship.ReturningMessages); err != nil {
		return err
	}
	if err := validatePercentPair("workload.relationship.returning_age", w.Relationship.ReturningLast24hPercent, w.Relationship.ReturningOlderPercent); err != nil {
		return err
	}
	if w.Retry.MaxCount < 0 {
		return fieldError("workload.retry.max_count", "must not be negative")
	}
	if w.Retry.MaxCount > 3 {
		return fieldError("workload.retry.max_count", "must not exceed 3")
	}
	if len(w.Retry.Delays) != 3 {
		return fieldError("workload.retry.delays", "must contain exactly 3 delays")
	}
	for i, delay := range w.Retry.Delays {
		if delay <= 0 {
			return fieldError(fmt.Sprintf("workload.retry.delays[%d]", i), "must be greater than zero")
		}
	}
	groupCategories := []struct {
		path  string
		count int
	}{
		{"workload.groups.small", w.Groups.Small},
		{"workload.groups.medium", w.Groups.Medium},
		{"workload.groups.large", w.Groups.Large},
		{"workload.groups.very_large", w.Groups.VeryLarge},
	}
	groupTotal := 0
	for _, category := range groupCategories {
		if category.count < 0 || category.count > formalGroupCatalogTotal {
			return fieldError(category.path, "must be in 0..2000")
		}
		var ok bool
		groupTotal, ok = checkedAddNonnegativeInt(groupTotal, category.count)
		if !ok {
			return fieldError("workload.groups", "catalog total must be in 1..2000")
		}
	}
	if groupTotal <= 0 || groupTotal > formalGroupCatalogTotal {
		return fieldError("workload.groups", "catalog total must be in 1..2000")
	}
	if profile == ProfileFormal && groupTotal != formalGroupCatalogTotal {
		return fieldError("workload.groups", "catalog counts must total 2000")
	}
	if profile == ProfileFormal && w.Groups.VeryLarge != 1 {
		return fieldError("workload.groups.very_large", "must equal 1")
	}
	if profile == ProfileFormal && w.Groups.VeryLargeMembers != formalVeryLargeMembers {
		return fieldError("workload.groups.very_large_members", "must equal 100000")
	}
	if w.HotSet.GroupChannels != groupTotal {
		return fieldError("workload.hot_set.group_channels", "must equal group catalog total")
	}
	if !w.Groups.FixedMembership {
		return fieldError("workload.groups.fixed_membership", "must be true")
	}
	if w.Groups.VeryLarge > 0 {
		if w.Groups.VeryLargeMembers <= 0 {
			return fieldError("workload.groups.very_large_members", "must be greater than zero when very_large is positive")
		}
		if w.Groups.VeryLargeSendEvery <= 0 {
			return fieldError("workload.groups.very_large_send_every", "must be greater than zero when very_large is positive")
		}
	} else {
		if w.Groups.VeryLargeMembers != 0 {
			return fieldError("workload.groups.very_large_members", "must be zero when very_large is zero")
		}
		if w.Groups.VeryLargeSendEvery != 0 {
			return fieldError("workload.groups.very_large_send_every", "must be zero when very_large is zero")
		}
	}
	if w.Topology.LogicalSlotGroups != formalLogicalSlotGroups || w.Topology.HashSlots != formalHashSlots || w.Topology.SlotReplicas != formalReplicas || w.Topology.ChannelReplicas != formalReplicas {
		return fieldError("workload.topology", "must preserve 12 logical slot groups, 256 hash slots, and 3 replicas")
	}
	return nil
}

func validatePercentPair(path string, first, second int) error {
	if first < 0 || first > 100 || second < 0 || second > 100 {
		return fieldError(path, "percentages must be in 0..100")
	}
	// Validate both shares before adding so the total is bounded to 0..200.
	if first+second != 100 {
		return fieldError(path, "percentages must total 100")
	}
	return nil
}

func validateDurationShares(path string, shares []DurationShare, requireRange bool) error {
	if len(shares) == 0 {
		return fieldError(path, "must not be empty")
	}
	if len(shares) > 100 {
		return fieldError(path, "must contain at most 100 buckets")
	}
	total := 0
	for i, share := range shares {
		if share.Percent <= 0 || share.Percent > 100 {
			return fieldError(fmt.Sprintf("%s[%d].percent", path, i), "must be in 1..100")
		}
		// Each share is bounded before it contributes to the total.
		total += share.Percent
		if share.Min == 0 && share.Max == 0 && !requireRange {
			continue
		}
		if share.Min <= 0 {
			return fieldError(fmt.Sprintf("%s[%d].min", path, i), "must be greater than zero")
		}
		if share.Max <= 0 {
			return fieldError(fmt.Sprintf("%s[%d].max", path, i), "must be greater than zero")
		}
		if share.Min > share.Max {
			return fieldError(fmt.Sprintf("%s[%d]", path, i), "min must not exceed max")
		}
	}
	if total != 100 {
		return fieldError(path, "percentages must total 100")
	}
	return nil
}

func validateLifecycle(lifecycle LifecycleDistribution) error {
	buckets := []struct {
		path          string
		bucket        LifecycleBucket
		requiresRange bool
	}{
		{"workload.lifecycle.one_shot", lifecycle.OneShot, false},
		{"workload.lifecycle.revisit", lifecycle.Revisit, false},
		{"workload.lifecycle.rotating", lifecycle.Rotating, true},
		{"workload.lifecycle.long", lifecycle.Long, true},
	}
	total := 0
	for _, entry := range buckets {
		if entry.bucket.Percent < 0 || entry.bucket.Percent > 100 {
			return fieldError(entry.path+".percent", "must be in 0..100")
		}
		// Each of the four shares is bounded before it contributes to the total.
		total += entry.bucket.Percent
		rangeValue := entry.bucket.ActiveDuration
		if !entry.requiresRange {
			if rangeValue.Min != 0 || rangeValue.Max != 0 {
				return fieldError(entry.path+".active_duration", "must be empty")
			}
			continue
		}
		if rangeValue.Min <= 0 {
			return fieldError(entry.path+".active_duration.min", "must be greater than zero")
		}
		if rangeValue.Max <= 0 {
			return fieldError(entry.path+".active_duration.max", "must be greater than zero")
		}
		if rangeValue.Min > rangeValue.Max {
			return fieldError(entry.path+".active_duration", "min must not exceed max")
		}
	}
	if total != 100 {
		return fieldError("workload.lifecycle", "percentages must total 100")
	}
	return nil
}

func validatePayloads(shares []PayloadShare) error {
	if len(shares) == 0 {
		return fieldError("workload.payloads", "must not be empty")
	}
	total := 0
	for i, share := range shares {
		if share.Percent < 0 || share.Percent > 100 {
			return fieldError(fmt.Sprintf("workload.payloads[%d].percent", i), "must be in 0..100")
		}
		if share.Bytes <= 0 {
			return fieldError(fmt.Sprintf("workload.payloads[%d].bytes", i), "must be greater than zero")
		}
		// Each share is bounded before it contributes to the total.
		total += share.Percent
	}
	if total != 100 {
		return fieldError("workload.payloads", "percentages must total 100")
	}
	return nil
}

func checkedAddNonnegativeInt(left, right int) (int, bool) {
	if left < 0 || right < 0 || left > math.MaxInt-right {
		return 0, false
	}
	return left + right, true
}

func checkedAddPositiveDuration(left, right time.Duration) (time.Duration, bool) {
	if left <= 0 || right <= 0 || left > time.Duration(math.MaxInt64)-right {
		return 0, false
	}
	return left + right, true
}

func validateIntRange(path string, r IntRange) error {
	if r.Min <= 0 {
		return fieldError(path+".min", "must be greater than zero")
	}
	if r.Max <= 0 {
		return fieldError(path+".max", "must be greater than zero")
	}
	if r.Min > r.Max {
		return fieldError(path, "min must not exceed max")
	}
	return nil
}

func validateDurationRange(path string, r DurationRange) error {
	if r.Min <= 0 {
		return fieldError(path+".min", "must be greater than zero")
	}
	if r.Max <= 0 {
		return fieldError(path+".max", "must be greater than zero")
	}
	if r.Min > r.Max {
		return fieldError(path, "min must not exceed max")
	}
	return nil
}
