package app

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/internal/runtime/online"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

type runtimeSummaryCollector struct {
	app *App
}

func (c runtimeSummaryCollector) localNodeSummary(context.Context) (accessnode.RuntimeSummary, error) {
	if c.app == nil {
		return accessnode.RuntimeSummary{Unknown: true}, nil
	}
	onlineSummary := c.app.onlineSummary()
	gatewaySummary := c.app.gatewaySummary()
	unknown := c.app.onlineRegistry == nil || c.app.gateway == nil
	return accessnode.RuntimeSummary{
		NodeID:               c.app.cfg.Node.ID,
		ActiveOnline:         onlineSummary.Active,
		ClosingOnline:        onlineSummary.Closing,
		TotalOnline:          onlineSummary.Total,
		GatewaySessions:      gatewaySummary.GatewaySessions,
		SessionsByListener:   mergeListenerCounts(onlineSummary.SessionsByListener, gatewaySummary.SessionsByListener),
		AcceptingNewSessions: gatewaySummary.AcceptingNewSessions,
		Draining:             c.app.nodeDrainState != nil && c.app.nodeDrainState.Draining(),
		Unknown:              unknown,
	}, nil
}

type managementRuntimeSummaryReader struct {
	collector  runtimeSummaryCollector
	nodeClient *accessnode.Client
}

func (r managementRuntimeSummaryReader) NodeRuntimeSummary(ctx context.Context, nodeID uint64) (managementusecase.NodeRuntimeSummary, error) {
	app := r.collector.app
	if app != nil && nodeID == app.cfg.Node.ID {
		summary, err := r.collector.localNodeSummary(ctx)
		return toManagementRuntimeSummary(summary), err
	}
	if r.nodeClient == nil {
		return managementusecase.NodeRuntimeSummary{NodeID: nodeID, Unknown: true}, nil
	}
	summary, err := r.nodeClient.RuntimeSummary(ctx, nodeID)
	if err != nil {
		return managementusecase.NodeRuntimeSummary{NodeID: nodeID, Unknown: true}, err
	}
	return toManagementRuntimeSummary(summary), nil
}

type nodeRuntimeSummaryProvider struct {
	collector runtimeSummaryCollector
}

func (p nodeRuntimeSummaryProvider) LocalRuntimeSummary(ctx context.Context) (accessnode.RuntimeSummary, error) {
	return p.collector.localNodeSummary(ctx)
}

func (a *App) onlineSummary() online.Summary {
	if a == nil || a.onlineRegistry == nil {
		return online.Summary{SessionsByListener: map[string]int{}}
	}
	return a.onlineRegistry.Summary()
}

func (a *App) gatewaySummary() accessnode.RuntimeSummary {
	if a == nil || a.gateway == nil {
		return accessnode.RuntimeSummary{SessionsByListener: map[string]int{}, Unknown: true}
	}
	summary := a.gateway.SessionSummary()
	return accessnode.RuntimeSummary{
		GatewaySessions:      summary.GatewaySessions,
		SessionsByListener:   summary.SessionsByListener,
		AcceptingNewSessions: summary.AcceptingNewSessions,
	}
}

func toManagementRuntimeSummary(summary accessnode.RuntimeSummary) managementusecase.NodeRuntimeSummary {
	return managementusecase.NodeRuntimeSummary{
		NodeID:               summary.NodeID,
		ActiveOnline:         summary.ActiveOnline,
		ClosingOnline:        summary.ClosingOnline,
		TotalOnline:          summary.TotalOnline,
		GatewaySessions:      summary.GatewaySessions,
		SessionsByListener:   cloneListenerCounts(summary.SessionsByListener),
		AcceptingNewSessions: summary.AcceptingNewSessions,
		Draining:             summary.Draining,
		Unknown:              summary.Unknown,
	}
}

func mergeListenerCounts(left, right map[string]int) map[string]int {
	out := cloneListenerCounts(left)
	if out == nil {
		out = make(map[string]int)
	}
	for key, value := range right {
		out[key] += value
	}
	return out
}

func cloneListenerCounts(values map[string]int) map[string]int {
	if len(values) == 0 {
		return map[string]int{}
	}
	out := make(map[string]int, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
