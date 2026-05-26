package management

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	// ChannelMigrationKindLeaderTransfer is the manager-facing leader-transfer kind.
	ChannelMigrationKindLeaderTransfer = "leader_transfer"
	// ChannelMigrationKindReplicaReplace is the manager-facing replica-replacement kind.
	ChannelMigrationKindReplicaReplace = "replica_replace"
)

var (
	channelMigrationLeaderTransferPhaseSequence = []string{
		"validate",
		"probe_target",
		"write_fence",
		"drain_leader",
		"final_target_catch_up",
		"commit_leader_meta",
		"verify_new_leader",
		"clear_fence",
	}
	channelMigrationReplicaReplacePhaseSequence = []string{
		"validate",
		"add_learner",
		"bootstrap_target",
		"warm_catch_up",
		"cutover_fence",
		"final_target_catch_up",
		"promote_and_remove",
		"verify_membership",
		"clear_fence",
	}
	channelMigrationEmbeddedReplicaReplacePhaseSequence = joinChannelMigrationPhaseSequences(
		channelMigrationLeaderTransferPhaseSequence[:len(channelMigrationLeaderTransferPhaseSequence)-1],
		channelMigrationReplicaReplacePhaseSequence[1:],
	)
	channelMigrationPhaseSequences = map[metadb.ChannelMigrationKind][]string{
		metadb.ChannelMigrationKindLeaderTransfer: channelMigrationLeaderTransferPhaseSequence,
		metadb.ChannelMigrationKindReplicaReplace: channelMigrationReplicaReplacePhaseSequence,
	}
)

// TransferChannelLeaderRequest describes a manual channel leader transfer.
type TransferChannelLeaderRequest struct {
	// TargetNodeID is the existing ISR replica that should become leader.
	TargetNodeID uint64
	// DryRun validates safety without creating a durable migration task.
	DryRun bool
}

// MigrateChannelReplicaRequest describes one channel replica replacement.
type MigrateChannelReplicaRequest struct {
	// SourceNodeID is the ISR replica being replaced.
	SourceNodeID uint64
	// TargetNodeID is the active data node that should be added as learner.
	TargetNodeID uint64
	// DryRun validates safety without creating a durable migration task.
	DryRun bool
}

// ChannelMigrationResult is returned by dry-runs and task creation calls.
type ChannelMigrationResult struct {
	// DryRun reports whether no task was created.
	DryRun bool
	// Valid reports whether validation found no blockers.
	Valid bool
	// TaskID is set after a durable task is created.
	TaskID string
	// Kind is the stable manager-facing migration kind.
	Kind string
	// Blockers contains stable machine-readable validation blocker codes.
	Blockers []string
	// PhaseSequence is the expected durable executor phase order.
	PhaseSequence []string
	// Detail contains the current or planned task details.
	Detail ChannelMigrationDetail
}

// ChannelMigrationDetail is the manager-facing active migration task detail.
type ChannelMigrationDetail struct {
	// TaskID identifies one channel migration attempt.
	TaskID string
	// Kind is the stable manager-facing migration kind.
	Kind string
	// Status is the stable manager-facing task status.
	Status string
	// Phase is the stable manager-facing task phase.
	Phase string
	// ChannelID identifies the channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// SourceNode is the source leader or replica.
	SourceNode uint64
	// TargetNode is the target leader or replacement replica.
	TargetNode uint64
	// DesiredLeader is the requested leader for leader-transfer semantics.
	DesiredLeader uint64
	// BaseChannelEpoch is the channel epoch captured when the task was created.
	BaseChannelEpoch uint64
	// BaseLeaderEpoch is the leader epoch captured when the task was created.
	BaseLeaderEpoch uint64
	// CurrentChannelEpoch is the latest authoritative channel epoch.
	CurrentChannelEpoch uint64
	// CurrentLeaderEpoch is the latest authoritative leader epoch.
	CurrentLeaderEpoch uint64
	// Progress stores durable executor observations.
	Progress metadb.ChannelMigrationProgress
	// FenceActive reports whether the task currently owns an active write fence.
	FenceActive bool
	// FenceUntilMS is the task write-fence deadline.
	FenceUntilMS int64
	// FenceReason is the raw runtime write-fence reason.
	FenceReason uint8
	// BlockerCode is a stable blocker code when Status is blocked.
	BlockerCode string
	// BlockerMessage is the human-readable blocker detail.
	BlockerMessage string
	// Attempt is the durable retry counter.
	Attempt uint32
	// NextRunAtMS is the next executor wake-up timestamp.
	NextRunAtMS int64
	// LastError is the last retryable or terminal error.
	LastError string
	// CreatedAtMS is the creation timestamp.
	CreatedAtMS int64
	// UpdatedAtMS is the last update timestamp.
	UpdatedAtMS int64
	// CompletedAtMS is set for terminal tasks.
	CompletedAtMS int64
}

