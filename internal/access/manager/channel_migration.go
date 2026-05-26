package manager

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

// channelLeaderTransferRequestDTO is the JSON body for a manual leader transfer.
type channelLeaderTransferRequestDTO struct {
	// TargetNodeID is the existing ISR node that should become channel leader.
	TargetNodeID uint64 `json:"target_node_id"`
	// DryRun validates the migration without creating a durable task.
	DryRun bool `json:"dry_run"`
}

// channelReplicaMigrationRequestDTO is the JSON body for one replica replacement.
type channelReplicaMigrationRequestDTO struct {
	// SourceNodeID is the existing replica being replaced.
	SourceNodeID uint64 `json:"source_node_id"`
	// TargetNodeID is the active data node to add as learner and promote.
	TargetNodeID uint64 `json:"target_node_id"`
	// DryRun validates the migration without creating a durable task.
	DryRun bool `json:"dry_run"`
}

// channelMigrationResultDTO is the JSON response for dry-run and create calls.
type channelMigrationResultDTO struct {
	// DryRun reports whether no durable task was created.
	DryRun bool `json:"dry_run"`
	// Valid reports whether validation found no blockers.
	Valid bool `json:"valid"`
	// TaskID identifies the created task when creation succeeded.
	TaskID string `json:"task_id,omitempty"`
	// Kind is the stable manager-facing migration kind.
	Kind string `json:"kind"`
	// Blockers contains stable machine-readable validation blockers.
	Blockers []string `json:"blockers"`
	// PhaseSequence is the expected executor phase order.
	PhaseSequence []string `json:"phase_sequence"`
	// Detail contains planned or current task details.
	Detail channelMigrationDetailDTO `json:"detail"`
}

// channelMigrationDetailDTO is the JSON response for an active migration task.
type channelMigrationDetailDTO struct {
	// TaskID identifies one channel migration attempt.
	TaskID string `json:"task_id"`
	// Kind is the stable manager-facing migration kind.
	Kind string `json:"kind"`
	// Status is the stable task lifecycle status.
	Status string `json:"status"`
	// Phase is the resumable executor phase.
	Phase string `json:"phase"`
	// ChannelID identifies the channel.
	ChannelID string `json:"channel_id"`
	// ChannelType identifies the channel namespace.
	ChannelType int64 `json:"channel_type"`
	// SourceNode is the source leader or replica.
	SourceNode uint64 `json:"source_node"`
	// TargetNode is the target leader or replacement replica.
	TargetNode uint64 `json:"target_node"`
	// DesiredLeader is the requested leader for leader-transfer semantics.
	DesiredLeader uint64 `json:"desired_leader"`
	// BaseChannelEpoch is the channel epoch captured at task creation.
	BaseChannelEpoch uint64 `json:"base_channel_epoch"`
	// BaseLeaderEpoch is the leader epoch captured at task creation.
	BaseLeaderEpoch uint64 `json:"base_leader_epoch"`
	// CurrentChannelEpoch is the latest authoritative channel epoch.
	CurrentChannelEpoch uint64 `json:"current_channel_epoch"`
	// CurrentLeaderEpoch is the latest authoritative leader epoch.
	CurrentLeaderEpoch uint64 `json:"current_leader_epoch"`
	// LeaderLEO is the latest observed leader log end offset.
	LeaderLEO uint64 `json:"leader_leo"`
	// LeaderHW is the latest observed leader high watermark.
	LeaderHW uint64 `json:"leader_hw"`
	// TargetLEO is the latest observed target log end offset.
	TargetLEO uint64 `json:"target_leo"`
	// TargetCheckpointHW is the latest target checkpoint high watermark.
	TargetCheckpointHW uint64 `json:"target_checkpoint_hw"`
	// LagRecords is the latest observed leader-to-target log gap.
	LagRecords uint64 `json:"lag_records"`
	// StableSinceMS records when the current catch-up proof became stable.
	StableSinceMS int64 `json:"stable_since_ms"`
	// FenceActive reports whether a migration write fence is active.
	FenceActive bool `json:"fence_active"`
	// FenceUntilMS is the write-fence lease deadline.
	FenceUntilMS int64 `json:"fence_until_ms"`
	// FenceReason is the raw runtime write-fence reason.
	FenceReason uint8 `json:"fence_reason"`
	// BlockerCode is a stable blocker code when blocked.
	BlockerCode string `json:"blocker_code,omitempty"`
	// BlockerMessage is a human-readable blocker detail.
	BlockerMessage string `json:"blocker_message,omitempty"`
	// Attempt is the durable retry counter.
	Attempt uint32 `json:"attempt"`
	// NextRunAtMS is the next executor wake-up timestamp.
	NextRunAtMS int64 `json:"next_run_at_ms"`
	// LastError is the latest retryable or terminal error.
	LastError string `json:"last_error,omitempty"`
	// CreatedAtMS is the task creation timestamp.
	CreatedAtMS int64 `json:"created_at_ms"`
	// UpdatedAtMS is the latest task update timestamp.
	UpdatedAtMS int64 `json:"updated_at_ms"`
	// CompletedAtMS is set for terminal tasks.
	CompletedAtMS int64 `json:"completed_at_ms,omitempty"`
}

