package management

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	// DefaultDynamicNodeDiagnosticTaskLimit is the default number of controller tasks shown in diagnostics.
	DefaultDynamicNodeDiagnosticTaskLimit = 20
	// MaxDynamicNodeDiagnosticTaskLimit is the maximum accepted task limit in diagnostics.
	MaxDynamicNodeDiagnosticTaskLimit = 50
	// DefaultDynamicNodeDiagnosticAuditLimit is the default number of task audits shown in diagnostics.
	DefaultDynamicNodeDiagnosticAuditLimit = 10
	// MaxDynamicNodeDiagnosticAuditLimit is the maximum accepted audit limit in diagnostics.
	MaxDynamicNodeDiagnosticAuditLimit = 20
	// DefaultDynamicNodeDiagnosticSlotLimit is the default number of Slot rows shown in diagnostics.
	DefaultDynamicNodeDiagnosticSlotLimit = 256
	// MaxDynamicNodeDiagnosticSlotLimit is the maximum accepted Slot limit in diagnostics.
	MaxDynamicNodeDiagnosticSlotLimit = 256
)

// ErrDynamicNodeDiagnosticsNotFound is returned when the requested node is not present in control state.
var ErrDynamicNodeDiagnosticsNotFound = errors.New("internalv2/usecase/management: dynamic node diagnostics not found")

// DynamicNodeDiagnosticsRequest selects one node and bounded projection fields for a diagnostics read.
type DynamicNodeDiagnosticsRequest struct {
	// NodeID is the durable node identity being diagnosed.
	NodeID uint64
	// TaskLimit is the maximum number of active controller tasks to include.
	TaskLimit int
	// AuditLimit is the maximum number of task-audit snapshots to include.
	AuditLimit int
	// SlotLimit is the maximum number of Slots to include.
	SlotLimit int
}

// DynamicNodeDiagnosticsResponse contains a compact, bounded diagnostics projection for one node.
type DynamicNodeDiagnosticsResponse struct {
	// GeneratedAt is when this response was assembled.
	GeneratedAt time.Time
	// StateRevision is the control-plane revision used by this projection.
	StateRevision uint64
	// NodeID is the selected node identity.
	NodeID uint64
	// Node is the manager-facing node projection.
	Node Node
	// ScaleIn is the active scale-in status when the target node is in leaving state.
	ScaleIn *NodeScaleInStatusResponse
	// Onboarding is the onboarding task status when the target node is not leaving.
	Onboarding *NodeOnboardingStatusResponse
	// ActiveTasks contains controller tasks that reference the target node.
	ActiveTasks []ControllerTask
	// TaskAudits contains retained task-audit snapshots for the selected node.
	TaskAudits []ControllerTaskAuditSnapshot
	// Slots contains Slots related to the target node by desired peers or active tasks.
	Slots []DynamicNodeDiagnosticSlot
	// Summary is the compact health/recommendation projection for the diagnostics page.
	Summary DynamicNodeDiagnosticSummary
	// Sources records whether each dependency read path succeeded.
	Sources DynamicNodeDiagnosticSources
	// Warnings contains non-fatal caveats discovered while assembling the projection.
	Warnings []string
}

// DynamicNodeDiagnosticSlot summarizes one Slot entry for node diagnostics.
type DynamicNodeDiagnosticSlot struct {
	// SlotID is the physical Slot identifier.
	SlotID uint32
	// DesiredPeers is the desired peer set from control assignment.
	DesiredPeers []uint64
	// PreferredLeader is the controller-preferred leader for this Slot.
	PreferredLeader uint64
	// ConfigEpoch is the desired assignment epoch for this Slot.
	ConfigEpoch uint64
	// TaskID is the latest relevant task identifier for this Slot, if available.
	TaskID string
	// TaskKind is the latest relevant task kind for this Slot, if available.
	TaskKind string
	// TaskStep is the latest relevant task step for this Slot, if available.
	TaskStep string
	// TaskStatus is the latest relevant task status for this Slot, if available.
	TaskStatus string
	// CurrentLeader is the runtime Slot leader if readable.
	CurrentLeader uint64
	// CurrentVoters is the runtime Slot voters if readable.
	CurrentVoters []uint64
}

