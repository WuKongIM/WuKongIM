package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type MonitorAPI struct {
	wklog.Log
	s                    *Server
	monitorChannelPrefix string
}

// NewMonitorAPI NewMonitorAPI
func NewMonitorAPI(s *Server) *MonitorAPI {
	return &MonitorAPI{
		Log:                  wklog.NewWKLog("MonitorAPI"),
		s:                    s,
		monitorChannelPrefix: "__monitor",
	}
}

// Route 用户相关路由配置
func (m *MonitorAPI) Route(r *wkhttp.WKHttp) {

	varz := NewVarzAPI(m.s)
	connz := NewConnzAPI(m.s)

	r.GET("/api/varz", varz.HandleVarz)          // 系统变量
	r.GET("/api/connz", connz.HandleConnz)       // 系统客户端连接
	r.GET("/api/metrics", m.s.monitor.Monitor)   // prometheus监控
	r.GET("/api/chart/realtime", m.realtime)     // 首页实时数据
	r.GET("/api/channels", m.channels)           // 频道
	r.GET("/api/messages", m.messages)           // 消息
	r.GET("/api/conversations", m.conversations) // 最近会话
	// r.GET("/chart/upstream_packet_count", m.upstreamPacketCount)

	go m.startRealtimePublish() // 开启实时数据推送
	go m.startConnzPublish()    // 开启连接数据推送
	go m.startVarzPublish()     // 开启变量数据推送

}

func (m *MonitorAPI) realtime(c *wkhttp.Context) {
	last := c.Query("last")
	connNums := m.s.monitor.ConnNums()
	upstreamPackages := m.s.monitor.UpstreamPackageSample()
	upstreamTraffics := m.s.monitor.UpstreamTrafficSample()

	downstreamPackages := m.s.monitor.DownstreamPackageSample()
	downstreamTraffics := m.s.monitor.DownstreamTrafficSample()

	var realtimeData realtimeData
	if last == "1" {
		if len(connNums) > 0 {
			realtimeData.ConnNums = connNums[len(connNums)-1:]
		}
		if len(upstreamPackages) > 0 {
			realtimeData.UpstreamPackets = upstreamPackages[len(upstreamPackages)-1:]
		}
		if len(downstreamPackages) > 0 {
			realtimeData.DownstreamPackets = downstreamPackages[len(downstreamPackages)-1:]
		}
		if len(upstreamTraffics) > 0 {
			realtimeData.UpstreamTraffics = upstreamTraffics[len(upstreamTraffics)-1:]
		}
		if len(downstreamTraffics) > 0 {
			realtimeData.DownstreamTraffics = downstreamTraffics[len(downstreamTraffics)-1:]
		}
	} else {
		realtimeData.ConnNums = connNums
		realtimeData.UpstreamPackets = upstreamPackages
		realtimeData.DownstreamPackets = downstreamPackages
		realtimeData.UpstreamTraffics = upstreamTraffics
		realtimeData.DownstreamTraffics = downstreamTraffics
	}
	c.JSON(http.StatusOK, realtimeData)
}

func (m *MonitorAPI) startRealtimePublish() {

	tk := time.NewTicker(time.Second)

	for {
		select {
		case <-tk.C:
			m.realtimePublish()
		case <-m.s.stopChan:
			return
		}
	}

}

func (m *MonitorAPI) startConnzPublish() {
	tk := time.NewTicker(time.Second)
	for {
		select {
		case <-tk.C:
			m.connzPublish()
		case <-m.s.stopChan:
			return
		}
	}

}

func (m *MonitorAPI) startVarzPublish() {
	tk := time.NewTicker(time.Second)
	for {
		select {
		case <-tk.C:
			m.varzPublish()
		case <-m.s.stopChan:
			return
		}
	}

}

func (m *MonitorAPI) writeDataToMonitor(typ string, dataCallback func(subscribeInfo *wkstore.SubscribeInfo) interface{}) {
	monitorChannel, err := m.s.channelManager.GetChannel(fmt.Sprintf("%s_%s", m.monitorChannelPrefix, typ), wkproto.ChannelTypeData)
	if err != nil {
		m.Error("realtimePublish fail", zap.Error(err))
		return
	}
	if monitorChannel == nil {
		return
	}
	subscribers := monitorChannel.GetAllSubscribers()
	if len(subscribers) == 0 {
		return
	}

	for _, subscriber := range subscribers {
		conns := m.s.connManager.GetConnsWithUID(subscriber)
		if len(conns) == 0 {
			continue
		}
		var rDataBytes []byte
		for _, conn := range conns {
			if conn == nil {
				continue
			}

			connCtx := conn.Context().(*connContext)

			subscribeInfo := connCtx.getSubscribeInfo(monitorChannel.ChannelID, monitorChannel.ChannelType)
			if subscribeInfo == nil { // 说明该连接没有订阅该频道
				continue
			}
			data := dataCallback(subscribeInfo)
			rDataBytes = []byte(wkutil.ToJSON(data))
			recvPacket := &wkproto.RecvPacket{
				Framer: wkproto.Framer{
					NoPersist: true,
					RedDot:    false,
					SyncOnce:  false,
				},
				MessageID:   m.s.dispatch.processor.genMessageID(),
				Timestamp:   int32(time.Now().Unix()),
				ChannelID:   monitorChannel.ChannelID,
				ChannelType: monitorChannel.ChannelType,
				Payload:     rDataBytes,
			}
			encryptPayload, _ := encryptMessagePayload(rDataBytes, conn)
			recvPacket.Payload = encryptPayload

			msgKey, _ := makeMsgKey(recvPacket.VerityString(), conn)
			recvPacket.MsgKey = msgKey

			m.s.dispatch.dataOut(conn, recvPacket)
		}
	}
}

