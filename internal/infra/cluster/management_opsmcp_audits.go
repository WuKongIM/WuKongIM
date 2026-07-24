package cluster

import (
	"context"
	"sort"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	opscontract "github.com/WuKongIM/WuKongIM/internal/contracts/opsmcp"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

const (
	opsMCPAuditFanoutConcurrency = 8
	opsMCPAuditFanoutTimeout     = 2 * time.Second
)

// ManagementOpsMCPAuditNode exposes cluster identity, state, and typed peer RPC.
type ManagementOpsMCPAuditNode interface {
	NodeID() uint64
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// ManagementOpsMCPAuditReader aggregates bounded node-local audit rings.
type ManagementOpsMCPAuditReader struct {
	node    ManagementOpsMCPAuditNode
	local   managementusecase.OpsMCPAuditReader
	remote  *accessnode.Client
	timeout time.Duration
}

// NewManagementOpsMCPAuditReader creates a best-effort cluster audit aggregator.
func NewManagementOpsMCPAuditReader(node ManagementOpsMCPAuditNode, local managementusecase.OpsMCPAuditReader) *ManagementOpsMCPAuditReader {
	return &ManagementOpsMCPAuditReader{
		node: node, local: local, remote: accessnode.NewClient(node), timeout: opsMCPAuditFanoutTimeout,
	}
}

// RecentAudits returns newest-first aggregate summaries from currently alive
// and suspect nodes. Unavailable peers do not hide evidence from healthy peers.
func (r *ManagementOpsMCPAuditReader) RecentAudits(ctx context.Context, limit int) ([]opscontract.AuditEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || r.node == nil || limit < 1 || limit > opscontract.MaxAuditEntries {
		return []opscontract.AuditEntry{}, nil
	}
	nodeIDs := r.auditNodeIDs(ctx)
	fanoutCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	workerCount := opsMCPAuditWorkerCount(len(nodeIDs))
	jobs := make(chan uint64, workerCount)
	results := make(chan opsMCPAuditFanoutResult, workerCount)
	go func() {
		defer close(jobs)
		for _, nodeID := range nodeIDs {
			select {
			case jobs <- nodeID:
			case <-fanoutCtx.Done():
				return
			}
		}
	}()
	for range workerCount {
		go func() {
			defer func() {
				select {
				case results <- opsMCPAuditFanoutResult{workerDone: true}:
				case <-fanoutCtx.Done():
				}
			}()
			for nodeID := range jobs {
				var entries []opscontract.AuditEntry
				var err error
				if nodeID == r.node.NodeID() {
					if r.local == nil {
						continue
					}
					entries, err = r.local.RecentAudits(fanoutCtx, limit)
				} else {
					entries, err = r.remote.ReadOpsMCPAudits(fanoutCtx, nodeID, limit)
				}
				if err != nil {
					if fanoutCtx.Err() != nil {
						return
					}
					continue
				}
				for index := range entries {
					entries[index].RecorderNodeID = nodeID
				}
				select {
				case results <- opsMCPAuditFanoutResult{entries: entries}:
				case <-fanoutCtx.Done():
					return
				}
			}
		}()
	}

	merged := make([]opscontract.AuditEntry, 0, limit)
	completedWorkers := 0
	for completedWorkers < workerCount {
		select {
		case result := <-results:
			if result.workerDone {
				completedWorkers++
				continue
			}
			merged = mergeOpsMCPAuditEntries(merged, result.entries, limit)
		case <-fanoutCtx.Done():
			for {
				select {
				case result := <-results:
					if !result.workerDone {
						merged = mergeOpsMCPAuditEntries(merged, result.entries, limit)
					}
				default:
					return merged, nil
				}
			}
		}
	}
	return merged, nil
}

type opsMCPAuditFanoutResult struct {
	entries    []opscontract.AuditEntry
	workerDone bool
}

func opsMCPAuditWorkerCount(nodeCount int) int {
	if nodeCount < opsMCPAuditFanoutConcurrency {
		return nodeCount
	}
	return opsMCPAuditFanoutConcurrency
}

func mergeOpsMCPAuditEntries(
	current []opscontract.AuditEntry,
	incoming []opscontract.AuditEntry,
	limit int,
) []opscontract.AuditEntry {
	merged := make([]opscontract.AuditEntry, 0, min(limit, len(current)+len(incoming)))
	merged = append(merged, current...)
	merged = append(merged, incoming...)
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].StartedAt.Equal(merged[j].StartedAt) {
			return merged[i].RecorderNodeID < merged[j].RecorderNodeID
		}
		return merged[i].StartedAt.After(merged[j].StartedAt)
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

func (r *ManagementOpsMCPAuditReader) auditNodeIDs(ctx context.Context) []uint64 {
	snapshot, err := r.node.LocalControlSnapshot(ctx)
	if err != nil {
		return []uint64{r.node.NodeID()}
	}
	nodeIDs := make([]uint64, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.NodeID != 0 && (node.Status == control.NodeAlive || node.Status == control.NodeSuspect) {
			nodeIDs = append(nodeIDs, node.NodeID)
		}
	}
	if len(nodeIDs) == 0 && r.node.NodeID() != 0 {
		nodeIDs = append(nodeIDs, r.node.NodeID())
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	return nodeIDs
}

var _ managementusecase.OpsMCPAuditReader = (*ManagementOpsMCPAuditReader)(nil)
