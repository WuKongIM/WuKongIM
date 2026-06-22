package app

import (
	"time"

	"github.com/WuKongIM/WuKongIM/internalv2/runtime/pluginhook"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
	obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
)

const (
	pluginHookMethodPersistAfter = "persist_after"
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

var _ pluginhook.Observer = pluginHookMetricsObserver{}
var _ pluginusecase.Observer = pluginHookMetricsObserver{}
