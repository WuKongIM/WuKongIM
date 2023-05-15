package monitor

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
)

var monitorGlob IMonitor
var monitorOnGlob bool

// 设置监控是否开启
func SetMonitorOn(on bool) {
	monitorOnGlob = on
}

// 获取监控
func GetMonitor() IMonitor {
	if monitorGlob == nil {
		monitorGlob = NewMonitor(monitorOnGlob)
	}
	return monitorGlob
}

func NewMonitor(on bool) IMonitor {
	if !on {
		return &monitorEmpty{}
	}
	return NewPrometheus()
}

type IMonitor interface {
	Start()
	Stop()
	Monitor(c *wkhttp.Context) // 暴露监控接口
	ConnInc()                  // 连接数量递增
	ConnDec()                  // 连接数递减

	RetryQueueMsgInc()          // 重试队列消息递增
	RetryQueueMsgDec()          // 重试队列消息递减
	NodeRetryQueueMsgInc()      // 节点消息重试队列递增
	NodeRetryQueueMsgDec()      // 节点消息重试队列递减
	NodeRetryQueueMsgSet(v int) // 节点消息重试队列设置

	NodeGRPCConnPoolInc(addr string)        // 节点之间grpc连接池的连接递增
	NodeGRPCConnPoolDec(addr string)        // 节点之间gprc连接池的连接递减
	NodeGRPCConnPoolSet(addr string, v int) // 节点之间的grpc连接池的连接数设置

	WebhookObserve(event string, v time.Duration) // webhook耗时记录

	// ---------- 上行 ----------
	UpstreamAdd(v int)               // 上行流量监控
	UpstreamPackageTrafficAdd(v int) // 上行数据包流量
	UpstreamPacketInc()              // 上行数据包数量

	SendSystemMsgInc() // 系统消息递增

	SendPacketInc(persist bool) // 发送包递增

	// ---------- 下行 ----------
	DownstreamAdd(v int)               // 下线流量
	DownstreamPackageTrafficAdd(v int) // 下行数据包流量
	DownstreamPacketInc()              // 下行数据包数量
	RecvPacketInc(persist bool)        // 接受包递增

	// ---------- db相关 ----------
	SlotCacheInc()
	SlotCacheDec()
	TopicCacheInc()
	TopicCacheDec()
	SegmentCacheInc()
	SegmentCacheDec()

	ConversationCacheSet(v int) // 最近会话缓存数量
	TmpChannelCacheCountInc()   // 临时频道缓存数量递增
	TmpChannelCacheCountDec()   // 临时频道缓存数量递减
	ChannelCacheCountInc()      // 频道缓存数量递增
	ChannelCacheCountDec()      // 频道缓存数量递减

	InFlightMessagesSet(v int) // 投递中的消息

	OnlineUserInc() // 在线用户递增
	OnlineUserDec() // 在线用户递减
}

type monitorEmpty struct {
}

func (m *monitorEmpty) Start()                    {}
func (m *monitorEmpty) Stop()                     {}
func (m *monitorEmpty) Monitor(c *wkhttp.Context) {}
func (m *monitorEmpty) ConnInc()                  {}
func (m *monitorEmpty) ConnDec()                  {}

func (m *monitorEmpty) RetryQueueMsgInc() {}
func (m *monitorEmpty) RetryQueueMsgDec() {}

func (m *monitorEmpty) NodeGRPCConnPoolInc(addr string)        {}
func (m *monitorEmpty) NodeGRPCConnPoolDec(addr string)        {}
func (m *monitorEmpty) NodeGRPCConnPoolSet(addr string, v int) {}

func (m *monitorEmpty) WebhookObserve(event string, v time.Duration) {}

func (m *monitorEmpty) NodeRetryQueueMsgInc()      {}
func (m *monitorEmpty) NodeRetryQueueMsgDec()      {}
func (m *monitorEmpty) NodeRetryQueueMsgSet(v int) {}

// ---------- 上行 ----------
func (m *monitorEmpty) UpstreamAdd(v int)               {}
func (m *monitorEmpty) UpstreamPackageTrafficAdd(v int) {}
func (m *monitorEmpty) UpstreamPacketInc()              {}

// ---------- 下行 ----------
func (m *monitorEmpty) DownstreamAdd(v int)               {}
func (m *monitorEmpty) DownstreamPackageTrafficAdd(v int) {}
func (m *monitorEmpty) DownstreamPacketInc()              {}

// ---------- db ----------

func (m *monitorEmpty) SlotCacheInc() {}
func (m *monitorEmpty) SlotCacheDec() {}

func (m *monitorEmpty) TopicCacheInc()   {}
func (m *monitorEmpty) TopicCacheDec()   {}
func (m *monitorEmpty) SegmentCacheInc() {}
func (m *monitorEmpty) SegmentCacheDec() {}

func (m *monitorEmpty) ConversationCacheSet(v int) {}

func (m *monitorEmpty) TmpChannelCacheCountInc() {}

func (m *monitorEmpty) TmpChannelCacheCountDec() {}

func (m *monitorEmpty) ChannelCacheCountInc() {}
func (m *monitorEmpty) ChannelCacheCountDec() {}

func (m *monitorEmpty) InFlightMessagesSet(v int) {}

func (m *monitorEmpty) SendPacketInc(persist bool) {}

func (m *monitorEmpty) RecvPacketInc(persist bool) {}

func (m *monitorEmpty) OnlineUserInc() {}
func (m *monitorEmpty) OnlineUserDec() {}

func (m *monitorEmpty) SendSystemMsgInc() {}
