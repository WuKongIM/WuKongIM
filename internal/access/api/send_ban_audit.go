package api

import (
	"context"
	"net"

	"github.com/WuKongIM/WuKongIM/internal/contracts/sendbanaudit"
	"github.com/gin-gonic/gin"
)

// sendBanManagementContext attributes backend writes without inventing a human
// principal or treating forwarded headers as a verified peer identity.
func sendBanManagementContext(c *gin.Context) context.Context {
	peer, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
	return sendbanaudit.WithActor(c.Request.Context(), sendbanaudit.Actor{Source: "server_api", Name: "unknown", Peer: peer})
}
