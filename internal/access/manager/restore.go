package manager

import (
	"context"
	"errors"
	"net/http"
	"strings"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/gin-gonic/gin"
)

// RestoreManagement is the narrow recovery-only Manager seam.
type RestoreManagement interface {
	PlanRestore(context.Context, backupusecase.RestorePlanRequest) (backupusecase.RestorePlan, error)
	StartRestore(context.Context, string) (backupusecase.RestorePlan, error)
	RestoreProgress(context.Context) (*backupusecase.RestoreProgress, error)
	VerifyRestore(context.Context, string) (backupusecase.RestorePlan, error)
	ActivateRestore(context.Context, string, string) (backupusecase.RestorePlan, error)
}

type restorePlanRequestDTO struct {
	RestorePointID   string                               `json:"restore_point_id"`
	LatestVerified   bool                                 `json:"latest_verified"`
	Repository       string                               `json:"repository"`
	InvalidateTokens bool                                 `json:"invalidate_tokens"`
	CatalogHead      *backupartifact.CatalogPageReference `json:"catalog_head"`
}

type restoreActivationRequestDTO struct {
	OldClusterFenceDigest string `json:"old_cluster_fence_digest"`
}

type restorePartitionDTO struct {
	HashSlot              uint16                               `json:"hash_slot"`
	Status                backupusecase.RestorePartitionStatus `json:"status"`
	TargetSlotID          uint32                               `json:"target_slot_id,omitempty"`
	LeaderNodeID          uint64                               `json:"leader_node_id,omitempty"`
	LeaderTerm            uint64                               `json:"leader_term,omitempty"`
	ConfigEpoch           uint64                               `json:"config_epoch,omitempty"`
	InstallAttempt        uint64                               `json:"install_attempt,omitempty"`
	Installed             bool                                 `json:"installed"`
	Verified              bool                                 `json:"verified"`
	PlainBytes            uint64                               `json:"plain_bytes"`
	MetadataRecordCount   uint64                               `json:"metadata_record_count"`
	MessageCount          uint64                               `json:"message_count"`
	ChannelBoundaryCount  uint64                               `json:"channel_boundary_count"`
	DownloadedBytes       uint64                               `json:"downloaded_bytes"`
	ReplicatedBytes       uint64                               `json:"replicated_bytes"`
	ReplicaCount          uint32                               `json:"replica_count"`
	ConvergedReplicas     uint32                               `json:"converged_replicas"`
	FailureCategory       string                               `json:"failure_category,omitempty"`
	StartedAtUnixMillis   int64                                `json:"started_at_unix_millis,omitempty"`
	InstalledAtUnixMillis int64                                `json:"installed_at_unix_millis,omitempty"`
	UpdatedAtUnixMillis   int64                                `json:"updated_at_unix_millis"`
}

type restoreProgressDTO struct {
	PlanID                   string                      `json:"plan_id"`
	Status                   backupusecase.RestoreStatus `json:"status"`
	TotalSlots               uint16                      `json:"total_slots"`
	PendingSlots             uint16                      `json:"pending_slots"`
	InstallingSlots          uint16                      `json:"installing_slots"`
	InstalledSlots           uint16                      `json:"installed_slots"`
	ConvergedSlots           uint16                      `json:"converged_slots"`
	FailedSlots              uint16                      `json:"failed_slots"`
	DownloadedBytes          uint64                      `json:"downloaded_bytes"`
	ReplicatedBytes          uint64                      `json:"replicated_bytes"`
	ThroughputBytesPerSecond uint64                      `json:"throughput_bytes_per_second"`
	ETASeconds               *uint64                     `json:"eta_seconds"`
	Partitions               []restorePartitionDTO       `json:"partitions"`
}

type restorePlanDTO struct {
	ID                    string                      `json:"id"`
	RestorePointID        string                      `json:"restore_point_id"`
	ManifestSHA256        string                      `json:"manifest_sha256"`
	Repository            string                      `json:"repository"`
	SourceClusterID       string                      `json:"source_cluster_id"`
	SourceGeneration      string                      `json:"source_generation"`
	TargetClusterID       string                      `json:"target_cluster_id"`
	TargetGeneration      string                      `json:"target_generation"`
	HashSlotCount         uint16                      `json:"hash_slot_count"`
	ErasureLedgerVersion  uint32                      `json:"erasure_ledger_version"`
	ErasureEventCount     uint64                      `json:"erasure_event_count"`
	ErasureStreams        []backupErasureStreamDTO    `json:"erasure_streams"`
	ErasureLedgerSHA256   string                      `json:"erasure_ledger_sha256"`
	InvalidateTokens      bool                        `json:"invalidate_tokens"`
	EstimatedPlainBytes   *uint64                     `json:"estimated_plain_bytes"`
	EstimatedCipherBytes  *uint64                     `json:"estimated_cipher_bytes"`
	Status                backupusecase.RestoreStatus `json:"status"`
	CreatedAtUnixMillis   int64                       `json:"created_at_unix_millis"`
	UpdatedAtUnixMillis   int64                       `json:"updated_at_unix_millis"`
	VerifiedAtUnixMillis  int64                       `json:"verified_at_unix_millis"`
	ActivatedAtUnixMillis int64                       `json:"activated_at_unix_millis"`
	Partitions            []restorePartitionDTO       `json:"partitions"`
}

