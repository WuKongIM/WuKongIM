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

// ChannelRuntimeBenchController adapts ChannelV2 runtime diagnostics to bench/v1 DTOs.
type ChannelRuntimeBenchController struct {
	node ChannelRuntimeBenchNode
}

// NewChannelRuntimeBenchController creates a ChannelRuntimeBenchController.
func NewChannelRuntimeBenchController(node ChannelRuntimeBenchNode) *ChannelRuntimeBenchController {
	return &ChannelRuntimeBenchController{node: node}
}

// Snapshot returns a bench/v1 snapshot of local ChannelV2 runtime state.
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

// Probe checks whether generated benchmark channels are loaded in the local ChannelV2 runtime.
func (c *ChannelRuntimeBenchController) Probe(ctx context.Context, query model.ChannelRuntimeQuery) (model.ChannelRuntimeProbeResult, error) {
	if c == nil || c.node == nil {
		return model.ChannelRuntimeProbeResult{}, fmt.Errorf("cluster: channel runtime bench node is required")
	}
	result, err := c.node.ChannelRuntimeProbe(ctx, runtimeSelectorFromQuery(query))
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
	}, nil
}

// Evict unloads selected generated benchmark channels from the local ChannelV2 runtime.
func (c *ChannelRuntimeBenchController) Evict(ctx context.Context, query model.ChannelRuntimeQuery) (model.ChannelRuntimeEvictResult, error) {
	if c == nil || c.node == nil {
		return model.ChannelRuntimeEvictResult{}, fmt.Errorf("cluster: channel runtime bench node is required")
	}
	result, err := c.node.ChannelRuntimeEvict(ctx, runtimeSelectorFromQuery(query))
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

func runtimeSelectorFromQuery(query model.ChannelRuntimeQuery) channelruntime.RuntimeSelector {
	channelIDs := make([]channelruntime.ChannelID, 0)
	if query.Range.End > query.Range.Start {
		channelIDs = make([]channelruntime.ChannelID, 0, query.Range.End-query.Range.Start)
	}
	runID := strings.TrimSpace(query.RunID)
	profile := strings.TrimSpace(query.Profile)
	for index := query.Range.Start; index < query.Range.End; index++ {
		channelIDs = append(channelIDs, channelruntime.ChannelID{
			ID:   fmt.Sprintf("%s-%s-%d", runID, profile, index),
			Type: query.ChannelType,
		})
	}
	return channelruntime.RuntimeSelector{ChannelIDs: channelIDs}
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
