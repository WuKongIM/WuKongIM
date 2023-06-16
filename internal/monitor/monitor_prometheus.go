package monitor

import (
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/atomic"
)

type Prometheus struct {
	connGauge                prometheus.Gauge
	retryQueueMsgGauge       prometheus.Gauge
	nodeRetryQueueMsgGauge   prometheus.Gauge
	nodeGRPCConnPoolGaugeVec *prometheus.GaugeVec

	webhookHistogram          *prometheus.HistogramVec
	tmpChannelCacheCountGauge prometheus.Gauge
	channelCacheCountGauge    prometheus.Gauge
	inFlightMessagesGauge     prometheus.Gauge

	onlineUserGauge prometheus.Gauge

	// ---------------- 上行 ----------------
	upstreamCounter               prometheus.Counter
	upstreamPackageTrafficCounter prometheus.Counter
	upstreamPacketGauge           prometheus.Gauge
	sendPacketCounter             *prometheus.CounterVec
	sendSystemMsgIncCounter       prometheus.Counter

	// ---------------- 下行 ----------------
	downstreamCounter               prometheus.Counter
	downstreamPackageTrafficCounter prometheus.Counter
	downstreamPacketGauge           prometheus.Gauge
	recvPacketCounter               *prometheus.CounterVec

	// ---------------- db相关 ----------------
	slotCacheGauge    prometheus.Gauge // slot缓存数量
	topicCacheGauge   prometheus.Gauge // topic缓存数量
	segmentCacheGauge prometheus.Gauge // segment缓存数量

	conversationCacheCountGauge prometheus.Gauge // 最近会话缓存数量

	stopChan chan struct{}

	connNumFifo         *wkutil.FIFO
	sample              int // 采样数量
	upstreamPackageFifo *wkutil.FIFO
	upstreamPackageQPS  atomic.Int64 // 上行数据包QPS

	upstreamTrafficFifo *wkutil.FIFO // 上行流量
	upstreamTrafficQPS  atomic.Int64 // 上行流量QPS

	downstreamPackageFifo *wkutil.FIFO
	downstreamPackageQPS  atomic.Int64 // 下行数据包QPS

	downstreamTrafficFifo *wkutil.FIFO // 下行流量
	downstreamTrafficQPS  atomic.Int64 // 下行流量QPS

	namespace string
	subsystem string
}