func (s *Server) registerRestoreRoutes() {
	reads := s.engine.Group("/manager")
	if s.auth.enabled() {
		reads.Use(s.requirePermission("cluster.backup", "r"))
	}
	reads.GET("/restore/status", s.handleRestoreStatus)

	writes := s.engine.Group("/manager")
	if s.auth.enabled() {
		writes.Use(s.requirePermission("cluster.backup", "w"))
	}
	writes.POST("/restore/plan", s.handleRestorePlan)
	writes.POST("/restore/:plan_id/start", s.handleRestoreStart)
	writes.POST("/restore/:plan_id/verify", s.handleRestoreVerify)

	activation := s.engine.Group("/manager")
	activation.Use(s.requireExplicitPermission("cluster.restore.activation", "w"))
	activation.POST("/restore/:plan_id/activate", s.handleRestoreActivate)
}

func (s *Server) handleRestorePlan(c *gin.Context) {
	if s == nil || s.restore == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "restore control is not configured")
		return
	}
	var request restorePlanRequestDTO
	if err := c.ShouldBindJSON(&request); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid restore plan request")
		return
	}
	plan, err := s.restore.PlanRestore(c.Request.Context(), backupusecase.RestorePlanRequest{
		RestorePointID: strings.TrimSpace(request.RestorePointID), LatestVerified: request.LatestVerified,
		Repository: strings.TrimSpace(request.Repository), InvalidateTokens: request.InvalidateTokens,
		CatalogHead: request.CatalogHead,
	})
	if err != nil {
		writeRestoreError(c, err)
		return
	}
	c.JSON(http.StatusCreated, restorePlanResponse(plan))
}

func (s *Server) handleRestoreStart(c *gin.Context) {
	if s == nil || s.restore == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "restore control is not configured")
		return
	}
	plan, err := s.restore.StartRestore(c.Request.Context(), strings.TrimSpace(c.Param("plan_id")))
	if err != nil {
		writeRestoreError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, restorePlanResponse(plan))
}

func (s *Server) handleRestoreStatus(c *gin.Context) {
	if s == nil || s.restore == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "restore control is not configured")
		return
	}
	progress, err := s.restore.RestoreProgress(c.Request.Context())
	if err != nil {
		writeRestoreError(c, err)
		return
	}
	if progress == nil {
		c.JSON(http.StatusOK, gin.H{"progress": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"progress": restoreProgressResponse(*progress)})
}

func (s *Server) handleRestoreVerify(c *gin.Context) {
	if s == nil || s.restore == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "restore control is not configured")
		return
	}
	plan, err := s.restore.VerifyRestore(c.Request.Context(), strings.TrimSpace(c.Param("plan_id")))
	if err != nil {
		writeRestoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, restorePlanResponse(plan))
}

func (s *Server) handleRestoreActivate(c *gin.Context) {
	if s == nil || s.restore == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "restore control is not configured")
		return
	}
	var request restoreActivationRequestDTO
	if err := c.ShouldBindJSON(&request); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid restore activation request")
		return
	}
	plan, err := s.restore.ActivateRestore(c.Request.Context(), strings.TrimSpace(c.Param("plan_id")), strings.TrimSpace(request.OldClusterFenceDigest))
	if err != nil {
		writeRestoreError(c, err)
		return
	}
	c.JSON(http.StatusOK, restorePlanResponse(plan))
}

