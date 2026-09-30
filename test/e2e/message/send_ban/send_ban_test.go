//go:build e2e

package send_ban

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

type observation struct {
	Topology      int       `json:"nodes"`
	Label         string    `json:"label"`
	Reason        uint8     `json:"reason"`
	Seq           uint64    `json:"seq"`
	StartedAt     time.Time `json:"started_at"`
	ElapsedMicros int64     `json:"elapsed_us"`
}

type policy struct {
	Ban     int    `json:"send_ban"`
	Version string `json:"send_ban_version"`
}

// Exercise actual gateway batches with each declared UID/Channel distribution.
// Public counters prove batching and bounded deduplication; complete history
// checks retain per-request decisions instead of inferring them from totals.
func TestSendBanGatewayDistributions(t *testing.T) {
	started := time.Now().UTC()
	var observations []map[string]any
	defer func() {
		path := os.Getenv("WK_E2E_SEND_BAN_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-send-ban-report.json")
		}
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "started_at": started, "finished_at": time.Now().UTC(), "source_revision": os.Getenv("WK_E2E_SOURCE_REVISION"), "nodes": 3, "hash_slots": 256, "observations": observations, "performance_qualified": false}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path+".gateway-distributions.json", append(raw, '\n'), 0644))
	}()
	opts := []suite.Option{suite.WithManagerHTTP()}
	for i := uint64(1); i <= 3; i++ {
		opts = append(opts, suite.WithNodeConfigOverrides(i, map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_MESSAGE_PERMISSION_CACHE_TTL": "1h"}))
	}
	cluster := suite.New(t).StartThreeNodeCluster(opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx))
	_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	metrics := func() map[string]float64 {
		t.Helper()
		out := map[string]float64{}
		for nodeID := uint64(1); nodeID <= 3; nodeID++ {
			samples, err := suite.FetchMetricSamples(ctx, cluster.MustNode(nodeID).APIAddr())
			require.NoError(t, err)
			for _, kind := range []string{"messages", "users", "channels", "facts_before", "facts", "node_envelopes", "slot_groups"} {
				out[kind] += suite.SumMetricSamples(samples, "wukongim_message_permission_counts_total", map[string]string{"kind": kind})
			}
			out["gateway_records"] += suite.SumMetricSamples(samples, "wukongim_gateway_async_send_batch_records_sum", nil)
			out["gateway_batches"] += suite.SumMetricSamples(samples, "wukongim_gateway_async_send_batch_records_count", nil)
		}
		return out
	}
	for _, config := range []struct {
		name                     string
		users, channels, perUser int
	}{
		{"one-user-one-channel", 1, 1, 32},
		{"many-users-one-channel", 4, 1, 8},
		{"one-user-many-channels", 1, 16, 64},
		{"many-users-many-channels", 16, 16, 8},
	} {
		t.Run(config.name, func(t *testing.T) {
			uids := make([]string, config.users)
			clients := make([]*suite.WKProtoClient, config.users)
			for i := range uids {
				uids[i] = fmt.Sprintf("gateway-%s-user-%02d", config.name, i)
				client, err := suite.NewWKProtoClientWithTimeout(10 * time.Second)
				require.NoError(t, err)
				require.NoError(t, client.Connect(cluster.MustNode(uint64(i%3+1)).GatewayAddr(), uids[i], "gateway-burst-device"))
				defer client.Close()
				clients[i] = client
			}
			channels := make([]string, config.channels)
			expected := make([][]string, config.channels)
			for i := range channels {
				channels[i] = fmt.Sprintf("gateway-%s-channel-%02d", config.name, i)
				require.NoError(t, suite.PostChannel(ctx, cluster.MustNode(1).APIAddr(), map[string]any{"channel_id": channels[i], "channel_type": 2, "subscribers": uids}))
				no := fmt.Sprintf("%s-warm-%d", config.name, i)
				got, err := suite.PostMessageSendEventually(ctx, cluster.MustNode(1).APIAddr(), map[string]any{"from_uid": uids[0], "channel_id": channels[i], "channel_type": 2, "client_msg_no": no, "payload": base64.StdEncoding.EncodeToString([]byte(no))})
				require.NoError(t, err)
				require.Equal(t, uint8(frame.ReasonSuccess), got.Reason)
				expected[i] = append(expected[i], no)
			}
			for phase := 0; phase < 2; phase++ {
				userBanned := make([]bool, len(uids))
				channelBanned := make([]bool, len(channels))
				if phase == 1 {
					for i, uid := range uids {
						userBanned[i] = i%2 == 1 || config.channels == 1 && config.users == 1
						if userBanned[i] {
							_, err := suite.SetUserSendBan(ctx, cluster.MustNode(2).APIAddr(), uid, 1)
							require.NoError(t, err)
						}
					}
					for i, id := range channels {
						channelBanned[i] = len(channels) > 1 && i%2 == 1
						if channelBanned[i] {
							_, err := suite.SetChannelSendBan(ctx, cluster.MustNode(3).APIAddr(), id, 2, 1)
							require.NoError(t, err)
						}
					}
				}
				before := metrics()
				results := make(chan error, len(clients))
				for userIndex, client := range clients {
					go func(userIndex int, client *suite.WKProtoClient) {
						for i := 0; i < config.perUser; i++ {
							channelIndex := (userIndex + i) % len(channels)
							no := fmt.Sprintf("%s-%d-%d-%d", config.name, phase, userIndex, i)
							if err := client.SendFrame(&frame.SendPacket{ChannelID: channels[channelIndex], ChannelType: 2, ClientSeq: uint64(phase*config.perUser + i + 1), ClientMsgNo: no, Payload: []byte(no)}); err != nil {
								results <- err
								return
							}
						}
						results <- nil
					}(userIndex, client)
				}
				for range clients {
					require.NoError(t, <-results)
				}
				allowed, denied := 0, 0
				for userIndex, client := range clients {
					seen := map[string]bool{}
					want := map[string]frame.ReasonCode{}
					channelByNo := map[string]int{}
					for i := 0; i < config.perUser; i++ {
						channelIndex := (userIndex + i) % len(channels)
						no := fmt.Sprintf("%s-%d-%d-%d", config.name, phase, userIndex, i)
						want[no] = frame.ReasonSuccess
						channelByNo[no] = channelIndex
						if userBanned[userIndex] || channelBanned[channelIndex] {
							want[no] = frame.ReasonSendBan
						}
					}
					for i := 0; i < config.perUser; i++ {
						ack, err := client.ReadSendAck()
						require.NoError(t, err)
						require.Contains(t, want, ack.ClientMsgNo)
						require.False(t, seen[ack.ClientMsgNo])
						seen[ack.ClientMsgNo] = true
						require.Equal(t, want[ack.ClientMsgNo], ack.ReasonCode)
						if ack.ReasonCode == frame.ReasonSuccess {
							allowed++
							require.Positive(t, ack.MessageID)
							index := channelByNo[ack.ClientMsgNo]
							expected[index] = append(expected[index], ack.ClientMsgNo)
						} else {
							denied++
							require.Zero(t, ack.MessageID)
							require.Zero(t, ack.MessageSeq)
						}
					}
				}
				delta := metrics()
				for key := range delta {
					delta[key] -= before[key]
				}
				total := float64(config.users * config.perUser)
				require.Equal(t, total, delta["messages"])
				require.Equal(t, total, delta["gateway_records"])
				require.Positive(t, delta["gateway_batches"])
				require.Less(t, delta["gateway_batches"], total, "must observe actual multi-record gateway batches")
				require.Positive(t, delta["facts"])
				require.LessOrEqual(t, delta["facts"], delta["facts_before"])
				observations = append(observations, map[string]any{"case": config.name, "phase": phase, "users": config.users, "channels": config.channels, "allowed": allowed, "denied": denied, "counters": delta})
			}
			for i, id := range channels {
				var history struct {
					HasMore bool `json:"has_more"`
					Items   []struct {
						ClientMsgNo string `json:"client_msg_no"`
					} `json:"items"`
				}
				endpoint := "http://" + cluster.MustNode(1).Spec.ManagerAddr + "/manager/messages?channel_id=" + url.QueryEscape(id) + "&channel_type=2&limit=100"
				require.Eventually(t, func() bool {
					_, err := suite.GetJSON(ctx, endpoint, &history)
					return err == nil && len(history.Items) >= len(expected[i])
				}, 10*time.Second, 100*time.Millisecond)
				require.False(t, history.HasMore)
				var seen []string
				for _, item := range history.Items {
					seen = append(seen, item.ClientMsgNo)
				}
				require.ElementsMatch(t, expected[i], seen)
				observations = append(observations, map[string]any{"case": config.name, "channel": id, "exact_history": seen})
			}
		})
	}
}