// DynamicNodeDiagnosticSummary contains compact operator-facing diagnostics summary fields.
type DynamicNodeDiagnosticSummary struct {
	// SafeToRemove reports whether the node currently appears safe to remove.
	SafeToRemove bool
	// BlockedReasons contains bounded machine-readable reasons blocking removal or finalization.
	BlockedReasons []string
	// SlotLeaderCount counts live Slot leaders still observed on the target node.
	SlotLeaderCount int
	// ActiveTaskCount counts controller tasks in pending/running state.
	ActiveTaskCount int
	// FailedTaskCount counts controller tasks in failed state.
	FailedTaskCount int
	// SlotReplicaCount counts desired Slots that still list the target node.
	SlotReplicaCount int
	// SlotReplicaMoveState summarizes the leading slot_replica_move blocker state.
	SlotReplicaMoveState string
	// ControlRevisionGap is the control revision gap when readers report stale state.
	ControlRevisionGap uint64
	// AuditAvailable reports whether task-audit reads were available.
	AuditAvailable bool
	// SlotRuntimeUnknown reports whether Slot runtime observations were unavailable.
	SlotRuntimeUnknown bool
	// RuntimeUnknown reports whether target runtime counters were unavailable.
	RuntimeUnknown bool
	// OldestTaskAgeSeconds reports the age of the oldest requested audit entry in seconds.
	OldestTaskAgeSeconds int64
	// RecommendedNextAction is the immediate operator recommendation.
	RecommendedNextAction string
	// BlockedByControlRevision reports that the node still has control-revision staleness.
	BlockedByControlRevision bool
	// BlockedBySlots reports that desired Slot replicas still reference this node.
	BlockedBySlots bool
	// BlockedByTasks reports active or failed task blockers are present.
	BlockedByTasks bool
}

// DynamicNodeDiagnosticSources reports source availability for each dependencies read path.
type DynamicNodeDiagnosticSources struct {
	// ControlSnapshot reports whether control snapshot read succeeded.
	ControlSnapshot DynamicNodeDiagnosticSource
	// TaskAudit reports whether task-audit reads succeeded.
	TaskAudit DynamicNodeDiagnosticSource
	// SlotRuntime reports whether Slot runtime reads succeeded for collected Slots.
	SlotRuntime DynamicNodeDiagnosticSource
}

// DynamicNodeDiagnosticSource captures source availability and failure reason for one dependency.
type DynamicNodeDiagnosticSource struct {
	// Available reports if this dependency was read successfully.
	Available bool
	// LastError preserves a bounded warning text for unavailable reads.
	LastError string
}

