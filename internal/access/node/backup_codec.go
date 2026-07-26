package node

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

var (
	backupMessageShardRequestMagic       = [...]byte{'W', 'K', 'V', 'B', 1}
	backupMessageShardResponseMagic      = [...]byte{'W', 'K', 'V', 'b', 2}
	backupPartitionRequestMagic          = [...]byte{'W', 'K', 'V', 'P', 1}
	backupPartitionResponseMagic         = [...]byte{'W', 'K', 'V', 'p', 1}
	backupRestoreTargetRequestMagic      = [...]byte{'W', 'K', 'V', 'R', 1}
	backupRestoreTargetResponseMagic     = [...]byte{'W', 'K', 'V', 'r', 1}
	backupRestoreInstallRequestMagic     = [...]byte{'W', 'K', 'V', 'I', 2}
	backupRestoreInstallResponseMagic    = [...]byte{'W', 'K', 'V', 'i', 2}
	backupRestoreVerifyRequestMagic      = [...]byte{'W', 'K', 'V', 'Y', 1}
	backupRestoreVerifyResponseMagic     = [...]byte{'W', 'K', 'V', 'y', 1}
	backupCheckpointReplicaRequestMagic  = [...]byte{'W', 'K', 'V', 'S', 1}
	backupCheckpointReplicaResponseMagic = [...]byte{'W', 'K', 'V', 's', 1}
)

const (
	maxBackupMessageShardRPCBytes = 8 << 20
	maxBackupMessageShardChannels = 4096
	maxBackupMessageShardObjects  = 8192
)

type backupMessageShardRPCRequest struct {
	Capture runtimebackup.CaptureRequest `json:"capture"`
	Shard   runtimebackup.MessageShard   `json:"shard"`
}

type backupMessageShardRPCResponse struct {
	Status         string                           `json:"status"`
	Objects        []backupartifact.ObjectEntry     `json:"objects"`
	Boundaries     []backupartifact.ChannelBoundary `json:"boundaries"`
	MessageRecords uint64                           `json:"message_records"`
	MaxMessageID   uint64                           `json:"max_message_id"`
}

type backupPartitionRPCRequest struct {
	Capture runtimebackup.CaptureRequest `json:"capture"`
}

type backupPartitionRPCResponse struct {
	Status string                        `json:"status"`
	Report backupusecase.PartitionReport `json:"report"`
}

type backupRestoreTargetRPCRequest struct{}

type backupRestoreTargetRPCResponse struct {
	Status string                             `json:"status"`
	State  clusterpkg.RestoreTargetLocalState `json:"state"`
}

type backupRestoreInstallRPCRequest struct {
	Plan     backupusecase.RestorePlan `json:"plan"`
	HashSlot uint16                    `json:"hash_slot"`
}

type backupRestoreInstallRPCResponse struct {
	Status string                         `json:"status"`
	Report backupusecase.RestorePartition `json:"report"`
}

type backupRestoreVerifyRPCRequest struct {
	HashSlot       uint16                             `json:"hash_slot"`
	MetadataSHA256 string                             `json:"metadata_sha256,omitempty"`
	Boundaries     []clusterpkg.RestoreVerifyBoundary `json:"boundaries"`
}

type backupRestoreVerifyRPCResponse struct {
	Status string `json:"status"`
}

type backupCheckpointReplicaRPCRequest struct {
	Request backupcontract.CheckpointReplicaRequest `json:"request"`
}

type backupCheckpointReplicaRPCResponse struct {
	Status   string                                   `json:"status"`
	Response backupcontract.CheckpointReplicaResponse `json:"response"`
}

func encodeBackupCheckpointReplicaRequest(
	request backupcontract.CheckpointReplicaRequest,
) ([]byte, error) {
	if err := validateBackupCheckpointReplicaRequest(request); err != nil {
		return nil, err
	}
	return encodeBackupJSON(
		backupCheckpointReplicaRequestMagic[:],
		backupCheckpointReplicaRPCRequest{Request: request},
	)
}

