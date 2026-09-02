package node

import (
	"context"
	"errors"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	runtimedelivery "github.com/WuKongIM/WuKongIM/internal/runtime/delivery"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
)

func TestNodeRPCHandlersRejectMalformedFramesBeforeCallingLocalPorts(t *testing.T) {
	adapter := New(Options{})
	channelAdapter := NewChannelAppendAdapter(ChannelAppendOptions{})
	malformed := []byte{0xff, 0x00, 0x01}
	handlers := []struct {
		name   string
		handle func(context.Context, []byte) ([]byte, error)
	}{
		{name: "presence authority", handle: adapter.HandlePresenceAuthorityRPC},
		{name: "presence owner", handle: adapter.HandlePresenceOwnerRPC},
		{name: "delivery", handle: adapter.HandleDeliveryPushRPC},
		{name: "channel append", handle: channelAdapter.HandleChannelAppendRPC},
		{name: "manager connections", handle: adapter.HandleManagerConnectionRPC},
		{name: "manager logs", handle: adapter.HandleManagerLogRPC},
		{name: "manager Controller Raft", handle: adapter.HandleManagerControllerRaftRPC},
		{name: "manager Slot Raft", handle: adapter.HandleManagerSlotRaftRPC},
		{name: "manager channels", handle: adapter.HandleManagerChannelRPC},
		{name: "manager retention", handle: adapter.HandleManagerMessageRetentionRPC},
		{name: "manager latest messages", handle: adapter.HandleManagerLatestMessagesRPC},
		{name: "manager DB inspect", handle: adapter.HandleManagerDBInspectRPC},
		{name: "manager app logs", handle: adapter.HandleManagerAppLogRPC},
		{name: "manager node config", handle: adapter.HandleManagerNodeConfigRPC},
		{name: "manager diagnostics", handle: adapter.HandleManagerDiagnosticsRPC},
		{name: "manager task audit", handle: adapter.HandleManagerTaskAuditRPC},
		{name: "manager plugins", handle: adapter.HandleManagerPluginRPC},
		{name: "node lifecycle", handle: adapter.HandleNodeLifecycleRPC},
		{name: "scheduled Slot backup", handle: adapter.HandleScheduledBackupSlotRPC},
		{name: "scheduled message backup", handle: adapter.HandleScheduledBackupMessageRPC},
		{name: "scheduled repository probe", handle: adapter.HandleScheduledBackupRepositoryProbeRPC},
		{name: "scheduled restore", handle: adapter.HandleScheduledBackupRestoreRPC},
	}

	for _, test := range handlers {
		t.Run(test.name, func(t *testing.T) {
			body, err := test.handle(context.Background(), malformed)
			if err == nil {
				t.Fatal("handler accepted malformed frame")
			}
			if body != nil {
				t.Fatalf("handler returned %d response bytes on malformed frame", len(body))
			}
		})
	}
}

