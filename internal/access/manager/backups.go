package manager

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/gin-gonic/gin"
)

// BackupManagement is the narrow Manager-facing cluster backup control seam.
type BackupManagement interface {
	Status(context.Context) (backupusecase.StatusSnapshot, error)
	ListRestorePointsPage(context.Context, backupusecase.RestorePointListRequest) (backupusecase.RestorePointPage, error)
	Trigger(context.Context, backupartifact.RestorePointKind) (backupusecase.Job, error)
	Cancel(context.Context, string, uint64) (backupusecase.Job, error)
	Hold(context.Context, string) (backupusecase.RestorePoint, error)
	Release(context.Context, string) (backupusecase.RestorePoint, error)
	StartVerification(context.Context, string) (backupusecase.VerificationTask, error)
}

type backupJobDTO struct {
	ID            string                          `json:"id"`
	Epoch         uint64                          `json:"epoch"`
	Kind          backupartifact.RestorePointKind `json:"kind"`
	Status        backupusecase.JobStatus         `json:"status"`
	HashSlotCount uint16                          `json:"hash_slot_count"`
	// RestorePointID is the preallocated immutable publication identity for this job.
	RestorePointID      string `json:"restore_point_id"`
	CompletedPartitions int    `json:"completed_partitions"`
	StartedAtUnixMillis int64  `json:"started_at_unix_millis"`
	UpdatedAtUnixMillis int64  `json:"updated_at_unix_millis"`
	FailureCategory     string `json:"failure_category,omitempty"`
}

type backupRestorePointDTO struct {
	ID                    string                          `json:"id"`
	Kind                  backupartifact.RestorePointKind `json:"kind"`
	EffectiveAtUnixMillis int64                           `json:"effective_at_unix_millis"`
	CreatedAtUnixMillis   int64                           `json:"created_at_unix_millis"`
	PrimaryVerified       bool                            `json:"primary_verified"`
	SecondaryVerified     bool                            `json:"secondary_verified"`
	Held                  bool                            `json:"held"`
	LastVerification      *backupVerificationEvidenceDTO  `json:"last_verification,omitempty"`
}

type backupStatusDTO struct {
	Enabled                    bool                       `json:"enabled"`
	Health                     backupusecase.Health       `json:"health"`
	RecoveryPointAgeSeconds    *int64                     `json:"recovery_point_age_seconds"`
	VerificationAgeSeconds     *int64                     `json:"verification_age_seconds"`
	PendingGarbageCount        int                        `json:"pending_garbage_count"`
	FailureCategory            string                     `json:"failure_category,omitempty"`
	Active                     *backupJobDTO              `json:"active,omitempty"`
	Latest                     *backupRestorePointDTO     `json:"latest,omitempty"`
	Verification               *backupVerificationTaskDTO `json:"verification,omitempty"`
	CoordinatorNodeID          uint64                     `json:"coordinator_node_id"`
	ObservedAtUnixMillis       int64                      `json:"observed_at_unix_millis"`
	AuthEnabled                bool                       `json:"auth_enabled"`
	Running                    bool                       `json:"running"`
	MaxRecoveryPointAgeSeconds int64                      `json:"max_recovery_point_age_seconds"`
	MaxVerificationAgeSeconds  int64                      `json:"max_verification_age_seconds"`
	Policy                     backupPolicyDTO            `json:"policy"`
	Dependencies               backupDependenciesDTO      `json:"dependencies"`
	Capacity                   backupCapacityDTO          `json:"capacity"`
}

type backupPolicyDTO struct {
	IncrementalIntervalSeconds      int64  `json:"incremental_interval_seconds"`
	RestorePointIntervalSeconds     int64  `json:"restore_point_interval_seconds"`
	IndependentFullIntervalSeconds  int64  `json:"independent_full_interval_seconds"`
	MaterializedFullIntervalSeconds int64  `json:"materialized_full_interval_seconds"`
	MonthlyRetentionMonths          int    `json:"monthly_retention_months"`
	ObjectLockDays                  int    `json:"object_lock_days"`
	MaxParallelPartitions           int    `json:"max_parallel_partitions"`
	StagingMaxBytes                 uint64 `json:"staging_max_bytes"`
	PrimaryRegion                   string `json:"primary_region"`
	SecondaryRegion                 string `json:"secondary_region"`
	KMSRegion                       string `json:"kms_region"`
}

type backupDependencyDTO struct {
	Health backupusecase.Health `json:"health"`
	Region string               `json:"region,omitempty"`
}

