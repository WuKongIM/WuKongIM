package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/service"
)

func (h *Handler) pushOffline(ctx *eventbus.PushContext) {
	service.Webhook.NotifyOfflineMsg(ctx.Events)
}
