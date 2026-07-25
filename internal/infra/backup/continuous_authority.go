package backup

import (
	"context"
	"errors"
	"fmt"

	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// CaptureAuthorityNode exposes a fresh local Slot Raft leadership proof.
type CaptureAuthorityNode interface {
	ObserveBackupCaptureAuthority(context.Context, uint16) (clusterpkg.BackupCaptureAuthority, error)
}

// ClusterSlotCaptureAuthority proves that the local node is the current Slot Leader.
type ClusterSlotCaptureAuthority struct {
	node CaptureAuthorityNode
}

// NewClusterSlotCaptureAuthority creates a route-backed capture authority source.
func NewClusterSlotCaptureAuthority(node CaptureAuthorityNode) (*ClusterSlotCaptureAuthority, error) {
	if node == nil {
		return nil, fmt.Errorf("backup capture authority: cluster node is required")
	}
	return &ClusterSlotCaptureAuthority{node: node}, nil
}

// CurrentCaptureAuthority returns only a complete local-leader authority identity.
func (a *ClusterSlotCaptureAuthority) CurrentCaptureAuthority(ctx context.Context, hashSlot uint16) (runtimebackup.SlotCaptureAuthority, error) {
	if a == nil || a.node == nil {
		return runtimebackup.SlotCaptureAuthority{}, runtimebackup.ErrCaptureNotLeader
	}
	if err := ctx.Err(); err != nil {
		return runtimebackup.SlotCaptureAuthority{}, err
	}
	authority, err := a.node.ObserveBackupCaptureAuthority(ctx, hashSlot)
	if err != nil {
		if errors.Is(err, clusterpkg.ErrNoSlotLeader) ||
			errors.Is(err, clusterpkg.ErrRouteNotReady) ||
			errors.Is(err, clusterpkg.ErrNotLeader) {
			return runtimebackup.SlotCaptureAuthority{}, runtimebackup.ErrCaptureNotLeader
		}
		return runtimebackup.SlotCaptureAuthority{}, err
	}
	if authority.HashSlot != hashSlot || authority.SlotID == 0 ||
		authority.HolderNodeID == 0 || authority.LeaderTerm == 0 ||
		authority.ConfigEpoch == 0 {
		return runtimebackup.SlotCaptureAuthority{}, runtimebackup.ErrCaptureNotLeader
	}
	return runtimebackup.SlotCaptureAuthority{
		SlotID: authority.SlotID, LeaderTerm: authority.LeaderTerm,
		ConfigEpoch: authority.ConfigEpoch, HolderNodeID: authority.HolderNodeID,
	}, nil
}

var _ runtimebackup.SlotCaptureAuthoritySource = (*ClusterSlotCaptureAuthority)(nil)
