package manager

import (
	"net/http"
	"strconv"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/gin-gonic/gin"
)

// SlotsResponse is the manager slot list response body.
type SlotsResponse struct {
	// Total is the number of returned manager slots.
	Total int `json:"total"`
	// Items contains the ordered manager slot DTO list.
	Items []SlotDTO `json:"items"`
}

// SlotDTO is the manager-facing slot response item.
type SlotDTO struct {
	// SlotID is the physical slot identifier.
	SlotID uint32 `json:"slot_id"`
	// HashSlots contains the logical hash-slot ownership when known.
	HashSlots *SlotHashSlotsDTO `json:"hash_slots,omitempty"`
	// State contains lightweight derived slot summaries.
	State SlotStateDTO `json:"state"`
	// Assignment contains the desired slot placement view.
	Assignment SlotAssignmentDTO `json:"assignment"`
	// Task contains the active task summary for this Slot, when any.
	Task *SlotTaskDTO `json:"task,omitempty"`
	// Runtime contains the best available slot runtime view.
	Runtime SlotRuntimeDTO `json:"runtime"`
	// NodeLog contains the selected node's local log watermark when requested.
	NodeLog *SlotNodeLogDTO `json:"node_log,omitempty"`
}

// SlotHashSlotsDTO contains the logical hash-slot ownership for one physical slot.
type SlotHashSlotsDTO struct {
	// Count is the number of logical hash slots owned by the physical slot.
	Count int `json:"count"`
	// Items contains the logical hash slot IDs in ascending order.
	Items []uint16 `json:"items"`
}

// SlotStateDTO contains derived slot list state fields.
type SlotStateDTO struct {
	// Quorum summarizes whether the slot currently has quorum.
	Quorum string `json:"quorum"`
	// Sync summarizes whether runtime peers/config match the assignment.
	Sync string `json:"sync"`
	// LeaderMatch reports whether the preferred leader is currently leading.
	LeaderMatch bool `json:"leader_match"`
	// LeaderTransferPending reports whether a leader transfer task is current.
	LeaderTransferPending bool `json:"leader_transfer_pending"`
}

// SlotAssignmentDTO contains desired slot placement fields.
type SlotAssignmentDTO struct {
	// DesiredPeers is the desired slot voter set.
	DesiredPeers []uint64 `json:"desired_peers"`
	// PreferredLeaderID is the controller preferred leader; zero means unset.
	PreferredLeaderID uint64 `json:"preferred_leader_id"`
	// ConfigEpoch is the desired slot config epoch.
	ConfigEpoch uint64 `json:"config_epoch"`
	// BalanceVersion is reserved for legacy response compatibility.
	BalanceVersion uint64 `json:"balance_version"`
}

// SlotRuntimeDTO contains observed slot runtime fields.
type SlotRuntimeDTO struct {
	// CurrentPeers is the currently observed peer set.
	CurrentPeers []uint64 `json:"current_peers"`
	// CurrentVoters is the currently observed voter set.
	CurrentVoters []uint64 `json:"current_voters"`
	// PreferredLeaderID is the controller preferred leader projected into the fallback runtime view.
	PreferredLeaderID uint64 `json:"preferred_leader_id"`
	// HealthyVoters is the observed healthy voter count.
	HealthyVoters uint32 `json:"healthy_voters"`
	// HasQuorum reports whether the slot currently has quorum.
	HasQuorum bool `json:"has_quorum"`
	// ObservedConfigEpoch is the observed runtime config epoch.
	ObservedConfigEpoch uint64 `json:"observed_config_epoch"`
	// LastReportAt is the latest runtime observation timestamp.
	LastReportAt time.Time `json:"last_report_at"`
}

// SlotTaskDTO contains one active Slot task summary.
type SlotTaskDTO struct {
	// TaskID is the durable task identity.
	TaskID string `json:"task_id"`
	// Kind is the reconcile workflow kind.
	Kind string `json:"kind"`
	// Step is the current workflow step.
	Step string `json:"step"`
	// Status is the task state.
	Status string `json:"status"`
	// SourceNode is the optional source node for move-like tasks.
	SourceNode uint64 `json:"source_node,omitempty"`
	// TargetNode is the primary task target when set.
	TargetNode uint64 `json:"target_node,omitempty"`
	// TargetPeers are the peers expected to participate.
	TargetPeers []uint64 `json:"target_peers,omitempty"`
	// CompletionPolicy describes how participant progress gates completion.
	CompletionPolicy string `json:"completion_policy"`
	// ConfigEpoch ties the task to a Slot assignment epoch.
	ConfigEpoch uint64 `json:"config_epoch,omitempty"`
	// Attempt is the global task attempt.
	Attempt uint32 `json:"attempt"`
	// LastError is the latest task-level error.
	LastError string `json:"last_error,omitempty"`
	// PhaseIndex is the externally observed Slot Raft phase index for this task.
	PhaseIndex uint32 `json:"phase_index,omitempty"`
	// ObservedConfigIndex is the Slot Raft applied index that proved the current phase.
	ObservedConfigIndex uint64 `json:"observed_config_index,omitempty"`
	// ObservedVoters is the Slot Raft voter set observed for the current phase.
	ObservedVoters []uint64 `json:"observed_voters,omitempty"`
	// ObservedLearners is the Slot Raft learner set observed for the current phase.
	ObservedLearners []uint64 `json:"observed_learners,omitempty"`
	// Participants contains per-node task progress.
	Participants []SlotTaskParticipantDTO `json:"participants,omitempty"`
}