// TransferChannelLeader validates or creates a manual channel leader-transfer task.
func (a *App) TransferChannelLeader(ctx context.Context, id channel.ChannelID, req TransferChannelLeaderRequest) (ChannelMigrationResult, error) {
	if err := validateManagementChannelID(id); err != nil {
		return ChannelMigrationResult{}, err
	}
	if req.TargetNodeID == 0 {
		return ChannelMigrationResult{}, metadb.ErrInvalidArgument
	}
	meta, err := a.getMigrationRuntimeMeta(ctx, id)
	if err != nil {
		return ChannelMigrationResult{}, err
	}

	blockers, err := a.validateNoActiveChannelMigration(ctx, id)
	if err != nil {
		return ChannelMigrationResult{}, err
	}
	nodes, err := a.listMigrationNodes(ctx)
	if err != nil {
		return ChannelMigrationResult{}, err
	}
	if meta.Status != uint8(channel.StatusActive) {
		blockers = append(blockers, "channel_not_active")
	}
	if meta.Leader == 0 {
		blockers = append(blockers, "missing_leader")
	}
	if channelMigrationWriteFenceActive(meta) {
		blockers = append(blockers, "write_fence_active")
	}
	if meta.Leader != 0 && !isActiveMigrationDataNode(nodes, meta.Leader) {
		blockers = append(blockers, "source_leader_not_alive")
	}
	if meta.Leader == req.TargetNodeID {
		blockers = append(blockers, "target_already_leader")
	}
	if !containsManagementUint64(meta.ISR, req.TargetNodeID) {
		blockers = append(blockers, "target_not_isr")
	}
	if !isActiveMigrationDataNode(nodes, req.TargetNodeID) {
		blockers = append(blockers, "target_node_not_alive")
	}

	task := a.newChannelMigrationTask(id, metadb.ChannelMigrationKindLeaderTransfer, meta.Leader, req.TargetNodeID, req.TargetNodeID, meta)
	return a.finishChannelMigrationValidation(ctx, req.DryRun, task, meta, blockers)
}

