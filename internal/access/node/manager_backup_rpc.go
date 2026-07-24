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
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

const (
	managerBackupMaxRequestBytes  = 16 << 10
	managerBackupMaxResponseBytes = 2 << 20
)

var (
	managerBackupRequestMagic  = [...]byte{'W', 'K', 'B', 'M', 'Q', 1}
	managerBackupResponseMagic = [...]byte{'W', 'K', 'B', 'M', 'R', 1}
)

// ManagerBackupRPCServiceID routes Manager backup operations to the Controller leader.
const ManagerBackupRPCServiceID uint8 = clusternet.RPCManagerBackup

// ManagerBackup handles the narrow cluster-level backup management surface.
type ManagerBackup interface {
	Status(context.Context) (backupusecase.StatusSnapshot, error)
	ListRestorePointsPage(context.Context, backupusecase.RestorePointListRequest) (backupusecase.RestorePointPage, error)
	Trigger(context.Context, backupartifact.RestorePointKind) (backupusecase.Job, error)
	Cancel(context.Context, string, uint64) (backupusecase.Job, error)
	Hold(context.Context, string) (backupusecase.RestorePoint, error)
	Release(context.Context, string) (backupusecase.RestorePoint, error)
	StartVerification(context.Context, string) (backupusecase.VerificationTask, error)
}

// ManagerBackupLeadership identifies the receiving node and current Controller leader.
type ManagerBackupLeadership interface {
	NodeID() uint64
	BackupControllerLeaderID() uint64
}

// ManagerBackupOptions configures the bounded Manager backup RPC adapter.
type ManagerBackupOptions struct {
	// Local executes operations only after this node proves it is the current leader.
	Local ManagerBackup
	// Leadership supplies the current leader fence.
	Leadership ManagerBackupLeadership
}

// ManagerBackupAdapter exposes cluster backup management through one internal RPC.
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
	managerBackupList              managerBackupOperation = "list"
	managerBackupTrigger           managerBackupOperation = "trigger"
	managerBackupCancel            managerBackupOperation = "cancel"
	managerBackupHold              managerBackupOperation = "hold"
	managerBackupRelease           managerBackupOperation = "release"
	managerBackupStartVerification managerBackupOperation = "start_verification"
)

type managerBackupRequest struct {
	Operation managerBackupOperation                `json:"operation"`
	List      backupusecase.RestorePointListRequest `json:"list,omitempty"`
	Kind      backupartifact.RestorePointKind       `json:"kind,omitempty"`
	ID        string                                `json:"id,omitempty"`
	Epoch     uint64                                `json:"epoch,omitempty"`
}

type managerBackupResponse struct {
	Error        string                          `json:"error,omitempty"`
	Status       *backupusecase.StatusSnapshot   `json:"status,omitempty"`
	Page         *backupusecase.RestorePointPage `json:"page,omitempty"`
	Job          *backupusecase.Job              `json:"job,omitempty"`
	RestorePoint *backupusecase.RestorePoint     `json:"restore_point,omitempty"`
	Verification *backupusecase.VerificationTask `json:"verification,omitempty"`
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
	case managerBackupList:
		value, err := a.local.ListRestorePointsPage(ctx, request.List)
		response.Page, response.Error = &value, managerBackupErrorCode(err)
	case managerBackupTrigger:
		value, err := a.local.Trigger(ctx, request.Kind)
		response.Job, response.Error = &value, managerBackupErrorCode(err)
	case managerBackupCancel:
		value, err := a.local.Cancel(ctx, request.ID, request.Epoch)
		response.Job, response.Error = &value, managerBackupErrorCode(err)
	case managerBackupHold:
		value, err := a.local.Hold(ctx, request.ID)
		response.RestorePoint, response.Error = &value, managerBackupErrorCode(err)
	case managerBackupRelease:
		value, err := a.local.Release(ctx, request.ID)
		response.RestorePoint, response.Error = &value, managerBackupErrorCode(err)
	case managerBackupStartVerification:
		value, err := a.local.StartVerification(ctx, request.ID)
		response.Verification, response.Error = &value, managerBackupErrorCode(err)
	default:
		response.Error = managerBackupErrorCode(backupusecase.ErrInvalidRequest)
	}
	return encodeManagerBackupResponse(response)
}

// ManagerBackupStatus reads cluster backup state from one exact leader node.
func (c *Client) ManagerBackupStatus(ctx context.Context, nodeID uint64) (backupusecase.StatusSnapshot, error) {
	response, err := c.callManagerBackup(ctx, nodeID, managerBackupRequest{Operation: managerBackupStatus})
	if err != nil || response.Status == nil {
		return backupusecase.StatusSnapshot{}, firstManagerBackupError(err, response.Error)
	}
	return *response.Status, managerBackupError(response.Error)
}

// ManagerBackupListRestorePoints reads one bounded page from one exact leader node.
func (c *Client) ManagerBackupListRestorePoints(ctx context.Context, nodeID uint64, request backupusecase.RestorePointListRequest) (backupusecase.RestorePointPage, error) {
	response, err := c.callManagerBackup(ctx, nodeID, managerBackupRequest{Operation: managerBackupList, List: request})
	if err != nil || response.Page == nil {
		return backupusecase.RestorePointPage{}, firstManagerBackupError(err, response.Error)
	}
	return *response.Page, managerBackupError(response.Error)
}

