package manager

import (
	"errors"
	"strings"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gin-gonic/gin"
)

func (s *Server) auditBackupMutation(
	c *gin.Context,
	action string,
	target string,
	err error,
) {
	if s == nil || s.logger == nil {
		return
	}
	actor := ""
	if value, ok := c.Get(managerUsernameContextKey); ok {
		actor, _ = value.(string)
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	fields := []wklog.Field{
		wklog.Event("internal.access.manager.backup_mutation"),
		wklog.String("actor", strings.TrimSpace(actor)),
		wklog.String("action", action),
		wklog.String("target", strings.TrimSpace(target)),
		wklog.String("result", result),
	}
	if err != nil {
		errorCode := "backup_operation_failed"
		if presentation, ok := backupRepositoryErrorForResponse(err); ok {
			errorCode = presentation.code
		}
		fields = append(fields, wklog.String("error_code", errorCode))
		var accessErr *backupcontract.RepositoryAccessError
		if errors.As(err, &accessErr) {
			fields = append(
				fields,
				wklog.String("provider", string(accessErr.Provider)),
				wklog.String("stage", string(accessErr.Stage)),
			)
			if accessErr.NodeID != 0 {
				fields = append(
					fields,
					wklog.Uint64("node_id", accessErr.NodeID),
				)
			}
		}
	}
	s.logger.Info("Manager backup mutation", fields...)
}