func restorePlanResponse(plan backupusecase.RestorePlan) restorePlanDTO {
	result := restorePlanDTO{
		ID: plan.ID, RestorePointID: plan.RestorePointID, ManifestSHA256: plan.ManifestSHA256, Repository: plan.Repository,
		SourceClusterID: plan.SourceClusterID, SourceGeneration: plan.SourceGeneration,
		TargetClusterID: plan.TargetClusterID, TargetGeneration: plan.TargetGeneration, HashSlotCount: plan.HashSlotCount,
		ErasureLedgerVersion: plan.ErasureLedgerVersion, ErasureEventCount: plan.ErasureEventCount,
		ErasureStreams: restoreErasureStreamResponses(plan.ErasureHeads), ErasureLedgerSHA256: plan.ErasureLedgerSHA256,
		InvalidateTokens: plan.InvalidateTokens, EstimatedPlainBytes: plan.EstimatedPlainBytes, EstimatedCipherBytes: plan.EstimatedCipherBytes,
		Status: plan.Status, CreatedAtUnixMillis: plan.CreatedAtUnixMillis, UpdatedAtUnixMillis: plan.UpdatedAtUnixMillis,
		VerifiedAtUnixMillis: plan.VerifiedAtUnixMillis, ActivatedAtUnixMillis: plan.ActivatedAtUnixMillis,
		Partitions: make([]restorePartitionDTO, len(plan.Partitions)),
	}
	for index, partition := range plan.Partitions {
		result.Partitions[index] = restorePartitionResponse(partition)
	}
	return result
}

func restoreProgressResponse(
	progress backupusecase.RestoreProgress,
) restoreProgressDTO {
	result := restoreProgressDTO{
		PlanID: progress.PlanID, Status: progress.Status,
		TotalSlots: progress.TotalSlots, PendingSlots: progress.PendingSlots,
		InstallingSlots:          progress.InstallingSlots,
		InstalledSlots:           progress.InstalledSlots,
		ConvergedSlots:           progress.ConvergedSlots,
		FailedSlots:              progress.FailedSlots,
		DownloadedBytes:          progress.DownloadedBytes,
		ReplicatedBytes:          progress.ReplicatedBytes,
		ThroughputBytesPerSecond: progress.ThroughputBytesPerSecond,
		ETASeconds:               progress.ETASeconds,
		Partitions:               make([]restorePartitionDTO, len(progress.Partitions)),
	}
	for index, partition := range progress.Partitions {
		result.Partitions[index] = restorePartitionResponse(partition)
	}
	return result
}

func restorePartitionResponse(
	partition backupusecase.RestorePartition,
) restorePartitionDTO {
	return restorePartitionDTO{
		HashSlot: partition.HashSlot, Status: partition.Status,
		TargetSlotID: partition.TargetSlotID,
		LeaderNodeID: partition.LeaderNodeID, LeaderTerm: partition.LeaderTerm,
		ConfigEpoch: partition.ConfigEpoch, InstallAttempt: partition.InstallAttempt,
		Installed: partition.Installed, Verified: partition.Verified,
		PlainBytes:            partition.PlainBytes,
		MetadataRecordCount:   partition.MetadataRecordCount,
		MessageCount:          partition.MessageCount,
		ChannelBoundaryCount:  partition.ChannelBoundaryCount,
		DownloadedBytes:       partition.DownloadedBytes,
		ReplicatedBytes:       partition.ReplicatedBytes,
		ReplicaCount:          partition.ReplicaCount,
		ConvergedReplicas:     partition.ConvergedReplicas,
		FailureCategory:       partition.FailureCategory,
		StartedAtUnixMillis:   partition.StartedAtUnixMillis,
		InstalledAtUnixMillis: partition.InstalledAtUnixMillis,
		UpdatedAtUnixMillis:   partition.UpdatedAtUnixMillis,
	}
}

func restoreErasureStreamResponses(heads []backupartifact.ErasureStreamHead) []backupErasureStreamDTO {
	result := make([]backupErasureStreamDTO, len(heads))
	for index, head := range heads {
		result[index] = backupErasureStreamDTO{HashSlot: head.HashSlot, Sequence: head.Sequence}
	}
	return result
}

func writeRestoreError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, backupusecase.ErrRestoreModeRequired):
		jsonError(c, http.StatusServiceUnavailable, "restore_mode_required", "explicit restore mode is required")
	case errors.Is(err, backupusecase.ErrInvalidRequest), errors.Is(err, backupusecase.ErrOldClusterFenceRequired), errors.Is(err, backupartifact.ErrInvalidManifest):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid or unsafe restore request")
	case errors.Is(err, backupusecase.ErrRestorePlanExists), errors.Is(err, backupusecase.ErrRestoreTransition), errors.Is(err, backupusecase.ErrStateConflict):
		jsonError(c, http.StatusConflict, "conflict", "restore state changed")
	case errors.Is(err, backupusecase.ErrRestorePointNotFound), errors.Is(err, backupartifact.ErrObjectNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "restore resource not found")
	default:
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "restore control unavailable")
	}
}
