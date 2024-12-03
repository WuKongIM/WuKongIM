package server

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/sendgrid/rest"
	"go.uber.org/zap"
)

// ConversationAPI ConversationAPI
type ConversationAPI struct {
	s *Server
	wklog.Log
}

// NewConversationAPI NewConversationAPI
func NewConversationAPI(s *Server) *ConversationAPI {
	return &ConversationAPI{
		s:   s,
		Log: wklog.NewWKLog("ConversationAPI"),
	}
}

// Route 路由
func (s *ConversationAPI) Route(r *wkhttp.WKHttp) {
	// r.GET("/conversations", s.conversationsList)                    // 获取会话列表 （此接口作废，使用/conversation/sync）
	r.POST("/conversations/clearUnread", s.clearConversationUnread) // 清空会话未读数量
	r.POST("/conversations/setUnread", s.setConversationUnread)     // 设置会话未读数量
	r.POST("/conversations/delete", s.deleteConversation)           // 删除会话
	r.POST("/conversation/sync", s.syncUserConversation)            // 同步会话
	r.POST("/conversation/syncMessages", s.syncRecentMessages)      // 同步会话最近消息
}

// // Get a list of recent conversations
// func (s *ConversationAPI) conversationsList(c *wkhttp.Context) {
// 	uid := c.Query("uid")
// 	if strings.TrimSpace(uid) == "" {
// 		c.ResponseError(errors.New("uid cannot be empty"))
// 		return
// 	}

// 	conversations := s.s.conversationManager.GetConversations(uid, 0, nil)
// 	conversationResps := make([]conversationResp, 0)
// 	if len(conversations) > 0 {
// 		for _, conversation := range conversations {
// 			fakeChannelID := conversation.ChannelID
// 			if conversation.ChannelType == wkproto.ChannelTypePerson {
// 				fakeChannelID = GetFakeChannelIDWith(uid, conversation.ChannelID)
// 			}
// 			// 获取到偏移位内的指定最大条数的最新消息
// 			message, err := s.s.store.LoadMsg(fakeChannelID, conversation.ChannelType, conversation.LastMsgSeq)
// 			if err != nil {
// 				s.Error("Failed to query recent news", zap.Error(err))
// 				c.ResponseError(err)
// 				return
// 			}
// 			messageResp := &MessageResp{}
// 			if message != nil {
// 				messageResp.from(message.(*Message), s.s.store)
// 			}
// 			conversationResps = append(conversationResps, conversationResp{
// 				ChannelID:   conversation.ChannelID,
// 				ChannelType: conversation.ChannelType,
// 				Unread:      conversation.UnreadCount,
// 				Timestamp:   conversation.Timestamp,
// 				LastMessage: messageResp,
// 			})
// 		}
// 	}
// 	c.JSON(http.StatusOK, conversationResps)
// }

