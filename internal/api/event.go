package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/pkg/wkcache"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type event struct {
	s *Server
	wklog.Log
}

func newEvent(s *Server) *event {
	return &event{
		s:   s,
		Log: wklog.NewWKLog("event"),
	}
}

func (e *event) route(r *wkhttp.WKHttp) {

	r.POST("/event", e.send) // 发送事件

}

func (e *event) send(c *wkhttp.Context) {
	var req eventReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		c.ResponseError(errors.Wrap(err, "数据格式有误！"))
		return
	}

	force := c.Query("force") == "1"

	eventType := req.Event.Type

	if eventType == "" {
		e.Error("事件类型不能为空！", zap.Any("req", req))
		c.ResponseError(errors.New("事件类型不能为空！"))
		return
	}

	if len(req.ClientMsgNo) <= 0 {
		e.Error("client_msg_no不能为空！", zap.Any("req", req))
		c.ResponseError(errors.New("client_msg_no不能为空！"))
		return
	}

	if strings.TrimSpace(req.FromUid) == "" {
		req.FromUid = options.G.SystemUID
	}
	fakeChannelId := req.ChannelId
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.ChannelId, req.FromUid)
	}
	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(fakeChannelId, req.ChannelType) // 获取频道的槽领导节点
	if err != nil {
		e.Error("获取频道所在节点失败！", zap.Error(err), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		e.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	if eventType == types.AGUIEventTextMessageStart.String() {
		if !force {
			if service.StreamCache.HasStreamInChannel(fakeChannelId, req.ChannelType) {
				c.ResponseError(errors.New("频道中已有流正在运行！"))
				e.Error("频道中已有流正在运行！", zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", req.ChannelType))
				return
			}
		} else {
			// 强制结束其他流
			err = service.StreamCache.EndStreamInChannel(fakeChannelId, req.ChannelType, wkcache.EndReasonForce)
			if err != nil {
				c.ResponseError(err)
				e.Error("强制结束其他流失败！", zap.Error(err), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", req.ChannelType))
				return
			}
		}
		err = e.handleTextMessageStart(req)
		if err != nil {
			e.Error("处理消息开始事件失败！", zap.Error(err), zap.Any("req", req))
			c.ResponseError(err)
			return
		}
	} else if eventType == types.AGUIEventTextMessageContent.String() {
		req.Event.Id = req.ClientMsgNo // textMessageContent事件的id即为clientMsgNo
		err = e.handleTextMessageContent(req)
		if err != nil {
			e.Error("处理消息内容事件失败！", zap.Error(err), zap.Any("req", req))
			c.ResponseError(err)
			return
		}
	} else if eventType == types.AGUIEventTextMessageEnd.String() {
		err = e.handleTextMessageEnd(req)
		if err != nil {
			e.Error("处理消息结束事件失败！", zap.Error(err), zap.Any("req", req))
			c.ResponseError(err)
			return
		}
	} else {
		messageId := options.G.GenMessageId()
		err = e.sendEvent(messageId, req)
		if err != nil {
			e.Error("发送事件失败！", zap.Error(err), zap.Any("req", req))
			c.ResponseError(err)
			return
		}
	}

	c.ResponseOK()

}

func (e *event) handleTextMessageStart(req eventReq) error {
	fakeChannelId := req.ChannelId
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.ChannelId, req.FromUid)
	}

	messageId := options.G.GenMessageId()

	meta := wkcache.NewStreamMetaBuilder(req.ClientMsgNo).
		Channel(fakeChannelId, req.ChannelType).
		MessageId(messageId).
		From(req.FromUid).
		Build()
	err := service.StreamCache.OpenStream(meta)
	if err != nil {
		return err
	}

	// 发送消息
	err = e.sendStreamMessage(messageId, req)
	if err != nil {
		e.Error("发送消息失败！", zap.Error(err))
		return err
	}

	return nil
}

func (e *event) handleTextMessageContent(req eventReq) error {
	fakeChannelId := req.ChannelId
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.ChannelId, req.FromUid)
	}
	meta, err := service.StreamCache.GetStreamInfo(req.ClientMsgNo)
	if err != nil {
		e.Error("获取流信息失败！", zap.Error(err), zap.String("clientMsgNo", req.ClientMsgNo))
		return err
	}
	if meta == nil {
		e.Error("流不存在！", zap.String("clientMsgNo", req.ClientMsgNo))
		return err
	}
	if meta.Closed {
		e.Error("流已关闭！", zap.String("clientMsgNo", req.ClientMsgNo))
		return errors.New("流已关闭！")
	}
	if meta.ChannelId != fakeChannelId || meta.ChannelType != req.ChannelType {
		e.Error("流的频道不匹配！", zap.String("clientMsgNo", req.ClientMsgNo))
		return errors.New("流的频道不匹配！")
	}
	chunkId := meta.NextSeq()
	err = service.StreamCache.AppendChunk(&wkcache.MessageChunk{
		ClientMsgNo: req.ClientMsgNo,
		ChunkId:     chunkId,
		Payload:     []byte(req.Event.Data),
	})
	if err != nil {
		e.Error("追加流消息失败！", zap.Error(err), zap.String("clientMsgNo", req.ClientMsgNo))
		return err
	}

	// send event
	err = e.sendEvent(meta.MessageId, req)
	if err != nil {
		e.Error("发送流消息失败！", zap.Error(err), zap.Uint64("chunkId", chunkId), zap.String("clientMsgNo", req.ClientMsgNo))
		return err
	}
	return nil
}

