package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/gin-gonic/gin"
)

// BackupManagement is the complete Manager-facing scheduled-backup seam.
type BackupManagement interface {
	Dashboard(context.Context) (backupusecase.Dashboard, error)
	Configure(
		context.Context,
		backupusecase.ConfigureManagementRequest,
	) (backupusecase.ConfigureResult, error)
	TestRepository(
		context.Context,
		backupusecase.TestRepositoryRequest,
	) (backupcontract.Plan, error)
	StartBackup(context.Context) (backupcontract.BackupJob, error)
	CancelBackup(context.Context, string) error
	Archive(context.Context, string) (backupusecase.ArchiveDetail, error)
	VerifyArchive(context.Context, string) (backupusecase.ArchiveDetail, error)
	HoldArchive(
		context.Context,
		string,
		bool,
		string,
	) (backupusecase.ArchiveSummary, error)
	DeleteArchive(context.Context, string) error
}

type backupStoreRequest struct {
	Kind      backupcontract.StoreKind `json:"kind"`
	Endpoint  string                   `json:"endpoint"`
	Region    string                   `json:"region"`
	Bucket    string                   `json:"bucket"`
	Prefix    string                   `json:"prefix"`
	PathStyle bool                     `json:"path_style"`
	AccessKey string                   `json:"access_key"`
	SecretKey string                   `json:"secret_key"`
}

type backupPlanRequest struct {
	ExpectedRevision uint64             `json:"expected_revision"`
	Enabled          bool               `json:"enabled"`
	Store            backupStoreRequest `json:"store"`
	Cron             string             `json:"cron"`
	TimeZone         string             `json:"time_zone"`
	RetentionCount   int                `json:"retention_count"`
	RateMiBPerSecond uint64             `json:"rate_mib_per_second"`
	WorkersPerNode   int                `json:"workers_per_node"`
	MaxDurationHours int                `json:"max_duration_hours"`
}

type backupRepositoryTestRequest struct {
	ExpectedPlanRevision uint64 `json:"expected_plan_revision"`
}

type backupConfigureResponse struct {
	Plan                  backupcontract.Plan       `json:"plan"`
	InitialJob            *backupcontract.BackupJob `json:"initial_job,omitempty"`
	CredentialsConfigured bool                      `json:"credentials_configured"`
}

type backupRepositoryErrorDetail struct {
	Provider     backupcontract.StoreKind              `json:"provider"`
	Stage        backupcontract.RepositoryAccessStage  `json:"stage"`
	Reason       backupcontract.RepositoryAccessReason `json:"reason"`
	ProviderCode string                                `json:"provider_code,omitempty"`
	RequestID    string                                `json:"request_id,omitempty"`
	NodeID       uint64                                `json:"node_id,omitempty"`
}

type backupHoldRequest struct {
	Held bool   `json:"held"`
	Note string `json:"note"`
}

type backupDeleteRequest struct {
	Confirmation string `json:"confirmation"`
}

const (
	maxBackupPlanRequestBytes   = 64 << 10
	maxBackupActionRequestBytes = 4 << 10
)

var errBackupConfirmation = errors.New("backup archive confirmation mismatch")

func (s *Server) registerBackupRoutes() {
	reads := s.engine.Group("/manager/backups")
	if s.auth.enabled() {
		reads.Use(s.requirePermission("cluster.backup", "r"))
	}
	reads.GET("", s.handleBackupDashboard)
	reads.GET("/archives/:archive_id", s.handleBackupArchive)

	writes := s.engine.Group("/manager/backups")
	if s.auth.enabled() {
		writes.Use(s.requirePermission("cluster.backup", "w"))
	}
	writes.Use(s.requireAuthenticatedBackupWrites())
	writes.PUT("/plan", s.handleBackupPlan)
	writes.POST("/repository/test", s.handleBackupRepositoryTest)
	writes.POST("/jobs", s.handleBackupStart)
	writes.POST("/jobs/:job_id/cancel", s.handleBackupCancel)
	writes.POST("/archives/:archive_id/verify", s.handleBackupVerify)
	writes.PUT("/archives/:archive_id/hold", s.handleBackupHold)
	writes.DELETE("/archives/:archive_id", s.handleBackupDelete)
}

