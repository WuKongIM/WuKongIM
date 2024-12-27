package handler

import (
	"context"

	"github.com/WuKongIM/WuKongIM/internal/common"
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/ingress"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"go.uber.org/zap"
)

type Handler struct {
	wklog.Log
	client        *ingress.Client
	commonService *common.Service
}

func NewHandler() *Handler {
	h := &Handler{
		Log:           wklog.NewWKLog("handler"),
		client:        ingress.NewClient(),
		commonService: common.NewService(),
	}
	h.routes()
	return h
}

func (h *Handler) routes() {

	// 发送消息
	eventbus.RegisterChannelHandlers(eventbus.EventChannelOnSend, h.onSend)
	// webhook
	eventbus.RegisterChannelHandlers(eventbus.EventChannelWebhook, h.webhook)
	// 分发消息
	eventbus.RegisterChannelHandlers(eventbus.EventChannelDistribute, h.distribute)

}

// 收到消息
func (h *Handler) OnMessage(m *proto.Message) {
	switch msgType(m.MsgType) {
	case msgForwardChannelEvent:
		h.onForwardChannelEvent(m)
	}
}

// 收到事件
func (h *Handler) OnEvent(ctx *eventbus.ChannelContext) {
	if options.G.IsLocalNode(ctx.LeaderId) || h.notForwardToLeader(ctx.EventType) {
		// 执行本地事件
		eventbus.ExecuteChannelEvent(ctx)
	} else {
		// 转发到leader节点
		h.forwardsToNode(ctx.LeaderId, ctx.ChannelId, ctx.ChannelType, ctx.Events)
	}
}

// 不需要转发给领导的事件
func (h *Handler) notForwardToLeader(eventType eventbus.EventType) bool {
	switch eventType {
	case eventbus.EventChannelWebhook,
		eventbus.EventChannelDistribute:
		return true
	}
	return false

}

func (h *Handler) forwardsToNode(nodeId uint64, channelId string, channelType uint8, events []*eventbus.Event) {

	req := &forwardChannelEventReq{
		channelId:   channelId,
		channelType: channelType,
		fromNode:    options.G.Cluster.NodeId,
		events:      events,
	}
	data, err := req.encode()
	if err != nil {
		h.Error("forwardToLeader: encode failed", zap.Error(err))
		return
	}
	msg := &proto.Message{
		MsgType: uint32(msgForwardChannelEvent),
		Content: data,
	}
	err = h.sendToNode(nodeId, msg)
	if err != nil {
		h.Error("forwardToLeader: send failed", zap.Error(err))
		return
	}
}

// func (h *Handler) forwardToNode(nodeId uint64, channelId string, channelType uint8, event *eventbus.Event) {
// 	h.forwardsToNode(nodeId, channelId, channelType, []*eventbus.Event{event})
// }

func (h *Handler) sendToNode(toNodeId uint64, msg *proto.Message) error {
	err := service.Cluster.Send(toNodeId, msg)
	return err
}

// 收到转发用户事件
func (h *Handler) onForwardChannelEvent(m *proto.Message) {
	req := &forwardChannelEventReq{}
	err := req.decode(m.Content)
	if err != nil {
		h.Error("onForwardChannelEvent: decode failed", zap.Error(err))
		return
	}

	for _, e := range req.events {
		eventbus.Channel.AddEvent(req.channelId, req.channelType, e)
	}
	eventbus.Channel.Advance(req.channelId, req.channelType)

}

func (h *Handler) WithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), options.G.Channel.ProcessTimeout)

}
