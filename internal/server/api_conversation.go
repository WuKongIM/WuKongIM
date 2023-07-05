package server

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
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
	r.GET("/conversations", s.conversationsList)                    // 获取会话列表
	r.POST("/conversations/clearUnread", s.clearConversationUnread) // 清空会话未读数量
	r.POST("/conversations/setUnread", s.setConversationUnread)     // 设置会话未读数量
	r.POST("/conversations/delete", s.deleteConversation)           // 删除会话
	r.POST("/conversation/sync", s.syncUserConversation)            // 同步会话
	r.POST("/conversation/syncMessages", s.syncRecentMessages)      // 同步会话最近消息
}

// Get a list of recent conversations
func (s *ConversationAPI) conversationsList(c *wkhttp.Context) {
	uid := c.Query("uid")
	if strings.TrimSpace(uid) == "" {
		c.ResponseError(errors.New("uid cannot be empty"))
		return
	}
	conversations := s.s.conversationManager.GetConversations(uid, 0, nil)
	conversationResps := make([]conversationResp, 0)
	if len(conversations) > 0 {
		for _, conversation := range conversations {
			fakeChannelID := conversation.ChannelID
			if conversation.ChannelType == wkproto.ChannelTypePerson {
				fakeChannelID = GetFakeChannelIDWith(uid, conversation.ChannelID)
			}
			// 获取到偏移位内的指定最大条数的最新消息
			message, err := s.s.store.LoadMsg(fakeChannelID, conversation.ChannelType, conversation.LastMsgSeq)
			if err != nil {
				s.Error("Failed to query recent news", zap.Error(err))
				c.ResponseError(err)
				return
			}
			messageResp := &MessageResp{}
			if message != nil {
				messageResp.from(message.(*Message), s.s.store)
			}
			conversationResps = append(conversationResps, conversationResp{
				ChannelID:   conversation.ChannelID,
				ChannelType: conversation.ChannelType,
				Unread:      conversation.UnreadCount,
				Timestamp:   conversation.Timestamp,
				LastMessage: messageResp,
			})
		}
	}
	c.JSON(http.StatusOK, conversationResps)
}

// 清楚会话未读数量
func (s *ConversationAPI) clearConversationUnread(c *wkhttp.Context) {
	var req clearConversationUnreadReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
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
	if err := c.BindJSON(&req); err != nil {
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
	if err := c.BindJSON(&req); err != nil {
		s.Error("Data Format", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	// 删除最近会话
	err := s.s.conversationManager.DeleteConversation([]string{req.UID}, req.ChannelID, req.ChannelType)
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
	if err := c.BindJSON(&req); err != nil {
		s.Error("格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	fmt.Println("syncUserConversation--->", req.UID, req.Version, req.LastMsgSeqs, req.MsgCount, req.Larges)
	// msgCount := req.MsgCount
	// if msgCount == 0 {
	// 	msgCount = 100
	// }

	channelLastMsgSeqStrList := strings.Split(req.LastMsgSeqs, "|")
	channelRecentMessageReqs := make([]*channelRecentMessageReq, 0, len(channelLastMsgSeqStrList))
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

	conversations := s.s.conversationManager.GetConversations(req.UID, req.Version, req.Larges)
	fmt.Println("conversations---->", len(conversations))
	var newConversations = make([]*wkstore.Conversation, 0, len(conversations)+20)
	if conversations != nil {
		newConversations = append(newConversations, conversations...)
	}

	if len(req.Larges) > 0 && req.MsgCount > 0 {
		for _, largeChannel := range req.Larges {
			var existConversation *wkstore.Conversation
			for _, cs := range conversations {
				if cs.ChannelID == largeChannel.ChannelID && cs.ChannelType == largeChannel.ChannelType {
					existConversation = cs
					break
				}
			}
			lastMessages, err := s.s.store.LoadLastMsgs(largeChannel.ChannelID, largeChannel.ChannelType, 1)
			if err != nil {
				s.Error("查询大群最后一条消息失败！", zap.Error(err))
				c.ResponseError(errors.New("查询大群最后一条消息失败！"))
				return
			}
			var lastMessage *Message
			if len(lastMessages) > 0 {
				lastMessage = lastMessages[len(lastMessages)-1].(*Message)
			}
			if existConversation != nil {

				if lastMessage != nil {
					existConversation.Timestamp = int64(lastMessage.Timestamp)
					existConversation.LastMsgSeq = lastMessage.MessageSeq
					existConversation.LastClientMsgNo = lastMessage.ClientMsgNo
					existConversation.LastMsgID = lastMessage.MessageID
					if lastMessage.MessageSeq > existConversation.LastMsgSeq {
						existConversation.UnreadCount = int(lastMessage.MessageSeq - existConversation.LastMsgSeq)
					}
				}

			} else {
				if lastMessage != nil {
					newConversations = append(newConversations, &wkstore.Conversation{
						UID:             req.UID,
						ChannelID:       largeChannel.ChannelID,
						ChannelType:     largeChannel.ChannelType,
						UnreadCount:     0, // TODO: 这里未读数量没办法计算
						Timestamp:       int64(lastMessage.Timestamp),
						LastMsgSeq:      lastMessage.MessageSeq,
						LastClientMsgNo: lastMessage.ClientMsgNo,
						LastMsgID:       lastMessage.MessageID,
					})
				}

			}
		}
	}

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
	if req.MsgCount > 0 {
		channelRecentMessages, err := s.getRecentMessages(req.UID, int(req.MsgCount), channelRecentMessageReqs)
		if err != nil {
			s.Error("获取最近消息失败！", zap.Error(err), zap.String("uid", req.UID))
			c.ResponseError(errors.New("获取最近消息失败！"))
			return
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
	channelRecentMessages, err := s.getRecentMessages(req.UID, req.MsgCount, req.Channels)
	if err != nil {
		s.Error("获取最近消息失败！", zap.Error(err))
		c.ResponseError(errors.New("获取最近消息失败！"))
		return
	}
	c.JSON(http.StatusOK, channelRecentMessages)
}

func (s *ConversationAPI) getRecentMessages(uid string, msgCount int, channels []*channelRecentMessageReq) ([]*channelRecentMessage, error) {
	fmt.Println("getRecentMessages-->", uid, msgCount, channels)
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
