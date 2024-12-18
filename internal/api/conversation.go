package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"go.uber.org/zap"
)

// conversation conversation
type conversation struct {
	s *Server
	wklog.Log
}

func newConversation(s *Server) *conversation {
	return &conversation{
		s:   s,
		Log: wklog.NewWKLog("conversation"),
	}
}

// Route 路由
func (s *conversation) route(r *wkhttp.WKHttp) {
	// r.GET("/conversations", s.conversationsList)                    // 获取会话列表 （此接口作废，使用/conversation/sync）
	r.POST("/conversations/clearUnread", s.clearConversationUnread) // 清空会话未读数量
	r.POST("/conversations/setUnread", s.setConversationUnread)     // 设置会话未读数量
	r.POST("/conversations/delete", s.deleteConversation)           // 删除会话
	r.POST("/conversation/sync", s.syncUserConversation)            // 同步会话
	r.POST("/conversation/syncMessages", s.syncRecentMessages)      // 同步会话最近消息
}

// // Get a list of recent conversations
// func (s *conversation) conversationsList(c *wkhttp.Context) {
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
// 			message, err := service.Store.LoadMsg(fakeChannelID, conversation.ChannelType, conversation.LastMsgSeq)
// 			if err != nil {
// 				s.Error("Failed to query recent news", zap.Error(err))
// 				c.ResponseError(err)
// 				return
// 			}
// 			messageResp := &MessageResp{}
// 			if message != nil {
// 				messageResp.from(message.(*Message), service.Store)
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
func (s *conversation) clearConversationUnread(c *wkhttp.Context) {
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

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
	if err != nil {
		s.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		s.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	fakeChannelId := req.ChannelID
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.UID, req.ChannelID)

	}

	conversation, err := service.Store.GetConversation(req.UID, fakeChannelId, req.ChannelType)
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
	msgSeq, err := service.Store.GetLastMsgSeq(fakeChannelId, req.ChannelType)
	if err != nil {
		s.Error("Failed to query last message", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if conversation.ReadToMsgSeq < msgSeq {
		conversation.ReadToMsgSeq = msgSeq

	}

	err = service.Store.AddOrUpdateUserConversations(req.UID, []wkdb.Conversation{conversation})
	if err != nil {
		s.Error("Failed to add conversation", zap.Error(err))
		c.ResponseError(err)
		return
	}

	service.ConversationManager.DeleteFromCache(req.UID, fakeChannelId, req.ChannelType)

	c.ResponseOK()
}

func (s *conversation) setConversationUnread(c *wkhttp.Context) {
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

	if options.G.ClusterOn() {
		leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
		if err != nil {
			s.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
		if !leaderIsSelf {
			s.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}

	fakeChannelId := req.ChannelID
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.UID, req.ChannelID)

	}
	// 获取此频道最新的消息
	msgSeq, err := service.Store.GetLastMsgSeq(fakeChannelId, req.ChannelType)
	if err != nil {
		s.Error("Failed to query last message", zap.Error(err))
		c.ResponseError(err)
		return
	}

	conversation, err := service.Store.GetConversation(req.UID, fakeChannelId, req.ChannelType)
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

	err = service.Store.AddOrUpdateUserConversations(req.UID, []wkdb.Conversation{conversation})
	if err != nil {
		s.Error("Failed to add conversation", zap.Error(err))
		c.ResponseError(err)
		return
	}

	service.ConversationManager.DeleteFromCache(req.UID, fakeChannelId, req.ChannelType)

	c.ResponseOK()
}

func (s *conversation) deleteConversation(c *wkhttp.Context) {
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

	if options.G.ClusterOn() {
		leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
		if err != nil {
			s.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId

		if !leaderIsSelf {
			s.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}
	fakeChannelId := req.ChannelID
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.UID, req.ChannelID)

	}

	err = service.Store.DeleteConversation(req.UID, fakeChannelId, req.ChannelType)
	if err != nil {
		s.Error("删除会话！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	service.ConversationManager.DeleteFromCache(req.UID, fakeChannelId, req.ChannelType)

	c.ResponseOK()
}

func (s *conversation) syncUserConversation(c *wkhttp.Context) {
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

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
	if err != nil {
		s.Error("获取频道所在节点失败！!", zap.Error(err), zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId

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
	conversations, err := service.Store.GetLastConversations(req.UID, wkdb.ConversationTypeChat, 0, options.G.Conversation.UserMaxCount)
	if err != nil && err != wkdb.ErrNotFound {
		s.Error("获取conversation失败！", zap.Error(err), zap.String("uid", req.UID))
		c.ResponseError(errors.New("获取conversation失败！"))
		return
	}

	// 获取用户缓存的最近会话
	cacheConversations := service.ConversationManager.GetFromCache(req.UID, wkdb.ConversationTypeChat)

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
			from, to := options.GetFromUIDAndToUIDWith(fakeChannelId)
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
		channelRecentMessages, err = s.s.requset.getRecentMessagesForCluster(req.UID, int(req.MsgCount), channelRecentMessageReqs, true)
		if err != nil {
			s.Error("获取最近消息失败！", zap.Error(err), zap.String("uid", req.UID))
			c.ResponseError(errors.New("获取最近消息失败！"))
			return
		}

		for i := 0; i < len(conversations); i++ {
			conversation := conversations[i]
			realChannelId := getRealChannelId(conversation.ChannelId, conversation.ChannelType)
			if conversation.ChannelType == wkproto.ChannelTypePerson && realChannelId == options.G.SystemUID { // 系统消息不返回
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

func (s *conversation) getChannelLastMsgSeqMap(lastMsgSeqs string) map[string]uint64 {
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

func (s *conversation) syncRecentMessages(c *wkhttp.Context) {
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
	channelRecentMessages, err := s.s.requset.getRecentMessages(req.UID, msgCount, req.Channels, wkutil.IntToBool(req.OrderByLast))
	if err != nil {
		s.Error("获取最近消息失败！", zap.Error(err))
		c.ResponseError(errors.New("获取最近消息失败！"))
		return
	}
	c.JSON(http.StatusOK, channelRecentMessages)
}