func (s *Server) requireAuthenticatedBackupWrites() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !s.auth.enabled() {
			jsonError(
				c,
				http.StatusForbidden,
				"manager_auth_required",
				"manager authentication must be enabled before changing backups",
			)
			c.Abort()
			return
		}
		c.Next()
	}
}

func (s *Server) handleBackupDashboard(c *gin.Context) {
	if s == nil || s.backup == nil {
		jsonError(
			c, http.StatusServiceUnavailable,
			"backup_service_unavailable", "backup management is unavailable",
		)
		return
	}
	dashboard, err := s.backup.Dashboard(c.Request.Context())
	if err != nil {
		writeBackupError(c, err)
		return
	}
	if dashboard.State.Plan != nil {
		dashboard.State.Plan.Store.CredentialCiphertext = nil
	}
	c.JSON(http.StatusOK, dashboard)
}

func (s *Server) handleBackupPlan(c *gin.Context) {
	request, ok := bindBackupPlanRequest(c)
	if !ok {
		return
	}
	result, err := s.backup.Configure(
		c.Request.Context(), managementConfigureRequest(request),
	)
	s.auditBackupMutation(c, "configure_plan", "", err)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	credentialsConfigured :=
		len(result.Plan.Store.CredentialCiphertext) > 0
	result.Plan.Store.CredentialCiphertext = nil
	c.JSON(http.StatusOK, backupConfigureResponse{
		Plan:                  result.Plan,
		InitialJob:            result.InitialJob,
		CredentialsConfigured: credentialsConfigured,
	})
}

func (s *Server) handleBackupRepositoryTest(c *gin.Context) {
	request, ok := bindBackupRepositoryTestRequest(c)
	if !ok {
		return
	}
	plan, err := s.backup.TestRepository(
		c.Request.Context(),
		backupusecase.TestRepositoryRequest{
			ExpectedPlanRevision: request.ExpectedPlanRevision,
		},
	)
	s.auditBackupMutation(c, "test_repository", "", err)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	plan.Store.CredentialCiphertext = nil
	c.JSON(http.StatusOK, gin.H{"ok": true, "plan": plan})
}

func (s *Server) handleBackupStart(c *gin.Context) {
	job, err := s.backup.StartBackup(c.Request.Context())
	target := ""
	if err == nil {
		target = job.ID
	}
	s.auditBackupMutation(c, "start_backup", target, err)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, job)
}

func (s *Server) handleBackupCancel(c *gin.Context) {
	jobID := strings.TrimSpace(c.Param("job_id"))
	err := s.backup.CancelBackup(c.Request.Context(), jobID)
	s.auditBackupMutation(c, "cancel_backup", jobID, err)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleBackupArchive(c *gin.Context) {
	archive, err := s.backup.Archive(
		c.Request.Context(), strings.TrimSpace(c.Param("archive_id")),
	)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.JSON(http.StatusOK, archive)
}

func (s *Server) handleBackupVerify(c *gin.Context) {
	archive, err := s.backup.VerifyArchive(
		c.Request.Context(), strings.TrimSpace(c.Param("archive_id")),
	)
	s.auditBackupMutation(
		c, "verify_archive", strings.TrimSpace(c.Param("archive_id")), err,
	)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.JSON(http.StatusOK, archive)
}

func (s *Server) handleBackupHold(c *gin.Context) {
	limitBackupJSONBody(c, maxBackupActionRequestBytes)
	var request backupHoldRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		jsonError(c, http.StatusBadRequest, "backup_bad_request", "invalid hold request")
		return
	}
	archive, err := s.backup.HoldArchive(
		c.Request.Context(),
		strings.TrimSpace(c.Param("archive_id")),
		request.Held,
		strings.TrimSpace(request.Note),
	)
	s.auditBackupMutation(
		c, "set_archive_hold", strings.TrimSpace(c.Param("archive_id")), err,
	)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.JSON(http.StatusOK, archive)
}

