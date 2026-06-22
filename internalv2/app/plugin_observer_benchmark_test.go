package app

import (
	"testing"
	"time"

	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

func BenchmarkPluginMetricsObserverSendInvoke(b *testing.B) {
	observer := pluginHookMetricsObserver{metrics: obsmetrics.New(1, "n1")}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		observer.ObserveSendInvoke("ok", time.Microsecond)
	}
}
