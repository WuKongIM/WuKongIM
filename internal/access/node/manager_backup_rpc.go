package node

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

const (
	managerBackupMaxRequestBytes  = 16 << 10
	managerBackupMaxResponseBytes = 2 << 20
)

var (
	managerBackupRequestMagic  = [...]byte{'W', 'K', 'B', 'M', 'Q', 2}
	managerBackupResponseMagic = [...]byte{'W', 'K', 'B', 'M', 'R', 2}
)

// ManagerBackupRPCServiceID routes continuous-backup operations to the Controller leader.
const ManagerBackupRPCServiceID uint8 = clusternet.RPCManagerBackup

// ManagerBackup is the narrow leader-owned continuous-backup mutation surface.
type ManagerBackup interface {
	Status(context.Context) (backupusecase.StatusSnapshot, error)
	PublishCheckpoint(context.Context) (backupusecase.CheckpointPublication, error)
	SetCheckpointHold(context.Context, string, bool) (backupusecase.CheckpointSummary, error)
	FenceSource(context.Context, backupusecase.SourceFenceRequest) (backupusecase.SourceFenceReceipt, error)
}

// ManagerBackupLeadership identifies the receiving node and current Controller leader.
type ManagerBackupLeadership interface {
	NodeID() uint64
	BackupControllerLeaderID() uint64
}

// ManagerBackupOptions configures the bounded Manager backup RPC adapter.
type ManagerBackupOptions struct {
	Local      ManagerBackup
	Leadership ManagerBackupLeadership
}

// ManagerBackupAdapter exposes continuous-backup operations through one internal RPC.
type ManagerBackupAdapter struct {
	local      ManagerBackup
	leadership ManagerBackupLeadership
}

// NewManagerBackupAdapter creates a bounded leader-fenced Manager backup adapter.
func NewManagerBackupAdapter(options ManagerBackupOptions) *ManagerBackupAdapter {
	return &ManagerBackupAdapter{local: options.Local, leadership: options.Leadership}
}

type managerBackupOperation string

const (
	managerBackupStatus            managerBackupOperation = "status"
	managerBackupPublishCheckpoint managerBackupOperation = "publish_checkpoint"
	managerBackupSetCheckpointHold managerBackupOperation = "set_checkpoint_hold"
	managerBackupFenceSource       managerBackupOperation = "fence_source"
)

type managerBackupRequest struct {
	Operation    managerBackupOperation           `json:"operation"`
	SourceFence  backupusecase.SourceFenceRequest `json:"source_fence,omitempty"`
	CheckpointID string                           `json:"checkpoint_id,omitempty"`
	Held         bool                             `json:"held,omitempty"`
}

type managerBackupResponse struct {
	Error           string                               `json:"error,omitempty"`
	Status          *backupusecase.StatusSnapshot        `json:"status,omitempty"`
	SourceFence     *backupusecase.SourceFenceReceipt    `json:"source_fence,omitempty"`
	Checkpoint      *backupusecase.CheckpointPublication `json:"checkpoint,omitempty"`
	CheckpointState *backupusecase.CheckpointSummary     `json:"checkpoint_state,omitempty"`
}

// HandleRPC executes one request only while the receiver is the current Controller leader.
func (a *ManagerBackupAdapter) HandleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	var request managerBackupRequest
	if err := decodeManagerBackupRequest(payload, &request); err != nil {
		return nil, err
	}
	response := managerBackupResponse{}
	if a == nil || a.local == nil || a.leadership == nil ||
		a.leadership.NodeID() == 0 ||
		a.leadership.BackupControllerLeaderID() != a.leadership.NodeID() {
		response.Error = managerBackupErrorCode(backupusecase.ErrControllerLeaderUnavailable)
		return encodeManagerBackupResponse(response)
	}
	switch request.Operation {
	case managerBackupStatus:
		value, err := a.local.Status(ctx)
		response.Status, response.Error = &value, managerBackupErrorCode(err)
	case managerBackupPublishCheckpoint:
		value, err := a.local.PublishCheckpoint(ctx)
		response.Checkpoint, response.Error = &value, managerBackupErrorCode(err)
	case managerBackupSetCheckpointHold:
		value, err := a.local.SetCheckpointHold(
			ctx, request.CheckpointID, request.Held,
		)
		response.CheckpointState, response.Error =
			&value, managerBackupErrorCode(err)
	case managerBackupFenceSource:
		value, err := a.local.FenceSource(ctx, request.SourceFence)
		response.SourceFence, response.Error = &value, managerBackupErrorCode(err)
	default:
		response.Error = managerBackupErrorCode(backupusecase.ErrInvalidRequest)
	}
	return encodeManagerBackupResponse(response)
}

