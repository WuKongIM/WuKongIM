package node

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
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

func TestScheduledBackupRestoreRPCPreservesAdmissionFencesAndBoundedReceipt(t *testing.T) {
	restorer := &fakeScheduledBackupRestore{
		receipt: backupcontract.RestoreNodeReceipt{
			LogicalBytes:         4096,
			AvailableBytes:       16384,
			CurrentBusinessBytes: 1024,
		},
	}
	adapter := New(Options{ScheduledRestore: restorer})
	node := &fakeManagerConnectionRPCNode{
		handler: adapter.HandleScheduledBackupRestoreRPC,
	}
	command := backupcontract.RestoreNodeCommand{
		Action:   backupcontract.RestoreNodeActionSwitch,
		Store:    backupcontract.StoreConfig{Kind: backupcontract.StoreKindS3, Region: "cn-test", Bucket: "archive"},
		JobID:    "restore-1",
		BackupID: "backup-9",
		HashSlot: 7,
		Attempt:  3,
		SlotReference: backupartifact.SlotReference{
			HashSlot:       7,
			ManifestKey:    "backups/backup-9/slots/7/manifest.json",
			ManifestSHA256: strings.Repeat("a", 64),
			LogicalBytes:   4096,
			StoredBytes:    2048,
			Records:        12,
			MaxMessageID:   99,
		},
		ControllerRevision: 81,
		TargetActivation:   "activation-2",
		RequiredBytes:      8192,
		MaxMessageID:       100,
		CoordinatorNodeID:  1,
		CoordinatorTerm:    17,
	}

	receipt, err := NewClient(node).RunBackupRestoreNode(context.Background(), 2, command)
	if err != nil {
		t.Fatalf("RunBackupRestoreNode() error = %v", err)
	}
	if node.serviceID != ScheduledBackupRestoreRPCServiceID {
		t.Fatalf("service id = %d, want %d", node.serviceID, ScheduledBackupRestoreRPCServiceID)
	}
	if len(restorer.commands) != 1 || !reflect.DeepEqual(restorer.commands[0], command) {
		t.Fatalf("restore commands = %#v, want exact fenced command %#v", restorer.commands, command)
	}
	if receipt != restorer.receipt {
		t.Fatalf("restore receipt = %#v, want %#v", receipt, restorer.receipt)
	}
}

func TestScheduledBackupRestoreRPCFailsClosedAndPreservesStableErrors(t *testing.T) {
	tests := []struct {
		name       string
		restorer   ScheduledBackupRestore
		wantIs     error
		wantString string
	}{
		{name: "service unavailable", wantString: "scheduled backup node operation rejected"},
		{name: "caller canceled", restorer: &fakeScheduledBackupRestore{err: context.Canceled}, wantIs: context.Canceled},
		{name: "caller deadline", restorer: &fakeScheduledBackupRestore{err: context.DeadlineExceeded}, wantIs: context.DeadlineExceeded},
		{name: "restore rejected", restorer: &fakeScheduledBackupRestore{err: errors.New("stale restore fence")}, wantString: "scheduled backup node operation rejected"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := New(Options{ScheduledRestore: tt.restorer})
			node := &fakeManagerConnectionRPCNode{handler: adapter.HandleScheduledBackupRestoreRPC}
			receipt, err := NewClient(node).RunBackupRestoreNode(
				context.Background(), 2, backupcontract.RestoreNodeCommand{},
			)
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Fatalf("RunBackupRestoreNode() error = %v, want %v", err, tt.wantIs)
			}
			if tt.wantString != "" && (err == nil || err.Error() != tt.wantString) {
				t.Fatalf("RunBackupRestoreNode() error = %v, want %q", err, tt.wantString)
			}
			if receipt != (backupcontract.RestoreNodeReceipt{}) {
				t.Fatalf("failed restore receipt = %#v, want zero", receipt)
			}
		})
	}
}

func TestScheduledBackupExportRejectsMismatchedAuthorityBeforeTransport(t *testing.T) {
	node := &countingRPCNode{}
	client := NewClient(node)

	_, slotErr := client.ExportBackupSlot(context.Background(), 2, backupcontract.SlotExportCommand{
		OwnerNodeID: 3,
	})
	if slotErr == nil {
		t.Fatal("ExportBackupSlot() error = nil, want owner-node mismatch")
	}
	_, messageErr := client.ExportBackupMessages(context.Background(), 2, backupcontract.MessageExportCommand{
		Shard: backupcontract.MessageShard{NodeID: 3},
	})
	if messageErr == nil {
		t.Fatal("ExportBackupMessages() error = nil, want shard-node mismatch")
	}
	if node.calls != 0 {
		t.Fatalf("transport calls = %d, want none after authority mismatch", node.calls)
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

type fakeScheduledBackupRestore struct {
	commands []backupcontract.RestoreNodeCommand
	receipt  backupcontract.RestoreNodeReceipt
	err      error
}

func (r *fakeScheduledBackupRestore) Run(
	_ context.Context,
	command backupcontract.RestoreNodeCommand,
) (backupcontract.RestoreNodeReceipt, error) {
	r.commands = append(r.commands, command)
	return r.receipt, r.err
}

type countingRPCNode struct {
	calls int
}

func (n *countingRPCNode) CallRPC(
	context.Context,
	uint64,
	uint8,
	[]byte,
) ([]byte, error) {
	n.calls++
	return nil, errors.New("unexpected RPC call")
}