func (s *Server) handleBackupDelete(c *gin.Context) {
	archiveID := strings.TrimSpace(c.Param("archive_id"))
	limitBackupJSONBody(c, maxBackupActionRequestBytes)
	var request backupDeleteRequest
	if err := c.ShouldBindJSON(&request); err != nil ||
		request.Confirmation != "DELETE "+archiveID {
		s.auditBackupMutation(c, "delete_archive", archiveID, errBackupConfirmation)
		jsonError(
			c, http.StatusBadRequest, "backup_confirmation_mismatch",
			"confirmation must exactly match DELETE <archive id>",
		)
		return
	}
	err := s.backup.DeleteArchive(c.Request.Context(), archiveID)
	s.auditBackupMutation(c, "delete_archive", archiveID, err)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func bindBackupPlanRequest(c *gin.Context) (backupPlanRequest, bool) {
	limitBackupJSONBody(c, maxBackupPlanRequestBytes)
	var request backupPlanRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		jsonError(c, http.StatusBadRequest, "backup_bad_request", "invalid backup plan")
		return backupPlanRequest{}, false
	}
	if request.RateMiBPerSecond < 1 || request.RateMiBPerSecond > 10_240 ||
		request.MaxDurationHours < 1 || request.MaxDurationHours > 48 ||
		len(request.Cron) > 256 || len(request.TimeZone) > 128 ||
		len(request.Store.Endpoint) > 2048 ||
		len(request.Store.Region) > 128 ||
		len(request.Store.Bucket) > 255 ||
		len(request.Store.Prefix) > 1024 ||
		len(request.Store.AccessKey) > 1024 ||
		len(request.Store.SecretKey) > 8192 ||
		!validBackupStoreRequest(request.Store) {
		jsonError(c, http.StatusBadRequest, "backup_bad_request", "invalid backup plan")
		return backupPlanRequest{}, false
	}
	return request, true
}

func bindBackupRepositoryTestRequest(
	c *gin.Context,
) (backupRepositoryTestRequest, bool) {
	limitBackupJSONBody(c, maxBackupActionRequestBytes)
	var request backupRepositoryTestRequest
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		jsonError(
			c,
			http.StatusBadRequest,
			"backup_bad_request",
			"invalid repository test request",
		)
		return backupRepositoryTestRequest{}, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) ||
		request.ExpectedPlanRevision == 0 {
		jsonError(
			c,
			http.StatusBadRequest,
			"backup_bad_request",
			"invalid repository test request",
		)
		return backupRepositoryTestRequest{}, false
	}
	return request, true
}

func validBackupStoreRequest(store backupStoreRequest) bool {
	endpoint := strings.TrimSpace(store.Endpoint)
	region := strings.TrimSpace(store.Region)
	bucket := strings.TrimSpace(store.Bucket)
	prefix := strings.Trim(strings.TrimSpace(store.Prefix), "/")
	accessKey := strings.TrimSpace(store.AccessKey)
	credentialsPaired :=
		(accessKey == "" && store.SecretKey == "") ||
			(accessKey != "" && store.SecretKey != "")
	if !credentialsPaired {
		return false
	}
	switch store.Kind {
	case backupcontract.StoreKindFile:
		return endpoint == "" && region == "" && bucket == "" && prefix == "" &&
			!store.PathStyle && accessKey == "" && store.SecretKey == ""
	case backupcontract.StoreKindS3:
		return endpoint != "" && bucket != "" && prefix != ""
	case backupcontract.StoreKindOSS:
		return backupusecase.ValidCloudRegion(region) &&
			bucket != "" && prefix != "" && !store.PathStyle
	case backupcontract.StoreKindCOS:
		return backupusecase.ValidCloudRegion(region) &&
			backupusecase.COSBucketHasAPPID(bucket) &&
			prefix != "" && !store.PathStyle
	default:
		return false
	}
}

func limitBackupJSONBody(c *gin.Context, limit int64) {
	if c == nil || c.Request == nil || c.Request.Body == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
}

func managementConfigureRequest(
	request backupPlanRequest,
) backupusecase.ConfigureManagementRequest {
	return backupusecase.ConfigureManagementRequest{
		ConfigureRequest: backupusecase.ConfigureRequest{
			ExpectedRevision: request.ExpectedRevision,
			Enabled:          request.Enabled,
			Store: backupcontract.StoreConfig{
				Kind:      request.Store.Kind,
				Endpoint:  strings.TrimSpace(request.Store.Endpoint),
				Region:    strings.TrimSpace(request.Store.Region),
				Bucket:    strings.TrimSpace(request.Store.Bucket),
				Prefix:    strings.TrimSpace(request.Store.Prefix),
				PathStyle: request.Store.PathStyle,
			},
			Cron:            strings.TrimSpace(request.Cron),
			TimeZone:        strings.TrimSpace(request.TimeZone),
			RetentionCount:  request.RetentionCount,
			RateBytesPerSec: request.RateMiBPerSecond << 20,
			WorkersPerNode:  request.WorkersPerNode,
			MaxDuration: time.Duration(request.MaxDurationHours) *
				time.Hour,
		},
		AccessKey: strings.TrimSpace(request.Store.AccessKey),
		SecretKey: request.Store.SecretKey,
	}
}

