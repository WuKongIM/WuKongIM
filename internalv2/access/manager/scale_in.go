package manager

import (
	"errors"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ManagerNodeScaleInRequest is the JSON body for bounded node scale-in actions.
type ManagerNodeScaleInRequest struct {
	// MaxSlotMoves bounds the number of Slot replica move candidates or tasks.
	MaxSlotMoves uint32 `json:"max_slot_moves"`
}

// ManagerNodeScaleInCandidate is one manager-facing Slot drain candidate.
type ManagerNodeScaleInCandidate struct {
	// SlotID is the physical Slot selected for a move.
	SlotID uint32 `json:"slot_id"`
	// SourceNodeID is the leaving peer that will be replaced.
	SourceNodeID uint64 `json:"source_node_id"`
	// TargetNodeID is the active data node that will replace SourceNodeID.
	TargetNodeID uint64 `json:"target_node_id"`
	// DesiredPeers is the current desired peer set observed in control state.
	DesiredPeers []uint64 `json:"desired_peers"`
	// TargetPeers is the desired peer set after replacement.
	TargetPeers []uint64 `json:"target_peers"`
	// ConfigEpoch fences the move to the observed Slot assignment.
	ConfigEpoch uint64 `json:"config_epoch"`
}

// ManagerNodeScaleInPlanResponse is the manager scale-in drain preview response.
type ManagerNodeScaleInPlanResponse struct {
	// GeneratedAt records when the response was assembled.
	GeneratedAt string `json:"generated_at"`
	// StateRevision fences the plan to the observed control snapshot.
	StateRevision uint64 `json:"state_revision"`
	// NodeID is the leaving data node being drained.
	NodeID uint64 `json:"node_id"`
	// Candidates contains planned Slot replica moves.
	Candidates []ManagerNodeScaleInCandidate `json:"candidates"`
	// BlockedByStatus reports that safety status blocks task creation.
	BlockedByStatus bool `json:"blocked_by_status"`
}

// ManagerNodeScaleInAdvanceResponse reports submitted Slot drain work.
type ManagerNodeScaleInAdvanceResponse struct {
	// GeneratedAt records when the response was assembled.
	GeneratedAt string `json:"generated_at"`
	// StateRevision fences the request to the observed control snapshot.
	StateRevision uint64 `json:"state_revision"`
	// NodeID is the leaving data node being drained.
	NodeID uint64 `json:"node_id"`
	// Created is the number of new durable tasks.
	Created uint32 `json:"created"`
	// Skipped is the number of candidates that did not create a new task.
	Skipped uint32 `json:"skipped"`
	// Candidates contains submitted Slot move candidates.
	Candidates []ManagerNodeScaleInCandidate `json:"candidates"`
}

// ManagerNodeScaleInStartResponse is returned after marking a node leaving.
type ManagerNodeScaleInStartResponse struct {
	// Changed reports whether the control writer advanced cluster state.
	Changed bool `json:"changed"`
	// NodeID is the durable node identity returned by control state.
	NodeID uint64 `json:"node_id"`
	// Addr is the durable cluster control-plane address returned by control state.
	Addr string `json:"addr"`
	// JoinState is the durable membership lifecycle state.
	JoinState string `json:"join_state"`
	// Revision is the control-state revision observed by the writer.
	Revision uint64 `json:"revision"`
}

// ManagerNodeScaleInDrainRequest is the JSON body for gateway drain mode changes.
type ManagerNodeScaleInDrainRequest struct {
	// Draining disables new gateway admission when true.
	Draining bool `json:"draining"`
}

// ManagerNodeScaleInDrainResponse reports target-node runtime state after a drain mode change.
type ManagerNodeScaleInDrainResponse struct {
	// NodeID is the target cluster node.
	NodeID uint64 `json:"node_id"`
	// Draining reports whether gateway drain mode is enabled.
	Draining bool `json:"draining"`
	// AcceptingNewSessions reports whether gateway admission currently accepts new sessions.
	AcceptingNewSessions bool `json:"accepting_new_sessions"`
	// GatewaySessions counts all gateway sessions, including unauthenticated sessions.
	GatewaySessions int `json:"gateway_sessions"`
	// ActiveOnline counts active authenticated online connections.
	ActiveOnline int `json:"active_online"`
	// ClosingOnline counts authenticated online connections that are closing but not fully removed.
	ClosingOnline int `json:"closing_online"`
	// TotalOnline counts all authenticated online connections tracked by the node.
	TotalOnline int `json:"total_online"`
	// PendingActivations counts local sessions accepted but not yet authority-active.
	PendingActivations int `json:"pending_activations"`
	// Unknown means runtime counters could not be read.
	Unknown bool `json:"unknown"`
}

// ManagerNodeScaleInStatusResponse exposes every scale-in safety bit.
type ManagerNodeScaleInStatusResponse struct {
	// NodeID is the evaluated node identity.
	NodeID uint64 `json:"node_id"`
	// JoinState is the durable membership lifecycle state observed in control state.
	JoinState string `json:"join_state"`
	// GeneratedAt records when the status read model was generated.
	GeneratedAt string `json:"generated_at"`
	// StateRevision is the control-state revision used for this status.
	StateRevision uint64 `json:"state_revision"`
	// SafeToProceed reports that no Slot, task, Channel, or control blocker remains.
	SafeToProceed bool `json:"safe_to_proceed"`
	// SafeToRemove reports that no final removal blocker remains.
	SafeToRemove bool `json:"safe_to_remove"`
	// BlockedByMissingNode reports that control state no longer contains the target node.
	BlockedByMissingNode bool `json:"blocked_by_missing_node"`
	// BlockedByJoinState reports that the target node is not marked leaving.
	BlockedByJoinState bool `json:"blocked_by_join_state"`
	// BlockedByControlRevision reports stale control revision observations.
	BlockedByControlRevision bool `json:"blocked_by_control_revision"`
	// BlockedByControllerRole reports that the target node still has the Controller role.
	BlockedByControllerRole bool `json:"blocked_by_controller_role"`
	// BlockedBySlots reports that desired Slot assignments still include the target node.
	BlockedBySlots bool `json:"blocked_by_slots"`
	// BlockedBySlotLeadership reports live Slot leadership still on the target node.
	BlockedBySlotLeadership bool `json:"blocked_by_slot_leadership"`
	// BlockedBySlotRuntime reports unavailable or unsafe Slot runtime observations.
	BlockedBySlotRuntime bool `json:"blocked_by_slot_runtime"`
	// BlockedByTasks reports active or failed Controller tasks referencing the target.
	BlockedByTasks bool `json:"blocked_by_tasks"`
	// BlockedByChannels reports Channel leader, replica, ISR, or unknown inventory blockers.
	BlockedByChannels bool `json:"blocked_by_channels"`
	// BlockedByRuntimeDrain reports gateway admission or runtime counters still block final removal.
	BlockedByRuntimeDrain bool `json:"blocked_by_runtime_drain"`
	// UnknownRuntime reports that one or more eligible nodes lacked runtime summary data.
	UnknownRuntime bool `json:"unknown_runtime"`
	// RuntimeUnknown reports that the target node runtime drain counters could not be read.
	RuntimeUnknown bool `json:"runtime_unknown"`
	// UnknownControlRevision reports missing control revision observations.
	UnknownControlRevision bool `json:"unknown_control_revision"`
	// UnknownChannelInventory reports that Channel inventory could not be proven.
	UnknownChannelInventory bool `json:"unknown_channel_inventory"`
	// SlotReplicaCount counts Slot replicas that still block removing the target node.
	SlotReplicaCount int `json:"slot_replica_count"`
	// SlotLeaderCount counts live Slot leaders still observed on the target node.
	SlotLeaderCount int `json:"slot_leader_count"`
	// ActiveTaskCount counts pending or running Controller tasks that reference the target.
	ActiveTaskCount int `json:"active_task_count"`
	// FailedTaskCount counts failed Controller tasks that reference the target.
	FailedTaskCount int `json:"failed_task_count"`
	// ChannelLeaderCount counts Channels led by the target node.
	ChannelLeaderCount int `json:"channel_leader_count"`
	// ChannelReplicaCount counts Channels where the target is a configured replica.
	ChannelReplicaCount int `json:"channel_replica_count"`
	// ChannelISRCount counts Channels where the target is in ISR.
	ChannelISRCount int `json:"channel_isr_count"`
	// GatewayDraining reports whether the target gateway is in drain mode.
	GatewayDraining bool `json:"gateway_draining"`
	// AcceptingNewSessions reports whether the target gateway still admits new sessions.
	AcceptingNewSessions bool `json:"accepting_new_sessions"`
	// GatewaySessions counts all target gateway sessions, including unauthenticated sessions.
	GatewaySessions int `json:"gateway_sessions"`
	// ActiveOnline counts active authenticated online connections on the target node.
	ActiveOnline int `json:"active_online"`
	// ClosingOnline counts authenticated online connections that are closing on the target node.
	ClosingOnline int `json:"closing_online"`
	// TotalOnline counts authenticated online connections tracked by the target node.
	TotalOnline int `json:"total_online"`
	// PendingActivations counts target sessions accepted but not yet authority-active.
	PendingActivations int `json:"pending_activations"`
}

func (s *Server) handleNodeScaleInPlan(c *gin.Context) {
	nodeID, body, ok := s.parseNodeScaleInRequest(c)
	if !ok {
		return
	}
	response, err := s.management.PlanNodeScaleIn(c.Request.Context(), managementusecase.NodeScaleInPlanRequest{
		NodeID:       nodeID,
		MaxSlotMoves: body.MaxSlotMoves,
	})
	if err != nil {
		writeNodeScaleInError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeScaleInPlanResponseDTO(response))
}

func (s *Server) handleNodeScaleInStart(c *gin.Context) {
	nodeID, ok := s.parseNodeScaleInNodeID(c)
	if !ok {
		return
	}
	response, err := s.management.MarkNodeLeaving(c.Request.Context(), managementusecase.MarkNodeLeavingRequest{NodeID: nodeID})
	if err != nil {
		writeNodeScaleInError(c, err)
		return
	}
	status := http.StatusOK
	if response.Changed {
		status = http.StatusAccepted
	}
	c.JSON(status, nodeScaleInStartResponseDTO(response))
}

func (s *Server) handleNodeScaleInStatus(c *gin.Context) {
	nodeID, ok := s.parseNodeScaleInNodeID(c)
	if !ok {
		return
	}
	response, err := s.management.NodeScaleInStatus(c.Request.Context(), managementusecase.NodeScaleInStatusRequest{NodeID: nodeID})
	if err != nil {
		writeNodeScaleInError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeScaleInStatusResponseDTO(response))
}

func (s *Server) handleNodeScaleInDrain(c *gin.Context) {
	nodeID, ok := s.parseNodeScaleInNodeID(c)
	if !ok {
		return
	}
	var body ManagerNodeScaleInDrainRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return
	}
	response, err := s.management.SetNodeDrainMode(c.Request.Context(), managementusecase.SetNodeDrainModeRequest{
		NodeID:   nodeID,
		Draining: body.Draining,
	})
	if err != nil {
		writeNodeScaleInError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeScaleInDrainResponseDTO(response))
}

