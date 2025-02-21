package plugin

import (
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	"github.com/WuKongIM/wkrpc"
	"go.uber.org/zap"
)

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