func (s *Server) handleChannelLeaderTransfer(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	id, ok := parseChannelMigrationID(c)
	if !ok {
		return
	}
	var req channelLeaderTransferRequestDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	if req.TargetNodeID == 0 {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid target_node_id")
		return
	}
	result, err := s.management.TransferChannelLeader(c.Request.Context(), id, managementusecase.TransferChannelLeaderRequest{
		TargetNodeID: req.TargetNodeID,
		DryRun:       req.DryRun,
	})
	if err != nil {
		if writeBlockedChannelMigrationResult(c, result, err) {
			return
		}
		s.writeChannelMigrationError(c, err)
		return
	}
	c.JSON(http.StatusOK, channelMigrationResultDTOFromUsecase(result))
}

func (s *Server) handleChannelReplicaMigrate(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	id, ok := parseChannelMigrationID(c)
	if !ok {
		return
	}
	var req channelReplicaMigrationRequestDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	if req.SourceNodeID == 0 {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid source_node_id")
		return
	}
	if req.TargetNodeID == 0 {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid target_node_id")
		return
	}
	result, err := s.management.MigrateChannelReplica(c.Request.Context(), id, managementusecase.MigrateChannelReplicaRequest{
		SourceNodeID: req.SourceNodeID,
		TargetNodeID: req.TargetNodeID,
		DryRun:       req.DryRun,
	})
	if err != nil {
		if writeBlockedChannelMigrationResult(c, result, err) {
			return
		}
		s.writeChannelMigrationError(c, err)
		return
	}
	c.JSON(http.StatusOK, channelMigrationResultDTOFromUsecase(result))
}

func (s *Server) handleChannelMigration(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	id, ok := parseChannelMigrationID(c)
	if !ok {
		return
	}
	detail, err := s.management.GetChannelMigration(c.Request.Context(), id)
	if err != nil {
		s.writeChannelMigrationError(c, err)
		return
	}
	c.JSON(http.StatusOK, channelMigrationDetailDTOFromUsecase(detail))
}

func (s *Server) handleChannelMigrationAbort(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	id, ok := parseChannelMigrationID(c)
	if !ok {
		return
	}
	taskID := c.Param("task_id")
	if taskID == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid task_id")
		return
	}
	detail, err := s.management.AbortChannelMigration(c.Request.Context(), id, taskID)
	if err != nil {
		s.writeChannelMigrationError(c, err)
		return
	}
	c.JSON(http.StatusOK, channelMigrationDetailDTOFromUsecase(detail))
}