func (s *Server) handleNodeScaleInAdvance(c *gin.Context) {
	nodeID, body, ok := s.parseNodeScaleInRequest(c)
	if !ok {
		return
	}
	response, err := s.management.AdvanceNodeScaleIn(c.Request.Context(), managementusecase.NodeScaleInAdvanceRequest{
		NodeID:       nodeID,
		MaxSlotMoves: body.MaxSlotMoves,
	})
	if err != nil {
		writeNodeScaleInError(c, err)
		return
	}
	status := http.StatusOK
	if response.Created > 0 {
		status = http.StatusAccepted
	}
	c.JSON(status, nodeScaleInAdvanceResponseDTO(response))
}

func (s *Server) parseNodeScaleInNodeID(c *gin.Context) (uint64, bool) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return 0, false
	}
	nodeID, err := parseRequiredLogNodeID(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
		return 0, false
	}
	return nodeID, true
}

func (s *Server) parseNodeScaleInRequest(c *gin.Context) (uint64, ManagerNodeScaleInRequest, bool) {
	nodeID, ok := s.parseNodeScaleInNodeID(c)
	if !ok {
		return 0, ManagerNodeScaleInRequest{}, false
	}
	var body ManagerNodeScaleInRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
			return 0, ManagerNodeScaleInRequest{}, false
		}
	}
	return nodeID, body, true
}