// DynamicNodeDiagnostics returns a bounded, read-only diagnostics view for one node.
func (a *App) DynamicNodeDiagnostics(ctx context.Context, req DynamicNodeDiagnosticsRequest) (DynamicNodeDiagnosticsResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return DynamicNodeDiagnosticsResponse{}, err
	}
	if req.NodeID == 0 {
		return DynamicNodeDiagnosticsResponse{}, metadb.ErrInvalidArgument
	}
	taskLimit, err := normalizeDynamicNodeDiagnosticTaskLimit(req.TaskLimit)
	if err != nil {
		return DynamicNodeDiagnosticsResponse{}, err
	}
	auditLimit, err := normalizeDynamicNodeDiagnosticAuditLimit(req.AuditLimit)
	if err != nil {
		return DynamicNodeDiagnosticsResponse{}, err
	}
	slotLimit, err := normalizeDynamicNodeDiagnosticSlotLimit(req.SlotLimit)
	if err != nil {
		return DynamicNodeDiagnosticsResponse{}, err
	}
	if a == nil || a.cluster == nil {
		return DynamicNodeDiagnosticsResponse{}, ErrNodeScaleInUnavailable
	}

	response := DynamicNodeDiagnosticsResponse{
		GeneratedAt: a.now(),
		TaskAudits:  make([]ControllerTaskAuditSnapshot, 0),
		ActiveTasks: make([]ControllerTask, 0),
		Slots:       make([]DynamicNodeDiagnosticSlot, 0),
		Sources:     DynamicNodeDiagnosticSources{},
		Warnings:    make([]string, 0),
		NodeID:      req.NodeID,
	}
	response.Sources.ControlSnapshot.Available = true

	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return DynamicNodeDiagnosticsResponse{}, err
	}
	response.StateRevision = snapshot.Revision

	node, ok := findControlNode(snapshot, req.NodeID)
	if !ok {
		return DynamicNodeDiagnosticsResponse{}, ErrDynamicNodeDiagnosticsNotFound
	}
	response.Node = buildNode(nodeBuildOptions{
		node:               node,
		slots:              a.summarizeSlots(ctx, snapshot.Slots),
		runtime:            a.nodeRuntimeSummary(ctx, req.NodeID),
		localNodeID:        a.cluster.NodeID(),
		controllerLeaderID: snapshot.ControllerID,
		generatedAt:        response.GeneratedAt,
	})

	joinedTasks := joinStateTasksForNode(snapshot.Tasks, req.NodeID)
	taskSnapshots := joinedTasks.snapshots()
	response.ActiveTasks = joinedTasks.controllerTasks(taskLimit)
	response.Summary.ActiveTaskCount, response.Summary.FailedTaskCount = dynamicNodeDiagnosticTaskCounts(taskSnapshots)
	response.Summary.BlockedByTasks = response.Summary.ActiveTaskCount > 0 || response.Summary.FailedTaskCount > 0
	taskRowsBySlot := tasksBySlot(taskSnapshots)
	slotRows, slotRuntimeSourceAvailable, slotRuntimeSourceError := buildDynamicNodeDiagnosticSlots(ctx, req.NodeID, snapshot.Slots, taskRowsBySlot, a.slotRuntimeStatus, slotLimit)
	response.Slots = slotRows
	response.Summary.SlotRuntimeUnknown = !slotRuntimeSourceAvailable
	if slotRuntimeSourceError != "" {
		response.Warnings = append(response.Warnings, slotRuntimeSourceError)
		response.Sources.SlotRuntime.Available = false
		response.Sources.SlotRuntime.LastError = slotRuntimeSourceError
	} else {
		response.Sources.SlotRuntime.Available = true
	}

	var sourceAuditError error
	response.TaskAudits, response.Sources.TaskAudit, sourceAuditError = a.dynamicNodeDiagnosticAudits(ctx, req.NodeID, auditLimit)
	if sourceAuditError != nil {
		return DynamicNodeDiagnosticsResponse{}, sourceAuditError
	}
	response.Summary.AuditAvailable = response.Sources.TaskAudit.Available
	if !response.Summary.AuditAvailable && response.Sources.TaskAudit.LastError != "" {
		response.Warnings = append(response.Warnings, response.Sources.TaskAudit.LastError)
	}
	response.Summary.OldestTaskAgeSeconds = dynamicNodeDiagnosticOldestTaskAgeSeconds(response.GeneratedAt, taskSnapshots, response.TaskAudits)

	response.Summary.SlotReplicaMoveState = joinStateTaskReplicaMoveState(taskSnapshots)

	joinState := managerControlJoinState(node.JoinState)
	if joinState == control.NodeJoinStateLeaving {
		scaleInStatus := a.nodeScaleInStatusFromSnapshot(ctx, snapshot, req.NodeID, nil)
		response.ScaleIn = &scaleInStatus
		response.Summary.BlockedReasons = append(response.Summary.BlockedReasons, scaleInStatus.BlockedReasons...)
		if scaleInStatus.BlockedByTasks {
			appendDynamicNodeDiagnosticBlockedReason(&response.Summary, "blocked_by_tasks")
		}
		response.Summary.SafeToRemove = scaleInStatus.SafeToRemove
		response.Summary.SlotReplicaCount = scaleInStatus.SlotReplicaCount
		response.Summary.SlotLeaderCount = scaleInStatus.SlotLeaderCount
		response.Summary.RuntimeUnknown = scaleInStatus.RuntimeUnknown || scaleInStatus.UnknownRuntime
		response.Summary.BlockedByControlRevision = scaleInStatus.BlockedByControlRevision
		response.Summary.BlockedBySlots = scaleInStatus.BlockedBySlots
		response.Summary.BlockedByTasks = scaleInStatus.BlockedByTasks
		response.Summary.SlotRuntimeUnknown = response.Summary.SlotRuntimeUnknown || scaleInStatus.BlockedBySlotRuntime
		response.Summary.ControlRevisionGap = nodeScaleInStatusControlRevisionGap(scaleInStatus)
	} else {
		if isActiveDataNode(node) {
			onboardingStatus := nodeOnboardingStatusFromSnapshot(snapshot, req.NodeID, response.GeneratedAt)
			response.Onboarding = &onboardingStatus
		}
		response.Summary.SlotReplicaCount = countSlotReplicas(snapshot.Slots, req.NodeID)
		response.Summary.BlockedBySlots = response.Summary.SlotReplicaCount > 0
	}

	response.Summary.RecommendedNextAction = summarizeDynamicNodeDiagnosticNextAction(response.ScaleIn != nil, response.Summary)
	response.NodeID = req.NodeID

	return response, nil
}

