package metrics

import (
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestGatewayMetricsTrackConnectionAndTraffic(t *testing.T) {
	reg := New(1, "node-1")

	reg.Gateway.ConnectionOpened("tcp")
	reg.Gateway.Auth("ok", 25*time.Millisecond)
	reg.Gateway.MessageReceived("tcp", 12)
	reg.Gateway.MessageDelivered("tcp", 18)
	reg.Gateway.FrameHandled("SEND", 5*time.Millisecond)
	reg.Gateway.ConnectionClosed("tcp")

	families, err := reg.Gather()
	require.NoError(t, err)

	active := requireMetricFamily(t, families, "wukongim_gateway_connections_active")
	require.Len(t, active.GetMetric(), 1)
	requireMetricLabels(t, active.GetMetric()[0], map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"protocol":  "tcp",
	})
	require.Equal(t, float64(0), active.GetMetric()[0].GetGauge().GetValue())

	receivedBytes := requireMetricFamily(t, families, "wukongim_gateway_messages_received_bytes_total")
	require.Len(t, receivedBytes.GetMetric(), 1)
	requireMetricLabels(t, receivedBytes.GetMetric()[0], map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"protocol":  "tcp",
	})
	require.Equal(t, float64(12), receivedBytes.GetMetric()[0].GetCounter().GetValue())

	deliveredTotal := requireMetricFamily(t, families, "wukongim_gateway_messages_delivered_total")
	require.Len(t, deliveredTotal.GetMetric(), 1)
	requireMetricLabels(t, deliveredTotal.GetMetric()[0], map[string]string{
		"node_id":   "1",
		"node_name": "node-1",
		"protocol":  "tcp",
	})
	require.Equal(t, float64(1), deliveredTotal.GetMetric()[0].GetCounter().GetValue())
}

func TestChannelMetricsTrackAppendFetchAndActiveChannels(t *testing.T) {
	reg := New(7, "node-7")

	reg.Channel.ObserveAppend("ok", 15*time.Millisecond)
	reg.Channel.ObserveFetch(8 * time.Millisecond)
	reg.Channel.SetActiveChannels(3)

	families, err := reg.Gather()
	require.NoError(t, err)

	appendTotal := requireMetricFamily(t, families, "wukongim_channel_append_total")
	require.Len(t, appendTotal.GetMetric(), 1)
	requireMetricLabels(t, appendTotal.GetMetric()[0], map[string]string{
		"node_id":   "7",
		"node_name": "node-7",
		"result":    "ok",
	})
	require.Equal(t, float64(1), appendTotal.GetMetric()[0].GetCounter().GetValue())

	active := requireMetricFamily(t, families, "wukongim_channel_active_channels")
	require.Len(t, active.GetMetric(), 1)
	requireMetricLabels(t, active.GetMetric()[0], map[string]string{
		"node_id":   "7",
		"node_name": "node-7",
	})
	require.Equal(t, float64(3), active.GetMetric()[0].GetGauge().GetValue())
}