// MigrateChannelReplica validates or creates a manual channel replica replacement task.
func (a *App) MigrateChannelReplica(ctx context.Context, id channel.ChannelID, req MigrateChannelReplicaRequest) (ChannelMigrationResult, error) {
	if err := validateManagementChannelID(id); err != nil {
		return ChannelMigrationResult{}, err
	}
	if req.SourceNodeID == 0 || req.TargetNodeID == 0 || req.SourceNodeID == req.TargetNodeID {
		return ChannelMigrationResult{}, metadb.ErrInvalidArgument
	}
	meta, err := a.getMigrationRuntimeMeta(ctx, id)
	if err != nil {
		return ChannelMigrationResult{}, err
	}

	blockers, err := a.validateNoActiveChannelMigration(ctx, id)
	if err != nil {
		return ChannelMigrationResult{}, err
	}
	nodes, err := a.listMigrationNodes(ctx)
	if err != nil {
		return ChannelMigrationResult{}, err
	}
	if meta.Status != uint8(channel.StatusActive) {
		blockers = append(blockers, "channel_not_active")
	}
	if channelMigrationWriteFenceActive(meta) {
		blockers = append(blockers, "write_fence_active")
	}
	if meta.Leader == 0 {
		blockers = append(blockers, "missing_leader")
	}
	if activeMigrationDataNodeCount(nodes) <= 1 {
		blockers = append(blockers, "single_node_cluster")
	}
	if meta.Leader != 0 && !isActiveMigrationDataNode(nodes, meta.Leader) {
		blockers = append(blockers, "source_leader_not_alive")
	}
	if !containsManagementUint64(meta.Replicas, req.SourceNodeID) {
		blockers = append(blockers, "source_not_replica")
	}
	if !containsManagementUint64(meta.ISR, req.SourceNodeID) {
		blockers = append(blockers, "source_not_isr")
	}
	if containsManagementUint64(meta.Replicas, req.TargetNodeID) {
		blockers = append(blockers, "target_already_replica")
	}
	if containsManagementUint64(meta.ISR, req.TargetNodeID) {
		blockers = append(blockers, "target_already_isr")
	}
	if meta.Leader == req.TargetNodeID {
		blockers = append(blockers, "target_is_leader")
	}
	if meta.MinISR > int64(len(meta.ISR)) {
		blockers = append(blockers, "min_isr_not_satisfied")
	}
	if !isActiveMigrationDataNode(nodes, req.TargetNodeID) {
		blockers = append(blockers, "target_node_not_alive")
	}
	if meta.Leader == req.SourceNodeID && !hasEligibleEmbeddedLeader(nodes, meta, req.SourceNodeID, req.TargetNodeID) {
		blockers = append(blockers, "no_eligible_embedded_leader")
	}

	task := a.newChannelMigrationTask(id, metadb.ChannelMigrationKindReplicaReplace, req.SourceNodeID, req.TargetNodeID, 0, meta)
	return a.finishChannelMigrationValidation(ctx, req.DryRun, task, meta, blockers)
}

// GetChannelMigration returns the active channel migration task detail.
func (a *App) GetChannelMigration(ctx context.Context, id channel.ChannelID) (ChannelMigrationDetail, error) {
	if err := validateManagementChannelID(id); err != nil {
		return ChannelMigrationDetail{}, err
	}
	if a == nil || a.channelMigration == nil {
		return ChannelMigrationDetail{}, metadb.ErrInvalidArgument
	}
	task, ok, err := a.channelMigration.GetActiveChannelMigrationTask(ctx, id.ID, int64(id.Type))
	if err != nil {
		return ChannelMigrationDetail{}, err
	}
	if !ok {
		return ChannelMigrationDetail{}, metadb.ErrNotFound
	}
	meta, err := a.getMigrationRuntimeMeta(ctx, id)
	if err != nil {
		return ChannelMigrationDetail{}, err
	}
	return channelMigrationDetailFromTask(task, meta), nil
}

// AbortChannelMigration aborts the active task when taskID matches the active task.
func (a *App) AbortChannelMigration(ctx context.Context, id channel.ChannelID, taskID string) (ChannelMigrationDetail, error) {
	if err := validateManagementChannelID(id); err != nil {
		return ChannelMigrationDetail{}, err
	}
	if taskID == "" || a == nil || a.channelMigration == nil {
		return ChannelMigrationDetail{}, metadb.ErrInvalidArgument
	}
	task, ok, err := a.channelMigration.GetActiveChannelMigrationTask(ctx, id.ID, int64(id.Type))
	if err != nil {
		return ChannelMigrationDetail{}, err
	}
	if !ok {
		return ChannelMigrationDetail{}, metadb.ErrNotFound
	}
	if task.TaskID != taskID {
		return ChannelMigrationDetail{}, metadb.ErrStaleMeta
	}
	meta, err := a.getMigrationRuntimeMeta(ctx, id)
	if err != nil {
		return ChannelMigrationDetail{}, err
	}
	nowMS := a.now().UnixMilli()
	req := metadb.ChannelMigrationAbortRequest{
		Guard:         channelMigrationGuardFromTask(task),
		RuntimeGuard:  channelMigrationRuntimeGuardFromMeta(meta),
		Status:        metadb.ChannelMigrationStatusAborted,
		Phase:         task.Phase,
		UpdatedAtMS:   nextManagementUpdatedAtMS(nowMS, task.UpdatedAtMS),
		CompletedAtMS: nextManagementUpdatedAtMS(nowMS, task.UpdatedAtMS),
		LastError:     "aborted by operator",
	}
	if err := a.channelMigration.AbortChannelMigration(ctx, req); err != nil {
		return ChannelMigrationDetail{}, err
	}
	task.Status = req.Status
	task.Phase = req.Phase
	task.UpdatedAtMS = req.UpdatedAtMS
	task.CompletedAtMS = req.CompletedAtMS
	task.LastError = req.LastError
	return channelMigrationDetailFromTask(task, meta), nil
}

