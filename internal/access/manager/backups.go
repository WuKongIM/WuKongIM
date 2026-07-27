package manager

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/gin-gonic/gin"
)

// BackupManagement is the narrow Manager-facing continuous-backup seam.
type BackupManagement interface {
	Status(context.Context) (backupusecase.StatusSnapshot, error)
	ListCheckpointsPage(context.Context, backupusecase.CheckpointListRequest) (backupusecase.CheckpointPage, error)
	CheckpointByID(context.Context, string) (backupusecase.CheckpointDetail, error)
	PublishCheckpoint(context.Context) (backupusecase.CheckpointPublication, error)
	SetCheckpointHold(context.Context, string, bool) (backupusecase.CheckpointSummary, error)
	FenceSource(context.Context, backupusecase.SourceFenceRequest) (backupusecase.SourceFenceReceipt, error)
}

type backupStatusDTO struct {
	Enabled                 bool                     `json:"enabled"`
	Health                  backupusecase.Health     `json:"health"`
	CheckpointAgeSeconds    *int64                   `json:"checkpoint_age_seconds"`
	LatestCheckpoint        *backupCheckpointDTO     `json:"latest_checkpoint,omitempty"`
	FailureCategory         string                   `json:"failure_category,omitempty"`
	CoordinatorNodeID       uint64                   `json:"coordinator_node_id"`
	ObservedAtUnixMillis    int64                    `json:"observed_at_unix_millis"`
	AuthEnabled             bool                     `json:"auth_enabled"`
	Running                 bool                     `json:"running"`
	MaxCheckpointAgeSeconds int64                    `json:"max_checkpoint_age_seconds"`
	Policy                  backupPolicyDTO          `json:"policy"`
	CaptureLeases           []backupCaptureLeaseDTO  `json:"capture_leases"`
	LocalCaptureStatuses    []backupCaptureStatusDTO `json:"local_capture_statuses"`
	ErasureStreams          []backupErasureStreamDTO `json:"erasure_streams"`
}

type backupPolicyDTO struct {
	CaptureReconcileIntervalSeconds int64  `json:"capture_reconcile_interval_seconds"`
	CheckpointIntervalSeconds       int64  `json:"checkpoint_interval_seconds"`
	CaptureWorkerCount              int    `json:"capture_worker_count"`
	StagingMaxBytes                 uint64 `json:"staging_max_bytes"`
	SourcePinMaxAgeSeconds          int64  `json:"source_pin_max_age_seconds"`
	MaxSourcePinnedBytes            uint64 `json:"max_source_pinned_bytes"`
}

type backupErasureStreamDTO struct {
	HashSlot uint16 `json:"hash_slot"`
	Sequence uint64 `json:"sequence"`
	Pending  bool   `json:"pending"`
}

type backupCaptureLeaseDTO struct {
	HashSlot                        uint16 `json:"hash_slot"`
	SlotID                          uint32 `json:"slot_id"`
	SourceSlotID                    uint32 `json:"source_slot_id"`
	HolderNodeID                    uint64 `json:"holder_node_id"`
	LeaderTerm                      uint64 `json:"leader_term"`
	ConfigEpoch                     uint64 `json:"config_epoch"`
	Generation                      string `json:"generation"`
	LeaseSequence                   uint64 `json:"lease_sequence"`
	FrontierRevision                uint64 `json:"frontier_revision"`
	LastPromotionPreviousGeneration string `json:"last_promotion_previous_generation,omitempty"`
	LastPromotionReason             string `json:"last_promotion_reason,omitempty"`
	LastPromotionAtUnixMillis       int64  `json:"last_promotion_at_unix_millis,omitempty"`
	MetadataSourceWatermark         uint64 `json:"metadata_source_watermark"`
	MessageSourceWatermark          uint64 `json:"message_source_watermark"`
	AcquiredAtUnixMillis            int64  `json:"acquired_at_unix_millis"`
	SourcePinStartedAtUnixMillis    int64  `json:"source_pin_started_at_unix_millis"`
	FrontierUpdatedUnixMillis       int64  `json:"frontier_updated_unix_millis"`
}

