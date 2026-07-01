package management

import (
	"context"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

// Slot is the manager-facing slot DTO.
type Slot struct {
	// SlotID is the physical slot identifier.
	SlotID uint32
	// HashSlots contains the logical hash slots currently owned by this physical slot.
	HashSlots *SlotHashSlots
	// State contains lightweight derived health summaries for list rendering.
	State SlotState
	// Assignment contains the desired slot placement view.
	Assignment SlotAssignment
	// Runtime contains the best available slot runtime view.
	Runtime SlotRuntime
	// NodeLog contains the selected node's local Raft log watermark when requested.
	NodeLog *SlotNodeLogStatus
	// Task contains the active task summary for this Slot, when any.
	Task *SlotTask
}

// SlotHashSlots contains the current logical hash-slot ownership for one physical slot.
type SlotHashSlots struct {
	// Count is the number of logical hash slots currently assigned to this slot.
	Count int
	// Items contains the sorted logical hash slot identifiers currently assigned to this slot.
	Items []uint16
}

// ListSlotsOptions contains optional filters for manager slot inventory reads.
type ListSlotsOptions struct {
	// NodeID limits the list to slots assigned to this node.
	NodeID uint64
}

// SlotState contains derived manager slot state fields.
type SlotState struct {
	// Quorum summarizes whether the slot currently has quorum.
	Quorum string
	// Sync summarizes whether runtime peers/config match the desired assignment.
	Sync string
	// LeaderMatch reports whether the preferred leader is currently leading.
	LeaderMatch bool
	// LeaderTransferPending reports whether a leader transfer task is current.
	LeaderTransferPending bool
}

// SlotAssignment contains the manager-facing desired slot placement view.
type SlotAssignment struct {
	// DesiredPeers is the desired slot voter set.
	DesiredPeers []uint64
	// PreferredLeader is the controller preferred leader; zero means unset.
	PreferredLeader uint64
	// ConfigEpoch is the desired slot config epoch.
	ConfigEpoch uint64
	// BalanceVersion is reserved for legacy response compatibility.
	BalanceVersion uint64
}

// SlotRuntime contains the manager-facing observed slot runtime view.
type SlotRuntime struct {
	// CurrentPeers is the currently observed slot peer set.
	CurrentPeers []uint64
	// CurrentVoters is the currently observed slot voter set.
	CurrentVoters []uint64
	// PreferredLeaderID is the controller preferred leader projected into the fallback runtime view.
	PreferredLeaderID uint64
	// HealthyVoters is the observed healthy voter count.
	HealthyVoters uint32
	// HasQuorum reports whether the slot currently has quorum.
	HasQuorum bool
	// ObservedConfigEpoch is the currently observed runtime config epoch.
	ObservedConfigEpoch uint64
	// LastReportAt is the latest runtime observation timestamp.
	LastReportAt time.Time
}

// SlotTask is the manager-facing active task summary for one Slot.
type SlotTask struct {
	// TaskID is the durable task identity.
	TaskID string
	// Kind is the reconcile workflow kind.
	Kind string
	// Step is the current workflow step.
	Step string
	// Status is the task state.
	Status string
	// SourceNode is the optional source node for move-like tasks.
	SourceNode uint64
	// TargetNode is the primary task target when set.
	TargetNode uint64
	// TargetPeers are the peers expected to participate.
	TargetPeers []uint64
	// CompletionPolicy describes how participant progress gates completion.
	CompletionPolicy string
	// ConfigEpoch ties the task to a Slot assignment epoch.
	ConfigEpoch uint64
	// Attempt is the global task attempt.
	Attempt uint32
	// LastError is the latest task-level error.
	LastError string
	// PhaseIndex is the externally observed Slot Raft phase index for this task.
	PhaseIndex uint32
	// ObservedConfigIndex is the Slot Raft applied index that proved the current phase.
	ObservedConfigIndex uint64
	// ObservedVoters is the Slot Raft voter set observed for the current phase.
	ObservedVoters []uint64
	// ObservedLearners is the Slot Raft learner set observed for the current phase.
	ObservedLearners []uint64
	// Participants contains per-node task progress.
	Participants []SlotTaskParticipant
}

// SlotTaskParticipant is one node's task progress summary.
type SlotTaskParticipant struct {
	// NodeID is the participant node identity.
	NodeID uint64
	// Attempt is the participant-local attempt.
	Attempt uint32
	// Status is the participant progress state.
	Status string
	// LastError is the latest participant-level error.
	LastError string
}

// SlotNodeLogStatus is one node's local Raft log watermark for a slot.
type SlotNodeLogStatus struct {
	// NodeID is the node that reported the local log watermark.
	NodeID uint64
	// LeaderID is the slot Raft leader known by the reporting node.
	LeaderID uint64
	// Role is the reporting node's local Raft role for this slot.
	Role string
	// CurrentVoters is the current Slot Raft voter set observed by the reporting node.
	CurrentVoters []uint64
	// CommitIndex is the highest committed Raft log index known by the reporting node.
	CommitIndex uint64
	// AppliedIndex is the highest Raft log index applied by the reporting node.
	AppliedIndex uint64
}

// ListSlots returns manager slot DTOs ordered by slot ID.
func (a *App) ListSlots(ctx context.Context, opts ListSlotsOptions) ([]Slot, error) {
	if a == nil || a.cluster == nil {
		return nil, nil
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	generatedAt := a.now()
	tasksBySlot := make(map[uint32]*SlotTask, len(snapshot.Tasks))
	for _, task := range snapshot.Tasks {
		if task.SlotID == 0 {
			continue
		}
		if _, exists := tasksBySlot[task.SlotID]; exists {
			continue
		}
		tasksBySlot[task.SlotID] = slotTaskFromControl(task)
	}
	slots := make([]Slot, 0, len(snapshot.Slots))
	for _, assignment := range snapshot.Slots {
		slot := slotFromControlAssignment(assignment, snapshot.HashSlots, generatedAt, tasksBySlot[assignment.SlotID])
		if opts.NodeID != 0 && !containsUint64(slot.Assignment.DesiredPeers, opts.NodeID) {
			continue
		}
		if opts.NodeID != 0 {
			slot.NodeLog = a.slotNodeLogStatus(ctx, opts.NodeID, slot.SlotID)
		}
		slots = append(slots, slot)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].SlotID < slots[j].SlotID })
	return slots, nil
}

