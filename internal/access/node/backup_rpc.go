package node

import (
	"context"
	"errors"
	"fmt"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// BackupMessageShardRPCServiceID is the direct source-node message capture service.
const BackupMessageShardRPCServiceID uint8 = clusternet.RPCBackupMessageShard

// BackupRestoreTargetRPCServiceID inspects one node's recovery target storage.
const BackupRestoreTargetRPCServiceID uint8 = clusternet.RPCBackupRestoreTarget

// BackupRestoreInstallRPCServiceID installs one recovery partition locally.
const BackupRestoreInstallRPCServiceID uint8 = clusternet.RPCBackupRestoreInstall

// BackupCheckpointReplicaRPCServiceID receives final plaintext target snapshots.
const BackupCheckpointReplicaRPCServiceID uint8 = clusternet.RPCBackupCheckpointReplica

// HandleBackupMessageShardRPC captures one bounded local message shard.
func (a *Adapter) HandleBackupMessageShardRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeBackupMessageShardRequest(payload)
	if err != nil {
		return nil, err
	}
	if a == nil || a.backupMessages == nil {
		return encodeBackupMessageShardResponse(backupMessageShardRPCResponse{Status: rpcStatusRejected})
	}
	captured, err := a.backupMessages.CaptureMessageShard(ctx, req.Capture, req.Shard)
	return encodeBackupMessageShardResponse(backupMessageShardRPCResponse{
		Status: backupMessageStatusForError(err), Objects: captured.Objects, Boundaries: captured.Boundaries,
		MessageRecords: captured.MessageRecords, MaxMessageID: captured.MaxMessageID,
	})
}

// HandleBackupRestoreTargetRPC returns local semantic storage evidence.
func (a *Adapter) HandleBackupRestoreTargetRPC(ctx context.Context, payload []byte) ([]byte, error) {
	if err := decodeBackupRestoreTargetRequest(payload); err != nil {
		return nil, err
	}
	if a == nil || a.backupRestoreTarget == nil {
		return encodeBackupRestoreTargetResponse(backupRestoreTargetRPCResponse{Status: rpcStatusRejected})
	}
	state, err := a.backupRestoreTarget.InspectLocalRestoreTarget(ctx)
	return encodeBackupRestoreTargetResponse(backupRestoreTargetRPCResponse{Status: backupMessageStatusForError(err), State: state})
}

// HandleBackupRestoreInstallRPC installs one authenticated partition locally.
func (a *Adapter) HandleBackupRestoreInstallRPC(ctx context.Context, payload []byte) ([]byte, error) {
	request, err := decodeBackupRestoreInstallRequest(payload)
	if err != nil {
		return nil, err
	}
	if a == nil || a.backupRestoreInstaller == nil {
		return encodeBackupRestoreInstallResponse(backupRestoreInstallRPCResponse{Status: rpcStatusRejected})
	}
	report, err := a.backupRestoreInstaller.InstallPartition(ctx, request.Plan, request.HashSlot)
	if err != nil {
		a.rpcLogger().Warn(
			"backup restore install rpc rejected",
			wklog.Event("internal.access.node.backup_restore_install_rejected"),
			wklog.Uint64("hashSlot", uint64(request.HashSlot)),
			wklog.Error(err),
		)
	}
	return encodeBackupRestoreInstallResponse(backupRestoreInstallRPCResponse{Status: backupMessageStatusForError(err), Report: report})
}

// HandleBackupCheckpointReplicaRPC stages one bounded target snapshot step.
func (a *Adapter) HandleBackupCheckpointReplicaRPC(
	ctx context.Context,
	payload []byte,
) ([]byte, error) {
	request, err := decodeBackupCheckpointReplicaRequest(payload)
	if err != nil {
		return nil, err
	}
	if a == nil || a.backupCheckpointReplica == nil {
		return encodeBackupCheckpointReplicaResponse(
			backupCheckpointReplicaRPCResponse{Status: rpcStatusRejected},
		)
	}
	response, err := a.backupCheckpointReplica.HandleCheckpointReplica(
		ctx, request,
	)
	return encodeBackupCheckpointReplicaResponse(
		backupCheckpointReplicaRPCResponse{
			Status: backupMessageStatusForError(err), Response: response,
		},
	)
}

