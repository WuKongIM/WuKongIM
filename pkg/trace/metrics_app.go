package trace

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type appMetrics struct {
	ctx  context.Context
	opts *Options
	wklog.Log
	connCount          atomic.Int64
	onlineUserCount    atomic.Int64
	onlineDeviceCount  atomic.Int64
	messageLatency     metric.Int64Histogram
	pingBytes          atomic.Int64
	pingCount          atomic.Int64
	pongBytes          atomic.Int64
	pongCount          atomic.Int64
	sendPacketBytes    atomic.Int64
	sendPacketCount    atomic.Int64
	sendackPacketBytes atomic.Int64
	sendackPacketCount atomic.Int64
	recvPacketBytes    atomic.Int64
	recvPacketCount    atomic.Int64
	recvackPacketBytes atomic.Int64
	recvackPacketCount atomic.Int64
	connPacketBytes    atomic.Int64
	connPacketCount    atomic.Int64
	connackPacketBytes atomic.Int64
	connackPacketCount atomic.Int64
}

func newAppMetrics(opts *Options) *appMetrics {
	a := &appMetrics{
		ctx:  context.Background(),
		opts: opts,
		Log:  wklog.NewWKLog("appMetrics"),
	}

	connCount := NewInt64ObservableCounter("app_conn_count")
	onlineUserCount := NewInt64ObservableCounter("app_online_user_count")
	onlineDeviceCount := NewInt64ObservableCounter("app_online_device_count")
	pingBytes := NewInt64ObservableCounter("app_ping_bytes")
	pingCount := NewInt64ObservableCounter("app_ping_count")
	pongBytes := NewInt64ObservableCounter("app_pong_bytes")
	pongCount := NewInt64ObservableCounter("app_pong_count")
	sendPacketBytes := NewInt64ObservableCounter("app_send_packet_bytes")
	sendPacketCount := NewInt64ObservableCounter("app_send_packet_count")
	sendackPacketBytes := NewInt64ObservableCounter("app_sendack_packet_bytes")
	sendackPacketCount := NewInt64ObservableCounter("app_sendack_packet_count")
	recvPacketBytes := NewInt64ObservableCounter("app_recv_packet_bytes")
	recvPacketCount := NewInt64ObservableCounter("app_recv_packet_count")
	recvackPacketBytes := NewInt64ObservableCounter("app_recvack_packet_bytes")
	recvackPacketCount := NewInt64ObservableCounter("app_recvack_packet_count")
	connPacketBytes := NewInt64ObservableCounter("app_conn_packet_bytes")
	connPacketCount := NewInt64ObservableCounter("app_conn_packet_count")
	connackPacketBytes := NewInt64ObservableCounter("app_connack_packet_bytes")
	connackPacketCount := NewInt64ObservableCounter("app_connack_packet_count")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(connCount, a.connCount.Load())
		obs.ObserveInt64(onlineUserCount, a.onlineUserCount.Load())
		obs.ObserveInt64(onlineDeviceCount, a.onlineDeviceCount.Load())
		obs.ObserveInt64(pingBytes, a.pingBytes.Load())
		obs.ObserveInt64(pingCount, a.pingCount.Load())
		obs.ObserveInt64(pongBytes, a.pongBytes.Load())
		obs.ObserveInt64(pongCount, a.pongCount.Load())
		obs.ObserveInt64(sendPacketBytes, a.sendPacketBytes.Load())
		obs.ObserveInt64(sendPacketCount, a.sendPacketCount.Load())
		obs.ObserveInt64(sendackPacketBytes, a.sendackPacketBytes.Load())
		obs.ObserveInt64(sendackPacketCount, a.sendackPacketCount.Load())
		obs.ObserveInt64(recvPacketBytes, a.recvPacketBytes.Load())
		obs.ObserveInt64(recvPacketCount, a.recvPacketCount.Load())
		obs.ObserveInt64(recvackPacketBytes, a.recvackPacketBytes.Load())
		obs.ObserveInt64(recvackPacketCount, a.recvackPacketCount.Load())
		obs.ObserveInt64(connPacketBytes, a.connPacketBytes.Load())
		obs.ObserveInt64(connPacketCount, a.connPacketCount.Load())
		obs.ObserveInt64(connackPacketBytes, a.connackPacketBytes.Load())
		obs.ObserveInt64(connackPacketCount, a.connackPacketCount.Load())
		return nil
	}, connCount, onlineUserCount, onlineDeviceCount, pingBytes, pingCount, pongBytes, pongCount, sendPacketBytes, sendPacketCount, sendackPacketBytes, sendackPacketCount, recvPacketBytes, recvPacketCount, recvackPacketBytes, recvackPacketCount, connPacketBytes, connPacketCount, connackPacketBytes, connackPacketCount)
	var err error
	a.messageLatency, err = meter.Int64Histogram("app_message_latency", metric.WithDescription("The latency of message processing in the app layer"), metric.WithUnit("ms"))
	if err != nil {
		a.Panic("Failed to create app_message_latency histogram", zap.Error(err))
	}
	return a
}

