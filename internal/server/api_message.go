package server

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// MessageAPI MessageAPI
type MessageAPI struct {
	s *Server
	wklog.Log

	syncRecordMap  map[string][]*syncRecord // 记录最后一次同步命令的记录（TODO：这个是临时方案，为了兼容老版本）
	syncRecordLock sync.RWMutex
}

// NewMessageAPI NewMessageAPI
func NewMessageAPI(s *Server) *MessageAPI {
	return &MessageAPI{
		s:             s,
		Log:           wklog.NewWKLog("MessageApi"),
		syncRecordMap: map[string][]*syncRecord{},
	}
}

// Route route
func (m *MessageAPI) Route(r *wkhttp.WKHttp) {
	r.POST("/message/send", m.send)           // 发送消息
	r.POST("/message/sendbatch", m.sendBatch) // 批量发送消息
	r.POST("/message/sync", m.sync)           // 消息同步(写模式)
	r.POST("/message/syncack", m.syncack)     // 消息同步回执(写模式)

	// // r.POST("/streammessage/start", m.streamMessageStart) // 流消息开始
	// // r.POST("/streammessage/end", m.streamMessageEnd)     // 流消息结束

	r.POST("/messages", m.searchMessages) // 查询消息

}

func (m *MessageAPI) send(c *wkhttp.Context) {
	var req MessageSendReq
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
		req.FromUID = m.s.opts.SystemUID
	}

	channelId := req.ChannelID
	channelType := req.ChannelType

	m.Debug("发送消息内容：", zap.String("msg", wkutil.ToJSON(req)))
	if strings.TrimSpace(channelId) == "" && len(req.Subscribers) == 0 { //指定了频道 才能正常发送
		m.Error("无法处理发送消息请求！", zap.Any("req", req))
		c.ResponseError(errors.New("无法处理发送消息请求！"))
		return
	}

	if strings.TrimSpace(channelId) == "" && len(req.Subscribers) > 0 {
		if req.Header.SyncOnce != 1 {
			m.Error("subscribers有值的情况下，消息必须是syncOnce消息", zap.Any("req", req))
			c.ResponseError(errors.New("无法处理发送消息请求！"))
			return
		}

		for _, subscriber := range req.Subscribers {
			clientMsgNo := fmt.Sprintf("%s0", wkutil.GenUUID())
			// 发送消息
			_, err := m.sendMessageToChannel(req, subscriber, wkproto.ChannelTypePerson, clientMsgNo, wkproto.StreamFlagIng)
			if err != nil {
				c.ResponseError(err)
				return
			}
		}

		c.ResponseOK()

		return
	}

	clientMsgNo := req.ClientMsgNo
	if strings.TrimSpace(clientMsgNo) == "" {
		clientMsgNo = fmt.Sprintf("%s0", wkutil.GenUUID())
	}

	// 发送消息
	messageId, err := m.sendMessageToChannel(req, channelId, channelType, clientMsgNo, wkproto.StreamFlagIng)
	if err != nil {
		c.ResponseError(err)
		return
	}
	c.ResponseOKWithData(map[string]interface{}{
		"message_id":    messageId,
		"client_msg_no": clientMsgNo,
	})
}

func (m *MessageAPI) sendMessageToChannel(req MessageSendReq, channelId string, channelType uint8, clientMsgNo string, streamFlag wkproto.StreamFlag) (int64, error) {

	// m.s.monitor.SendPacketInc(req.Header.NoPersist != 1)
	// m.s.monitor.SendSystemMsgInc()

	// var messageID = m.s.dispatch.processor.genMessageID()

	fakeChannelId := channelId
	fakeChannelType := req.ChannelType
	if channelType == wkproto.ChannelTypePerson {
		fakeChannelId = GetFakeChannelIDWith(req.FromUID, channelId)
	}

	if req.Header.SyncOnce == 1 { // 命令消息，将原频道转换为cmd频道
		fakeChannelId = m.s.opts.OrginalConvertCmdChannel(fakeChannelId)
	}

	channel := m.s.channelReactor.loadOrCreateChannel(fakeChannelId, fakeChannelType)

	if channel == nil {
		return 0, errors.New("频道信息不存在！")
	}

	var setting wkproto.Setting
	if len(strings.TrimSpace(req.StreamNo)) > 0 {
		setting = setting.Set(wkproto.SettingStream)
	}

	// 将消息提交到频道
	systemDeviceId := req.FromUID
	messageId, err := channel.proposeSend(req.FromUID, systemDeviceId, 0, m.s.opts.Cluster.NodeId, false, true, &wkproto.SendPacket{
		Framer: wkproto.Framer{
			RedDot:    wkutil.IntToBool(req.Header.RedDot),
			SyncOnce:  wkutil.IntToBool(req.Header.SyncOnce),
			NoPersist: wkutil.IntToBool(req.Header.NoPersist),
		},
		Setting:     setting,
		Expire:      req.Expire,
		StreamNo:    req.StreamNo,
		ClientMsgNo: clientMsgNo,
		ChannelID:   req.ChannelID,
		ChannelType: req.ChannelType,
		Payload:     req.Payload,
	})
	if err != nil {
		return messageId, err
	}

	return messageId, nil
}

