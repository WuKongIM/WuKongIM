package node

import (
	"context"
	"fmt"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

var (
	scheduledBackupSlotRequestMagic     = [...]byte{'W', 'K', 'F', 'S', 1}
	scheduledBackupSlotResponseMagic    = [...]byte{'W', 'K', 'F', 's', 1}
	scheduledBackupMessageRequestMagic  = [...]byte{'W', 'K', 'F', 'M', 1}
	scheduledBackupMessageResponseMagic = [...]byte{'W', 'K', 'F', 'm', 1}
	scheduledBackupProbeRequestMagic    = [...]byte{'W', 'K', 'F', 'P', 1}
	scheduledBackupProbeResponseMagic   = [...]byte{'W', 'K', 'F', 'p', 1}
	scheduledBackupRestoreRequestMagic  = [...]byte{'W', 'K', 'F', 'R', 1}
	scheduledBackupRestoreResponseMagic = [...]byte{'W', 'K', 'F', 'r', 1}
)

const (
	// ScheduledBackupSlotRPCServiceID routes one full Hash Slot export to its
	// physical Slot leader.
	ScheduledBackupSlotRPCServiceID uint8 = clusternet.RPCScheduledBackupSlot
	// ScheduledBackupMessageRPCServiceID routes one message stream to its
	// Channel leader.
	ScheduledBackupMessageRPCServiceID uint8 = clusternet.RPCScheduledBackupMessages
	// ScheduledBackupRepositoryProbeRPCServiceID proves shared repository visibility.
	ScheduledBackupRepositoryProbeRPCServiceID uint8 = clusternet.RPCScheduledBackupRepositoryProbe
	// ScheduledBackupRestoreRPCServiceID performs node-local staged restore steps.
	ScheduledBackupRestoreRPCServiceID uint8 = clusternet.RPCScheduledBackupRestore
)

type scheduledBackupSlotResponse struct {
	Status  string                           `json:"status"`
	Receipt backupcontract.SlotExportReceipt `json:"receipt"`
}

type scheduledBackupMessageResponse struct {
	Status  string                              `json:"status"`
	Receipt backupcontract.MessageExportReceipt `json:"receipt"`
}

type scheduledBackupProbeResponse struct {
	Status string `json:"status"`
}

type scheduledBackupRestoreResponse struct {
	Status  string                            `json:"status"`
	Receipt backupcontract.RestoreNodeReceipt `json:"receipt"`
}

// HandleScheduledBackupSlotRPC performs one owner-local full Slot export.
func (a *Adapter) HandleScheduledBackupSlotRPC(
	ctx context.Context,
	payload []byte,
) ([]byte, error) {
	var command backupcontract.SlotExportCommand
	if err := decodeBackupJSON(
		payload, scheduledBackupSlotRequestMagic[:], &command,
	); err != nil {
		return nil, err
	}
	if a == nil || a.scheduledBackup == nil {
		return encodeBackupJSON(
			scheduledBackupSlotResponseMagic[:],
			scheduledBackupSlotResponse{Status: rpcStatusRejected},
		)
	}
	receipt, err := a.scheduledBackup.ExportSlot(ctx, command)
	return encodeBackupJSON(
		scheduledBackupSlotResponseMagic[:],
		scheduledBackupSlotResponse{
			Status: backupMessageStatusForError(err), Receipt: receipt,
		},
	)
}

// HandleScheduledBackupMessageRPC performs one owner-local message export.
func (a *Adapter) HandleScheduledBackupMessageRPC(
	ctx context.Context,
	payload []byte,
) ([]byte, error) {
	var command backupcontract.MessageExportCommand
	if err := decodeBackupJSON(
		payload, scheduledBackupMessageRequestMagic[:], &command,
	); err != nil {
		return nil, err
	}
	if a == nil || a.scheduledBackup == nil {
		return encodeBackupJSON(
			scheduledBackupMessageResponseMagic[:],
			scheduledBackupMessageResponse{Status: rpcStatusRejected},
		)
	}
	receipt, err := a.scheduledBackup.ExportMessages(ctx, command)
	return encodeBackupJSON(
		scheduledBackupMessageResponseMagic[:],
		scheduledBackupMessageResponse{
			Status: backupMessageStatusForError(err), Receipt: receipt,
		},
	)
}

// HandleScheduledBackupRepositoryProbeRPC observes a coordinator marker and
// writes this node's receipt through the configured repository.
func (a *Adapter) HandleScheduledBackupRepositoryProbeRPC(
	ctx context.Context,
	payload []byte,
) ([]byte, error) {
	var command backupcontract.RepositoryProbeCommand
	if err := decodeBackupJSON(
		payload, scheduledBackupProbeRequestMagic[:], &command,
	); err != nil {
		return nil, err
	}
	if a == nil || a.scheduledBackupProbe == nil {
		return encodeBackupJSON(
			scheduledBackupProbeResponseMagic[:],
			scheduledBackupProbeResponse{Status: rpcStatusRejected},
		)
	}
	err := a.scheduledBackupProbe.ObserveRepositoryProbe(ctx, command)
	return encodeBackupJSON(
		scheduledBackupProbeResponseMagic[:],
		scheduledBackupProbeResponse{Status: backupMessageStatusForError(err)},
	)
}

// HandleScheduledBackupRestoreRPC performs one idempotent node-local restore
// step while payload bytes remain in the shared repository or local staging.
func (a *Adapter) HandleScheduledBackupRestoreRPC(
	ctx context.Context,
	payload []byte,
) ([]byte, error) {
	var command backupcontract.RestoreNodeCommand
	if err := decodeBackupJSON(
		payload, scheduledBackupRestoreRequestMagic[:], &command,
	); err != nil {
		return nil, err
	}
	if a == nil || a.scheduledRestore == nil {
		return encodeBackupJSON(
			scheduledBackupRestoreResponseMagic[:],
			scheduledBackupRestoreResponse{Status: rpcStatusRejected},
		)
	}
	receipt, err := a.scheduledRestore.Run(ctx, command)
	return encodeBackupJSON(
		scheduledBackupRestoreResponseMagic[:],
		scheduledBackupRestoreResponse{
			Status: backupMessageStatusForError(err), Receipt: receipt,
		},
	)
}

// ExportBackupSlot forwards one bounded Slot command.
func (c *Client) ExportBackupSlot(
	ctx context.Context,
	nodeID uint64,
	command backupcontract.SlotExportCommand,
) (backupcontract.SlotExportReceipt, error) {
	if c == nil || c.node == nil || nodeID == 0 ||
		command.OwnerNodeID != nodeID {
		return backupcontract.SlotExportReceipt{},
			fmt.Errorf("backup full Slot RPC: invalid request")
	}
	payload, err := encodeBackupJSON(
		scheduledBackupSlotRequestMagic[:], command,
	)
	if err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	body, err := c.node.CallRPC(
		ctx, nodeID, ScheduledBackupSlotRPCServiceID, payload,
	)
	if err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	var response scheduledBackupSlotResponse
	if err := decodeBackupJSON(
		body, scheduledBackupSlotResponseMagic[:], &response,
	); err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	if err := backupMessageErrorForStatus(response.Status); err != nil {
		return backupcontract.SlotExportReceipt{}, err
	}
	return response.Receipt, nil
}

// ExportBackupMessages forwards one bounded message command.
func (c *Client) ExportBackupMessages(
	ctx context.Context,
	nodeID uint64,
	command backupcontract.MessageExportCommand,
) (backupcontract.MessageExportReceipt, error) {
	if c == nil || c.node == nil || nodeID == 0 ||
		command.Shard.NodeID != nodeID {
		return backupcontract.MessageExportReceipt{},
			fmt.Errorf("backup full message RPC: invalid request")
	}
	payload, err := encodeBackupJSON(
		scheduledBackupMessageRequestMagic[:], command,
	)
	if err != nil {
		return backupcontract.MessageExportReceipt{}, err
	}
	body, err := c.node.CallRPC(
		ctx, nodeID, ScheduledBackupMessageRPCServiceID, payload,
	)
	if err != nil {
		return backupcontract.MessageExportReceipt{}, err
	}
	var response scheduledBackupMessageResponse
	if err := decodeBackupJSON(
		body, scheduledBackupMessageResponseMagic[:], &response,
	); err != nil {
		return backupcontract.MessageExportReceipt{}, err
	}
	if err := backupMessageErrorForStatus(response.Status); err != nil {
		return backupcontract.MessageExportReceipt{}, err
	}
	return response.Receipt, nil
}

// ProbeBackupRepository forwards one cross-node visibility observation.
func (c *Client) ProbeBackupRepository(
	ctx context.Context,
	nodeID uint64,
	command backupcontract.RepositoryProbeCommand,
) error {
	if c == nil || c.node == nil || nodeID == 0 {
		return fmt.Errorf("backup repository probe RPC: invalid request")
	}
	payload, err := encodeBackupJSON(
		scheduledBackupProbeRequestMagic[:], command,
	)
	if err != nil {
		return err
	}
	body, err := c.node.CallRPC(
		ctx, nodeID, ScheduledBackupRepositoryProbeRPCServiceID, payload,
	)
	if err != nil {
		return err
	}
	var response scheduledBackupProbeResponse
	if err := decodeBackupJSON(
		body, scheduledBackupProbeResponseMagic[:], &response,
	); err != nil {
		return err
	}
	return backupMessageErrorForStatus(response.Status)
}

// RunBackupRestoreNode forwards one bounded staged restore command.
func (c *Client) RunBackupRestoreNode(
	ctx context.Context,
	nodeID uint64,
	command backupcontract.RestoreNodeCommand,
) (backupcontract.RestoreNodeReceipt, error) {
	if c == nil || c.node == nil || nodeID == 0 {
		return backupcontract.RestoreNodeReceipt{},
			fmt.Errorf("backup restore RPC: invalid request")
	}
	payload, err := encodeBackupJSON(
		scheduledBackupRestoreRequestMagic[:], command,
	)
	if err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	body, err := c.node.CallRPC(
		ctx, nodeID, ScheduledBackupRestoreRPCServiceID, payload,
	)
	if err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	var response scheduledBackupRestoreResponse
	if err := decodeBackupJSON(
		body, scheduledBackupRestoreResponseMagic[:], &response,
	); err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	if err := backupMessageErrorForStatus(response.Status); err != nil {
		return backupcontract.RestoreNodeReceipt{}, err
	}
	return response.Receipt, nil
}
