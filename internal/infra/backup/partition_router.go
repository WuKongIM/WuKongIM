package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

// PartitionRouteNode resolves current Slot leadership for backup dispatch.
type PartitionRouteNode interface {
	NodeID() uint64
	RouteHashSlot(uint16) (clusterpkg.Route, error)
}

// RemotePartitionClient captures a logical partition on another Slot leader.
type RemotePartitionClient interface {
	CaptureBackupPartition(context.Context, uint64, runtimebackup.CaptureRequest) (backupusecase.PartitionReport, error)
}

// PartitionRouterOptions configures local and remote partition capture.
type PartitionRouterOptions struct {
	Node              PartitionRouteNode
	Local             *runtimebackup.DistributedWorker
	Remote            RemotePartitionClient
	ConfigFingerprint string
}

// PartitionRouter validates configuration agreement and routes work to the
// current Slot leader. The same object backs the local node RPC handler.
type PartitionRouter struct {
	node        PartitionRouteNode
	local       *runtimebackup.DistributedWorker
	remote      RemotePartitionClient
	fingerprint string
}

// NewPartitionRouter creates a fenced logical-partition router.
func NewPartitionRouter(options PartitionRouterOptions) (*PartitionRouter, error) {
	if options.Node == nil || options.Node.NodeID() == 0 || options.Local == nil || options.Remote == nil || !validConfigFingerprint(options.ConfigFingerprint) {
		return nil, fmt.Errorf("backup partition router: invalid options")
	}
	return &PartitionRouter{node: options.Node, local: options.Local, remote: options.Remote, fingerprint: options.ConfigFingerprint}, nil
}

// CaptureBackupPartition captures locally and is safe to expose through the
// node RPC adapter.
func (r *PartitionRouter) CaptureBackupPartition(ctx context.Context, request runtimebackup.CaptureRequest) (backupusecase.PartitionReport, error) {
	if r == nil || request.ConfigFingerprint != r.fingerprint {
		return backupusecase.PartitionReport{}, runtimebackup.ErrStaleCapture
	}
	route, err := r.node.RouteHashSlot(request.HashSlot)
	if err != nil {
		return backupusecase.PartitionReport{}, err
	}
	if route.Leader != r.node.NodeID() {
		return backupusecase.PartitionReport{}, runtimebackup.ErrStaleCapture
	}
	return r.local.Capture(ctx, request)
}

// Dispatch routes one partition request to the current Slot leader.
func (r *PartitionRouter) Dispatch(ctx context.Context, request runtimebackup.CaptureRequest) (backupusecase.PartitionReport, error) {
	if r == nil || request.ConfigFingerprint != r.fingerprint {
		return backupusecase.PartitionReport{}, runtimebackup.ErrStaleCapture
	}
	route, err := r.node.RouteHashSlot(request.HashSlot)
	if err != nil {
		return backupusecase.PartitionReport{}, err
	}
	if route.Leader == 0 {
		return backupusecase.PartitionReport{}, runtimebackup.ErrStaleCapture
	}
	if route.Leader == r.node.NodeID() {
		return r.CaptureBackupPartition(ctx, request)
	}
	return r.remote.CaptureBackupPartition(ctx, route.Leader, request)
}

func validConfigFingerprint(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

var _ interface {
	CaptureBackupPartition(context.Context, runtimebackup.CaptureRequest) (backupusecase.PartitionReport, error)
} = (*PartitionRouter)(nil)