type backupCaptureStatusDTO struct {
	HashSlot                  uint16 `json:"hash_slot"`
	State                     string `json:"state"`
	FailureCategory           string `json:"failure_category,omitempty"`
	LeaseCurrent              bool   `json:"lease_current"`
	FrontierRevision          uint64 `json:"frontier_revision"`
	MetadataSourceWatermark   uint64 `json:"metadata_source_watermark"`
	MessageSourceWatermark    uint64 `json:"message_source_watermark"`
	MetadataFrontierWatermark uint64 `json:"metadata_frontier_watermark"`
	MessageFrontierWatermark  uint64 `json:"message_frontier_watermark"`
	ObservedAtUnixMillis      int64  `json:"observed_at_unix_millis"`
}

type backupCheckpointDTO struct {
	ID                    string `json:"id"`
	CreatedAtUnixMillis   int64  `json:"created_at_unix_millis"`
	EffectiveAtUnixMillis int64  `json:"effective_at_unix_millis"`
	Held                  bool   `json:"held"`
}

type backupCheckpointDetailDTO struct {
	backupCheckpointDTO
	SourceClusterID  string                   `json:"source_cluster_id"`
	SourceGeneration string                   `json:"source_generation"`
	HashSlotCount    uint16                   `json:"hash_slot_count"`
	ErasureStreams   []backupErasureStreamDTO `json:"erasure_streams"`
}

type backupCheckpointListDTO struct {
	CatalogHeadToken string                `json:"catalog_head_token,omitempty"`
	Items            []backupCheckpointDTO `json:"items"`
	NextCursor       string                `json:"next_cursor,omitempty"`
	Total            int                   `json:"total"`
}

type backupCheckpointPublicationDTO struct {
	Checkpoint       backupCheckpointDTO `json:"checkpoint"`
	CheckpointSHA256 string              `json:"checkpoint_sha256"`
	CatalogHeadToken string              `json:"catalog_head_token"`
}

type backupSourceFenceRequestDTO struct {
	RestorePlanID    string `json:"restore_plan_id"`
	CheckpointID     string `json:"checkpoint_id"`
	TargetClusterID  string `json:"target_cluster_id"`
	TargetGeneration string `json:"target_generation"`
}

type backupCheckpointHoldRequestDTO struct {
	Held bool `json:"held"`
}

func (s *Server) handleBackupStatus(c *gin.Context) {
	if s == nil || s.backup == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control is not configured")
		return
	}
	status, err := s.backup.Status(c.Request.Context())
	if err != nil {
		writeBackupError(c, err)
		return
	}
	response := backupStatusResponse(status)
	response.AuthEnabled = s.auth.enabled()
	c.JSON(http.StatusOK, response)
}

func (s *Server) handleBackupCheckpoints(c *gin.Context) {
	if s == nil || s.backup == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control is not configured")
		return
	}
	request := backupusecase.CheckpointListRequest{
		Cursor:  strings.TrimSpace(c.Query("cursor")),
		IDQuery: strings.TrimSpace(c.Query("id")),
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			jsonError(c, http.StatusBadRequest, "bad_request", "invalid checkpoint page limit")
			return
		}
		if limit > backupusecase.MaxCheckpointPageSize {
			limit = backupusecase.MaxCheckpointPageSize
		}
		request.Limit = limit
	}
	page, err := s.backup.ListCheckpointsPage(c.Request.Context(), request)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	items := make([]backupCheckpointDTO, len(page.Items))
	for index := range page.Items {
		items[index] = backupCheckpointResponse(page.Items[index])
	}
	c.JSON(http.StatusOK, backupCheckpointListDTO{
		CatalogHeadToken: page.CatalogHeadToken,
		Items:            items, NextCursor: page.NextCursor, Total: page.Total,
	})
}

func (s *Server) handleBackupCheckpoint(c *gin.Context) {
	if s == nil || s.backup == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control is not configured")
		return
	}
	detail, err := s.backup.CheckpointByID(
		c.Request.Context(), strings.TrimSpace(c.Param("checkpoint_id")),
	)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	erasureStreams := make([]backupErasureStreamDTO, len(detail.ErasureHeads))
	for index, head := range detail.ErasureHeads {
		erasureStreams[index] = backupErasureStreamDTO{
			HashSlot: head.HashSlot, Sequence: head.Sequence,
		}
	}
	c.JSON(http.StatusOK, backupCheckpointDetailDTO{
		backupCheckpointDTO: backupCheckpointResponse(detail.CheckpointSummary),
		SourceClusterID:     detail.SourceClusterID,
		SourceGeneration:    detail.SourceGeneration,
		HashSlotCount:       detail.HashSlotCount,
		ErasureStreams:      erasureStreams,
	})
}

