package management

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const (
	// DefaultChannelDrainScanLimit bounds each Slot page during scale-in Channel inventory.
	DefaultChannelDrainScanLimit = 256
	// MaxChannelDrainScanLimit caps operator-provided Channel inventory page sizes.
	MaxChannelDrainScanLimit = 1024
)

var errChannelDrainCursorStalled = errors.New("channel drain inventory cursor did not advance")

// NodeChannelDrainInventoryRequest configures a target-node Channel drain scan.
type NodeChannelDrainInventoryRequest struct {
	// NodeID is the leaving node being checked.
	NodeID uint64
	// PageLimit bounds each physical Slot metadata scan page.
	PageLimit int
}

// NodeChannelDrainInventoryResponse reports Channel blockers for target-node removal.
type NodeChannelDrainInventoryResponse struct {
	// NodeID is the target node being checked.
	NodeID uint64
	// Safe reports that inventory is known and the target has no Channel role.
	Safe bool
	// Unknown reports that Channel inventory could not be proven.
	Unknown bool
	// ScannedSlotCount counts physical Slots scanned before the result was produced.
	ScannedSlotCount int
	// LeaderCount counts Channels led by the target node.
	LeaderCount int
	// ReplicaCount counts Channels where the target is a configured replica.
	ReplicaCount int
	// ISRCount counts Channels where the target is in ISR.
	ISRCount int
	// LastError contains the latest scan error text when inventory is unknown.
	LastError string
}

// NodeChannelDrainInventory scans authoritative Channel runtime metadata for target-node blockers.
func (a *App) NodeChannelDrainInventory(ctx context.Context, req NodeChannelDrainInventoryRequest) (NodeChannelDrainInventoryResponse, error) {
	if err := ctxErr(ctx); err != nil {
		return NodeChannelDrainInventoryResponse{}, err
	}
	if req.NodeID == 0 {
		return NodeChannelDrainInventoryResponse{}, metadb.ErrInvalidArgument
	}
	resp := NodeChannelDrainInventoryResponse{NodeID: req.NodeID}
	if a == nil || a.cluster == nil || a.channelRuntimeMeta == nil {
		resp.Unknown = true
		return resp, nil
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return NodeChannelDrainInventoryResponse{}, err
	}
	return a.nodeChannelDrainInventoryFromSnapshot(ctx, snapshot, req), nil
}

func (a *App) nodeChannelDrainInventoryFromSnapshot(ctx context.Context, snapshot control.Snapshot, req NodeChannelDrainInventoryRequest) NodeChannelDrainInventoryResponse {
	resp := NodeChannelDrainInventoryResponse{NodeID: req.NodeID}
	if a == nil || a.channelRuntimeMeta == nil {
		resp.Unknown = true
		return resp
	}
	limit := normalizeChannelDrainLimit(req.PageLimit)
	for _, slotID := range sortedSnapshotSlotIDs(snapshot.Slots) {
		resp.ScannedSlotCount++
		after := metadb.ChannelRuntimeMetaCursor{}
		for {
			page, nextCursor, done, err := a.channelRuntimeMeta.ScanChannelRuntimeMetaSlotPage(ctx, slotID, after, limit)
			if err != nil {
				resp.Unknown = true
				resp.Safe = false
				resp.LastError = err.Error()
				return resp
			}
			for _, meta := range page {
				countChannelDrainMeta(&resp, req.NodeID, meta)
			}
			if done {
				break
			}
			if nextCursor == after {
				resp.Unknown = true
				resp.Safe = false
				resp.LastError = errChannelDrainCursorStalled.Error()
				return resp
			}
			after = nextCursor
		}
	}
	resp.Safe = !resp.Unknown && resp.LeaderCount == 0 && resp.ReplicaCount == 0 && resp.ISRCount == 0
	return resp
}

func normalizeChannelDrainLimit(limit int) int {
	if limit <= 0 {
		return DefaultChannelDrainScanLimit
	}
	if limit > MaxChannelDrainScanLimit {
		return MaxChannelDrainScanLimit
	}
	return limit
}

func countChannelDrainMeta(resp *NodeChannelDrainInventoryResponse, targetNode uint64, meta metadb.ChannelRuntimeMeta) {
	if meta.Leader == targetNode {
		resp.LeaderCount++
	}
	if containsUint64(meta.Replicas, targetNode) {
		resp.ReplicaCount++
	}
	if containsUint64(meta.ISR, targetNode) {
		resp.ISRCount++
	}
}
