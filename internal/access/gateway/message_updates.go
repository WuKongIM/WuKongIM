package gateway

import (
	"encoding/json"
	coregateway "github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

// MessageUpdateCapabilities records connection-scoped EVENT support without
// changing CONNECT or any CMD packet semantics.
type MessageUpdateCapabilities interface {
	EnableMessageUpdates(string, uint64, bool) error
}

func (h *Handler) handleMessageUpdateCapability(ctx *coregateway.Context, pkt *frame.EventPacket) error {
	if ctx == nil || ctx.Session == nil || h.messageUpdateCapabilities == nil {
		return ErrUnsupportedFrame
	}
	uid, _ := ctx.Session.Value(coregateway.SessionValueUID).(string)
	if uid == "" {
		return ErrUnauthenticatedSession
	}
	if len(pkt.Data) > 128 {
		return ErrUnsupportedFrame
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(pkt.Data, &req); err != nil || req.Enabled == nil {
		return ErrUnsupportedFrame
	}
	if err := h.messageUpdateCapabilities.EnableMessageUpdates(uid, ctx.Session.ID(), *req.Enabled); err != nil {
		return err
	}
	body, _ := json.Marshal(req)
	return ctx.WriteFrame(&frame.EventPacket{Type: "message_updates.ready", Data: body})
}