type dynamicNodeDiagnosticJoinedTasks struct {
	controlTasks []control.ReconcileTask
}

func (j dynamicNodeDiagnosticJoinedTasks) controllerTasks(limit int) []ControllerTask {
	tasks := make([]ControllerTask, 0, len(j.controlTasks))
	for _, task := range j.controlTasks {
		tasks = append(tasks, controllerTaskFromControl(task))
	}
	if len(tasks) <= limit {
		return tasks
	}
	return tasks[:limit]
}

func joinStateTasksForNode(tasks []control.ReconcileTask, nodeID uint64) dynamicNodeDiagnosticJoinedTasks {
	filtered := make([]control.ReconcileTask, 0, len(tasks))
	for _, task := range tasks {
		if controllerTaskHasNode(task, nodeID) {
			filtered = append(filtered, task)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].SlotID != filtered[j].SlotID {
			return filtered[i].SlotID < filtered[j].SlotID
		}
		if string(filtered[i].Kind) != string(filtered[j].Kind) {
			return string(filtered[i].Kind) < string(filtered[j].Kind)
		}
		return filtered[i].TaskID < filtered[j].TaskID
	})
	return dynamicNodeDiagnosticJoinedTasks{controlTasks: filtered}
}

func (j dynamicNodeDiagnosticJoinedTasks) snapshots() []control.ReconcileTask {
	return append([]control.ReconcileTask(nil), j.controlTasks...)
}

func normalizeDynamicNodeDiagnosticTaskLimit(limit int) (int, error) {
	if limit < 0 || limit > MaxDynamicNodeDiagnosticTaskLimit {
		return 0, metadb.ErrInvalidArgument
	}
	if limit == 0 {
		return DefaultDynamicNodeDiagnosticTaskLimit, nil
	}
	return limit, nil
}

func normalizeDynamicNodeDiagnosticAuditLimit(limit int) (int, error) {
	if limit < 0 || limit > MaxDynamicNodeDiagnosticAuditLimit {
		return 0, metadb.ErrInvalidArgument
	}
	if limit == 0 {
		return DefaultDynamicNodeDiagnosticAuditLimit, nil
	}
	return limit, nil
}

func normalizeDynamicNodeDiagnosticSlotLimit(limit int) (int, error) {
	if limit < 0 || limit > MaxDynamicNodeDiagnosticSlotLimit {
		return 0, metadb.ErrInvalidArgument
	}
	if limit == 0 {
		return DefaultDynamicNodeDiagnosticSlotLimit, nil
	}
	return limit, nil
}

func tasksBySlot(tasks []control.ReconcileTask) map[uint32]control.ReconcileTask {
	slots := map[uint32]control.ReconcileTask{}
	for _, task := range tasks {
		if _, exists := slots[task.SlotID]; exists {
			continue
		}
		slots[task.SlotID] = task
	}
	return slots
}

