package process

import (
	"context"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/reactor"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkserver/proto"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

func (p *Push) processPush(messages []*reactor.ChannelMessage) {
	channelMessages := p.groupByChannel(messages)
	for channelKey, messages := range channelMessages {
		p.processChannelPush(channelKey, messages)
	}
}

func (p *Push) processChannelPush(channelKey string, messages []*reactor.ChannelMessage) {
	fakeChannelId, channelType := wkutil.ChannelFromlKey(channelKey)
	tag, err := p.getOrRequestTag(fakeChannelId, channelType)
	if err != nil {
		p.Error("get or request tag failed", zap.Error(err), zap.String("channelKey", channelKey))
		return
	}
	if tag == nil {
		p.Error("push: processTagKeyPush: tag not found, not push", zap.String("channelKey", channelKey))
		return
	}
	// 获取属于本节点的用户
	toUids := tag.GetNodeUsers(options.G.Cluster.NodeId)
	onlineConns := make([]*reactor.Conn, 0, 100)
	offlineUids := make([]string, 0, len(toUids))
	for _, toUid := range toUids {
		conns := reactor.User.ConnsByUid(toUid)
		hasMasterDevice := false // 是否有主设备在线（如果有主设备在线才不推送离线）
		for _, conn := range conns {
			if !conn.Auth {
				continue
			}
			if conn.DeviceLevel == wkproto.DeviceLevelMaster {
				hasMasterDevice = true
			}
		}
		if !hasMasterDevice {
			offlineUids = append(offlineUids, toUid)
		}
		if len(conns) > 0 {
			for _, conn := range conns {
				if !conn.Auth {
					continue
				}
				onlineConns = append(onlineConns, conn)
			}
		}

	}

	for _, message := range messages {

		sendPacket := message.SendPacket
		fromUid := message.Conn.Uid
		// 如果发送者是系统账号，则不显示发送者
		if options.G.IsSystemUid(fromUid) {
			fromUid = ""
		}

		recvPacket := &wkproto.RecvPacket{}

		recvPacket.Framer = wkproto.Framer{
			RedDot:    sendPacket.GetRedDot(),
			SyncOnce:  sendPacket.GetsyncOnce(),
			NoPersist: sendPacket.GetNoPersist(),
		}
		recvPacket.Setting = sendPacket.Setting
		recvPacket.MessageID = message.MessageId
		recvPacket.MessageSeq = uint32(message.MessageSeq)
		recvPacket.ClientMsgNo = sendPacket.ClientMsgNo
		recvPacket.StreamNo = sendPacket.StreamNo
		recvPacket.StreamFlag = wkproto.StreamFlagIng
		recvPacket.FromUID = fromUid
		recvPacket.Expire = sendPacket.Expire
		recvPacket.ChannelID = sendPacket.ChannelID
		recvPacket.ChannelType = sendPacket.ChannelType
		recvPacket.Topic = sendPacket.Topic
		recvPacket.Timestamp = int32(time.Now().Unix())
		recvPacket.ClientSeq = sendPacket.ClientSeq

		for _, conn := range onlineConns {
			if conn.Uid == message.Conn.Uid && conn.DeviceId == message.Conn.DeviceId { // 自己发的不处理
				continue
			}

			// 这里需要把channelID改成fromUID 比如A给B发消息，B收到的消息channelID应该是A A收到的消息channelID应该是B
			recvPacket.ChannelID = sendPacket.ChannelID
			if recvPacket.ChannelType == wkproto.ChannelTypePerson &&
				recvPacket.ChannelID == conn.Uid {
				recvPacket.ChannelID = recvPacket.FromUID
			}
			// 红点设置
			recvPacket.RedDot = sendPacket.RedDot
			if conn.Uid == recvPacket.FromUID { // 如果是自己则不显示红点
				recvPacket.RedDot = false
			}
			if len(conn.AesIV) == 0 || len(conn.AesKey) == 0 {
				p.Error("aesIV or aesKey is empty",
					zap.String("uid", conn.Uid),
					zap.String("deviceId", conn.DeviceId),
					zap.String("channelId", recvPacket.ChannelID),
					zap.Uint8("channelType", recvPacket.ChannelType),
				)
				continue
			}
			encryptPayload, err := encryptMessagePayload(sendPacket.Payload, conn)
			if err != nil {
				p.Error("加密payload失败！",
					zap.Error(err),
					zap.String("uid", conn.Uid),
					zap.String("channelId", recvPacket.ChannelID),
					zap.Uint8("channelType", recvPacket.ChannelType),
				)
				continue
			}
			recvPacket.Payload = encryptPayload
			signStr := recvPacket.VerityString()
			msgKey, err := makeMsgKey(signStr, conn)
			if err != nil {
				p.Error("生成MsgKey失败！", zap.Error(err))
				continue
			}
			recvPacket.MsgKey = msgKey
			recvPacketData, err := reactor.Proto.EncodeFrame(recvPacket, conn.ProtoVersion)
			if err != nil {
				p.Error("encode recvPacket failed", zap.String("uid", conn.Uid), zap.String("channelId", recvPacket.ChannelID), zap.Uint8("channelType", recvPacket.ChannelType), zap.Error(err))
				continue
			}

			if !recvPacket.NoPersist { // 只有存储的消息才重试
				// d.dm.s.retryManager.addRetry(&retryMessage{
				// 	uid:            conn.uid,
				// 	connId:         conn.connId,
				// 	messageId:      message.MessageId,
				// 	recvPacketData: recvPacketData,
				// })
			}

			reactor.User.ConnWriteBytesNoAdvance(conn, recvPacketData)
		}

	}

	if len(offlineUids) > 0 {
		offlineUidsPtr := &offlineUids // 使用指针避免数组多次复制，节省内存
		for _, message := range messages {
			message.MsgType = reactor.ChannelMsgOffline
			message.OfflineUsers = offlineUidsPtr
		}
		reactor.Push.PushOfflineMessages(messages)
	}

}

// 获取或请求tag
func (p *Push) getOrRequestTag(fakeChannelId string, channelType uint8) (*types.Tag, error) {
	if channelType == wkproto.ChannelTypePerson {
		return p.getPersonTag(fakeChannelId)
	}

	tagKey := service.TagMananger.GetChannelTag(fakeChannelId, channelType)
	var tag *types.Tag
	if tagKey != "" {
		tag = service.TagMananger.Get(tagKey)
	}
	if tag != nil {
		return tag, nil
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	leader, err := service.Cluster.LeaderOfChannel(timeoutCtx, fakeChannelId, channelType)
	cancel()
	if err != nil {
		p.Error("getOrRequestTag: getLeaderOfChannel failed", zap.Error(err), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return nil, err
	}

	// tagKey在频道的领导节点是一定存在的，
	// 如果不存在可能就是失效了，这里直接忽略,只能等下条消息触发重构tag
	if leader.Id == options.G.Cluster.NodeId {
		p.Warn("tag not exist in leader node", zap.String("tagKey", tagKey), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return nil, nil
	}

	tagResp, err := p.requesTag(leader.Id, &tagReq{
		tagKey: tagKey,
		nodeId: options.G.Cluster.NodeId,
	})
	if err != nil {
		p.Error("getOrRequestTag: get tag failed", zap.Error(err), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelType))
		return nil, err
	}

	tag, err = service.TagMananger.MakeTagWithTagKey(tagKey, tagResp.uids)
	if err != nil {
		p.Error("getOrRequestTag: make tag failed", zap.Error(err))
		return nil, err
	}
	return tag, nil
}

// 请求节点对应tag的用户集合
func (p *Push) requesTag(nodeId uint64, req *tagReq) (*tagResp, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	data, err := req.encode()
	if err != nil {
		return nil, err
	}
	resp, err := service.Cluster.RequestWithContext(timeoutCtx, nodeId, "/wk/getTag", data)
	if err != nil {
		return nil, err
	}
	if resp.Status != proto.StatusOK {
		return nil, fmt.Errorf("requestNodeChannelTag failed, status: %d err:%s", resp.Status, string(resp.Body))
	}
	var tagResp = &tagResp{}
	err = tagResp.decode(resp.Body)
	if err != nil {
		return nil, err
	}
	return tagResp, nil
}

// 获取个人频道的投递tag
func (p *Push) getPersonTag(fakeChannelId string) (*types.Tag, error) {
	orgFakeChannelId := fakeChannelId
	if options.G.IsCmdChannel(fakeChannelId) {
		// 处理命令频道
		orgFakeChannelId = options.G.CmdChannelConvertOrginalChannel(fakeChannelId)
	}
	// 处理普通假个人频道
	u1, u2 := options.GetFromUIDAndToUIDWith(orgFakeChannelId)

	nodeUs := make([]*types.Node, 0, 2)

	u1NodeId, err := service.Cluster.SlotLeaderIdOfChannel(u1, wkproto.ChannelTypePerson)
	if err != nil {
		return nil, err
	}
	u2NodeId, err := service.Cluster.SlotLeaderIdOfChannel(u2, wkproto.ChannelTypePerson)
	if err != nil {
		return nil, err
	}

	if options.G.IsLocalNode(u1NodeId) && u1NodeId == u2NodeId {
		nodeUs = append(nodeUs, &types.Node{
			LeaderId: u1NodeId,
			Uids:     []string{u1, u2},
		})
	} else if u1NodeId == options.G.Cluster.NodeId {
		nodeUs = append(nodeUs, &types.Node{
			LeaderId: u1NodeId,
			Uids:     []string{u1},
		})
	} else if u2NodeId == options.G.Cluster.NodeId {
		nodeUs = append(nodeUs, &types.Node{
			LeaderId: u2NodeId,
			Uids:     []string{u2},
		})
	}

	tg := &types.Tag{
		Nodes: nodeUs,
	}
	return tg, nil
}

// 消息按照频道分组
func (p *Push) groupByChannel(messages []*reactor.ChannelMessage) map[string][]*reactor.ChannelMessage {
	channelMessages := make(map[string][]*reactor.ChannelMessage)
	for _, m := range messages {
		channelKey := wkutil.ChannelToKey(m.FakeChannelId, m.ChannelType)
		if _, ok := channelMessages[channelKey]; !ok {
			channelMessages[channelKey] = make([]*reactor.ChannelMessage, 0)
		}
		channelMessages[channelKey] = append(channelMessages[channelKey], m)
	}
	return channelMessages
}

// 加密消息
func encryptMessagePayload(payload []byte, conn *reactor.Conn) ([]byte, error) {
	aesKey, aesIV := conn.AesKey, conn.AesIV
	// 加密payload
	payloadEnc, err := wkutil.AesEncryptPkcs7Base64(payload, aesKey, aesIV)
	if err != nil {
		return nil, err
	}
	return payloadEnc, nil
}

func makeMsgKey(signStr string, conn *reactor.Conn) (string, error) {
	aesKey, aesIV := conn.AesKey, conn.AesIV
	// 生成MsgKey
	msgKeyBytes, err := wkutil.AesEncryptPkcs7Base64([]byte(signStr), []byte(aesKey), []byte(aesIV))
	if err != nil {
		wklog.Error("生成MsgKey失败！", zap.Error(err))
		return "", err
	}
	return wkutil.MD5(string(msgKeyBytes)), nil
}
