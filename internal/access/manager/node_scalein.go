package manager

import (
	"errors"
	"io"
	"net/http"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/gin-gonic/gin"
)

const managerMaxScaleInLeaderTransfers = 3

// nodeScaleInPlanRequest is the HTTP request body for plan/start scale-in routes.
type nodeScaleInPlanRequest struct {
	ConfirmStatefulSetTail bool   `json:"confirm_statefulset_tail"`
	ExpectedTailNodeID     uint64 `json:"expected_tail_node_id"`
}

// advanceNodeScaleInRequest is the HTTP request body for one scale-in advance step.
type advanceNodeScaleInRequest struct {
	MaxLeaderTransfers    int  `json:"max_leader_transfers"`
	ForceCloseConnections bool `json:"force_close_connections"`
}

// nodeScaleInReportDTO is the shared response body for scale-in routes.
type nodeScaleInReportDTO struct {
	NodeID                   uint64                        `json:"node_id"`
	Status                   string                        `json:"status"`
	SafeToRemove             bool                          `json:"safe_to_remove"`
	CanStart                 bool                          `json:"can_start"`
	CanAdvance               bool                          `json:"can_advance"`
	CanCancel                bool                          `json:"can_cancel"`
	ConnectionSafetyVerified bool                          `json:"connection_safety_verified"`
	BlockedReasons           []nodeScaleInBlockedReasonDTO `json:"blocked_reasons"`
	Checks                   nodeScaleInChecksDTO          `json:"checks"`
	Progress                 nodeScaleInProgressDTO        `json:"progress"`
	Runtime                  nodeScaleInRuntimeSummaryDTO  `json:"runtime"`
	Leaders                  []nodeScaleInLeaderDTO        `json:"leaders"`
	NextAction               string                        `json:"next_action,omitempty"`
}

// nodeScaleInChecksDTO exposes stable preflight check names for the UI.
type nodeScaleInChecksDTO struct {
	TargetExists                          bool `json:"target_exists"`
	TargetIsDataNode                      bool `json:"target_is_data_node"`
	TargetIsActiveOrDraining              bool `json:"target_is_active_or_draining"`
	TargetIsNotControllerVoter            bool `json:"target_is_not_controller_voter"`
	TailNodeMappingVerified               bool `json:"tail_node_mapping_verified"`
	RemainingDataNodesEnough              bool `json:"remaining_data_nodes_enough"`
	ControllerLeaderAvailable             bool `json:"controller_leader_available"`
	SlotReplicaCountKnown                 bool `json:"slot_replica_count_known"`
	NoOtherDrainingNode                   bool `json:"no_other_draining_node"`
	NoActiveHashslotMigrations            bool `json:"no_active_hashslot_migrations"`
	NoRunningOnboarding                   bool `json:"no_running_onboarding"`
	NoActiveReconcileTasksInvolvingTarget bool `json:"no_active_reconcile_tasks_involving_target"`
	NoFailedReconcileTasks                bool `json:"no_failed_reconcile_tasks"`
	RuntimeViewsCompleteAndFresh          bool `json:"runtime_views_complete_and_fresh"`
	AllSlotsHaveQuorum                    bool `json:"all_slots_have_quorum"`
	TargetNotUniqueHealthyReplica         bool `json:"target_not_unique_healthy_replica"`
}

// nodeScaleInProgressDTO contains live scale-in progress counters.
type nodeScaleInProgressDTO struct {
	AssignedSlotReplicas          int  `json:"assigned_slot_replicas"`
	ObservedSlotReplicas          int  `json:"observed_slot_replicas"`
	SlotLeaders                   int  `json:"slot_leaders"`
	ActiveTasksInvolvingNode      int  `json:"active_tasks_involving_node"`
	ActiveMigrationsInvolvingNode int  `json:"active_migrations_involving_node"`
	ActiveConnections             int  `json:"active_connections"`
	ClosingConnections            int  `json:"closing_connections"`
	GatewaySessions               int  `json:"gateway_sessions"`
	ActiveConnectionsUnknown      bool `json:"active_connections_unknown"`
}

