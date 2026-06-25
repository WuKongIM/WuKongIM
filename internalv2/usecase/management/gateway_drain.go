package management

import (
	"context"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

// GatewayDrainWriter toggles gateway admission for a target node and returns fresh runtime counters.
type GatewayDrainWriter interface {
	SetNodeDrainMode(context.Context, uint64, bool) (NodeRuntimeSummary, error)
}

// SetNodeDrainModeRequest configures target-node gateway drain mode.
type SetNodeDrainModeRequest struct {
	// NodeID identifies the target cluster node.
	NodeID uint64
	// Draining disables new gateway session admission when true.
	Draining bool
}

// SetNodeDrainModeResponse reports the target node runtime state after changing drain mode.
type SetNodeDrainModeResponse struct {
	// NodeID identifies the target cluster node.
	NodeID uint64
	// Draining reports whether gateway drain mode is enabled.
	Draining bool
	// AcceptingNewSessions reports whether gateway admission currently accepts new sessions.
	AcceptingNewSessions bool
	// GatewaySessions counts all gateway sessions, including unauthenticated sessions.
	GatewaySessions int
	// ActiveOnline counts active authenticated online connections.
	ActiveOnline int
	// ClosingOnline counts authenticated online connections that are closing but not fully removed.
	ClosingOnline int
	// TotalOnline counts all authenticated online connections tracked by the node.
	TotalOnline int
	// PendingActivations counts local sessions accepted but not yet authority-active.
	PendingActivations int
	// Unknown means runtime counters could not be read.
	Unknown bool
}

// SetNodeDrainMode toggles gateway admission drain mode through the configured writer.
func (a *App) SetNodeDrainMode(ctx context.Context, req SetNodeDrainModeRequest) (SetNodeDrainModeResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return SetNodeDrainModeResponse{}, err
	}
	if req.NodeID == 0 {
		return SetNodeDrainModeResponse{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.gatewayDrain == nil {
		return SetNodeDrainModeResponse{}, ErrNodeScaleInUnavailable
	}
	summary, err := a.gatewayDrain.SetNodeDrainMode(ctx, req.NodeID, req.Draining)
	if err != nil {
		return SetNodeDrainModeResponse{}, err
	}
	if summary.NodeID == 0 {
		summary.NodeID = req.NodeID
	}
	return SetNodeDrainModeResponse{
		NodeID:               summary.NodeID,
		Draining:             summary.Draining,
		AcceptingNewSessions: summary.AcceptingNewSessions,
		GatewaySessions:      summary.GatewaySessions,
		ActiveOnline:         summary.ActiveOnline,
		ClosingOnline:        summary.ClosingOnline,
		TotalOnline:          summary.TotalOnline,
		PendingActivations:   summary.PendingActivations,
		Unknown:              summary.Unknown,
	}, nil
}
