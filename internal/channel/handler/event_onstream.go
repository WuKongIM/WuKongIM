package handler

import (
	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// 收到流消息
func (h *Handler) onStream(ctx *eventbus.ChannelContext) {

	for _, event := range ctx.Events {
		event.ReasonCode = wkproto.ReasonSuccess
	}
	// 持久化流消息
	h.persistStreams(ctx)
	// 发送消息回执
	h.sendack(ctx)
}

// 持久化流消息
func (h *Handler) persistStreams(ctx *eventbus.ChannelContext) {

	events := ctx.Events
	// 存储消息
	streams := h.toPersistStreams(events)

	reasonCode := wkproto.ReasonSuccess
	if len(streams) > 0 {
		err := service.Store.AddStreams(ctx.ChannelId, ctx.ChannelType, streams)
		if err != nil {
			h.Error("store stream failed", zap.Error(err), zap.Int("events", len(streams)), zap.String("fakeChannelId", ctx.ChannelId), zap.Uint8("channelType", ctx.ChannelType))
			reasonCode = wkproto.ReasonSystemError
		}
	}
	// 修改原因码
	for _, event := range events {
		for _, stream := range streams {
			if event.StreamNo == stream.StreamNo {
				event.ReasonCode = reasonCode
				break
			}
		}
	}

	// 分发
	for _, e := range events {
		if e.ReasonCode != wkproto.ReasonSuccess {
			continue
		}
		cloneEvent := e.Clone()
		cloneEvent.Type = eventbus.EventChannelDistribute
		eventbus.Channel.AddEvent(ctx.ChannelId, ctx.ChannelType, cloneEvent)
	}
	eventbus.Channel.Advance(ctx.ChannelId, ctx.ChannelType)

}

// 转换成存储消息
func (h *Handler) toPersistStreams(events []*eventbus.Event) []*wkdb.Stream {

	var streams []*wkdb.Stream
	for _, e := range events {
		sendPacket := e.Frame.(*wkproto.SendPacket)
		if sendPacket.NoPersist || e.ReasonCode != wkproto.ReasonSuccess {
			continue
		}
		stream := &wkdb.Stream{
			StreamNo: e.StreamNo,
			StreamId: uint64(e.MessageId),
			Payload:  sendPacket.Payload,
		}
		streams = append(streams, stream)
	}
	return streams
}