func nodeScaleInStartResponseDTO(response managementusecase.MarkNodeLeavingResponse) ManagerNodeScaleInStartResponse {
	return ManagerNodeScaleInStartResponse{
		Changed:   response.Changed,
		NodeID:    response.NodeID,
		Addr:      response.Addr,
		JoinState: response.JoinState,
		Revision:  response.Revision,
	}
}

func nodeScaleInStatusResponseDTO(response managementusecase.NodeScaleInStatusResponse) ManagerNodeScaleInStatusResponse {
	return ManagerNodeScaleInStatusResponse{
		NodeID:                   response.NodeID,
		JoinState:                response.JoinState,
		GeneratedAt:              managerTimeString(response.GeneratedAt),
		StateRevision:            response.StateRevision,
		SafeToProceed:            response.SafeToProceed,
		SafeToRemove:             response.SafeToRemove,
		BlockedByMissingNode:     response.BlockedByMissingNode,
		BlockedByJoinState:       response.BlockedByJoinState,
		BlockedByControlRevision: response.BlockedByControlRevision,
		BlockedByControllerRole:  response.BlockedByControllerRole,
		BlockedBySlots:           response.BlockedBySlots,
		BlockedBySlotLeadership:  response.BlockedBySlotLeadership,
		BlockedBySlotRuntime:     response.BlockedBySlotRuntime,
		BlockedByTasks:           response.BlockedByTasks,
		BlockedByChannels:        response.BlockedByChannels,
		BlockedByRuntimeDrain:    response.BlockedByRuntimeDrain,
		UnknownRuntime:           response.UnknownRuntime,
		RuntimeUnknown:           response.RuntimeUnknown,
		UnknownControlRevision:   response.UnknownControlRevision,
		UnknownChannelInventory:  response.UnknownChannelInventory,
		SlotReplicaCount:         response.SlotReplicaCount,
		SlotLeaderCount:          response.SlotLeaderCount,
		ActiveTaskCount:          response.ActiveTaskCount,
		FailedTaskCount:          response.FailedTaskCount,
		ChannelLeaderCount:       response.ChannelLeaderCount,
		ChannelReplicaCount:      response.ChannelReplicaCount,
		ChannelISRCount:          response.ChannelISRCount,
		GatewayDraining:          response.GatewayDraining,
		AcceptingNewSessions:     response.AcceptingNewSessions,
		GatewaySessions:          response.GatewaySessions,
		ActiveOnline:             response.ActiveOnline,
		ClosingOnline:            response.ClosingOnline,
		TotalOnline:              response.TotalOnline,
		PendingActivations:       response.PendingActivations,
	}
}

