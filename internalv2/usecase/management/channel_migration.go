package management

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelwrapper "github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

var (
	// ErrChannelMigrationUnavailable reports that ChannelV2 migration storage is not wired.
	ErrChannelMigrationUnavailable = errors.New("internalv2/usecase/management: channel migration unavailable")
	// ErrChannelMigrationConflict reports a duplicate or stale ChannelV2 migration request.
	ErrChannelMigrationConflict = errors.New("internalv2/usecase/management: channel migration conflict")
	// ErrChannelMigrationNotFound reports that the requested migration task is absent.
	ErrChannelMigrationNotFound = errors.New("internalv2/usecase/management: channel migration not found")
)

// ChannelMigrationStore exposes Slot-owned ChannelV2 migration task commands.
type ChannelMigrationStore interface {
	// CreateLeaderTransfer creates a runtime-guarded leader-transfer task.
	CreateLeaderTransfer(context.Context, channelwrapper.CreateLeaderTransferRequest) (metadb.ChannelMigrationTask, error)
	// CreateReplicaReplace creates a runtime-guarded replica-replacement task.
	CreateReplicaReplace(context.Context, channelwrapper.CreateReplicaReplaceRequest) (metadb.ChannelMigrationTask, error)
	// GetActive reads the active task for one channel.
	GetActive(context.Context, ch.ChannelID) (metadb.ChannelMigrationTask, bool, error)
	// Get reads one migration task for one channel.
	Get(context.Context, ch.ChannelID, string) (metadb.ChannelMigrationTask, bool, error)
	// ListActive lists active tasks for one channel.
	ListActive(context.Context, ch.ChannelID, int) ([]metadb.ChannelMigrationTask, error)
	// Abort marks a task aborted.
	Abort(context.Context, metadb.ChannelMigrationTask, string) error
}

// LeaderTransferInput describes a manager ChannelV2 leader-transfer intent.
type LeaderTransferInput struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the logical channel type.
	ChannelType uint8
	// TargetNode is the existing replica that should become leader.
	TargetNode uint64
	// TaskID optionally supplies an idempotency key.
	TaskID string
}

// ReplicaReplaceInput describes a manager ChannelV2 replica-replacement intent.
type ReplicaReplaceInput struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the logical channel type.
	ChannelType uint8
	// SourceNode is the replica being replaced.
	SourceNode uint64
	// TargetNode is the new replica candidate.
	TargetNode uint64
	// TaskID optionally supplies an idempotency key.
	TaskID string
}

// ChannelMigrationLookupInput identifies one channel-scoped migration task.
type ChannelMigrationLookupInput struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the logical channel type.
	ChannelType uint8
	// TaskID is the durable migration task ID.
	TaskID string
}

// ChannelMigrationListInput configures active task reads for one channel.
type ChannelMigrationListInput struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the logical channel type.
	ChannelType uint8
	// Limit bounds returned active tasks.
	Limit int
}

// ChannelMigrationAbortInput identifies a task and operator reason to abort.
type ChannelMigrationAbortInput struct {
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the logical channel type.
	ChannelType uint8
	// TaskID is the durable migration task ID.
	TaskID string
	// Reason is the optional operator-facing abort reason.
	Reason string
}

// ChannelMigrationSummary is the manager-facing ChannelV2 migration task row.
type ChannelMigrationSummary struct {
	// TaskID is the durable migration task identity.
	TaskID string
	// ChannelID is the logical channel identifier.
	ChannelID string
	// ChannelType is the logical channel type.
	ChannelType int64
	// Kind is the stable workflow kind.
	Kind string
	// Status is the stable task lifecycle status.
	Status string
	// Phase is the stable executor phase.
	Phase string
	// SourceNode is the source leader or replica, depending on Kind.
	SourceNode uint64
	// TargetNode is the desired leader or replacement node.
	TargetNode uint64
	// DesiredLeader is the desired leader when known.
	DesiredLeader uint64
	// BlockerMessage is the bounded blocker detail for blocked tasks.
	BlockerMessage string
	// LastError is the bounded failure detail for failed tasks.
	LastError string
}

// ChannelMigrationListResponse is returned by active migration list reads.
type ChannelMigrationListResponse struct {
	// Items contains active task summaries.
	Items []ChannelMigrationSummary
}

