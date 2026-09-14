package api

import (
	"errors"
	"net/http"

	"github.com/WuKongIM/WuKongIM/internal/runtime/readavailability"
	conversation "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	message "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/gin-gonic/gin"
)

// writeReadUnavailable keeps the legacy error envelope while giving callers a
// stable retry decision. It never publishes partial messages or a next cursor.
func writeReadUnavailable(c *gin.Context, err error) bool {
	unavailable := readavailability.Unavailable(err) ||
		errors.Is(err, conversation.ErrRouteNotReady) || errors.Is(err, conversation.ErrListBusy) ||
		errors.Is(err, message.ErrRouteNotReady) || errors.Is(err, message.ErrNotLeader) ||
		errors.Is(err, message.ErrNotChannelAuthority) || errors.Is(err, message.ErrStaleRoute) ||
		errors.Is(err, message.ErrBackpressured) || errors.Is(err, message.ErrChannelBusy) ||
		errors.Is(err, message.ErrUpdateUnavailable)
	if !unavailable {
		return false
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"status": http.StatusServiceUnavailable, "code": "unavailable", "msg": "temporarily unavailable"})
	return true
}