// CaptureBackupMessageShard asks one source node to upload a committed-message shard.
func (c *Client) CaptureBackupMessageShard(ctx context.Context, nodeID uint64, request runtimebackup.CaptureRequest, shard runtimebackup.MessageShard) (runtimebackup.MessageShardCapture, error) {
	if c == nil || c.node == nil || nodeID == 0 || shard.NodeID != nodeID {
		return runtimebackup.MessageShardCapture{}, runtimebackup.ErrInvalidCapture
	}
	body, err := encodeBackupMessageShardRequest(backupMessageShardRPCRequest{Capture: request, Shard: shard})
	if err != nil {
		return runtimebackup.MessageShardCapture{}, err
	}
	responseBody, err := c.node.CallRPC(ctx, nodeID, BackupMessageShardRPCServiceID, body)
	if err != nil {
		return runtimebackup.MessageShardCapture{}, err
	}
	response, err := decodeBackupMessageShardResponse(responseBody)
	if err != nil {
		return runtimebackup.MessageShardCapture{}, err
	}
	if err := backupMessageErrorForStatus(response.Status); err != nil {
		return runtimebackup.MessageShardCapture{}, err
	}
	return runtimebackup.MessageShardCapture{
		Objects:        append([]backupartifact.ObjectEntry(nil), response.Objects...),
		Boundaries:     append([]backupartifact.ChannelBoundary(nil), response.Boundaries...),
		MessageRecords: response.MessageRecords,
		MaxMessageID:   response.MaxMessageID,
	}, nil
}

// InspectBackupRestoreTarget asks one exact node to prove semantic storage emptiness.
func (c *Client) InspectBackupRestoreTarget(ctx context.Context, nodeID uint64) (clusterpkg.RestoreTargetLocalState, error) {
	if c == nil || c.node == nil || nodeID == 0 {
		return clusterpkg.RestoreTargetLocalState{}, runtimebackup.ErrInvalidCapture
	}
	body, err := encodeBackupRestoreTargetRequest()
	if err != nil {
		return clusterpkg.RestoreTargetLocalState{}, err
	}
	responseBody, err := c.node.CallRPC(ctx, nodeID, BackupRestoreTargetRPCServiceID, body)
	if err != nil {
		return clusterpkg.RestoreTargetLocalState{}, err
	}
	response, err := decodeBackupRestoreTargetResponse(responseBody)
	if err != nil {
		return clusterpkg.RestoreTargetLocalState{}, err
	}
	if err := backupMessageErrorForStatus(response.Status); err != nil {
		return clusterpkg.RestoreTargetLocalState{}, err
	}
	if response.State.NodeID != nodeID {
		return clusterpkg.RestoreTargetLocalState{}, fmt.Errorf("backup restore target node identity mismatch")
	}
	return response.State, nil
}

// InstallBackupRestorePartition asks one node to install an authenticated partition.
func (c *Client) InstallBackupRestorePartition(ctx context.Context, nodeID uint64, plan backupusecase.RestorePlan, hashSlot uint16) (backupusecase.RestorePartition, error) {
	if c == nil || c.node == nil || nodeID == 0 {
		return backupusecase.RestorePartition{}, runtimebackup.ErrInvalidCapture
	}
	body, err := encodeBackupRestoreInstallRequest(backupRestoreInstallRPCRequest{Plan: plan, HashSlot: hashSlot})
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	responseBody, err := c.node.CallRPC(ctx, nodeID, BackupRestoreInstallRPCServiceID, body)
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	response, err := decodeBackupRestoreInstallResponse(responseBody)
	if err != nil {
		return backupusecase.RestorePartition{}, err
	}
	if err := backupMessageErrorForStatus(response.Status); err != nil {
		return backupusecase.RestorePartition{}, err
	}
	if response.Report.HashSlot != hashSlot || !response.Report.Installed || !validBackupSHA256(response.Report.MetadataSHA256) {
		return backupusecase.RestorePartition{}, fmt.Errorf("backup restore install response mismatch")
	}
	return response.Report, nil
}

// HandleCheckpointReplica sends one bounded target-snapshot transfer step.
func (c *Client) HandleCheckpointReplica(
	ctx context.Context,
	nodeID uint64,
	request backupcontract.CheckpointReplicaRequest,
) (backupcontract.CheckpointReplicaResponse, error) {
	if c == nil || c.node == nil || nodeID == 0 {
		return backupcontract.CheckpointReplicaResponse{},
			runtimebackup.ErrInvalidCapture
	}
	body, err := encodeBackupCheckpointReplicaRequest(request)
	if err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	responseBody, err := c.node.CallRPC(
		ctx, nodeID, BackupCheckpointReplicaRPCServiceID, body,
	)
	if err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	response, err := decodeBackupCheckpointReplicaResponse(
		responseBody, request.Action,
	)
	if err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	if err := backupMessageErrorForStatus(response.Status); err != nil {
		return backupcontract.CheckpointReplicaResponse{}, err
	}
	return response.Response, nil
}

func backupMessageStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, runtimebackup.ErrInvalidCapture):
		return rpcStatusInvalidArgument
	case errors.Is(err, runtimebackup.ErrStaleCapture):
		return rpcStatusStaleRoute
	default:
		return rpcStatusRejected
	}
}

func backupMessageErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusInvalidArgument:
		return runtimebackup.ErrInvalidCapture
	case rpcStatusStaleRoute, rpcStatusNotLeader:
		return runtimebackup.ErrStaleCapture
	case rpcStatusRejected:
		return fmt.Errorf("backup message shard capture rejected")
	default:
		return fmt.Errorf("internal/access/node: unknown backup message RPC status %q", status)
	}
}