// Rejected requests must produce neither a receiver packet nor committed history.
// Positive controls bracket bans and quorum loss on the same live receiver.
func TestRejectedSendHasNoDelivery(t *testing.T) {
	started := time.Now().UTC()
	var evidence []map[string]any
	defer func() {
		path := os.Getenv("WK_E2E_SEND_BAN_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-send-ban-report.json")
		}
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "started_at": started, "finished_at": time.Now().UTC(), "source_revision": os.Getenv("WK_E2E_SOURCE_REVISION"), "nodes": 3, "hash_slots": 256, "slot_replicas": 3, "permission_cache_ttl": "1h", "observations": evidence}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path+".delivery.json", append(raw, '\n'), 0644))
	}()
	opts := []suite.Option{suite.WithManagerHTTP()}
	for i := uint64(1); i <= 3; i++ {
		opts = append(opts, suite.WithNodeConfigOverrides(i, map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_MESSAGE_PERMISSION_CACHE_TTL": "1h"}))
	}
	cluster := suite.New(t).StartThreeNodeCluster(opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx))
	_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	const sender, receiver, room = "delivery-ban-sender", "delivery-ban-receiver", "delivery-ban-room"
	person, err := channelid.NormalizePersonChannel(sender, receiver)
	require.NoError(t, err)
	hashSlot := uint16(crc32.ChecksumIEEE([]byte(sender)) % 256)
	var ownerID uint64
	for _, slot := range cluster.ManagerClient(t, 1).MustSlots(t) {
		if slot.HashSlots != nil {
			for _, h := range slot.HashSlots.Items {
				if h == hashSlot {
					ownerID = slot.Runtime.LeaderID
				}
			}
		}
	}
	require.NotZero(t, ownerID)
	owner := cluster.MustNode(ownerID)
	require.NoError(t, suite.PostChannel(ctx, owner.APIAddr(), map[string]any{"channel_id": room, "channel_type": 2, "subscribers": []string{sender, receiver}}))
	recv, err := suite.NewWKProtoClientWithTimeout(2 * time.Second)
	require.NoError(t, err)
	defer recv.Close()
	require.NoError(t, recv.Connect(owner.GatewayAddr(), receiver, "delivery-observer"))
	sendClient, err := suite.NewWKProtoClientWithTimeout(5 * time.Second)
	require.NoError(t, err)
	defer sendClient.Close()
	require.NoError(t, sendClient.Connect(owner.GatewayAddr(), sender, "delivery-sender"))
	expected := map[uint8][]string{1: {}, 2: {}}
	serial := 0
	send := func(nodeID uint64, label string, kind uint8, flags frame.Framer, protocol bool, want frame.ReasonCode) {
		t.Helper()
		serial++
		target := room
		if kind == 1 {
			target = receiver
		}
		at := time.Now().UTC()
		if protocol {
			require.NoError(t, sendClient.SendFrame(&frame.SendPacket{Framer: flags, ChannelID: target, ChannelType: kind, ClientSeq: uint64(serial), ClientMsgNo: label, Payload: []byte(label)}))
			ack, err := sendClient.ReadSendAck()
			require.NoError(t, err)
			require.Equal(t, want, ack.ReasonCode, label)
			if want != frame.ReasonSuccess {
				require.Zero(t, ack.MessageID)
				require.Zero(t, ack.MessageSeq)
			}
		} else {
			body := map[string]any{"from_uid": sender, "channel_id": target, "channel_type": kind, "client_msg_no": label, "payload": base64.StdEncoding.EncodeToString([]byte(label))}
			if flags.NoPersist {
				body["no_persist"] = 1
			}
			if flags.SyncOnce {
				body["sync_once"] = 1
			}
			got, err := suite.PostMessageSendEventually(ctx, cluster.MustNode(nodeID).APIAddr(), body)
			require.NoError(t, err)
			require.Equal(t, uint8(want), got.Reason, label)
			if want != frame.ReasonSuccess {
				require.Zero(t, got.MessageID)
				require.Zero(t, got.MessageSeq)
			}
		}
		if want == frame.ReasonSuccess {
			packet, err := recv.ReadRecv()
			require.NoError(t, err, label)
			require.Equal(t, label, string(packet.Payload), "a rejected packet must not leak ahead of the positive control")
			require.Equal(t, sender, packet.FromUID)
			require.NoError(t, recv.RecvAck(packet.MessageID, packet.MessageSeq))
			expected[kind] = append(expected[kind], label)
		}
		evidence = append(evidence, map[string]any{"label": label, "kind": kind, "wkproto": protocol, "reason": want, "started_at": at, "finished_at": time.Now().UTC()})
	}
	quiet := func(label string) {
		t.Helper()
		packet, err := recv.ReadRecv()
		require.ErrorIs(t, err, context.DeadlineExceeded, label)
		require.Nil(t, packet)
		evidence = append(evidence, map[string]any{"label": label, "no_recv_window_ms": 2000, "at": time.Now().UTC()})
	}
	controls := func(prefix string) {
		for _, kind := range []uint8{1, 2} {
			send(ownerID, fmt.Sprintf("%s-http-%d", prefix, kind), kind, frame.Framer{}, false, frame.ReasonSuccess)
			send(ownerID, fmt.Sprintf("%s-proto-%d", prefix, kind), kind, frame.Framer{}, true, frame.ReasonSuccess)
		}
	}
	controls("warm")
	for _, scope := range []string{"user", "channel"} {
		set := func(value int) {
			t.Helper()
			if scope == "user" {
				_, err := suite.SetUserSendBan(ctx, owner.APIAddr(), sender, value)
				require.NoError(t, err)
			} else {
				_, err := suite.SetChannelSendBan(ctx, owner.APIAddr(), room, 2, value)
				require.NoError(t, err)
				_, err = suite.SetChannelSendBan(ctx, owner.APIAddr(), person, 1, value)
				require.NoError(t, err)
			}
		}
		set(1)
		for _, kind := range []uint8{1, 2} {
			for flagIndex, flags := range []frame.Framer{{}, {NoPersist: true}, {SyncOnce: true}} {
				for nodeID := uint64(1); nodeID <= 3; nodeID++ {
					send(nodeID, fmt.Sprintf("%s-http-%d-%d-%d", scope, kind, flagIndex, nodeID), kind, flags, false, frame.ReasonSendBan)
				}
				send(ownerID, fmt.Sprintf("%s-proto-%d-%d", scope, kind, flagIndex), kind, flags, true, frame.ReasonSendBan)
			}
		}
		quiet(scope + "-no-delivery")
		set(0)
		controls(scope + "-recovered")
	}
	// Keep the receiver's ingress alive while removing the UID Slot quorum.
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		if nodeID != ownerID {
			require.NoError(t, cluster.MustNode(nodeID).Stop())
		}
	}
	failCtx, failCancel := context.WithTimeout(ctx, 12*time.Second)
	got, err := suite.PostMessageSend(failCtx, owner.APIAddr(), map[string]any{"from_uid": sender, "channel_id": room, "channel_type": 2, "client_msg_no": "quorum-no-delivery", "payload": base64.StdEncoding.EncodeToString([]byte("quorum-no-delivery"))})
	failCancel()
	var statusErr *suite.HTTPStatusError
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, http.StatusServiceUnavailable, statusErr.StatusCode)
	require.Zero(t, got.MessageID)
	require.Zero(t, got.MessageSeq)
	quiet("quorum-no-delivery")
	for nodeID := uint64(1); nodeID <= 3; nodeID++ {
		if nodeID != ownerID {
			require.NoError(t, cluster.StartStoppedNode(nodeID))
		}
	}
	require.NoError(t, cluster.WaitHTTPReady(ctx))
	_, err = cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	controls("quorum-recovered")
	quiet("final-no-delivery")
	for _, kind := range []uint8{1, 2} {
		id := room
		if kind == 1 {
			id = receiver
		}
		var history struct {
			More     int `json:"more"`
			Messages []struct {
				ClientMsgNo string `json:"client_msg_no"`
			} `json:"messages"`
		}
		require.Eventually(t, func() bool {
			_, err := suite.PostJSON(ctx, "http://"+owner.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": sender, "channel_id": id, "channel_type": kind, "limit": 100}, &history)
			return err == nil && len(history.Messages) >= len(expected[kind])
		}, 10*time.Second, 100*time.Millisecond)
		require.Zero(t, history.More)
		var seen []string
		for _, message := range history.Messages {
			seen = append(seen, message.ClientMsgNo)
		}
		require.ElementsMatch(t, expected[kind], seen)
		evidence = append(evidence, map[string]any{"kind": kind, "exact_history": seen})
	}
}