func (m *MonitorAPI) varzPublish() {
	m.writeDataToMonitor("varz", func(subscriberInfo *wkstore.SubscribeInfo) interface{} {
		show := ""
		if subscriberInfo != nil && subscriberInfo.Param != nil {
			if subscriberInfo.Param["show"] != nil {
				show = subscriberInfo.Param["show"].(string)
			}
		}
		varz := CreateVarz(m.s)
		connLimit := 20
		if show == "conn" {
			resultConns := m.s.GetConnInfos(ByInMsgDesc, 0, connLimit)
			connInfos := make([]*ConnInfo, 0, len(resultConns))
			for _, resultConn := range resultConns {
				if resultConn == nil || !resultConn.IsAuthed() {
					continue
				}
				connInfos = append(connInfos, newConnInfo(resultConn))
			}
			varz.Conns = connInfos
		}
		return varz
	})
}

func (m *MonitorAPI) realtimePublish() {
	m.writeDataToMonitor("realtime", func(subscriberInfo *wkstore.SubscribeInfo) interface{} {
		return m.getRealtimeData()
	})
}

func (m *MonitorAPI) connzPublish() {
	var (
		sortOpt   SortOpt
		offset    int
		limit     int = 20
		connInfos []*ConnInfo
	)
	m.writeDataToMonitor("connz", func(subscriberInfo *wkstore.SubscribeInfo) interface{} {
		if subscriberInfo != nil && subscriberInfo.Param != nil {
			if subscriberInfo.Param["sort_opt"] != nil {
				sortOpt = SortOpt(subscriberInfo.Param["sort_opt"].(string))
			}
			if subscriberInfo.Param["offset"] != nil {
				offsetI64, _ := subscriberInfo.Param["offset"].(json.Number).Int64()
				offset = int(offsetI64)
			}
			if subscriberInfo.Param["limit"] != nil {
				limitI64, _ := subscriberInfo.Param["limit"].(json.Number).Int64()
				limit = int(limitI64)
			}
		}
		resultConns := m.s.GetConnInfos(sortOpt, offset, limit)
		connInfos = make([]*ConnInfo, 0, len(resultConns))
		for _, resultConn := range resultConns {
			if resultConn == nil || !resultConn.IsAuthed() {
				continue
			}
			connInfos = append(connInfos, newConnInfo(resultConn))
		}
		return connInfos
	})
}

func (m *MonitorAPI) getRealtimeData() *realtimeData {
	connNums := m.s.monitor.ConnNums()
	upstreamPackages := m.s.monitor.UpstreamPackageSample()
	upstreamTraffics := m.s.monitor.UpstreamTrafficSample()

	downstreamPackages := m.s.monitor.DownstreamPackageSample()
	downstreamTraffics := m.s.monitor.DownstreamTrafficSample()

	var realtimeData = &realtimeData{}

	if len(connNums) > 0 {
		realtimeData.ConnNums = connNums[len(connNums)-1:]
	}
	if len(upstreamPackages) > 0 {
		realtimeData.UpstreamPackets = upstreamPackages[len(upstreamPackages)-1:]
	}
	if len(downstreamPackages) > 0 {
		realtimeData.DownstreamPackets = downstreamPackages[len(downstreamPackages)-1:]
	}
	if len(upstreamTraffics) > 0 {
		realtimeData.UpstreamTraffics = upstreamTraffics[len(upstreamTraffics)-1:]
	}
	if len(downstreamTraffics) > 0 {
		realtimeData.DownstreamTraffics = downstreamTraffics[len(downstreamTraffics)-1:]
	}
	return realtimeData
}

