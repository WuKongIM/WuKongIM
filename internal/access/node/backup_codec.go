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

	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

var (
	backupMessageShardRequestMagic    = [...]byte{'W', 'K', 'V', 'B', 1}
	backupMessageShardResponseMagic   = [...]byte{'W', 'K', 'V', 'b', 1}
	backupPartitionRequestMagic       = [...]byte{'W', 'K', 'V', 'P', 1}
	backupPartitionResponseMagic      = [...]byte{'W', 'K', 'V', 'p', 1}
	backupRestoreTargetRequestMagic   = [...]byte{'W', 'K', 'V', 'R', 1}
	backupRestoreTargetResponseMagic  = [...]byte{'W', 'K', 'V', 'r', 1}
	backupRestoreInstallRequestMagic  = [...]byte{'W', 'K', 'V', 'I', 1}
	backupRestoreInstallResponseMagic = [...]byte{'W', 'K', 'V', 'i', 1}
	backupRestoreVerifyRequestMagic   = [...]byte{'W', 'K', 'V', 'Y', 1}
	backupRestoreVerifyResponseMagic  = [...]byte{'W', 'K', 'V', 'y', 1}
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
	Status     string                           `json:"status"`
	Objects    []backupartifact.ObjectEntry     `json:"objects"`
	Boundaries []backupartifact.ChannelBoundary `json:"boundaries"`
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
		(plan.Repository != "primary" && plan.Repository != "secondary") || len(plan.Partitions) != int(plan.HashSlotCount) {
		return fmt.Errorf("internal/access/node: invalid restore install request")
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