// This opt-in proof measures permission work separately from successful fanout.
// Rejections must use a fixed fact plan and never enter recipient delivery.
func TestHundredKGroupSendBan(t *testing.T) {
	if os.Getenv("WK_E2E_SEND_BAN_100K") != "1" {
		t.Skip("set WK_E2E_SEND_BAN_100K=1 for the 100,000-member policy proof")
	}
	started := time.Now().UTC()
	var evidence []map[string]any
	t.Cleanup(func() {
		path := os.Getenv("WK_E2E_SEND_BAN_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-send-ban-report.json")
		}
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "members": 100000, "hash_slots": 256, "nodes": 1, "initial_slots": 12, "permission_cache_ttl": "1h", "started_at": started, "finished_at": time.Now().UTC(), "source_revision": os.Getenv("WK_E2E_SOURCE_REVISION"), "observations": evidence, "performance_qualified": false}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path+".100k.json", append(raw, '\n'), 0644))
	})
	node := suite.New(t).StartSingleNodeCluster(suite.WithNodeConfigOverrides(1, map[string]string{
		"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_MESSAGE_PERMISSION_CACHE_TTL": "1h",
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	const room, sender = "send-ban-100k-room", "send-ban-100k-sender"
	members := make([]string, 100000)
	members[0] = sender
	for i := 1; i < len(members); i++ {
		members[i] = fmt.Sprintf("send-ban-100k-%06d", i)
	}
	setupStarted := time.Now()
	require.NoError(t, suite.PostChannel(ctx, node.APIAddr(), map[string]any{"channel_id": room, "channel_type": 2, "subscribers": members}))
	evidence = append(evidence, map[string]any{"label": "100k-setup", "requested_members": len(members), "elapsed_us": time.Since(setupStarted).Microseconds()})
	metrics := func() []suite.MetricSample {
		t.Helper()
		samples, err := suite.FetchMetricSamples(ctx, node.APIAddr())
		require.NoError(t, err)
		return samples
	}
	send := func(label string, want frame.ReasonCode) suite.MessageSendResponse {
		t.Helper()
		before := metrics()
		at := time.Now().UTC()
		out, err := suite.PostMessageSendEventually(ctx, node.APIAddr(), map[string]any{"from_uid": sender, "channel_id": room, "channel_type": 2, "client_msg_no": label, "payload": base64.StdEncoding.EncodeToString([]byte(label))})
		require.NoError(t, err, node.DumpDiagnostics())
		require.Equal(t, uint8(want), out.Reason)
		after := metrics()
		facts := suite.SumMetricSamples(after, "wukongim_message_permission_counts_total", map[string]string{"kind": "facts"}) - suite.SumMetricSamples(before, "wukongim_message_permission_counts_total", map[string]string{"kind": "facts"})
		fanout := suite.SumMetricSamples(after, "wukongim_delivery_recipient_worker_process_recipients_sum", nil) - suite.SumMetricSamples(before, "wukongim_delivery_recipient_worker_process_recipients_sum", nil)
		if want == frame.ReasonSendBan {
			require.Zero(t, out.MessageID)
			require.Zero(t, out.MessageSeq)
			require.Equal(t, float64(6), facts, "permission plan must be independent of group cardinality")
			require.Zero(t, fanout, "rejected SEND must not walk recipients")
		}
		evidence = append(evidence, map[string]any{"label": label, "reason": out.Reason, "message_seq": out.MessageSeq, "started_at": at, "elapsed_us": time.Since(at).Microseconds(), "permission_facts": facts, "recipient_rows": fanout})
		return out
	}
	first := send("100k-before-ban", frame.ReasonSuccess)
	suite.RequireMetricAtLeastEventually(t, *node, "wukongim_delivery_recipient_worker_process_recipients_sum", map[string]string{"result": "ok"}, 100000)
	evidence = append(evidence, map[string]any{"label": "100k-fanout-positive-control", "processed_recipient_rows": suite.SumMetricSamples(metrics(), "wukongim_delivery_recipient_worker_process_recipients_sum", map[string]string{"result": "ok"})})
	_, err := suite.SetUserSendBan(ctx, node.APIAddr(), sender, 1)
	require.NoError(t, err)
	send("100k-user-rejected", frame.ReasonSendBan)
	_, err = suite.SetChannelSendBan(ctx, node.APIAddr(), room, 2, 1)
	require.NoError(t, err)
	_, err = suite.SetUserSendBan(ctx, node.APIAddr(), sender, 0)
	require.NoError(t, err)
	send("100k-channel-rejected", frame.ReasonSendBan)
	_, err = suite.SetChannelSendBan(ctx, node.APIAddr(), room, 2, 0)
	require.NoError(t, err)
	last := send("100k-after-unban", frame.ReasonSuccess)
	require.Greater(t, last.MessageSeq, first.MessageSeq)
	var history struct {
		More     int `json:"more"`
		Messages []struct {
			ClientMsgNo string `json:"client_msg_no"`
		} `json:"messages"`
	}
	require.Eventually(t, func() bool {
		_, err := suite.PostJSON(ctx, "http://"+node.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": sender, "channel_id": room, "channel_type": 2, "limit": 100}, &history)
		return err == nil && len(history.Messages) >= 2
	}, 15*time.Second, 100*time.Millisecond)
	require.Zero(t, history.More)
	var numbers []string
	for _, message := range history.Messages {
		numbers = append(numbers, message.ClientMsgNo)
	}
	require.ElementsMatch(t, []string{"100k-before-ban", "100k-after-unban"}, numbers)
	evidence = append(evidence, map[string]any{"label": "100k-complete-history", "more": history.More, "client_msg_numbers": numbers})
}

func TestUserAndChannelSendBan(t *testing.T) {
	var observations []observation
	var rpcEvidence []map[string]any
	var combinationEvidence []map[string]any
	startedAt := time.Now().UTC()
	t.Cleanup(func() {
		path := os.Getenv("WK_E2E_SEND_BAN_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-send-ban-report.json")
		}
		report, err := json.MarshalIndent(map[string]any{
			"passed": !t.Failed(), "hash_slots": 256, "observations": observations,
			"started_at": startedAt, "finished_at": time.Now().UTC(),
			"source_revision":          os.Getenv("WK_E2E_SOURCE_REVISION"),
			"working_tree_fingerprint": os.Getenv("WK_E2E_SOURCE_FINGERPRINT"),
			"go_version":               runtime.Version(), "platform": runtime.GOOS + "/" + runtime.GOARCH,
			"permission_cache_ttl": "1h", "initial_slots": 12, "token_auth": false, "rpc_evidence": rpcEvidence,
			"scenario_seed": "fixed-ban-identities-v1", "performance_qualified": false,
			"independent_ban_combinations": combinationEvidence,
		}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, append(report, '\n'), 0644))
		t.Logf("send-ban report: %s", path)
	})
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d-node-cluster", count), func(t *testing.T) {
			opts := []suite.Option{suite.WithManagerHTTP()}
			for i := 1; i <= count; i++ {
				opts = append(opts, suite.WithNodeConfigOverrides(uint64(i), map[string]string{
					"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_GATEWAY_TOKEN_AUTH_ON": "false",
					"WK_MESSAGE_PERMISSION_CACHE_TTL": "1h",
				}))
			}
			s := suite.New(t)
			var nodes []*suite.StartedNode
			var cluster *suite.StartedCluster
			if count == 1 {
				nodes = []*suite.StartedNode{s.StartSingleNodeCluster(opts...)}
			} else {
				c := s.StartThreeNodeCluster(opts...)
				cluster = c
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				require.NoError(t, c.WaitClusterReady(ctx), c.DumpDiagnostics())
				_, err := c.WaitSlotLeadersStable(ctx, time.Second)
				require.NoError(t, err, c.DumpDiagnostics())
				nodes = []*suite.StartedNode{c.MustNode(1), c.MustNode(2), c.MustNode(3)}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
			defer cancel()
			first, last := nodes[0], nodes[len(nodes)-1]
			const a, b, c, room = "ban-a", "ban-b", "ban-c", "ban-room"
			canonical, err := channelid.NormalizePersonChannel(a, b)
			require.NoError(t, err)
			require.NoError(t, suite.PostChannel(ctx, first.APIAddr(), map[string]any{
				"channel_id": room, "channel_type": 2, "subscribers": []string{a, b},
			}))
			serial := 0
			send := func(n *suite.StartedNode, label, from, target string, kind uint8, extra map[string]any, want frame.ReasonCode) suite.MessageSendResponse {
				t.Helper()
				serial++
				body := map[string]any{"from_uid": from, "channel_id": target, "channel_type": kind,
					"client_msg_no": fmt.Sprintf("ban-%d-%d", count, serial), "payload": base64.StdEncoding.EncodeToString([]byte(label))}
				for k, v := range extra {
					body[k] = v
				}
				started := time.Now().UTC()
				got, err := suite.PostMessageSendEventually(ctx, n.APIAddr(), body)
				require.NoError(t, err, n.DumpDiagnostics())
				require.Equal(t, uint8(want), got.Reason, label)
				if want != frame.ReasonSuccess {
					require.Zero(t, got.MessageID)
					require.Zero(t, got.MessageSeq)
				}
				observations = append(observations, observation{Topology: count, Label: label, Reason: got.Reason, Seq: got.MessageSeq, StartedAt: started, ElapsedMicros: time.Since(started).Microseconds()})
				return got
			}
			set := func(path string, body map[string]any) policy {
				t.Helper()
				var out struct {
					Data policy `json:"data"`
				}
				_, err := suite.PostJSON(ctx, "http://"+first.APIAddr()+path, body, &out)
				require.NoError(t, err, first.DumpDiagnostics())
				require.NotEmpty(t, out.Data.Version)
				return out.Data
			}
			user := func(value int) policy { return set("/user/send_ban", map[string]any{"uid": a, "send_ban": value}) }
			channel := func(id string, kind, value int) policy {
				return set("/channel/send_ban", map[string]any{"channel_id": id, "channel_type": kind, "send_ban": value})
			}
			before := send(last, "warm-group", a, room, 2, nil, frame.ReasonSuccess)
			personBefore := send(last, "warm-person", a, b, 1, nil, frame.ReasonSuccess)
			// Cover every additional declared source type and two devices that
			// stay connected across the policy transitions. Each channel has
			// positive controls on both sides and an exact history set.
			type sourceTarget struct {
				id        string
				kind      uint8
				committed map[string]bool
			}
			var targets []sourceTarget
			for kind := uint8(3); kind <= 12; kind++ {
				id := fmt.Sprintf("ban-source-%d", kind)
				if kind == frame.ChannelTypeAgent {
					id = channelid.EncodeAgentChannel(a, "ban-agent")
				}
				if kind == frame.ChannelTypeVisitors {
					id = a
				}
				if kind != frame.ChannelTypeAgent {
					require.NoError(t, suite.PostChannel(ctx, first.APIAddr(), map[string]any{"channel_id": id, "channel_type": kind, "subscribers": []string{a, b}}))
				}
				targets = append(targets, sourceTarget{id: id, kind: kind, committed: make(map[string]bool)})
			}
			var devices []*suite.WKProtoClient
			for i := 0; i < 2; i++ {
				device, e := suite.NewWKProtoClient()
				require.NoError(t, e)
				require.NoError(t, device.Connect(nodes[i%len(nodes)].GatewayAddr(), a, fmt.Sprintf("ban-existing-device-%d", i)))
				defer device.Close()
				devices = append(devices, device)
			}
			deviceSend := func(device *suite.WKProtoClient, target sourceTarget, label string, want frame.ReasonCode) {
				t.Helper()
				serial++
				no := fmt.Sprintf("ban-extra-%d-%d", count, serial)
				started := time.Now().UTC()
				require.NoError(t, device.SendFrame(&frame.SendPacket{ChannelID: target.id, ChannelType: target.kind, ClientSeq: uint64(serial), ClientMsgNo: no, Payload: []byte(label)}))
				ack, e := device.ReadSendAck()
				require.NoError(t, e)
				require.Equal(t, want, ack.ReasonCode, label)
				if want == frame.ReasonSuccess {
					target.committed[no] = true
					require.NotZero(t, ack.MessageID)
					require.NotZero(t, ack.MessageSeq)
				} else {
					require.Zero(t, ack.MessageID)
					require.Zero(t, ack.MessageSeq)
				}
				observations = append(observations, observation{Topology: count, Label: label, Reason: uint8(ack.ReasonCode), Seq: ack.MessageSeq, StartedAt: started, ElapsedMicros: time.Since(started).Microseconds()})
			}
			for _, target := range targets {
				send(last, fmt.Sprintf("source-%d-http-warm", target.kind), a, target.id, target.kind, nil, frame.ReasonSuccess)
				target.committed[fmt.Sprintf("ban-%d-%d", count, serial)] = true
				for i, device := range devices {
					deviceSend(device, target, fmt.Sprintf("source-%d-device-%d-warm", target.kind, i), frame.ReasonSuccess)
				}
			}
			ua := user(1)
			require.Equal(t, 1, ua.Ban)
			require.Equal(t, ua, user(1), "same value must preserve version")
			for _, n := range nodes {
				send(n, "user-group", a, room, 2, nil, frame.ReasonSendBan)
				send(n, "user-person", a, b, 1, nil, frame.ReasonSendBan)
				send(n, "user-transient", a, room, 2, map[string]any{"no_persist": 1}, frame.ReasonSendBan)
				send(n, "user-cmd", a, room, 2, map[string]any{"sync_once": 1}, frame.ReasonSendBan)
				send(n, "user-device", a, room, 2, map[string]any{"device_id": "____device"}, frame.ReasonSendBan)
				send(n, "user-request-scoped", a, "", 0, map[string]any{"subscribers": []string{b}, "sync_once": 1}, frame.ReasonSendBan)
			}
			for _, target := range targets {
				for _, node := range nodes {
					send(node, fmt.Sprintf("source-%d-user-ban", target.kind), a, target.id, target.kind, nil, frame.ReasonSendBan)
				}
				for i, device := range devices {
					deviceSend(device, target, fmt.Sprintf("source-%d-device-%d-banned", target.kind, i), frame.ReasonSendBan)
				}
			}
			// A banned sender may remain connected, receive and read history.
			client, err := suite.NewWKProtoClient()
			require.NoError(t, err)
			require.NoError(t, client.Connect(last.GatewayAddr(), a, "ban-device"))
			defer client.Close()
			require.NoError(t, client.SendFrame(&frame.SendPacket{ChannelID: room, ChannelType: 2, ClientSeq: 1, ClientMsgNo: "ban-gateway", Payload: []byte("blocked")}))
			ack, err := client.ReadSendAck()
			require.NoError(t, err)
			require.Equal(t, frame.ReasonSendBan, ack.ReasonCode)
			send(first, "other-user-receive", b, room, 2, nil, frame.ReasonSuccess)
			received, err := client.ReadRecv()
			require.NoError(t, err)
			require.Equal(t, []byte("other-user-receive"), received.Payload)
			require.NoError(t, client.RecvAck(received.MessageID, received.MessageSeq))
			// Token preparation must not clear policy-only user rows.
			_, err = suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/token", map[string]any{"uid": a, "token": "test-token", "device_flag": 1, "device_level": 0}, nil)
			require.NoError(t, err)
			send(last, "after-token-update", a, room, 2, nil, frame.ReasonSendBan)
			_, err = suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/send_ban", map[string]any{"uid": a, "send_ban": 0, "expected_version": "0"}, nil)
			var statusErr *suite.HTTPStatusError
			require.ErrorAs(t, err, &statusErr)
			require.Equal(t, http.StatusConflict, statusErr.StatusCode)
			user(0)
			require.NoError(t, client.SendFrame(&frame.SendPacket{ChannelID: room, ChannelType: 2, ClientSeq: 2, ClientMsgNo: "ban-gateway-unbanned", Payload: []byte("unbanned without reconnect")}))
			ack, err = client.ReadSendAck()
			require.NoError(t, err)
			require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
			after := send(last, "unban-immediate", a, room, 2, nil, frame.ReasonSuccess)
			require.Equal(t, before.MessageSeq+3, after.MessageSeq, "rejected sends must never append")
			for _, target := range targets {
				send(last, fmt.Sprintf("source-%d-http-unban", target.kind), a, target.id, target.kind, nil, frame.ReasonSuccess)
				target.committed[fmt.Sprintf("ban-%d-%d", count, serial)] = true
				for i, device := range devices {
					deviceSend(device, target, fmt.Sprintf("source-%d-device-%d-unban", target.kind, i), frame.ReasonSuccess)
				}
				var history struct {
					HasMore  bool `json:"has_more"`
					Messages []struct {
						ClientMsgNo string `json:"client_msg_no"`
					} `json:"items"`
				}
				require.Eventually(t, func() bool {
					_, e := suite.GetJSON(ctx, "http://"+last.Spec.ManagerAddr+"/manager/messages?channel_id="+url.QueryEscape(target.id)+fmt.Sprintf("&channel_type=%d&limit=100", target.kind), &history)
					return e == nil && len(history.Messages) >= len(target.committed)
				}, 10*time.Second, 100*time.Millisecond, "source type %d history", target.kind)
				require.False(t, history.HasMore, "complete history required")
				require.Len(t, history.Messages, len(target.committed), "no rejected SEND may persist")
				seen := make(map[string]bool)
				for _, m := range history.Messages {
					require.True(t, target.committed[m.ClientMsgNo], "unexpected or rejected history item %s", m.ClientMsgNo)
					require.False(t, seen[m.ClientMsgNo], "duplicate history item")
					seen[m.ClientMsgNo] = true
				}
				observations = append(observations, observation{Topology: count, Label: fmt.Sprintf("source-%d-exact-history-%d", target.kind, len(seen)), StartedAt: time.Now().UTC()})
			}
			channel(canonical, 1, 1)
			send(last, "channel-forward", a, b, 1, nil, frame.ReasonSendBan)
			send(last, "channel-reverse", b, a, 1, nil, frame.ReasonSendBan)
			send(last, "channel-independent", a, c, 1, nil, frame.ReasonSuccess)
			user(1)
			channel(canonical, 1, 0)
			send(last, "channel-unban-keeps-user-ban", a, b, 1, nil, frame.ReasonSendBan)
			user(0)
			personAfter := send(last, "both-unbanned", a, b, 1, nil, frame.ReasonSuccess)
			require.Equal(t, personBefore.MessageSeq+1, personAfter.MessageSeq, "person restrictions must not append")
			runIndependentBanCombinations(t, ctx, nodes, &combinationEvidence)
			channel(room, 2, 1)
			_, err = suite.PostJSON(ctx, "http://"+first.APIAddr()+"/channel/info", map[string]any{"channel_id": room, "channel_type": 2, "large": 1}, nil)
			require.NoError(t, err)
			send(last, "metadata-keeps-ban", a, room, 2, nil, frame.ReasonSendBan)
			send(last, "channel-system", "____system", room, 2, nil, frame.ReasonSendBan)
			_, err = suite.PostJSON(ctx, "http://"+first.APIAddr()+"/channel/info", map[string]any{"channel_id": room, "channel_type": 2, "send_ban": 0}, nil)
			require.NoError(t, err)
			send(last, "channel-unban-immediate", a, room, 2, nil, frame.ReasonSuccess)
			// A conversation may be restricted before its first SEND.
			fresh, err := channelid.NormalizePersonChannel(a, "ban-new-peer")
			require.NoError(t, err)
			channel(fresh, 1, 1)
			send(last, "preban-person", a, "ban-new-peer", 1, nil, frame.ReasonSendBan)
			for _, bad := range []map[string]any{{"uid": a}, {"uid": a, "send_ban": 2}, {"uid": "", "send_ban": 1}} {
				_, err = suite.PostJSON(ctx, "http://"+first.APIAddr()+"/user/send_ban", bad, nil)
				require.ErrorAs(t, err, &statusErr)
				require.Equal(t, http.StatusBadRequest, statusErr.StatusCode)
			}
			// Durable bans survive process restart; the new read path must also
			// resume with the registered format instead of accepting stale facts.
			user(1)
			channel(room, 2, 1)
			require.NoError(t, last.Restart(last.Process.BinaryPath))
			require.NoError(t, suite.WaitHTTPReady(ctx, last.APIAddr(), "/readyz"))
			send(last, "user-ban-after-restart", a, room, 2, nil, frame.ReasonSendBan)
			user(0)
			send(last, "channel-ban-after-restart", a, room, 2, nil, frame.ReasonSendBan)
			channel(room, 2, 0)
			send(last, "unban-after-restart", a, room, 2, nil, frame.ReasonSuccess)
			metrics, err := suite.FetchMetricSamples(ctx, last.APIAddr())
			require.NoError(t, err)
			require.Positive(t, suite.SumMetricSamples(metrics, "wukongim_message_permission_counts_total", map[string]string{"kind": "messages"}))
			require.Positive(t, suite.SumMetricSamples(metrics, "wukongim_message_send_ban_rejections_total", map[string]string{"scope": "user"}))

			var state struct {
				Data policy `json:"data"`
			}
			_, err = suite.GetJSON(ctx, "http://"+last.APIAddr()+"/user/send_ban?uid="+a, &state)
			require.NoError(t, err)
			require.Zero(t, state.Data.Ban)
			require.NotEmpty(t, state.Data.Version)
			if cluster != nil {

				_, err = cluster.WaitSlotLeadersStable(ctx, time.Second)
				require.NoError(t, err)
				placements := make(map[uint16]suite.SlotDTO)
				for _, slot := range cluster.ManagerClient(t, 1).MustSlots(t) {
					if slot.HashSlots != nil {
						for _, h := range slot.HashSlots.Items {
							placements[h] = slot
						}
					}
				}
				systemSlot := placements[uint16(crc32.ChecksumIEEE([]byte("____system"))%256)]
				require.NotZero(t, systemSlot.Runtime.LeaderID)
				var pairedChannel string
				var pairedSlot suite.SlotDTO
				for i := 0; i < 10000; i++ {
					id := fmt.Sprintf("ban-rpc-pair-%d", i)
					slot := placements[uint16(crc32.ChecksumIEEE([]byte(id))%256)]
					if slot.SlotID != systemSlot.SlotID && slot.Runtime.LeaderID == systemSlot.Runtime.LeaderID {
						pairedChannel, pairedSlot = id, slot
						break
					}
				}
				require.NotEmpty(t, pairedChannel, "need two distinct Slots at the same leader")
				require.NoError(t, suite.PostChannel(ctx, first.APIAddr(), map[string]any{"channel_id": pairedChannel, "channel_type": 2, "send_ban": 1}))
				owner := cluster.MustNode(systemSlot.Runtime.LeaderID)
				var ingress *suite.StartedNode
				for _, n := range nodes {
					if n != owner {
						ingress = n
						break
					}
				}
				for _, source := range []*suite.StartedNode{ingress, owner} {
					beforeRPC, e := suite.FetchMetricSamples(ctx, source.APIAddr())
					require.NoError(t, e)
					beforeOwner, e := suite.FetchMetricSamples(ctx, owner.APIAddr())
					require.NoError(t, e)
					send(source, "two-slots-one-envelope", "____system", pairedChannel, 2, nil, frame.ReasonSendBan)
					afterRPC, e := suite.FetchMetricSamples(ctx, source.APIAddr())
					require.NoError(t, e)
					afterOwner, e := suite.FetchMetricSamples(ctx, owner.APIAddr())
					require.NoError(t, e)
					countMetric := func(samples []suite.MetricSample, kind string) float64 {
						return suite.SumMetricSamples(samples, "wukongim_message_permission_counts_total", map[string]string{"kind": kind})
					}
					rpcDelta := countMetric(afterRPC, "node_envelopes") - countMetric(beforeRPC, "node_envelopes")
					slotDelta := countMetric(afterOwner, "slot_groups") - countMetric(beforeOwner, "slot_groups")
					wantRPC := float64(1)
					if source == owner {
						wantRPC = 0
					}
					require.Equal(t, wantRPC, rpcDelta, "one remote node envelope or no loopback")
					require.EqualValues(t, 2, slotDelta, "each distinct Slot retains its own fresh read")
					rpcEvidence = append(rpcEvidence, map[string]any{"ingress": source.Spec.ID, "leader": owner.Spec.ID, "user_slot": systemSlot.SlotID, "channel_slot": pairedSlot.SlotID, "node_envelopes": rpcDelta, "slot_groups": slotDelta})
				}
				// Identify the UID's real Slot leader from public topology before stopping it.
				user(1)
				hashSlot := uint16(crc32.ChecksumIEEE([]byte(a)) % 256)
				var leader uint64
				for _, slot := range cluster.ManagerClient(t, 1).MustSlots(t) {
					if slot.HashSlots == nil {
						continue
					}
					for _, h := range slot.HashSlots.Items {
						if h == hashSlot {
							leader = slot.Runtime.LeaderID
						}
					}
				}
				require.NotZero(t, leader)
				stopped := cluster.MustNode(leader)
				var survivor *suite.StartedNode
				for _, n := range nodes {
					if n != stopped {
						if survivor == nil {
							survivor = n
						}
					}
				}
				require.NoError(t, stopped.Stop())
				// Only authority readiness is polled. Once the new leader proves the ban,
				// the SEND itself gets one attempt and must return the deny reason.
				require.Eventually(t, func() bool {
					probeCtx, probeCancel := context.WithTimeout(ctx, 2*time.Second)
					defer probeCancel()
					var response struct {
						Data policy `json:"data"`
					}
					_, e := suite.GetJSON(probeCtx, "http://"+survivor.APIAddr()+"/user/send_ban?uid="+a, &response)
					return e == nil && response.Data.Ban == 1
				}, 30*time.Second, 200*time.Millisecond)
				send(survivor, "user-ban-after-slot-leader-loss", a, room, 2, nil, frame.ReasonSendBan)
				_, err = suite.PostJSON(ctx, "http://"+survivor.APIAddr()+"/user/send_ban", map[string]any{"uid": a, "send_ban": 0}, nil)
				require.NoError(t, err)
				require.NoError(t, cluster.StartStoppedNode(leader))
				require.NoError(t, suite.WaitHTTPReady(ctx, stopped.APIAddr(), "/readyz"))
				_, err = cluster.WaitSlotLeadersStable(ctx, time.Second)
				require.NoError(t, err)
				// Isolate the current UID Slot leader to challenge its earlier local allow.
				var isolatedLeader uint64
				for _, slot := range cluster.ManagerClient(t, survivor.Spec.ID).MustSlots(t) {
					if slot.HashSlots != nil {
						for _, h := range slot.HashSlots.Items {
							if h == hashSlot {
								isolatedLeader = slot.Runtime.LeaderID
							}
						}
					}
				}
				require.NotZero(t, isolatedLeader)
				survivor = cluster.MustNode(isolatedLeader)
				beforeLoss := send(survivor, "allow-before-quorum-loss", a, room, 2, nil, frame.ReasonSuccess)
				for _, n := range nodes {
					if n != survivor {
						require.NoError(t, n.Stop())
					}
				}
				failCtx, failCancel := context.WithTimeout(ctx, 12*time.Second)
				var rejected suite.MessageSendResponse
				started := time.Now().UTC()
				_, failErr := suite.PostJSON(failCtx, "http://"+survivor.APIAddr()+"/message/send", map[string]any{
					"from_uid": a, "channel_id": room, "channel_type": 2,
					"client_msg_no": "ban-quorum-lost", "payload": base64.StdEncoding.EncodeToString([]byte("must-not-append")),
				}, &rejected)
				failCancel()
				require.Error(t, failErr)
				require.ErrorAs(t, failErr, &statusErr)
				require.Equal(t, http.StatusServiceUnavailable, statusErr.StatusCode)
				require.Zero(t, rejected.MessageID)
				require.Zero(t, rejected.MessageSeq)
				observations = append(observations, observation{Topology: count, Label: "isolated-user-slot-leader-fails-closed-http-503", StartedAt: started, ElapsedMicros: time.Since(started).Microseconds()})
				for _, n := range nodes {
					if n != survivor {
						require.NoError(t, cluster.StartStoppedNode(n.Spec.ID))
					}
				}
				require.NoError(t, cluster.WaitHTTPReady(ctx))
				_, err = cluster.WaitSlotLeadersStable(ctx, time.Second)
				require.NoError(t, err)
				afterLoss := send(survivor, "allow-after-quorum-recovery", a, room, 2, nil, frame.ReasonSuccess)
				require.Greater(t, afterLoss.MessageSeq, beforeLoss.MessageSeq)
				// Check committed history, with a positive control, instead of inferring
				// absence from sequence density across leadership changes.
				var history struct {
					More     int `json:"more"`
					Messages []struct {
						MessageSeq  uint64 `json:"message_seq"`
						ClientMsgNo string `json:"client_msg_no"`
					} `json:"messages"`
				}
				expectedNo := fmt.Sprintf("ban-%d-%d", count, serial)
				var historyErr error
				require.Eventually(t, func() bool {
					_, historyErr = suite.PostJSON(ctx, "http://"+survivor.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": a, "channel_id": room, "channel_type": 2, "limit": 100}, &history)
					if historyErr != nil {
						return false
					}
					for _, m := range history.Messages {
						if m.ClientMsgNo == expectedNo && m.MessageSeq == afterLoss.MessageSeq {
							return true
						}
					}
					return false
				}, 10*time.Second, 100*time.Millisecond, "positive committed history lookup: want %s seq %d", expectedNo, afterLoss.MessageSeq)
				require.NoError(t, historyErr)
				require.Zero(t, history.More, "absence proof requires the full small-channel history")
				for _, m := range history.Messages {
					require.NotEqual(t, "ban-quorum-lost", m.ClientMsgNo, "quorum-rejected SEND must not persist")
				}

			}

		})
	}
}

