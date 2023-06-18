package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type MonitorAPI struct {
	wklog.Log
	s *Server
}

// NewMonitorAPI NewMonitorAPI
func NewMonitorAPI(s *Server) *MonitorAPI {
	return &MonitorAPI{
		Log: wklog.NewWKLog("MonitorAPI"),
		s:   s,
	}
}

// Route 用户相关路由配置
func (m *MonitorAPI) Route(r *wkhttp.WKHttp) {

	r.GetGinRoute().Use(gzip.Gzip(gzip.DefaultCompression))

	r.GetGinRoute().NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/web") {
			c.File("./web/dist/index.html")
			return
		}
	})

	r.Static("/web", "./web/dist")

	varz := NewVarzAPI(m.s)
	connz := NewConnzAPI(m.s)

	r.GET("/api/varz", varz.HandleVarz)
	r.GET("/api/connz", connz.HandleConnz)

	r.GET("/api/metrics", m.s.monitor.Monitor)   // prometheus监控
	r.GET("/api/chart/realtime", m.realtime)     // 首页实时数据
	r.GET("/api/channels", m.channels)           // 频道
	r.GET("/api/messages", m.messages)           // 消息
	r.GET("/api/conversations", m.conversations) // 最近会话
	// r.GET("/chart/upstream_packet_count", m.upstreamPacketCount)

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

func (m *MonitorAPI) channels(c *wkhttp.Context) {
	channelID := c.Query("channel_id")
	channelTypeI, _ := strconv.Atoi(c.Query("channel_type"))
	channelType := uint8(channelTypeI)
	channelInfo, err := m.s.store.GetChannel(channelID, channelType)
	if err != nil {
		m.Error("get channel error", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if channelInfo == nil {
		c.ResponseError(ErrChannelNotFound)
		return
	}
	subscribers, err := m.s.store.GetSubscribers(channelID, channelType)
	if err != nil {
		c.ResponseError(err)
		return
	}
	allowlist, err := m.s.store.GetAllowlist(channelID, channelType)
	if err != nil {
		c.ResponseError(err)
		return
	}
	denylist, err := m.s.store.GetDenylist(channelID, channelType)
	if err != nil {
		c.ResponseError(err)
		return
	}
	lastMsgSeq, err := m.s.store.GetLastMsgSeq(channelID, channelType)
	if err != nil {
		c.ResponseError(err)
		return
	}
	topic := m.topicName(channelID, channelType)
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

	messages, err := m.s.store.LoadNextRangeMsgs(channelID, channelType, uint32(startMessageSeq), 0, limt)
	if err != nil {
		c.ResponseError(err)
		return
	}
	maxMsgSeq, _ := m.s.store.GetLastMsgSeq(channelID, channelType)
	messageResps := make([]*MessageResp, 0)
	if len(messages) > 0 {
		for _, message := range messages {
			msgResp := &MessageResp{}
			msgResp.from(message.(*Message))
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

func newChannelInfoResult(channelInfo *wkstore.ChannelInfo, lastMsgSeq uint32, slotNum uint32, subscribers []string, allowList []string, denyList []string) *channelInfoResult {

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
