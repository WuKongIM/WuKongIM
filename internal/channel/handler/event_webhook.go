package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"go.uber.org/zap"
)

func (h *Handler) webhook(ctx *eventbus.ChannelContext) {
	var err error
	if options.G.WebhookOn(types.EventMsgNotify) {
		err = service.Store.AppendMessageOfNotifyQueue(h.toPersistMessages(ctx.ChannelId, ctx.ChannelType, ctx.Events))
		if err != nil {
			h.Error("store notify queue message failed", zap.Error(err), zap.Int("msgs", len(ctx.Events)), zap.String("channelId", ctx.ChannelId), zap.Uint8("channelType", ctx.ChannelType))
		}
	}
}
