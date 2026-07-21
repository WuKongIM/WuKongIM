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

// BackupManagement is the narrow Manager-facing cluster backup control seam.
type BackupManagement interface {
	Status(context.Context) (backupusecase.StatusSnapshot, error)
	ListRestorePoints(context.Context) ([]backupusecase.RestorePoint, error)
	Trigger(context.Context, backupartifact.RestorePointKind) (backupusecase.Job, error)
	Cancel(context.Context, string, uint64) (backupusecase.Job, error)
	Hold(context.Context, string) (backupusecase.RestorePoint, error)
	Release(context.Context, string) (backupusecase.RestorePoint, error)
	Verify(context.Context, string) (backupusecase.Verification, error)
}

type backupJobDTO struct {
	ID                  string                          `json:"id"`
	Epoch               uint64                          `json:"epoch"`
	Kind                backupartifact.RestorePointKind `json:"kind"`
	Status              backupusecase.JobStatus         `json:"status"`
	HashSlotCount       uint16                          `json:"hash_slot_count"`
	CompletedPartitions int                             `json:"completed_partitions"`
	StartedAtUnixMillis int64                           `json:"started_at_unix_millis"`
	UpdatedAtUnixMillis int64                           `json:"updated_at_unix_millis"`
	FailureCategory     string                          `json:"failure_category,omitempty"`
}

type backupRestorePointDTO struct {
	ID                    string                          `json:"id"`
	Kind                  backupartifact.RestorePointKind `json:"kind"`
	EffectiveAtUnixMillis int64                           `json:"effective_at_unix_millis"`
	CreatedAtUnixMillis   int64                           `json:"created_at_unix_millis"`
	PrimaryVerified       bool                            `json:"primary_verified"`
	SecondaryVerified     bool                            `json:"secondary_verified"`
	Held                  bool                            `json:"held"`
}

type backupStatusDTO struct {
	Enabled                 bool                   `json:"enabled"`
	Health                  backupusecase.Health   `json:"health"`
	RecoveryPointAgeSeconds *int64                 `json:"recovery_point_age_seconds"`
	VerificationAgeSeconds  *int64                 `json:"verification_age_seconds"`
	PendingGarbageCount     int                    `json:"pending_garbage_count"`
	FailureCategory         string                 `json:"failure_category,omitempty"`
	Active                  *backupJobDTO          `json:"active,omitempty"`
	Latest                  *backupRestorePointDTO `json:"latest,omitempty"`
}

type backupRestorePointListDTO struct {
	Items []backupRestorePointDTO `json:"items"`
}

type backupTriggerRequestDTO struct {
	Kind backupartifact.RestorePointKind `json:"kind"`
}

type backupCancelRequestDTO struct {
	Epoch uint64 `json:"epoch"`
}

type backupVerificationDTO struct {
	RestorePointID       string `json:"restore_point_id"`
	VerifiedAtUnixMillis int64  `json:"verified_at_unix_millis"`
	PrimaryVerified      bool   `json:"primary_verified"`
	SecondaryVerified    bool   `json:"secondary_verified"`
	ManifestSHA256       string `json:"manifest_sha256"`
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
	c.JSON(http.StatusOK, backupStatusResponse(status))
}

func (s *Server) handleBackupRestorePoints(c *gin.Context) {
	if s == nil || s.backup == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control is not configured")
		return
	}
	points, err := s.backup.ListRestorePoints(c.Request.Context())
	if err != nil {
		writeBackupError(c, err)
		return
	}
	items := make([]backupRestorePointDTO, len(points))
	for index := range points {
		items[index] = backupRestorePointResponse(points[index])
	}
	c.JSON(http.StatusOK, backupRestorePointListDTO{Items: items})
}

func (s *Server) handleBackupTrigger(c *gin.Context) {
	if s == nil || s.backup == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control is not configured")
		return
	}
	var request backupTriggerRequestDTO
	if err := c.ShouldBindJSON(&request); err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid backup trigger request")
		return
	}
	job, err := s.backup.Trigger(c.Request.Context(), request.Kind)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, backupJobResponse(job))
}

