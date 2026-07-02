package observe

import (
	"context"
	"sync/atomic"

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
	// RuntimeReady reports whether foreground cluster traffic can be served.
	RuntimeReady func() bool
	// ObservedControlRevision returns the latest observed control-plane revision.
	ObservedControlRevision func() uint64
	// ObservedSlotRevision returns the latest observed local Slot runtime revision.
	ObservedSlotRevision func() uint64
	// SlotStatus returns current local Slot statuses.
	SlotStatus func() []SlotStatus
}

// Reporter sends low-frequency runtime reports to the control adapter.
type Reporter struct {
	cfg       ReporterConfig
	reportSeq atomic.Uint64
}

// NewReporter creates a Reporter.
func NewReporter(cfg ReporterConfig) *Reporter { return &Reporter{cfg: cfg} }

// ReportNode reports local node state and returns the report that was submitted.
func (r *Reporter) ReportNode(ctx context.Context) (control.NodeReport, error) {
	if r == nil || r.cfg.Controller == nil {
		return control.NodeReport{}, nil
	}
	seq := r.reportSeq.Add(1)
	report := control.NodeReport{
		NodeID:                  r.cfg.NodeID,
		Addr:                    r.cfg.Addr,
		Status:                  control.NodeAlive,
		RuntimeReady:            callBool(r.cfg.RuntimeReady),
		ObservedControlRevision: callUint64(r.cfg.ObservedControlRevision),
		ObservedSlotRevision:    callUint64(r.cfg.ObservedSlotRevision),
		ReportSeq:               seq,
	}
	return report, r.cfg.Controller.ReportNode(ctx, report)
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

func callBool(fn func() bool) bool {
	if fn == nil {
		return false
	}
	return fn()
}

func callUint64(fn func() uint64) uint64 {
	if fn == nil {
		return 0
	}
	return fn()
}