// Failure cases: an unban must not clear the other scope, a person ban must
// apply in both directions, and a channel ban must not leak to another target.
// Complete committed histories use exact successful request identities as a
// positive control; rejected requests must never appear in those histories.
func runIndependentBanCombinations(t *testing.T, ctx context.Context, nodes []*suite.StartedNode, evidence *[]map[string]any) {
	t.Helper()
	const a, b, c, group, other = "matrix-a", "matrix-b", "matrix-c", "matrix-group", "matrix-other-group"
	pair, err := channelid.NormalizePersonChannel(a, b)
	require.NoError(t, err)
	otherPair, err := channelid.NormalizePersonChannel(a, c)
	require.NoError(t, err)
	for _, id := range []string{group, other} {
		require.NoError(t, suite.PostChannel(ctx, nodes[0].APIAddr(), map[string]any{"channel_id": id, "channel_type": 2, "subscribers": []string{a, b}}))
	}
	type historyKey struct {
		id   string
		kind uint8
	}
	expected := map[historyKey]map[string]bool{}
	for step, state := range []struct {
		label         string
		user, channel int
	}{
		{"warm-allow", 0, 0}, {"user-only", 1, 0}, {"both", 1, 1},
		{"unban-user-first", 0, 1}, {"unban-channel-last", 0, 0},
		{"both-again", 1, 1}, {"unban-channel-first", 1, 0}, {"unban-user-last", 0, 0},
	} {
		writer := nodes[step%len(nodes)]
		versions := map[string]string{}
		for _, update := range []struct {
			scope, path string
			body        map[string]any
		}{
			{"user", "/user/send_ban", map[string]any{"uid": a, "send_ban": state.user}},
			{"person", "/channel/send_ban", map[string]any{"channel_id": pair, "channel_type": 1, "send_ban": state.channel}},
			{"group", "/channel/send_ban", map[string]any{"channel_id": group, "channel_type": 2, "send_ban": state.channel}},
		} {
			var out struct {
				Data policy `json:"data"`
			}
			_, err := suite.PostJSON(ctx, "http://"+writer.APIAddr()+update.path, update.body, &out)
			require.NoError(t, err)
			require.Equal(t, update.body["send_ban"], out.Data.Ban)
			require.NotEmpty(t, out.Data.Version)
			versions[update.scope] = out.Data.Version
		}
		*evidence = append(*evidence, map[string]any{"nodes": len(nodes), "step": step, "state": state.label, "user_ban": state.user, "channel_ban": state.channel, "writer": step%len(nodes) + 1, "versions": versions})
		for ingress, node := range nodes {
			for routeIndex, route := range []struct {
				label, from, to, actual string
				kind                    uint8
				restricted              bool
			}{
				{"person-forward", a, b, pair, 1, true}, {"person-reverse", b, a, pair, 1, true},
				{"other-person", a, c, otherPair, 1, false}, {"group-sender-a", a, group, group, 2, true},
				{"group-sender-b", b, group, group, 2, true}, {"other-group", a, other, other, 2, false},
			} {
				no := fmt.Sprintf("matrix-%d-%d-%d-%d", len(nodes), step, ingress, routeIndex)
				body := map[string]any{"from_uid": route.from, "channel_id": route.to, "channel_type": route.kind, "client_msg_no": no, "payload": base64.StdEncoding.EncodeToString([]byte(no))}
				started := time.Now().UTC()
				got, err := suite.PostMessageSendEventually(ctx, node.APIAddr(), body)
				require.NoError(t, err, node.DumpDiagnostics())
				want := frame.ReasonSuccess
				if (route.from == a && state.user == 1) || (route.restricted && state.channel == 1) {
					want = frame.ReasonSendBan
				}
				require.Equal(t, uint8(want), got.Reason, "%s/%s/ingress-%d", state.label, route.label, ingress+1)
				key := historyKey{route.actual, route.kind}
				if expected[key] == nil {
					expected[key] = map[string]bool{}
				}
				if want == frame.ReasonSuccess {
					require.Positive(t, got.MessageID)
					require.Positive(t, got.MessageSeq)
					expected[key][no] = true
				} else {
					require.Zero(t, got.MessageID)
					require.Zero(t, got.MessageSeq)
				}
				*evidence = append(*evidence, map[string]any{"nodes": len(nodes), "step": step, "state": state.label, "route": route.label, "ingress": ingress + 1, "user_ban": state.user, "channel_ban": state.channel, "client_msg_no": no, "reason": got.Reason, "seq": got.MessageSeq, "started_at": started, "elapsed_us": time.Since(started).Microseconds()})
			}
		}
	}
	for key, committed := range expected {
		var history struct {
			Messages []struct {
				ClientMsgNo string `json:"client_msg_no"`
			} `json:"items"`
			HasMore bool `json:"has_more"`
		}
		node := nodes[len(nodes)-1]
		endpoint := "http://" + node.Spec.ManagerAddr + "/manager/messages?channel_id=" + url.QueryEscape(key.id) + fmt.Sprintf("&channel_type=%d&limit=100", key.kind)
		require.Eventually(t, func() bool {
			_, err := suite.GetJSON(ctx, endpoint, &history)
			return err == nil && len(history.Messages) >= len(committed)
		}, 10*time.Second, 100*time.Millisecond, "matrix history %s", key.id)
		require.NotEmpty(t, committed, "history absence requires positive controls")
		require.False(t, history.HasMore)
		require.Len(t, history.Messages, len(committed))
		seen := map[string]bool{}
		for _, message := range history.Messages {
			require.True(t, committed[message.ClientMsgNo], "rejected or unknown message %s", message.ClientMsgNo)
			require.False(t, seen[message.ClientMsgNo], "duplicate history record")
			seen[message.ClientMsgNo] = true
		}
		*evidence = append(*evidence, map[string]any{"nodes": len(nodes), "channel_id": key.id, "channel_type": key.kind, "exact_history": seen})
	}
}