// ManagerBackupSetCheckpointHold appends one hold/release decision on the
// exact current Controller Leader.
func (c *Client) ManagerBackupSetCheckpointHold(
	ctx context.Context,
	nodeID uint64,
	checkpointID string,
	held bool,
) (backupusecase.CheckpointSummary, error) {
	response, err := c.callManagerBackup(
		ctx, nodeID, managerBackupRequest{
			Operation:    managerBackupSetCheckpointHold,
			CheckpointID: strings.TrimSpace(checkpointID), Held: held,
		},
	)
	if err != nil || response.CheckpointState == nil {
		return backupusecase.CheckpointSummary{},
			firstManagerBackupError(err, response.Error)
	}
	return *response.CheckpointState, managerBackupError(response.Error)
}

// ManagerBackupStatus reads continuous-backup state from one exact leader node.
func (c *Client) ManagerBackupStatus(
	ctx context.Context,
	nodeID uint64,
) (backupusecase.StatusSnapshot, error) {
	response, err := c.callManagerBackup(
		ctx, nodeID, managerBackupRequest{Operation: managerBackupStatus},
	)
	if err != nil || response.Status == nil {
		return backupusecase.StatusSnapshot{},
			firstManagerBackupError(err, response.Error)
	}
	return *response.Status, managerBackupError(response.Error)
}

// ManagerBackupPublishCheckpoint publishes one complete vector cut on one exact leader.
func (c *Client) ManagerBackupPublishCheckpoint(
	ctx context.Context,
	nodeID uint64,
) (backupusecase.CheckpointPublication, error) {
	response, err := c.callManagerBackup(
		ctx, nodeID,
		managerBackupRequest{Operation: managerBackupPublishCheckpoint},
	)
	if err != nil || response.Checkpoint == nil {
		return backupusecase.CheckpointPublication{},
			firstManagerBackupError(err, response.Error)
	}
	return *response.Checkpoint, managerBackupError(response.Error)
}

// ManagerBackupFenceSource irreversibly fences one source generation through
// the exact current Controller leader.
func (c *Client) ManagerBackupFenceSource(
	ctx context.Context,
	nodeID uint64,
	request backupusecase.SourceFenceRequest,
) (backupusecase.SourceFenceReceipt, error) {
	response, err := c.callManagerBackup(
		ctx, nodeID,
		managerBackupRequest{
			Operation: managerBackupFenceSource, SourceFence: request,
		},
	)
	if err != nil || response.SourceFence == nil {
		return backupusecase.SourceFenceReceipt{},
			firstManagerBackupError(err, response.Error)
	}
	return *response.SourceFence, managerBackupError(response.Error)
}

func (c *Client) callManagerBackup(
	ctx context.Context,
	nodeID uint64,
	request managerBackupRequest,
) (managerBackupResponse, error) {
	if c == nil || c.node == nil || nodeID == 0 {
		return managerBackupResponse{},
			backupusecase.ErrControllerLeaderUnavailable
	}
	payload, err := encodeManagerBackupRequest(request)
	if err != nil {
		return managerBackupResponse{}, backupusecase.ErrInvalidRequest
	}
	body, err := c.node.CallRPC(
		ctx, nodeID, ManagerBackupRPCServiceID, payload,
	)
	if err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return managerBackupResponse{}, err
		}
		return managerBackupResponse{},
			backupusecase.ErrControllerLeaderUnavailable
	}
	var response managerBackupResponse
	if err := decodeManagerBackupResponse(body, &response); err != nil {
		return managerBackupResponse{},
			backupusecase.ErrControllerLeaderUnavailable
	}
	return response, nil
}