func writeBackupError(c *gin.Context, err error) {
	if presentation, ok := backupRepositoryErrorForResponse(err); ok {
		c.AbortWithStatusJSON(presentation.status, errorResponse{
			Error:   presentation.code,
			Message: presentation.message,
			Detail:  presentation.detail,
		})
		return
	}
	switch {
	case errors.Is(err, backupusecase.ErrInvalidRequest):
		jsonError(c, http.StatusBadRequest, "backup_bad_request", "invalid backup request")
	case errors.Is(err, backupusecase.ErrRepositoryUnverified):
		jsonError(
			c,
			http.StatusConflict,
			"backup_repository_unverified",
			"Save and test the repository before enabling or starting backups.",
		)
	case errors.Is(err, backupusecase.ErrDisabled):
		jsonError(c, http.StatusConflict, "backup_not_configured", "backup is not configured")
	case errors.Is(err, backupusecase.ErrBackupJobActive):
		jsonError(c, http.StatusConflict, "backup_job_active", "a full backup is already running")
	case errors.Is(err, backupusecase.ErrRestoreJobActive):
		jsonError(c, http.StatusConflict, "backup_restore_active", "a restore is already running")
	case errors.Is(err, backupusecase.ErrStateConflict):
		jsonError(c, http.StatusConflict, "backup_plan_conflict", "backup state changed; refresh and retry")
	case errors.Is(err, backupusecase.ErrArchiveOperationActive):
		jsonError(c, http.StatusConflict, "backup_archive_operation_active", "another backup archive operation is running")
	case errors.Is(err, backupusecase.ErrArchiveHeld):
		jsonError(c, http.StatusConflict, "backup_archive_held", "held backup archives cannot be deleted")
	case errors.Is(err, backupusecase.ErrArchiveInUse):
		jsonError(c, http.StatusConflict, "backup_archive_in_use", "the archive is used by the active restore")
	case errors.Is(err, backupusecase.ErrLastUsableArchive):
		jsonError(c, http.StatusConflict, "backup_last_archive", "the last healthy backup archive cannot be deleted")
	case errors.Is(err, backupusecase.ErrArchiveNotFound):
		jsonError(c, http.StatusNotFound, "backup_archive_not_found", "backup archive not found")
	case errors.Is(err, backupusecase.ErrArchiveCorrupt):
		jsonError(c, http.StatusUnprocessableEntity, "backup_archive_corrupt", "backup archive verification failed")
	case errors.Is(err, backupusecase.ErrStoreUnreachable):
		jsonError(c, http.StatusServiceUnavailable, "backup_store_unreachable", "backup repository is unreachable or lacks required access")
	default:
		jsonError(c, http.StatusServiceUnavailable, "backup_service_unavailable", "backup operation failed")
	}
}

type backupRepositoryErrorPresentation struct {
	status  int
	code    string
	message string
	detail  backupRepositoryErrorDetail
}

