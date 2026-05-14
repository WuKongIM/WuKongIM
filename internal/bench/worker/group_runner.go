package worker

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/bench/metrics"
	"github.com/WuKongIM/WuKongIM/internal/bench/model"
	"github.com/WuKongIM/WuKongIM/internal/bench/target"
	benchworkload "github.com/WuKongIM/WuKongIM/internal/bench/workload"
)

type groupWorkloadBundle struct {
	profile  model.ProfileShard
	traffic  model.TrafficConfig
	channels []benchworkload.GroupChannel
}

type groupExecutionPlan struct {
	bundles []groupWorkloadBundle
	users   []benchworkload.ConnectionUser
}

func prepareGroupData(ctx context.Context, assignment Assignment) error {
	profiles := scenarioProfilesByName(assignment.Scenario)
	if len(profiles) == 0 {
		return nil
	}
	client := groupPrepareClient(assignment.Target)
	profileNames := sortedProfileNames(assignment.Plan.Profiles)
	for _, profileName := range profileNames {
		shard := assignment.Plan.Profiles[profileName]
		if shard.ChannelType != model.ChannelTypeGroup {
			continue
		}
		profileDef, ok := profiles[profileName]
		if !ok {
			return fmt.Errorf("group profile %q missing from scenario", profileName)
		}
		cfg := benchworkload.GroupPrepareConfig{
			RunID:                assignment.RunID,
			WorkerID:             assignment.WorkerID,
			ProfileName:          profileName,
			ShardMode:            profileDef.Shard.Mode,
			OwnsChannel:          groupShardOwnsChannel(shard, profileDef),
			ChannelRange:         shard.ChannelRange,
			MemberRange:          shard.MemberRange,
			MembersPerChannel:    profileDef.Members.Count,
			SubscribersBatchSize: profileDef.Prepare.SubscribersBatchSize,
			UIDPrefix:            assignment.Scenario.Identity.UIDPrefix,
		}
		if err := benchworkload.PrepareGroup(ctx, cfg, client, benchworkload.NoopGroupPrepareBarrier{}); err != nil {
			return fmt.Errorf("group profile %q prepare: %w", profileName, err)
		}
	}
	return nil
}

func buildGroupExecutionPlan(assignment Assignment) (groupExecutionPlan, error) {
	trafficByProfile := make(map[string][]model.TrafficConfig, len(assignment.Scenario.Messages.Traffic))
	for _, traffic := range assignment.Scenario.Messages.Traffic {
		ref := strings.TrimSpace(traffic.ChannelRef)
		if ref != "" {
			trafficByProfile[ref] = append(trafficByProfile[ref], traffic)
		}
	}
	profiles := scenarioProfilesByName(assignment.Scenario)
	profileNames := sortedProfileNames(assignment.Plan.Profiles)
	plan := groupExecutionPlan{}
	seenUsers := make(map[string]struct{})
	addUser := func(uid string) {
		if uid == "" {
			return
		}
		if _, ok := seenUsers[uid]; ok {
			return
		}
		seenUsers[uid] = struct{}{}
		plan.users = append(plan.users, benchworkload.ConnectionUser{
			UID:      uid,
			DeviceID: groupDeviceID(assignment.Scenario.Identity, uid),
			Token:    personToken(assignment.Scenario.Identity.Token.Mode, uid),
		})
	}

	for _, profileName := range profileNames {
		shard := assignment.Plan.Profiles[profileName]
		if shard.ChannelType != model.ChannelTypeGroup {
			continue
		}
		profileDef, ok := profiles[profileName]
		if !ok {
			return groupExecutionPlan{}, fmt.Errorf("group profile %q missing from scenario", profileName)
		}
		channels := groupChannelsForShard(assignment.RunID, shard, profileDef, assignment.Scenario.Identity)
		if len(channels) == 0 {
			continue
		}
		for _, ch := range channels {
			for _, uid := range ch.OnlineMembers {
				addUser(uid)
			}
		}
		trafficItems := trafficByProfile[profileName]
		if len(trafficItems) == 0 {
			return groupExecutionPlan{}, fmt.Errorf("group profile %q has assigned channels but no matching traffic", profileName)
		}
		for _, traffic := range trafficItems {
			plan.bundles = append(plan.bundles, groupWorkloadBundle{profile: shard, traffic: traffic, channels: channels})
		}
	}
	return plan, nil
}

