package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (c *Channel) processSendack(messages []*reactor.ChannelMessage) {
	for _, m := range messages {
		m.Track.Record(track.PositionChannelSendack)
		if options.G.Logger.TraceOn {
			c.Trace(m.Track.String(),
				"sendack",
				zap.Int64("messageId", m.MessageId),
				zap.Uint64("messageSeq", m.MessageSeq),
				zap.Uint64("clientSeq", m.SendPacket.ClientSeq),
				zap.String("clientMsgNo", m.SendPacket.ClientMsgNo),
				zap.String("channelId", m.FakeChannelId),
				zap.Uint8("channelType", m.ChannelType),
				zap.String("reasonCode", m.ReasonCode.String()),
				zap.String("conn.uid", m.Conn.Uid),
				zap.String("conn.deviceId", m.Conn.DeviceId),
				zap.Uint64("conn.fromNode", m.Conn.FromNode),
				zap.Int64("conn.connId", m.Conn.ConnId),
			)
		}
	}

	sendackMap := make(map[uint64][]*sendackReq)
	for _, m := range messages {
		sendackMap[m.Conn.FromNode] = append(sendackMap[m.Conn.FromNode], &sendackReq{
			framer:       wkproto.ToFixHeaderUint8(m.SendPacket.Framer),
			messageId:    m.MessageId,
			messageSeq:   m.MessageSeq,
			clientMsgNo:  m.SendPacket.ClientMsgNo,
			clientSeq:    m.SendPacket.ClientSeq,
			reasonCode:   uint8(m.ReasonCode),
			fromUid:      m.Conn.Uid,
			FromNode:     m.Conn.FromNode,
			ConnId:       m.Conn.ConnId,
			protoVersion: m.Conn.ProtoVersion,
		})

	}

	for fromNode, reqs := range sendackMap {
		if options.G.IsLocalNode(fromNode) {
			c.sendSendack(reqs)
			continue
		}
		if fromNode == 0 {
			continue
		}
		batchReq := sendackBatchReq(reqs)
		data, err := batchReq.encode()
		if err != nil {
			c.Error("sendack encode error", zap.Error(err), zap.Uint64("fromNode", fromNode))
			continue
		}
		err = service.Cluster.Send(fromNode, &proto.Message{
			MsgType: msgSendack.uint32(),
			Content: data,
		})
		if err != nil {
			c.Error("send sendack failed", zap.Error(err), zap.Uint64("fromNode", fromNode))
		}
	}

}

func (c *Channel) sendSendack(reqs []*sendackReq) {

	var uidMap = make(map[string]struct{})
	for _, req := range reqs {
		if !options.G.IsLocalNode(req.FromNode) {
			c.Warn("sendack from node not equal self", zap.Uint64("fromNode", req.FromNode), zap.Uint64("self", options.G.Cluster.NodeId))
			continue
		}
		packet := c.toSendack(req)

		trace.GlobalTrace.Metrics.App().SendackPacketCountAdd(1)
		trace.GlobalTrace.Metrics.App().SendackPacketBytesAdd(packet.GetFrameSize())

		reactor.User.ConnWriteNoAdvance(&reactor.Conn{
			Uid:          req.fromUid,
			ConnId:       req.ConnId,
			FromNode:     req.FromNode,
			ProtoVersion: req.protoVersion,
		}, packet)
		uidMap[req.fromUid] = struct{}{}
	}

	for uid := range uidMap {
		reactor.User.Advance(uid)
	}
}

func (c *Channel) toSendack(req *sendackReq) *wkproto.SendackPacket {

	return &wkproto.SendackPacket{
		Framer:      wkproto.FramerFromUint8(req.framer),
		MessageID:   req.messageId,
		MessageSeq:  uint32(req.messageSeq),
		ClientMsgNo: req.clientMsgNo,
		ClientSeq:   req.clientSeq,
		ReasonCode:  wkproto.ReasonCode(req.reasonCode),
	}
}
