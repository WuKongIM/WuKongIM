package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/ingress"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/track"
	"github.com/WuKongIM/WuKongIM/internal/types"
	cluster "github.com/WuKongIM/WuKongIM/pkg/cluster/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/wkcache"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb/key"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/sendgrid/rest"
	"go.uber.org/zap"
)

type message struct {
	s *Server
	wklog.Log

	syncRecordMap  map[string][]*syncRecord // 记录最后一次同步命令的记录（TODO：这个是临时方案，为了兼容老版本）
	syncRecordLock sync.RWMutex
}

func newMessage(s *Server) *message {
	return &message{
		s:             s,
		Log:           wklog.NewWKLog("message"),
		syncRecordMap: map[string][]*syncRecord{},
	}
}

// Route route
func (m *message) route(r *wkhttp.WKHttp) {
	r.POST("/message/send", m.send)           // 发送消息
	r.POST("/message/sendbatch", m.sendBatch) // 批量发送消息

	// 此接口后续会废弃（以后不提供带存储的命令消息，业务端通过不存储的命令 + 调用业务端接口一样可以实现相同效果）
	r.POST("/message/sync", m.sync)       // 消息同步(写模式) （将废弃）
	r.POST("/message/syncack", m.syncack) // 消息同步回执(写模式) （将废弃）

	r.POST("/messages", m.searchMessages)                      // 批量查询消息
	r.POST("/message", m.searchMessage)                        // 搜索单条消息
	r.GET("/message/byclientmsgno", m.getMessageByClientMsgNo) // 通过 client_msg_no 查询消息
	r.POST("/message/event", m.eventAppend)                    // 追加消息事件
	r.POST("/message/eventsync", m.eventSync)                  // 同步消息事件

	// 频道消息相关端点（从 channel.go 移动过来）
	r.POST("/channel/messagesync", m.syncMessages)               // 同步频道消息
	r.GET("/channel/max_message_seq", m.getChannelMaxMessageSeq) // 获取某个频道最大的消息序号
	r.GET("/channel/last_message", m.getChannelLastMessage)      // 获取频道最后一条消息

}

func (m *message) send(c *wkhttp.Context) {
	var req messageSendReq
	if err := c.BindJSON(&req); err != nil {
		m.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}

	if strings.TrimSpace(req.FromUID) == "" {
		req.FromUID = options.G.SystemUID
	}

	channelId := req.ChannelID
	channelType := req.ChannelType

	m.Debug("发送消息内容：", zap.String("msg", wkutil.ToJSON(req)))
	if strings.TrimSpace(channelId) == "" && len(req.Subscribers) == 0 { //指定了频道 才能正常发送
		m.Error("无法处理发送消息请求！", zap.Any("req", req))
		c.ResponseError(errors.New("无法处理发送消息请求！"))
		return
	}

	if len(req.Subscribers) > 0 && req.Header.SyncOnce != 1 {
		m.Error("subscribers有值的情况下，消息必须是syncOnce消息", zap.Any("req", req))
		c.ResponseError(errors.New("无法处理发送消息请求！"))
		return
	}

	if strings.TrimSpace(channelId) != "" && len(req.Subscribers) > 0 {
		m.Error("channelId和subscribers不能同时存在！", zap.Any("req", req))
		c.ResponseError(errors.New("无法处理发送消息请求！"))
		return
	}

	if len(req.Subscribers) > 0 {

		// 生成临时频道id
		tmpChannelId := options.G.Channel.OnlineCmdChannelId // 如果不是要存储的消息，则放系统目录的在线cmd频道就行
		tmpChannelType := wkproto.ChannelTypeTemp
		persist := req.Header.NoPersist == 0
		if persist {
			// 生成临时频道id
			tmpChannelId = fmt.Sprintf("%d", key.HashWithString(strings.Join(req.Subscribers, ","))) // 获取临时频道id
		}

		// 设置订阅者到临时频道
		if persist {
			tmpChannelId = options.G.OrginalConvertCmdChannel(tmpChannelId) // 转换为cmd频道
			err := m.requestSetSubscribersForTmpChannel(tmpChannelId, req.Subscribers)
			if err != nil {
				m.Error("请求设置临时频道的订阅者失败！", zap.Error(err), zap.String("channelId", tmpChannelId), zap.Strings("subscribers", req.Subscribers))
				c.ResponseError(errors.New("请求设置临时频道的订阅者失败！"))
				return
			}
		} else {
			// 生成tag
			nodeInfo, err := service.Cluster.SlotLeaderOfChannel(tmpChannelId, wkproto.ChannelTypeTemp)
			if err != nil {
				m.Error("获取在线cmd频道所在节点失败！", zap.Error(err), zap.String("channelID", tmpChannelId), zap.Uint8("channelType", wkproto.ChannelTypeTemp))
				c.ResponseError(errors.New("获取频道所在节点失败！"))
				return
			}
			newTagKey := fmt.Sprintf("%scmd", wkutil.GenUUID())
			req.TagKey = newTagKey // 设置tagKey
			if options.G.IsLocalNode(nodeInfo.Id) {
				_, err := service.TagManager.MakeTagWithTagKey(newTagKey, req.Subscribers)
				if err != nil {
					m.Error("生成tag失败！", zap.Error(err), zap.Uint64("nodeId", nodeInfo.Id))
					c.ResponseError(errors.New("生成tag失败！"))
					return
				}
			} else {
				err = m.s.client.AddTag(nodeInfo.Id, &ingress.TagAddReq{
					TagKey: newTagKey,
					Uids:   req.Subscribers,
				})
				if err != nil {
					m.Error("添加tag失败！", zap.Error(err), zap.Uint64("nodeId", nodeInfo.Id))
					c.ResponseError(errors.New("更新tag失败！"))
					return
				}
			}

		}

		clientMsgNo := fmt.Sprintf("%s0", wkutil.GenUUID())
		// 发送消息
		_, err := sendMessageToChannel(req, tmpChannelId, tmpChannelType, clientMsgNo)
		if err != nil {
			c.ResponseError(err)
			return
		}
		c.ResponseOK()

		return
	}

	clientMsgNo := req.ClientMsgNo
	if strings.TrimSpace(clientMsgNo) == "" {
		clientMsgNo = fmt.Sprintf("%s0", wkutil.GenUUID())
	}

	// 发送消息
	messageId, err := sendMessageToChannel(req, channelId, channelType, clientMsgNo)
	if err != nil {
		c.ResponseError(err)
		return
	}

	// 流式消息 → 注册元信息到缓存，供后续 eventappend 自动填充
	if req.IsStream == 1 && service.MessageEventCache != nil {
		fakeChannelId := channelId
		if channelType == wkproto.ChannelTypePerson {
			fakeChannelId = options.GetFakeChannelIDWith(req.FromUID, channelId)
		} else if channelType == wkproto.ChannelTypeAgent {
			fakeChannelId = options.GetAgentChannelIDWith(req.FromUID, channelId)
		}
		_ = service.MessageEventCache.RegisterStreamMessage(wkcache.MessageEventSessionMeta{
			ClientMsgNo:       clientMsgNo,
			ChannelId:         fakeChannelId,
			ChannelType:       channelType,
			FromUid:           req.FromUID,
			OriginalChannelId: channelId,
			MessageId:         messageId,
		})
	}

	c.ResponseOKWithData(map[string]interface{}{
		"message_id":    messageId,
		"client_msg_no": clientMsgNo,
	})
}