type backupDependenciesDTO struct {
	Primary             backupDependencyDTO `json:"primary"`
	Secondary           backupDependencyDTO `json:"secondary"`
	KMS                 backupDependencyDTO `json:"kms"`
	Staging             backupDependencyDTO `json:"staging"`
	UTC                 backupDependencyDTO `json:"utc"`
	CheckedAtUnixMillis int64               `json:"checked_at_unix_millis,omitempty"`
}

type backupCapacityDTO struct {
	Total      int    `json:"total"`
	Held       int    `json:"held"`
	Pending    int    `json:"pending"`
	Max        int    `json:"max"`
	WarningAt  int    `json:"warning_at"`
	CriticalAt int    `json:"critical_at"`
	Level      string `json:"level"`
}

type backupRestorePointListDTO struct {
	Items      []backupRestorePointDTO `json:"items"`
	NextCursor string                  `json:"next_cursor,omitempty"`
	Total      int                     `json:"total"`
}

type backupTriggerRequestDTO struct {
	Kind backupartifact.RestorePointKind `json:"kind"`
}

type backupCancelRequestDTO struct {
	Epoch uint64 `json:"epoch"`
}

type backupVerificationEvidenceDTO struct {
	Status                backupusecase.VerificationTaskStatus `json:"status"`
	StartedAtUnixMillis   int64                                `json:"started_at_unix_millis"`
	CompletedAtUnixMillis int64                                `json:"completed_at_unix_millis,omitempty"`
	PrimaryVerified       bool                                 `json:"primary_verified"`
	SecondaryVerified     bool                                 `json:"secondary_verified"`
	ManifestSHA256        string                               `json:"manifest_sha256,omitempty"`
	FailureCategory       string                               `json:"failure_category,omitempty"`
}

type backupVerificationTaskDTO struct {
	ID             string `json:"id"`
	RestorePointID string `json:"restore_point_id"`
	backupVerificationEvidenceDTO
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

func (s *Server) handleBackupRestorePoints(c *gin.Context) {
	if s == nil || s.backup == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control is not configured")
		return
	}
	request := backupusecase.RestorePointListRequest{
		Cursor:  strings.TrimSpace(c.Query("cursor")),
		IDQuery: strings.TrimSpace(c.Query("id")),
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			jsonError(c, http.StatusBadRequest, "bad_request", "invalid restore-point page limit")
			return
		}
		if limit > backupusecase.MaxRestorePointPageSize {
			limit = backupusecase.MaxRestorePointPageSize
		}
		request.Limit = limit
	}
	if raw := strings.TrimSpace(c.Query("held")); raw != "" {
		held, err := strconv.ParseBool(raw)
		if err != nil {
			jsonError(c, http.StatusBadRequest, "bad_request", "invalid restore-point held filter")
			return
		}
		request.HeldOnly = held
	}
	page, err := s.backup.ListRestorePointsPage(c.Request.Context(), request)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	items := make([]backupRestorePointDTO, len(page.Items))
	for index := range page.Items {
		items[index] = backupRestorePointResponse(page.Items[index])
	}
	c.JSON(http.StatusOK, backupRestorePointListDTO{Items: items, NextCursor: page.NextCursor, Total: page.Total})
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
	result, err := s.backup.StartVerification(c.Request.Context(), strings.TrimSpace(c.Param("restore_point_id")))
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, backupVerificationTaskResponse(result))
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
		FailureCategory: status.FailureCategory, CoordinatorNodeID: status.CoordinatorNodeID,
		ObservedAtUnixMillis: status.ObservedAtUnixMillis, Running: status.Running,
		MaxRecoveryPointAgeSeconds: status.MaxRecoveryPointAgeSeconds,
		MaxVerificationAgeSeconds:  status.MaxVerificationAgeSeconds,
		Policy:                     backupPolicyResponse(status.Policy), Dependencies: backupDependenciesResponse(status.Dependencies),
		Capacity: backupCapacityResponse(status.Capacity),
	}
	if status.Active != nil {
		active := backupJobResponse(*status.Active)
		result.Active = &active
	}
	if status.Latest != nil {
		latest := backupRestorePointResponse(*status.Latest)
		result.Latest = &latest
	}
	if status.Verification != nil {
		verification := backupVerificationTaskResponse(*status.Verification)
		result.Verification = &verification
	}
	return result
}

