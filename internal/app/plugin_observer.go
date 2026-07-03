package app

import (
	"time"

	"github.com/WuKongIM/WuKongIM/internal/runtime/pluginhook"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

const (
	pluginHookMethodPersistAfter = "persist_after"
	pluginHookMethodReceive      = "receive"
	pluginHookMethodSend         = "send"
)

type pluginHookMetricsObserver struct {
	metrics *obsmetrics.Registry
}

func (a *App) pluginHookObserver() pluginhook.Observer {
	if a == nil || a.metrics == nil {
		return nil
	}
	return pluginHookMetricsObserver{metrics: a.metrics}
}

func (a *App) pluginUsecaseObserver() pluginusecase.Observer {
	if a == nil || a.metrics == nil {
		return nil
	}
	return pluginHookMetricsObserver{metrics: a.metrics}
}

func (o pluginHookMetricsObserver) ObservePersistAfterEnqueue(result string, wait time.Duration) {
	if o.metrics == nil || o.metrics.Plugin == nil {
		return
	}
	o.metrics.Plugin.ObserveHookEnqueue(pluginHookMethodPersistAfter, result, wait)
}

func (o pluginHookMetricsObserver) ObserveReceiveEnqueue(result string, wait time.Duration) {
	if o.metrics == nil || o.metrics.Plugin == nil {
		return
	}
	o.metrics.Plugin.ObserveHookEnqueue(pluginHookMethodReceive, result, wait)
}

func (o pluginHookMetricsObserver) ObservePersistAfterInvoke(result string, d time.Duration) {
	if o.metrics == nil || o.metrics.Plugin == nil {
		return
	}
	o.metrics.Plugin.ObserveHookInvoke(pluginHookMethodPersistAfter, result, d)
}

func (o pluginHookMetricsObserver) ObserveSendInvoke(result string, d time.Duration) {
	if o.metrics == nil || o.metrics.Plugin == nil {
		return
	}
	o.metrics.Plugin.ObserveHookInvoke(pluginHookMethodSend, result, d)
}

func (o pluginHookMetricsObserver) ObserveReceiveInvoke(result string, d time.Duration) {
	if o.metrics == nil || o.metrics.Plugin == nil {
		return
	}
	o.metrics.Plugin.ObserveHookInvoke(pluginHookMethodReceive, result, d)
}

var _ pluginhook.Observer = pluginHookMetricsObserver{}
var _ pluginusecase.Observer = pluginHookMetricsObserver{}