func slotFromControlAssignment(assignment control.SlotAssignment, hashSlots control.HashSlotTable, generatedAt time.Time, task *SlotTask) Slot {
	runtime, hasRuntime := runtimeFromControlAssignment(assignment, generatedAt)
	return Slot{
		SlotID:    assignment.SlotID,
		HashSlots: slotHashSlotsFromControlTable(hashSlots, assignment.SlotID),
		Task:      task,
		State: SlotState{
			Quorum:      managerSlotQuorumState(hasRuntime, runtime.HasQuorum),
			Sync:        managerSlotSyncState(hasRuntime),
			LeaderMatch: assignment.PreferredLeader != 0 && assignment.PreferredLeader == runtime.PreferredLeaderID,
		},
		Assignment: SlotAssignment{
			DesiredPeers:    append([]uint64(nil), assignment.DesiredPeers...),
			PreferredLeader: assignment.PreferredLeader,
			ConfigEpoch:     assignment.ConfigEpoch,
		},
		Runtime: runtime,
	}
}

func slotTaskFromControl(task control.ReconcileTask) *SlotTask {
	participants := make([]SlotTaskParticipant, 0, len(task.ParticipantProgress))
	for _, item := range task.ParticipantProgress {
		participants = append(participants, SlotTaskParticipant{
			NodeID:    item.NodeID,
			Attempt:   item.Attempt,
			Status:    string(item.Status),
			LastError: item.LastError,
		})
	}
	return &SlotTask{
		TaskID:              task.TaskID,
		Kind:                string(task.Kind),
		Step:                string(task.Step),
		Status:              string(task.Status),
		SourceNode:          task.SourceNode,
		TargetNode:          task.TargetNode,
		TargetPeers:         append([]uint64(nil), task.TargetPeers...),
		CompletionPolicy:    string(task.CompletionPolicy),
		ConfigEpoch:         task.ConfigEpoch,
		Attempt:             task.Attempt,
		LastError:           task.LastError,
		PhaseIndex:          task.PhaseIndex,
		ObservedConfigIndex: task.ObservedConfigIndex,
		ObservedVoters:      append([]uint64(nil), task.ObservedVoters...),
		ObservedLearners:    append([]uint64(nil), task.ObservedLearners...),
		Participants:        participants,
	}
}

func runtimeFromControlAssignment(assignment control.SlotAssignment, generatedAt time.Time) (SlotRuntime, bool) {
	if len(assignment.DesiredPeers) == 0 {
		return SlotRuntime{}, false
	}
	peers := append([]uint64(nil), assignment.DesiredPeers...)
	return SlotRuntime{
		CurrentPeers:        peers,
		CurrentVoters:       append([]uint64(nil), peers...),
		PreferredLeaderID:   assignment.PreferredLeader,
		HealthyVoters:       uint32(len(peers)),
		HasQuorum:           uint32(len(peers)) >= quorumSize(len(peers)),
		ObservedConfigEpoch: assignment.ConfigEpoch,
		LastReportAt:        generatedAt,
	}, true
}

func quorumSize(voters int) uint32 {
	if voters <= 0 {
		return 1
	}
	return uint32(voters/2 + 1)
}

func slotHashSlotsFromControlTable(table control.HashSlotTable, slotID uint32) *SlotHashSlots {
	if table.Count == 0 && len(table.Ranges) == 0 {
		return nil
	}
	items := make([]uint16, 0)
	for _, item := range table.Ranges {
		if item.SlotID != slotID {
			continue
		}
		for hashSlot := int(item.From); hashSlot <= int(item.To); hashSlot++ {
			items = append(items, uint16(hashSlot))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i] < items[j] })
	return &SlotHashSlots{Count: len(items), Items: items}
}

func managerSlotQuorumState(hasRuntime, hasQuorum bool) string {
	if !hasRuntime {
		return "unknown"
	}
	if hasQuorum {
		return "ready"
	}
	return "lost"
}

func managerSlotSyncState(hasRuntime bool) string {
	if !hasRuntime {
		return "unreported"
	}
	return "matched"
}

func (a *App) slotNodeLogStatus(ctx context.Context, nodeID uint64, slotID uint32) *SlotNodeLogStatus {
	if a == nil || a.slotRaft == nil {
		return nil
	}
	status, err := a.slotRaft.SlotRaftStatus(ctx, nodeID, slotID)
	if err != nil {
		return nil
	}
	if status.NodeID == 0 {
		status.NodeID = nodeID
	}
	if status.Role == "" {
		status.Role = "unknown"
	}
	return &status
}

func containsUint64(items []uint64, want uint64) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