func (a *App) finishChannelMigrationValidation(ctx context.Context, dryRun bool, task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta, blockers []string) (ChannelMigrationResult, error) {
	result := ChannelMigrationResult{
		DryRun:        dryRun,
		Valid:         len(blockers) == 0,
		Kind:          managerChannelMigrationKind(task.Kind),
		Blockers:      append([]string(nil), blockers...),
		PhaseSequence: channelMigrationPhaseSequence(task, meta),
		Detail:        channelMigrationDetailFromTask(task, meta),
	}
	if dryRun {
		result.Detail.TaskID = ""
		return result, nil
	}
	if len(blockers) > 0 {
		return result, metadb.ErrInvalidArgument
	}
	if a == nil || a.channelMigration == nil {
		return ChannelMigrationResult{}, metadb.ErrInvalidArgument
	}
	if err := a.channelMigration.CreateChannelMigrationTaskWithRuntimeGuard(ctx, metadb.ChannelMigrationTaskCreate{
		Task:         task,
		RuntimeGuard: channelMigrationRuntimeGuardFromMeta(meta),
	}); err != nil {
		return ChannelMigrationResult{}, err
	}
	result.TaskID = task.TaskID
	result.Detail.TaskID = task.TaskID
	return result, nil
}

func (a *App) validateNoActiveChannelMigration(ctx context.Context, id channel.ChannelID) ([]string, error) {
	if a == nil || a.channelMigration == nil {
		return nil, metadb.ErrInvalidArgument
	}
	_, ok, err := a.channelMigration.GetActiveChannelMigrationTask(ctx, id.ID, int64(id.Type))
	if err != nil {
		return nil, err
	}
	if ok {
		return []string{"active_task_exists"}, nil
	}
	return nil, nil
}

func (a *App) getMigrationRuntimeMeta(ctx context.Context, id channel.ChannelID) (metadb.ChannelRuntimeMeta, error) {
	if a == nil || a.channelRuntimeMeta == nil {
		return metadb.ChannelRuntimeMeta{}, metadb.ErrInvalidArgument
	}
	return a.channelRuntimeMeta.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
}

func (a *App) newChannelMigrationTask(id channel.ChannelID, kind metadb.ChannelMigrationKind, source, target, desired uint64, meta metadb.ChannelRuntimeMeta) metadb.ChannelMigrationTask {
	nowMS := a.now().UnixMilli()
	return metadb.ChannelMigrationTask{
		TaskID:           newChannelMigrationTaskID(nowMS),
		Kind:             kind,
		Status:           metadb.ChannelMigrationStatusPending,
		Phase:            metadb.ChannelMigrationPhaseValidate,
		ChannelID:        id.ID,
		ChannelType:      int64(id.Type),
		SourceNode:       source,
		TargetNode:       target,
		DesiredLeader:    desired,
		BaseChannelEpoch: meta.ChannelEpoch,
		BaseLeaderEpoch:  meta.LeaderEpoch,
		CreatedAtMS:      nowMS,
		UpdatedAtMS:      nowMS,
	}
}