func buildDynamicNodeDiagnosticSlots(
	ctx context.Context,
	nodeID uint64,
	assignments []control.SlotAssignment,
	taskBySlot map[uint32]control.ReconcileTask,
	slotRuntime SlotRuntimeStatusReader,
	limit int,
) ([]DynamicNodeDiagnosticSlot, bool, string) {
	slotByID := map[uint32]control.SlotAssignment{}
	for _, assignment := range assignments {
		slotByID[assignment.SlotID] = assignment
		if containsUint64(assignment.DesiredPeers, nodeID) {
			if _, ok := taskBySlot[assignment.SlotID]; ok {
				continue
			}
			taskBySlot[assignment.SlotID] = control.ReconcileTask{SlotID: assignment.SlotID}
		}
	}

	slotIDs := make([]uint32, 0, len(taskBySlot))
	for slotID := range taskBySlot {
		slotIDs = append(slotIDs, slotID)
	}
	sort.Slice(slotIDs, func(i, j int) bool { return slotIDs[i] < slotIDs[j] })
	if len(slotIDs) > limit {
		slotIDs = slotIDs[:limit]
	}

	out := make([]DynamicNodeDiagnosticSlot, 0, len(slotIDs))
	slotRuntimeAvailable := slotRuntime != nil
	slotRuntimeError := ""
	if !slotRuntimeAvailable {
		slotRuntimeError = "slot runtime unavailable for node diagnostics"
	}
	if len(slotIDs) == 0 {
		if slotRuntimeError != "" {
			return out, false, slotRuntimeError
		}
		return out, true, ""
	}
	for _, slotID := range slotIDs {
		assignment, ok := slotByID[slotID]
		slot := DynamicNodeDiagnosticSlot{
			SlotID:       slotID,
			DesiredPeers: append([]uint64(nil), assignment.DesiredPeers...),
		}
		if task, ok := taskBySlot[slotID]; ok {
			slot.TaskID = task.TaskID
			slot.TaskKind = string(task.Kind)
			slot.TaskStep = string(task.Step)
			slot.TaskStatus = string(task.Status)
		}
		if ok {
			slot.PreferredLeader = assignment.PreferredLeader
			slot.ConfigEpoch = assignment.ConfigEpoch
		}
		if slotRuntime == nil {
			slotRuntimeAvailable = false
		} else {
			runtime, err := slotRuntime.SlotRuntimeStatus(ctx, slotID, append([]uint64(nil), assignment.DesiredPeers...))
			if err != nil {
				slotRuntimeAvailable = false
				slotRuntimeError = "slot runtime unavailable for node diagnostics"
				continue
			}
			slot.CurrentLeader = runtime.LeaderID
			slot.CurrentVoters = append([]uint64(nil), runtime.CurrentVoters...)
		}
		out = append(out, slot)
	}
	return out, slotRuntimeAvailable, slotRuntimeError
}

func (a *App) dynamicNodeDiagnosticAudits(ctx context.Context, nodeID uint64, limit int) ([]ControllerTaskAuditSnapshot, DynamicNodeDiagnosticSource, error) {
	if a == nil || a.controllerTaskAudit == nil {
		return nil, DynamicNodeDiagnosticSource{
			Available: false,
			LastError: "task audit unavailable",
		}, nil
	}
	response, err := a.controllerTaskAudit.ListControllerTaskAudits(ctx, ControllerTaskAuditListRequest{
		NodeID: nodeID,
		Limit:  limit,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, DynamicNodeDiagnosticSource{}, err
		}
		if errors.Is(err, ErrControllerTaskAuditUnavailable) {
			return nil, DynamicNodeDiagnosticSource{
				Available: false,
				LastError: "task audit unavailable",
			}, nil
		}
		return nil, DynamicNodeDiagnosticSource{Available: false, LastError: "task audit unavailable: " + err.Error()}, nil
	}
	return response.Items, DynamicNodeDiagnosticSource{Available: true}, nil
}

func dynamicNodeDiagnosticTaskCounts(tasks []control.ReconcileTask) (active int, failed int) {
	for _, task := range tasks {
		switch task.Status {
		case control.TaskStatusPending, control.TaskStatusRunning:
			active++
		case control.TaskStatusFailed:
			failed++
		}
	}
	return active, failed
}