// nodeScaleInRuntimeSummaryDTO exposes runtime counters used for connection safety.
type nodeScaleInRuntimeSummaryDTO struct {
	NodeID               uint64         `json:"node_id"`
	ActiveOnline         int            `json:"active_online"`
	ClosingOnline        int            `json:"closing_online"`
	TotalOnline          int            `json:"total_online"`
	GatewaySessions      int            `json:"gateway_sessions"`
	SessionsByListener   map[string]int `json:"sessions_by_listener"`
	AcceptingNewSessions bool           `json:"accepting_new_sessions"`
	Draining             bool           `json:"draining"`
	Unknown              bool           `json:"unknown"`
}

// nodeScaleInBlockedReasonDTO describes one stable blocked reason.
type nodeScaleInBlockedReasonDTO struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int    `json:"count"`
	SlotID  uint32 `json:"slot_id"`
	NodeID  uint64 `json:"node_id"`
}

// nodeScaleInLeaderDTO is reserved for leader transfer suggestions.
type nodeScaleInLeaderDTO struct {
	SlotID             uint32   `json:"slot_id"`
	CurrentLeaderID    uint64   `json:"current_leader_id"`
	TransferCandidates []uint64 `json:"transfer_candidates"`
}

type nodeScaleInErrorBody struct {
	Error   string               `json:"error"`
	Message string               `json:"message"`
	Report  nodeScaleInReportDTO `json:"report"`
}

