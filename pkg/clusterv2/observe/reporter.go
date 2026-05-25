package observe

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

// SlotStatus is the compact Slot observation used by Reporter.
type SlotStatus struct {
	// SlotID is the physical Slot ID.
	SlotID uint32
	// Leader is the observed Slot leader.
	Leader uint64
}

// Controller receives low-frequency node and Slot reports.
type Controller interface {
	// ReportNode reports local node state.
	ReportNode(context.Context, control.NodeReport) error
	// ReportSlots reports local Slot runtime state.
	ReportSlots(context.Context, control.SlotRuntimeReport) error
}

// ReporterConfig wires a Reporter.
type ReporterConfig struct {
	// NodeID is the local node ID.
	NodeID uint64
	// Addr is the local cluster RPC address.
	Addr string
	// Controller receives reports.
	Controller Controller
	// SlotStatus returns current local Slot statuses.
	SlotStatus func() []SlotStatus
}

// Reporter sends low-frequency runtime reports to the control adapter.
type Reporter struct {
	cfg ReporterConfig
}

// NewReporter creates a Reporter.
func NewReporter(cfg ReporterConfig) *Reporter { return &Reporter{cfg: cfg} }

// ReportNode reports local node state.
func (r *Reporter) ReportNode(ctx context.Context) error {
	if r == nil || r.cfg.Controller == nil {
		return nil
	}
	return r.cfg.Controller.ReportNode(ctx, control.NodeReport{NodeID: r.cfg.NodeID, Addr: r.cfg.Addr, Status: control.NodeAlive})
}

// ReportSlots reports local Slot runtime state.
func (r *Reporter) ReportSlots(ctx context.Context) error {
	if r == nil || r.cfg.Controller == nil {
		return nil
	}
	var statuses []SlotStatus
	if r.cfg.SlotStatus != nil {
		statuses = r.cfg.SlotStatus()
	}
	views := make([]control.SlotRuntimeView, 0, len(statuses))
	for _, status := range statuses {
		views = append(views, control.SlotRuntimeView{SlotID: status.SlotID, Leader: status.Leader})
	}
	return r.cfg.Controller.ReportSlots(ctx, control.SlotRuntimeReport{NodeID: r.cfg.NodeID, Slots: views})
}
