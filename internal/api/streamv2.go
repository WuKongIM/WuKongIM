package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wkcache"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type streamV2 struct {
	s *Server
	wklog.Log
}

func newStreamv2(s *Server) *streamV2 {
	return &streamV2{
		s:   s,
		Log: wklog.NewWKLog("streamV2"),
	}
}

func (s *streamV2) route(r *wkhttp.WKHttp) {
	r.POST("/streamv2/open", s.open)   // 流消息开始
	r.POST("/streamv2/write", s.write) // 流消息写入
	r.POST("/streamv2/close", s.close) // 流消息结束
}

func (s *streamV2) open(c *wkhttp.Context) {
	var req streamv2OpenReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		c.ResponseError(errors.Wrap(err, "数据格式有误！"))
		return
	}

	if err := req.check(); err != nil {
		c.ResponseError(err)
		return
	}

	if strings.TrimSpace(req.FromUid) == "" {
		req.FromUid = options.G.SystemUID
	}
	if strings.TrimSpace(req.ClientMsgNo) == "" {
		req.ClientMsgNo = fmt.Sprintf("%s0", wkutil.GenUUID())
	}

	fakeChannelId := req.ChannelId
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.ChannelId, req.FromUid)
	}

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(fakeChannelId, req.ChannelType) // 获取频道的槽领导节点
	if err != nil {
		s.Error("获取频道所在节点失败！", zap.Error(err), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", req.ChannelType))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		s.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	if req.Force {
		// 强制结束其他流
		err = service.StreamCache.EndStreamInChannel(fakeChannelId, req.ChannelType, wkcache.EndReasonForce)
		if err != nil {
			c.ResponseError(err)
			s.Error("强制结束其他流失败！", zap.Error(err), zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", req.ChannelType))
			return
		}
	} else {
		if service.StreamCache.HasStreamInChannel(fakeChannelId, req.ChannelType) {
			c.ResponseError(errors.New("频道中已有流正在运行！"))
			s.Error("频道中已有流正在运行！", zap.String("fakeChannelId", fakeChannelId), zap.Uint8("channelType", req.ChannelType))
			return
		}
	}

	// 保存消息
	message, err := s.storeMessage(req)
	if err != nil {
		c.ResponseError(err)
		return
	}

	// 开启流
	meta := wkcache.NewStreamMetaBuilder(message.MessageID).
		Channel(req.ChannelId, req.ChannelType).
		From(req.FromUid).
		Build()
	err = s.openStream(meta)
	if err != nil {
		c.ResponseError(err)
		return
	}

	// 发送消息
	err = s.sendMessage(req.ChannelId, message)
	if err != nil {
		c.ResponseError(err)
		s.Error("发送消息失败！", zap.Error(err))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message_id":  message.MessageID,
		"message_seq": message.MessageSeq,
	})
}

func (s *streamV2) openStream(meta *wkcache.StreamMeta) error {
	err := service.StreamCache.OpenStream(meta)
	if err != nil {
		s.Error("打开流失败！", zap.Error(err))
		return err
	}
	return nil
}

func (s *streamV2) storeMessage(req streamv2OpenReq) (wkdb.Message, error) {
	fakeChannelId := req.ChannelId
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.ChannelId, req.FromUid)
	}

	clientMsgNo := req.ClientMsgNo
	if strings.TrimSpace(clientMsgNo) == "" {
		clientMsgNo = fmt.Sprintf("%s0", wkutil.GenUUID())
	}

	messageId := options.G.GenMessageId()
	streamNo := strconv.FormatInt(messageId, 10)

	var setting wkproto.Setting
	if strings.TrimSpace(streamNo) != "" {
		setting = setting.Set(wkproto.SettingStream)
	}

	msg := wkdb.Message{
		RecvPacket: wkproto.RecvPacket{
			Framer: wkproto.Framer{
				RedDot:    wkutil.IntToBool(req.Header.RedDot),
				SyncOnce:  wkutil.IntToBool(req.Header.SyncOnce),
				NoPersist: wkutil.IntToBool(req.Header.NoPersist),
			},
			Setting:     setting,
			MessageID:   messageId,
			ClientMsgNo: clientMsgNo,
			FromUID:     req.FromUid,
			ChannelID:   fakeChannelId,
			ChannelType: req.ChannelType,
			Timestamp:   int32(time.Now().Unix()),
			StreamNo:    streamNo,
			StreamId:    uint64(messageId),
			Payload:     req.Payload,
		},
	}

	// 保存消息
	timeoutCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	results, err := service.Store.AppendMessages(timeoutCtx, fakeChannelId, req.ChannelType, []wkdb.Message{
		msg,
	})
	cancel()
	if err != nil {
		s.Error("保存消息失败！", zap.Error(err))
		return msg, err
	}
	if len(results) == 0 {
		s.Error("保存消息失败！没返回结果", zap.Error(errors.New("保存消息失败")))
		return msg, errors.New("保存消息失败")
	}
	result := results[0]

	msg.MessageSeq = uint32(result.Index)

	return msg, nil

}