func parseChannelMigrationID(c *gin.Context) (channel.ChannelID, bool) {
	channelType, err := parseChannelMigrationTypeParam(c.Param("channel_type"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel_type")
		return channel.ChannelID{}, false
	}
	channelID := c.Param("channel_id")
	if channelID == "" {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel_id")
		return channel.ChannelID{}, false
	}
	return channel.ChannelID{ID: channelID, Type: channelType}, true
}

func parseChannelMigrationTypeParam(raw string) (uint8, error) {
	if raw == "" {
		return 0, strconv.ErrSyntax
	}
	channelType, err := strconv.ParseUint(raw, 10, 8)
	if err != nil || channelType == 0 {
		return 0, strconv.ErrSyntax
	}
	return uint8(channelType), nil
}

func (s *Server) writeChannelMigrationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid channel migration request")
	case errors.Is(err, metadb.ErrNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "channel migration or channel not found")
	case errors.Is(err, metadb.ErrStaleMeta), errors.Is(err, metadb.ErrAlreadyExists):
		jsonError(c, http.StatusConflict, "conflict", "stale channel migration state")
	case channelMigrationUnavailable(err):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "channel migration unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func writeBlockedChannelMigrationResult(c *gin.Context, result managementusecase.ChannelMigrationResult, err error) bool {
	if !errors.Is(err, metadb.ErrInvalidArgument) || result.Valid || !channelMigrationResultHasBody(result) {
		return false
	}
	c.JSON(http.StatusBadRequest, channelMigrationResultDTOFromUsecase(result))
	return true
}

func channelMigrationResultHasBody(result managementusecase.ChannelMigrationResult) bool {
	return result.Kind != "" ||
		result.TaskID != "" ||
		len(result.Blockers) > 0 ||
		len(result.PhaseSequence) > 0 ||
		result.Detail.ChannelID != ""
}

func channelMigrationUnavailable(err error) bool {
	return errors.Is(err, raftcluster.ErrNoLeader) ||
		errors.Is(err, raftcluster.ErrNotLeader) ||
		errors.Is(err, raftcluster.ErrSlotNotFound) ||
		errors.Is(err, raftcluster.ErrNotStarted) ||
		errors.Is(err, context.DeadlineExceeded)
}

func channelMigrationResultDTOFromUsecase(result managementusecase.ChannelMigrationResult) channelMigrationResultDTO {
	return channelMigrationResultDTO{
		DryRun:        result.DryRun,
		Valid:         result.Valid,
		TaskID:        result.TaskID,
		Kind:          result.Kind,
		Blockers:      append([]string{}, result.Blockers...),
		PhaseSequence: append([]string{}, result.PhaseSequence...),
		Detail:        channelMigrationDetailDTOFromUsecase(result.Detail),
	}
}

func channelMigrationDetailDTOFromUsecase(detail managementusecase.ChannelMigrationDetail) channelMigrationDetailDTO {
	return channelMigrationDetailDTO{
		TaskID:              detail.TaskID,
		Kind:                detail.Kind,
		Status:              detail.Status,
		Phase:               detail.Phase,
		ChannelID:           detail.ChannelID,
		ChannelType:         detail.ChannelType,
		SourceNode:          detail.SourceNode,
		TargetNode:          detail.TargetNode,
		DesiredLeader:       detail.DesiredLeader,
		BaseChannelEpoch:    detail.BaseChannelEpoch,
		BaseLeaderEpoch:     detail.BaseLeaderEpoch,
		CurrentChannelEpoch: detail.CurrentChannelEpoch,
		CurrentLeaderEpoch:  detail.CurrentLeaderEpoch,
		LeaderLEO:           detail.Progress.LeaderLEO,
		LeaderHW:            detail.Progress.LeaderHW,
		TargetLEO:           detail.Progress.TargetLEO,
		TargetCheckpointHW:  detail.Progress.TargetCheckpointHW,
		LagRecords:          detail.Progress.LagRecords,
		StableSinceMS:       detail.Progress.StableSinceMS,
		FenceActive:         detail.FenceActive,
		FenceUntilMS:        detail.FenceUntilMS,
		FenceReason:         detail.FenceReason,
		BlockerCode:         detail.BlockerCode,
		BlockerMessage:      detail.BlockerMessage,
		Attempt:             detail.Attempt,
		NextRunAtMS:         detail.NextRunAtMS,
		LastError:           detail.LastError,
		CreatedAtMS:         detail.CreatedAtMS,
		UpdatedAtMS:         detail.UpdatedAtMS,
		CompletedAtMS:       detail.CompletedAtMS,
	}
}