// 请求临时频道设置订阅者
func (m *message) requestSetSubscribersForTmpChannel(tmpChannelId string, uids []string) error {
	nodeInfo, err := service.Cluster.SlotLeaderOfChannel(tmpChannelId, wkproto.ChannelTypeTemp)
	if err != nil {
		return err
	}
	if options.G.IsLocalNode(nodeInfo.Id) {
		_ = setTmpSubscriberWithReq(tmpSubscriberSetReq{
			ChannelId: tmpChannelId,
			Uids:      uids,
		})
		return nil
	}
	reqURL := fmt.Sprintf("%s/%s", nodeInfo.ApiServerAddr, "tmpchannel/subscriber_set")
	request := rest.Request{
		Method:  rest.Method("POST"),
		BaseURL: reqURL,
		Body: []byte(wkutil.ToJSON(map[string]interface{}{
			"channel_id": tmpChannelId,
			"uids":       uids,
		})),
	}
	resp, err := rest.Send(request)
	if err != nil {
		return err
	}
	if err := handlerIMError(resp); err != nil {
		return err
	}
	return nil
}

func sendMessageToChannel(req messageSendReq, channelId string, channelType uint8, clientMsgNo string) (int64, error) {

	// m.s.monitor.SendPacketInc(req.Header.NoPersist != 1)
	// m.s.monitor.SendSystemMsgInc()

	// var messageID = m.s.dispatch.processor.genMessageID()

	if options.IsSpecialChar(channelId) {
		return 0, errors.New("频道ID不合法！")
	}

	fakeChannelId := channelId
	if channelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.FromUID, channelId)
	} else if channelType == wkproto.ChannelTypeAgent {
		fakeChannelId = options.GetAgentChannelIDWith(req.FromUID, channelId)
	}

	if req.Header.SyncOnce == 1 && !options.G.IsOnlineCmdChannel(channelId) && channelType != wkproto.ChannelTypeTemp { // 命令消息，将原频道转换为cmd频道
		fakeChannelId = options.G.OrginalConvertCmdChannel(fakeChannelId)
	}

	var setting wkproto.Setting
	if req.IsStream == 1 {
		setting = setting.Set(wkproto.SettingStream)
	}

	sendPacket := &wkproto.SendPacket{
		Framer: wkproto.Framer{
			RedDot:    wkutil.IntToBool(req.Header.RedDot),
			SyncOnce:  wkutil.IntToBool(req.Header.SyncOnce),
			NoPersist: wkutil.IntToBool(req.Header.NoPersist),
		},
		Setting:     setting,
		Expire:      req.Expire,
		ClientMsgNo: clientMsgNo,
		ChannelID:   channelId,
		ChannelType: channelType,
		Payload:     req.Payload,
	}
	messageId := options.G.GenMessageId()

	event := &eventbus.Event{
		Conn: &eventbus.Conn{
			Uid:      req.FromUID,
			DeviceId: options.G.SystemDeviceId,
		},
		Type:      eventbus.EventChannelOnSend,
		Frame:     sendPacket,
		MessageId: messageId,
		TagKey:    req.TagKey,
		Track: track.Message{
			PreStart: time.Now(),
		},
	}
	eventbus.Channel.SendMessage(fakeChannelId, channelType, event)
	event.Track.Record(track.PositionStart)
	eventbus.Channel.Advance(fakeChannelId, channelType)

	return messageId, nil
}

