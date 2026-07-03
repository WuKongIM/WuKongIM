package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// ManagementLogNode exposes node-local and node-remote distributed log reads.
type ManagementLogNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
	// LocalControllerLogEntries reads this node's local Controller Raft log page.
	LocalControllerLogEntries(context.Context, cluster.LogEntriesOptions) (cluster.ControllerLogEntries, error)
	// LocalSlotLogEntries reads this node's local Slot Raft log page.
	LocalSlotLogEntries(context.Context, uint32, cluster.LogEntriesOptions) (cluster.SlotLogEntries, error)
}

// ManagementLogReader routes manager distributed log reads to selected nodes.
type ManagementLogReader struct {
	node   ManagementLogNode
	remote *accessnode.Client
}

// NewManagementLogReader creates a cluster-routed manager distributed log reader.
func NewManagementLogReader(node ManagementLogNode) *ManagementLogReader {
	return &ManagementLogReader{
		node:   node,
		remote: accessnode.NewClient(node),
	}
}

// ControllerLogEntries reads one node's local Controller Raft log page.
func (r *ManagementLogReader) ControllerLogEntries(ctx context.Context, req managementusecase.ListControllerLogEntriesRequest) (managementusecase.ControllerLogEntriesResponse, error) {
	if r == nil || r.node == nil || r.remote == nil {
		return managementusecase.ControllerLogEntriesResponse{}, managementusecase.ErrLogReaderUnavailable
	}
	if req.NodeID == r.node.NodeID() {
		page, err := r.node.LocalControllerLogEntries(ctx, cluster.LogEntriesOptions{Limit: req.Limit, Cursor: req.Cursor})
		if err != nil {
			return managementusecase.ControllerLogEntriesResponse{}, err
		}
		return controllerLogPageFromCluster(page), nil
	}
	return r.remote.GetManagerControllerLogEntries(ctx, req)
}

// SlotLogEntries reads one node's local Slot Raft log page.
func (r *ManagementLogReader) SlotLogEntries(ctx context.Context, req managementusecase.ListSlotLogEntriesRequest) (managementusecase.SlotLogEntriesResponse, error) {
	if r == nil || r.node == nil || r.remote == nil {
		return managementusecase.SlotLogEntriesResponse{}, managementusecase.ErrLogReaderUnavailable
	}
	if req.NodeID == r.node.NodeID() {
		page, err := r.node.LocalSlotLogEntries(ctx, req.SlotID, cluster.LogEntriesOptions{Limit: req.Limit, Cursor: req.Cursor})
		if err != nil {
			return managementusecase.SlotLogEntriesResponse{}, err
		}
		return slotLogPageFromCluster(page), nil
	}
	return r.remote.GetManagerSlotLogEntries(ctx, req)
}

func controllerLogPageFromCluster(page cluster.ControllerLogEntries) managementusecase.ControllerLogEntriesResponse {
	return managementusecase.ControllerLogEntriesResponse{
		NodeID:       page.NodeID,
		FirstIndex:   page.FirstIndex,
		LastIndex:    page.LastIndex,
		CommitIndex:  page.CommitIndex,
		AppliedIndex: page.AppliedIndex,
		NextCursor:   page.NextCursor,
		Items:        managementLogEntriesFromCluster[managementusecase.ControllerLogEntry](page.Items),
	}
}

func slotLogPageFromCluster(page cluster.SlotLogEntries) managementusecase.SlotLogEntriesResponse {
	return managementusecase.SlotLogEntriesResponse{
		NodeID:       page.NodeID,
		SlotID:       page.SlotID,
		FirstIndex:   page.FirstIndex,
		LastIndex:    page.LastIndex,
		CommitIndex:  page.CommitIndex,
		AppliedIndex: page.AppliedIndex,
		NextCursor:   page.NextCursor,
		Items:        managementLogEntriesFromCluster[managementusecase.SlotLogEntry](page.Items),
	}
}

func managementLogEntriesFromCluster[T ~struct {
	Index        uint64
	Term         uint64
	Type         string
	CreatedAtMS  int64
	DataSize     int
	DecodeStatus string
	DecodedType  string
	Decoded      map[string]any
}](items []cluster.LogEntry) []T {
	out := make([]T, 0, len(items))
	for _, item := range items {
		out = append(out, T(managementusecase.LogEntry{
			Index:        item.Index,
			Term:         item.Term,
			Type:         item.Type,
			CreatedAtMS:  item.CreatedAtMS,
			DataSize:     item.DataSize,
			DecodeStatus: item.DecodeStatus,
			DecodedType:  item.DecodedType,
			Decoded:      cloneLogDecoded(item.Decoded),
		}))
	}
	return out
}

func cloneLogDecoded(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