// A non-replica has no usable local policy row. Fresh routing must still observe
// a completed ban and its immediate reversal, with auxiliary caching enabled.
func TestNonReplicaIngressReadsUserSendBan(t *testing.T) {
	started := time.Now().UTC()
	var leader, ingressID uint64
	var cacheEvidence []map[string]any
	defer func() {
		path := os.Getenv("WK_E2E_SEND_BAN_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-send-ban-report.json")
		}
		path += ".non-replica.json"
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "started_at": started, "finished_at": time.Now().UTC(), "hash_slots": 256, "slot_replicas": 1, "nodes": 3, "leader": leader, "ingress": ingressID, "source_revision": os.Getenv("WK_E2E_SOURCE_REVISION"), "permission_cache_ttl": "1h", "cache_transitions": cacheEvidence}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, append(raw, '\n'), 0644))
		t.Logf("non-replica report: %s", path)
	}()
	opts := []suite.Option{suite.WithManagerHTTP()}
	for i := uint64(1); i <= 3; i++ {
		opts = append(opts, suite.WithNodeConfigOverrides(i, map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_CLUSTER_SLOT_REPLICA_N": "1", "WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_MESSAGE_PERMISSION_CACHE_TTL": "1h"}))
	}
	c := suite.New(t).StartThreeNodeCluster(opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	require.NoError(t, c.WaitClusterReady(ctx))
	_, err := c.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	const uid = "ban-non-replica"
	hashSlot := uint16(crc32.ChecksumIEEE([]byte(uid)) % 256)
	for _, slot := range c.ManagerClient(t, 1).MustSlots(t) {
		if slot.HashSlots == nil {
			continue
		}
		for _, h := range slot.HashSlots.Items {
			if h == hashSlot {
				require.Len(t, slot.Runtime.CurrentVoters, 1)
				leader = slot.Runtime.LeaderID
				require.Equal(t, slot.Runtime.CurrentVoters[0], leader)
			}
		}
	}
	require.NotZero(t, leader)
	ingressID = leader%3 + 1
	owner, ingress := c.MustNode(leader), c.MustNode(ingressID)
	_, err = suite.PostJSON(ctx, "http://"+owner.APIAddr()+"/user/send_ban", map[string]any{"uid": uid, "send_ban": 1}, nil)
	require.NoError(t, err)
	var state struct {
		Data policy `json:"data"`
	}
	_, err = suite.GetJSON(ctx, "http://"+ingress.APIAddr()+"/user/send_ban?uid="+uid, &state)
	require.NoError(t, err)
	require.Equal(t, 1, state.Data.Ban)
	// The missing destination has lower error priority than the known user ban.
	body := map[string]any{"from_uid": uid, "channel_id": "ban-non-replica-missing", "channel_type": 2, "client_msg_no": "non-replica-denied", "payload": base64.StdEncoding.EncodeToString([]byte("blocked"))}
	got, err := suite.PostMessageSend(ctx, ingress.APIAddr(), body)
	require.NoError(t, err)
	require.Equal(t, uint8(frame.ReasonSendBan), got.Reason)
	require.Zero(t, got.MessageSeq)
	_, err = suite.PostJSON(ctx, "http://"+owner.APIAddr()+"/user/send_ban", map[string]any{"uid": uid, "send_ban": 0}, nil)
	require.NoError(t, err)
	body["client_msg_no"] = "non-replica-unbanned"
	got, err = suite.PostMessageSend(ctx, ingress.APIAddr(), body)
	require.NoError(t, err)
	require.Equal(t, uint8(frame.ReasonChannelNotExist), got.Reason)
	cacheEvidence = append(cacheEvidence, map[string]any{"state": "missing-channel-warmed", "reason": got.Reason})
	const room = "ban-non-replica-missing"
	require.NoError(t, suite.PostChannel(ctx, owner.APIAddr(), map[string]any{"channel_id": room, "channel_type": 2, "send_ban": 1, "subscribers": []string{uid}}))
	check := func(label string, want frame.ReasonCode) {
		t.Helper()
		body["client_msg_no"] = label
		got, err := suite.PostMessageSendEventually(ctx, ingress.APIAddr(), body)
		require.NoError(t, err)
		require.Equal(t, uint8(want), got.Reason, label)
		if want != frame.ReasonSuccess {
			require.Zero(t, got.MessageID)
			require.Zero(t, got.MessageSeq)
		}
		cacheEvidence = append(cacheEvidence, map[string]any{"state": label, "reason": got.Reason, "seq": got.MessageSeq, "at": time.Now().UTC()})
	}
	check("missing-to-channel-ban", frame.ReasonSendBan)
	// The trusted device isolates mandatory policy transitions from the legacy
	// auxiliary membership TTL warmed while this group did not exist.
	body["device_id"] = "____device"
	check("channel-ban-with-trusted-device", frame.ReasonSendBan)
	_, err = suite.PostJSON(ctx, "http://"+owner.APIAddr()+"/channel/send_ban", map[string]any{"channel_id": room, "channel_type": 2, "send_ban": 0}, nil)
	require.NoError(t, err)
	check("channel-ban-to-allow", frame.ReasonSuccess)
	_, err = suite.PostJSON(ctx, "http://"+owner.APIAddr()+"/user/send_ban", map[string]any{"uid": uid, "send_ban": 1}, nil)
	require.NoError(t, err)
	check("warm-allow-to-user-ban", frame.ReasonSendBan)
	_, err = suite.PostJSON(ctx, "http://"+owner.APIAddr()+"/user/send_ban", map[string]any{"uid": uid, "send_ban": 0}, nil)
	require.NoError(t, err)
	check("warm-user-ban-to-allow", frame.ReasonSuccess)
	var history struct {
		More     int `json:"more"`
		Messages []struct {
			ClientMsgNo string `json:"client_msg_no"`
		} `json:"messages"`
	}
	require.Eventually(t, func() bool {
		_, err := suite.PostJSON(ctx, "http://"+ingress.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": uid, "channel_id": room, "channel_type": 2, "limit": 100}, &history)
		return err == nil && len(history.Messages) >= 2
	}, 10*time.Second, 100*time.Millisecond)
	require.Zero(t, history.More)
	seen := []string{}
	for _, message := range history.Messages {
		seen = append(seen, message.ClientMsgNo)
	}
	require.ElementsMatch(t, []string{"channel-ban-to-allow", "warm-user-ban-to-allow"}, seen)
	cacheEvidence = append(cacheEvidence, map[string]any{"state": "exact-history", "client_msg_nos": seen})
}

// Failure cases: a failed Channel Slot must not hide an authoritative user ban;
// after user unban, that unavailable Slot must fail closed rather than reuse its
// warm allow. Neither failure may append a message before or after recovery.
func TestKnownUserBanPrecedesUnavailableChannelSlot(t *testing.T) {
	started := time.Now().UTC()
	var evidence []map[string]any
	var userSlot, channelSlot suite.SlotDTO
	var ingressID uint64
	defer func() {
		path := os.Getenv("WK_E2E_SEND_BAN_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-send-ban-report.json")
		}
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "started_at": started, "finished_at": time.Now().UTC(), "nodes": 3, "hash_slots": 256, "initial_slots": 12, "slot_replicas": 1, "source_revision": os.Getenv("WK_E2E_SOURCE_REVISION"), "user_slot": userSlot, "channel_slot": channelSlot, "ingress": ingressID, "observations": evidence, "performance_qualified": false}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path+".priority.json", append(raw, '\n'), 0644))
	}()
	opts := []suite.Option{suite.WithManagerHTTP()}
	for id := uint64(1); id <= 3; id++ {
		opts = append(opts, suite.WithNodeConfigOverrides(id, map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_CLUSTER_SLOT_REPLICA_N": "1", "WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_MESSAGE_PERMISSION_CACHE_TTL": "1h"}))
	}
	cluster := suite.New(t).StartThreeNodeCluster(opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx))
	_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	placements := map[uint16]suite.SlotDTO{}
	for _, slot := range cluster.ManagerClient(t, 1).MustSlots(t) {
		if slot.HashSlots != nil {
			for _, h := range slot.HashSlots.Items {
				placements[h] = slot
			}
		}
	}
	const uid = "ban-priority-sender"
	userSlot = placements[uint16(crc32.ChecksumIEEE([]byte(uid))%256)]
	require.NotZero(t, userSlot.Runtime.LeaderID)
	require.Equal(t, []uint64{userSlot.Runtime.LeaderID}, userSlot.Runtime.CurrentVoters)
	var room string
	for i := 0; i < 10000; i++ {
		id := fmt.Sprintf("ban-priority-room-%d", i)
		slot := placements[uint16(crc32.ChecksumIEEE([]byte(id))%256)]
		if slot.Runtime.LeaderID != 0 && slot.Runtime.LeaderID != userSlot.Runtime.LeaderID {
			room, channelSlot = id, slot
			break
		}
	}
	require.NotEmpty(t, room)
	require.Equal(t, []uint64{channelSlot.Runtime.LeaderID}, channelSlot.Runtime.CurrentVoters)
	for id := uint64(1); id <= 3; id++ {
		if id != userSlot.Runtime.LeaderID && id != channelSlot.Runtime.LeaderID {
			ingressID = id
		}
	}
	require.NotZero(t, ingressID)
	owner, ingress, stopped := cluster.MustNode(userSlot.Runtime.LeaderID), cluster.MustNode(ingressID), cluster.MustNode(channelSlot.Runtime.LeaderID)
	require.NoError(t, suite.PostChannel(ctx, owner.APIAddr(), map[string]any{"channel_id": room, "channel_type": 2, "subscribers": []string{uid}}))
	setUser := func(value int) {
		t.Helper()
		var out struct {
			Data policy `json:"data"`
		}
		_, err := suite.PostJSON(ctx, "http://"+owner.APIAddr()+"/user/send_ban", map[string]any{"uid": uid, "send_ban": value}, &out)
		require.NoError(t, err)
		require.Equal(t, value, out.Data.Ban)
		evidence = append(evidence, map[string]any{"operation": "user-policy-write", "value": value, "version": out.Data.Version, "at": time.Now().UTC()})
	}
	committed := map[string]bool{}
	send := func(node *suite.StartedNode, label string, unavailable bool, want frame.ReasonCode) {
		t.Helper()
		body := map[string]any{"from_uid": uid, "channel_id": room, "channel_type": 2, "client_msg_no": label, "payload": base64.StdEncoding.EncodeToString([]byte(label))}
		requestCtx, requestCancel := context.WithTimeout(ctx, 12*time.Second)
		defer requestCancel()
		began := time.Now().UTC()
		var got suite.MessageSendResponse
		var err error
		if want == frame.ReasonSuccess && !unavailable {
			got, err = suite.PostMessageSendEventually(requestCtx, node.APIAddr(), body)
		} else {
			got, err = suite.PostMessageSend(requestCtx, node.APIAddr(), body)
		}
		status := http.StatusOK
		if unavailable {
			var statusErr *suite.HTTPStatusError
			require.ErrorAs(t, err, &statusErr)
			status = statusErr.StatusCode
			require.Equal(t, http.StatusServiceUnavailable, status)
		} else {
			require.NoError(t, err, node.DumpDiagnostics())
			require.Equal(t, uint8(want), got.Reason)
		}
		if unavailable || want != frame.ReasonSuccess {
			require.Zero(t, got.MessageID)
			require.Zero(t, got.MessageSeq)
		} else {
			committed[label] = true
		}
		evidence = append(evidence, map[string]any{"operation": "send", "label": label, "ingress": node.Spec.ID, "status": status, "reason": got.Reason, "seq": got.MessageSeq, "started_at": began, "elapsed_us": time.Since(began).Microseconds()})
	}
	send(ingress, "priority-warm", false, frame.ReasonSuccess)
	setUser(1)
	require.NoError(t, stopped.Stop())
	probeCtx, probeCancel := context.WithTimeout(ctx, 12*time.Second)
	_, err = suite.GetJSON(probeCtx, "http://"+ingress.APIAddr()+"/channel/send_ban?channel_id="+url.QueryEscape(room)+"&channel_type=2", nil)
	probeCancel()
	var statusErr *suite.HTTPStatusError
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, http.StatusServiceUnavailable, statusErr.StatusCode, "target policy authority must actually be unavailable")
	evidence = append(evidence, map[string]any{"operation": "channel-policy-unavailable", "status": statusErr.StatusCode, "stopped_node": stopped.Spec.ID, "at": time.Now().UTC()})
	for _, node := range []*suite.StartedNode{owner, ingress} {
		send(node, fmt.Sprintf("priority-banned-%d", node.Spec.ID), false, frame.ReasonSendBan)
	}
	setUser(0)
	for _, node := range []*suite.StartedNode{owner, ingress} {
		send(node, fmt.Sprintf("priority-unavailable-%d", node.Spec.ID), true, 0)
	}
	setUser(1)
	send(ingress, "priority-rebanned", false, frame.ReasonSendBan)
	require.NoError(t, cluster.StartStoppedNode(stopped.Spec.ID))
	require.NoError(t, cluster.WaitHTTPReady(ctx))
	_, err = cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	setUser(0)
	send(ingress, "priority-recovered", false, frame.ReasonSuccess)
	// Keep the Manager observation for fault attribution, then use the member's
	// public sync contract for the complete committed-history assertion.
	for _, node := range []*suite.StartedNode{owner, ingress, stopped} {
		raw, queryErr := suite.GetJSON(ctx, "http://"+node.Spec.ManagerAddr+"/manager/messages?channel_id="+url.QueryEscape(room)+"&channel_type=2&limit=100", nil)
		observation := map[string]any{"operation": "manager-history-probe", "node": node.Spec.ID, "body": string(raw)}
		if queryErr != nil {
			observation["error"] = queryErr.Error()
		}
		evidence = append(evidence, observation)
	}
	var history struct {
		Messages []struct {
			ClientMsgNo string `json:"client_msg_no"`
		} `json:"messages"`
		More int `json:"more"`
	}
	historyEvidence := map[string]any{"operation": "sync-history-probe"}
	evidence = append(evidence, historyEvidence)
	require.Eventually(t, func() bool {
		raw, err := suite.PostJSON(ctx, "http://"+ingress.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": uid, "channel_id": room, "channel_type": 2, "limit": 100}, &history)
		historyEvidence["body"] = string(raw)
		delete(historyEvidence, "error")
		if err != nil {
			historyEvidence["error"] = err.Error()
		}
		return err == nil && len(history.Messages) >= len(committed)
	}, 10*time.Second, 100*time.Millisecond)
	require.Zero(t, history.More)
	require.Len(t, history.Messages, 2)
	seen := map[string]bool{}
	for _, message := range history.Messages {
		require.True(t, committed[message.ClientMsgNo], "rejected request persisted")
		require.False(t, seen[message.ClientMsgNo])
		seen[message.ClientMsgNo] = true
	}
	evidence = append(evidence, map[string]any{"operation": "exact-history", "committed": seen})
}