func dynamicNodeDiagnosticOldestTaskAgeSeconds(generatedAt time.Time, tasks []control.ReconcileTask, audits []ControllerTaskAuditSnapshot) int64 {
	if generatedAt.IsZero() {
		return 0
	}
	relevantTasks := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		switch task.Status {
		case control.TaskStatusPending, control.TaskStatusRunning, control.TaskStatusFailed:
			relevantTasks[task.TaskID] = struct{}{}
		}
	}
	if len(relevantTasks) == 0 {
		return 0
	}
	var oldest *time.Time
	for _, item := range audits {
		if item.TaskID == "" || item.StartedAt.IsZero() {
			continue
		}
		if _, ok := relevantTasks[item.TaskID]; !ok {
			continue
		}
		if oldest == nil || item.StartedAt.Before(*oldest) {
			at := item.StartedAt
			oldest = &at
		}
	}
	if oldest == nil {
		return 0
	}
	age := generatedAt.Sub(*oldest).Seconds()
	if age < 0 {
		return 0
	}
	return int64(age)
}

func summarizeDynamicNodeDiagnosticNextAction(hasScaleIn bool, summary DynamicNodeDiagnosticSummary) string {
	switch {
	case summary.ActiveTaskCount > 0 || summary.FailedTaskCount > 0:
		return "inspect_controller_task"
	case summary.ControlRevisionGap > 0:
		return "wait_control_revision"
	case summary.SlotRuntimeUnknown:
		return "inspect_slot_runtime"
	case summary.RuntimeUnknown:
		return "inspect_runtime"
	case summary.BlockedBySlots && hasScaleIn:
		return "advance_slot_drain"
	case summary.SafeToRemove:
		return "ready_to_remove"
	default:
		return "no_action"
	}
}

func nodeScaleInStatusControlRevisionGap(status NodeScaleInStatusResponse) uint64 {
	if status.ObservedControlRevision > status.RequiredControlRevision {
		return 0
	}
	if status.RequiredControlRevision > status.ObservedControlRevision {
		return status.RequiredControlRevision - status.ObservedControlRevision
	}
	return 0
}

func joinStateTaskReplicaMoveState(tasks []control.ReconcileTask) string {
	hasSlotReplicaMove := false
	for _, task := range tasks {
		if task.Kind != control.TaskKindSlotReplicaMove {
			continue
		}
		hasSlotReplicaMove = true
		if task.Status == control.TaskStatusFailed {
			return "task_failed"
		}
	}
	if !hasSlotReplicaMove {
		return "no_active_move"
	}
	for _, task := range tasks {
		if task.Kind != control.TaskKindSlotReplicaMove || !isActiveDiagnosticTask(task.Status) {
			continue
		}
		if task.ObservedConfigIndex == 0 && len(task.ObservedVoters) == 0 && len(task.ObservedLearners) == 0 {
			return "phase_observation_missing"
		}
		switch task.Step {
		case control.TaskStepRemoveVoter, control.TaskStepTransferLeader:
			return "waiting_leader_transfer"
		case control.TaskStepOpenLearner, control.TaskStepAddLearner, control.TaskStepPromoteLearner:
			return "waiting_learner_catchup"
		}
		return "unknown"
	}
	return "unknown"
}

func isActiveDiagnosticTask(status control.TaskStatus) bool {
	return status == control.TaskStatusPending || status == control.TaskStatusRunning
}

func appendDynamicNodeDiagnosticBlockedReason(summary *DynamicNodeDiagnosticSummary, reason string) {
	if summary == nil || reason == "" {
		return
	}
	for _, existing := range summary.BlockedReasons {
		if existing == reason {
			return
		}
	}
	summary.BlockedReasons = append(summary.BlockedReasons, reason)
}

func countSlotReplicas(assignments []control.SlotAssignment, nodeID uint64) int {
	count := 0
	for _, assignment := range assignments {
		if containsUint64(assignment.DesiredPeers, nodeID) {
			count++
		}
	}
	return count
}
