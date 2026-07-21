package node

import (
	"context"
	"errors"
	"fmt"

	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

// BackupMessageShardRPCServiceID is the direct source-node message capture service.
const BackupMessageShardRPCServiceID uint8 = clusternet.RPCBackupMessageShard

// BackupPartitionRPCServiceID is the Slot-leader partition capture service.
const BackupPartitionRPCServiceID uint8 = clusternet.RPCBackupPartition

// BackupRestoreTargetRPCServiceID inspects one node's recovery target storage.
const BackupRestoreTargetRPCServiceID uint8 = clusternet.RPCBackupRestoreTarget

// BackupRestoreInstallRPCServiceID installs one recovery partition locally.
const BackupRestoreInstallRPCServiceID uint8 = clusternet.RPCBackupRestoreInstall

// BackupRestoreVerifyRPCServiceID validates restored durable boundaries locally.
const BackupRestoreVerifyRPCServiceID uint8 = clusternet.RPCBackupRestoreVerify

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
	return encodeBackupMessageShardResponse(backupMessageShardRPCResponse{Status: backupMessageStatusForError(err), Objects: captured.Objects, Boundaries: captured.Boundaries})
}

// HandleBackupPartitionRPC captures one logical partition on the local Slot leader.
func (a *Adapter) HandleBackupPartitionRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeBackupPartitionRequest(payload)
	if err != nil {
		return nil, err
	}
	if a == nil || a.backupPartitions == nil {
		return encodeBackupPartitionResponse(backupPartitionRPCResponse{Status: rpcStatusRejected})
	}
	report, err := a.backupPartitions.CaptureBackupPartition(ctx, req.Capture)
	return encodeBackupPartitionResponse(backupPartitionRPCResponse{Status: backupMessageStatusForError(err), Report: report})
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
	return encodeBackupRestoreInstallResponse(backupRestoreInstallRPCResponse{Status: backupMessageStatusForError(err), Report: report})
}

// HandleBackupRestoreVerifyRPC checks one bounded batch of restored cuts.
func (a *Adapter) HandleBackupRestoreVerifyRPC(ctx context.Context, payload []byte) ([]byte, error) {
	request, err := decodeBackupRestoreVerifyRequest(payload)
	if err != nil {
		return nil, err
	}
	if a == nil || a.backupRestoreVerifier == nil {
		return encodeBackupRestoreVerifyResponse(backupRestoreVerifyRPCResponse{Status: rpcStatusRejected})
	}
	err = a.backupRestoreVerifier.VerifyLocalRestorePartition(ctx, request.HashSlot, request.MetadataSHA256, request.Boundaries)
	return encodeBackupRestoreVerifyResponse(backupRestoreVerifyRPCResponse{Status: backupMessageStatusForError(err)})
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
		Objects:    append([]backupartifact.ObjectEntry(nil), response.Objects...),
		Boundaries: append([]backupartifact.ChannelBoundary(nil), response.Boundaries...),
	}, nil
}

// CaptureBackupPartition asks one Slot leader to capture a logical partition.
func (c *Client) CaptureBackupPartition(ctx context.Context, nodeID uint64, request runtimebackup.CaptureRequest) (backupusecase.PartitionReport, error) {
	if c == nil || c.node == nil || nodeID == 0 {
		return backupusecase.PartitionReport{}, runtimebackup.ErrInvalidCapture
	}
	body, err := encodeBackupPartitionRequest(backupPartitionRPCRequest{Capture: request})
	if err != nil {
		return backupusecase.PartitionReport{}, err
	}
	responseBody, err := c.node.CallRPC(ctx, nodeID, BackupPartitionRPCServiceID, body)
	if err != nil {
		return backupusecase.PartitionReport{}, err
	}
	response, err := decodeBackupPartitionResponse(responseBody)
	if err != nil {
		return backupusecase.PartitionReport{}, err
	}
	if err := backupMessageErrorForStatus(response.Status); err != nil {
		return backupusecase.PartitionReport{}, err
	}
	return response.Report, nil
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

// VerifyBackupRestorePartition asks one node to validate a bounded batch of restored cuts.
func (c *Client) VerifyBackupRestorePartition(ctx context.Context, nodeID uint64, hashSlot uint16, metadataSHA256 string, boundaries []clusterpkg.RestoreVerifyBoundary) error {
	if c == nil || c.node == nil || nodeID == 0 {
		return runtimebackup.ErrInvalidCapture
	}
	body, err := encodeBackupRestoreVerifyRequest(backupRestoreVerifyRPCRequest{HashSlot: hashSlot, MetadataSHA256: metadataSHA256, Boundaries: boundaries})
	if err != nil {
		return err
	}
	responseBody, err := c.node.CallRPC(ctx, nodeID, BackupRestoreVerifyRPCServiceID, body)
	if err != nil {
		return err
	}
	response, err := decodeBackupRestoreVerifyResponse(responseBody)
	if err != nil {
		return err
	}
	return backupMessageErrorForStatus(response.Status)
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