func isActiveMigrationDataNode(nodes []controllermeta.ClusterNode, nodeID uint64) bool {
	for _, node := range nodes {
		if node.NodeID == nodeID &&
			node.Role == controllermeta.NodeRoleData &&
			node.JoinState == controllermeta.NodeJoinStateActive &&
			node.Status == controllermeta.NodeStatusAlive {
			return true
		}
	}
	return false
}

func activeMigrationDataNodeCount(nodes []controllermeta.ClusterNode) int {
	count := 0
	for _, node := range nodes {
		if node.Role == controllermeta.NodeRoleData &&
			node.JoinState == controllermeta.NodeJoinStateActive &&
			node.Status == controllermeta.NodeStatusAlive {
			count++
		}
	}
	return count
}

func channelMigrationWriteFenceActive(meta metadb.ChannelRuntimeMeta) bool {
	return meta.WriteFenceToken != ""
}

func hasEligibleEmbeddedLeader(nodes []controllermeta.ClusterNode, meta metadb.ChannelRuntimeMeta, source, target uint64) bool {
	for _, nodeID := range meta.ISR {
		if nodeID == source || nodeID == target {
			continue
		}
		if isActiveMigrationDataNode(nodes, nodeID) {
			return true
		}
	}
	return false
}

func (a *App) listMigrationNodes(ctx context.Context) ([]controllermeta.ClusterNode, error) {
	if a == nil || a.cluster == nil {
		return nil, metadb.ErrInvalidArgument
	}
	return a.cluster.ListNodesStrict(ctx)
}

func channelMigrationDetailFromTask(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta) ChannelMigrationDetail {
	return ChannelMigrationDetail{
		TaskID:              task.TaskID,
		Kind:                managerChannelMigrationKind(task.Kind),
		Status:              managerChannelMigrationStatus(task.Status),
		Phase:               managerChannelMigrationPhase(task.Phase),
		ChannelID:           task.ChannelID,
		ChannelType:         task.ChannelType,
		SourceNode:          task.SourceNode,
		TargetNode:          task.TargetNode,
		DesiredLeader:       task.DesiredLeader,
		BaseChannelEpoch:    task.BaseChannelEpoch,
		BaseLeaderEpoch:     task.BaseLeaderEpoch,
		CurrentChannelEpoch: meta.ChannelEpoch,
		CurrentLeaderEpoch:  meta.LeaderEpoch,
		Progress:            task.Progress,
		FenceActive:         task.FenceToken != "" || meta.WriteFenceToken == task.TaskID,
		FenceUntilMS:        task.FenceUntilMS,
		FenceReason:         meta.WriteFenceReason,
		BlockerCode:         task.BlockerCode,
		BlockerMessage:      task.BlockerMessage,
		Attempt:             task.Attempt,
		NextRunAtMS:         task.NextRunAtMS,
		LastError:           task.LastError,
		CreatedAtMS:         task.CreatedAtMS,
		UpdatedAtMS:         task.UpdatedAtMS,
		CompletedAtMS:       task.CompletedAtMS,
	}
}

func channelMigrationPhaseSequence(task metadb.ChannelMigrationTask, meta metadb.ChannelRuntimeMeta) []string {
	if task.Kind == metadb.ChannelMigrationKindReplicaReplace && meta.Leader == task.SourceNode {
		return append([]string(nil), channelMigrationEmbeddedReplicaReplacePhaseSequence...)
	}
	return append([]string(nil), channelMigrationPhaseSequences[task.Kind]...)
}