func TestNodeRPCClientsPropagateCancellationAndRejectMalformedResponses(t *testing.T) {
	calls := []struct {
		name string
		call func(*Client) error
	}{
		{name: "register presence route", call: func(client *Client) error {
			_, err := client.RegisterRoute(context.Background(), presence.RouteTarget{LeaderNodeID: 2}, presence.Route{UID: "u1"})
			return err
		}},
		{name: "commit presence route", call: func(client *Client) error {
			return client.CommitRoute(context.Background(), presence.RouteTarget{LeaderNodeID: 2}, "pending")
		}},
		{name: "delivery push", call: func(client *Client) error {
			_, err := client.PushBatch(context.Background(), 2, runtimedelivery.PushCommand{OwnerNodeID: 2})
			return err
		}},
		{name: "connection detail", call: func(client *Client) error {
			_, err := client.GetManagerConnection(context.Background(), 2, 9)
			return err
		}},
		{name: "runtime summary", call: func(client *Client) error {
			_, err := client.GetManagerRuntimeSummary(context.Background(), 2)
			return err
		}},
		{name: "drain mode", call: func(client *Client) error {
			_, err := client.SetManagerDrainMode(context.Background(), 2, true)
			return err
		}},
		{name: "Controller compaction", call: func(client *Client) error {
			_, err := client.CompactManagerControllerRaftLog(context.Background(), 2)
			return err
		}},
		{name: "Slot Raft status", call: func(client *Client) error {
			_, err := client.GetManagerSlotRaftStatus(context.Background(), 2, 7)
			return err
		}},
		{name: "Slot compaction", call: func(client *Client) error {
			_, err := client.CompactManagerSlotRaftLog(context.Background(), 2, 7)
			return err
		}},
		{name: "Slot log page", call: func(client *Client) error {
			_, err := client.GetManagerSlotLogEntries(context.Background(), managementusecase.ListSlotLogEntriesRequest{NodeID: 2, SlotID: 7, Limit: 10})
			return err
		}},
		{name: "business channels", call: func(client *Client) error {
			_, err := client.ListManagerBusinessChannels(context.Background(), managementusecase.ListBusinessChannelsRequest{NodeID: 2, Limit: 10})
			return err
		}},
		{name: "message retention", call: func(client *Client) error {
			_, err := client.AdvanceManagerMessageRetention(context.Background(), 2, managementusecase.AdvanceMessageRetentionRequest{ChannelID: "room", ChannelType: 2})
			return err
		}},
		{name: "latest messages", call: func(client *Client) error {
			_, err := client.ListManagerLatestMessages(context.Background(), 2, 100, 10)
			return err
		}},
		{name: "DB inspect", call: func(client *Client) error {
			_, err := client.NodeDBInspectQuery(context.Background(), managementusecase.DBInspectQueryRequest{NodeID: 2})
			return err
		}},
		{name: "application log entries", call: func(client *Client) error {
			_, err := client.GetManagerApplicationLogEntries(context.Background(), managementusecase.ApplicationLogEntriesRequest{NodeID: 2, Source: "app", Limit: 10})
			return err
		}},
		{name: "node config", call: func(client *Client) error {
			_, err := client.GetManagerNodeConfig(context.Background(), 2)
			return err
		}},
		{name: "add diagnostics rule", call: func(client *Client) error {
			_, err := client.AddManagerDiagnosticsTrackingRule(context.Background(), 2, diagnostics.TrackingRuleInput{ID: "rule-1"})
			return err
		}},
		{name: "list diagnostics rules", call: func(client *Client) error {
			_, err := client.ListManagerDiagnosticsTrackingRules(context.Background(), 2)
			return err
		}},
		{name: "delete diagnostics rule", call: func(client *Client) error {
			return client.DeleteManagerDiagnosticsTrackingRule(context.Background(), 2, "rule-1")
		}},
		{name: "task audit events", call: func(client *Client) error {
			_, err := client.ManagerControllerTaskAuditEvents(context.Background(), 2, "task-1")
			return err
		}},
		{name: "plugin detail", call: func(client *Client) error {
			_, err := client.GetManagerPlugin(context.Background(), 2, "wk.audit")
			return err
		}},
		{name: "plugin config", call: func(client *Client) error {
			_, err := client.UpdateManagerPluginConfig(context.Background(), 2, "wk.audit", []byte(`{"enabled":true}`))
			return err
		}},
		{name: "plugin restart", call: func(client *Client) error {
			_, err := client.RestartManagerPlugin(context.Background(), 2, "wk.audit")
			return err
		}},
		{name: "plugin uninstall", call: func(client *Client) error {
			return client.UninstallManagerPlugin(context.Background(), 2, "wk.audit")
		}},
		{name: "join node", call: func(client *Client) error {
			_, err := client.JoinNode(context.Background(), 1, NodeJoinRequest{NodeID: 2, ClusterID: "cluster", JoinToken: "token"})
			return err
		}},
		{name: "node readiness", call: func(client *Client) error {
			_, err := client.NodeReadiness(context.Background(), 2, NodeReadinessRequest{NodeID: 2, ClusterID: "cluster"})
			return err
		}},
		{name: "Controller voter readiness", call: func(client *Client) error {
			_, err := client.ControllerVoterReadiness(context.Background(), 2, ControllerVoterReadinessRequest{NodeID: 2, ClusterID: "cluster"})
			return err
		}},
		{name: "prepare Controller voter", call: func(client *Client) error {
			_, err := client.PrepareControllerVoter(context.Background(), 2, PrepareControllerVoterRequest{NodeID: 2, ClusterID: "cluster"})
			return err
		}},
		{name: "Slot backup", call: func(client *Client) error {
			_, err := client.ExportBackupSlot(context.Background(), 2, backupcontract.SlotExportCommand{OwnerNodeID: 2})
			return err
		}},
		{name: "message backup", call: func(client *Client) error {
			_, err := client.ExportBackupMessages(context.Background(), 2, backupcontract.MessageExportCommand{Shard: backupcontract.MessageShard{NodeID: 2}})
			return err
		}},
		{name: "repository probe", call: func(client *Client) error {
			return client.ProbeBackupRepository(context.Background(), 2, backupcontract.RepositoryProbeCommand{})
		}},
		{name: "restore", call: func(client *Client) error {
			_, err := client.RunBackupRestoreNode(context.Background(), 2, backupcontract.RestoreNodeCommand{})
			return err
		}},
	}

	for _, test := range calls {
		t.Run(test.name, func(t *testing.T) {
			canceledNode := &nodeRPCFailureTransport{err: context.Canceled}
			if err := test.call(NewClient(canceledNode)); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled transport error = %v, want context.Canceled", err)
			}
			if canceledNode.calls != 1 {
				t.Fatalf("canceled transport calls = %d, want 1", canceledNode.calls)
			}

			malformedNode := &nodeRPCFailureTransport{response: []byte{0xff, 0x00}}
			if err := test.call(NewClient(malformedNode)); err == nil {
				t.Fatal("client accepted malformed response")
			}
			if malformedNode.calls != 1 {
				t.Fatalf("malformed response transport calls = %d, want 1", malformedNode.calls)
			}
		})
	}

	t.Run("channel append aligned cancellation", func(t *testing.T) {
		node := &nodeRPCFailureTransport{err: context.Canceled}
		results := NewClient(node).ForwardSendBatch(
			context.Background(),
			channelappend.AuthorityTarget{LeaderNodeID: 2},
			[]channelappend.SendBatchItem{{Command: channelappend.SendCommand{ChannelID: "room", ChannelType: 2}}},
		)
		if len(results) != 1 || !errors.Is(results[0].Err, context.Canceled) {
			t.Fatalf("ForwardSendBatch() results = %#v, want one aligned cancellation", results)
		}
	})
}

type nodeRPCFailureTransport struct {
	response []byte
	err      error
	calls    int
}

func (n *nodeRPCFailureTransport) CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error) {
	n.calls++
	return append([]byte(nil), n.response...), n.err
}