func decodeBackupCheckpointReplicaRequest(
	body []byte,
) (backupcontract.CheckpointReplicaRequest, error) {
	var envelope backupCheckpointReplicaRPCRequest
	if err := decodeBackupJSON(
		body, backupCheckpointReplicaRequestMagic[:], &envelope,
	); err != nil {
		return backupcontract.CheckpointReplicaRequest{}, err
	}
	return envelope.Request,
		validateBackupCheckpointReplicaRequest(envelope.Request)
}

func encodeBackupCheckpointReplicaResponse(
	response backupCheckpointReplicaRPCResponse,
) ([]byte, error) {
	return encodeBackupJSON(
		backupCheckpointReplicaResponseMagic[:], response,
	)
}

func decodeBackupCheckpointReplicaResponse(
	body []byte,
	action backupcontract.CheckpointReplicaAction,
) (backupCheckpointReplicaRPCResponse, error) {
	var response backupCheckpointReplicaRPCResponse
	if err := decodeBackupJSON(
		body, backupCheckpointReplicaResponseMagic[:], &response,
	); err != nil {
		return response, err
	}
	if response.Response.AcceptedOffset < 0 {
		return response,
			fmt.Errorf("internal/access/node: invalid checkpoint replica response")
	}
	if response.Status == rpcStatusOK {
		if action == backupcontract.CheckpointReplicaCleanup {
			if response.Response != (backupcontract.CheckpointReplicaResponse{
				Completed: true,
			}) {
				return response,
					fmt.Errorf("internal/access/node: invalid checkpoint replica cleanup response")
			}
			return response, nil
		}
		if response.Response.Completed {
			if response.Response.AcceptedOffset != 0 ||
				response.Response.InstalledBytes == 0 ||
				!validBackupSHA256(response.Response.MetadataSHA256) {
				return response,
					fmt.Errorf("internal/access/node: invalid completed checkpoint replica response")
			}
		} else if response.Response.MetadataSHA256 != "" ||
			response.Response.InstalledBytes != 0 {
			return response,
				fmt.Errorf("internal/access/node: invalid active checkpoint replica response")
		}
	}
	return response, nil
}

func validateBackupCheckpointReplicaRequest(
	request backupcontract.CheckpointReplicaRequest,
) error {
	fence := request.Fence
	if fence.PlanID == "" || fence.CheckpointID == "" ||
		!validBackupSHA256(fence.CheckpointSHA256) ||
		fence.TargetGeneration == "" || fence.TargetSlotID == 0 ||
		fence.ReplicaCount == 0 || fence.LeaderNodeID == 0 ||
		fence.LeaderTerm == 0 || fence.ConfigEpoch == 0 ||
		fence.Attempt == 0 {
		return fmt.Errorf("internal/access/node: invalid checkpoint replica fence")
	}
	switch request.Action {
	case backupcontract.CheckpointReplicaBegin:
		if len(request.Files) < 2 ||
			len(request.Files) > maxBackupMessageShardChannels ||
			request.File != (backupcontract.CheckpointReplicaFile{}) ||
			len(request.Data) != 0 || request.Offset != 0 ||
			request.InstalledAtUnixMillis <= 0 ||
			request.Evidence.Version != backupartifact.RestoreEvidenceVersion {
			return fmt.Errorf("internal/access/node: invalid checkpoint replica begin")
		}
		seen := make(map[string]struct{}, len(request.Files))
		for _, file := range request.Files {
			if err := validateBackupCheckpointReplicaFile(file); err != nil {
				return err
			}
			key := fmt.Sprintf("%s:%d", file.Kind, file.Ordinal)
			if _, found := seen[key]; found {
				return fmt.Errorf("internal/access/node: duplicate checkpoint replica file")
			}
			seen[key] = struct{}{}
		}
	case backupcontract.CheckpointReplicaChunk:
		if err := validateBackupCheckpointReplicaFile(request.File); err != nil {
			return err
		}
		if len(request.Files) != 0 || request.Offset < 0 ||
			len(request.Data) == 0 || len(request.Data) > 3<<20 ||
			request.Evidence != (backupartifact.RestoreEvidence{}) ||
			request.FinalMessageCount != 0 ||
			request.FinalMaxMessageID != 0 ||
			request.DownloadedBytes != 0 ||
			request.InstalledAtUnixMillis != 0 ||
			request.Offset > request.File.Size ||
			int64(len(request.Data)) > request.File.Size-request.Offset {
			return fmt.Errorf("internal/access/node: invalid checkpoint replica chunk")
		}
	case backupcontract.CheckpointReplicaCommit,
		backupcontract.CheckpointReplicaStatus,
		backupcontract.CheckpointReplicaCleanup:
		if len(request.Files) != 0 || len(request.Data) != 0 ||
			request.File != (backupcontract.CheckpointReplicaFile{}) ||
			request.Offset != 0 ||
			request.Evidence != (backupartifact.RestoreEvidence{}) ||
			request.FinalMessageCount != 0 ||
			request.FinalMaxMessageID != 0 ||
			request.DownloadedBytes != 0 ||
			request.InstalledAtUnixMillis != 0 {
			return fmt.Errorf("internal/access/node: invalid checkpoint replica terminal request")
		}
	default:
		return fmt.Errorf("internal/access/node: invalid checkpoint replica action")
	}
	return nil
}

