package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
	"github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

// ReportNodeHealthRequest stores one low-frequency node health report.
type ReportNodeHealthRequest struct {
	// NodeID is the reporting node identity.
	NodeID uint64
	// Status is the reported runtime health state.
	Status NodeStatus
	// RuntimeReady reports whether the node can serve foreground cluster traffic.
	RuntimeReady bool
	// ObservedControlRevision is the latest Controller revision observed by the node.
	ObservedControlRevision uint64
	// ObservedSlotRevision is the latest local Slot runtime revision observed by the node.
	ObservedSlotRevision uint64
	// ReportSeq is a node-local sequence used for diagnostics.
	ReportSeq uint64
	// ErrorCode is a bounded machine-readable runtime reason.
	ErrorCode string
}

// ReportNodeHealthResult describes the committed report outcome.
type ReportNodeHealthResult struct {
	// Updated is true when the report changed durable state without a logical revision bump.
	Updated bool
	// Changed is true when the proposal advanced the logical cluster-state revision.
	Changed bool
	// Revision is the resulting logical cluster-state revision.
	Revision uint64
	// AppliedRaftIndex is the resulting durable Raft applied index.
	AppliedRaftIndex uint64
}

// ReportNodeHealth persists a bounded node health report through Controller Raft.
func (r *Runtime) ReportNodeHealth(ctx context.Context, req ReportNodeHealthRequest) (ReportNodeHealthResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ReportNodeHealthResult{}, err
	}
	if r == nil || r.raft == nil {
		return ReportNodeHealthResult{}, ErrNotStarted
	}
	// gofail: var wkReportNodeHealthFault string
	// if err := gofailReportNodeHealthFault(wkReportNodeHealthFault, req.NodeID); err != nil { return ReportNodeHealthResult{}, err }
	nowFunc := r.cfg.Now
	if nowFunc == nil {
		nowFunc = time.Now
	}
	now := nowFunc()
	if now.IsZero() {
		now = time.Now()
	}
	report := state.NodeHealthReport{
		NodeID:                  req.NodeID,
		Status:                  state.NodeStatus(req.Status),
		RuntimeReady:            req.RuntimeReady,
		ObservedControlRevision: req.ObservedControlRevision,
		ObservedSlotRevision:    req.ObservedSlotRevision,
		ReportSeq:               req.ReportSeq,
		ReportedAtUnixMilli:     now.UTC().UnixMilli(),
		ErrorCode:               req.ErrorCode,
	}
	result, err := r.raft.ProposeResult(ctx, command.Command{
		Kind:       command.KindReportNodeHealth,
		IssuedAt:   now.UTC(),
		NodeHealth: &report,
	})
	if err != nil {
		return ReportNodeHealthResult{}, err
	}
	if err := r.publishFromState(ctx); err != nil {
		return ReportNodeHealthResult{}, err
	}
	return ReportNodeHealthResult{
		Updated:          result.Updated,
		Changed:          result.Changed,
		Revision:         result.Revision,
		AppliedRaftIndex: result.AppliedRaftIndex,
	}, nil
}

func gofailReportNodeHealthFault(raw string, nodeID uint64) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	target, message, ok := strings.Cut(raw, ":")
	if !ok {
		return fmt.Errorf("controller: %s", raw)
	}
	target = strings.TrimSpace(target)
	message = strings.TrimSpace(message)
	if message == "" {
		message = "node health report fault"
	}
	if target == "all" {
		return fmt.Errorf("controller: %s", message)
	}
	want, err := strconv.ParseUint(target, 10, 64)
	if err != nil || want != nodeID {
		return nil
	}
	return fmt.Errorf("controller: %s", message)
}