func (e *event) handleTextMessageEnd(req eventReq) error {
	meta, err := service.StreamCache.GetStreamInfo(req.ClientMsgNo)
	if err != nil {
		e.Error("获取流信息失败！", zap.Error(err), zap.String("clientMsgNo", req.ClientMsgNo))
		return err
	}
	if meta == nil {
		e.Error("流不存在！", zap.String("clientMsgNo", req.ClientMsgNo))
		return errors.New("流不存在！")
	}
	if meta.Closed {
		e.Error("流已关闭！", zap.String("clientMsgNo", req.ClientMsgNo))
		return errors.New("流已关闭！")
	}
	err = service.StreamCache.EndStream(req.ClientMsgNo, wkcache.EndReasonSuccess)
	if err != nil {
		e.Error("关闭流失败！", zap.Error(err), zap.String("clientMsgNo", req.ClientMsgNo))
		return err
	}
	return nil
}

func (e *event) storeMessage(req eventReq) (wkdb.Message, error) {
	fakeChannelId := req.ChannelId
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.ChannelId, req.FromUid)
	}

	clientMsgNo := req.ClientMsgNo

	messageId := options.G.GenMessageId()

	var setting wkproto.Setting
	setting = setting.Set(wkproto.SettingStream)

	msg := wkdb.Message{
		RecvPacket: wkproto.RecvPacket{
			Setting:     setting,
			MessageID:   messageId,
			ClientMsgNo: clientMsgNo,
			FromUID:     req.FromUid,
			ChannelID:   fakeChannelId,
			ChannelType: req.ChannelType,
			Timestamp:   int32(time.Now().Unix()),
			Payload:     []byte(req.Event.Data),
		},
	}

	// 保存消息
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	results, err := service.Store.AppendMessages(timeoutCtx, fakeChannelId, req.ChannelType, []wkdb.Message{
		msg,
	})
	cancel()
	if err != nil {
		e.Error("保存消息失败！", zap.Error(err))
		return msg, err
	}
	if len(results) == 0 {
		e.Error("保存消息失败！没返回结果", zap.Error(errors.New("保存消息失败")))
		return msg, errors.New("保存消息失败")
	}
	result := results[0]

	msg.MessageSeq = uint32(result.Index)

	return msg, nil

}

func (e *event) sendStreamMessage(messageId int64, req eventReq) error {

	fakeChannelId := req.ChannelId
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.ChannelId, req.FromUid)
	}

	clientMsgNo := req.ClientMsgNo

	var setting wkproto.Setting
	setting = setting.Set(wkproto.SettingStream)

	channelType := req.ChannelType
	fromUid := req.FromUid

	sendPacket := &wkproto.SendPacket{
		Setting:     setting,
		ClientMsgNo: clientMsgNo,
		ChannelID:   req.ChannelId,
		ChannelType: channelType,
		Payload:     []byte(req.Event.Data),
	}

	eventbus.Channel.SendMessage(fakeChannelId, channelType, &eventbus.Event{
		Conn: &eventbus.Conn{
			Uid:      fromUid,
			DeviceId: options.G.SystemDeviceId,
		},
		Type:       eventbus.EventChannelOnSend,
		Frame:      sendPacket,
		MessageId:  messageId,
		StreamFlag: wkproto.StreamFlagStart,
	})
	eventbus.Channel.Advance(fakeChannelId, channelType)

	time.Sleep(time.Millisecond * 100) // TODO: 这里等待一下，让消息发送出去,要不然可能会出现流的开始消息还没收到，流消息先到了

	return nil
}

func (e *event) sendEvent(messageId int64, req eventReq) error {
	event := &wkproto.EventPacket{
		Type:      req.Event.Type,
		Id:        req.Event.Id,
		Timestamp: req.Event.Timestamp,
		Data:      []byte(req.Event.Data),
	}

	fakeChannelId := req.ChannelId
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.ChannelId, req.FromUid)
	}

	eventbus.Channel.SendMessage(fakeChannelId, req.ChannelType, &eventbus.Event{
		Conn: &eventbus.Conn{
			Uid:      req.FromUid,
			DeviceId: options.G.SystemDeviceId,
		},
		Type:      eventbus.EventChannelDistribute, // 直接进行分发
		Frame:     event,
		MessageId: messageId,
	})

	// eventbus.Channel.Advance(fakeChannelId, req.ChannelType)
	return nil
}