func (m *MonitorAPI) channels(c *wkhttp.Context) {
	channelID := c.Query("channel_id")
	channelTypeI, _ := strconv.Atoi(c.Query("channel_type"))
	channelType := uint8(channelTypeI)

	fakeChannelID := channelID
	if channelType == wkproto.ChannelTypePerson {
		fromUID, toUID := GetFromUIDAndToUIDWith(channelID)
		fakeChannelID = GetFakeChannelIDWith(fromUID, toUID)
	}
	channelInfo, err := m.s.channelManager.GetChannel(fakeChannelID, channelType)
	if err != nil {
		m.Error("get channel error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if channelInfo == nil {
		c.ResponseError(ErrChannelNotFound)
		return
	}
	subscribers, err := m.s.store.GetSubscribers(fakeChannelID, channelType)
	if err != nil {
		c.ResponseError(err)
		return
	}
	allowlist, err := m.s.store.GetAllowlist(fakeChannelID, channelType)
	if err != nil {
		c.ResponseError(err)
		return
	}
	denylist, err := m.s.store.GetDenylist(fakeChannelID, channelType)
	if err != nil {
		c.ResponseError(err)
		return
	}
	lastMsgSeq, err := m.s.store.GetLastMsgSeq(fakeChannelID, channelType)
	if err != nil {
		c.ResponseError(err)
		return
	}
	topic := m.topicName(fakeChannelID, channelType)
	slotNum := wkutil.GetSlotNum(m.s.opts.SlotNum, topic)
	c.JSON(http.StatusOK, newChannelInfoResult(channelInfo, lastMsgSeq, slotNum, subscribers, allowlist, denylist))
}

func (m *MonitorAPI) topicName(channelID string, channelType uint8) string {
	return fmt.Sprintf("%d-%s", channelType, channelID)
}

func (m *MonitorAPI) messages(c *wkhttp.Context) {
	channelID := c.Query("channel_id")
	channelTypeI, _ := strconv.Atoi(c.Query("channel_type"))
	channelType := uint8(channelTypeI)
	limt, _ := strconv.Atoi(c.Query("limit"))
	startMessageSeq, _ := strconv.ParseInt(c.Query("start_message_seq"), 10, 64)
	if limt <= 0 {
		limt = 20
	}
	fakeChannelID := channelID
	if channelType == wkproto.ChannelTypePerson {
		fromUID, toUID := GetFromUIDAndToUIDWith(channelID)
		fakeChannelID = GetFakeChannelIDWith(fromUID, toUID)
	}

	messages, err := m.s.store.LoadNextRangeMsgs(fakeChannelID, channelType, uint32(startMessageSeq), 0, limt)
	if err != nil {
		c.ResponseError(err)
		return
	}
	maxMsgSeq, _ := m.s.store.GetLastMsgSeq(fakeChannelID, channelType)
	messageResps := make([]*MessageResp, 0)
	if len(messages) > 0 {
		for _, message := range messages {
			msgResp := &MessageResp{}
			msgResp.from(message.(*Message), m.s.store)
			messageResps = append(messageResps, msgResp)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"data":              messageResps,
		"limit":             limt,
		"start_message_seq": startMessageSeq,
		"max_message_seq":   maxMsgSeq,
	})
}

func (m *MonitorAPI) conversations(c *wkhttp.Context) {
	uid := c.Query("uid")

	if strings.TrimSpace(uid) == "" {
		c.ResponseError(ErrParamInvalid)
		return
	}

	conversationResults := make([]*syncUserConversationResp, 0)
	if m.s.opts.Conversation.On {
		conversations := m.s.conversationManager.GetConversations(uid, 0, nil)
		if len(conversations) > 0 {
			for _, conversation := range conversations {
				conversationResults = append(conversationResults, newSyncUserConversationResp(conversation))
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"on":            m.s.opts.Conversation.On,
		"conversations": conversationResults,
	})
}

type realtimeData struct {
	ConnNums           []int `json:"conn_nums"`
	UpstreamPackets    []int `json:"upstream_packets"`
	UpstreamTraffics   []int `json:"upstream_traffics"`
	DownstreamPackets  []int `json:"downstream_packets"`
	DownstreamTraffics []int `json:"downstream_traffics"`
}

type channelInfoResult struct {
	ChannelID   string   `json:"channel_id"`
	ChannelType uint8    `json:"channel_type"`
	Ban         bool     `json:"ban"`                   // 是否被封
	Large       bool     `json:"large"`                 // 是否是超大群
	LastMsgSeq  uint32   `json:"last_msg_seq"`          // 最后一条消息的seq
	Subscribers []string `json:"subscribers,omitempty"` // 订阅者集合
	AllowList   []string `json:"allow_list,omitempty"`  // 白名单
	DenyList    []string `json:"deny_list,omitempty"`   // 黑名单
	SlotNum     uint32   `json:"slot_num"`              // slot编号
}

func newChannelInfoResult(channelInfo *Channel, lastMsgSeq uint32, slotNum uint32, subscribers []string, allowList []string, denyList []string) *channelInfoResult {

	return &channelInfoResult{
		ChannelID:   channelInfo.ChannelID,
		ChannelType: channelInfo.ChannelType,
		Ban:         channelInfo.Ban,
		Large:       channelInfo.Large,
		LastMsgSeq:  lastMsgSeq,
		Subscribers: subscribers,
		AllowList:   allowList,
		DenyList:    denyList,
		SlotNum:     slotNum,
	}
}
