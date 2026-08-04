package cluster

import (
	"context"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
)

const benchRuntimeVersion = "bench/v1"

// ChannelRuntimeBenchNode is the cluster runtime diagnostic surface used by bench/v1.
type ChannelRuntimeBenchNode interface {
	NodeID() uint64
	ChannelRuntimeSnapshot(context.Context) (channelruntime.RuntimeSnapshot, error)
	ChannelRuntimeProbe(context.Context, channelruntime.RuntimeSelector) (channelruntime.RuntimeProbeResult, error)
	ChannelRuntimeEvict(context.Context, channelruntime.RuntimeSelector) (channelruntime.RuntimeEvictResult, error)
}

// ChannelRuntimeBenchController adapts channel runtime diagnostics to bench/v1 DTOs.
type ChannelRuntimeBenchController struct {
	node ChannelRuntimeBenchNode
}

// NewChannelRuntimeBenchController creates a ChannelRuntimeBenchController.
func NewChannelRuntimeBenchController(node ChannelRuntimeBenchNode) *ChannelRuntimeBenchController {
	return &ChannelRuntimeBenchController{node: node}
}

// Snapshot returns a bench/v1 snapshot of local channel runtime state.
func (c *ChannelRuntimeBenchController) Snapshot(ctx context.Context, query model.ChannelRuntimeQuery) (model.ChannelRuntimeSnapshot, error) {
	if c == nil || c.node == nil {
		return model.ChannelRuntimeSnapshot{}, fmt.Errorf("cluster: channel runtime bench node is required")
	}
	snapshot, err := c.node.ChannelRuntimeSnapshot(ctx)
	if err != nil {
		return model.ChannelRuntimeSnapshot{}, err
	}
	return fromRuntimeSnapshot(snapshot, c.node.NodeID(), query), nil
}

// Probe checks whether generated or explicit channels are loaded in the local channel runtime.
func (c *ChannelRuntimeBenchController) Probe(ctx context.Context, query model.ChannelRuntimeProbeQuery) (model.ChannelRuntimeProbeResult, error) {
	if c == nil || c.node == nil {
		return model.ChannelRuntimeProbeResult{}, fmt.Errorf("cluster: channel runtime bench node is required")
	}
	selector := runtimeSelectorFromProbeQuery(query)
	result, err := c.node.ChannelRuntimeProbe(ctx, selector)
	if err != nil {
		return model.ChannelRuntimeProbeResult{}, err
	}
	channels, err := runtimeProbeChannels(selector.ChannelIDs, result)
	if err != nil {
		return model.ChannelRuntimeProbeResult{}, err
	}
	return model.ChannelRuntimeProbeResult{
		Version:        benchRuntimeVersion,
		NodeID:         c.node.NodeID(),
		RunID:          query.RunID,
		Profile:        query.Profile,
		Checked:        result.Checked,
		LoadedLeader:   result.LoadedLeader,
		LoadedFollower: result.LoadedFollower,
		Missing:        missingChannelIDs(result.Missing),
		Channels:       channels,
	}, nil
}

// Evict unloads selected generated benchmark channels from the local channel runtime.
func (c *ChannelRuntimeBenchController) Evict(ctx context.Context, query model.ChannelRuntimeQuery) (model.ChannelRuntimeEvictResult, error) {
	if c == nil || c.node == nil {
		return model.ChannelRuntimeEvictResult{}, fmt.Errorf("cluster: channel runtime bench node is required")
	}
	result, err := c.node.ChannelRuntimeEvict(ctx, runtimeSelectorFromGeneratedQuery(query))
	if err != nil {
		return model.ChannelRuntimeEvictResult{}, err
	}
	return model.ChannelRuntimeEvictResult{
		Version:     benchRuntimeVersion,
		NodeID:      c.node.NodeID(),
		RunID:       query.RunID,
		Profile:     query.Profile,
		Requested:   result.Requested,
		Evicted:     result.Evicted,
		SkippedBusy: result.SkippedBusy,
		Missing:     result.Missing,
	}, nil
}

func fromRuntimeSnapshot(snapshot channelruntime.RuntimeSnapshot, fallbackNodeID uint64, query model.ChannelRuntimeQuery) model.ChannelRuntimeSnapshot {
	nodeID := uint64(snapshot.NodeID)
	if nodeID == 0 {
		nodeID = fallbackNodeID
	}
	return model.ChannelRuntimeSnapshot{
		Version:                 benchRuntimeVersion,
		NodeID:                  nodeID,
		RunID:                   query.RunID,
		Profile:                 query.Profile,
		ActiveTotal:             snapshot.ActiveTotal,
		ActiveLeader:            snapshot.ActiveLeader,
		ActiveFollower:          snapshot.ActiveFollower,
		FollowerParked:          snapshot.FollowerParked,
		ActivationRejectedTotal: snapshot.ActivationRejectedTotal,
		Reactors:                fromRuntimeReactors(snapshot.Reactors),
		WorkerQueues:            fromRuntimeWorkerQueues(snapshot.WorkerQueues),
	}
}

