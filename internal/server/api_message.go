package server

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// MessageAPI MessageAPI
type MessageAPI struct {
	s *Server
	wklog.Log
}

// NewMessageAPI NewMessageAPI
func NewMessageAPI(s *Server) *MessageAPI {
	return &MessageAPI{
		s:   s,
		Log: wklog.NewWKLog("MessageApi"),
	}
}

// Route route
func (m *MessageAPI) Route(r *wkhttp.WKHttp) {
	r.POST("/message/send", m.send)           // 发送消息
	r.POST("/message/sendbatch", m.sendBatch) // 批量发送消息
	r.POST("/message/sync", m.sync)           // 消息同步(写模式)
	r.POST("/message/syncack", m.syncack)     // 消息同步回执(写模式)

	r.POST("/streammessage/start", m.streamMessageStart) // 流消息开始
	r.POST("/streammessage/end", m.streamMessageEnd)     // 流消息结束

}

// 消息同步
func (m *MessageAPI) sync(c *wkhttp.Context) {
	var req syncReq
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	readedMessageSeq, err := m.s.store.GetMessageOfUserCursor(req.UID) // 获取当前用户已读消息seq
	if err != nil {
		c.ResponseError(err)
		return
	}
	sartSeq := req.MessageSeq
	if sartSeq <= 0 {
		sartSeq = readedMessageSeq
	} else if sartSeq < readedMessageSeq {
		sartSeq = req.MessageSeq
	} else {
		sartSeq = readedMessageSeq
	}
	sartSeq = sartSeq + 1 // 从已读消息的下一条开始同步

	messages, err := m.s.store.SyncMessageOfUser(req.UID, sartSeq, req.Limit)
	if err != nil {
		m.Error("同步消息失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	resps := make([]*MessageResp, 0, len(messages))
	if len(messages) > 0 {
		for _, message := range messages {
			resp := &MessageResp{}
			resp.from(message.(*Message), m.s.store)
			resps = append(resps, resp)
		}
	}
	c.JSON(http.StatusOK, resps)
}

// 同步回执
func (m *MessageAPI) syncack(c *wkhttp.Context) {
	var req syncackReq
	if err := c.BindJSON(&req); err != nil {
		m.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(errors.New("数据格式有误！"))
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}
	err := m.s.store.UpdateMessageOfUserCursorIfNeed(req.UID, req.LastMessageSeq)
	if err != nil {
		c.ResponseError(err)
		return
	}
	c.ResponseOK()

}

// TODO: 这个批量接口比较慢 需要优化
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
		_, _, err := m.sendMessageToChannel(MessageSendReq{
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

	channelID := req.ChannelID
	channelType := req.ChannelType
	if strings.TrimSpace(channelID) == "" && len(req.Subscribers) > 0 { //如果没频道ID 但是有订阅者，则创建一个临时频道
		channelID = fmt.Sprintf("%s%s", wkutil.GenUUID(), m.s.opts.TmpChannel.Suffix)
		channelType = wkproto.ChannelTypeGroup
		m.s.channelManager.CreateTmpChannel(channelID, channelType, req.Subscribers)
	}
	m.Debug("发送消息内容：", zap.String("msg", wkutil.ToJSON(req)))
	if strings.TrimSpace(channelID) == "" { //指定了频道 正常发送
		m.Error("无法处理发送消息请求！", zap.Any("req", req))
		c.ResponseError(errors.New("无法处理发送消息请求！"))
		return
	}
	clientMsgNo := req.ClientMsgNo
	if strings.TrimSpace(clientMsgNo) == "" {
		clientMsgNo = fmt.Sprintf("%s0", wkutil.GenUUID())
	}

	// 发送消息
	messageID, messageSeq, err := m.sendMessageToChannel(req, channelID, channelType, clientMsgNo, wkproto.StreamFlagIng)
	if err != nil {
		c.ResponseError(err)
		return
	}
	c.ResponseOKWithData(map[string]interface{}{
		"message_id":    messageID,
		"client_msg_no": clientMsgNo,
		"message_seq":   messageSeq,
	})
}

func (m *MessageAPI) sendMessageToChannel(req MessageSendReq, channelID string, channelType uint8, clientMsgNo string, streamFlag wkproto.StreamFlag) (int64, uint32, error) {

	m.s.monitor.SendPacketInc(req.Header.NoPersist != 1)
	m.s.monitor.SendSystemMsgInc()

	var messageID = m.s.dispatch.processor.genMessageID()

	fakeChannelID := channelID
	if channelType == wkproto.ChannelTypePerson && req.FromUID != "" {
		fakeChannelID = GetFakeChannelIDWith(req.FromUID, channelID)
	}

	// 获取频道
	channel, err := m.s.channelManager.GetChannel(fakeChannelID, channelType)
	if err != nil {
		m.Error("查询频道信息失败！", zap.Error(err))
		return 0, 0, errors.New("查询频道信息失败！")
	}

	if channel == nil {
		return 0, 0, errors.New("频道信息不存在！")
	}
	if channel.Large && req.Header.SyncOnce == 1 {
		m.Error("超大群不支持发送SyncOnce类型消息！", zap.String("channelID", channelID), zap.Uint8("channelType", channelType))
		return 0, 0, errors.New("超大群不支持发送SyncOnce类型消息！")
	}

	// var messageSeq uint32
	// if req.Header.NoPersist == 0 && req.Header.SyncOnce != 1 {
	// 	messageSeq, err = m.l.store.GetNextMessageSeq(fakeChannelID, channelType)
	// 	if err != nil {
	// 		m.Error("获取频道消息序列号失败！", zap.String("channelID", fakeChannelID), zap.Uint8("channelType", channelType), zap.Error(err))
	// 		return errors.New("获取频道消息序列号失败！")
	// 	}
	// }
	subscribers := req.Subscribers
	if len(subscribers) > 0 {
		subscribers = wkutil.RemoveRepeatedElement(req.Subscribers)
	}
	var setting wkproto.Setting
	if len(strings.TrimSpace(req.StreamNo)) > 0 {
		setting = setting.Set(wkproto.SettingStream)
	}

	fmt.Println("setting--->", setting.IsSet(wkproto.SettingStream))

	msg := &Message{
		RecvPacket: &wkproto.RecvPacket{
			Framer: wkproto.Framer{
				RedDot:    wkutil.IntToBool(req.Header.RedDot),
				SyncOnce:  wkutil.IntToBool(req.Header.SyncOnce),
				NoPersist: wkutil.IntToBool(req.Header.NoPersist),
			},
			Setting:     setting,
			MessageID:   messageID,
			ClientMsgNo: clientMsgNo,
			StreamNo:    req.StreamNo,
			StreamFlag:  streamFlag,
			FromUID:     req.FromUID,
			ChannelID:   channelID,
			ChannelType: channelType,
			Timestamp:   int32(time.Now().Unix()),
			Payload:     req.Payload,
		},
		fromDeviceFlag: wkproto.SYSTEM,
		Subscribers:    subscribers,
	}
	messages := []wkstore.Message{msg}
	if !msg.NoPersist && !msg.SyncOnce && !m.s.opts.IsTmpChannel(channelID) {

		if msg.StreamIng() {
			streamSeq, err := m.s.store.AppendStreamItem(fakeChannelID, channelType, msg.StreamNo, &wkstore.StreamItem{
				ClientMsgNo: msg.ClientMsgNo,
				Blob:        msg.Payload,
			})
			if err != nil {
				m.Error("Failed to save stream message", zap.Error(err))
				return 0, 0, errors.New("failed to save stream message")
			}
			msg.StreamSeq = streamSeq // stream seq
		} else {
			_, err = m.s.store.AppendMessages(fakeChannelID, channelType, messages)
			if err != nil {
				m.Error("Failed to save history message", zap.Error(err))
				return 0, 0, errors.New("failed to save history message")
			}
		}

	}
	if m.s.opts.WebhookOn() {
		if !msg.StreamIng() {
			// Add a message to the notification queue, the data in this queue will be notified to third-party applications
			err = m.s.store.AppendMessageOfNotifyQueue(messages)
			if err != nil {
				m.Error("添加消息到通知队列失败！", zap.Error(err))
				return 0, 0, errors.New("添加消息到通知队列失败！")
			}
		}
	}
	// 将消息放入频道
	err = channel.Put([]*Message{msg}, msg.Subscribers, req.FromUID, wkproto.DeviceFlag(wkproto.DeviceLevelMaster), "system")
	if err != nil {
		m.Error("将消息放入频道内失败！", zap.Error(err))
		return 0, 0, errors.New("将消息放入频道内失败！")
	}
	return messageID, msg.MessageSeq, nil
}

func (m *MessageAPI) streamMessageStart(c *wkhttp.Context) {
	var req messageStreamStartReq
	if err := c.BindJSON(&req); err != nil {
		m.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	channelID := req.ChannelID
	channelType := req.ChannelType

	m.Debug("消息流开始", zap.String("msg", wkutil.ToJSON(req)))

	if strings.TrimSpace(channelID) == "" { //指定了频道 正常发送
		m.Error("无法处理发送消息请求！", zap.Any("req", req))
		c.ResponseError(errors.New("无法处理发送消息请求！"))
		return
	}
	clientMsgNo := req.ClientMsgNo
	if strings.TrimSpace(clientMsgNo) == "" {
		clientMsgNo = fmt.Sprintf("%s0", wkutil.GenUUID())
	}

	streamNo := wkutil.GenUUID()
	streamFlag := wkproto.StreamFlagStart

	messageID, messageSeq, err := m.sendMessageToChannel(MessageSendReq{
		Header:      req.Header,
		ClientMsgNo: clientMsgNo,
		StreamNo:    streamNo,
		FromUID:     req.FromUID,
		ChannelID:   channelID,
		ChannelType: channelType,
		Payload:     req.Payload,
	}, channelID, channelType, clientMsgNo, streamFlag)

	if err != nil {
		m.Error("发送消息失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	err = m.s.store.SaveStreamMeta(&wkstore.StreamMeta{
		StreamNo:    streamNo,
		MessageID:   messageID,
		ChannelID:   channelID,
		ChannelType: channelType,
		MessageSeq:  messageSeq,
		StreamFlag:  streamFlag,
	})
	if err != nil {
		m.Error("保存流消息元数据失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"stream_no": streamNo,
	})

}

func (m *MessageAPI) streamMessageEnd(c *wkhttp.Context) {

	var req messageStreamEndReq
	if err := c.BindJSON(&req); err != nil {
		m.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	streamMeta, err := m.s.store.GetStreamMeta(req.ChannelID, req.ChannelType, req.StreamNo)
	if err != nil {
		m.Error("获取流消息元数据失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if streamMeta == nil {
		m.Error("流消息元数据不存在！", zap.Error(err), zap.String("streamNo", req.StreamNo))
		c.ResponseError(errors.New("流消息元数据不存在！"))
		return
	}

	streamMeta.StreamFlag = wkproto.StreamFlagEnd

	err = m.s.store.SaveStreamMeta(streamMeta)
	if err != nil {
		m.Error("保存流消息元数据失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	c.ResponseOK()

}