func validateBackupCheckpointReplicaFile(
	file backupcontract.CheckpointReplicaFile,
) error {
	switch file.Kind {
	case backupcontract.CheckpointReplicaMetadata,
		backupcontract.CheckpointReplicaMessages,
		backupcontract.CheckpointReplicaErasures:
	default:
		return fmt.Errorf("internal/access/node: invalid checkpoint replica file kind")
	}
	if file.Size < 0 || !validBackupSHA256(file.SHA256) ||
		(file.Kind != backupcontract.CheckpointReplicaMessages &&
			file.Ordinal != 0) {
		return fmt.Errorf("internal/access/node: invalid checkpoint replica file")
	}
	return nil
}

func encodeBackupRestoreVerifyRequest(request backupRestoreVerifyRPCRequest) ([]byte, error) {
	if len(request.Boundaries) > maxBackupMessageShardChannels {
		return nil, fmt.Errorf("internal/access/node: restore verify batch exceeds limit")
	}
	if request.MetadataSHA256 != "" && !validBackupSHA256(request.MetadataSHA256) {
		return nil, fmt.Errorf("internal/access/node: restore verify metadata digest is invalid")
	}
	return encodeBackupJSON(backupRestoreVerifyRequestMagic[:], request)
}

func decodeBackupRestoreVerifyRequest(body []byte) (backupRestoreVerifyRPCRequest, error) {
	var request backupRestoreVerifyRPCRequest
	if err := decodeBackupJSON(body, backupRestoreVerifyRequestMagic[:], &request); err != nil {
		return request, err
	}
	if len(request.Boundaries) > maxBackupMessageShardChannels {
		return request, fmt.Errorf("internal/access/node: restore verify batch exceeds limit")
	}
	if request.MetadataSHA256 != "" && !validBackupSHA256(request.MetadataSHA256) {
		return request, fmt.Errorf("internal/access/node: restore verify metadata digest is invalid")
	}
	return request, nil
}

