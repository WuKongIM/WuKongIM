//go:build e2e

package storage_capacity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
)

// Separate sources prevent one shared body or one Session's quota from
// substituting for aggregate admission.
func TestAggregateStorageRejectsNewDebt(t *testing.T) {
	runStorageCapacity(t, 1, 8192, 8192, false, false)
}

func TestAggregateStorageSurvivesRestartAndReopens(t *testing.T) {
	runStorageCapacity(t, 1, 8192, 8192, true, false)
}

func TestThreeNodeStorageLimits(t *testing.T) {
	for _, q := range []struct {
		name          string
		node, cluster uint64
	}{{"node", 8192, 98304}, {"cluster", 65536, 24576}} {
		t.Run(q.name, func(t *testing.T) { runStorageCapacity(t, 3, q.node, q.cluster, false, false) })
	}
}

// The legacy process has config parsing only, without capacity accounting.
// Its exact binary hash is recorded in the acceptance artifact provenance.
func TestThreeNodeRegistersLegacyDebtBeforeAdmission(t *testing.T) {
	if os.Getenv("WK_E2E_MQTT_STORAGE_LEGACY_BINARY") == "" {
		t.Skip("requires the recorded config-only pre-capacity binary")
	}
	runStorageCapacity(t, 3, 65536, 24576, true, true)
}

// Unknown outcomes are injected only in temporary-copy instrumented binaries.
func TestUnknownCancellationSettlesFromDurableFloor(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" {
		t.Skip("requires temporary-copy gofail binary")
	}
	t.Setenv("WK_E2E_MQTT_CAPACITY_FAULT", "wkMQTTStorageCancelAfterCommit")
	runStorageCapacity(t, 3, 65536, 24576, false, false)
}
func TestUnknownPreparationSettlesFromCancellationFloor(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" {
		t.Skip("requires temporary-copy gofail binary")
	}
	t.Setenv("WK_E2E_MQTT_CAPACITY_FAULT", "wkMQTTStoragePrepareBeforeCommit")
	runStorageCapacity(t, 1, 8192, 8192, false, false)
}
func TestUnknownRetirementSettlesFromDurableMarker(t *testing.T) {
	if os.Getenv("WK_E2E_GOFAIL_MQTT") != "1" {
		t.Skip("requires temporary-copy gofail binary")
	}
	t.Setenv("WK_E2E_MQTT_CAPACITY_FAULT", "wkMQTTStorageRetirementAfterCommit")
	runStorageCapacity(t, 1, 8192, 8192, true, false)
}

