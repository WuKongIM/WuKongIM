package server

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
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

	conversation := s.s.conversationManager.GetConversation(req.UID, req.ChannelID, req.ChannelType)
	if conversation == nil && req.MessageSeq > 0 {
		conversation = &wkstore.Conversation{
			UID:         req.UID,
			ChannelID:   req.ChannelID,
			ChannelType: req.ChannelType,
			LastMsgSeq:  req.MessageSeq,
		}
		s.s.conversationManager.AddOrUpdateConversation(req.UID, conversation)
	} else {
		err := s.s.conversationManager.SetConversationUnread(req.UID, req.ChannelID, req.ChannelType, 0, req.MessageSeq)
		if err != nil {
			c.ResponseError(err)
			return
		}
	}
	c.ResponseOK()
}

func (s *ConversationAPI) setConversationUnread(c *wkhttp.Context) {
	var req struct {
		UID         string `json:"uid"`
		ChannelID   string `json:"channel_id"`
		ChannelType uint8  `json:"channel_type"`
		Unread      int    `json:"unread"`
		MessageSeq  uint32 `json:"message_seq"` // messageSeq 只有超大群才会传 因为超大群最近会话服务器不会维护，需要客户端传递messageSeq进行主动维护
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

	conversation := s.s.conversationManager.GetConversation(req.UID, req.ChannelID, req.ChannelType)
	if conversation == nil && req.MessageSeq > 0 && req.Unread == 0 {
		conversation = &wkstore.Conversation{
			UID:         req.UID,
			ChannelID:   req.ChannelID,
			ChannelType: req.ChannelType,
			UnreadCount: 0,
			LastMsgSeq:  req.MessageSeq,
		}
		s.s.conversationManager.AddOrUpdateConversation(req.UID, conversation)
	} else {
		err := s.s.conversationManager.SetConversationUnread(req.UID, req.ChannelID, req.ChannelType, req.Unread, req.MessageSeq)
		if err != nil {
			c.ResponseError(err)
			return
		}
	}

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

	// 删除最近会话
	err = s.s.conversationManager.DeleteConversation([]string{req.UID}, req.ChannelID, req.ChannelType)
	if err != nil {
		s.Error("删除最近会话！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	c.ResponseOK()
}

func (s *ConversationAPI) syncUserConversation(c *wkhttp.Context) {
	var req struct {
		UID         string             `json:"uid"`
		Version     int64              `json:"version"`       // 当前客户端的会话最大版本号(客户端最新会话的时间戳)
		LastMsgSeqs string             `json:"last_msg_seqs"` // 客户端所有会话的最后一条消息序列号 格式： channelID:channelType:last_msg_seq|channelID:channelType:last_msg_seq
		MsgCount    int64              `json:"msg_count"`     // 每个会话消息数量
		Larges      []*wkproto.Channel `json:"larges"`        // 超大频道集合
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		s.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if s.s.opts.ClusterOn() {
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
	}

	var (
		channelLastMsgMap        = s.getChannelLastMsgSeqMap(req.LastMsgSeqs) // 获取频道对应的最后一条消息的messageSeq
		channelRecentMessageReqs = make([]*channelRecentMessageReq, 0, len(channelLastMsgMap))
	)

	// 获取用户最近会话基础数据

	conversations := s.s.conversationManager.GetConversations(req.UID, req.Version, req.Larges)
	var newConversations = make([]*wkstore.Conversation, 0, len(conversations)+20)
	if conversations != nil {
		newConversations = append(newConversations, conversations...)
	}

	// ==================== 获取大群频道的最近会话 ====================
	if len(req.Larges) > 0 && req.MsgCount > 0 {
		for _, largeChannel := range req.Larges {
			var existConversation *wkstore.Conversation
			for _, cs := range conversations {
				if cs.ChannelID == largeChannel.ChannelID && cs.ChannelType == largeChannel.ChannelType {
					existConversation = cs
					break
				}
			}
			if existConversation == nil {
				newConversations = append(newConversations, &wkstore.Conversation{
					UID:         req.UID,
					ChannelID:   largeChannel.ChannelID,
					ChannelType: largeChannel.ChannelType,
					UnreadCount: 0, // 超大群未读数，服务器不做计算
				})

			}
		}
	}

	// ==================== 返回最近会话的数据组合 ====================
	resps := make([]*syncUserConversationResp, 0, len(newConversations))
	if len(newConversations) > 0 {
		for _, conversation := range newConversations {
			syncUserConversationR := newSyncUserConversationResp(conversation)
			resps = append(resps, syncUserConversationR)

			msgSeq := channelLastMsgMap[fmt.Sprintf("%s-%d", conversation.ChannelID, conversation.ChannelType)]
			channelRecentMessageReqs = append(channelRecentMessageReqs, &channelRecentMessageReq{
				ChannelID:   conversation.ChannelID,
				ChannelType: conversation.ChannelType,
				LastMsgSeq:  msgSeq,
			})
		}
	}

	// ==================== 获取最近会话的最近的消息列表 ====================
	if req.MsgCount > 0 {
		var channelRecentMessages []*channelRecentMessage
		if s.s.opts.ClusterOn() {
			channelRecentMessages, err = s.getRecentMessagesForCluster(req.UID, int(req.MsgCount), channelRecentMessageReqs)
			if err != nil {
				s.Error("获取最近消息失败！", zap.Error(err), zap.String("uid", req.UID))
				c.ResponseError(errors.New("获取最近消息失败！"))
				return
			}
		} else {
			channelRecentMessages, err = s.getRecentMessages(req.UID, int(req.MsgCount), channelRecentMessageReqs)
			if err != nil {
				s.Error("获取最近消息失败！", zap.Error(err), zap.String("uid", req.UID))
				c.ResponseError(errors.New("获取最近消息失败！"))
				return
			}
		}

		if len(channelRecentMessages) > 0 {
			for i := 0; i < len(resps); i++ {
				resp := resps[i]
				for _, channelRecentMessage := range channelRecentMessages {
					if resp.ChannelID == channelRecentMessage.ChannelID && resp.ChannelType == channelRecentMessage.ChannelType {
						resp.Recents = channelRecentMessage.Messages
					}
				}
			}
		}
	}
	c.JSON(http.StatusOK, resps)
}

func (s *ConversationAPI) getChannelLastMsgSeqMap(lastMsgSeqs string) map[string]uint32 {
	channelLastMsgSeqStrList := strings.Split(lastMsgSeqs, "|")
	channelLastMsgMap := map[string]uint32{} // 频道对应的messageSeq
	for _, channelLastMsgSeqStr := range channelLastMsgSeqStrList {
		channelLastMsgSeqs := strings.Split(channelLastMsgSeqStr, ":")
		if len(channelLastMsgSeqs) != 3 {
			continue
		}
		channelID := channelLastMsgSeqs[0]
		channelTypeI, _ := strconv.Atoi(channelLastMsgSeqs[1])
		lastMsgSeq, _ := strconv.ParseUint(channelLastMsgSeqs[2], 10, 64)
		channelLastMsgMap[fmt.Sprintf("%s-%d", channelID, channelTypeI)] = uint32(lastMsgSeq)
	}
	return channelLastMsgMap
}

func (s *ConversationAPI) syncRecentMessages(c *wkhttp.Context) {
	var req struct {
		UID      string                     `json:"uid"`
		Channels []*channelRecentMessageReq `json:"channels"`
		MsgCount int                        `json:"msg_count"`
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
	channelRecentMessages, err := s.getRecentMessages(req.UID, msgCount, req.Channels)
	if err != nil {
		s.Error("获取最近消息失败！", zap.Error(err))
		c.ResponseError(errors.New("获取最近消息失败！"))
		return
	}
	c.JSON(http.StatusOK, channelRecentMessages)
}

func (s *ConversationAPI) getRecentMessagesForCluster(uid string, msgCount int, channels []*channelRecentMessageReq) ([]*channelRecentMessage, error) {
	if len(channels) == 0 {
		return nil, nil
	}
	channelRecentMessages := make([]*channelRecentMessage, 0)
	var (
		err error
	)
	localPeerChannelRecentMessageReqs := make([]*channelRecentMessageReq, 0)
	peerChannelRecentMessageReqsMap := make(map[uint64][]*channelRecentMessageReq)
	for _, channelRecentMsgReq := range channels {
		fakeChannelID := channelRecentMsgReq.ChannelID
		if channelRecentMsgReq.ChannelType == wkproto.ChannelTypePerson {
			fakeChannelID = GetFakeChannelIDWith(uid, channelRecentMsgReq.ChannelID)
		}
		leaderInfo, err := s.s.cluster.LeaderOfChannelForRead(fakeChannelID, channelRecentMsgReq.ChannelType) // 获取频道的领导节点
		if err != nil {
			s.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", fakeChannelID), zap.Uint8("channelType", channelRecentMsgReq.ChannelType))
			return nil, err
		}
		leaderIsSelf := leaderInfo.Id == s.s.opts.Cluster.NodeId
		if leaderIsSelf {
			localPeerChannelRecentMessageReqs = append(localPeerChannelRecentMessageReqs, channelRecentMsgReq)
		} else {
			peerChannelRecentMessageReqs := peerChannelRecentMessageReqsMap[leaderInfo.Id]
			if peerChannelRecentMessageReqs == nil {
				peerChannelRecentMessageReqs = make([]*channelRecentMessageReq, 0)
			}
			peerChannelRecentMessageReqs = append(peerChannelRecentMessageReqs, channelRecentMsgReq)
			peerChannelRecentMessageReqsMap[leaderInfo.Id] = peerChannelRecentMessageReqs
		}

	}

	// 请求远程的消息列表
	if len(peerChannelRecentMessageReqsMap) > 0 {
		var reqErr error
		wg := &sync.WaitGroup{}
		for nodeId, peerChannelRecentMessageReqs := range peerChannelRecentMessageReqsMap {
			wg.Add(1)
			go func(pID uint64, reqs []*channelRecentMessageReq, uidStr string, msgCt int) {
				results, err := s.requestSyncMessage(pID, reqs, uidStr, msgCt)
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
	if len(localPeerChannelRecentMessageReqs) > 0 {
		results, err := s.getRecentMessages(uid, msgCount, localPeerChannelRecentMessageReqs)
		if err != nil {
			return nil, err
		}
		channelRecentMessages = append(channelRecentMessages, results...)
	}
	return channelRecentMessages, nil
}

func (s *ConversationAPI) requestSyncMessage(nodeID uint64, reqs []*channelRecentMessageReq, uid string, msgCount int) ([]*channelRecentMessage, error) {

	nodeInfo, err := s.s.cluster.NodeInfoByID(nodeID) // 获取频道的领导节点
	if err != nil {
		s.Error("通过节点ID获取节点失败！", zap.Uint64("nodeID", nodeID))
		return nil, err
	}
	reqURL := fmt.Sprintf("%s/%s", nodeInfo.ApiServerAddr, "conversation/syncMessages")
	request := rest.Request{
		Method:  rest.Method("POST"),
		BaseURL: reqURL,
		Body: []byte(wkutil.ToJSON(map[string]interface{}{
			"uid":       uid,
			"msg_count": msgCount,
			"channels":  reqs,
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

func (s *ConversationAPI) getRecentMessages(uid string, msgCount int, channels []*channelRecentMessageReq) ([]*channelRecentMessage, error) {
	channelRecentMessages := make([]*channelRecentMessage, 0)
	if len(channels) > 0 {
		var (
			recentMessages []wkstore.Message
			err            error
		)
		for _, channel := range channels {
			fakeChannelID := channel.ChannelID
			if channel.ChannelType == wkproto.ChannelTypePerson {
				fakeChannelID = GetFakeChannelIDWith(uid, channel.ChannelID)
			}
			recentMessages, err = s.s.store.LoadLastMsgsWithEnd(fakeChannelID, channel.ChannelType, channel.LastMsgSeq, msgCount)
			if err != nil {
				s.Error("查询最近消息失败！", zap.Error(err), zap.String("uid", uid), zap.String("fakeChannelID", fakeChannelID), zap.Uint8("channelType", channel.ChannelType), zap.Uint32("LastMsgSeq", channel.LastMsgSeq))
				return nil, err
			}
			messageResps := MessageRespSlice{}
			if len(recentMessages) > 0 {
				for _, recentMessage := range recentMessages {
					messageResp := &MessageResp{}
					messageResp.from(recentMessage.(*Message), s.s.store)
					messageResps = append(messageResps, messageResp)
				}
			}
			sort.Sort(sort.Reverse(messageResps))
			channelRecentMessages = append(channelRecentMessages, &channelRecentMessage{
				ChannelID:   channel.ChannelID,
				ChannelType: channel.ChannelType,
				Messages:    messageResps,
			})
		}
	}
	return channelRecentMessages, nil
}