func TestSlotAndTransportMetricsTrackProposalsLeaderChangesAndRPCs(t *testing.T) {
	reg := New(9, "node-9")

	reg.Slot.ObserveProposal(3, 12*time.Millisecond)
	reg.Slot.ObserveLeaderChange(3)
	reg.Transport.ObserveRPC("controller_list_nodes", "ok", 7*time.Millisecond)
	reg.Transport.ObserveRPC("controller_list_nodes", "err", 9*time.Millisecond)
	reg.Transport.ObserveSentBytes("rpc_request", 64)
	reg.Transport.ObserveReceivedBytes("rpc_response", 72)
	reg.Transport.SetPoolConnections(
		map[string]int{"2": 1, "3": 2},
		map[string]int{"2": 3, "3": 2},
	)

	families, err := reg.Gather()
	require.NoError(t, err)

	proposals := requireMetricFamily(t, families, "wukongim_slot_proposals_total")
	require.Len(t, proposals.GetMetric(), 1)
	requireMetricLabels(t, proposals.GetMetric()[0], map[string]string{
		"node_id":   "9",
		"node_name": "node-9",
		"slot_id":   "3",
	})
	require.Equal(t, float64(1), proposals.GetMetric()[0].GetCounter().GetValue())

	leaderChanges := requireMetricFamily(t, families, "wukongim_slot_leader_elections_total")
	require.Len(t, leaderChanges.GetMetric(), 1)
	requireMetricLabels(t, leaderChanges.GetMetric()[0], map[string]string{
		"node_id":   "9",
		"node_name": "node-9",
	})
	require.Equal(t, float64(1), leaderChanges.GetMetric()[0].GetCounter().GetValue())

	rpcTotal := requireMetricFamily(t, families, "wukongim_transport_rpc_total")
	require.Len(t, rpcTotal.GetMetric(), 2)
	for _, metric := range rpcTotal.GetMetric() {
		requireMetricLabels(t, metric, map[string]string{
			"node_id":   "9",
			"node_name": "node-9",
			"service":   "controller_list_nodes",
		})
	}

	sentBytes := requireMetricFamily(t, families, "wukongim_transport_sent_bytes_total")
	require.Len(t, sentBytes.GetMetric(), 1)
	requireMetricLabels(t, sentBytes.GetMetric()[0], map[string]string{
		"node_id":   "9",
		"node_name": "node-9",
		"msg_type":  "rpc_request",
	})
	require.Equal(t, float64(64), sentBytes.GetMetric()[0].GetCounter().GetValue())

	receivedBytes := requireMetricFamily(t, families, "wukongim_transport_received_bytes_total")
	require.Len(t, receivedBytes.GetMetric(), 1)
	requireMetricLabels(t, receivedBytes.GetMetric()[0], map[string]string{
		"node_id":   "9",
		"node_name": "node-9",
		"msg_type":  "rpc_response",
	})
	require.Equal(t, float64(72), receivedBytes.GetMetric()[0].GetCounter().GetValue())

	poolActive := requireMetricFamily(t, families, "wukongim_transport_connections_pool_active")
	require.Len(t, poolActive.GetMetric(), 2)

	poolIdle := requireMetricFamily(t, families, "wukongim_transport_connections_pool_idle")
	require.Len(t, poolIdle.GetMetric(), 2)
}

func TestTransportMetricsTrackRPCClientDialAndEnqueue(t *testing.T) {
	reg := New(15, "node-15")

	reg.Transport.ObserveRPCClient("3", "channel_append", "ok", 7*time.Millisecond)
	reg.Transport.ObserveRPCClient("3", "channel_append", "timeout", 11*time.Millisecond)
	reg.Transport.SetRPCInflight("3", "channel_append", 2)
	reg.Transport.ObserveEnqueue("3", "rpc", "queue_full")
	reg.Transport.ObserveDial("3", "dial_error", 5*time.Millisecond)

	families, err := reg.Gather()
	require.NoError(t, err)

	rpcTotal := requireMetricFamily(t, families, "wukongim_transport_rpc_client_total")
	require.Len(t, rpcTotal.GetMetric(), 2)

	rpcDuration := requireMetricFamily(t, families, "wukongim_transport_rpc_client_duration_seconds")
	require.Len(t, rpcDuration.GetMetric(), 1)
	requireMetricLabels(t, rpcDuration.GetMetric()[0], map[string]string{
		"node_id":     "15",
		"node_name":   "node-15",
		"target_node": "3",
		"service":     "channel_append",
	})

	rpcInflight := requireMetricFamily(t, families, "wukongim_transport_rpc_inflight")
	require.Len(t, rpcInflight.GetMetric(), 1)
	requireMetricLabels(t, rpcInflight.GetMetric()[0], map[string]string{
		"node_id":     "15",
		"node_name":   "node-15",
		"target_node": "3",
		"service":     "channel_append",
	})
	require.Equal(t, float64(2), rpcInflight.GetMetric()[0].GetGauge().GetValue())

	enqueueTotal := requireMetricFamily(t, families, "wukongim_transport_enqueue_total")
	require.Len(t, enqueueTotal.GetMetric(), 1)
	requireMetricLabels(t, enqueueTotal.GetMetric()[0], map[string]string{
		"node_id":     "15",
		"node_name":   "node-15",
		"target_node": "3",
		"kind":        "rpc",
		"result":      "queue_full",
	})
	require.Equal(t, float64(1), enqueueTotal.GetMetric()[0].GetCounter().GetValue())

	dialTotal := requireMetricFamily(t, families, "wukongim_transport_dial_total")
	require.Len(t, dialTotal.GetMetric(), 1)
	requireMetricLabels(t, dialTotal.GetMetric()[0], map[string]string{
		"node_id":     "15",
		"node_name":   "node-15",
		"target_node": "3",
		"result":      "dial_error",
	})
	require.Equal(t, float64(1), dialTotal.GetMetric()[0].GetCounter().GetValue())

	dialDuration := requireMetricFamily(t, families, "wukongim_transport_dial_duration_seconds")
	require.Len(t, dialDuration.GetMetric(), 1)
	requireMetricLabels(t, dialDuration.GetMetric()[0], map[string]string{
		"node_id":     "15",
		"node_name":   "node-15",
		"target_node": "3",
	})
}