func (s *Server) handleBackupCheckpointPublish(c *gin.Context) {
	if s == nil || s.backup == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control is not configured")
		return
	}
	publication, err := s.backup.PublishCheckpoint(c.Request.Context())
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.JSON(http.StatusCreated, backupCheckpointPublicationDTO{
		Checkpoint:       backupCheckpointResponse(publication.Checkpoint),
		CheckpointSHA256: publication.CheckpointSHA256,
		CatalogHeadToken: publication.CatalogHeadToken,
	})
}

func (s *Server) handleBackupCheckpointHold(c *gin.Context) {
	if s == nil || s.backup == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control is not configured")
		return
	}
	var request backupCheckpointHoldRequestDTO
	if err := c.ShouldBindJSON(&request); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid checkpoint hold request")
		return
	}
	checkpoint, err := s.backup.SetCheckpointHold(
		c.Request.Context(),
		strings.TrimSpace(c.Param("checkpoint_id")), request.Held,
	)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.JSON(http.StatusOK, backupCheckpointResponse(checkpoint))
}

func (s *Server) handleBackupSourceFence(c *gin.Context) {
	if s == nil || s.backup == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control is not configured")
		return
	}
	var request backupSourceFenceRequestDTO
	if err := c.ShouldBindJSON(&request); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid source fence request")
		return
	}
	receipt, err := s.backup.FenceSource(
		c.Request.Context(),
		backupusecase.SourceFenceRequest{
			RestorePlanID:    strings.TrimSpace(request.RestorePlanID),
			CheckpointID:     strings.TrimSpace(request.CheckpointID),
			TargetClusterID:  strings.TrimSpace(request.TargetClusterID),
			TargetGeneration: strings.TrimSpace(request.TargetGeneration),
		},
	)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.JSON(http.StatusOK, receipt)
}

func backupStatusResponse(status backupusecase.StatusSnapshot) backupStatusDTO {
	result := backupStatusDTO{
		Enabled: status.Enabled, Health: status.Health,
		CheckpointAgeSeconds:    status.CheckpointAgeSeconds,
		FailureCategory:         status.FailureCategory,
		CoordinatorNodeID:       status.CoordinatorNodeID,
		ObservedAtUnixMillis:    status.ObservedAtUnixMillis,
		Running:                 status.Running,
		MaxCheckpointAgeSeconds: status.MaxCheckpointAgeSeconds,
		Policy:                  backupPolicyResponse(status.Policy),
		CaptureLeases:           backupCaptureLeaseResponses(status.CaptureLeases),
		LocalCaptureStatuses:    backupCaptureStatusResponses(status.LocalCaptureStatuses),
		ErasureStreams:          backupErasureStreamResponses(status.ErasureStreams),
	}
	if status.LatestCheckpoint != nil {
		latest := backupCheckpointResponse(*status.LatestCheckpoint)
		result.LatestCheckpoint = &latest
	}
	return result
}

func backupPolicyResponse(policy backupusecase.PolicySnapshot) backupPolicyDTO {
	return backupPolicyDTO{
		CaptureReconcileIntervalSeconds: policy.CaptureReconcileIntervalSeconds,
		CheckpointIntervalSeconds:       policy.CheckpointIntervalSeconds,
		CaptureWorkerCount:              policy.CaptureWorkerCount,
		StagingMaxBytes:                 policy.StagingMaxBytes,
		SourcePinMaxAgeSeconds:          policy.SourcePinMaxAgeSeconds,
		MaxSourcePinnedBytes:            policy.MaxSourcePinnedBytes,
	}
}