func joinChannelMigrationPhaseSequences(groups ...[]string) []string {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	out := make([]string, 0, total)
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

func channelMigrationGuardFromTask(task metadb.ChannelMigrationTask) metadb.ChannelMigrationTaskGuard {
	return metadb.ChannelMigrationTaskGuard{
		ChannelID:                 task.ChannelID,
		ChannelType:               task.ChannelType,
		TaskID:                    task.TaskID,
		ExpectedStatus:            task.Status,
		ExpectedPhase:             task.Phase,
		ExpectedOwnerNodeID:       task.OwnerNodeID,
		ExpectedOwnerLeaseUntilMS: task.OwnerLeaseUntilMS,
		ExpectedUpdatedAtMS:       task.UpdatedAtMS,
	}
}

func channelMigrationRuntimeGuardFromMeta(meta metadb.ChannelRuntimeMeta) metadb.ChannelMigrationRuntimeGuard {
	return metadb.ChannelMigrationRuntimeGuard{
		ChannelID:            meta.ChannelID,
		ChannelType:          meta.ChannelType,
		ExpectedChannelEpoch: meta.ChannelEpoch,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedLeader:       meta.Leader,
		ExpectedFenceToken:   meta.WriteFenceToken,
		ExpectedFenceVersion: meta.WriteFenceVersion,
	}
}

func validateManagementChannelID(id channel.ChannelID) error {
	if strings.TrimSpace(id.ID) == "" || id.Type <= 0 {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func managerChannelMigrationKind(kind metadb.ChannelMigrationKind) string {
	switch kind {
	case metadb.ChannelMigrationKindLeaderTransfer:
		return ChannelMigrationKindLeaderTransfer
	case metadb.ChannelMigrationKindReplicaReplace:
		return ChannelMigrationKindReplicaReplace
	default:
		return "unknown"
	}
}

func managerChannelMigrationStatus(status metadb.ChannelMigrationStatus) string {
	switch status {
	case metadb.ChannelMigrationStatusPending:
		return "pending"
	case metadb.ChannelMigrationStatusRunning:
		return "running"
	case metadb.ChannelMigrationStatusBlocked:
		return "blocked"
	case metadb.ChannelMigrationStatusCompleted:
		return "completed"
	case metadb.ChannelMigrationStatusFailed:
		return "failed"
	case metadb.ChannelMigrationStatusAborted:
		return "aborted"
	default:
		return "unknown"
	}
}

func managerChannelMigrationPhase(phase metadb.ChannelMigrationPhase) string {
	switch phase {
	case metadb.ChannelMigrationPhaseValidate:
		return "validate"
	case metadb.ChannelMigrationPhaseProbeTarget:
		return "probe_target"
	case metadb.ChannelMigrationPhaseWriteFence:
		return "write_fence"
	case metadb.ChannelMigrationPhaseDrainLeader:
		return "drain_leader"
	case metadb.ChannelMigrationPhaseFinalTargetCatchUp:
		return "final_target_catch_up"
	case metadb.ChannelMigrationPhaseCommitLeaderMeta:
		return "commit_leader_meta"
	case metadb.ChannelMigrationPhaseVerifyNewLeader:
		return "verify_new_leader"
	case metadb.ChannelMigrationPhaseAddLearner:
		return "add_learner"
	case metadb.ChannelMigrationPhaseBootstrapTarget:
		return "bootstrap_target"
	case metadb.ChannelMigrationPhaseWarmCatchUp:
		return "warm_catch_up"
	case metadb.ChannelMigrationPhaseCutoverFence:
		return "cutover_fence"
	case metadb.ChannelMigrationPhasePromoteAndRemove:
		return "promote_and_remove"
	case metadb.ChannelMigrationPhaseVerifyMembership:
		return "verify_membership"
	case metadb.ChannelMigrationPhaseClearFence:
		return "clear_fence"
	default:
		return "unknown"
	}
}

func newChannelMigrationTaskID(nowMS int64) string {
	var random [8]byte
	if _, err := rand.Read(random[:]); err == nil {
		return fmt.Sprintf("chmig-%d-%s", nowMS, hex.EncodeToString(random[:]))
	}
	return fmt.Sprintf("chmig-%d", nowMS)
}

func nextManagementUpdatedAtMS(nowMS, previous int64) int64 {
	if nowMS <= previous {
		return previous + 1
	}
	return nowMS
}