func backupPolicyResponse(policy backupusecase.PolicySnapshot) backupPolicyDTO {
	return backupPolicyDTO{
		IncrementalIntervalSeconds:      policy.IncrementalIntervalSeconds,
		RestorePointIntervalSeconds:     policy.RestorePointIntervalSeconds,
		IndependentFullIntervalSeconds:  policy.IndependentFullIntervalSeconds,
		MaterializedFullIntervalSeconds: policy.MaterializedFullIntervalSeconds,
		MonthlyRetentionMonths:          policy.MonthlyRetentionMonths, ObjectLockDays: policy.ObjectLockDays,
		MaxParallelPartitions: policy.MaxParallelPartitions, StagingMaxBytes: policy.StagingMaxBytes,
		PrimaryRegion: policy.PrimaryRegion, SecondaryRegion: policy.SecondaryRegion, KMSRegion: policy.KMSRegion,
	}
}

func backupDependenciesResponse(dependencies backupusecase.DependenciesSnapshot) backupDependenciesDTO {
	return backupDependenciesDTO{
		Primary:             backupDependencyDTO{Health: dependencies.Primary.Health, Region: dependencies.Primary.Region},
		Secondary:           backupDependencyDTO{Health: dependencies.Secondary.Health, Region: dependencies.Secondary.Region},
		KMS:                 backupDependencyDTO{Health: dependencies.KMS.Health, Region: dependencies.KMS.Region},
		Staging:             backupDependencyDTO{Health: dependencies.Staging.Health},
		UTC:                 backupDependencyDTO{Health: dependencies.UTC.Health},
		CheckedAtUnixMillis: dependencies.CheckedAtUnixMillis,
	}
}

func backupCapacityResponse(capacity backupusecase.CapacitySnapshot) backupCapacityDTO {
	return backupCapacityDTO{
		Total: capacity.Total, Held: capacity.Held, Pending: capacity.Pending, Max: capacity.Max,
		WarningAt: capacity.WarningAt, CriticalAt: capacity.CriticalAt, Level: capacity.Level,
	}
}

func backupJobResponse(job backupusecase.Job) backupJobDTO {
	return backupJobDTO{
		ID: job.ID, Epoch: job.Epoch, Kind: job.Kind, Status: job.Status,
		HashSlotCount: job.HashSlotCount, RestorePointID: job.RestorePointID, CompletedPartitions: len(job.Partitions),
		StartedAtUnixMillis: job.StartedAtUnixMillis, UpdatedAtUnixMillis: job.UpdatedAtUnixMillis,
		FailureCategory: job.FailureCategory,
	}
}

func backupRestorePointResponse(point backupusecase.RestorePoint) backupRestorePointDTO {
	return backupRestorePointDTO{
		ID: point.ID, Kind: point.Kind, EffectiveAtUnixMillis: point.EffectiveAtUnixMillis,
		CreatedAtUnixMillis: point.CreatedAtUnixMillis, PrimaryVerified: point.PrimaryVerified,
		SecondaryVerified: point.SecondaryVerified, Held: point.Held,
		LastVerification: backupVerificationEvidenceResponse(point.LastVerification),
	}
}

func backupVerificationTaskResponse(task backupusecase.VerificationTask) backupVerificationTaskDTO {
	return backupVerificationTaskDTO{
		ID: task.ID, RestorePointID: task.RestorePointID,
		backupVerificationEvidenceDTO: *backupVerificationEvidenceResponse(&task.VerificationEvidence),
	}
}

func backupVerificationEvidenceResponse(evidence *backupusecase.VerificationEvidence) *backupVerificationEvidenceDTO {
	if evidence == nil {
		return nil
	}
	return &backupVerificationEvidenceDTO{
		Status: evidence.Status, StartedAtUnixMillis: evidence.StartedAtUnixMillis,
		CompletedAtUnixMillis: evidence.CompletedAtUnixMillis,
		PrimaryVerified:       evidence.PrimaryVerified, SecondaryVerified: evidence.SecondaryVerified,
		ManifestSHA256: evidence.ManifestSHA256, FailureCategory: evidence.FailureCategory,
	}
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
	case errors.Is(err, backupusecase.ErrJobActive):
		jsonError(c, http.StatusConflict, "backup_job_active", "a backup job is already active")
	case errors.Is(err, backupusecase.ErrVerificationJobActive):
		jsonError(c, http.StatusConflict, "verification_job_active", "a verification job is already active")
	case errors.Is(err, backupusecase.ErrStateConflict), errors.Is(err, backupusecase.ErrJobNotFound):
		jsonError(c, http.StatusConflict, "state_conflict", "backup state changed")
	case errors.Is(err, backupusecase.ErrRestorePointNotFound):
		jsonError(c, http.StatusNotFound, "restore_point_not_found", "restore point not found")
	default:
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "backup control unavailable")
	}
}