func backupRepositoryErrorForResponse(
	err error,
) (backupRepositoryErrorPresentation, bool) {
	var accessErr *backupcontract.RepositoryAccessError
	if !errors.As(err, &accessErr) {
		return backupRepositoryErrorPresentation{}, false
	}
	presentation := backupRepositoryErrorPresentation{
		status: http.StatusServiceUnavailable,
		detail: backupRepositoryErrorDetail{
			Provider: accessErr.Provider,
			Stage:    accessErr.Stage,
			Reason:   accessErr.Reason,
			ProviderCode: safeBackupRepositoryDiagnostic(
				accessErr.ProviderCode,
			),
			RequestID: safeBackupRepositoryDiagnostic(accessErr.RequestID),
			NodeID:    accessErr.NodeID,
		},
	}
	provider := backupRepositoryProviderName(accessErr.Provider)
	switch accessErr.Reason {
	case backupcontract.RepositoryAccessInvalidAccessKey:
		presentation.code = "backup_repository_auth_failed"
		presentation.message = provider + " rejected the AccessKey ID."
	case backupcontract.RepositoryAccessSignatureMismatch:
		presentation.code = "backup_repository_auth_failed"
		presentation.message =
			provider + " rejected the request signature. Check the AccessKey secret."
	case backupcontract.RepositoryAccessDenied:
		presentation.code = "backup_repository_permission_denied"
		presentation.message =
			provider + " denied a required repository operation."
	case backupcontract.RepositoryAccessBucketNotFound:
		presentation.code = "backup_repository_bucket_not_found"
		presentation.message = provider + " bucket was not found."
	case backupcontract.RepositoryAccessRegionMismatch:
		presentation.code = "backup_repository_region_mismatch"
		presentation.message =
			provider + " region or endpoint does not match the bucket."
	case backupcontract.RepositoryAccessEndpointUnreachable:
		presentation.code = "backup_repository_endpoint_unreachable"
		presentation.message = "Cannot reach the " + provider + " endpoint."
	case backupcontract.RepositoryAccessTLSFailure:
		presentation.code = "backup_repository_tls_failed"
		presentation.message =
			"TLS validation failed while connecting to " + provider + "."
	case backupcontract.RepositoryAccessTimeout:
		presentation.code = "backup_repository_timeout"
		presentation.message = "Timed out while accessing " + provider + "."
	case backupcontract.RepositoryAccessReadFailed,
		backupcontract.RepositoryAccessWriteFailed,
		backupcontract.RepositoryAccessListFailed,
		backupcontract.RepositoryAccessDeleteFailed:
		presentation.code = "backup_repository_operation_failed"
		presentation.message = fmt.Sprintf(
			"%s repository test failed during %s.",
			provider,
			backupRepositoryStageName(accessErr.Stage),
		)
	case backupcontract.RepositoryAccessRepositoryInUse:
		presentation.status = http.StatusConflict
		presentation.code = "backup_repository_identity_conflict"
		presentation.message =
			"The repository is already bound to another WuKongIM cluster."
	case backupcontract.RepositoryAccessNodeUnreachable:
		presentation.code = "backup_repository_node_unreachable"
		if accessErr.NodeID != 0 {
			presentation.message = fmt.Sprintf(
				"Cluster node %d could not access %s.",
				accessErr.NodeID,
				provider,
			)
		} else {
			presentation.message =
				"A cluster node could not access " + provider + "."
		}
	default:
		presentation.code = "backup_repository_unknown"
		presentation.message = provider + " repository test failed."
	}
	return presentation, true
}

func backupRepositoryProviderName(
	provider backupcontract.StoreKind,
) string {
	switch provider {
	case backupcontract.StoreKindOSS:
		return "Alibaba Cloud OSS"
	case backupcontract.StoreKindCOS:
		return "Tencent Cloud COS"
	case backupcontract.StoreKindS3:
		return "S3-compatible storage"
	case backupcontract.StoreKindFile:
		return "the file repository"
	default:
		return "the backup repository"
	}
}

func backupRepositoryStageName(
	stage backupcontract.RepositoryAccessStage,
) string {
	switch stage {
	case backupcontract.RepositoryAccessOpen:
		return "opening the repository"
	case backupcontract.RepositoryAccessWriteMarker:
		return "writing the test marker"
	case backupcontract.RepositoryAccessReadMarker:
		return "reading the test marker"
	case backupcontract.RepositoryAccessWriteReceipt:
		return "writing a node receipt"
	case backupcontract.RepositoryAccessReadReceipt:
		return "reading a node receipt"
	case backupcontract.RepositoryAccessList:
		return "listing test objects"
	case backupcontract.RepositoryAccessDelete:
		return "deleting test objects"
	case backupcontract.RepositoryAccessBindIdentity:
		return "validating repository identity"
	case backupcontract.RepositoryAccessMarkVerified:
		return "saving verification state"
	default:
		return "an unknown operation"
	}
}

func safeBackupRepositoryDiagnostic(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 256 {
		value = value[:256]
	}
	return strings.Map(func(char rune) rune {
		if char < 0x20 || char == 0x7f {
			return -1
		}
		return char
	}, value)
}
