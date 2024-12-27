package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
)

type Handler struct {
	wklog.Log
}

func NewHandler() *Handler {
	h := &Handler{
		Log: wklog.NewWKLog("handler"),
	}
	h.routes()
	return h
}

func (h *Handler) routes() {

	// 在线推送
	eventbus.RegisterPusherHandlers(eventbus.EventPushOnline, h.pushOnline)
	// 离线推送
	eventbus.RegisterPusherHandlers(eventbus.EventPushOffline, h.pushOffline)
}

// 收到消息
func (h *Handler) OnMessage(m *proto.Message) {

}

// 收到事件
func (h *Handler) OnEvent(ctx *eventbus.PushContext) {
	// 执行本地事件
	eventbus.ExecutePusherEvent(ctx)
}