// RequestChannelLeaderTransfer validates and submits a manual ChannelV2 leader transfer.
func (a *App) RequestChannelLeaderTransfer(ctx context.Context, input LeaderTransferInput) (ChannelMigrationSummary, error) {
	if err := ctxErr(ctx); err != nil {
		return ChannelMigrationSummary{}, err
	}
	id, err := channelMigrationInputID(input.ChannelID, input.ChannelType)
	if err != nil {
		return ChannelMigrationSummary{}, err
	}
	if input.TargetNode == 0 {
		return ChannelMigrationSummary{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.channelMigration == nil {
		return ChannelMigrationSummary{}, ErrChannelMigrationUnavailable
	}
	task, err := a.channelMigration.CreateLeaderTransfer(ctx, channelwrapper.CreateLeaderTransferRequest{
		ChannelID:     id,
		TaskID:        strings.TrimSpace(input.TaskID),
		DesiredLeader: ch.NodeID(input.TargetNode),
	})
	if err != nil {
		return ChannelMigrationSummary{}, mapChannelMigrationError(err)
	}
	return managerChannelMigrationSummary(task), nil
}

// RequestChannelReplicaReplace validates and submits a manual ChannelV2 replica replacement.
func (a *App) RequestChannelReplicaReplace(ctx context.Context, input ReplicaReplaceInput) (ChannelMigrationSummary, error) {
	if err := ctxErr(ctx); err != nil {
		return ChannelMigrationSummary{}, err
	}
	id, err := channelMigrationInputID(input.ChannelID, input.ChannelType)
	if err != nil {
		return ChannelMigrationSummary{}, err
	}
	if input.SourceNode == 0 || input.TargetNode == 0 || input.SourceNode == input.TargetNode {
		return ChannelMigrationSummary{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.channelMigration == nil {
		return ChannelMigrationSummary{}, ErrChannelMigrationUnavailable
	}
	task, err := a.channelMigration.CreateReplicaReplace(ctx, channelwrapper.CreateReplicaReplaceRequest{
		ChannelID:  id,
		TaskID:     strings.TrimSpace(input.TaskID),
		SourceNode: ch.NodeID(input.SourceNode),
		TargetNode: ch.NodeID(input.TargetNode),
	})
	if err != nil {
		return ChannelMigrationSummary{}, mapChannelMigrationError(err)
	}
	return managerChannelMigrationSummary(task), nil
}

// ActiveChannelMigration returns the active migration task for one channel when present.
func (a *App) ActiveChannelMigration(ctx context.Context, input ChannelMigrationListInput) (ChannelMigrationSummary, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return ChannelMigrationSummary{}, false, err
	}
	id, err := channelMigrationInputID(input.ChannelID, input.ChannelType)
	if err != nil {
		return ChannelMigrationSummary{}, false, err
	}
	if a == nil || a.channelMigration == nil {
		return ChannelMigrationSummary{}, false, ErrChannelMigrationUnavailable
	}
	task, ok, err := a.channelMigration.GetActive(ctx, id)
	if err != nil {
		return ChannelMigrationSummary{}, false, mapChannelMigrationError(err)
	}
	if !ok {
		return ChannelMigrationSummary{}, false, nil
	}
	return managerChannelMigrationSummary(task), true, nil
}

// ListActiveChannelMigrations returns active migration tasks for one channel.
func (a *App) ListActiveChannelMigrations(ctx context.Context, input ChannelMigrationListInput) (ChannelMigrationListResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return ChannelMigrationListResponse{}, err
	}
	id, err := channelMigrationInputID(input.ChannelID, input.ChannelType)
	if err != nil {
		return ChannelMigrationListResponse{}, err
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 10
	} else if limit > 100 {
		limit = 100
	}
	if a == nil || a.channelMigration == nil {
		return ChannelMigrationListResponse{}, ErrChannelMigrationUnavailable
	}
	tasks, err := a.channelMigration.ListActive(ctx, id, limit)
	if err != nil {
		return ChannelMigrationListResponse{}, mapChannelMigrationError(err)
	}
	items := make([]ChannelMigrationSummary, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, managerChannelMigrationSummary(task))
	}
	return ChannelMigrationListResponse{Items: items}, nil
}

// ChannelMigration returns one migration task by id within its channel scope.
func (a *App) ChannelMigration(ctx context.Context, input ChannelMigrationLookupInput) (ChannelMigrationSummary, error) {
	if err := ctxErr(ctx); err != nil {
		return ChannelMigrationSummary{}, err
	}
	id, taskID, err := channelMigrationLookupInput(input)
	if err != nil {
		return ChannelMigrationSummary{}, err
	}
	if a == nil || a.channelMigration == nil {
		return ChannelMigrationSummary{}, ErrChannelMigrationUnavailable
	}
	task, ok, err := a.channelMigration.Get(ctx, id, taskID)
	if err != nil {
		return ChannelMigrationSummary{}, mapChannelMigrationError(err)
	}
	if !ok {
		return ChannelMigrationSummary{}, ErrChannelMigrationNotFound
	}
	return managerChannelMigrationSummary(task), nil
}

// AbortChannelMigration aborts one channel-scoped migration task.
func (a *App) AbortChannelMigration(ctx context.Context, input ChannelMigrationAbortInput) (ChannelMigrationSummary, error) {
	if err := ctxErr(ctx); err != nil {
		return ChannelMigrationSummary{}, err
	}
	id, taskID, err := channelMigrationLookupInput(ChannelMigrationLookupInput{
		ChannelID:   input.ChannelID,
		ChannelType: input.ChannelType,
		TaskID:      input.TaskID,
	})
	if err != nil {
		return ChannelMigrationSummary{}, err
	}
	if a == nil || a.channelMigration == nil {
		return ChannelMigrationSummary{}, ErrChannelMigrationUnavailable
	}
	task, ok, err := a.channelMigration.Get(ctx, id, taskID)
	if err != nil {
		return ChannelMigrationSummary{}, mapChannelMigrationError(err)
	}
	if !ok {
		return ChannelMigrationSummary{}, ErrChannelMigrationNotFound
	}
	if err := a.channelMigration.Abort(ctx, task, strings.TrimSpace(input.Reason)); err != nil {
		return ChannelMigrationSummary{}, mapChannelMigrationError(err)
	}
	task.Status = metadb.ChannelMigrationStatusAborted
	if strings.TrimSpace(input.Reason) != "" {
		task.LastError = strings.TrimSpace(input.Reason)
	}
	return managerChannelMigrationSummary(task), nil
}

func channelMigrationInputID(channelID string, channelType uint8) (ch.ChannelID, error) {
	id := strings.TrimSpace(channelID)
	if id == "" {
		return ch.ChannelID{}, metadb.ErrInvalidArgument
	}
	return ch.ChannelID{ID: id, Type: channelType}, nil
}

func channelMigrationLookupInput(input ChannelMigrationLookupInput) (ch.ChannelID, string, error) {
	id, err := channelMigrationInputID(input.ChannelID, input.ChannelType)
	if err != nil {
		return ch.ChannelID{}, "", err
	}
	taskID := strings.TrimSpace(input.TaskID)
	if taskID == "" {
		return ch.ChannelID{}, "", metadb.ErrInvalidArgument
	}
	return id, taskID, nil
}

func mapChannelMigrationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ch.ErrInvalidConfig), errors.Is(err, metadb.ErrInvalidArgument):
		return fmt.Errorf("%w: %v", metadb.ErrInvalidArgument, err)
	case errors.Is(err, metadb.ErrAlreadyExists), errors.Is(err, metadb.ErrStaleMeta):
		return fmt.Errorf("%w: %v", ErrChannelMigrationConflict, err)
	case errors.Is(err, metadb.ErrNotFound):
		return fmt.Errorf("%w: %v", ErrChannelMigrationNotFound, err)
	default:
		return err
	}
}

func managerChannelMigrationSummary(task metadb.ChannelMigrationTask) ChannelMigrationSummary {
	return ChannelMigrationSummary{
		TaskID:         task.TaskID,
		ChannelID:      task.ChannelID,
		ChannelType:    task.ChannelType,
		Kind:           managerChannelMigrationKind(task.Kind),
		Status:         managerChannelMigrationStatus(task.Status),
		Phase:          managerChannelMigrationPhase(task.Phase),
		SourceNode:     task.SourceNode,
		TargetNode:     task.TargetNode,
		DesiredLeader:  task.DesiredLeader,
		BlockerMessage: task.BlockerMessage,
		LastError:      task.LastError,
	}
}

func managerChannelMigrationKind(kind metadb.ChannelMigrationKind) string {
	switch kind {
	case metadb.ChannelMigrationKindLeaderTransfer:
		return "leader_transfer"
	case metadb.ChannelMigrationKindLeaderFailover:
		return "leader_failover"
	case metadb.ChannelMigrationKindReplicaReplace:
		return "replica_replace"
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