func NewPrometheus() IMonitor {

	namespace := "wukong"
	subsystem := "im"

	connGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "conn_count",
		Help:      "连接数量监听",
	})

	retryQueueMsgGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "retry_queue_msg_count",
		Help:      "重试队列里的消息数量",
	})

	nodeRetryQueueMsgGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "node_retry_queue_msg_count",
		Help:      "节点消息重试队列里的消息数量",
	})

	nodeGRPCConnPoolGaugeVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "node_grpc_pool_conn_count",
		Help:      "节点之间的grpc连接池的连接数量",
	}, []string{"addr"})

	webhookHistogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "webhook_histogram",
		Help:      "webhook请求耗时",
		Buckets:   []float64{0.001, 0.002, 0.005, 0.1, 0.2, 0.3, 0.4, 0.5, 0.8, 1, 2, 5, 10},
	}, []string{"event"})

	tmpChannelCacheCountGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "tmp_channel_cache_count",
		Help:      "临时频道缓存数量",
	})

	channelCacheCountGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "channel_cache_count",
		Help:      "频道缓存数量",
	})

	inFlightMessagesGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "in_flight_messages",
		Help:      "投递中的消息",
	})

	onlineUserGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "online_user",
		Help:      "在线用户数量",
	})

	prometheus.MustRegister(retryQueueMsgGauge)
	prometheus.MustRegister(nodeRetryQueueMsgGauge)
	prometheus.MustRegister(connGauge)
	prometheus.MustRegister(nodeGRPCConnPoolGaugeVec)
	prometheus.MustRegister(webhookHistogram)
	prometheus.MustRegister(tmpChannelCacheCountGauge)
	prometheus.MustRegister(channelCacheCountGauge)
	prometheus.MustRegister(inFlightMessagesGauge)
	prometheus.MustRegister(onlineUserGauge)

	// ---------------- 上行 ----------------
	upstreamCounter := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "upstream_traffic",
		Help:      "整个应用的上行流量",
	})

	upstreamPackageTrafficCounter := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "upstream_package_traffic",
		Help:      "数据包的上行流量",
	})

	upstreamPacketGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "upstream_package",
		Help:      "上行数据包的数量",
	})

	sendSystemMsgIncCounter := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "send_system_msg_count",
		Help:      "发送系统消息发送数量",
	})

	sendPacketCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "send_packet_count",
		Help:      "发送包数量",
	}, []string{"persist"})

	prometheus.MustRegister(upstreamCounter)
	prometheus.MustRegister(upstreamPackageTrafficCounter)
	prometheus.MustRegister(upstreamPacketGauge)
	prometheus.MustRegister(sendPacketCounter)
	prometheus.MustRegister(sendSystemMsgIncCounter)

	// ---------------- 下行 ----------------

	downstreamCounter := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "downstream_traffic",
		Help:      "整个应用的下行流量",
	})

	downstreamPackageTrafficCounter := prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "downstream_package_traffic",
		Help:      "数据包的下行流量",
	})

	downstreamPacketGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "downstream_package",
		Help:      "下行数据包的数量",
	})

	recvPacketCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "recv_packet_count",
		Help:      "接受包数量",
	}, []string{"persist"})

	prometheus.MustRegister(downstreamCounter)
	prometheus.MustRegister(downstreamPackageTrafficCounter)
	prometheus.MustRegister(downstreamPacketGauge)
	prometheus.MustRegister(recvPacketCounter)

	// ---------------- db ----------------
	slotCacheGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "db_slot_cache",
		Help:      "slot缓存",
	})
	topicCacheGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "db_topic_cache",
		Help:      "topic缓存",
	})

	segmentCacheGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "db_segment_cache",
		Help:      "segment缓存",
	})

	conversationCacheCountGauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: subsystem,
		Name:      "conversation_cache",
		Help:      "最近会话缓存数量",
	})

	prometheus.MustRegister(slotCacheGauge)
	prometheus.MustRegister(topicCacheGauge)
	prometheus.MustRegister(segmentCacheGauge)
	prometheus.MustRegister(conversationCacheCountGauge)

	sample := 60

	return &Prometheus{
		connGauge:                connGauge,
		retryQueueMsgGauge:       retryQueueMsgGauge,
		nodeRetryQueueMsgGauge:   nodeRetryQueueMsgGauge,
		webhookHistogram:         webhookHistogram,
		nodeGRPCConnPoolGaugeVec: nodeGRPCConnPoolGaugeVec,

		upstreamCounter:               upstreamCounter,
		upstreamPacketGauge:           upstreamPacketGauge,
		upstreamPackageTrafficCounter: upstreamPackageTrafficCounter,
		upstreamTrafficFifo:           wkutil.NewFIFO(sample),

		downstreamCounter:               downstreamCounter,
		downstreamPacketGauge:           downstreamPacketGauge,
		downstreamPackageTrafficCounter: downstreamPackageTrafficCounter,
		downstreamTrafficFifo:           wkutil.NewFIFO(sample),

		tmpChannelCacheCountGauge: tmpChannelCacheCountGauge,
		channelCacheCountGauge:    channelCacheCountGauge,
		sendPacketCounter:         sendPacketCounter,
		recvPacketCounter:         recvPacketCounter,
		onlineUserGauge:           onlineUserGauge,
		sendSystemMsgIncCounter:   sendSystemMsgIncCounter,
		stopChan:                  make(chan struct{}),
		connNumFifo:               wkutil.NewFIFO(sample),
		upstreamPackageFifo:       wkutil.NewFIFO(sample),
		downstreamPackageFifo:     wkutil.NewFIFO(sample),
		namespace:                 namespace,
		subsystem:                 subsystem,
		sample:                    sample,
		// ---------------- db -----------------
		slotCacheGauge:              slotCacheGauge,
		topicCacheGauge:             topicCacheGauge,
		segmentCacheGauge:           segmentCacheGauge,
		conversationCacheCountGauge: conversationCacheCountGauge,
		inFlightMessagesGauge:       inFlightMessagesGauge,
	}
}

func (p *Prometheus) Start() {
	go func() {
		ticker := time.NewTicker(time.Second)
		for {
			select {
			case <-ticker.C:

				// ################### 上行统计 ###################
				//  上行包QPS统计
				p.upstreamPackageFifo.Push(int(p.upstreamPackageQPS.Load()))
				p.upstreamPackageQPS.Store(0)
				// 上行流量统计
				p.upstreamTrafficFifo.Push(int(p.upstreamTrafficQPS.Load()))
				p.upstreamTrafficQPS.Store(0)
				// ################### 下行QPS统计 ###################
				// 下行包QPS统计
				p.downstreamPackageFifo.Push(int(p.downstreamPackageQPS.Load()))
				p.downstreamPackageQPS.Store(0)
				// 下行流量统计
				p.downstreamTrafficFifo.Push(int(p.downstreamTrafficQPS.Load()))
				p.downstreamTrafficQPS.Store(0)

				// ################### 其他统计 ###################
				metrics, _ := prometheus.DefaultGatherer.Gather()
				if len(metrics) > 0 {
					for _, mf := range metrics {
						name := mf.GetName()
						if name == prometheus.BuildFQName(p.namespace, p.subsystem, "conn_count") {
							value := int(mf.GetMetric()[0].GetGauge().GetValue())
							p.connNumFifo.Push(value)
						}
					}
				}
			case <-p.stopChan:
				return
			}

		}
	}()
}

func (p *Prometheus) Stop() {
	close(p.stopChan)
}

func (p *Prometheus) ConnInc() {
	p.connGauge.Inc()
}

func (p *Prometheus) ConnDec() {
	p.connGauge.Dec()
}

func (p *Prometheus) ConnNums() []int {

	return p.connNumFifo.Data()
}