func runStorageCapacity(t *testing.T, count int, nodeBytes, clusterBytes uint64, restart, legacy bool) {
	t.Helper()
	candidate := os.Getenv("WK_E2E_BINARY")
	if legacy {
		require.NotEmpty(t, candidate)
		t.Setenv("WK_E2E_BINARY", os.Getenv("WK_E2E_MQTT_STORAGE_LEGACY_BINARY"))
	}
	s := suite.New(t)
	addrs := make([]string, count)
	var opts []suite.Option
	faultName := os.Getenv("WK_E2E_MQTT_CAPACITY_FAULT")
	var faults []suite.GofailEndpoint
	if root := os.Getenv("WK_E2E_MQTT_CAPACITY_WORKSPACE"); root != "" {
		opts = append(opts, suite.WithWorkspaceRootDir(root))
	}
	for i := range count {
		if faultName != "" {
			f := suite.ReserveGofailEndpoint(t)
			faults = append(faults, f)
			opts = append(opts, suite.WithNodeEnv(uint64(i+1), f.Env(), "GOFAIL_FAILPOINTS="+faultName+"=1*return(true)"))
		}
		addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
		opts = append(opts, suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true",
			"WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i], "WK_METRICS_ENABLE": "true",
			"WK_MQTT_QUOTA_MESSAGES": "1000", "WK_MQTT_QUOTA_BYTES": "8388608",
		}), suite.WithNodeEnv(uint64(i+1), fmt.Sprintf("WK_MQTT_STORAGE_NODE_BYTES=%d", nodeBytes), fmt.Sprintf("WK_MQTT_STORAGE_CLUSTER_BYTES=%d", clusterBytes)))
	}
	var c3 *suite.StartedCluster
	var n *suite.StartedNode
	var nodes []*suite.StartedNode
	if count == 1 {
		n = s.StartSingleNodeCluster(opts...)
		nodes = []*suite.StartedNode{n}
	} else {
		cluster := s.StartThreeNodeCluster(append(opts, suite.WithManagerHTTP())...)
		ready, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		require.NoError(t, cluster.WaitClusterReady(ready), cluster.DumpDiagnostics())
		_, err := cluster.WaitSlotLeadersStable(ready, time.Second)
		cancel()
		require.NoError(t, err, cluster.DumpDiagnostics())
		c3 = cluster
		n = cluster.MustNode(1)
		for i := range count {
			nodes = append(nodes, cluster.MustNode(uint64(i+1)))
		}
	}
	for _, f := range faults {
		inspect, done := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := f.WaitListed(inspect, faultName)
		done()
		require.NoError(t, err, "instrumented candidate lacks the requested physical outcome cut")
	}
	addr := addrs[0]
	payloadBytes := 2048
	if legacy {
		payloadBytes = 4096
	}
	report := map[string]any{"nodes": count, "hash_slots": 256, "node_bytes": nodeBytes,
		"fault": faultName, "cluster_bytes": clusterBytes, "payload_bytes": payloadBytes, "legacy_debt": legacy, "passed": false}
	t.Cleanup(func() {
		dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
		if dir == "" {
			dir = n.Spec.RootDir
		}
		require.NoError(t, os.MkdirAll(dir, 0755))
		if faultName != "" {
			counts := make([]int, len(faults))
			inspect, done := context.WithTimeout(context.Background(), 3*time.Second)
			defer done()
			for i, f := range faults {
				n, err := f.Count(inspect, faultName)
				require.NoError(t, err)
				counts[i] = n
			}
			var total int
			for _, n := range counts {
				total += n
			}
			require.Positive(t, total, "physical outcome cut was never exercised")
			report["fault_hits"] = counts
		}
		report["passed"] = !t.Failed()
		roots := make([]string, len(nodes))
		for i, node := range nodes {
			roots[i] = node.Spec.RootDir
		}
		report["node_roots"] = roots
		if t.Failed() {
			snapshots := map[uint64]any{}
			for _, node := range nodes {
				inspect, done := context.WithTimeout(context.Background(), 2*time.Second)
				samples, err := suite.FetchMetricSamples(inspect, node.APIAddr())
				done()
				var selected []suite.MetricSample
				for _, sample := range samples {
					if strings.HasPrefix(sample.Name, "wukongim_mqtt_") {
						selected = append(selected, sample)
					}
				}
				snapshots[node.Spec.ID] = map[string]any{"error": fmt.Sprint(err), "metrics": selected}
			}
			report["failure_metrics"] = snapshots
		}
		data, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		name := "aggregate-storage-tracer.json"
		if restart {
			name = "aggregate-storage-restart.json"
		}
		if count == 3 {
			name = fmt.Sprintf("aggregate-storage-%d-%d.json", nodeBytes, clusterBytes)
		}
		if legacy {
			name = "aggregate-storage-legacy-bootstrap.json"
		}
		if faultName != "" {
			name = "aggregate-storage-" + faultName + ".json"
		}
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
		t.Logf("result artifact: %s", path)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	for _, uid := range []string{"alice", "bob", "carol"} {
		_, err := suite.PostJSON(ctx, "http://"+n.APIAddr()+"/user/token", map[string]any{
			"uid": uid, "token": uid + "-capacity-token", "device_flag": 1, "device_level": 1}, nil)
		require.NoError(t, err)
		if uid == "alice" {
			continue
		}
		c, err := suite.ConnectMQTT(ctx, addr, uid, uid+"-capacity-token", "capacity-"+uid,
			false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
		require.NoError(t, err, n.DumpDiagnostics())
		t.Cleanup(func() { _ = c.Abort() })
		topic := "wk/v1/users/" + base64.RawURLEncoding.EncodeToString([]byte(uid)) + "/messages"
		ack, err := c.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: 1}}})
		require.NoError(t, err)
		require.Equal(t, []byte{1}, ack.Reasons)
		require.NoError(t, c.Close())
	}
	alice, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = alice.Close() })
	_, err = alice.ConnectAuthenticatedContext(ctx, n.GatewayAddr(), "alice", "capacity-wk",
		"alice-capacity-token", frame.WEB)
	require.NoError(t, err)
	var accepted *frame.SendackPacket
	var before []float64
	reserved := func() []float64 {
		var values []float64
		for _, node := range nodes {
			value, err := suite.FetchMetricValue(ctx, node.APIAddr(), "wukongim_mqtt_storage_bytes", map[string]string{"state": "reserved"})
			require.NoError(t, err)
			values = append(values, value)
		}
		return values
	}
	for i, uid := range []string{"bob", "carol"} {
		if legacy && i == 1 {
			break
		}
		err = alice.SendFrame(&frame.SendPacket{ChannelID: uid, ChannelType: frame.ChannelTypePerson,
			ClientSeq: uint64(i + 1), ClientMsgNo: fmt.Sprintf("capacity-%d", i+1), Payload: make([]byte, payloadBytes)})
		require.NoError(t, err)
		ack, err := alice.ReadSendAck()
		require.NoError(t, err, n.DumpDiagnostics())
		report[uid+"_reason"] = ack.ReasonCode
		if i == 0 {
			if faultName == "wkMQTTStoragePrepareBeforeCommit" {
				require.NotEqual(t, frame.ReasonSuccess, ack.ReasonCode, "unknown prepare dispatched an original")
				require.Eventually(t, func() bool {
					for _, n := range reserved() {
						if n != 0 {
							return false
						}
					}
					return true
				}, 10*time.Second, 100*time.Millisecond, "handle reclamation lost an unknown preparation debit")
				report["reserved_after_preparation_cancellation"] = reserved()
				require.NoError(t, alice.SendFrame(&frame.SendPacket{ChannelID: uid, ChannelType: frame.ChannelTypePerson, ClientSeq: 1, ClientMsgNo: "capacity-1", Payload: make([]byte, payloadBytes)}))
				ack, err = alice.ReadSendAck()
				require.NoError(t, err)
				report["unknown_preparation_canceled"] = true
			}
			require.Equal(t, frame.ReasonSuccess, ack.ReasonCode, "first bounded responsibility was rejected: %s", n.DumpDiagnostics())
			report["accepted_message_id"] = ack.MessageID
			accepted = ack
			if legacy {
				continue
			}
			require.Eventually(t, func() bool {
				for _, used := range reserved() {
					if used == 0 {
						return false
					}
				}
				return true
			}, 5*time.Second, 100*time.Millisecond, "successful receipt did not fund every storage replica")
			before = reserved()
			report["reserved_before_denial"] = before
		} else {
			require.NotEqual(t, frame.ReasonSuccess, ack.ReasonCode,
				"new source exceeded aggregate capacity while both Sessions remained below their quotas")
			after := reserved()
			report["reserved_after_denial"] = after
			if faultName == "wkMQTTStorageCancelAfterCommit" {
				require.Eventually(t, func() bool { return fmt.Sprint(before) == fmt.Sprint(reserved()) }, 10*time.Second, 100*time.Millisecond, "unknown cancellation failed to reconcile its exact durable floor")
				report["reserved_after_cancellation_settlement"] = reserved()
			} else {
				require.Equal(t, before, after, "capacity refusal left a partly written original obligation on some replica")
			}
		}
	}
	if !restart {
		return
	}
	require.NoError(t, alice.Close())
	if legacy {
		for _, node := range nodes {
			require.NoError(t, node.Process.Stop())
		}
		for _, node := range nodes {
			node.Process = &suite.NodeProcess{Spec: node.Spec, BinaryPath: candidate}
			require.NoError(t, node.Process.Start(), node.DumpDiagnostics())
		}
		ready, done := context.WithTimeout(ctx, 30*time.Second)
		require.NoError(t, c3.WaitClusterReady(ready), c3.DumpDiagnostics())
		done()
	} else {
		require.NoError(t, n.Restart(n.Process.BinaryPath), n.DumpDiagnostics())
	}
	require.NoError(t, n.Process.WaitWKProtoReady(ctx, n.GatewayAddr()), n.DumpDiagnostics())
	report["joined_restart"] = true
	alice, err = suite.NewWKProtoClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = alice.Close() })
	_, err = alice.ConnectAuthenticatedContext(ctx, n.GatewayAddr(), "alice", "capacity-wk-after", "alice-capacity-token", frame.WEB)
	require.NoError(t, err)
	sendCarol := func(seq uint64, no string) *frame.SendackPacket {
		require.NoError(t, alice.SendFrame(&frame.SendPacket{ChannelID: "carol", ChannelType: frame.ChannelTypePerson, ClientSeq: seq, ClientMsgNo: no, Payload: make([]byte, 2048)}))
		ack, err := alice.ReadSendAck()
		require.NoError(t, err, n.DumpDiagnostics())
		return ack
	}
	require.NotEqual(t, frame.ReasonSuccess, sendCarol(3, "capacity-after-restart-full").ReasonCode, "restart reset aggregate use")
	bob, err := suite.ConnectMQTT(ctx, addr, "bob", "bob-capacity-token", "capacity-bob", false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
	require.NoError(t, err, n.DumpDiagnostics())
	t.Cleanup(func() { _ = bob.Abort() })
	require.True(t, bob.Connack.SessionPresent)
	p, err := bob.Receive(ctx)
	require.NoError(t, err, n.DumpDiagnostics())
	require.Equal(t, make([]byte, payloadBytes), p.Payload)
	require.Equal(t, strconv.FormatInt(accepted.MessageID, 10), p.Properties.User.Get("wk.message_id"))
	report["replayed_message_id"] = p.Properties.User.Get("wk.message_id")
	require.NoError(t, bob.Client.Ack(p))
	if legacy {
		quiet, done := context.WithTimeout(ctx, 3*time.Second)
		extra, err := bob.Receive(quiet)
		done()
		require.ErrorIs(t, err, context.DeadlineExceeded, "native authority maintenance interrupted MQTT: %+v", extra)
	}
	require.Eventually(t, func() bool {
		for _, node := range nodes {
			used, err := suite.FetchMetricValue(ctx, node.APIAddr(), "wukongim_mqtt_storage_bytes", map[string]string{"state": "reserved"})
			if err != nil || used != 0 {
				return false
			}
		}
		return true
	}, 25*time.Second, 100*time.Millisecond, "ACK did not lead to proved shared-body retirement: %s", n.DumpDiagnostics())
	report["safe_retirement"] = true
	report["reserved_after_retirement"] = reserved()
	var reopened *frame.SendackPacket
	require.Eventually(t, func() bool {
		reopened = sendCarol(4, "capacity-reopened")
		return reopened.ReasonCode == frame.ReasonSuccess
	}, 15*time.Second, 250*time.Millisecond, "idle cluster escrow was not returned")
	require.Equal(t, frame.ReasonSuccess, reopened.ReasonCode, "proved retirement did not reopen new admission")
	report["reopened_message_id"] = reopened.MessageID

}