func backupCheckpointResponse(checkpoint backupusecase.CheckpointSummary) backupCheckpointDTO {
	return backupCheckpointDTO{
		ID:                    checkpoint.ID,
		CreatedAtUnixMillis:   checkpoint.CreatedAtUnixMillis,
		EffectiveAtUnixMillis: checkpoint.EffectiveAtUnixMillis,
		Held:                  checkpoint.Held,
	}
}

func backupCaptureStatusResponses(statuses []backupusecase.SlotCaptureStatus) []backupCaptureStatusDTO {
	result := make([]backupCaptureStatusDTO, len(statuses))
	for index, status := range statuses {
		result[index] = backupCaptureStatusDTO{
			HashSlot: status.HashSlot, State: string(status.State),
			FailureCategory:           status.FailureCategory,
			LeaseCurrent:              status.LeaseCurrent,
			FrontierRevision:          status.Frontier.Revision,
			MetadataSourceWatermark:   status.MetadataSourceWatermark,
			MessageSourceWatermark:    status.MessageSourceWatermark,
			MetadataFrontierWatermark: status.Frontier.Metadata.SourceHighWatermark,
			MessageFrontierWatermark:  status.Frontier.Messages.SourceHighWatermark,
			ObservedAtUnixMillis:      status.ObservedAtUnixMillis,
		}
	}
	return result
}

func backupErasureStreamResponses(streams []backupusecase.ErasureStreamProgress) []backupErasureStreamDTO {
	result := make([]backupErasureStreamDTO, len(streams))
	for index, stream := range streams {
		result[index] = backupErasureStreamDTO{
			HashSlot: stream.HashSlot, Sequence: stream.Sequence, Pending: stream.Pending,
		}
	}
	return result
}

func backupCaptureLeaseResponses(leases []backupusecase.CaptureLeaseSnapshot) []backupCaptureLeaseDTO {
	result := make([]backupCaptureLeaseDTO, len(leases))
	for index, lease := range leases {
		result[index] = backupCaptureLeaseDTO{
			HashSlot: lease.HashSlot, SlotID: lease.SlotID,
			SourceSlotID: lease.SourceSlotID,
			HolderNodeID: lease.HolderNodeID, LeaderTerm: lease.LeaderTerm,
			ConfigEpoch: lease.ConfigEpoch, Generation: lease.Generation,
			LeaseSequence: lease.LeaseSequence, FrontierRevision: lease.FrontierRevision,
			LastPromotionPreviousGeneration: lease.LastPromotionPreviousGeneration,
			LastPromotionReason:             lease.LastPromotionReason,
			LastPromotionAtUnixMillis:       lease.LastPromotionAtUnixMillis,
			MetadataSourceWatermark:         lease.MetadataSourceWatermark,
			MessageSourceWatermark:          lease.MessageSourceWatermark,
			AcquiredAtUnixMillis:            lease.AcquiredAtUnixMillis,
			SourcePinStartedAtUnixMillis:    lease.SourcePinStartedAtUnixMillis,
			FrontierUpdatedUnixMillis:       lease.FrontierUpdatedUnixMillis,
		}
	}
	return result
}

func writeBackupError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, backupusecase.ErrDisabled):
		jsonError(c, http.StatusServiceUnavailable, "backup_disabled", "cluster backup is disabled")
	case errors.Is(err, backupusecase.ErrInvalidRequest):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid backup request")
	case errors.Is(err, backupusecase.ErrDoctorUnhealthy):
		jsonError(c, http.StatusServiceUnavailable, "backup_doctor_unhealthy", "backup dependency preflight is not healthy")
	case errors.Is(err, backupusecase.ErrControllerLeaderUnavailable):
		c.Header("Retry-After", "1")
		jsonError(c, http.StatusServiceUnavailable, "controller_leader_unavailable", "backup coordinator is temporarily unavailable")
	case errors.Is(err, backupusecase.ErrSourceFenceExists):
		jsonError(c, http.StatusConflict, "source_fence_exists", "source generation is already fenced for a different restore plan")
	case errors.Is(err, backupusecase.ErrStateConflict):
		jsonError(c, http.StatusConflict, "state_conflict", "backup state changed")
	case errors.Is(err, backupusecase.ErrCheckpointNotFound):
		jsonError(c, http.StatusNotFound, "checkpoint_not_found", "checkpoint not found")
	default:
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control unavailable")
	}
}
