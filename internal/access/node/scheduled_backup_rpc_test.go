package node

import (
	"context"
	"errors"
	"strings"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

func TestScheduledBackupRPCForwardsOnlyBoundedReceipts(t *testing.T) {
	exporter := &fakeScheduledBackupExporter{}
	adapter := New(Options{ScheduledBackup: exporter})
	node := &fakeManagerConnectionRPCNode{
		handler: adapter.HandleScheduledBackupSlotRPC,
	}
	command := backupcontract.SlotExportCommand{
		Plan: backupcontract.Plan{
			Store: backupcontract.StoreConfig{
				Kind: backupcontract.StoreKindFile,
			},
		},
		BackupID:    "backup-1",
		HashSlot:    7,
		OwnerNodeID: 2,
		OwnerTerm:   9,
	}
	receipt, err := NewClient(node).ExportBackupSlot(
		context.Background(), 2, command,
	)
	if err != nil {
		t.Fatalf("ExportBackupSlot(): %v", err)
	}
	if node.serviceID != ScheduledBackupSlotRPCServiceID ||
		exporter.slot.HashSlot != 7 ||
		receipt.StoredBytes != 8 {
		t.Fatalf(
			"service=%d command=%#v receipt=%#v",
			node.serviceID, exporter.slot, receipt,
		)
	}

	node.handler = adapter.HandleScheduledBackupMessageRPC
	message := backupcontract.MessageExportCommand{
		Store: backupcontract.StoreConfig{
			Kind: backupcontract.StoreKindFile,
		},
		BackupID: "backup-1",
		HashSlot: 7,
		Shard: backupcontract.MessageShard{
			ID:     "n2-0000",
			NodeID: 2,
			Channels: []backupcontract.ChannelFence{{
				ChannelID: "room", ChannelType: 2, LeaderNodeID: 2,
				ChannelEpoch: 3, LeaderEpoch: 4, MinISR: 1,
			}},
		},
		FirstSequence:   1,
		StreamNumber:    1,
		RateBytesPerSec: 50 << 20,
	}
	messageReceipt, err := NewClient(node).ExportBackupMessages(
		context.Background(), 2, message,
	)
	if err != nil {
		t.Fatalf("ExportBackupMessages(): %v", err)
	}
	if node.serviceID != ScheduledBackupMessageRPCServiceID ||
		exporter.message.Shard.ID != "n2-0000" ||
		messageReceipt.Records != 3 {
		t.Fatalf(
			"service=%d command=%#v receipt=%#v",
			node.serviceID, exporter.message, messageReceipt,
		)
	}
}

func TestScheduledBackupRepositoryProbeRPCRetainsSafeFailure(t *testing.T) {
	probe := &fakeScheduledBackupRepositoryProbe{
		err: &backupcontract.RepositoryAccessError{
			Reason:       backupcontract.RepositoryAccessInvalidAccessKey,
			Stage:        backupcontract.RepositoryAccessReadMarker,
			Provider:     backupcontract.StoreKindOSS,
			ProviderCode: "InvalidAccessKeyId",
			RequestID:    "request-1",
			Cause:        errors.New("AccessKeyId=secret-access-key"),
		},
	}
	adapter := New(Options{ScheduledBackupProbe: probe})
	node := &fakeManagerConnectionRPCNode{
		handler: adapter.HandleScheduledBackupRepositoryProbeRPC,
	}

	err := NewClient(node).ProbeBackupRepository(
		context.Background(),
		2,
		backupcontract.RepositoryProbeCommand{
			Store: backupcontract.StoreConfig{
				Kind: backupcontract.StoreKindOSS,
			},
			MarkerKey:      "probes/one/marker",
			MarkerSHA256:   strings.Repeat("a", 64),
			ReceiptKey:     "probes/one/node-2",
			ReceiptContent: "2:one",
		},
	)
	var accessErr *backupcontract.RepositoryAccessError
	if !errors.As(err, &accessErr) {
		t.Fatalf("ProbeBackupRepository() error = %T %v", err, err)
	}
	if accessErr.Reason != backupcontract.RepositoryAccessInvalidAccessKey ||
		accessErr.Stage != backupcontract.RepositoryAccessReadMarker ||
		accessErr.Provider != backupcontract.StoreKindOSS ||
		accessErr.ProviderCode != "InvalidAccessKeyId" ||
		accessErr.RequestID != "request-1" ||
		accessErr.NodeID != 2 {
		t.Fatalf("repository access error = %#v", accessErr)
	}
	if strings.Contains(err.Error(), "secret-access-key") {
		t.Fatalf("RPC error leaked secret: %v", err)
	}
}

type fakeScheduledBackupExporter struct {
	slot    backupcontract.SlotExportCommand
	message backupcontract.MessageExportCommand
}

func (e *fakeScheduledBackupExporter) ExportSlot(
	_ context.Context,
	command backupcontract.SlotExportCommand,
) (backupcontract.SlotExportReceipt, error) {
	e.slot = command
	return backupcontract.SlotExportReceipt{
		LogicalBytes: 10, StoredBytes: 8, Records: 3,
	}, nil
}

func (e *fakeScheduledBackupExporter) ExportMessages(
	_ context.Context,
	command backupcontract.MessageExportCommand,
) (backupcontract.MessageExportReceipt, error) {
	e.message = command
	return backupcontract.MessageExportReceipt{Records: 3}, nil
}

type fakeScheduledBackupRepositoryProbe struct {
	err error
}

func (p *fakeScheduledBackupRepositoryProbe) ObserveRepositoryProbe(
	context.Context,
	backupcontract.RepositoryProbeCommand,
) error {
	return p.err
}
