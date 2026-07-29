package manager

import (
	"strings"

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
		// Keep audit records useful but bounded. Repository and SDK errors may
		// contain endpoints, object keys, or credential-adjacent request data.
		fields = append(fields, wklog.String("error_code", "backup_operation_failed"))
	}
	s.logger.Info("Manager backup mutation", fields...)
}
