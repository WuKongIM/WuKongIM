package process

import (
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (c *Channel) processSendack(fakeChanelId string, channelType uint8, messages []*reactor.ChannelMessage) {

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

		// 将成功的消息进入到扩散流程
		if m.ReasonCode == wkproto.ReasonSuccess {
			m.MsgType = reactor.ChannelMsgDiffuse // 消息扩散
			reactor.Diffuse.AddMessage(m)
		}
	}
	// 推进扩散流程
	reactor.Diffuse.Advance(fakeChanelId, channelType)

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
