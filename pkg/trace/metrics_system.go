package trace

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/shirou/gopsutil/cpu"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type systemMetrics struct {
	opts *Options
	ctx  context.Context
	wklog.Log

	intranetIncomingBytes atomic.Int64
	intranetOutgoingBytes atomic.Int64
	extranetIncomingBytes atomic.Int64
	extranetOutgoingBytes atomic.Int64
}

func newSystemMetrics(opts *Options) *systemMetrics {
	s := &systemMetrics{
		ctx:  context.Background(),
		opts: opts,
		Log:  wklog.NewWKLog("systemMetrics"),
	}

	intranetIncomingBytes := NewInt64ObservableCounter("system_intranet_incoming_bytes")
	intranetOutgoingBytes := NewInt64ObservableCounter("system_intranet_outgoing_bytes")
	extranetIncomingBytes := NewInt64ObservableCounter("system_extranet_incoming_bytes")
	extranetOutgoingBytes := NewInt64ObservableCounter("system_extranet_outgoing_bytes")

	cpuPercent := NewFloat64ObservableGauge("system_cpu_percent")

	RegisterCallback(func(ctx context.Context, obs metric.Observer) error {
		obs.ObserveInt64(intranetIncomingBytes, s.intranetIncomingBytes.Load())
		obs.ObserveInt64(intranetOutgoingBytes, s.intranetOutgoingBytes.Load())
		obs.ObserveInt64(extranetIncomingBytes, s.extranetIncomingBytes.Load())
		obs.ObserveInt64(extranetOutgoingBytes, s.extranetOutgoingBytes.Load())

		// 获取一段时间内的CPU使用率
		percentages, err := cpu.Percent(time.Second, false)
		if err != nil {
			s.Error("Error retrieving CPU usage", zap.Error(err))
		}
		obs.ObserveFloat64(cpuPercent, percentages[0])

		return nil
	}, intranetIncomingBytes, intranetOutgoingBytes, extranetIncomingBytes, extranetOutgoingBytes, cpuPercent)

	return s
}

// IntranetIncomingAdd 内网入口流量
func (s *systemMetrics) IntranetIncomingAdd(v int64) {
	s.intranetIncomingBytes.Add(v)
}

// IntranetOutgoingAdd 内网出口流量
func (s *systemMetrics) IntranetOutgoingAdd(v int64) {
	s.intranetOutgoingBytes.Add(v)
}

// ExtranetIncomingAdd 外网入口流量
func (s *systemMetrics) ExtranetIncomingAdd(v int64) {
	s.extranetIncomingBytes.Add(v)
}

// ExtranetOutgoingAdd 外网出口流量
func (s *systemMetrics) ExtranetOutgoingAdd(v int64) {
	s.extranetOutgoingBytes.Add(v)
}

// CPUUsageAdd CPU使用率
func (s *systemMetrics) CPUUsageAdd(v float64) {

}

// MemoryUsageAdd 内存使用率
func (s *systemMetrics) MemoryUsageAdd(v float64) {

}

// DiskIOReadCountAdd 磁盘读取次数
func (s *systemMetrics) DiskIOReadCountAdd(v int64) {

}

// DiskIOWriteCountAdd 磁盘写入次数
func (s *systemMetrics) DiskIOWriteCountAdd(v int64) {

}