// Failure cases: a lost write response is ambiguous, not an implicit rollback;
// concurrent metadata writers must preserve policy fields and their versions.
func TestSendBanWriteRecoveryAndConcurrentMetadata(t *testing.T) {
	started := time.Now().UTC()
	var evidence []map[string]any
	defer func() {
		path := os.Getenv("WK_E2E_SEND_BAN_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-send-ban-report.json")
		}
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "started_at": started, "finished_at": time.Now().UTC(), "nodes": 3, "hash_slots": 256, "initial_slots": 12, "token_auth": true, "source_revision": os.Getenv("WK_E2E_SOURCE_REVISION"), "observations": evidence, "performance_qualified": false}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path+".write-recovery.json", append(raw, '\n'), 0644))
	}()
	opts := []suite.Option{suite.WithManagerHTTP()}
	for id := uint64(1); id <= 3; id++ {
		opts = append(opts, suite.WithNodeConfigOverrides(id, map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_GATEWAY_TOKEN_AUTH_ON": "true", "WK_MESSAGE_PERMISSION_CACHE_TTL": "1h"}))
	}
	cluster := suite.New(t).StartThreeNodeCluster(opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	require.NoError(t, cluster.WaitClusterReady(ctx))
	_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
	require.NoError(t, err)
	const uid, room, token = "atomic-ban-user", "atomic-ban-room", "atomic-ban-test-token"
	first, last := cluster.MustNode(1), cluster.MustNode(3)
	require.NoError(t, suite.PostChannel(ctx, first.APIAddr(), map[string]any{"channel_id": room, "channel_type": 2, "subscribers": []string{uid}}))
	send := func(label string, want frame.ReasonCode) {
		t.Helper()
		got, err := suite.PostMessageSendEventually(ctx, last.APIAddr(), map[string]any{"from_uid": uid, "channel_id": room, "channel_type": 2, "client_msg_no": label, "payload": base64.StdEncoding.EncodeToString([]byte(label))})
		require.NoError(t, err)
		require.Equal(t, uint8(want), got.Reason)
		if want != frame.ReasonSuccess {
			require.Zero(t, got.MessageID)
			require.Zero(t, got.MessageSeq)
		}
		evidence = append(evidence, map[string]any{"operation": "send", "client_msg_no": label, "reason": got.Reason, "seq": got.MessageSeq, "at": time.Now().UTC()})
	}
	send("atomic-warm", frame.ReasonSuccess)
	policies := []struct {
		name, path, query string
		identity          map[string]any
	}{
		{"user", "/user/send_ban", "uid=" + uid, map[string]any{"uid": uid}},
		{"channel", "/channel/send_ban", "channel_id=" + room + "&channel_type=2", map[string]any{"channel_id": room, "channel_type": 2}},
	}
	get := func(node *suite.StartedNode, index, value int, version string) {
		t.Helper()
		p := policies[index]
		var out struct {
			Data policy `json:"data"`
		}
		_, err := suite.GetJSON(ctx, "http://"+node.APIAddr()+p.path+"?"+p.query, &out)
		require.NoError(t, err)
		require.Equal(t, value, out.Data.Ban)
		require.Equal(t, version, out.Data.Version)
	}
	for index, p := range policies {
		body := map[string]any{"send_ban": 1, "expected_version": "0"}
		for k, v := range p.identity {
			body[k] = v
		}
		upstream, callerErr := suite.PostJSONWithResponseLoss(ctx, "http://"+first.APIAddr()+p.path, body, 2*time.Second)
		require.ErrorIs(t, callerErr, context.DeadlineExceeded)
		require.NoError(t, upstream.Err)
		require.Equal(t, http.StatusOK, upstream.StatusCode, "fault must withhold a successful real write response")
		get(last, index, 1, "1")
		evidence = append(evidence, map[string]any{"operation": "lost-write-response", "scope": p.name, "caller_timeout": errors.Is(callerErr, context.DeadlineExceeded), "upstream_status": upstream.StatusCode, "recovered_version": "1"})
		body["send_ban"] = 0
		_, err := suite.PostJSON(ctx, "http://"+last.APIAddr()+p.path, body, nil)
		var statusErr *suite.HTTPStatusError
		require.ErrorAs(t, err, &statusErr)
		require.Equal(t, http.StatusConflict, statusErr.StatusCode)
		get(first, index, 1, "1")
	}
	// Four concurrent writer families share a start gate each round. Values and
	// versions are checked after every round, not only after the final write.
	for round := 0; round < 8; round++ {
		start := make(chan struct{})
		results := make(chan error, 4)
		post := func(nodeID uint64, path string, body map[string]any) error {
			_, err := suite.PostJSON(ctx, "http://"+cluster.MustNode(nodeID).APIAddr()+path, body, nil)
			return err
		}
		jobs := []func() error{
			func() error {
				return post(1, "/user/token", map[string]any{"uid": uid, "token": token, "device_flag": int(frame.APP), "device_level": 0})
			},
			func() error {
				return post(2, "/channel/info", map[string]any{"channel_id": room, "channel_type": 2, "large": round % 2})
			},
			func() error {
				body := map[string]any{"channel_id": room, "channel_type": 2, "subscribers": []string{fmt.Sprintf("atomic-peer-%d", round)}}
				if err := post(3, "/channel/subscriber_add", body); err != nil {
					return err
				}
				return post(3, "/channel/subscriber_remove", body)
			},
			func() error {
				if err := post(2, "/user/send_ban", map[string]any{"uid": uid, "send_ban": 1, "expected_version": "1"}); err != nil {
					return err
				}
				return post(1, "/channel/send_ban", map[string]any{"channel_id": room, "channel_type": 2, "send_ban": 1, "expected_version": "1"})
			},
		}
		var joined sync.WaitGroup
		for _, job := range jobs {
			joined.Add(1)
			go func() { defer joined.Done(); <-start; results <- job() }()
		}
		close(start)
		joined.Wait()
		close(results)
		for err := range results {
			require.NoError(t, err, "concurrent round %d", round)
		}
		for id := uint64(1); id <= 3; id++ {
			for index := range policies {
				get(cluster.MustNode(id), index, 1, "1")
			}
		}
		send(fmt.Sprintf("atomic-denied-%d", round), frame.ReasonSendBan)
		evidence = append(evidence, map[string]any{"operation": "concurrent-writers", "round": round, "writers": len(jobs), "user_ban": 1, "channel_ban": 1, "versions": []string{"1", "1"}})
	}
	for id := uint64(1); id <= 3; id++ {
		client, err := wkclient.New(wkclient.Config{Addr: cluster.MustNode(id).GatewayAddr(), OperationTimeout: 5 * time.Second})
		require.NoError(t, err)
		_, connectErr := client.Connect(ctx, wkclient.ConnectOptions{UID: uid, DeviceID: fmt.Sprintf("atomic-device-%d", id), DeviceFlag: frame.APP, Token: token})
		require.NoError(t, client.Close())
		require.NoError(t, connectErr, "banned user retains valid Token at node %d", id)
		evidence = append(evidence, map[string]any{"operation": "token-auth-while-banned", "node": id, "success": true})
	}
	for index, p := range policies {
		body := map[string]any{"send_ban": 0, "expected_version": "1"}
		for k, v := range p.identity {
			body[k] = v
		}
		_, err := suite.PostJSON(ctx, "http://"+first.APIAddr()+p.path, body, nil)
		require.NoError(t, err)
		get(last, index, 0, "2")
		if index == 0 {
			send("atomic-one-unbanned", frame.ReasonSendBan)
		}
	}
	send("atomic-recovered", frame.ReasonSuccess)
	var history struct {
		More     int `json:"more"`
		Messages []struct {
			ClientMsgNo string `json:"client_msg_no"`
		} `json:"messages"`
	}
	require.Eventually(t, func() bool {
		_, err := suite.PostJSON(ctx, "http://"+last.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": uid, "channel_id": room, "channel_type": 2, "limit": 100}, &history)
		return err == nil && len(history.Messages) >= 2
	}, 10*time.Second, 100*time.Millisecond)
	require.Zero(t, history.More)
	seen := []string{}
	for _, message := range history.Messages {
		seen = append(seen, message.ClientMsgNo)
	}
	require.ElementsMatch(t, []string{"atomic-warm", "atomic-recovered"}, seen)
	evidence = append(evidence, map[string]any{"operation": "exact-history", "client_msg_nos": seen})
}