// Distinct producers on distinct ingress nodes race the same cluster escrow.
// If their prepares conflict, exact keyed retries wait for canceled credit.
func TestConcurrentSourcesShareAggregateCapacity(t *testing.T) {
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d-node-cluster", count), func(t *testing.T) {
			s := suite.New(t)
			addrs := make([]string, count)
			opts := []suite.Option{}
			if root := os.Getenv("WK_E2E_MQTT_CAPACITY_WORKSPACE"); root != "" {
				opts = append(opts, suite.WithWorkspaceRootDir(root))
			}
			for i := range count {
				addrs[i] = suite.ReserveLoopbackPorts(t).GatewayAddr
				opts = append(opts, suite.WithNodeConfigOverrides(uint64(i+1), map[string]string{
					"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MQTT_ENABLE": "true", "WK_MQTT_LISTEN_ADDR": addrs[i], "WK_METRICS_ENABLE": "true",
					"WK_MQTT_STORAGE_NODE_BYTES": "8192", "WK_MQTT_STORAGE_CLUSTER_BYTES": strconv.Itoa(8192 * count),
				}))
			}
			var nodes []*suite.StartedNode
			ctx, done := context.WithTimeout(context.Background(), 90*time.Second)
			defer done()
			if count == 1 {
				nodes = []*suite.StartedNode{s.StartSingleNodeCluster(opts...)}
			} else {
				c := s.StartThreeNodeCluster(append(opts, suite.WithManagerHTTP())...)
				require.NoError(t, c.WaitClusterReady(ctx), c.DumpDiagnostics())
				for i := range count {
					nodes = append(nodes, c.MustNode(uint64(i+1)))
				}
			}
			first, last := nodes[0], nodes[count-1]
			report := map[string]any{"nodes": count, "hash_slots": 256, "cluster_bytes": 8192 * count, "passed": false}
			t.Cleanup(func() {
				dir := os.Getenv("WK_E2E_MQTT_REPORT_DIR")
				if dir == "" {
					dir = first.Spec.RootDir
				}
				require.NoError(t, os.MkdirAll(dir, 0755))
				report["passed"] = !t.Failed()
				data, err := json.MarshalIndent(report, "", "  ")
				require.NoError(t, err)
				path := filepath.Join(dir, fmt.Sprintf("aggregate-storage-concurrent-%d.json", count))
				require.NoError(t, os.WriteFile(path, append(data, '\n'), 0600))
				t.Logf("result artifact: %s", path)
			})
			for _, uid := range []string{"alice", "dave", "bob", "carol"} {
				_, err := suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": uid + "-concurrent-token", "device_flag": 1, "device_level": 1}, nil)
				require.NoError(t, err)
			}
			consumers := make([]*suite.MQTTClient, 2)
			uids := []string{"bob", "carol"}
			// Establish persistent offline obligations before either producer can race.
			for _, uid := range uids {
				c, err := suite.ConnectMQTT(ctx, addrs[0], uid, uid+"-concurrent-token", "concurrent-"+uid, false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
				require.NoError(t, err)
				topic := "wk/v1/users/" + base64.RawURLEncoding.EncodeToString([]byte(uid)) + "/messages"
				ack, err := c.Client.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: 1}}})
				require.NoError(t, err)
				require.Equal(t, []byte{1}, ack.Reasons)
				require.NoError(t, c.Close())
			}
			clients := make([]*suite.WKProtoClient, 2)
			for i, uid := range []string{"alice", "dave"} {
				c, err := suite.NewWKProtoClient()
				require.NoError(t, err)
				clients[i] = c
				t.Cleanup(func() { _ = c.Close() })
				node := first
				if i == 1 {
					node = last
				}
				_, err = c.ConnectAuthenticatedContext(ctx, node.GatewayAddr(), uid, "concurrent-producer", uid+"-concurrent-token", frame.WEB)
				require.NoError(t, err)
			}
			type result struct {
				index int
				ack   *frame.SendackPacket
				err   error
			}
			send := func(i int) result {
				c := clients[i]
				err := c.SendFrame(&frame.SendPacket{ChannelID: uids[i], ChannelType: frame.ChannelTypePerson, ClientSeq: 1, ClientMsgNo: "concurrent-funded", Payload: make([]byte, 2048)})
				if err != nil {
					return result{i, nil, err}
				}
				ack, err := c.ReadSendAck()
				return result{i, ack, err}
			}
			start := make(chan struct{})
			results := make(chan result, 2)
			for i := range 2 {
				go func() { <-start; results <- send(i) }()
			}
			close(start)
			winner := -1
			initialSuccesses := 0
			for range 2 {
				r := <-results
				require.NoError(t, r.err)
				if r.ack.ReasonCode == frame.ReasonSuccess {
					initialSuccesses++
					winner = r.index
				}
			}
			require.LessOrEqual(t, initialSuccesses, 1, "concurrent receipts overspent shared capacity")
			report["initial_successes"] = initialSuccesses
			if winner < 0 {
				require.Eventually(t, func() bool {
					r := send(0)
					require.NoError(t, r.err)
					if r.ack.ReasonCode == frame.ReasonSuccess {
						winner = 0
						return true
					}
					return false
				}, 15*time.Second, 250*time.Millisecond, "conflicting prepares failed to return space")
			}
			var used, granted []float64
			for _, n := range nodes {
				u, err := suite.FetchMetricValue(ctx, n.APIAddr(), "wukongim_mqtt_storage_bytes", map[string]string{"state": "reserved"})
				require.NoError(t, err)
				require.LessOrEqual(t, u, float64(8192))
				used = append(used, u)
				g, err := suite.FetchMetricValue(ctx, n.APIAddr(), "wukongim_mqtt_storage_bytes", map[string]string{"state": "granted"})
				require.NoError(t, err)
				granted = append(granted, g)
			}
			var total, escrow float64
			for i := range used {
				total += used[i]
				escrow += granted[i]
			}
			require.LessOrEqual(t, total, float64(8192*count))
			require.LessOrEqual(t, escrow, float64(8192*count))
			report["reserved"] = used
			report["granted"] = granted
			report["winner"] = winner
			for i, uid := range uids {
				c, err := suite.ConnectMQTT(ctx, addrs[count-1], uid, uid+"-concurrent-token", "concurrent-"+uid, false, 600, suite.MQTTConnectOptions{ManualAcknowledgements: true, ReceiveMaximum: 1})
				require.NoError(t, err)
				consumers[i] = c
				t.Cleanup(func() { _ = c.Abort() })
			}
			p, err := consumers[winner].Receive(ctx)
			require.NoError(t, err)
			require.Equal(t, make([]byte, 2048), p.Payload)
			quiet, stop := context.WithTimeout(ctx, 3*time.Second)
			_, err = consumers[1-winner].Receive(quiet)
			stop()
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.NoError(t, consumers[winner].Client.Ack(p))
			report["rejected_source_quiet"] = true
		})
	}
}