func (s *Server) handleBackupCancel(c *gin.Context) {
	if s == nil || s.backup == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control is not configured")
		return
	}
	var request backupCancelRequestDTO
	if err := c.ShouldBindJSON(&request); err != nil || request.Epoch == 0 {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid backup cancel request")
		return
	}
	job, err := s.backup.Cancel(c.Request.Context(), strings.TrimSpace(c.Param("job_id")), request.Epoch)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.JSON(http.StatusOK, backupJobResponse(job))
}

func (s *Server) handleBackupHold(c *gin.Context)    { s.handleBackupHoldMutation(c, true) }
func (s *Server) handleBackupRelease(c *gin.Context) { s.handleBackupHoldMutation(c, false) }

func (s *Server) handleBackupVerify(c *gin.Context) {
	if s == nil || s.backup == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control is not configured")
		return
	}
	result, err := s.backup.Verify(c.Request.Context(), strings.TrimSpace(c.Param("restore_point_id")))
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.JSON(http.StatusOK, backupVerificationDTO{
		RestorePointID: result.RestorePointID, VerifiedAtUnixMillis: result.VerifiedAtUnixMillis,
		PrimaryVerified: result.PrimaryVerified, SecondaryVerified: result.SecondaryVerified,
		ManifestSHA256: result.ManifestSHA256,
	})
}

func (s *Server) handleBackupHoldMutation(c *gin.Context, held bool) {
	if s == nil || s.backup == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control is not configured")
		return
	}
	id := strings.TrimSpace(c.Param("restore_point_id"))
	var (
		point backupusecase.RestorePoint
		err   error
	)
	if held {
		point, err = s.backup.Hold(c.Request.Context(), id)
	} else {
		point, err = s.backup.Release(c.Request.Context(), id)
	}
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.JSON(http.StatusOK, backupRestorePointResponse(point))
}

func backupStatusResponse(status backupusecase.StatusSnapshot) backupStatusDTO {
	result := backupStatusDTO{
		Enabled: status.Enabled, Health: status.Health, RecoveryPointAgeSeconds: status.RecoveryPointAgeSeconds,
		VerificationAgeSeconds: status.VerificationAgeSeconds, PendingGarbageCount: status.PendingGarbageCount,
		FailureCategory: status.FailureCategory,
	}
	if status.Active != nil {
		active := backupJobResponse(*status.Active)
		result.Active = &active
	}
	if status.Latest != nil {
		latest := backupRestorePointResponse(*status.Latest)
		result.Latest = &latest
	}
	return result
}

func backupJobResponse(job backupusecase.Job) backupJobDTO {
	return backupJobDTO{
		ID: job.ID, Epoch: job.Epoch, Kind: job.Kind, Status: job.Status,
		HashSlotCount: job.HashSlotCount, CompletedPartitions: len(job.Partitions),
		StartedAtUnixMillis: job.StartedAtUnixMillis, UpdatedAtUnixMillis: job.UpdatedAtUnixMillis,
		FailureCategory: job.FailureCategory,
	}
}

func backupRestorePointResponse(point backupusecase.RestorePoint) backupRestorePointDTO {
	return backupRestorePointDTO{
		ID: point.ID, Kind: point.Kind, EffectiveAtUnixMillis: point.EffectiveAtUnixMillis,
		CreatedAtUnixMillis: point.CreatedAtUnixMillis, PrimaryVerified: point.PrimaryVerified,
		SecondaryVerified: point.SecondaryVerified, Held: point.Held,
	}
}

func writeBackupError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, backupusecase.ErrDisabled):
		jsonError(c, http.StatusServiceUnavailable, "backup_disabled", "cluster backup is disabled")
	case errors.Is(err, backupusecase.ErrInvalidRequest):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid backup request")
	case errors.Is(err, backupusecase.ErrJobActive), errors.Is(err, backupusecase.ErrStateConflict):
		jsonError(c, http.StatusConflict, "conflict", "backup state changed")
	case errors.Is(err, backupusecase.ErrJobNotFound), errors.Is(err, backupusecase.ErrRestorePointNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "backup resource not found")
	default:
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control unavailable")
	}
}