func (s *streamV2) sendMessage(channelId string, message wkdb.Message) error {

	header := message.RecvPacket.Framer
	setting := message.RecvPacket.Setting
	fakeChannelId := message.ChannelID
	channelType := message.ChannelType
	fromUid := message.FromUID

	sendPacket := &wkproto.SendPacket{
		Framer:      header,
		Setting:     setting,
		StreamNo:    message.StreamNo,
		ClientMsgNo: message.ClientMsgNo,
		ChannelID:   channelId,
		ChannelType: channelType,
		Payload:     message.Payload,
	}

	eventbus.Channel.SendMessage(fakeChannelId, channelType, &eventbus.Event{
		Conn: &eventbus.Conn{
			Uid:      fromUid,
			DeviceId: options.G.SystemDeviceId,
		},
		Type:       eventbus.EventChannelOnSend,
		Frame:      sendPacket,
		MessageId:  message.MessageID,
		StreamNo:   message.StreamNo,
		StreamFlag: wkproto.StreamFlagStart,
	})
	eventbus.Channel.Advance(fakeChannelId, channelType)

	time.Sleep(time.Millisecond * 100) // TODO: 这里等待一下，让消息发送出去,要不然可能会出现流的开始消息还没收到，流消息先到了

	return nil
}

func (s *streamV2) write(c *wkhttp.Context) {
	var req streamv2WriteReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		c.ResponseError(errors.Wrap(err, "数据格式有误！"))
		s.Error("数据格式有误！", zap.Error(err))
		return
	}

	if err := req.check(); err != nil {
		c.ResponseError(err)
		return
	}
	fakeChannelId := req.ChannelId
	channelType := req.ChannelType
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.ChannelId, req.FromUid)
	}
	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(fakeChannelId, channelType) // 获取消息的槽领导节点
	if err != nil {
		s.Error("获取消息所在节点失败！", zap.Error(err), zap.Int64("messageId", req.MessageId))
		c.ResponseError(errors.New("获取消息所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		s.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	meta, err := service.StreamCache.GetStreamInfo(req.MessageId)
	if err != nil {
		c.ResponseError(err)
		s.Error("获取流信息失败！", zap.Error(err), zap.Int64("messageId", req.MessageId))
		return
	}

	if meta == nil {
		c.ResponseError(errors.New("流不存在！"))
		s.Error("流不存在！", zap.Int64("messageId", req.MessageId))
		return
	}
	if meta.Closed {
		c.ResponseError(errors.New("流已关闭！"))
		s.Error("流已关闭！", zap.Int64("messageId", req.MessageId))
		return
	}
	if meta.ChannelId != req.ChannelId || meta.ChannelType != req.ChannelType {
		c.ResponseError(errors.New("流的频道不匹配！"))
		s.Error("流的频道不匹配！", zap.Int64("messageId", req.MessageId))
		return
	}

	chunkId := meta.NextSeq()
	err = service.StreamCache.AppendChunk(&wkcache.MessageChunk{
		MessageId: req.MessageId,
		ChunkId:   chunkId,
		Payload:   req.Payload,
	})
	if err != nil {
		c.ResponseError(err)
		s.Error("追加流消息失败！", zap.Error(err), zap.Int64("messageId", req.MessageId))
		return
	}

	// send chunk
	err = s.sendChunk(req.MessageId, chunkId, req.Payload, meta)
	if err != nil {
		c.ResponseError(err)
		s.Error("发送流消息失败！", zap.Error(err), zap.Uint64("chunkId", chunkId), zap.Int64("messageId", req.MessageId))
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message_id": req.MessageId,
		"chunk_id":   chunkId,
	})
}

func (s *streamV2) sendChunk(messageId int64, chunkId uint64, payload []byte, meta *wkcache.StreamMeta) error {

	return nil
}

func (s *streamV2) close(c *wkhttp.Context) {
	var req streamv2CloseReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		c.ResponseError(errors.Wrap(err, "数据格式有误！"))
		s.Error("数据格式有误！", zap.Error(err))
		return
	}
	if err := req.check(); err != nil {
		c.ResponseError(err)
		return
	}

	fakeChannelId := req.ChannelId
	channelType := req.ChannelType
	if req.ChannelType == wkproto.ChannelTypePerson {
		fakeChannelId = options.GetFakeChannelIDWith(req.ChannelId, req.FromUid)
	}
	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(fakeChannelId, channelType) // 获取消息的槽领导节点
	if err != nil {
		s.Error("获取消息所在节点失败！", zap.Error(err), zap.Int64("messageId", req.MessageId))
		c.ResponseError(errors.New("获取消息所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		s.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	meta, err := service.StreamCache.GetStreamInfo(req.MessageId)
	if err != nil {
		c.ResponseError(err)
		s.Error("获取流信息失败！", zap.Error(err), zap.Int64("messageId", req.MessageId))
		return
	}
	if meta == nil {
		c.ResponseError(errors.New("流不存在！"))
		s.Error("流不存在！", zap.Int64("messageId", req.MessageId))
		return
	}
	if meta.Closed {
		c.ResponseError(errors.New("流已关闭！"))
		s.Error("流已关闭！", zap.Int64("messageId", req.MessageId))
		return
	}
	if meta.ChannelId != req.ChannelId || meta.ChannelType != req.ChannelType {
		c.ResponseError(errors.New("流的频道不匹配！"))
		s.Error("流的频道不匹配！", zap.Int64("messageId", req.MessageId))
		return
	}
	err = service.StreamCache.EndStream(req.MessageId, wkcache.EndReasonSuccess)
	if err != nil {
		c.ResponseError(err)
		s.Error("关闭流失败！", zap.Error(err), zap.Int64("messageId", req.MessageId))
		return
	}
	c.ResponseOK()
}
