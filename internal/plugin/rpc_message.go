package plugin

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/WuKongIM/wkrpc"
	"go.uber.org/zap"
)

func (a *rpc) messageSend(c *wkrpc.Context) {
	req := &pluginproto.SendReq{}
	err := req.Unmarshal(c.Body())
	if err != nil {
		a.Error("SendReq unmarshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	if strings.TrimSpace(req.FromUid) == "" {
		req.FromUid = options.G.SystemUID
	}

	if len(req.Payload) <= 0 {
		a.Error("SendReq payload is empty")
		c.WriteErr(errors.New("payload is empty"))
		return
	}
	channelId := req.ChannelId
	channelType := uint8(req.ChannelType)
	if strings.TrimSpace(channelId) == "" {
		a.Error("SendReq channelId is empty")
		c.WriteErr(errors.New("channelId is empty"))
		return
	}
	if options.IsSpecialChar(channelId) {
		a.Error("SendReq channelId is special char")
		c.WriteErr(errors.New("channelId is special char"))
		return
	}

	if req.Header == nil {
		req.Header = &pluginproto.Header{
			RedDot:    false,
			SyncOnce:  false,
			NoPersist: false,
		}
	}

	clientMsgNo := req.ClientMsgNo
	if strings.TrimSpace(clientMsgNo) == "" {
		clientMsgNo = fmt.Sprintf("%s0", wkutil.GenUUID())
	}
	fakeChannelId := channelId
	if channelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.FromUid, channelId)
	}

	sendPacket := &wkproto.SendPacket{
		Framer: wkproto.Framer{
			RedDot:    req.Header.RedDot,
			SyncOnce:  req.Header.SyncOnce,
			NoPersist: req.Header.NoPersist,
		},
		ClientMsgNo: clientMsgNo,
		ChannelID:   channelId,
		ChannelType: uint8(channelType),
		Payload:     req.Payload,
	}
	messageId := options.G.GenMessageId()

	eventType := eventbus.EventChannelOnSend

	event := &eventbus.Event{
		Conn: &eventbus.Conn{
			Uid:      req.FromUid,
			DeviceId: options.G.SystemDeviceId,
		},
		Type:      eventType,
		Frame:     sendPacket,
		MessageId: messageId,
		Track: track.Message{
			PreStart: time.Now(),
		},
	}
	eventbus.Channel.SendMessage(fakeChannelId, channelType, event)
	event.Track.Record(track.PositionStart)
	eventbus.Channel.Advance(fakeChannelId, channelType)

	resp := &pluginproto.SendResp{
		MessageId: messageId,
	}
	data, err := resp.Marshal()
	if err != nil {
		a.Error("SendResp marshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(data)
}

func (a *rpc) channelMessages(c *wkrpc.Context) {
	channelMessageBatchReq := &pluginproto.ChannelMessageBatchReq{}
	err := channelMessageBatchReq.Unmarshal(c.Body())
	if err != nil {
		a.Error("ChannelMessages unmarshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}

	resps := make([]*pluginproto.ChannelMessageResp, 0, len(channelMessageBatchReq.ChannelMessageReqs))
	for _, req := range channelMessageBatchReq.ChannelMessageReqs {
		msgs, err := service.Store.LoadNextRangeMsgs(req.ChannelId, uint8(req.ChannelType), uint64(req.StartMessageSeq), 0, int(req.Limit))
		if err != nil {
			a.Error("LoadNextRangeMsgs failed", zap.Error(err))
			c.WriteErr(err)
			return
		}

		messageResps := make([]*pluginproto.Message, 0, len(msgs))
		for _, msg := range msgs {
			messageResps = append(messageResps, &pluginproto.Message{
				MessageId:   msg.MessageID,
				ClientMsgNo: msg.ClientMsgNo,
				ChannelId:   msg.ChannelID,
				ChannelType: uint32(msg.ChannelType),
				MessageSeq:  uint64(msg.MessageSeq),
				From:        msg.FromUID,
				Payload:     msg.Payload,
				Topic:       msg.Topic,
				StreamNo:    msg.StreamNo,
				StreamId:    msg.StreamId,
				Timestamp:   uint32(msg.Timestamp),
			})
		}

		resp := &pluginproto.ChannelMessageResp{
			ChannelId:       req.ChannelId,
			ChannelType:     req.ChannelType,
			StartMessageSeq: req.StartMessageSeq,
			Messages:        messageResps,
		}
		resps = append(resps, resp)
	}
	channelMsgResp := &pluginproto.ChannelMessageBatchResp{
		ChannelMessageResps: resps,
	}

	data, err := channelMsgResp.Marshal()
	if err != nil {
		a.Error("ChannelMessageBatchResp marshal failed", zap.Error(err))
		c.WriteErr(err)
		return
	}
	c.Write(data)
}
