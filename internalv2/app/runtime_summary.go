package app

import (
	"context"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	gatewaycore "github.com/WuKongIM/WuKongIM/pkg/gateway/core"
)

type nodeRuntimeSummaryReader interface {
	NodeRuntimeSummary(context.Context, uint64) (managementusecase.NodeRuntimeSummary, error)
}

type gatewaySummaryRuntime interface {
	SessionSummary() gatewaycore.SessionSummary
}

type managementRuntimeSummaryReader struct {
	app         *App
	localNodeID uint64
	remote      nodeRuntimeSummaryReader
}

func (r managementRuntimeSummaryReader) NodeRuntimeSummary(ctx context.Context, nodeID uint64) (managementusecase.NodeRuntimeSummary, error) {
	if nodeID == r.localNodeID || r.localNodeID == 0 {
		return r.localRuntimeSummary(nodeID), nil
	}
	if r.remote == nil {
		return managementusecase.NodeRuntimeSummary{NodeID: nodeID, Unknown: true}, nil
	}
	return r.remote.NodeRuntimeSummary(ctx, nodeID)
}

func (r managementRuntimeSummaryReader) localRuntimeSummary(nodeID uint64) managementusecase.NodeRuntimeSummary {
	summary := managementusecase.NodeRuntimeSummary{
		NodeID:             nodeID,
		SessionsByListener: map[string]int{},
		Unknown:            true,
	}
	if r.app != nil {
		if snapshots, ok := r.app.cluster.(interface {
			LocalControlSnapshot(context.Context) (control.Snapshot, error)
		}); ok && snapshots != nil {
			if snapshot, err := snapshots.LocalControlSnapshot(context.Background()); err == nil {
				summary.ControlRevision = snapshot.Revision
			}
		}
	}
	if r.app == nil || (r.app.online == nil && r.app.gateway == nil) {
		return summary
	}
	if r.app.online != nil {
		onlineSummary := r.app.online.Snapshot()
		summary.ActiveOnline = onlineSummary.Active
		summary.TotalOnline = onlineSummary.Active + onlineSummary.Pending
	}
	if gatewayRuntime, ok := r.app.gateway.(gatewaySummaryRuntime); ok && gatewayRuntime != nil {
		gatewaySummary := gatewayRuntime.SessionSummary()
		summary.GatewaySessions = gatewaySummary.GatewaySessions
		summary.SessionsByListener = cloneRuntimeListenerCounts(gatewaySummary.SessionsByListener)
		summary.AcceptingNewSessions = gatewaySummary.AcceptingNewSessions
		summary.Draining = !gatewaySummary.AcceptingNewSessions
	}
	if summary.SessionsByListener == nil {
		summary.SessionsByListener = map[string]int{}
	}
	summary.Unknown = false
	return summary
}

func cloneRuntimeListenerCounts(values map[string]int) map[string]int {
	if len(values) == 0 {
		return map[string]int{}
	}
	out := make(map[string]int, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