func fromRuntimeReactors(in []channelruntime.RuntimeReactorSnapshot) []model.ChannelRuntimeReactorSnapshot {
	if len(in) == 0 {
		return nil
	}
	out := make([]model.ChannelRuntimeReactorSnapshot, 0, len(in))
	for _, reactor := range in {
		out = append(out, model.ChannelRuntimeReactorSnapshot{
			ReactorID:    reactor.ReactorID,
			Leader:       reactor.Leader,
			Follower:     reactor.Follower,
			Parked:       reactor.Parked,
			MailboxDepth: reactor.MailboxDepth,
		})
	}
	return out
}

func fromRuntimeWorkerQueues(in []channelruntime.RuntimeWorkerQueue) []model.ChannelRuntimeWorkerQueue {
	if len(in) == 0 {
		return nil
	}
	out := make([]model.ChannelRuntimeWorkerQueue, 0, len(in))
	for _, queue := range in {
		out = append(out, model.ChannelRuntimeWorkerQueue{
			Pool:  queue.Pool,
			Depth: queue.Depth,
		})
	}
	return out
}

func runtimeSelectorFromProbeQuery(query model.ChannelRuntimeProbeQuery) channelruntime.RuntimeSelector {
	if query.Channels != nil {
		channelIDs := make([]channelruntime.ChannelID, 0, len(query.Channels))
		for _, identity := range query.Channels {
			channelIDs = append(channelIDs, channelruntime.ChannelID{ID: identity.ChannelID, Type: identity.ChannelType})
		}
		return channelruntime.RuntimeSelector{ChannelIDs: channelIDs}
	}
	return runtimeSelectorFromGeneratedFields(query.RunID, query.Profile, query.ChannelType, query.Range)
}

func runtimeSelectorFromGeneratedQuery(query model.ChannelRuntimeQuery) channelruntime.RuntimeSelector {
	return runtimeSelectorFromGeneratedFields(query.RunID, query.Profile, query.ChannelType, query.Range)
}

func runtimeSelectorFromGeneratedFields(runID, profile string, channelType uint8, selectedRange model.ChannelRuntimeRange) channelruntime.RuntimeSelector {
	channelIDs := make([]channelruntime.ChannelID, 0)
	if selectedRange.End > selectedRange.Start {
		channelIDs = make([]channelruntime.ChannelID, 0, selectedRange.End-selectedRange.Start)
	}
	runID = strings.TrimSpace(runID)
	profile = strings.TrimSpace(profile)
	for index := selectedRange.Start; index < selectedRange.End; index++ {
		channelIDs = append(channelIDs, channelruntime.ChannelID{
			ID:   fmt.Sprintf("%s-%s-%d", runID, profile, index),
			Type: channelType,
		})
	}
	return channelruntime.RuntimeSelector{ChannelIDs: channelIDs}
}

func runtimeProbeChannels(requested []channelruntime.ChannelID, result channelruntime.RuntimeProbeResult) ([]model.ChannelRuntimeProbeChannel, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	loaded := make(map[channelruntime.ChannelID]channelruntime.RuntimeProbeChannel, len(result.Channels))
	for _, channel := range result.Channels {
		loaded[channel.ChannelID] = channel
	}
	missing := make(map[channelruntime.ChannelID]struct{}, len(result.Missing))
	for _, identity := range result.Missing {
		missing[identity] = struct{}{}
	}
	out := make([]model.ChannelRuntimeProbeChannel, 0, len(result.Channels)+len(result.Missing))
	for _, identity := range requested {
		if channel, ok := loaded[identity]; ok {
			if channel.LeaderEpoch > uint64(^uint32(0)) || channel.ChannelEpoch > uint64(^uint32(0)) {
				return nil, fmt.Errorf("cluster: channel runtime probe epoch exceeds bench/v1 contract")
			}
			out = append(out, model.ChannelRuntimeProbeChannel{
				ChannelID:    channel.ChannelID.ID,
				ChannelType:  channel.ChannelID.Type,
				Role:         runtimeRoleString(channel.Role),
				Status:       runtimeStatusString(channel.Status),
				LEO:          channel.LEO,
				HW:           channel.HW,
				CheckpointHW: channel.CheckpointHW,
				LeaderEpoch:  uint32(channel.LeaderEpoch),
				ChannelEpoch: uint32(channel.ChannelEpoch),
			})
			continue
		}
		if _, ok := missing[identity]; ok {
			out = append(out, model.ChannelRuntimeProbeChannel{
				ChannelID: identity.ID, ChannelType: identity.Type, Role: "missing", Status: "missing",
			})
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func runtimeRoleString(role channelruntime.Role) string {
	switch role {
	case channelruntime.RoleLeader:
		return "leader"
	case channelruntime.RoleFollower:
		return "follower"
	default:
		return "unknown"
	}
}

func runtimeStatusString(status channelruntime.Status) string {
	switch status {
	case channelruntime.StatusCreating:
		return "creating"
	case channelruntime.StatusActive:
		return "active"
	case channelruntime.StatusDeleting:
		return "deleting"
	case channelruntime.StatusDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

func missingChannelIDs(in []channelruntime.ChannelID) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, channelID := range in {
		out = append(out, channelID.ID)
	}
	return out
}
