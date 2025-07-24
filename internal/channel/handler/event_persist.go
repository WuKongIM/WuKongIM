package handler

import (
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// 消息持久化
func (h *Handler) persist(ctx *eventbus.ChannelContext) {
	// 记录消息轨迹
	events := ctx.Events
	for _, e := range events {
		e.Track.Record(track.PositionChannelPersist)
	}

	// ========== 存储消息 ==========
	persists := h.toPersistMessages(ctx.ChannelId, ctx.ChannelType, events)
	if len(persists) > 0 {

		timeoutCtx, cancel := h.WithTimeout()
		defer cancel()
		reasonCode := wkproto.ReasonSuccess

		results, err := service.Store.AppendMessages(timeoutCtx, ctx.ChannelId, ctx.ChannelType, persists)
		if err != nil {
			h.Error("store message failed", zap.Error(err), zap.Int("events", len(persists)), zap.String("fakeChannelId", ctx.ChannelId), zap.Uint8("channelType", ctx.ChannelType))
			reasonCode = wkproto.ReasonSystemError
		}

		// 填充messageSeq
		if reasonCode == wkproto.ReasonSuccess {
			for _, e := range events {
				for _, result := range results {
					if result.Id == uint64(e.MessageId) {
						e.MessageSeq = result.Index
						break
					}
				}
			}

			for i, m := range persists {
				for _, result := range results {
					if result.Id == uint64(m.MessageID) {
						persists[i].MessageSeq = uint32(result.Index)
						break
					}
				}
			}

			// 通知插件
			h.pluginInvokePersistAfter(persists)
		}

		// 修改原因码
		for _, event := range events {
			for _, msg := range persists {
				if event.MessageId == msg.MessageID {
					event.ReasonCode = reasonCode
					break
				}
			}
			if options.G.Logger.TraceOn {

				msgTip := "消息保存成功..."
				if reasonCode != wkproto.ReasonSuccess {
					msgTip = "消息保存失败..."
				}
				h.Trace(msgTip,
					"persist",
					zap.Int64("messageId", event.MessageId),
					zap.Uint64("messageSeq", event.MessageSeq),
					zap.String("from", event.Conn.Uid),
					zap.String("channelId", ctx.ChannelId),
					zap.Uint8("channelType", ctx.ChannelType),
					zap.String("resson", reasonCode.String()),
				)
			}
		}
	}

	// ========== webhook ==========
	if options.G.WebhookOn(types.EventMsgNotify) {
		for _, e := range events {
			sendPacket := e.Frame.(*wkproto.SendPacket)
			if e.ReasonCode == wkproto.ReasonSuccess && !sendPacket.NoPersist {
				cloneEvent := e.Clone()
				cloneEvent.Type = eventbus.EventChannelWebhook
				eventbus.Channel.AddEvent(ctx.ChannelId, ctx.ChannelType, cloneEvent)
			}
		}
	}

	// ========== 分发 ==========
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

func (h *Handler) pluginInvokePersistAfter(msgs []wkdb.Message) {
	plugins := service.PluginManager.Plugins(types.PluginPersistAfter)
	if len(plugins) == 0 {
		return
	}

	timeoutCtx, cancel := h.WithTimeout()
	defer cancel()

	pluginMessages := make([]*pluginproto.Message, 0, len(msgs))
	for _, msg := range msgs {
		pluginMessages = append(pluginMessages, &pluginproto.Message{
			MessageId:   msg.MessageID,
			MessageSeq:  uint64(msg.MessageSeq),
			ClientMsgNo: msg.ClientMsgNo,
			StreamNo:    msg.StreamNo,
			StreamId:    msg.StreamId,
			Timestamp:   uint32(msg.Timestamp),
			From:        msg.FromUID,
			ChannelId:   msg.ChannelID,
			Topic:       msg.Topic,
			ChannelType: uint32(msg.ChannelType),
			Payload:     msg.Payload,
		})
	}

	msgBatch := &pluginproto.MessageBatch{
		Messages: pluginMessages,
	}
	for _, pg := range plugins {
		err := pg.PersistAfter(timeoutCtx, msgBatch)
		if err != nil {
			h.Error("plugin persist after error", zap.Error(err))
		}
	}
}

// 转换成存储消息
func (h *Handler) toPersistMessages(channelId string, channelType uint8, events []*eventbus.Event) []wkdb.Message {
	persists := make([]wkdb.Message, 0, len(events))
	for _, e := range events {
		sendPacket := e.Frame.(*wkproto.SendPacket)
		if sendPacket.NoPersist || e.ReasonCode != wkproto.ReasonSuccess || strings.TrimSpace(e.StreamNo) != "" {
			continue
		}

		msg := wkdb.Message{
			RecvPacket: wkproto.RecvPacket{
				Framer: wkproto.Framer{
					RedDot:    sendPacket.Framer.RedDot,
					SyncOnce:  sendPacket.Framer.SyncOnce,
					NoPersist: sendPacket.Framer.NoPersist,
				},
				Setting:     sendPacket.Setting,
				MessageID:   e.MessageId,
				MessageSeq:  uint32(e.MessageSeq),
				ClientMsgNo: sendPacket.ClientMsgNo,
				ClientSeq:   sendPacket.ClientSeq,
				FromUID:     e.Conn.Uid,
				ChannelID:   channelId,
				ChannelType: channelType,
				Expire:      sendPacket.Expire,
				Timestamp:   int32(time.Now().Unix()),
				Topic:       sendPacket.Topic,
				StreamNo:    sendPacket.StreamNo,
				Payload:     sendPacket.Payload,
			},
		}
		persists = append(persists, msg)
	}
	return persists
}
