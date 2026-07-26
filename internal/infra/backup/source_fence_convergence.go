package backup

import (
	"context"
	"fmt"
	"sort"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

const sourceFenceConvergencePollInterval = 25 * time.Millisecond

// SourceFenceStateReader loads the locally visible Controller state.
type SourceFenceStateReader interface {
	LoadBackupCoordinationState(context.Context) (controller.ClusterState, error)
}

// ControllerSourceFenceConvergence waits for durable per-node Controller
// revision reports instead of trusting a fixed propagation delay.
type ControllerSourceFenceConvergence struct {
	reader SourceFenceStateReader
}

// NewControllerSourceFenceConvergence creates the Controller-backed barrier.
func NewControllerSourceFenceConvergence(
	reader SourceFenceStateReader,
) (*ControllerSourceFenceConvergence, error) {
	if reader == nil {
		return nil, fmt.Errorf("backup source fence convergence: state reader is required")
	}
	return &ControllerSourceFenceConvergence{reader: reader}, nil
}

// WaitForSourceFence returns only after every active or leaving data node has
// reported the fence revision while its ordinary runtime is not ready.
func (c *ControllerSourceFenceConvergence) WaitForSourceFence(
	ctx context.Context,
	record backupartifact.SourceFenceRecord,
) error {
	if c == nil || c.reader == nil ||
		backupartifact.ValidateSourceFenceRecord(record, false) != nil {
		return fmt.Errorf("backup source fence convergence: invalid fence")
	}
	ticker := time.NewTicker(sourceFenceConvergencePollInterval)
	defer ticker.Stop()
	var pending []uint64
	for {
		state, err := c.reader.LoadBackupCoordinationState(ctx)
		if err == nil {
			pending, err = pendingSourceFenceNodes(state, record)
			if err == nil && len(pending) == 0 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			if len(pending) > 0 {
				return fmt.Errorf(
					"backup source fence convergence: nodes %v: %w",
					pending, ctx.Err(),
				)
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func pendingSourceFenceNodes(
	state controller.ClusterState,
	record backupartifact.SourceFenceRecord,
) ([]uint64, error) {
	if state.Revision < record.FenceControllerRevision ||
		state.Backup == nil || state.Backup.SourceFence == nil ||
		state.Backup.SourceFence.ID != record.ID ||
		state.Backup.SourceFence.FenceControllerRevision !=
			record.FenceControllerRevision {
		return nil, fmt.Errorf("backup source fence convergence: Controller fence is not visible")
	}
	reports := make(map[uint64]controller.NodeHealthReport, len(state.NodeHealthReports))
	for _, report := range state.NodeHealthReports {
		reports[report.NodeID] = report
	}
	pending := make([]uint64, 0)
	for _, node := range state.Nodes {
		if !controllerNodeHasDataRole(node) ||
			(node.JoinState != "" &&
				node.JoinState != controller.NodeJoinStateActive &&
				node.JoinState != controller.NodeJoinStateLeaving) {
			continue
		}
		report, ok := reports[node.NodeID]
		if !ok ||
			report.ObservedControlRevision < record.FenceControllerRevision ||
			report.ReportedAtUnixMilli < record.RequestedAtUnixMillis ||
			report.RuntimeReady {
			pending = append(pending, node.NodeID)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i] < pending[j] })
	return pending, nil
}

func controllerNodeHasDataRole(node controller.Node) bool {
	for _, role := range node.Roles {
		if role == controller.NodeRoleData {
			return true
		}
	}
	return false
}
