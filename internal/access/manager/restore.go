package manager

import (
	"context"
	"errors"
	"net/http"
	"strings"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	"github.com/gin-gonic/gin"
)

// RestoreManagement admits current-cluster maintenance restores.
type RestoreManagement interface {
	StartRestore(context.Context, string, string) (backupcontract.RestoreJob, error)
	CancelRestore(context.Context, string) error
}

var (
	errRestoreRequest          = errors.New("invalid restore request")
	errRestoreReauthentication = errors.New("restore reauthentication failed")
	errRestoreConfirmation     = errors.New("restore confirmation mismatch")
)

type restoreStartRequest struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	Confirmation string `json:"confirmation"`
}

func (s *Server) registerRestoreRoutes() {
	restore := s.engine.Group("/manager/backups")
	restore.Use(s.requireAuthenticatedBackupWrites())
	restore.Use(s.requireExplicitPermission("cluster.restore", "w"))
	restore.POST(
		"/archives/:archive_id/restore",
		s.handleRestoreStart,
	)
	restore.POST(
		"/restores/:job_id/cancel",
		s.handleRestoreCancel,
	)
}

func (s *Server) handleRestoreStart(c *gin.Context) {
	archiveID := strings.TrimSpace(c.Param("archive_id"))
	if s == nil || s.restore == nil {
		s.auditBackupMutation(c, "start_restore", archiveID, errRestoreRequest)
		jsonError(
			c, http.StatusServiceUnavailable,
			"service_unavailable", "restore management is unavailable",
		)
		return
	}
	limitBackupJSONBody(c, maxBackupActionRequestBytes)
	var request restoreStartRequest
	if err := c.ShouldBindJSON(&request); err != nil ||
		len(request.Username) > 128 ||
		len(request.Password) > 8192 ||
		len(request.Confirmation) > 512 {
		s.auditBackupMutation(c, "start_restore", archiveID, errRestoreRequest)
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid restore request")
		return
	}
	authenticated, _ := c.Get(managerUsernameContextKey)
	username, _ := authenticated.(string)
	if strings.TrimSpace(request.Username) != username ||
		!s.auth.verifyCredentials(username, request.Password) {
		s.auditBackupMutation(
			c, "start_restore", archiveID, errRestoreReauthentication,
		)
		jsonError(
			c, http.StatusUnauthorized,
			"reauthentication_failed", "administrator reauthentication failed",
		)
		return
	}
	if request.Confirmation != "RESTORE "+archiveID {
		s.auditBackupMutation(c, "start_restore", archiveID, errRestoreConfirmation)
		jsonError(
			c, http.StatusBadRequest,
			"confirmation_mismatch", "restore confirmation does not match the archive",
		)
		return
	}
	job, err := s.restore.StartRestore(c.Request.Context(), archiveID, username)
	target := archiveID
	if err == nil {
		target = archiveID + ":" + job.ID
	}
	s.auditBackupMutation(c, "start_restore", target, err)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, job)
}

func (s *Server) handleRestoreCancel(c *gin.Context) {
	if s == nil || s.restore == nil {
		jsonError(
			c, http.StatusServiceUnavailable,
			"service_unavailable", "restore management is unavailable",
		)
		return
	}
	jobID := strings.TrimSpace(c.Param("job_id"))
	err := s.restore.CancelRestore(c.Request.Context(), jobID)
	s.auditBackupMutation(c, "cancel_restore", jobID, err)
	if err != nil {
		writeBackupError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
