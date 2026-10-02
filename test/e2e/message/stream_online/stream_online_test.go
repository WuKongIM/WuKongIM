//go:build e2e

package stream_online_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestStreamOnlineAndOfflineRecovery(t *testing.T) {
	var passed []string
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d_node_cluster", count), func(t *testing.T) {
			s := suite.New(t)
			opts := []suite.Option{suite.WithWebSocketGateway()}
			for i := 1; i <= count; i++ {
				opts = append(opts, suite.WithNodeConfigOverrides(uint64(i), map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256"}))
			}
			var ingress, receiver *suite.StartedNode
			if count == 1 {
				ingress = s.StartSingleNodeCluster(opts...)
				receiver = ingress
			} else {
				cluster := s.StartThreeNodeCluster(opts...)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
				ingress = cluster.MustNode(1)
				receiver = cluster.MustNode(3)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			uid := fmt.Sprintf("stream-reader-%d", count)
			require.NoError(t, postJSON(ctx, receiver.APIAddr()+"/user/token", map[string]any{"uid": uid, "token": "stream-test-token", "device_flag": 1, "device_level": 1}, nil))
			ws, _, err := websocket.DefaultDialer.Dial(receiver.WebSocketURL(), nil)
			require.NoError(t, err)
			defer ws.Close()
			require.NoError(t, ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": "connect", "method": "connect", "params": map[string]any{"uid": uid, "token": "stream-test-token", "deviceId": "stream-test-browser", "deviceFlag": 1, "clientTimestamp": time.Now().UnixMilli()}}))
			require.NoError(t, ws.SetReadDeadline(time.Now().Add(8*time.Second)))
			var ack map[string]any
			require.NoError(t, ws.ReadJSON(&ack))
			require.Nil(t, ack["error"])
			for _, kind := range []int{2, 1} {
				channel := fmt.Sprintf("stream-room-%d", count)
				from := fmt.Sprintf("stream-bot-%d", count)
				if kind == 2 {
					require.NoError(t, suite.PostChannel(ctx, ingress.APIAddr(), map[string]any{"channel_id": channel, "channel_type": 2, "reset": 1, "subscribers": []string{uid, from}}))
				} else {
					channel = uid
				}
				for _, terminal := range []string{"stream.finish", "stream.cancel", "stream.error"} {
					key := fmt.Sprintf("case-%d-%d-%s", count, kind, terminal)
					sent, err := suite.PostMessageSend(ctx, ingress.APIAddr(), map[string]any{"channel_id": channel, "channel_type": kind, "from_uid": from, "client_msg_no": key, "setting": 2, "payload": base64.StdEncoding.EncodeToString([]byte(`{"type":1,"content":""}`))})
					require.NoError(t, err)
					require.Equal(t, uint8(1), sent.Reason)
					post := func(id, event, visibility string, payload any) {
						var response map[string]any
						lane := "main"
						if visibility == "private" {
							lane = "secret"
						}
						require.NoError(t, postJSON(ctx, ingress.APIAddr()+"/message/event", map[string]any{"channel_id": channel, "channel_type": kind, "from_uid": from, "client_msg_no": key, "event_id": key + id, "event_type": event, "event_key": lane, "visibility": visibility, "payload": payload}, &response))
						require.Equal(t, float64(200), response["status"])
					}
					post("-private", "stream.delta", "private", map[string]any{"kind": "text", "delta": "private"})
					post("-open", "stream.open", "public", map[string]any{"kind": "text", "text": ""})
					post("-delta", "stream.delta", "public", map[string]any{"kind": "text", "delta": "hello"})
					post("-end", terminal, "public", map[string]any{"snapshot": map[string]any{"kind": "text", "text": "hello"}, "error": "simulated failure"})
					for _, expected := range []string{"stream.open", "stream.delta", terminal} {
						event := nextEvent(t, ws, key)
						require.Equal(t, expected, event["type"])
						require.NotEmpty(t, event["id"])
						require.NotZero(t, event["timestamp"])
						var data map[string]any
						require.NoError(t, json.Unmarshal([]byte(event["data"].(string)), &data))
						projected := channel
						if kind == 1 {
							projected = from
						}
						require.Equal(t, projected, data["channel_id"])
						require.Equal(t, key, data["client_msg_no"])
						if expected == "stream.delta" {
							require.Equal(t, "hello", data["payload"].(map[string]any)["delta"])
						}
					}
					if terminal != "stream.finish" {
						post("-finish", "stream.finish", "public", map[string]any{"snapshot": map[string]any{"kind": "text", "text": "hello"}})
						require.Equal(t, "stream.finish", nextEvent(t, ws, key)["type"])
					}
					// A late retry must not reopen the completed persisted projection.
					post("-delta", "stream.delta", "public", map[string]any{"kind": "text", "delta": "hello"})
					syncChannel := channel
					if kind == 1 {
						syncChannel = from
					}
					var history map[string]any
					var historyErr error
					require.Eventually(t, func() bool {
						historyErr = postJSON(ctx, receiver.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": uid, "channel_id": syncChannel, "channel_type": kind, "start_message_seq": sent.MessageSeq, "limit": 1, "event_summary_mode": "full"}, &history)
						rows, _ := history["messages"].([]any)
						return historyErr == nil && len(rows) == 1
					}, 5*time.Second, 100*time.Millisecond, "offline history: %v", history)
					require.NoError(t, historyErr)
					rows := history["messages"].([]any)
					meta := rows[0].(map[string]any)["event_meta"].(map[string]any)
					require.Equal(t, true, meta["completed"])
					var main map[string]any
					for _, lane := range meta["events"].([]any) {
						l := lane.(map[string]any)
						if l["event_key"] == "main" {
							main = l
						}
					}
					require.NotNil(t, main)
					require.Equal(t, "hello", main["snapshot"].(map[string]any)["text"])
					status := "closed"
					if terminal == "stream.cancel" {
						status = "cancelled"
					}
					if terminal == "stream.error" {
						status = "error"
					}
					require.Equal(t, status, main["status"])
					passed = append(passed, key)
				}
			}
			// An event must never create a phantom online message without a committed base.
			var rejected map[string]any
			err = postJSON(ctx, ingress.APIAddr()+"/message/event", map[string]any{"channel_id": uid, "channel_type": 1, "from_uid": "stream-bot", "client_msg_no": "missing-base", "event_id": "bad", "event_type": "stream.delta", "payload": map[string]any{"delta": "bad"}}, &rejected)
			require.True(t, err != nil || rejected["status"] != float64(200))
		})
	}
	if !t.Failed() {
		path := os.Getenv("WK_E2E_STREAM_REPORT")
		if path != "" {
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
			body, err := json.MarshalIndent(map[string]any{"source": "public HTTP and JSON-RPC WebSocket", "hash_slots": 256, "passed": passed}, "", "  ")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, body, 0644))
			t.Logf("report: %s", path)
		}
	}
}

func nextEvent(t *testing.T, ws *websocket.Conn, key string) map[string]any {
	t.Helper()
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(5*time.Second)))
	for i := 0; i < 32; i++ {
		var packet map[string]any
		require.NoError(t, ws.ReadJSON(&packet))
		if packet["method"] == "recv" {
			p := packet["params"].(map[string]any)
			require.NoError(t, ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "method": "recvack", "params": map[string]any{"messageId": p["messageId"], "messageSeq": p["messageSeq"], "header": p["header"]}}))
		}
		if packet["method"] == "event" {
			e := packet["params"].(map[string]any)
			var data map[string]any
			require.NoError(t, json.Unmarshal([]byte(e["data"].(string)), &data))
			if data["client_msg_no"] == key {
				return e
			}
		}
	}
	t.Fatal("no matching stream event in bounded frame window")
	return nil
}

func postJSON(ctx context.Context, url string, body, out any) error {
	_, err := suite.PostJSON(ctx, "http://"+url, body, out)
	return err
}