// 清楚会话未读数量
func (s *ConversationAPI) clearConversationUnread(c *wkhttp.Context) {
	var req clearConversationUnreadReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		s.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}

	if s.s.opts.ClusterOn() {
		leaderInfo, err := s.s.cluster.SlotLeaderOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
		if err != nil {
			s.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == s.s.opts.Cluster.NodeId
		if !leaderIsSelf {
			s.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}

	fakeChannelId := req.ChannelID
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = GetFakeChannelIDWith(req.UID, req.ChannelID)

	}

	conversation, err := s.s.store.GetConversation(req.UID, fakeChannelId, req.ChannelType)
	if err != nil && err != wkdb.ErrNotFound {
		s.Error("Failed to query conversation", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if wkdb.IsEmptyConversation(conversation) {
		createdAt := time.Now()
		updatedAt := time.Now()
		conversation = wkdb.Conversation{
			Type:        wkdb.ConversationTypeChat,
			Uid:         req.UID,
			ChannelId:   fakeChannelId,
			ChannelType: req.ChannelType,
			CreatedAt:   &createdAt,
			UpdatedAt:   &updatedAt,
		}
	}

	// 获取此频道最新的消息
	msgSeq, err := s.s.store.GetLastMsgSeq(fakeChannelId, req.ChannelType)
	if err != nil {
		s.Error("Failed to query last message", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if conversation.ReadToMsgSeq < msgSeq {
		conversation.ReadToMsgSeq = msgSeq

	}

	err = s.s.store.AddOrUpdateUserConversations(req.UID, []wkdb.Conversation{conversation})
	if err != nil {
		s.Error("Failed to add conversation", zap.Error(err))
		c.ResponseError(err)
		return
	}

	s.s.conversationManager.DeleteUserConversationFromCache(req.UID, fakeChannelId, req.ChannelType)

	c.ResponseOK()
}

func (s *ConversationAPI) setConversationUnread(c *wkhttp.Context) {
	var req struct {
		UID         string `json:"uid"`
		ChannelID   string `json:"channel_id"`
		ChannelType uint8  `json:"channel_type"`
		Unread      int    `json:"unread"`
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		s.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if req.UID == "" {
		c.ResponseError(errors.New("UID cannot be empty"))
		return
	}
	if req.ChannelID == "" || req.ChannelType == 0 {
		c.ResponseError(errors.New("channel_id or channel_type cannot be empty"))
		return
	}

	if s.s.opts.ClusterOn() {
		leaderInfo, err := s.s.cluster.SlotLeaderOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
		if err != nil {
			s.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == s.s.opts.Cluster.NodeId
		if !leaderIsSelf {
			s.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}

	fakeChannelId := req.ChannelID
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = GetFakeChannelIDWith(req.UID, req.ChannelID)

	}
	// 获取此频道最新的消息
	msgSeq, err := s.s.store.GetLastMsgSeq(fakeChannelId, req.ChannelType)
	if err != nil {
		s.Error("Failed to query last message", zap.Error(err))
		c.ResponseError(err)
		return
	}

	conversation, err := s.s.store.GetConversation(req.UID, fakeChannelId, req.ChannelType)
	if err != nil && err != wkdb.ErrNotFound {
		s.Error("Failed to query conversation", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if wkdb.IsEmptyConversation(conversation) {
		createdAt := time.Now()
		updatedAt := time.Now()
		conversation = wkdb.Conversation{
			Uid:         req.UID,
			Type:        wkdb.ConversationTypeChat,
			ChannelId:   fakeChannelId,
			ChannelType: req.ChannelType,
			CreatedAt:   &createdAt,
			UpdatedAt:   &updatedAt,
		}

	}

	var unread uint32 = 0
	var readedMsgSeq uint64 = msgSeq

	if uint64(req.Unread) > msgSeq {
		unread = 1
		readedMsgSeq = msgSeq - 1
	} else if req.Unread > 0 {
		unread = uint32(req.Unread)
		readedMsgSeq = msgSeq - uint64(req.Unread)
	}

	conversation.ReadToMsgSeq = readedMsgSeq
	conversation.UnreadCount = unread

	err = s.s.store.AddOrUpdateUserConversations(req.UID, []wkdb.Conversation{conversation})
	if err != nil {
		s.Error("Failed to add conversation", zap.Error(err))
		c.ResponseError(err)
		return
	}

	s.s.conversationManager.DeleteUserConversationFromCache(req.UID, fakeChannelId, req.ChannelType)

	c.ResponseOK()
}

func (s *ConversationAPI) deleteConversation(c *wkhttp.Context) {
	var req deleteChannelReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		s.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}

	if s.s.opts.ClusterOn() {
		leaderInfo, err := s.s.cluster.SlotLeaderOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
		if err != nil {
			s.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == s.s.opts.Cluster.NodeId

		if !leaderIsSelf {
			s.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}
	fakeChannelId := req.ChannelID
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = GetFakeChannelIDWith(req.UID, req.ChannelID)

	}

	err = s.s.store.DeleteConversation(req.UID, fakeChannelId, req.ChannelType)
	if err != nil {
		s.Error("删除会话！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	s.s.conversationManager.DeleteUserConversationFromCache(req.UID, fakeChannelId, req.ChannelType)

	c.ResponseOK()
}

func (s *ConversationAPI) syncUserConversation(c *wkhttp.Context) {
	var req struct {
		UID         string `json:"uid"`
		Version     int64  `json:"version"`       // 当前客户端的会话最大版本号(客户端最新会话的时间戳)
		LastMsgSeqs string `json:"last_msg_seqs"` // 客户端所有会话的最后一条消息序列号 格式： channelID:channelType:last_msg_seq|channelID:channelType:last_msg_seq
		MsgCount    int64  `json:"msg_count"`     // 每个会话消息数量
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		s.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	leaderInfo, err := s.s.cluster.SlotLeaderOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
	if err != nil {
		s.Error("获取频道所在节点失败！!", zap.Error(err), zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == s.s.opts.Cluster.NodeId

	if !leaderIsSelf {
		s.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	var (
		channelLastMsgMap        = s.getChannelLastMsgSeqMap(req.LastMsgSeqs) // 获取频道对应的最后一条消息的messageSeq
		channelRecentMessageReqs = make([]*channelRecentMessageReq, 0, len(channelLastMsgMap))
	)

	// 获取用户最近会话基础数据

	var (
		resps = make([]*syncUserConversationResp, 0)
	)

	// ==================== 获取用户活跃的最近会话 ====================
	conversations, err := s.s.store.GetLastConversations(req.UID, wkdb.ConversationTypeChat, 0, s.s.opts.Conversation.UserMaxCount)
	if err != nil && err != wkdb.ErrNotFound {
		s.Error("获取conversation失败！", zap.Error(err), zap.String("uid", req.UID))
		c.ResponseError(errors.New("获取conversation失败！"))
		return
	}

	// 获取用户缓存的最近会话
	cacheConversations := s.s.conversationManager.GetUserConversationFromCache(req.UID, wkdb.ConversationTypeChat)

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

	// conversations里去掉重复的

	// 获取真实的频道ID
	getRealChannelId := func(fakeChannelId string, channelType uint8) string {
		realChannelId := fakeChannelId
		if channelType == wkproto.ChannelTypePerson {
			from, to := GetFromUIDAndToUIDWith(fakeChannelId)
			if req.UID == from {
				realChannelId = to
			} else {
				realChannelId = from
			}
		}
		return realChannelId
	}

	// 去掉重复的会话
	conversations = removeDuplicates(conversations)

	// 设置最近会话已读至的消息序列号
	for _, conversation := range conversations {

		realChannelId := getRealChannelId(conversation.ChannelId, conversation.ChannelType)

		msgSeq := channelLastMsgMap[fmt.Sprintf("%s-%d", realChannelId, conversation.ChannelType)]

		if msgSeq != 0 {
			msgSeq = msgSeq + 1 // 如果客户端传递了messageSeq，则需要获取这个messageSeq之后的消息
		}

		channelRecentMessageReqs = append(channelRecentMessageReqs, &channelRecentMessageReq{
			ChannelId:   conversation.ChannelId,
			ChannelType: conversation.ChannelType,
			LastMsgSeq:  msgSeq,
		})
		// syncUserConversationR := newSyncUserConversationResp(conversation)
		// resps = append(resps, syncUserConversationR)
	}

	// ==================== 获取最近会话的最近的消息列表 ====================
	if req.MsgCount > 0 {
		var channelRecentMessages []*channelRecentMessage

		// 获取用户最近会话的最近消息
		channelRecentMessages, err = s.s.getRecentMessagesForCluster(req.UID, int(req.MsgCount), channelRecentMessageReqs, true)
		if err != nil {
			s.Error("获取最近消息失败！", zap.Error(err), zap.String("uid", req.UID))
			c.ResponseError(errors.New("获取最近消息失败！"))
			return
		}

		for i := 0; i < len(conversations); i++ {
			conversation := conversations[i]
			realChannelId := getRealChannelId(conversation.ChannelId, conversation.ChannelType)
			if conversation.ChannelType == wkproto.ChannelTypePerson && realChannelId == s.s.opts.SystemUID { // 系统消息不返回
				continue
			}
			resp := newSyncUserConversationResp(conversation)

			for _, channelRecentMessage := range channelRecentMessages {
				if conversation.ChannelId == channelRecentMessage.ChannelId && conversation.ChannelType == channelRecentMessage.ChannelType {
					if len(channelRecentMessage.Messages) > 0 {
						lastMsg := channelRecentMessage.Messages[0]
						resp.LastMsgSeq = uint32(lastMsg.MessageSeq)
						resp.LastClientMsgNo = lastMsg.ClientMsgNo
						resp.Timestamp = int64(lastMsg.Timestamp)
						if lastMsg.MessageSeq > uint64(resp.ReadedToMsgSeq) {
							resp.Unread = int(lastMsg.MessageSeq - uint64(resp.ReadedToMsgSeq))
						}

						resp.Version = time.Unix(int64(lastMsg.Timestamp), 0).UnixNano()
					}

					resp.Recents = channelRecentMessage.Messages
					break
				}
			}

			msgSeq := channelLastMsgMap[fmt.Sprintf("%s-%d", conversation.ChannelId, conversation.ChannelType)]

			if msgSeq != 0 && msgSeq >= uint64(resp.LastMsgSeq) {
				continue
			}

			if len(resp.Recents) > 0 {
				resps = append(resps, resp)
			}
		}
	}

	c.JSON(http.StatusOK, resps)
}

func removeDuplicates(conversations []wkdb.Conversation) []wkdb.Conversation {
	seen := make(map[string]bool)
	result := []wkdb.Conversation{}

	for _, conversation := range conversations {
		channelKey := wkutil.ChannelToKey(conversation.ChannelId, conversation.ChannelType)
		if !seen[channelKey] {
			seen[channelKey] = true
			result = append(result, conversation)
		}
	}
	return result
}

func (s *ConversationAPI) getChannelLastMsgSeqMap(lastMsgSeqs string) map[string]uint64 {
	channelLastMsgSeqStrList := strings.Split(lastMsgSeqs, "|")
	channelLastMsgMap := map[string]uint64{} // 频道对应的messageSeq
	for _, channelLastMsgSeqStr := range channelLastMsgSeqStrList {
		channelLastMsgSeqs := strings.Split(channelLastMsgSeqStr, ":")
		if len(channelLastMsgSeqs) != 3 {
			continue
		}
		channelID := channelLastMsgSeqs[0]
		channelTypeI, _ := strconv.Atoi(channelLastMsgSeqs[1])
		lastMsgSeq, _ := strconv.ParseUint(channelLastMsgSeqs[2], 10, 64)
		channelLastMsgMap[fmt.Sprintf("%s-%d", channelID, channelTypeI)] = lastMsgSeq
	}
	return channelLastMsgMap
}

func (s *ConversationAPI) syncRecentMessages(c *wkhttp.Context) {
	var req struct {
		UID         string                     `json:"uid"`
		Channels    []*channelRecentMessageReq `json:"channels"`
		MsgCount    int                        `json:"msg_count"`
		OrderByLast int                        `json:"order_by_last"`
	}
	if err := c.BindJSON(&req); err != nil {
		s.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(errors.New("数据格式有误！"))
		return
	}
	msgCount := req.MsgCount
	if msgCount <= 0 {
		msgCount = 15
	}
	channelRecentMessages, err := s.s.getRecentMessages(req.UID, msgCount, req.Channels, wkutil.IntToBool(req.OrderByLast))
	if err != nil {
		s.Error("获取最近消息失败！", zap.Error(err))
		c.ResponseError(errors.New("获取最近消息失败！"))
		return
	}
	c.JSON(http.StatusOK, channelRecentMessages)
}

func (s *Server) getRecentMessagesForCluster(uid string, msgCount int, channels []*channelRecentMessageReq, orderByLast bool) ([]*channelRecentMessage, error) {
	if len(channels) == 0 {
		return nil, nil
	}
	channelRecentMessages := make([]*channelRecentMessage, 0)
	var (
		err error
	)

	// 按照频道所在节点进行分组
	peerChannelRecentMessageReqsMap := make(map[uint64][]*channelRecentMessageReq)
	for _, channelRecentMsgReq := range channels {
		fakeChannelId := channelRecentMsgReq.ChannelId
		leaderInfo, err := s.cluster.LeaderOfChannelForRead(fakeChannelId, channelRecentMsgReq.ChannelType) // 获取频道的领导节点
		if err != nil {
			s.Warn("getRecentMessagesForCluster: 获取频道所在节点失败！", zap.Error(err), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelRecentMsgReq.ChannelType))
			continue
		}
		if !s.cluster.NodeIsOnline(leaderInfo.Id) { // 如果领导节点不在线，则使用能触发选举的方法
			leaderInfo, err = s.cluster.SlotLeaderOfChannel(fakeChannelId, channelRecentMsgReq.ChannelType)
			if err != nil {
				s.Error("getRecentMessagesForCluster: SlotLeaderOfChannel获取频道所在节点失败！", zap.Error(err), zap.String("channelId", fakeChannelId), zap.Uint8("channelType", channelRecentMsgReq.ChannelType))
				return nil, err
			}
		}

		peerChannelRecentMessageReqsMap[leaderInfo.Id] = append(peerChannelRecentMessageReqsMap[leaderInfo.Id], channelRecentMsgReq)
	}

	// 请求远程的消息列表
	if len(peerChannelRecentMessageReqsMap) > 0 {
		var reqErr error
		wg := &sync.WaitGroup{}
		for nodeId, peerChannelRecentMessageReqs := range peerChannelRecentMessageReqsMap {
			if nodeId == s.opts.Cluster.NodeId { // 本机节点忽略
				continue
			}
			wg.Add(1)
			go func(pID uint64, reqs []*channelRecentMessageReq, uidStr string, msgCt int) {
				results, err := s.requestSyncMessage(pID, reqs, uidStr, msgCt, orderByLast)
				if err != nil {
					s.Error("请求同步消息失败！", zap.Error(err))
					reqErr = err
				} else {
					channelRecentMessages = append(channelRecentMessages, results...)
				}
				wg.Done()
			}(nodeId, peerChannelRecentMessageReqs, uid, msgCount)
		}
		wg.Wait()
		if reqErr != nil {
			s.Error("请求同步消息失败！!", zap.Error(err))
			return nil, reqErr
		}
	}

	// 请求本地的最近消息列表
	localPeerChannelRecentMessageReqs := peerChannelRecentMessageReqsMap[s.opts.Cluster.NodeId]
	if len(localPeerChannelRecentMessageReqs) > 0 {
		results, err := s.getRecentMessages(uid, msgCount, localPeerChannelRecentMessageReqs, orderByLast)
		if err != nil {
			return nil, err
		}
		channelRecentMessages = append(channelRecentMessages, results...)
	}
	return channelRecentMessages, nil
}

func (s *Server) requestSyncMessage(nodeID uint64, reqs []*channelRecentMessageReq, uid string, msgCount int, orderByLast bool) ([]*channelRecentMessage, error) {

	nodeInfo, err := s.cluster.NodeInfoById(nodeID) // 获取频道的领导节点
	if err != nil {
		s.Error("通过节点ID获取节点失败！", zap.Uint64("nodeID", nodeID))
		return nil, err
	}
	reqURL := fmt.Sprintf("%s/%s", nodeInfo.ApiServerAddr, "conversation/syncMessages")
	request := rest.Request{
		Method:  rest.Method("POST"),
		BaseURL: reqURL,
		Body: []byte(wkutil.ToJSON(map[string]interface{}{
			"uid":           uid,
			"msg_count":     msgCount,
			"channels":      reqs,
			"order_by_last": wkutil.BoolToInt(orderByLast),
		})),
	}
	s.Debug("同步会话消息!", zap.String("apiURL", reqURL), zap.String("uid", uid), zap.Any("channels", reqs))
	resp, err := rest.API(request)
	if err != nil {
		return nil, err
	}
	if err := handlerIMError(resp); err != nil {
		return nil, err
	}
	var results []*channelRecentMessage
	if err := wkutil.ReadJSONByByte([]byte(resp.Body), &results); err != nil {
		return nil, err
	}
	return results, nil
}

// getRecentMessages 获取频道最近消息
// orderByLast: true 按照最新的消息排序 false 按照最旧的消息排序
func (s *Server) getRecentMessages(uid string, msgCount int, channels []*channelRecentMessageReq, orderByLast bool) ([]*channelRecentMessage, error) {
	channelRecentMessages := make([]*channelRecentMessage, 0)
	if len(channels) > 0 {
		var (
			recentMessages []wkdb.Message
			err            error
		)
		for _, channel := range channels {
			fakeChannelID := channel.ChannelId
			msgSeq := channel.LastMsgSeq
			messageResps := MessageRespSlice{}
			if orderByLast {

				if msgSeq > 0 {
					msgSeq = msgSeq - 1 // 这里减1的目的是为了获取到最后一条消息
				}

				recentMessages, err = s.store.LoadLastMsgsWithEnd(fakeChannelID, channel.ChannelType, msgSeq, msgCount)
				if err != nil {
					s.Error("查询最近消息失败！", zap.Error(err), zap.String("uid", uid), zap.String("fakeChannelID", fakeChannelID), zap.Uint8("channelType", channel.ChannelType), zap.Uint64("LastMsgSeq", channel.LastMsgSeq))
					return nil, err
				}
				if len(recentMessages) > 0 {
					for _, recentMessage := range recentMessages {
						messageResp := &MessageResp{}
						messageResp.from(recentMessage, s)
						messageResps = append(messageResps, messageResp)
					}
				}
				sort.Sort(sort.Reverse(messageResps))
			} else {
				recentMessages, err = s.store.LoadNextRangeMsgs(fakeChannelID, channel.ChannelType, msgSeq, 0, msgCount)
				if err != nil {
					s.Error("查询最近消息失败！", zap.Error(err), zap.String("uid", uid), zap.String("fakeChannelID", fakeChannelID), zap.Uint8("channelType", channel.ChannelType), zap.Uint64("LastMsgSeq", channel.LastMsgSeq))
					return nil, err
				}
				if len(recentMessages) > 0 {
					for _, recentMessage := range recentMessages {
						messageResp := &MessageResp{}
						messageResp.from(recentMessage, s)
						messageResps = append(messageResps, messageResp)
					}
				}
			}

			channelRecentMessages = append(channelRecentMessages, &channelRecentMessage{
				ChannelId:   channel.ChannelId,
				ChannelType: channel.ChannelType,
				Messages:    messageResps,
			})
		}
	}
	return channelRecentMessages, nil
}