// ManagerBackupTrigger starts one materialization strategy on one exact leader node.
func (c *Client) ManagerBackupTrigger(ctx context.Context, nodeID uint64, kind backupartifact.RestorePointKind) (backupusecase.Job, error) {
	response, err := c.callManagerBackup(ctx, nodeID, managerBackupRequest{Operation: managerBackupTrigger, Kind: kind})
	if err != nil || response.Job == nil {
		return backupusecase.Job{}, firstManagerBackupError(err, response.Error)
	}
	return *response.Job, managerBackupError(response.Error)
}

// ManagerBackupCancel cancels one exactly fenced active backup job.
func (c *Client) ManagerBackupCancel(ctx context.Context, nodeID uint64, id string, epoch uint64) (backupusecase.Job, error) {
	response, err := c.callManagerBackup(ctx, nodeID, managerBackupRequest{Operation: managerBackupCancel, ID: id, Epoch: epoch})
	if err != nil || response.Job == nil {
		return backupusecase.Job{}, firstManagerBackupError(err, response.Error)
	}
	return *response.Job, managerBackupError(response.Error)
}

// ManagerBackupHold mutates one restore-point retention hold.
func (c *Client) ManagerBackupHold(ctx context.Context, nodeID uint64, id string, held bool) (backupusecase.RestorePoint, error) {
	operation := managerBackupRelease
	if held {
		operation = managerBackupHold
	}
	response, err := c.callManagerBackup(ctx, nodeID, managerBackupRequest{Operation: operation, ID: id})
	if err != nil || response.RestorePoint == nil {
		return backupusecase.RestorePoint{}, firstManagerBackupError(err, response.Error)
	}
	return *response.RestorePoint, managerBackupError(response.Error)
}

// ManagerBackupStartVerification creates one durable audit task on one exact leader node.
func (c *Client) ManagerBackupStartVerification(ctx context.Context, nodeID uint64, id string) (backupusecase.VerificationTask, error) {
	response, err := c.callManagerBackup(ctx, nodeID, managerBackupRequest{Operation: managerBackupStartVerification, ID: id})
	if err != nil || response.Verification == nil {
		return backupusecase.VerificationTask{}, firstManagerBackupError(err, response.Error)
	}
	return *response.Verification, managerBackupError(response.Error)
}

func (c *Client) callManagerBackup(ctx context.Context, nodeID uint64, request managerBackupRequest) (managerBackupResponse, error) {
	if c == nil || c.node == nil || nodeID == 0 {
		return managerBackupResponse{}, backupusecase.ErrControllerLeaderUnavailable
	}
	payload, err := encodeManagerBackupRequest(request)
	if err != nil {
		return managerBackupResponse{}, backupusecase.ErrInvalidRequest
	}
	body, err := c.node.CallRPC(ctx, nodeID, ManagerBackupRPCServiceID, payload)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return managerBackupResponse{}, err
		}
		return managerBackupResponse{}, backupusecase.ErrControllerLeaderUnavailable
	}
	var response managerBackupResponse
	if err := decodeManagerBackupResponse(body, &response); err != nil {
		return managerBackupResponse{}, backupusecase.ErrControllerLeaderUnavailable
	}
	return response, nil
}

func encodeManagerBackupRequest(request managerBackupRequest) ([]byte, error) {
	return encodeManagerBackupJSON(managerBackupRequestMagic[:], request, managerBackupMaxRequestBytes)
}

func decodeManagerBackupRequest(payload []byte, request *managerBackupRequest) error {
	return decodeManagerBackupJSON(payload, managerBackupRequestMagic[:], managerBackupMaxRequestBytes, request)
}

func encodeManagerBackupResponse(response managerBackupResponse) ([]byte, error) {
	return encodeManagerBackupJSON(managerBackupResponseMagic[:], response, managerBackupMaxResponseBytes)
}

func decodeManagerBackupResponse(payload []byte, response *managerBackupResponse) error {
	return decodeManagerBackupJSON(payload, managerBackupResponseMagic[:], managerBackupMaxResponseBytes, response)
}

func encodeManagerBackupJSON(magic []byte, value any, limit int) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(magic)+len(payload) > limit {
		return nil, fmt.Errorf("internal/access/node: manager backup payload exceeds limit")
	}
	return append(append([]byte(nil), magic...), payload...), nil
}

func decodeManagerBackupJSON(payload, magic []byte, limit int, target any) error {
	if len(payload) <= len(magic) || len(payload) > limit || !hasMagic(payload, magic) {
		return fmt.Errorf("internal/access/node: invalid manager backup payload size")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload[len(magic):]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("internal/access/node: trailing manager backup payload")
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
	case errors.Is(err, backupusecase.ErrJobActive):
		return "backup_job_active"
	case errors.Is(err, backupusecase.ErrVerificationJobActive):
		return "verification_job_active"
	case errors.Is(err, backupusecase.ErrStateConflict), errors.Is(err, backupusecase.ErrJobNotFound):
		return "state_conflict"
	case errors.Is(err, backupusecase.ErrRestorePointNotFound):
		return "restore_point_not_found"
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
	case "backup_job_active":
		return backupusecase.ErrJobActive
	case "verification_job_active":
		return backupusecase.ErrVerificationJobActive
	case "state_conflict":
		return backupusecase.ErrStateConflict
	case "restore_point_not_found":
		return backupusecase.ErrRestorePointNotFound
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