func (s *Server) handleNodeScaleInPlan(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseNodeIDParam(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	var req nodeScaleInPlanRequest
	if err := bindOptionalJSON(c, &req); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	report, err := s.management.PlanNodeScaleIn(c.Request.Context(), nodeID, managementusecase.NodeScaleInPlanRequest{
		ConfirmStatefulSetTail: req.ConfirmStatefulSetTail,
		ExpectedTailNodeID:     req.ExpectedTailNodeID,
	})
	if err != nil {
		handleNodeScaleInError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeScaleInReportDTOFromUsecase(report))
}

func (s *Server) handleNodeScaleInStart(c *gin.Context) {
	s.handleNodeScaleInPlanAction(c, func(nodeID uint64, req managementusecase.NodeScaleInPlanRequest) (managementusecase.NodeScaleInReport, error) {
		return s.management.StartNodeScaleIn(c.Request.Context(), nodeID, req)
	})
}

func (s *Server) handleNodeScaleInStatus(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseNodeIDParam(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	report, err := s.management.GetNodeScaleInStatus(c.Request.Context(), nodeID)
	if err != nil {
		handleNodeScaleInError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeScaleInReportDTOFromUsecase(report))
}

func (s *Server) handleNodeScaleInAdvance(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseNodeIDParam(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	var req advanceNodeScaleInRequest
	if err := bindOptionalJSON(c, &req); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	if req.MaxLeaderTransfers > managerMaxScaleInLeaderTransfers {
		req.MaxLeaderTransfers = managerMaxScaleInLeaderTransfers
	}
	report, err := s.management.AdvanceNodeScaleIn(c.Request.Context(), nodeID, managementusecase.AdvanceNodeScaleInRequest{
		MaxLeaderTransfers:    req.MaxLeaderTransfers,
		ForceCloseConnections: req.ForceCloseConnections,
	})
	if err != nil {
		handleNodeScaleInError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeScaleInReportDTOFromUsecase(report))
}

func (s *Server) handleNodeScaleInCancel(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseNodeIDParam(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	report, err := s.management.CancelNodeScaleIn(c.Request.Context(), nodeID)
	if err != nil {
		handleNodeScaleInError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeScaleInReportDTOFromUsecase(report))
}

func (s *Server) handleNodeScaleInPlanAction(c *gin.Context, action func(uint64, managementusecase.NodeScaleInPlanRequest) (managementusecase.NodeScaleInReport, error)) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseNodeIDParam(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	var body nodeScaleInPlanRequest
	if err := bindOptionalJSON(c, &body); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	report, err := action(nodeID, managementusecase.NodeScaleInPlanRequest{
		ConfirmStatefulSetTail: body.ConfirmStatefulSetTail,
		ExpectedTailNodeID:     body.ExpectedTailNodeID,
	})
	if err != nil {
		handleNodeScaleInError(c, err)
		return
	}
	c.JSON(http.StatusOK, nodeScaleInReportDTOFromUsecase(report))
}

func bindOptionalJSON(c *gin.Context, dst any) error {
	if c.Request == nil || c.Request.Body == nil {
		return nil
	}
	if err := c.ShouldBindJSON(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

func handleNodeScaleInError(c *gin.Context, err error) {
	var reportErr *managementusecase.NodeScaleInReportError
	switch {
	case errors.As(err, &reportErr) && errors.Is(reportErr.Err, managementusecase.ErrNodeScaleInBlocked):
		c.AbortWithStatusJSON(http.StatusConflict, nodeScaleInErrorResponse("scale_in_blocked", "scale-in blocked", reportErr.Report))
	case errors.As(err, &reportErr) && errors.Is(reportErr.Err, managementusecase.ErrInvalidNodeScaleInState):
		c.AbortWithStatusJSON(http.StatusConflict, nodeScaleInErrorResponse("invalid_scale_in_state", "invalid scale-in state", reportErr.Report))
	case errors.Is(err, controllermeta.ErrNotFound):
		jsonError(c, http.StatusNotFound, "node_not_found", "node not found")
	case errors.Is(err, raftcluster.ErrObservationNotReady):
		jsonError(c, http.StatusServiceUnavailable, "controller_observation_not_ready", "controller observation not ready")
	case leaderConsistentReadUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "controller_unavailable", "controller leader unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func nodeScaleInErrorResponse(code, message string, report managementusecase.NodeScaleInReport) nodeScaleInErrorBody {
	return nodeScaleInErrorBody{Error: code, Message: message, Report: nodeScaleInReportDTOFromUsecase(report)}
}

func nodeScaleInReportDTOFromUsecase(report managementusecase.NodeScaleInReport) nodeScaleInReportDTO {
	blocked := scaleInBlockedReasonSet(report.BlockedReasons)
	return nodeScaleInReportDTO{
		NodeID:                   report.NodeID,
		Status:                   string(report.Status),
		SafeToRemove:             report.SafeToRemove,
		CanStart:                 report.Status == managementusecase.NodeScaleInStatusNotStarted && len(report.BlockedReasons) == 0,
		CanAdvance:               nodeScaleInCanAdvance(report),
		CanCancel:                nodeScaleInCanCancel(report),
		ConnectionSafetyVerified: report.ConnectionSafetyVerified,
		BlockedReasons:           nodeScaleInBlockedReasonDTOs(report.BlockedReasons),
		Checks: nodeScaleInChecksDTO{
			TargetExists:                          report.Checks.TargetNodeFound,
			TargetIsDataNode:                      report.Checks.TargetIsDataNode,
			TargetIsActiveOrDraining:              report.Checks.TargetActiveOrDraining,
			TargetIsNotControllerVoter:            !blocked["target_is_controller_voter"],
			TailNodeMappingVerified:               report.Checks.TailNodeMappingVerified,
			RemainingDataNodesEnough:              !blocked["remaining_data_nodes_insufficient"],
			ControllerLeaderAvailable:             report.Checks.ControllerReadsAvailable,
			SlotReplicaCountKnown:                 report.Checks.SlotReplicaCountKnown,
			NoOtherDrainingNode:                   !blocked["other_draining_node_exists"],
			NoActiveHashslotMigrations:            !blocked["active_hashslot_migrations_exist"],
			NoRunningOnboarding:                   !blocked["running_onboarding_exists"],
			NoActiveReconcileTasksInvolvingTarget: !blocked["active_reconcile_tasks_involving_target"],
			NoFailedReconcileTasks:                !blocked["failed_reconcile_tasks_exist"],
			RuntimeViewsCompleteAndFresh:          report.Checks.RuntimeViewsFresh,
			AllSlotsHaveQuorum:                    !blocked["slot_quorum_lost"],
			TargetNotUniqueHealthyReplica:         !blocked["target_unique_healthy_replica"],
		},
		Progress: nodeScaleInProgressDTO{
			AssignedSlotReplicas:          report.Progress.AssignedSlotReplicas,
			ObservedSlotReplicas:          report.Progress.ObservedSlotReplicas,
			SlotLeaders:                   report.Progress.SlotLeaders,
			ActiveTasksInvolvingNode:      report.Progress.ActiveTasksInvolvingNode,
			ActiveMigrationsInvolvingNode: report.Progress.ActiveMigrationsInvolvingNode,
			ActiveConnections:             report.Progress.ActiveConnections,
			ClosingConnections:            report.Progress.ClosingConnections,
			GatewaySessions:               report.Progress.GatewaySessions,
			ActiveConnectionsUnknown:      report.Progress.ActiveConnectionsUnknown,
		},
		Runtime: nodeScaleInRuntimeSummaryDTO{
			NodeID:               report.Runtime.NodeID,
			ActiveOnline:         report.Runtime.ActiveOnline,
			ClosingOnline:        report.Runtime.ClosingOnline,
			TotalOnline:          report.Runtime.TotalOnline,
			GatewaySessions:      report.Runtime.GatewaySessions,
			SessionsByListener:   cloneStringIntMap(report.Runtime.SessionsByListener),
			AcceptingNewSessions: report.Runtime.AcceptingNewSessions,
			Draining:             report.Runtime.Draining,
			Unknown:              report.Runtime.Unknown,
		},
		Leaders:    []nodeScaleInLeaderDTO{},
		NextAction: nodeScaleInNextAction(report),
	}
}

func nodeScaleInBlockedReasonDTOs(reasons []managementusecase.NodeScaleInBlockedReason) []nodeScaleInBlockedReasonDTO {
	out := make([]nodeScaleInBlockedReasonDTO, 0, len(reasons))
	for _, reason := range reasons {
		out = append(out, nodeScaleInBlockedReasonDTO{
			Code:    reason.Code,
			Message: reason.Message,
			Count:   reason.Count,
			SlotID:  reason.SlotID,
			NodeID:  reason.NodeID,
		})
	}
	return out
}

func scaleInBlockedReasonSet(reasons []managementusecase.NodeScaleInBlockedReason) map[string]bool {
	out := make(map[string]bool, len(reasons))
	for _, reason := range reasons {
		out[reason.Code] = true
	}
	return out
}

func nodeScaleInCanAdvance(report managementusecase.NodeScaleInReport) bool {
	if len(report.BlockedReasons) > 0 || report.SafeToRemove {
		return false
	}
	switch report.Status {
	case managementusecase.NodeScaleInStatusMigratingReplicas,
		managementusecase.NodeScaleInStatusTransferringLeaders,
		managementusecase.NodeScaleInStatusWaitingConnections:
		return true
	default:
		return false
	}
}

func nodeScaleInCanCancel(report managementusecase.NodeScaleInReport) bool {
	return report.Status != managementusecase.NodeScaleInStatusNotStarted && report.Status != "" && !report.SafeToRemove
}

func nodeScaleInNextAction(report managementusecase.NodeScaleInReport) string {
	switch report.Status {
	case managementusecase.NodeScaleInStatusNotStarted:
		return "start"
	case managementusecase.NodeScaleInStatusMigratingReplicas:
		return "wait_reconcile_tasks"
	case managementusecase.NodeScaleInStatusTransferringLeaders:
		return "transfer_leaders"
	case managementusecase.NodeScaleInStatusWaitingConnections:
		return "wait_connections"
	case managementusecase.NodeScaleInStatusReadyToRemove:
		return "remove_pod"
	default:
		return ""
	}
}

func cloneStringIntMap(values map[string]int) map[string]int {
	if len(values) == 0 {
		return map[string]int{}
	}
	out := make(map[string]int, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