// SlotTaskParticipantDTO contains one task participant progress summary.
type SlotTaskParticipantDTO struct {
	// NodeID is the participant node identity.
	NodeID uint64 `json:"node_id"`
	// Attempt is the participant-local attempt.
	Attempt uint32 `json:"attempt"`
	// Status is the participant progress state.
	Status string `json:"status"`
	// LastError is the latest participant-level error.
	LastError string `json:"last_error,omitempty"`
}

// SlotNodeLogDTO contains one selected node's local slot log watermark.
type SlotNodeLogDTO struct {
	// NodeID is the node that reported the local log watermark.
	NodeID uint64 `json:"node_id"`
	// LeaderID is the slot Raft leader known by the reporting node.
	LeaderID uint64 `json:"leader_id"`
	// Role is the reporting node's local Raft role for this slot.
	Role string `json:"role"`
	// CurrentVoters is the current Slot Raft voter set observed by the reporting node.
	CurrentVoters []uint64 `json:"current_voters,omitempty"`
	// CommitIndex is the highest committed Raft log index known by the reporting node.
	CommitIndex uint64 `json:"commit_index"`
	// AppliedIndex is the highest Raft log index applied by the reporting node.
	AppliedIndex uint64 `json:"applied_index"`
}

func (s *Server) handleSlots(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	opts, err := parseListSlotsOptions(c)
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	items, err := s.management.ListSlots(c.Request.Context(), opts)
	if err != nil {
		if controlSnapshotUnavailable(err) {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "controller snapshot unavailable")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, SlotsResponse{Total: len(items), Items: slotDTOs(items)})
}

func parseListSlotsOptions(c *gin.Context) (managementusecase.ListSlotsOptions, error) {
	rawNodeID := c.Query("node_id")
	if rawNodeID == "" {
		return managementusecase.ListSlotsOptions{}, nil
	}
	nodeID, err := strconv.ParseUint(rawNodeID, 10, 64)
	if err != nil || nodeID == 0 {
		return managementusecase.ListSlotsOptions{}, strconv.ErrSyntax
	}
	return managementusecase.ListSlotsOptions{NodeID: nodeID}, nil
}

func slotDTOs(items []managementusecase.Slot) []SlotDTO {
	out := make([]SlotDTO, 0, len(items))
	for _, item := range items {
		out = append(out, slotDTO(item))
	}
	return out
}

func slotDTO(item managementusecase.Slot) SlotDTO {
	return SlotDTO{
		SlotID:    item.SlotID,
		HashSlots: slotHashSlotsDTO(item.HashSlots),
		State: SlotStateDTO{
			Quorum:                item.State.Quorum,
			Sync:                  item.State.Sync,
			LeaderMatch:           item.State.LeaderMatch,
			LeaderTransferPending: item.State.LeaderTransferPending,
		},
		Assignment: SlotAssignmentDTO{
			DesiredPeers:      append([]uint64(nil), item.Assignment.DesiredPeers...),
			PreferredLeaderID: item.Assignment.PreferredLeader,
			ConfigEpoch:       item.Assignment.ConfigEpoch,
			BalanceVersion:    item.Assignment.BalanceVersion,
		},
		Task: slotTaskDTO(item.Task),
		Runtime: SlotRuntimeDTO{
			CurrentPeers:        append([]uint64(nil), item.Runtime.CurrentPeers...),
			CurrentVoters:       append([]uint64(nil), item.Runtime.CurrentVoters...),
			PreferredLeaderID:   item.Runtime.PreferredLeaderID,
			HealthyVoters:       item.Runtime.HealthyVoters,
			HasQuorum:           item.Runtime.HasQuorum,
			ObservedConfigEpoch: item.Runtime.ObservedConfigEpoch,
			LastReportAt:        item.Runtime.LastReportAt,
		},
		NodeLog: slotNodeLogDTO(item.NodeLog),
	}
}

func slotTaskDTO(item *managementusecase.SlotTask) *SlotTaskDTO {
	if item == nil {
		return nil
	}
	participants := make([]SlotTaskParticipantDTO, 0, len(item.Participants))
	for _, participant := range item.Participants {
		participants = append(participants, SlotTaskParticipantDTO{
			NodeID:    participant.NodeID,
			Attempt:   participant.Attempt,
			Status:    participant.Status,
			LastError: participant.LastError,
		})
	}
	return &SlotTaskDTO{
		TaskID:              item.TaskID,
		Kind:                item.Kind,
		Step:                item.Step,
		Status:              item.Status,
		SourceNode:          item.SourceNode,
		TargetNode:          item.TargetNode,
		TargetPeers:         append([]uint64(nil), item.TargetPeers...),
		CompletionPolicy:    item.CompletionPolicy,
		ConfigEpoch:         item.ConfigEpoch,
		Attempt:             item.Attempt,
		LastError:           item.LastError,
		PhaseIndex:          item.PhaseIndex,
		ObservedConfigIndex: item.ObservedConfigIndex,
		ObservedVoters:      append([]uint64(nil), item.ObservedVoters...),
		ObservedLearners:    append([]uint64(nil), item.ObservedLearners...),
		Participants:        participants,
	}
}

func slotHashSlotsDTO(item *managementusecase.SlotHashSlots) *SlotHashSlotsDTO {
	if item == nil {
		return nil
	}
	return &SlotHashSlotsDTO{
		Count: item.Count,
		Items: append([]uint16(nil), item.Items...),
	}
}

func slotNodeLogDTO(item *managementusecase.SlotNodeLogStatus) *SlotNodeLogDTO {
	if item == nil {
		return nil
	}
	return &SlotNodeLogDTO{
		NodeID:        item.NodeID,
		LeaderID:      item.LeaderID,
		Role:          item.Role,
		CurrentVoters: append([]uint64(nil), item.CurrentVoters...),
		CommitIndex:   item.CommitIndex,
		AppliedIndex:  item.AppliedIndex,
	}
}
