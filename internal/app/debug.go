package app

import (
	"context"
	"errors"
	"fmt"
	"sort"

	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

const (
	maxDebugClusterSlots    = 256
	maxDebugClusterReplicas = 256
)

func (a *App) debugConfigSnapshot() any {
	if a == nil {
		return map[string]any{}
	}
	clusterCfg := defaultClusterConfig(a.cfg)
	return map[string]any{
		"node_id":                  diagnosticsNodeID(a.cfg),
		"node_data_dir":            a.cfg.DataDir,
		"cluster_listen":           a.cfg.Cluster.ListenAddr,
		"api_listen":               a.cfg.API.ListenAddr,
		"gateway_listeners":        len(a.cfg.Gateway.Listeners),
		"metrics_enable":           a.cfg.Observability.MetricsEnabled,
		"debug_api_enable":         a.cfg.Observability.DebugAPIEnabled,
		"diagnostics_enable":       a.cfg.Observability.Diagnostics.Enabled,
		"initial_slot_count":       clusterCfg.Slots.InitialSlotCount,
		"hash_slot_count":          clusterCfg.Slots.HashSlotCount,
		"slot_replica_count":       clusterCfg.Slots.ReplicaCount,
		"channel_replica_count":    clusterCfg.Channel.ReplicaCount,
		"channel_max_loaded_count": clusterCfg.Channel.MaxChannels,
	}
}

type debugClusterRuntime interface {
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
	LocalSlotRaftStatus(context.Context, uint32) (clusterpkg.SlotRaftStatus, error)
}

type debugClusterResponse struct {
	NodeID        uint64             `json:"node_id"`
	StateRevision uint64             `json:"state_revision"`
	Slots         []debugClusterSlot `json:"slots"`
}

type debugClusterSlot struct {
	SlotID          uint32                 `json:"slot_id"`
	LeaderID        uint64                 `json:"leader_id"`
	Replicas        []uint64               `json:"replicas"`
	Voters          []uint64               `json:"voters"`
	Term            uint64                 `json:"term"`
	CommitIndex     uint64                 `json:"commit_index"`
	AppliedIndex    uint64                 `json:"applied_index"`
	ReplicaProgress []debugReplicaProgress `json:"replica_progress"`
}

type debugReplicaProgress struct {
	NodeID     uint64 `json:"node_id"`
	MatchIndex uint64 `json:"match_index"`
	LagEntries uint64 `json:"lag_entries"`
	State      string `json:"state"`
}

func (a *App) debugClusterSnapshot(ctx context.Context) (debugClusterResponse, error) {
	if a == nil {
		return debugClusterResponse{}, errors.New("debug cluster unavailable")
	}
	runtime, ok := a.cluster.(debugClusterRuntime)
	if !ok {
		return debugClusterResponse{}, errors.New("debug cluster unavailable")
	}
	snapshot, err := runtime.LocalControlSnapshot(ctx)
	if err != nil {
		return debugClusterResponse{}, errors.New("debug cluster control snapshot unavailable")
	}
	if len(snapshot.Slots) > maxDebugClusterSlots {
		return debugClusterResponse{}, errors.New("debug cluster slot limit exceeded")
	}
	assignments := append([]control.SlotAssignment(nil), snapshot.Slots...)
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].SlotID < assignments[j].SlotID })
	response := debugClusterResponse{
		NodeID: diagnosticsNodeID(a.cfg), StateRevision: snapshot.Revision,
		Slots: make([]debugClusterSlot, 0, len(assignments)),
	}
	var previousSlotID uint32
	for _, assignment := range assignments {
		if assignment.SlotID == 0 || assignment.SlotID == previousSlotID || len(assignment.DesiredPeers) > maxDebugClusterReplicas {
			return debugClusterResponse{}, errors.New("debug cluster invalid slot assignment")
		}
		previousSlotID = assignment.SlotID
		status, err := runtime.LocalSlotRaftStatus(ctx, assignment.SlotID)
		if err != nil {
			return debugClusterResponse{}, errors.New("debug cluster Slot Raft status unavailable")
		}
		row, err := debugClusterSlotFromStatus(assignment, status)
		if err != nil {
			return debugClusterResponse{}, err
		}
		response.Slots = append(response.Slots, row)
	}
	return response, nil
}

func debugClusterSlotFromStatus(assignment control.SlotAssignment, status clusterpkg.SlotRaftStatus) (debugClusterSlot, error) {
	if status.SlotID != assignment.SlotID || len(status.CurrentVoters) > maxDebugClusterReplicas || !status.ReplicaProgressComplete {
		return debugClusterSlot{}, errors.New("debug cluster invalid Slot Raft status")
	}
	row := debugClusterSlot{
		SlotID: assignment.SlotID, LeaderID: status.LeaderID,
		Replicas: append([]uint64(nil), assignment.DesiredPeers...),
		Voters:   append([]uint64(nil), status.CurrentVoters...), Term: status.Term,
		CommitIndex: status.CommitIndex, AppliedIndex: status.AppliedIndex,
	}
	if status.Role != "leader" {
		return row, nil
	}
	if status.NodeID == 0 || status.LeaderID != status.NodeID || len(status.ReplicaProgress) > maxDebugClusterReplicas {
		return debugClusterSlot{}, errors.New("debug cluster invalid Slot Raft leader status")
	}
	row.ReplicaProgress = make([]debugReplicaProgress, 0, len(status.ReplicaProgress))
	var previousNodeID uint64
	for _, progress := range status.ReplicaProgress {
		if progress.NodeID == 0 || progress.NodeID <= previousNodeID || !validDebugProgressState(progress.State) {
			return debugClusterSlot{}, fmt.Errorf(
				"debug cluster invalid Slot Raft replica progress: slot=%d replica=%d previous_replica=%d match=%d commit=%d state=%q",
				assignment.SlotID, progress.NodeID, previousNodeID, progress.MatchIndex, status.CommitIndex, progress.State,
			)
		}
		previousNodeID = progress.NodeID
		lagEntries := uint64(0)
		if progress.MatchIndex < status.CommitIndex {
			lagEntries = status.CommitIndex - progress.MatchIndex
		}
		row.ReplicaProgress = append(row.ReplicaProgress, debugReplicaProgress{
			NodeID: progress.NodeID, MatchIndex: progress.MatchIndex,
			LagEntries: lagEntries, State: progress.State,
		})
	}
	return row, nil
}

func validDebugProgressState(state string) bool {
	switch state {
	case "StateProbe", "StateReplicate", "StateSnapshot":
		return true
	default:
		return false
	}
}