func nodeScaleInDrainResponseDTO(response managementusecase.SetNodeDrainModeResponse) ManagerNodeScaleInDrainResponse {
	return ManagerNodeScaleInDrainResponse{
		NodeID:               response.NodeID,
		Draining:             response.Draining,
		AcceptingNewSessions: response.AcceptingNewSessions,
		GatewaySessions:      response.GatewaySessions,
		ActiveOnline:         response.ActiveOnline,
		ClosingOnline:        response.ClosingOnline,
		TotalOnline:          response.TotalOnline,
		PendingActivations:   response.PendingActivations,
		Unknown:              response.Unknown,
	}
}

func nodeScaleInPlanResponseDTO(response managementusecase.NodeScaleInPlanResponse) ManagerNodeScaleInPlanResponse {
	return ManagerNodeScaleInPlanResponse{
		GeneratedAt:     managerTimeString(response.GeneratedAt),
		StateRevision:   response.StateRevision,
		NodeID:          response.NodeID,
		Candidates:      nodeScaleInCandidateDTOs(response.Candidates),
		BlockedByStatus: response.BlockedByStatus,
	}
}

func nodeScaleInAdvanceResponseDTO(response managementusecase.NodeScaleInAdvanceResponse) ManagerNodeScaleInAdvanceResponse {
	return ManagerNodeScaleInAdvanceResponse{
		GeneratedAt:   managerTimeString(response.GeneratedAt),
		StateRevision: response.StateRevision,
		NodeID:        response.NodeID,
		Created:       response.Created,
		Skipped:       response.Skipped,
		Candidates:    nodeScaleInCandidateDTOs(response.Candidates),
	}
}

func nodeScaleInCandidateDTOs(items []managementusecase.NodeScaleInCandidate) []ManagerNodeScaleInCandidate {
	out := make([]ManagerNodeScaleInCandidate, 0, len(items))
	for _, item := range items {
		out = append(out, ManagerNodeScaleInCandidate{
			SlotID:       item.SlotID,
			SourceNodeID: item.SourceNodeID,
			TargetNodeID: item.TargetNodeID,
			DesiredPeers: append([]uint64(nil), item.DesiredPeers...),
			TargetPeers:  append([]uint64(nil), item.TargetPeers...),
			ConfigEpoch:  item.ConfigEpoch,
		})
	}
	return out
}

func writeNodeScaleInError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "bad_request")
	case errors.Is(err, managementusecase.ErrNodeLifecycleNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "not_found")
	case errors.Is(err, managementusecase.ErrNodeLifecycleConflict),
		errors.Is(err, managementusecase.ErrNodeScaleInConflict),
		errors.Is(err, managementusecase.ErrNodeScaleInUnsafe):
		jsonError(c, http.StatusConflict, "conflict", "conflict")
	case errors.Is(err, managementusecase.ErrNodeLifecycleUnavailable),
		errors.Is(err, managementusecase.ErrNodeScaleInUnavailable),
		errors.Is(err, clusterv2.ErrNotStarted),
		errors.Is(err, clusterv2.ErrNotLeader),
		errors.Is(err, clusterv2.ErrStopping):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "service_unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