func validBackupSHA256(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func encodeBackupRestoreVerifyResponse(response backupRestoreVerifyRPCResponse) ([]byte, error) {
	return encodeBackupJSON(backupRestoreVerifyResponseMagic[:], response)
}

func decodeBackupRestoreVerifyResponse(body []byte) (backupRestoreVerifyRPCResponse, error) {
	var response backupRestoreVerifyRPCResponse
	return response, decodeBackupJSON(body, backupRestoreVerifyResponseMagic[:], &response)
}

func encodeBackupRestoreInstallRequest(request backupRestoreInstallRPCRequest) ([]byte, error) {
	if err := validateBackupRestoreInstallRequest(request); err != nil {
		return nil, err
	}
	return encodeBackupJSON(backupRestoreInstallRequestMagic[:], request)
}

func decodeBackupRestoreInstallRequest(body []byte) (backupRestoreInstallRPCRequest, error) {
	var request backupRestoreInstallRPCRequest
	if err := decodeBackupJSON(body, backupRestoreInstallRequestMagic[:], &request); err != nil {
		return request, err
	}
	return request, validateBackupRestoreInstallRequest(request)
}

func validateBackupRestoreInstallRequest(request backupRestoreInstallRPCRequest) error {
	plan := request.Plan
	if plan.ID == "" || plan.RestorePointID == "" || len(plan.ManifestSHA256) != 64 || plan.HashSlotCount == 0 || request.HashSlot >= plan.HashSlotCount ||
		(plan.Repository != "primary" && plan.Repository != "secondary") || len(plan.Partitions) != int(plan.HashSlotCount) ||
		plan.ErasureLedgerVersion != backupartifact.ErasureLedgerSnapshotVersion || !validBackupSHA256(plan.ErasureLedgerSHA256) {
		return fmt.Errorf("internal/access/node: invalid restore install request")
	}
	var boundary uint64
	for index, head := range plan.ErasureHeads {
		if head.HashSlot >= plan.HashSlotCount || backupartifact.ValidateErasureStreamHead(head) != nil ||
			(index > 0 && plan.ErasureHeads[index-1].HashSlot >= head.HashSlot) ||
			head.Sequence > uint64(backupartifact.MaxErasureLedgerEvents)-boundary {
			return fmt.Errorf("internal/access/node: invalid restore erasure stream heads")
		}
		boundary += head.Sequence
	}
	if boundary != plan.ErasureEventCount {
		return fmt.Errorf("internal/access/node: invalid restore erasure stream boundary")
	}
	return nil
}

func encodeBackupRestoreInstallResponse(response backupRestoreInstallRPCResponse) ([]byte, error) {
	return encodeBackupJSON(backupRestoreInstallResponseMagic[:], response)
}

func decodeBackupRestoreInstallResponse(body []byte) (backupRestoreInstallRPCResponse, error) {
	var response backupRestoreInstallRPCResponse
	return response, decodeBackupJSON(body, backupRestoreInstallResponseMagic[:], &response)
}

func encodeBackupRestoreTargetRequest() ([]byte, error) {
	return encodeBackupJSON(backupRestoreTargetRequestMagic[:], backupRestoreTargetRPCRequest{})
}

func decodeBackupRestoreTargetRequest(body []byte) error {
	var request backupRestoreTargetRPCRequest
	return decodeBackupJSON(body, backupRestoreTargetRequestMagic[:], &request)
}

func encodeBackupRestoreTargetResponse(response backupRestoreTargetRPCResponse) ([]byte, error) {
	return encodeBackupJSON(backupRestoreTargetResponseMagic[:], response)
}

func decodeBackupRestoreTargetResponse(body []byte) (backupRestoreTargetRPCResponse, error) {
	var response backupRestoreTargetRPCResponse
	return response, decodeBackupJSON(body, backupRestoreTargetResponseMagic[:], &response)
}

func encodeBackupPartitionRequest(req backupPartitionRPCRequest) ([]byte, error) {
	if req.Capture.JobID == "" || req.Capture.BackupEpoch == 0 || len(req.Capture.ConfigFingerprint) != 64 {
		return nil, fmt.Errorf("internal/access/node: invalid backup partition request")
	}
	return encodeBackupJSON(backupPartitionRequestMagic[:], req)
}

func decodeBackupPartitionRequest(body []byte) (backupPartitionRPCRequest, error) {
	var req backupPartitionRPCRequest
	if err := decodeBackupJSON(body, backupPartitionRequestMagic[:], &req); err != nil {
		return req, err
	}
	if req.Capture.JobID == "" || req.Capture.BackupEpoch == 0 || len(req.Capture.ConfigFingerprint) != 64 {
		return req, fmt.Errorf("internal/access/node: invalid backup partition request")
	}
	return req, nil
}

func encodeBackupPartitionResponse(resp backupPartitionRPCResponse) ([]byte, error) {
	return encodeBackupJSON(backupPartitionResponseMagic[:], resp)
}

func decodeBackupPartitionResponse(body []byte) (backupPartitionRPCResponse, error) {
	var resp backupPartitionRPCResponse
	if err := decodeBackupJSON(body, backupPartitionResponseMagic[:], &resp); err != nil {
		return resp, err
	}
	return resp, nil
}

func encodeBackupMessageShardRequest(req backupMessageShardRPCRequest) ([]byte, error) {
	if err := validateBackupMessageShardRequest(req); err != nil {
		return nil, err
	}
	return encodeBackupJSON(backupMessageShardRequestMagic[:], req)
}

func decodeBackupMessageShardRequest(body []byte) (backupMessageShardRPCRequest, error) {
	var req backupMessageShardRPCRequest
	if err := decodeBackupJSON(body, backupMessageShardRequestMagic[:], &req); err != nil {
		return req, err
	}
	return req, validateBackupMessageShardRequest(req)
}

func encodeBackupMessageShardResponse(resp backupMessageShardRPCResponse) ([]byte, error) {
	if len(resp.Objects) > maxBackupMessageShardObjects || len(resp.Boundaries) > maxBackupMessageShardChannels {
		return nil, fmt.Errorf("internal/access/node: backup message object count exceeds limit")
	}
	return encodeBackupJSON(backupMessageShardResponseMagic[:], resp)
}

func decodeBackupMessageShardResponse(body []byte) (backupMessageShardRPCResponse, error) {
	var resp backupMessageShardRPCResponse
	if err := decodeBackupJSON(body, backupMessageShardResponseMagic[:], &resp); err != nil {
		return resp, err
	}
	if len(resp.Objects) > maxBackupMessageShardObjects || len(resp.Boundaries) > maxBackupMessageShardChannels {
		return resp, fmt.Errorf("internal/access/node: backup message object count exceeds limit")
	}
	return resp, nil
}

func validateBackupMessageShardRequest(req backupMessageShardRPCRequest) error {
	if req.Capture.JobID == "" || req.Capture.BackupEpoch == 0 || len(req.Capture.ConfigFingerprint) != 64 || req.Shard.ID == "" || req.Shard.NodeID == 0 || len(req.Shard.Channels) == 0 || len(req.Shard.Channels) > maxBackupMessageShardChannels {
		return fmt.Errorf("internal/access/node: invalid backup message shard request")
	}
	seen := make(map[string]struct{}, len(req.Shard.Channels))
	for _, channel := range req.Shard.Channels {
		key := fmt.Sprintf("%d:%s", channel.ChannelType, channel.ChannelID)
		if channel.ChannelID == "" || channel.LeaderNodeID != req.Shard.NodeID || channel.ChannelEpoch == 0 || channel.LeaderEpoch == 0 || channel.MinISR <= 0 {
			return fmt.Errorf("internal/access/node: invalid backup Channel fence")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("internal/access/node: duplicate backup Channel fence")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func encodeBackupJSON(magic []byte, value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(payload)+len(magic) > maxBackupMessageShardRPCBytes {
		return nil, fmt.Errorf("internal/access/node: backup RPC payload exceeds limit")
	}
	return append(append([]byte(nil), magic...), payload...), nil
}

func decodeBackupJSON(body, magic []byte, target any) error {
	if len(body) > maxBackupMessageShardRPCBytes || !hasMagic(body, magic) {
		return fmt.Errorf("internal/access/node: invalid backup RPC codec")
	}
	decoder := json.NewDecoder(bytes.NewReader(body[len(magic):]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("internal/access/node: trailing backup RPC data")
	}
	return nil
}
