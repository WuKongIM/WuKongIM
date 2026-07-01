package manager

import (
	"errors"
	"net/http"
	"strconv"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// ManagerDynamicNodeDiagnosticsResponse is the manager diagnostics response for one node.
type ManagerDynamicNodeDiagnosticsResponse struct {
	// GeneratedAt is when the diagnostics projection was assembled.
	GeneratedAt string `json:"generated_at"`
	// StateRevision is the control snapshot revision used to build this response.
	StateRevision uint64 `json:"state_revision"`
	// NodeID is the diagnosed node ID.
	NodeID uint64 `json:"node_id"`
	// Node is the manager-facing node projection.
	Node NodeDTO `json:"node"`
	// ScaleIn contains scale-in evidence when the node is leaving.
	ScaleIn *ManagerNodeScaleInStatusResponse `json:"scale_in"`
	// Onboarding contains onboarding evidence when the node is an onboarding target.
	Onboarding *ManagerNodeOnboardingStatusResponse `json:"onboarding"`
	// ActiveTasks contains active Controller tasks relevant to the node.
	ActiveTasks []ManagerControllerTask `json:"active_tasks"`
	// TaskAudits contains retained task-audit snapshots relevant to the node.
	TaskAudits []ManagerControllerTaskAuditSnapshot `json:"task_audits"`
	// Slots contains bounded Slot evidence relevant to the node.
	Slots []ManagerDynamicNodeDiagnosticSlot `json:"slots"`
	// Summary contains compact operator-facing recommendation fields.
	Summary ManagerDynamicNodeDiagnosticSummary `json:"summary"`
	// Sources reports source availability for dependency reads.
	Sources ManagerDynamicNodeDiagnosticSources `json:"sources"`
	// Warnings contains bounded non-fatal warnings.
	Warnings []string `json:"warnings"`
}

// ManagerDynamicNodeDiagnosticSlot is the manager-facing Slot evidence row.
type ManagerDynamicNodeDiagnosticSlot struct {
	// SlotID is the physical Slot ID.
	SlotID uint32 `json:"slot_id"`
	// DesiredPeers is the durable desired peer set.
	DesiredPeers []uint64 `json:"desired_peers"`
	// PreferredLeader is the controller-preferred leader for the Slot.
	PreferredLeader uint64 `json:"preferred_leader"`
	// ConfigEpoch is the Slot assignment epoch.
	ConfigEpoch uint64 `json:"config_epoch"`
	// TaskID is the related Controller task ID when available.
	TaskID string `json:"task_id"`
	// TaskKind is the related Controller task kind when available.
	TaskKind string `json:"task_kind"`
	// TaskStep is the related Controller task step when available.
	TaskStep string `json:"task_step"`
	// TaskStatus is the related Controller task status when available.
	TaskStatus string `json:"task_status"`
	// CurrentLeader is the runtime-observed Slot leader when available.
	CurrentLeader uint64 `json:"current_leader"`
	// CurrentVoters is the runtime-observed voter set when available.
	CurrentVoters []uint64 `json:"current_voters"`
}

// ManagerDynamicNodeDiagnosticSummary is the compact manager diagnostics summary.
type ManagerDynamicNodeDiagnosticSummary struct {
	// SafeToRemove reports whether the node currently appears safe to remove.
	SafeToRemove bool `json:"safe_to_remove"`
	// BlockedReasons contains bounded machine-readable blockers.
	BlockedReasons []string `json:"blocked_reasons"`
	// ActiveTaskCount is the number of active Controller task blockers.
	ActiveTaskCount int `json:"active_task_count"`
	// FailedTaskCount is the number of failed Controller task blockers.
	FailedTaskCount int `json:"failed_task_count"`
	// SlotReplicaCount is the number of desired Slot replicas still on the node.
	SlotReplicaCount int `json:"slot_replica_count"`
	// SlotLeaderCount is the number of live Slot leaders still observed on the node.
	SlotLeaderCount int `json:"slot_leader_count"`
	// ControlRevisionGap is the durable control revision gap when observed.
	ControlRevisionGap uint64 `json:"control_revision_gap"`
	// SlotReplicaMoveState is the summarized Slot replica move state.
	SlotReplicaMoveState string `json:"slot_replica_move_state"`
	// OldestTaskAgeSeconds is the oldest relevant task age in seconds.
	OldestTaskAgeSeconds int64 `json:"oldest_task_age_seconds"`
	// AuditAvailable reports whether retained task audits were available.
	AuditAvailable bool `json:"audit_available"`
	// RuntimeUnknown reports whether node runtime counters were unavailable.
	RuntimeUnknown bool `json:"runtime_unknown"`
	// SlotRuntimeUnknown reports whether Slot runtime evidence was unavailable.
	SlotRuntimeUnknown bool `json:"slot_runtime_unknown"`
	// RecommendedNextAction is the compact operator recommendation.
	RecommendedNextAction string `json:"recommended_next_action"`
}

// ManagerDynamicNodeDiagnosticSources reports dependency source availability.
type ManagerDynamicNodeDiagnosticSources struct {
	// ControlSnapshot reports control snapshot read availability.
	ControlSnapshot ManagerDynamicNodeDiagnosticSource `json:"control_snapshot"`
	// TaskAudit reports task-audit read availability.
	TaskAudit ManagerDynamicNodeDiagnosticSource `json:"task_audit"`
	// SlotRuntime reports Slot runtime read availability.
	SlotRuntime ManagerDynamicNodeDiagnosticSource `json:"slot_runtime"`
}

// ManagerDynamicNodeDiagnosticSource captures one dependency read status.
type ManagerDynamicNodeDiagnosticSource struct {
	// Available reports whether the dependency read succeeded.
	Available bool `json:"available"`
	// LastError preserves a bounded dependency failure string.
	LastError string `json:"last_error"`
}

func (s *Server) handleDynamicNodeDiagnostics(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	req, err := parseDynamicNodeDiagnosticsRequest(c)
	if err != nil {
		writeDynamicNodeDiagnosticsError(c, err)
		return
	}
	resp, err := s.management.DynamicNodeDiagnostics(c.Request.Context(), req)
	if err != nil {
		writeDynamicNodeDiagnosticsError(c, err)
		return
	}
	c.JSON(http.StatusOK, dynamicNodeDiagnosticsDTO(resp))
}

func parseDynamicNodeDiagnosticsRequest(c *gin.Context) (managementusecase.DynamicNodeDiagnosticsRequest, error) {
	nodeID, err := parseRequiredLogNodeID(c.Param("node_id"))
	if err != nil {
		return managementusecase.DynamicNodeDiagnosticsRequest{}, metadb.ErrInvalidArgument
	}
	taskLimit, err := parseDynamicNodeDiagnosticsLimit(c.Query("task_limit"), managementusecase.MaxDynamicNodeDiagnosticTaskLimit)
	if err != nil {
		return managementusecase.DynamicNodeDiagnosticsRequest{}, err
	}
	auditLimit, err := parseDynamicNodeDiagnosticsLimit(c.Query("audit_limit"), managementusecase.MaxDynamicNodeDiagnosticAuditLimit)
	if err != nil {
		return managementusecase.DynamicNodeDiagnosticsRequest{}, err
	}
	slotLimit, err := parseDynamicNodeDiagnosticsLimit(c.Query("slot_limit"), managementusecase.MaxDynamicNodeDiagnosticSlotLimit)
	if err != nil {
		return managementusecase.DynamicNodeDiagnosticsRequest{}, err
	}
	return managementusecase.DynamicNodeDiagnosticsRequest{
		NodeID:     nodeID,
		TaskLimit:  taskLimit,
		AuditLimit: auditLimit,
		SlotLimit:  slotLimit,
	}, nil
}

func dynamicNodeDiagnosticsDTO(resp managementusecase.DynamicNodeDiagnosticsResponse) ManagerDynamicNodeDiagnosticsResponse {
	activeTasks := make([]ManagerControllerTask, 0, len(resp.ActiveTasks))
	for _, task := range resp.ActiveTasks {
		activeTasks = append(activeTasks, controllerTaskDTO(task))
	}
	taskAudits := make([]ManagerControllerTaskAuditSnapshot, 0, len(resp.TaskAudits))
	for _, audit := range resp.TaskAudits {
		taskAudits = append(taskAudits, controllerTaskAuditSnapshotDTO(audit))
	}
	slots := make([]ManagerDynamicNodeDiagnosticSlot, 0, len(resp.Slots))
	for _, slot := range resp.Slots {
		slots = append(slots, ManagerDynamicNodeDiagnosticSlot{
			SlotID:          slot.SlotID,
			DesiredPeers:    append([]uint64(nil), slot.DesiredPeers...),
			PreferredLeader: slot.PreferredLeader,
			ConfigEpoch:     slot.ConfigEpoch,
			TaskID:          slot.TaskID,
			TaskKind:        slot.TaskKind,
			TaskStep:        slot.TaskStep,
			TaskStatus:      slot.TaskStatus,
			CurrentLeader:   slot.CurrentLeader,
			CurrentVoters:   append([]uint64(nil), slot.CurrentVoters...),
		})
	}
	warnings := append([]string(nil), resp.Warnings...)
	if warnings == nil {
		warnings = []string{}
	}

	dto := ManagerDynamicNodeDiagnosticsResponse{
		GeneratedAt:   managerTimeString(resp.GeneratedAt),
		StateRevision: resp.StateRevision,
		NodeID:        resp.NodeID,
		Node:          nodeDTO(resp.Node),
		ScaleIn:       nil,
		Onboarding:    nil,
		ActiveTasks:   activeTasks,
		TaskAudits:    taskAudits,
		Slots:         slots,
		Summary: ManagerDynamicNodeDiagnosticSummary{
			SafeToRemove:          resp.Summary.SafeToRemove,
			BlockedReasons:        append([]string(nil), resp.Summary.BlockedReasons...),
			ActiveTaskCount:       resp.Summary.ActiveTaskCount,
			FailedTaskCount:       resp.Summary.FailedTaskCount,
			SlotReplicaCount:      resp.Summary.SlotReplicaCount,
			SlotLeaderCount:       resp.Summary.SlotLeaderCount,
			ControlRevisionGap:    resp.Summary.ControlRevisionGap,
			SlotReplicaMoveState:  resp.Summary.SlotReplicaMoveState,
			OldestTaskAgeSeconds:  resp.Summary.OldestTaskAgeSeconds,
			AuditAvailable:        resp.Summary.AuditAvailable,
			RuntimeUnknown:        resp.Summary.RuntimeUnknown,
			SlotRuntimeUnknown:    resp.Summary.SlotRuntimeUnknown,
			RecommendedNextAction: resp.Summary.RecommendedNextAction,
		},
		Sources: ManagerDynamicNodeDiagnosticSources{
			ControlSnapshot: dynamicNodeDiagnosticSourceDTO(resp.Sources.ControlSnapshot),
			TaskAudit:       dynamicNodeDiagnosticSourceDTO(resp.Sources.TaskAudit),
			SlotRuntime:     dynamicNodeDiagnosticSourceDTO(resp.Sources.SlotRuntime),
		},
		Warnings: warnings,
	}
	if dto.Summary.BlockedReasons == nil {
		dto.Summary.BlockedReasons = []string{}
	}
	if resp.ScaleIn != nil {
		scaleIn := nodeScaleInStatusResponseDTO(*resp.ScaleIn)
		dto.ScaleIn = &scaleIn
	}
	if resp.Onboarding != nil {
		onboarding := nodeOnboardingStatusResponseDTO(*resp.Onboarding)
		dto.Onboarding = &onboarding
	}
	return dto
}

func dynamicNodeDiagnosticSourceDTO(source managementusecase.DynamicNodeDiagnosticSource) ManagerDynamicNodeDiagnosticSource {
	return ManagerDynamicNodeDiagnosticSource{
		Available: source.Available,
		LastError: source.LastError,
	}
}

func writeDynamicNodeDiagnosticsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid dynamic node diagnostics request")
	case errors.Is(err, managementusecase.ErrDynamicNodeDiagnosticsNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "dynamic node diagnostics not found")
	case controlSnapshotUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "dynamic node diagnostics unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func parseDynamicNodeDiagnosticsLimit(raw string, max int) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > max {
		return 0, metadb.ErrInvalidArgument
	}
	return value, nil
}