func TestControllerMetricsTrackDecisionStateAndTaskCounts(t *testing.T) {
	reg := New(11, "node-11")

	reg.Controller.ObserveDecision("repair", 18*time.Millisecond)
	reg.Controller.ObserveTaskCompleted("repair", "ok")
	reg.Controller.ObserveMigrationCompleted("ok")
	reg.Controller.SetNodeCounts(2, 1, 1)
	reg.Controller.SetTaskActive(map[string]int{
		"repair":    2,
		"rebalance": 1,
	})
	reg.Controller.SetMigrationsActive(2)

	families, err := reg.Gather()
	require.NoError(t, err)

	decisions := requireMetricFamily(t, families, "wukongim_controller_decisions_total")
	require.Len(t, decisions.GetMetric(), 3)
	foundRepair := false
	for _, metric := range decisions.GetMetric() {
		labels := metric.GetLabel()
		for _, label := range labels {
			if label.GetName() == "type" && label.GetValue() == "repair" {
				requireMetricLabels(t, metric, map[string]string{
					"node_id":   "11",
					"node_name": "node-11",
					"type":      "repair",
				})
				require.Equal(t, float64(1), metric.GetCounter().GetValue())
				foundRepair = true
			}
		}
	}
	require.True(t, foundRepair, "repair decision counter should be present")

	nodesAlive := requireMetricFamily(t, families, "wukongim_controller_nodes_alive")
	require.Len(t, nodesAlive.GetMetric(), 1)
	require.Equal(t, float64(2), nodesAlive.GetMetric()[0].GetGauge().GetValue())

	tasksActive := requireMetricFamily(t, families, "wukongim_controller_tasks_active")
	require.Len(t, tasksActive.GetMetric(), 3)

	migrationsActive := requireMetricFamily(t, families, "wukongim_controller_hashslot_migrations_active")
	require.Len(t, migrationsActive.GetMetric(), 1)
	require.Equal(t, float64(2), migrationsActive.GetMetric()[0].GetGauge().GetValue())

	migrationsTotal := requireMetricFamily(t, families, "wukongim_controller_hashslot_migrations_total")
	require.Len(t, migrationsTotal.GetMetric(), 3)
	foundOK := false
	for _, metric := range migrationsTotal.GetMetric() {
		for _, label := range metric.GetLabel() {
			if label.GetName() == "result" && label.GetValue() == "ok" {
				require.Equal(t, float64(1), metric.GetCounter().GetValue())
				foundOK = true
			}
		}
	}
	require.True(t, foundOK, "ok migration counter should be present")
}

func TestStorageMetricsTrackDiskUsageByStore(t *testing.T) {
	reg := New(13, "node-13")

	reg.Storage.SetDiskUsage(map[string]int64{
		"meta":            12,
		"channel_log":     34,
		"controller_meta": 56,
	})

	families, err := reg.Gather()
	require.NoError(t, err)

	usage := requireMetricFamily(t, families, "wukongim_storage_disk_usage_bytes")
	require.Len(t, usage.GetMetric(), 5)

	want := map[string]float64{
		"meta":            12,
		"raft":            0,
		"channel_log":     34,
		"controller_meta": 56,
		"controller_raft": 0,
	}
	for _, metric := range usage.GetMetric() {
		labels := map[string]string{}
		for _, label := range metric.GetLabel() {
			labels[label.GetName()] = label.GetValue()
		}
		store := labels["store"]
		expected, ok := want[store]
		require.True(t, ok, "unexpected store label %q", store)
		require.Equal(t, expected, metric.GetGauge().GetValue(), "store %s", store)
	}
}

func requireMetricFamily(t *testing.T, families []*dto.MetricFamily, name string) *dto.MetricFamily {
	t.Helper()
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	t.Fatalf("metric family %q not found", name)
	return nil
}

func requireMetricLabels(t *testing.T, metric *dto.Metric, want map[string]string) {
	t.Helper()
	got := make(map[string]string, len(metric.GetLabel()))
	for _, label := range metric.GetLabel() {
		got[label.GetName()] = label.GetValue()
	}
	for key, value := range want {
		require.Equal(t, value, got[key], "label %s", key)
	}
}