func buildGroupWorkloads(assignment Assignment, bundles []groupWorkloadBundle, clients map[string]benchworkload.PersonClient) ([]*benchworkload.GroupWorkload, error) {
	workloads := make([]*benchworkload.GroupWorkload, 0, len(bundles))
	for _, bundle := range bundles {
		wl, err := benchworkload.NewGroupWorkload(benchworkload.GroupConfig{
			RunID:                  assignment.RunID,
			ProfileName:            bundle.profile.Name,
			TrafficName:            bundle.traffic.Name,
			ClientMsgPrefix:        assignment.Scenario.Identity.ClientMsgPrefix,
			PayloadSizeBytes:       assignment.Scenario.Messages.Payload.SizeBytes,
			VerifyRecvMode:         bundle.traffic.Verify.Recv.Mode,
			RecvSampleSize:         bundle.traffic.Verify.Recv.SampleSizePerMessage,
			RecvAck:                bundle.traffic.RecvAck,
			GlobalRate:             bundle.profile.GlobalRate,
			LocalRate:              bundle.profile.LocalRate,
			TrafficPartitionCount:  bundle.profile.TrafficPartitionCount,
			OwnedTrafficPartitions: bundle.profile.OwnedTrafficPartitions,
			Channels:               bundle.channels,
			Metrics:                metrics.NewRegistry(),
		}, clients)
		if err != nil {
			return nil, err
		}
		workloads = append(workloads, wl)
	}
	return workloads, nil
}

func groupPrepareClient(tgt model.Target) *target.Client {
	addrs := append([]string(nil), tgt.BenchAPI.Addrs...)
	if len(addrs) == 0 {
		addrs = append(addrs, tgt.API.Addrs...)
	}
	return target.NewClient(target.Config{APIAddrs: addrs, Token: tgt.BenchAPI.Token})
}

func scenarioProfilesByName(s model.Scenario) map[string]model.ChannelProfile {
	profiles := make(map[string]model.ChannelProfile, len(s.Channels.Profiles))
	for _, profile := range s.Channels.Profiles {
		name := strings.TrimSpace(profile.Name)
		if name != "" {
			profiles[name] = profile
		}
	}
	return profiles
}

func sortedProfileNames(profiles map[string]model.ProfileShard) []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func groupShardOwnsChannel(shard model.ProfileShard, profile model.ChannelProfile) bool {
	if profile.Shard.Mode != model.ShardModeSplitMembersAndTraffic {
		return true
	}
	return shard.ChannelRange.Len() > 0 && shard.MemberRange.Start == 0
}

func groupChannelsForShard(runID string, shard model.ProfileShard, profile model.ChannelProfile, identity model.IdentityConfig) []benchworkload.GroupChannel {
	if profile.Shard.Mode == model.ShardModeSplitMembersAndTraffic {
		online := onlineGroupMemberIDs(identity.UIDPrefix, indexesInWorkerRange(shard.MemberRange), profile.Online.MemberRatio)
		if len(online) == 0 || shard.ChannelRange.Len() <= 0 {
			return nil
		}
		return []benchworkload.GroupChannel{{
			ChannelIndex:   shard.ChannelRange.Start,
			ChannelID:      benchworkload.GroupChannelID(runID, shard.Name, shard.ChannelRange.Start),
			OnlineMembers:  online,
			TrafficIndexes: append([]int(nil), shard.OwnedTrafficPartitions...),
		}}
	}
	channels := make([]benchworkload.GroupChannel, 0, shard.ChannelRange.Len())
	for channelIndex := shard.ChannelRange.Start; channelIndex < shard.ChannelRange.End; channelIndex++ {
		memberIndexes := make([]int, 0, profile.Members.Count)
		for offset := 0; offset < profile.Members.Count; offset++ {
			memberIndexes = append(memberIndexes, channelIndex*profile.Members.Count+offset)
		}
		online := onlineGroupMemberIDs(identity.UIDPrefix, memberIndexes, profile.Online.MemberRatio)
		if len(online) == 0 {
			continue
		}
		channels = append(channels, benchworkload.GroupChannel{
			ChannelIndex:  channelIndex,
			ChannelID:     benchworkload.GroupChannelID(runID, shard.Name, channelIndex),
			OnlineMembers: online,
		})
	}
	return channels
}

func indexesInWorkerRange(r model.Range) []int {
	indexes := make([]int, 0, r.Len())
	for idx := r.Start; idx < r.End; idx++ {
		indexes = append(indexes, idx)
	}
	return indexes
}

func onlineGroupMemberIDs(uidPrefix string, memberIndexes []int, ratio float64) []string {
	count := len(memberIndexes)
	if count == 0 {
		return nil
	}
	if ratio > 0 && ratio < 1 {
		count = int(math.Round(float64(count) * ratio))
		if count < 1 {
			count = 1
		}
	}
	if count > len(memberIndexes) {
		count = len(memberIndexes)
	}
	uids := make([]string, 0, count)
	for _, memberIndex := range memberIndexes[:count] {
		uids = append(uids, indexedID(uidPrefix, memberIndex))
	}
	return uids
}

func groupDeviceID(identity model.IdentityConfig, uid string) string {
	prefix := strings.TrimSpace(identity.DevicePrefix)
	if prefix == "" {
		prefix = "bench-device"
	}
	suffix := uid
	if lastDash := strings.LastIndex(uid, "-"); lastDash >= 0 && lastDash < len(uid)-1 {
		suffix = uid[lastDash+1:]
	}
	return fmt.Sprintf("%s-%s", prefix, suffix)
}