func (m *message) sendBatch(c *wkhttp.Context) {
	var req struct {
		Header      types.MessageHeader `json:"header"`      // 消息头
		FromUID     string              `json:"from_uid"`    // 发送者UID
		Subscribers []string            `json:"subscribers"` // 订阅者 如果此字段有值，表示消息只发给指定的订阅者
		Payload     []byte              `json:"payload"`     // 消息内容
	}
	if err := c.BindJSON(&req); err != nil {
		m.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if strings.TrimSpace(req.FromUID) == "" {
		c.ResponseError(errors.New("from_uid不能为空！"))
		return
	}
	if len(req.Subscribers) == 0 {
		c.ResponseError(errors.New("subscribers不能为空！"))
		return
	}
	if len(req.Payload) == 0 {
		c.ResponseError(errors.New("payload不能为空！"))
		return
	}
	failUids := make([]string, 0)
	reasons := make([]string, 0)
	for _, subscriber := range req.Subscribers {
		clientMsgNo := fmt.Sprintf("%s0", wkutil.GenUUID())
		_, err := sendMessageToChannel(messageSendReq{
			Header:      req.Header,
			FromUID:     req.FromUID,
			ChannelID:   subscriber,
			ChannelType: wkproto.ChannelTypePerson,
			Payload:     req.Payload,
		}, subscriber, wkproto.ChannelTypePerson, clientMsgNo)
		if err != nil {
			failUids = append(failUids, subscriber)
			reasons = append(reasons, err.Error())
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"fail_uids": failUids,
		"reason":    reasons,
	})
}

// 消息同步
// Deprecated: 将废弃
func (m *message) sync(c *wkhttp.Context) {

	if options.G.DisableCMDMessageSync {
		c.JSON(http.StatusOK, []string{})
		return
	}

	var req syncReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		m.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}

	if req.Limit <= 0 {
		req.Limit = 200
	}

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
	if err != nil {
		m.Error("获取频道所在节点失败！!", zap.Error(err), zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId

	if !leaderIsSelf {
		m.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	// ==================== 获取用户活跃的最近会话 ====================
	conversations, err := service.Store.GetConversationsByType(req.UID, wkdb.ConversationTypeCMD)
	if err != nil {
		m.Error("获取conversation失败！", zap.Error(err), zap.String("uid", req.UID))
		c.ResponseError(errors.New("获取conversation失败！"))
		return
	}

	// 获取用户缓存的最近会话
	cacheChannels, err := service.ConversationManager.GetUserChannelsFromCache(req.UID, wkdb.ConversationTypeCMD)
	if err != nil {
		m.Error("获取用户缓存的最近会话失败！", zap.Error(err), zap.String("uid", req.UID))
		c.ResponseError(errors.New("获取用户缓存的最近会话失败！"))
		return
	}
	for _, cacheChannel := range cacheChannels {
		exist := false
		for _, conversation := range conversations {
			if cacheChannel.ChannelID == conversation.ChannelId && cacheChannel.ChannelType == conversation.ChannelType {
				exist = true
				break
			}
		}
		if !exist {
			conversations = append(conversations, wkdb.Conversation{
				Uid:         req.UID,
				ChannelId:   cacheChannel.ChannelID,
				ChannelType: cacheChannel.ChannelType,
				Type:        wkdb.ConversationTypeCMD,
			})
		}
	}

	// 获取真实的频道ID
	// getRealChannelId := func(fakeChannelId string, channelType uint8) string {
	// 	realChannelId := fakeChannelId
	// 	if channelType == wkproto.ChannelTypePerson {
	// 		from, to := GetFromUIDAndToUIDWith(fakeChannelId)
	// 		if req.UID == from {
	// 			realChannelId = to
	// 		} else {
	// 			realChannelId = from
	// 		}
	// 	}
	// 	return realChannelId
	// }

	var channelRecentMessageReqs []*channelRecentMessageReq
	for _, conversation := range conversations {

		channelRecentMessageReqs = append(channelRecentMessageReqs, &channelRecentMessageReq{
			ChannelId:   conversation.ChannelId,
			ChannelType: conversation.ChannelType,
			LastMsgSeq:  conversation.ReadToMsgSeq + 1, // 这里加1的目的是为了不查询到ReadedToMsgSeq本身这条消息
		})
	}

	// 先清空旧记录
	m.syncRecordLock.Lock()
	m.syncRecordMap[req.UID] = nil
	m.syncRecordLock.Unlock()

	// 获取每个session的消息
	messageResps := make([]*types.MessageResp, 0)
	if len(channelRecentMessageReqs) > 0 {
		channelRecentMessages, err := m.s.requset.getRecentMessagesForCluster(req.UID, req.Limit, channelRecentMessageReqs, false)
		if err != nil {
			m.Error("获取最近消息失败！", zap.Error(err), zap.String("uid", req.UID))
			c.ResponseError(errors.New("获取最近消息失败！"))
			return
		}
		for _, channelRecentMessage := range channelRecentMessages {

			if len(channelRecentMessage.Messages) == 0 {
				continue
			}
			isExceedLimit := false // 是否超过限制

			for _, message := range channelRecentMessage.Messages {
				if len(messageResps) >= req.Limit {
					isExceedLimit = true
					break
				}
				messageResps = append(messageResps, message)
			}
			var lastMsg *types.MessageResp
			if isExceedLimit {
				lastMsg = messageResps[len(messageResps)-1]
			} else {
				lastMsg = channelRecentMessage.Messages[len(channelRecentMessage.Messages)-1]
			}
			m.syncRecordLock.Lock()
			m.syncRecordMap[req.UID] = append(m.syncRecordMap[req.UID], &syncRecord{
				channelId:   channelRecentMessage.ChannelId,
				channelType: channelRecentMessage.ChannelType,
				lastMsgSeq:  lastMsg.MessageSeq,
			})
			m.syncRecordLock.Unlock()
		}
	}
	c.JSON(http.StatusOK, messageResps)

}

type syncRecord struct {
	channelId   string
	channelType uint8
	lastMsgSeq  uint64
}

func (m *message) syncack(c *wkhttp.Context) {

	if options.G.DisableCMDMessageSync {
		c.ResponseOK()
		return
	}

	var req syncackReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		m.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
	if err != nil {
		m.Error("获取频道所在节点失败！!", zap.Error(err), zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId

	if !leaderIsSelf {
		m.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	m.syncRecordLock.Lock()
	records := m.syncRecordMap[req.UID]
	m.syncRecordMap[req.UID] = nil
	m.syncRecordLock.Unlock()
	if len(records) == 0 {
		c.ResponseOK()
		return
	}

	conversations := make([]wkdb.Conversation, 0)
	for _, record := range records {
		needAdd := false

		fakeChannelId := record.channelId
		if !options.G.IsCmdChannel(fakeChannelId) {
			m.Warn("不是cmd频道！", zap.String("uid", req.UID), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", record.channelType))
			continue
		}
		if record.lastMsgSeq <= 0 {
			continue
		}

		conversation, err := service.Store.GetConversation(req.UID, fakeChannelId, record.channelType)
		if err != nil {
			if err == wkdb.ErrNotFound {
				m.Warn("会话不存在！", zap.String("uid", req.UID), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", record.channelType))
				continue
			} else {
				m.Error("获取conversation失败！", zap.Error(err), zap.String("uid", req.UID), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", record.channelType))
				c.ResponseError(errors.New("获取conversation失败！"))
				return
			}
		}
		if conversation.Type != wkdb.ConversationTypeCMD {
			continue
		}

		if record.lastMsgSeq > conversation.ReadToMsgSeq || needAdd {
			conversation.ReadToMsgSeq = record.lastMsgSeq
			conversations = append(conversations, conversation)
		}

	}
	if len(conversations) > 0 {
		err := service.Store.AddOrUpdateUserConversations(req.UID, conversations)
		if err != nil {
			m.Error("消息同步回执失败！", zap.Error(err), zap.String("uid", req.UID))
			c.ResponseError(errors.New("消息同步回执失败！"))
			return
		}
	}

	c.ResponseOK()
}

func (m *message) searchMessages(c *wkhttp.Context) {
	var req struct {
		LoginUid     string   `json:"login_uid"`
		ChannelID    string   `json:"channel_id"`
		ChannelType  uint8    `json:"channel_type"`
		MessageSeqs  []uint32 `json:"message_seqs"`
		MessageIds   []int64  `json:"message_ids"`
		ClientMsgNos []string `json:"client_msg_nos"`
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		m.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(errors.New("数据格式有误！"))
		return
	}
	if strings.TrimSpace(req.ChannelID) == "" {
		c.ResponseError(errors.New("channel_id不能为空！"))
		return
	}

	fakeChannelId := req.ChannelID
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.LoginUid, req.ChannelID)
	}

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(fakeChannelId, req.ChannelType) // 获取频道的领导节点
	if err != nil {
		m.Error("获取频道所在节点失败！!", zap.Error(err), zap.String("channelID", fakeChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId

	if !leaderIsSelf {
		m.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	var messages []wkdb.Message
	for _, seq := range req.MessageSeqs {
		msg, err := service.Store.LoadMsg(fakeChannelId, req.ChannelType, uint64(seq))
		if err != nil && err != wkdb.ErrNotFound {
			m.Error("查询消息失败！", zap.Error(err))
			c.ResponseError(err)
			return
		}
		if err == nil {
			messages = append(messages, msg)
		}
	}

	for _, msgID := range req.MessageIds {
		results, err := service.Store.SearchMessages(wkdb.MessageSearchReq{
			ChannelId:   fakeChannelId,
			ChannelType: req.ChannelType,
			MessageId:   msgID,
			Limit:       1000,
		})
		if err != nil && err != wkdb.ErrNotFound {
			m.Error("查询消息失败！", zap.Error(err), zap.Int64("msgID", msgID))
			c.ResponseError(err)
			return
		}
		if len(results) > 0 {
			messages = append(messages, results[0])
		}
	}

	for _, clientMsgNo := range req.ClientMsgNos {
		results, err := service.Store.SearchMessages(wkdb.MessageSearchReq{
			ChannelId:   fakeChannelId,
			ChannelType: req.ChannelType,
			ClientMsgNo: clientMsgNo,
			Limit:       1000,
		})
		if err != nil && err != wkdb.ErrNotFound {
			m.Error("查询消息失败！", zap.Error(err), zap.String("clientMsgNo", clientMsgNo))
			c.ResponseError(err)
			return
		}
		if len(results) > 0 {
			messages = append(messages, results[0])
		}
	}

	resps := make([]*types.MessageResp, 0, len(messages))
	if len(messages) > 0 {
		for _, message := range messages {
			resp := &types.MessageResp{}
			resp.From(message, options.G.SystemUID)
			resps = append(resps, resp)
		}
	}
	c.JSON(http.StatusOK, &syncMessageResp{
		Messages: resps,
	})
}

func (m *message) searchMessage(c *wkhttp.Context) {
	var req struct {
		LoginUid    string `json:"login_uid"`
		ChannelId   string `json:"channel_id"`
		ChannelType uint8  `json:"channel_type"`
		// message_id 和 client_msg_no 二选一
		MessageId   int64  `json:"message_id"`
		ClientMsgNo string `json:"client_msg_no"`
	}

	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		m.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(errors.New("数据格式有误！"))
		return
	}

	if strings.TrimSpace(req.ChannelId) == "" {
		c.ResponseError(errors.New("channel_id不能为空！"))
		return
	}

	if req.ChannelType == 0 {
		c.ResponseError(errors.New("channel_type不能为0"))
		return
	}

	if req.ChannelType == wkproto.ChannelTypePerson && strings.TrimSpace(req.LoginUid) == "" {
		c.ResponseError(errors.New("login_uid不能为空！"))
		return

	}

	fakeChannelId := req.ChannelId
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.LoginUid, req.ChannelId)
	}

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(fakeChannelId, req.ChannelType) // 获取频道的领导节点
	if err != nil {
		m.Error("获取频道所在节点失败！!", zap.Error(err), zap.String("channelID", fakeChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId

	if !leaderIsSelf {
		m.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	messages, err := service.Store.SearchMessages(wkdb.MessageSearchReq{
		ChannelId:   fakeChannelId,
		ChannelType: req.ChannelType,
		MessageId:   req.MessageId,
		ClientMsgNo: req.ClientMsgNo,
	})
	if err != nil && err != wkdb.ErrNotFound {
		m.Error("查询消息失败！", zap.Error(err), zap.String("req", wkutil.ToJSON(req)))
		c.ResponseError(err)
		return
	}

	if len(messages) == 0 {
		m.Info("消息不存在！", zap.String("req", wkutil.ToJSON(req)))
		c.ResponseStatus(http.StatusNotFound)
		return
	}

	resp := &types.MessageResp{}
	resp.From(messages[0], options.G.SystemUID)
	c.JSON(http.StatusOK, resp)
}

// getMessageByClientMsgNo 通过 client_msg_no 查询消息
func (m *message) getMessageByClientMsgNo(c *wkhttp.Context) {
	channelId := c.Query("channel_id")
	channelType := wkutil.StringToUint8(c.Query("channel_type"))
	clientMsgNo := c.Query("client_msg_no")

	if strings.TrimSpace(channelId) == "" {
		c.ResponseError(errors.New("channel_id不能为空"))
		return
	}

	if channelType == 0 {
		c.ResponseError(errors.New("channel_type不能为0"))
		return
	}

	if strings.TrimSpace(clientMsgNo) == "" {
		c.ResponseError(errors.New("client_msg_no不能为空"))
		return
	}

	fakeChannelId := channelId
	// 注意：如果是个人频道，需要传入完整的 fakeChannelId（如 uid1@uid2）

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(fakeChannelId, channelType)
	if err != nil {
		m.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", fakeChannelId), zap.Uint8("channelType", channelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId

	if !leaderIsSelf {
		m.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.Forward(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path))
		return
	}

	msg, err := service.Store.LoadMsgByClientMsgNo(fakeChannelId, channelType, clientMsgNo)
	if err != nil {
		if err == wkdb.ErrNotFound {
			m.Info("消息不存在！", zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.String("clientMsgNo", clientMsgNo))
			c.ResponseStatus(http.StatusNotFound)
			return
		}
		m.Error("查询消息失败！", zap.Error(err), zap.String("channelId", channelId), zap.Uint8("channelType", channelType), zap.String("clientMsgNo", clientMsgNo))
		c.ResponseError(err)
		return
	}

	resp := &types.MessageResp{}
	resp.From(msg, options.G.SystemUID)
	c.JSON(http.StatusOK, resp)
}

func (m *message) eventAppend(c *wkhttp.Context) {

	// -------------------- 1. 解析请求 --------------------
	var req eventAppendReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		m.Error("eventAppend: 数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	// ---------- 自动填充：从缓存补全 from_uid 和 message_id ----------
	needReserialization := false
	if service.MessageEventCache != nil {
		if meta, ok := service.MessageEventCache.GetSessionMetaByClientMsgNo(req.ClientMsgNo); ok {
			if strings.TrimSpace(req.FromUID) == "" && strings.TrimSpace(meta.FromUid) != "" {
				req.FromUID = meta.FromUid
				needReserialization = true
			}
			if req.MessageID == 0 && meta.MessageId != 0 {
				req.MessageID = meta.MessageId
				needReserialization = true
			}
		}
	}

	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	if strings.TrimSpace(req.FromUID) == "" {
		req.FromUID = options.G.SystemUID
	}

	fakeChannelID := req.ChannelID
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelID = options.GetFakeChannelIDWith(req.ChannelID, req.FromUID)
	} else if req.ChannelType == wkproto.ChannelTypeAgent {
		fakeChannelID = options.GetAgentChannelIDWith(req.FromUID, req.ChannelID)
	}

	eventType := strings.ToLower(strings.TrimSpace(req.EventType))
	eventKey := strings.TrimSpace(req.EventKey)
	if eventKey == "" {
		eventKey = wkdb.EventKeyDefault
	}
	if eventType == wkdb.EventTypeStreamFinish {
		eventKey = wkdb.EventKeyFinish
	}

	// -------------------- 2. 转发到 leader --------------------
	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(fakeChannelID, req.ChannelType)
	if err != nil {
		m.Error("eventAppend: 获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	if leaderInfo.Id != options.G.Cluster.NodeId {
		if needReserialization {
			bodyBytes, _ = json.Marshal(req)
		}
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	// -------------------- 3. 按事件类型分发 --------------------
	switch eventType {
	case wkdb.EventTypeStreamDelta:
		m.handleStreamDelta(c, &req, fakeChannelID, eventKey, eventType)
	default:
		m.handleStreamPersist(c, &req, fakeChannelID, eventKey, eventType)
	}
}

// handleStreamDelta 处理 stream.delta 事件，全部走内存缓存，不落盘。
func (m *message) handleStreamDelta(c *wkhttp.Context, req *eventAppendReq, fakeChannelID, eventKey, eventType string) {
	if service.MessageEventCache == nil {
		c.ResponseError(errors.New("event cache not available"))
		return
	}

	cacheKeyState, err := service.MessageEventCache.AppendDelta(
		req.ClientMsgNo, fakeChannelID, req.ChannelType,
		eventKey, req.EventID, eventType, req.Visibility, req.OccurredAt, req.Payload,
	)
	// 首次 delta 无 session → 自动创建（隐式 open）
	if err == wkcache.ErrMessageEventSessionNotFound || err == wkcache.ErrMessageEventKeyNotFound {
		if _, upsertErr := service.MessageEventCache.UpsertSession(wkcache.MessageEventSessionMeta{
			ClientMsgNo:       req.ClientMsgNo,
			ChannelId:         fakeChannelID,
			ChannelType:       req.ChannelType,
			FromUid:           req.FromUID,
			OriginalChannelId: req.ChannelID,
			MessageId:         req.MessageID,
		}, eventKey, 0); upsertErr != nil {
			c.ResponseError(upsertErr)
			return
		}
		cacheKeyState, err = service.MessageEventCache.AppendDelta(
			req.ClientMsgNo, fakeChannelID, req.ChannelType,
			eventKey, req.EventID, eventType, req.Visibility, req.OccurredAt, req.Payload,
		)
	}
	if err != nil {
		m.Error("eventAppend: delta cache append failed", zap.Error(err), zap.String("clientMsgNo", req.ClientMsgNo), zap.String("eventID", req.EventID))
		c.ResponseError(err)
		return
	}

	// 推送事件到在线客户端
	m.sendEvent(req, fakeChannelID, eventKey, req.MessageID, cacheKeyState.PersistedSeq, cacheKeyState.Status)

	c.ResponseOKWithData(eventAppendResp{
		ClientMsgNo:  req.ClientMsgNo,
		EventKey:     eventKey,
		EventID:      req.EventID,
		MsgEventSeq:  cacheKeyState.PersistedSeq,
		StreamStatus: cacheKeyState.Status,
		ChannelID:    req.ChannelID,
		ChannelType:  req.ChannelType,
		FromUID:      req.FromUID,
	})
}

// handleStreamPersist 处理终态事件 / 非 stream 事件，通过 raft 提案持久化。
func (m *message) handleStreamPersist(c *wkhttp.Context, req *eventAppendReq, fakeChannelID, eventKey, eventType string) {
	payload := []byte(req.Payload)

	// 终态事件：将缓存中累积的快照合并到 payload
	if isTerminalStreamEventType(eventType) && service.MessageEventCache != nil {
		if mergedPayload, _, err := service.MessageEventCache.BuildTerminalPayload(
			req.ClientMsgNo, fakeChannelID, req.ChannelType,
			eventKey, payload, req.EventID, eventType, req.Visibility, req.OccurredAt,
		); err == nil {
			payload = mergedPayload
		}
	}

	// raft 提案落盘
	evt := &wkdb.MessageEvent{
		ClientMsgNo: req.ClientMsgNo,
		EventID:     req.EventID,
		EventKey:    eventKey,
		EventType:   eventType,
		Visibility:  req.Visibility,
		OccurredAt:  req.OccurredAt,
		Payload:     payload,
		Headers:     req.Headers,
	}
	stored, eventState, err := service.Store.AppendMessageEventWithState(fakeChannelID, req.ChannelType, evt)
	if err != nil {
		m.Error("eventAppend: 追加事件失败！", zap.Error(err), zap.String("clientMsgNo", req.ClientMsgNo), zap.String("eventID", req.EventID))
		c.ResponseError(err)
		return
	}

	// 同步持久化结果到缓存
	m.syncEventCacheAfterPersist(req, fakeChannelID, eventKey, stored.MsgEventSeq, eventState.Status)

	// 推送事件到在线客户端
	m.sendEvent(req, fakeChannelID, eventKey, req.MessageID, stored.MsgEventSeq, eventState.Status)

	c.ResponseOKWithData(eventAppendResp{
		ClientMsgNo:  stored.ClientMsgNo,
		EventKey:     stored.EventKey,
		EventID:      stored.EventID,
		MsgEventSeq:  stored.MsgEventSeq,
		StreamStatus: eventState.Status,
		ChannelID:    req.ChannelID,
		ChannelType:  req.ChannelType,
		FromUID:      req.FromUID,
	})
}

// syncEventCacheAfterPersist 在 raft 提案落盘后，将结果同步到内存缓存。
func (m *message) syncEventCacheAfterPersist(req *eventAppendReq, fakeChannelID, eventKey string, persistedSeq uint64, status string) {
	if service.MessageEventCache == nil {
		return
	}
	if _, err := service.MessageEventCache.UpsertSession(wkcache.MessageEventSessionMeta{
		ClientMsgNo:       req.ClientMsgNo,
		ChannelId:         fakeChannelID,
		ChannelType:       req.ChannelType,
		FromUid:           req.FromUID,
		OriginalChannelId: req.ChannelID,
		MessageId:         req.MessageID,
	}, eventKey, persistedSeq); err != nil && err != wkcache.ErrMessageEventChannelMismatch {
		m.Warn("eventAppend: upsert cache failed", zap.Error(err), zap.String("clientMsgNo", req.ClientMsgNo))
	}
	if err := service.MessageEventCache.MarkEventKeyPersisted(req.ClientMsgNo, fakeChannelID, req.ChannelType, eventKey, status, persistedSeq); err != nil && err != wkcache.ErrMessageEventSessionNotFound {
		m.Warn("eventAppend: mark event key persisted failed", zap.Error(err), zap.String("clientMsgNo", req.ClientMsgNo))
	}
}

// eventPushData 是推送给客户端的事件 Data 结构，包含合流所需的完整上下文。
type eventPushData struct {
	ClientMsgNo  string          `json:"client_msg_no"`
	ChannelID    string          `json:"channel_id"`
	ChannelType  uint8           `json:"channel_type"`
	FromUID      string          `json:"from_uid"`
	MessageID    int64           `json:"message_id"`
	EventKey     string          `json:"event_key"`
	MsgEventSeq  uint64          `json:"msg_event_seq"`
	StreamStatus string          `json:"stream_status"`
	Visibility   string          `json:"visibility,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}

// sendEvent 将事件推送给频道内的在线客户端
func (m *message) sendEvent(req *eventAppendReq, fakeChannelID string, eventKey string, messageId int64, msgEventSeq uint64, streamStatus string) {
	pushData := eventPushData{
		ClientMsgNo:  req.ClientMsgNo,
		ChannelID:    req.ChannelID,
		ChannelType:  req.ChannelType,
		FromUID:      req.FromUID,
		MessageID:    messageId,
		EventKey:     eventKey,
		MsgEventSeq:  msgEventSeq,
		StreamStatus: streamStatus,
		Visibility:   req.Visibility,
		Payload:      req.Payload,
	}
	data, err := json.Marshal(pushData)
	if err != nil {
		m.Error("sendEvent: marshal push data failed", zap.Error(err))
		return
	}

	event := &wkproto.EventPacket{
		Type:      req.EventType,
		Id:        req.EventID,
		Timestamp: req.OccurredAt,
		Data:      data,
	}

	eventbus.Channel.SendMessage(fakeChannelID, req.ChannelType, &eventbus.Event{
		Conn: &eventbus.Conn{
			Uid:      req.FromUID,
			DeviceId: options.G.SystemDeviceId,
		},
		Type:      eventbus.EventChannelDistribute,
		Frame:     event,
		MessageId: messageId,
	})
}

func (m *message) eventSync(c *wkhttp.Context) {
	var req eventSyncReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		m.Error("eventSync: 数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	if req.Limit == 0 {
		req.Limit = 200
	}
	if req.Limit > 2000 {
		req.Limit = 2000
	}

	fakeChannelID := req.ChannelID
	if req.ChannelType == wkproto.ChannelTypePerson && strings.TrimSpace(req.FromUID) != "" {
		fakeChannelID = options.GetFakeChannelIDWith(req.ChannelID, req.FromUID)
	}
	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(fakeChannelID, req.ChannelType)
	if err != nil {
		m.Error("eventSync: 获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	if leaderInfo.Id != options.G.Cluster.NodeId {
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	events, err := service.Store.ListMessageEvents(fakeChannelID, req.ChannelType, req.ClientMsgNo, req.FromMsgEventSeq, req.EventKey, req.Limit+1)
	if err != nil {
		m.Error("eventSync: 加载事件失败！", zap.Error(err), zap.String("clientMsgNo", req.ClientMsgNo))
		c.ResponseError(err)
		return
	}

	filtered := make([]wkdb.MessageEvent, 0, len(events))
	for _, evt := range events {
		if req.IncludePrivate != 1 && (evt.Visibility == wkdb.VisibilityPrivate || evt.Visibility == wkdb.VisibilityRestricted) {
			continue
		}
		filtered = append(filtered, evt)
		if len(filtered) >= req.Limit+1 {
			break
		}
	}

	more := 0
	if len(filtered) > req.Limit {
		more = 1
		filtered = filtered[:req.Limit]
	}

	nextSeq := req.FromMsgEventSeq
	if len(filtered) > 0 {
		nextSeq = filtered[len(filtered)-1].MsgEventSeq
	}

	respEvents := make([]map[string]interface{}, 0, len(filtered))
	for _, evt := range filtered {
		respEvt := map[string]interface{}{
			"msg_event_seq": evt.MsgEventSeq,
			"event_id":      evt.EventID,
			"event_key":     evt.EventKey,
			"event_type":    evt.EventType,
			"visibility":    evt.Visibility,
			"occurred_at":   evt.OccurredAt,
		}
		if len(evt.Payload) > 0 {
			var payload interface{}
			if err := json.Unmarshal(evt.Payload, &payload); err != nil {
				respEvt["payload"] = string(evt.Payload)
			} else {
				respEvt["payload"] = payload
			}
		}
		respEvents = append(respEvents, respEvt)
	}

	c.ResponseOKWithData(map[string]interface{}{
		"client_msg_no":         req.ClientMsgNo,
		"from_msg_event_seq":    req.FromMsgEventSeq,
		"next_msg_event_seq":    nextSeq,
		"more":                  more,
		"events":                respEvents,
		"filtered_by_event_key": req.EventKey,
	})
}

// ==================== 从 channel.go 移动过来的方法 ====================

// 同步频道内的消息
func (m *message) syncMessages(c *wkhttp.Context) {

	var req struct {
		LoginUID         string   `json:"login_uid"` // 当前登录用户的uid
		ChannelID        string   `json:"channel_id"`
		ChannelType      uint8    `json:"channel_type"`
		StartMessageSeq  uint64   `json:"start_message_seq"`  //开始消息列号（结果包含start_message_seq的消息）
		EndMessageSeq    uint64   `json:"end_message_seq"`    // 结束消息列号（结果不包含end_message_seq的消息）
		Limit            int      `json:"limit"`              // 每次同步数量限制
		PullMode         PullMode `json:"pull_mode"`          // 拉取模式 0:向下拉取 1:向上拉取
		EventSummaryMode string   `json:"event_summary_mode"` // 事件摘要模式：basic/full
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		m.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if strings.TrimSpace(req.ChannelID) == "" {
		m.Error("channel_id不能为空！", zap.Any("req", req))
		c.ResponseError(errors.New("channel_id不能为空！"))
		return
	}
	if strings.TrimSpace(req.LoginUID) == "" {
		m.Error("login_uid不能为空！", zap.Any("req", req))
		c.ResponseError(errors.New("login_uid不能为空！"))
		return
	}

	var (
		limit         = req.Limit
		fakeChannelID = req.ChannelID
		messages      []wkdb.Message
	)

	if limit > 10000 {
		limit = 10000
	}

	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelID = options.GetFakeChannelIDWith(req.LoginUID, req.ChannelID)
	}

	leaderInfo, err := service.Cluster.LeaderOfChannel(fakeChannelID, req.ChannelType) // 获取频道的领导节点
	if errors.Is(err, cluster.ErrChannelClusterConfigNotFound) {
		m.Info("空频道，返回空消息.", zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.JSON(http.StatusOK, emptySyncMessageResp)
		return
	}
	if err != nil {
		m.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.ChannelID), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId

	if !leaderIsSelf {
		m.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}
	if req.StartMessageSeq == 0 && req.EndMessageSeq == 0 {
		messages, err = service.Store.LoadLastMsgs(fakeChannelID, req.ChannelType, limit)
	} else if req.PullMode == PullModeUp { // 向上拉取
		messages, err = service.Store.LoadNextRangeMsgs(fakeChannelID, req.ChannelType, req.StartMessageSeq, req.EndMessageSeq, limit)
	} else {
		if req.EndMessageSeq <= req.StartMessageSeq {
			messages, err = service.Store.LoadPrevRangeMsgs(fakeChannelID, req.ChannelType, req.StartMessageSeq, req.EndMessageSeq, limit)
		}
	}
	if err != nil {
		m.Error("获取消息失败！", zap.Error(err), zap.Any("req", req))
		c.ResponseError(err)
		return
	}

	messageResps := make([]*types.MessageResp, 0, len(messages))
	if len(messages) > 0 {
		for _, message := range messages {
			messageResp := &types.MessageResp{}
			messageResp.From(message, options.G.SystemUID)
			messageResps = append(messageResps, messageResp)
		}
	}
	var more bool = true // 是否有更多数据
	if len(messageResps) < limit {
		more = false
	}
	if len(messageResps) > 0 {

		if req.PullMode == PullModeDown {
			if req.EndMessageSeq != 0 {
				messageSeq := messageResps[0].MessageSeq
				if req.EndMessageSeq == messageSeq {
					more = false
				}
			}
		} else {
			if req.EndMessageSeq != 0 {
				messageSeq := messageResps[len(messageResps)-1].MessageSeq
				if req.EndMessageSeq == messageSeq {
					more = false
				}
			}
		}
	}

	// 填充事件元数据和 legacy 流字段
	if err := m.fillMessagesEventMeta(fakeChannelID, req.ChannelType, messageResps, req.EventSummaryMode); err != nil {
		m.Error("syncMessages: fillMessagesEventMeta failed", zap.Error(err), zap.Any("req", req))
		c.ResponseError(err)
		return
	}

	// 返回消息
	c.JSON(http.StatusOK, syncMessageResp{
		StartMessageSeq: req.StartMessageSeq,
		EndMessageSeq:   req.EndMessageSeq,
		More:            wkutil.BoolToInt(more),
		Messages:        messageResps,
	})
}

func (m *message) fillMessagesEventMeta(channelId string, channelType uint8, messages []*types.MessageResp, summaryMode string) error {
	mode := strings.ToLower(strings.TrimSpace(summaryMode))
	if mode == "" {
		mode = "full"
	}

	// Only collect clientMsgNos from stream messages (SettingStream flag).
	clientMsgNos := make([]string, 0, len(messages))
	for _, msg := range messages {
		setting := wkproto.Setting(msg.Setting)
		if setting.IsSet(wkproto.SettingStream) && msg.ClientMsgNo != "" {
			clientMsgNos = append(clientMsgNos, msg.ClientMsgNo)
		}
	}
	if len(clientMsgNos) == 0 {
		return nil
	}

	allEventStates, err := service.Store.GetMessageEventStatesBatch(channelId, channelType, clientMsgNos)
	if err != nil {
		return err
	}

	for _, msg := range messages {
		eventStates := mergeEventStatesWithCache(channelId, channelType, msg.ClientMsgNo, allEventStates[msg.ClientMsgNo])
		if len(eventStates) == 0 {
			continue
		}
		meta := &types.MessageEventMeta{
			HasEvents: true,
			Events:    make([]*types.MessageEventKeyMeta, 0, len(eventStates)),
		}

		for _, eventState := range eventStates {
			if eventState.EventKey == wkdb.EventKeyFinish {
				meta.Completed = true
				continue
			}
			keyMeta := &types.MessageEventKeyMeta{
				EventKey:        eventState.EventKey,
				Status:          eventState.Status,
				LastMsgEventSeq: eventState.LastMsgEventSeq,
				EndReason:       eventState.EndReason,
				Error:           eventState.Error,
			}
			if eventState.LastMsgEventSeq > meta.LastMsgEventSeq {
				meta.LastMsgEventSeq = eventState.LastMsgEventSeq
			}
			if eventState.Status == wkdb.EventStatusOpen {
				meta.OpenEventCount++
			}
			if mode == "full" && len(eventState.SnapshotPayload) > 0 {
				var snapshot interface{}
				if err := json.Unmarshal(eventState.SnapshotPayload, &snapshot); err != nil {
					keyMeta.Snapshot = string(eventState.SnapshotPayload)
				} else {
					keyMeta.Snapshot = snapshot
				}
			}
			meta.Events = append(meta.Events, keyMeta)
		}
		meta.EventCount = len(meta.Events)
		meta.EventVersion = meta.LastMsgEventSeq
		msg.EventMeta = meta
		msg.EventHint = &types.MessageEventSyncHint{
			ClientMsgNo:     msg.ClientMsgNo,
			FromMsgEventSeq: 0,
		}

		mainState := findMainEventState(eventStates)
		if mainState != nil {
			if len(mainState.SnapshotPayload) > 0 {
				msg.StreamData = toLegacyStreamData(mainState.SnapshotPayload)
			}
			msg.End = toLegacyEnd(mainState.Status)
			msg.EndReason = mainState.EndReason
			msg.Error = mainState.Error
		}
	}
	return nil
}

// mergeEventStatesWithCache merges DB event states with in-memory cache states.
// Cache states (from in-progress streams) take priority over DB states for the same event key.
func mergeEventStatesWithCache(channelId string, channelType uint8, clientMsgNo string, dbStates []wkdb.MessageEventState) []wkdb.MessageEventState {
	if service.MessageEventCache == nil {
		return dbStates
	}
	cacheStates, ok := service.MessageEventCache.GetEventKeyStates(clientMsgNo, channelId, channelType)
	if !ok || len(cacheStates) == 0 {
		return dbStates
	}

	// Build a map from DB states keyed by event key.
	merged := make(map[string]wkdb.MessageEventState, len(dbStates)+len(cacheStates))
	for _, s := range dbStates {
		merged[s.EventKey] = s
	}

	// Override or add from cache states.
	for _, cs := range cacheStates {
		existing, exists := merged[cs.EventKey]
		if !exists {
			// Cache-only entry: stream opened but nothing persisted yet.
			merged[cs.EventKey] = cacheStateToDBState(channelId, channelType, clientMsgNo, cs)
			continue
		}
		// Cache has more recent data if its PersistedSeq >= DB seq or status is open.
		if cs.PersistedSeq >= existing.LastMsgEventSeq || cs.Status == wkdb.EventStatusOpen {
			ms := cacheStateToDBState(channelId, channelType, clientMsgNo, cs)
			// Preserve DB-only fields if cache doesn't have them.
			if ms.LastMsgEventSeq == 0 {
				ms.LastMsgEventSeq = existing.LastMsgEventSeq
			}
			if ms.EndReason == 0 {
				ms.EndReason = existing.EndReason
			}
			if ms.Error == "" {
				ms.Error = existing.Error
			}
			// Build snapshot from cache text if available.
			if cs.TextSnapshot != "" {
				snapshot := map[string]interface{}{
					"kind": wkdb.SnapshotKindText,
					"text": cs.TextSnapshot,
				}
				if data, err := json.Marshal(snapshot); err == nil {
					ms.SnapshotPayload = data
				}
			} else if len(cs.SnapshotPayload) > 0 {
				ms.SnapshotPayload = append([]byte(nil), cs.SnapshotPayload...)
			} else if len(existing.SnapshotPayload) > 0 {
				ms.SnapshotPayload = existing.SnapshotPayload
			}
			merged[cs.EventKey] = ms
		}
	}

	result := make([]wkdb.MessageEventState, 0, len(merged))
	for _, s := range merged {
		result = append(result, s)
	}
	return result
}

func cacheStateToDBState(channelId string, channelType uint8, clientMsgNo string, cs wkcache.EventKeyState) wkdb.MessageEventState {
	state := wkdb.MessageEventState{
		ChannelId:       channelId,
		ChannelType:     channelType,
		ClientMsgNo:     clientMsgNo,
		EventKey:        cs.EventKey,
		Status:          cs.Status,
		LastMsgEventSeq: cs.PersistedSeq,
		LastEventID:     cs.LastEventID,
		LastEventType:   cs.LastEventType,
		LastVisibility:  cs.LastVisibility,
		LastOccurredAt:  cs.LastOccurredAt,
	}
	if cs.TextSnapshot != "" {
		snapshot := map[string]interface{}{
			"kind": wkdb.SnapshotKindText,
			"text": cs.TextSnapshot,
		}
		if data, err := json.Marshal(snapshot); err == nil {
			state.SnapshotPayload = data
		}
	} else if len(cs.SnapshotPayload) > 0 {
		state.SnapshotPayload = append([]byte(nil), cs.SnapshotPayload...)
	}
	return state
}

func findMainEventState(states []wkdb.MessageEventState) *wkdb.MessageEventState {
	for _, state := range states {
		if state.EventKey == wkdb.EventKeyDefault {
			cp := state
			return &cp
		}
	}
	return nil
}

func toLegacyEnd(status string) uint8 {
	switch status {
	case wkdb.EventStatusClosed, wkdb.EventStatusError, wkdb.EventStatusCancelled:
		return 1
	default:
		return 0
	}
}

func toLegacyStreamData(snapshotPayload []byte) []byte {
	if len(snapshotPayload) == 0 {
		return nil
	}
	m := map[string]interface{}{}
	if err := json.Unmarshal(snapshotPayload, &m); err != nil {
		return snapshotPayload
	}
	kind, _ := m["kind"].(string)
	switch kind {
	case wkdb.SnapshotKindText:
		text, _ := m["text"].(string)
		return []byte(text)
	case "binary":
		if data, ok := m["data"].(string); ok {
			return []byte(data)
		}
	}
	return snapshotPayload
}

func isTerminalStreamEventType(eventType string) bool {
	switch eventType {
	case wkdb.EventTypeStreamClose, wkdb.EventTypeStreamError, wkdb.EventTypeStreamCancel, wkdb.EventTypeStreamFinish:
		return true
	default:
		return false
	}
}

func (m *message) getChannelMaxMessageSeq(c *wkhttp.Context) {
	channelId := c.Query("channel_id")
	channelType := wkutil.StringToUint8(c.Query("channel_type"))
	loginUid := c.Query("login_uid")

	if channelId == "" {
		c.ResponseError(errors.New("channel_id不能为空"))
		return
	}

	fakeChannelId := channelId

	if channelType == wkproto.ChannelTypePerson {
		if strings.Contains(channelId, "@") {
			fakeChannelId = channelId
		} else {
			if strings.TrimSpace(loginUid) == "" {
				c.ResponseError(errors.New("login_uid不能为空"))
				return
			}
			fakeChannelId = options.GetFakeChannelIDWith(loginUid, channelId)
		}
	}

	leaderInfo, err := service.Cluster.LeaderOfChannelForRead(fakeChannelId, channelType)
	if err != nil && errors.Is(err, cluster.ErrChannelClusterConfigNotFound) {
		c.JSON(http.StatusOK, gin.H{
			"message_seq": 0,
		})
		return
	}
	if err != nil {
		c.ResponseError(err)
		return
	}

	if leaderInfo.Id != options.G.Cluster.NodeId {
		c.Forward(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path))
		return
	}

	msgSeq, err := service.Store.GetLastMsgSeq(fakeChannelId, channelType)
	if err != nil {
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message_seq": msgSeq,
	})
}

// 获取频道最后一条消息
func (m *message) getChannelLastMessage(c *wkhttp.Context) {
	loginUid := c.Query("login_uid")
	channelId := c.Query("channel_id")
	channelType := wkutil.StringToUint8(c.Query("channel_type"))

	if channelId == "" {
		c.ResponseError(errors.New("channel_id不能为空"))
		return
	}

	fakeChannelId := channelId
	if channelType == wkproto.ChannelTypePerson {
		if strings.TrimSpace(loginUid) == "" {
			c.ResponseError(errors.New("login_uid不能为空"))
			return
		}
		fakeChannelId = options.GetFakeChannelIDWith(loginUid, channelId)
	}

	leaderInfo, err := service.Cluster.LeaderOfChannelForRead(fakeChannelId, channelType)
	if err != nil && errors.Is(err, cluster.ErrChannelClusterConfigNotFound) {
		c.ResponseStatus(http.StatusNotFound)
		return
	}
	if err != nil {
		m.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", channelId), zap.Uint8("channelType", channelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}

	if leaderInfo.Id != options.G.Cluster.NodeId {
		c.Forward(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path))
		return
	}

	messages, err := service.Store.LoadLastMsgs(fakeChannelId, channelType, 1)
	if err != nil {
		m.Error("获取最后一条消息失败！", zap.Error(err), zap.String("channelID", channelId), zap.Uint8("channelType", channelType))
		c.ResponseError(err)
		return
	}

	if len(messages) == 0 {
		c.ResponseStatus(http.StatusNotFound)
		return
	}

	resp := &types.MessageResp{}
	resp.From(messages[0], options.G.SystemUID)
	c.JSON(http.StatusOK, resp)
}
