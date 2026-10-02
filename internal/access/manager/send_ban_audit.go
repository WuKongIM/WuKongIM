package manager

import (
	"context"
	"net"

	"github.com/WuKongIM/WuKongIM/internal/contracts/sendbanaudit"
	"github.com/gin-gonic/gin"
)

// sendBanManagementContext forwards only the principal established by Manager
// authentication; disabled authentication leaves the operator explicitly unknown.
func sendBanManagementContext(c *gin.Context) context.Context {
	peer, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
	return sendbanaudit.WithActor(c.Request.Context(), sendbanaudit.Actor{Source: "manager", Name: c.GetString(managerUsernameContextKey), Peer: peer})
}