func (p *Prometheus) UpstreamAdd(v int) {
	p.upstreamCounter.Add(float64(v))
}
func (p *Prometheus) UpstreamTrafficAdd(v int) {
	p.upstreamTrafficQPS.Add(int64(v))
	p.upstreamPackageTrafficCounter.Add(float64(v))
}

func (p *Prometheus) UpstreamPackageAdd(v int) {
	p.upstreamPackageQPS.Add(int64(v))
	p.upstreamPacketGauge.Add(float64(v))
}

func (p *Prometheus) UpstreamPackageSample() []int {
	return p.upstreamPackageFifo.Data()
}
func (p *Prometheus) UpstreamTrafficSample() []int {
	return p.upstreamTrafficFifo.Data()
}

func (p *Prometheus) DownstreamTrafficSample() []int {
	return p.downstreamTrafficFifo.Data()
}
func (p *Prometheus) DownstreamPackageSample() []int {
	return p.downstreamPackageFifo.Data()
}

func (p *Prometheus) DownstreamPackageAdd(v int) {
	p.downstreamPackageQPS.Add(int64(v))
	p.downstreamPacketGauge.Add(float64(v))
}

func (p *Prometheus) DownstreamTrafficAdd(v int) {
	p.downstreamTrafficQPS.Add(int64(v))
	p.downstreamPackageTrafficCounter.Add(float64(v))
}

func (p *Prometheus) RetryQueueMsgInc() {
	p.retryQueueMsgGauge.Inc()
}
func (p *Prometheus) RetryQueueMsgDec() {
	p.retryQueueMsgGauge.Dec()
}

func (p *Prometheus) NodeRetryQueueMsgInc() {
	p.nodeRetryQueueMsgGauge.Inc()
}
func (p *Prometheus) NodeRetryQueueMsgDec() {
	p.nodeRetryQueueMsgGauge.Dec()
}
func (p *Prometheus) NodeRetryQueueMsgSet(v int) {
	p.nodeRetryQueueMsgGauge.Set(float64(v))
}

func (p *Prometheus) NodeGRPCConnPoolInc(addr string) {
	p.nodeGRPCConnPoolGaugeVec.With(prometheus.Labels{"addr": addr}).Inc()
}
func (p *Prometheus) NodeGRPCConnPoolDec(addr string) {
	p.nodeGRPCConnPoolGaugeVec.With(prometheus.Labels{"addr": addr}).Dec()
}

func (p *Prometheus) NodeGRPCConnPoolSet(addr string, v int) {
	p.nodeGRPCConnPoolGaugeVec.With(prometheus.Labels{"addr": addr}).Set(float64(v))
}

func (p *Prometheus) WebhookObserve(event string, v time.Duration) {
	p.webhookHistogram.With(prometheus.Labels{"event": event}).Observe(float64(v) / (1000 * 1000 * 1000))
}

func (p *Prometheus) SlotCacheInc() {
	p.slotCacheGauge.Inc()

}
func (p *Prometheus) SlotCacheDec() {
	p.slotCacheGauge.Dec()
}

func (p *Prometheus) TopicCacheInc() {
	p.topicCacheGauge.Inc()
}

func (p *Prometheus) TopicCacheDec() {
	p.topicCacheGauge.Dec()
}

func (p *Prometheus) SegmentCacheInc() {
	p.segmentCacheGauge.Inc()
}

func (p *Prometheus) SegmentCacheDec() {
	p.segmentCacheGauge.Dec()
}

func (p *Prometheus) ConversationCacheSet(v int) {
	p.conversationCacheCountGauge.Set(float64(v))
}

func (p *Prometheus) TmpChannelCacheCountInc() {
	p.tmpChannelCacheCountGauge.Inc()
}

func (p *Prometheus) TmpChannelCacheCountDec() {
	p.tmpChannelCacheCountGauge.Dec()
}

func (p *Prometheus) ChannelCacheCountInc() {
	p.channelCacheCountGauge.Inc()
}

func (p *Prometheus) ChannelCacheCountDec() {
	p.channelCacheCountGauge.Dec()
}

func (p *Prometheus) InFlightMessagesSet(v int) {
	p.inFlightMessagesGauge.Set(float64(v))
}

func (p *Prometheus) SendPacketInc(persist bool) {
	p.sendPacketCounter.With(prometheus.Labels{"persist": fmt.Sprintf("%t", persist)}).Inc()
}

func (p *Prometheus) RecvPacketInc(persist bool) {
	p.recvPacketCounter.With(prometheus.Labels{"persist": fmt.Sprintf("%t", persist)}).Inc()
}

func (p *Prometheus) OnlineUserInc() {
	p.onlineUserGauge.Inc()
}
func (p *Prometheus) OnlineUserDec() {
	p.onlineUserGauge.Dec()
}

func (p *Prometheus) SendSystemMsgInc() {
	p.sendSystemMsgIncCounter.Inc()
}

func (p *Prometheus) Monitor(c *wkhttp.Context) {
	promhttp.Handler().ServeHTTP(c.Writer, c.Request)
}
