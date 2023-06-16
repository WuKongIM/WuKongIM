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
	ConnNums() []int           // 一定周期的连接数

	RetryQueueMsgInc() // 重试队列消息递增
	RetryQueueMsgDec() // 重试队列消息递减

	WebhookObserve(event string, v time.Duration) // webhook耗时记录

	// ---------- 上行 ----------
	UpstreamTrafficSample() []int
	UpstreamPackageSample() []int // 上行包数
	UpstreamPackageAdd(v int)     // 上行包
	UpstreamTrafficAdd(v int)     // 上行数据包流量

	SendSystemMsgInc() // 系统消息递增

	SendPacketInc(persist bool) // 发送包递增

	// ---------- 下行 ----------
	DownstreamTrafficSample() []int
	DownstreamPackageSample() []int // 下行包数
	DownstreamPackageAdd(v int)     // 下线包
	DownstreamTrafficAdd(v int)     // 下行数据包流量
	RecvPacketInc(persist bool)     // 接受包递增

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

func (m *monitorEmpty) ConnNums() []int { return nil }

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
func (m *monitorEmpty) UpstreamTrafficSample() []int { return nil }
func (m *monitorEmpty) UpstreamTrafficAdd(v int)     {}
func (m *monitorEmpty) UpstreamPackageAdd(v int)     {}
func (m *monitorEmpty) UpstreamPackageSample() []int { return nil }

// ---------- 下行 ----------
func (m *monitorEmpty) DownstreamTrafficSample() []int { return nil }
func (m *monitorEmpty) DownstreamPackageSample() []int { return nil }
func (m *monitorEmpty) DownstreamPackageAdd(v int)     {}
func (m *monitorEmpty) DownstreamTrafficAdd(v int)     {}

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