func (a *appMetrics) ConnCountAdd(v int64) {
	a.connCount.Add(v)
}

func (a *appMetrics) OnlineUserCountAdd(v int64) {
	a.onlineUserCount.Add(v)
}

func (a *appMetrics) OnlineUserCountSet(v int64) {
	a.onlineUserCount.Store(v)
}

func (a *appMetrics) OnlineDeviceCountAdd(v int64) {
	a.onlineDeviceCount.Add(v)
}

func (a *appMetrics) OnlineDeviceCountSet(v int64) {
	a.onlineDeviceCount.Store(v)
}

func (a *appMetrics) MessageLatencyOb(v int64) {
	a.messageLatency.Record(a.ctx, v)
}

func (a *appMetrics) PingBytesAdd(v int64) {
	a.pingBytes.Add(v)
}

func (a *appMetrics) PingBytes() int64 {
	return a.pingBytes.Load()
}

func (a *appMetrics) PingCountAdd(v int64) {
	a.pingCount.Add(v)
}

func (a *appMetrics) PingCount() int64 {
	return a.pingCount.Load()
}

func (a *appMetrics) PongBytesAdd(v int64) {
	a.pongBytes.Add(v)
}

func (a *appMetrics) PongBytes() int64 {
	return a.pongBytes.Load()
}

func (a *appMetrics) PongCountAdd(v int64) {
	a.pongCount.Add(v)
}

func (a *appMetrics) PongCount() int64 {
	return a.pongCount.Load()
}

func (a *appMetrics) SendPacketBytesAdd(v int64) {
	a.sendPacketBytes.Add(v)
}

func (a *appMetrics) SendPacketBytes() int64 {
	return a.sendPacketBytes.Load()
}

func (a *appMetrics) SendPacketCountAdd(v int64) {
	a.sendPacketCount.Add(v)
}

func (a *appMetrics) SendPacketCount() int64 {
	return a.sendPacketCount.Load()
}

func (a *appMetrics) SendackPacketBytesAdd(v int64) {
	a.sendackPacketBytes.Add(v)
}

func (a *appMetrics) SendackPacketBytes() int64 {
	return a.sendackPacketBytes.Load()
}

func (a *appMetrics) SendackPacketCount() int64 {
	return a.sendackPacketCount.Load()
}

func (a *appMetrics) SendackPacketCountAdd(v int64) {
	a.sendackPacketCount.Add(v)
}

func (a *appMetrics) RecvackPacketBytes() int64 {
	return a.recvackPacketBytes.Load()
}

func (a *appMetrics) RecvackPacketBytesAdd(v int64) {
	a.recvackPacketBytes.Add(v)
}

func (a *appMetrics) RecvackPacketCountAdd(v int64) {
	a.recvackPacketCount.Add(v)
}

func (a *appMetrics) RecvackPacketCount() int64 {
	return a.recvackPacketCount.Load()
}

func (a *appMetrics) RecvPacketBytesAdd(v int64) {
	a.recvPacketBytes.Add(v)
}

func (a *appMetrics) RecvPacketBytes() int64 {
	return a.recvPacketBytes.Load()
}

func (a *appMetrics) RecvPacketCount() int64 {
	return a.recvPacketCount.Load()
}

func (a *appMetrics) RecvPacketCountAdd(v int64) {
	a.recvPacketCount.Add(v)
}

func (a *appMetrics) ConnPacketBytesAdd(v int64) {
	a.connPacketBytes.Add(v)
}

func (a *appMetrics) ConnPacketBytes() int64 {
	return a.connPacketBytes.Load()
}

func (a *appMetrics) ConnPacketCountAdd(v int64) {
	a.connPacketCount.Add(v)
}
func (a *appMetrics) ConnPacketCount() int64 {
	return a.connPacketCount.Load()
}

func (a *appMetrics) ConnackPacketBytesAdd(v int64) {
	a.connackPacketBytes.Add(v)
}

func (a *appMetrics) ConnackPacketBytes() int64 {
	return a.connackPacketBytes.Load()
}

func (a *appMetrics) ConnackPacketCountAdd(v int64) {
	a.connackPacketCount.Add(v)
}

func (a *appMetrics) ConnackPacketCount() int64 {
	return a.connackPacketCount.Load()
}