func (m *MessageAPI) sendBatch(c *wkhttp.Context) {
	var req struct {
		Header      MessageHeader `json:"header"`      // 消息头
		FromUID     string        `json:"from_uid"`    // 发送者UID
		Subscribers []string      `json:"subscribers"` // 订阅者 如果此字段有值，表示消息只发给指定的订阅者
		Payload     []byte        `json:"payload"`     // 消息内容
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
		_, err := m.sendMessageToChannel(MessageSendReq{
			Header:      req.Header,
			FromUID:     req.FromUID,
			ChannelID:   subscriber,
			ChannelType: wkproto.ChannelTypePerson,
			Payload:     req.Payload,
		}, subscriber, wkproto.ChannelTypePerson, clientMsgNo, wkproto.StreamFlagIng)
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
func (m *MessageAPI) sync(c *wkhttp.Context) {

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
		req.Limit = 50
	}

	leaderInfo, err := m.s.cluster.SlotLeaderOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
	if err != nil {
		m.Error("获取频道所在节点失败！!", zap.Error(err), zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == m.s.opts.Cluster.NodeId

	if !leaderIsSelf {
		m.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	// ==================== 获取用户活跃的最近会话 ====================
	conversations, err := m.s.store.GetConversationsByType(req.UID, wkdb.ConversationTypeCMD)
	if err != nil {
		m.Error("获取conversation失败！", zap.Error(err), zap.String("uid", req.UID))
		c.ResponseError(errors.New("获取conversation失败！"))
		return
	}

	// 获取用户缓存的最近会话
	cacheConversations := m.s.conversationManager.GetUserConversationFromCache(req.UID, wkdb.ConversationTypeCMD)
	cacheConversationMap := map[string]uint64{}
	for _, cacheConversation := range cacheConversations {
		if cacheConversation.ReadToMsgSeq > 0 {
			cacheConversationMap[fmt.Sprintf("%s-%d", cacheConversation.ChannelId, cacheConversation.ChannelType)] = cacheConversation.ReadToMsgSeq
		}
	}
	for _, cacheConversation := range cacheConversations {
		exist := false
		for i, conversation := range conversations {
			if cacheConversation.ChannelId == conversation.ChannelId && cacheConversation.ChannelType == conversation.ChannelType {
				if cacheConversation.ReadToMsgSeq > conversation.ReadToMsgSeq {
					conversations[i].ReadToMsgSeq = cacheConversation.ReadToMsgSeq
				}
				exist = true
				break
			}
		}
		if !exist {
			conversations = append(conversations, cacheConversation)
		}
	}

	var channelRecentMessageReqs []*channelRecentMessageReq
	if len(conversations) > 0 {
		for _, conversation := range conversations {
			realChannelId := conversation.ChannelId
			if conversation.ChannelType == wkproto.ChannelTypePerson {
				u1, u2 := GetFromUIDAndToUIDWith(conversation.ChannelId)
				if u1 != req.UID {
					realChannelId = u1
				}
				if u2 != req.UID {
					realChannelId = u2
				}
			}
			channelRecentMessageReqs = append(channelRecentMessageReqs, &channelRecentMessageReq{
				ChannelId:   realChannelId,
				ChannelType: conversation.ChannelType,
				LastMsgSeq:  conversation.ReadToMsgSeq + 1, // 这里加1的目的是为了不查询到ReadedToMsgSeq本身这条消息
			})
		}
	}

	// 先清空旧记录
	m.syncRecordLock.Lock()
	m.syncRecordMap[req.UID] = nil
	m.syncRecordLock.Unlock()

	// 获取每个session的消息
	messageResps := make([]*MessageResp, 0)

	if len(channelRecentMessageReqs) > 0 {
		channelRecentMessages, err := m.s.getRecentMessagesForCluster(req.UID, req.Limit, channelRecentMessageReqs, false)
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
			var lastMsg *MessageResp
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

func (m *MessageAPI) syncack(c *wkhttp.Context) {
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

	leaderInfo, err := m.s.cluster.SlotLeaderOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
	if err != nil {
		m.Error("获取频道所在节点失败！!", zap.Error(err), zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == m.s.opts.Cluster.NodeId

	if !leaderIsSelf {
		m.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	m.syncRecordLock.RLock()
	defer m.syncRecordLock.RUnlock()
	if len(m.syncRecordMap[req.UID]) == 0 {
		c.ResponseOK()
		return
	}

	conversations := make([]wkdb.Conversation, 0)
	for _, record := range m.syncRecordMap[req.UID] {
		needAdd := false

		fakeChannelId := record.channelId
		if record.channelType == wkproto.ChannelTypePerson {
			fakeChannelId = GetFakeChannelIDWith(req.UID, record.channelId)
		}
		if !m.s.opts.IsCmdChannel(fakeChannelId) {
			m.Warn("不是cmd频道！", zap.String("uid", req.UID), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", record.channelType))
			continue
		}
		if record.lastMsgSeq <= 0 {
			continue
		}

		conversation, err := m.s.store.GetConversation(req.UID, fakeChannelId, record.channelType)
		if err != nil {
			if err == wkdb.ErrNotFound {
				m.Warn("会话不存在！", zap.String("uid", req.UID), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", record.channelType))
				conversation = wkdb.Conversation{
					Uid:          req.UID,
					ChannelId:    fakeChannelId,
					ChannelType:  record.channelType,
					Type:         wkdb.ConversationTypeCMD,
					ReadToMsgSeq: record.lastMsgSeq,
				}
				needAdd = true
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
		err := m.s.store.AddOrUpdateConversations(req.UID, conversations)
		if err != nil {
			m.Error("消息同步回执失败！", zap.Error(err), zap.String("uid", req.UID))
			c.ResponseError(errors.New("消息同步回执失败！"))
			return
		}
	}
	// if len(deletes) > 0 {
	// 	err = m.s.store.DeleteConversations(req.UID, deletes)
	// 	if err != nil {
	// 		m.Error("删除最近会话失败！", zap.Error(err))
	// 		c.ResponseError(err)
	// 		return
	// 	}
	// 	for _, deleteConversation := range deletes {
	// 		m.s.conversationManager.DeleteUserConversationFromCache(req.UID, deleteConversation.ChannelId, deleteConversation.ChannelType)
	// 	}
	// }

	c.ResponseOK()
}

func (m *MessageAPI) searchMessages(c *wkhttp.Context) {
	var req struct {
		LoginUid    string   `json:"login_uid"`
		ChannelID   string   `json:"channel_id"`
		ChannelType uint8    `json:"channel_type"`
		MessageSeqs []uint32 `json:"message_seqs"`
	}
	if err := c.BindJSON(&req); err != nil {
		m.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if strings.TrimSpace(req.ChannelID) == "" {
		c.ResponseError(errors.New("channel_id不能为空！"))
		return
	}

	if len(req.MessageSeqs) == 0 {
		c.ResponseError(errors.New("message_seqs不能为空！"))
		return

	}
	fakeChannelid := req.ChannelID
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelid = GetFakeChannelIDWith(req.LoginUid, req.ChannelID)
	}
	var messages []wkdb.Message
	for _, seq := range req.MessageSeqs {
		msg, err := m.s.store.LoadMsg(fakeChannelid, req.ChannelType, uint64(seq))
		if err != nil && err != wkdb.ErrNotFound {
			m.Error("查询消息失败！", zap.Error(err))
			c.ResponseError(err)
			return
		}
		if err == nil {
			messages = append(messages, msg)
		}
	}

	resps := make([]*MessageResp, 0, len(messages))
	if len(messages) > 0 {
		for _, message := range messages {
			resp := &MessageResp{}
			resp.from(message)
			resps = append(resps, resp)
		}
	}
	c.JSON(http.StatusOK, &syncMessageResp{
		Messages: resps,
	})
}
