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
	RestoreStatus(context.Context) (*backupusecase.RestorePlan, error)
	VerifyRestore(context.Context, string) (backupusecase.RestorePlan, error)
	ActivateRestore(context.Context, string, string) (backupusecase.RestorePlan, error)
}

type restorePlanRequestDTO struct {
	RestorePointID   string `json:"restore_point_id"`
	LatestVerified   bool   `json:"latest_verified"`
	Repository       string `json:"repository"`
	InvalidateTokens bool   `json:"invalidate_tokens"`
}

type restoreActivationRequestDTO struct {
	OldClusterFenceDigest string `json:"old_cluster_fence_digest"`
}

type restorePartitionDTO struct {
	HashSlot            uint16 `json:"hash_slot"`
	Installed           bool   `json:"installed"`
	Verified            bool   `json:"verified"`
	PlainBytes          uint64 `json:"plain_bytes"`
	MessageCount        uint64 `json:"message_count"`
	MetadataSHA256      string `json:"metadata_sha256"`
	FailureCategory     string `json:"failure_category,omitempty"`
	UpdatedAtUnixMillis int64  `json:"updated_at_unix_millis"`
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
	plan, err := s.restore.RestoreStatus(c.Request.Context())
	if err != nil {
		writeRestoreError(c, err)
		return
	}
	if plan == nil {
		c.JSON(http.StatusOK, gin.H{"plan": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"plan": restorePlanResponse(*plan)})
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
		InvalidateTokens: plan.InvalidateTokens, EstimatedPlainBytes: plan.EstimatedPlainBytes, EstimatedCipherBytes: plan.EstimatedCipherBytes,
		Status: plan.Status, CreatedAtUnixMillis: plan.CreatedAtUnixMillis, UpdatedAtUnixMillis: plan.UpdatedAtUnixMillis,
		VerifiedAtUnixMillis: plan.VerifiedAtUnixMillis, ActivatedAtUnixMillis: plan.ActivatedAtUnixMillis,
		Partitions: make([]restorePartitionDTO, len(plan.Partitions)),
	}
	for index, partition := range plan.Partitions {
		result.Partitions[index] = restorePartitionDTO{
			HashSlot: partition.HashSlot, Installed: partition.Installed, Verified: partition.Verified,
			PlainBytes: partition.PlainBytes, MessageCount: partition.MessageCount,
			MetadataSHA256:  partition.MetadataSHA256,
			FailureCategory: partition.FailureCategory, UpdatedAtUnixMillis: partition.UpdatedAtUnixMillis,
		}
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