func encodeManagerBackupRequest(request managerBackupRequest) ([]byte, error) {
	return encodeManagerBackupJSON(
		managerBackupRequestMagic[:], request, managerBackupMaxRequestBytes,
	)
}

func decodeManagerBackupRequest(payload []byte, request *managerBackupRequest) error {
	return decodeManagerBackupJSON(
		payload, managerBackupRequestMagic[:],
		managerBackupMaxRequestBytes, request,
	)
}

func encodeManagerBackupResponse(response managerBackupResponse) ([]byte, error) {
	return encodeManagerBackupJSON(
		managerBackupResponseMagic[:], response, managerBackupMaxResponseBytes,
	)
}

func decodeManagerBackupResponse(payload []byte, response *managerBackupResponse) error {
	return decodeManagerBackupJSON(
		payload, managerBackupResponseMagic[:],
		managerBackupMaxResponseBytes, response,
	)
}

func encodeManagerBackupJSON(
	magic []byte,
	value any,
	limit int,
) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(magic)+len(payload) > limit {
		return nil, fmt.Errorf(
			"internal/access/node: manager backup payload exceeds limit",
		)
	}
	return append(append([]byte(nil), magic...), payload...), nil
}

func decodeManagerBackupJSON(
	payload, magic []byte,
	limit int,
	target any,
) error {
	if len(payload) <= len(magic) || len(payload) > limit ||
		!hasMagic(payload, magic) {
		return fmt.Errorf(
			"internal/access/node: invalid manager backup payload size",
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload[len(magic):]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf(
			"internal/access/node: trailing manager backup payload",
		)
	}
	return nil
}

func firstManagerBackupError(callErr error, responseCode string) error {
	if callErr != nil {
		return callErr
	}
	if responseCode != "" {
		return managerBackupError(responseCode)
	}
	return backupusecase.ErrControllerLeaderUnavailable
}

func managerBackupErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, backupusecase.ErrDisabled):
		return "backup_disabled"
	case errors.Is(err, backupusecase.ErrDoctorUnhealthy):
		return "backup_doctor_unhealthy"
	case errors.Is(err, backupusecase.ErrControllerLeaderUnavailable):
		return "controller_leader_unavailable"
	case errors.Is(err, backupusecase.ErrStateConflict):
		return "state_conflict"
	case errors.Is(err, backupusecase.ErrCheckpointNotFound):
		return "checkpoint_not_found"
	case errors.Is(err, backupusecase.ErrSourceFenceExists):
		return "source_fence_exists"
	case errors.Is(err, backupusecase.ErrInvalidRequest):
		return "bad_request"
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "context_deadline_exceeded"
	default:
		return "service_unavailable"
	}
}

func managerBackupError(code string) error {
	switch strings.TrimSpace(code) {
	case "":
		return nil
	case "backup_disabled":
		return backupusecase.ErrDisabled
	case "backup_doctor_unhealthy":
		return backupusecase.ErrDoctorUnhealthy
	case "controller_leader_unavailable":
		return backupusecase.ErrControllerLeaderUnavailable
	case "state_conflict":
		return backupusecase.ErrStateConflict
	case "checkpoint_not_found":
		return backupusecase.ErrCheckpointNotFound
	case "source_fence_exists":
		return backupusecase.ErrSourceFenceExists
	case "bad_request":
		return backupusecase.ErrInvalidRequest
	case "context_canceled":
		return context.Canceled
	case "context_deadline_exceeded":
		return context.DeadlineExceeded
	default:
		return fmt.Errorf("backup management service unavailable")
	}
}
