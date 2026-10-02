//go:build e2e

package send_ban

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	rpc "github.com/WuKongIM/wkrpc/proto"
	"github.com/stretchr/testify/require"
)

type observation struct {
	Nodes      int       `json:"nodes"`
	Label      string    `json:"label"`
	Status     uint16    `json:"status"`
	MessageID  int64     `json:"message_id"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
}

// Exercise policy admission and host acknowledgement through an actual plugin
// process, with successful delivery/history controls on either side of bans.
func TestPluginSendBan(t *testing.T) {
	var observations []observation
	var boundaries []map[string]any
	started := time.Now().UTC()
	t.Cleanup(func() {
		report := os.Getenv("WK_E2E_PLUGIN_SEND_BAN_REPORT")
		if report == "" {
			report = filepath.Join(os.TempDir(), "wukongim-plugin-send-ban.json")
		}
		raw, err := json.MarshalIndent(map[string]any{"passed": !t.Failed(), "started_at": started, "finished_at": time.Now().UTC(), "hash_slots": 256, "initial_slots": 12, "permission_cache_ttl": "1h", "source_revision": os.Getenv("WK_E2E_SOURCE_REVISION"), "source_fingerprint": os.Getenv("WK_E2E_SOURCE_FINGERPRINT"), "observations": observations, "inflight_boundaries": boundaries}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(report), 0755))
		require.NoError(t, os.WriteFile(report, append(raw, '\n'), 0644))
		t.Logf("plugin send-ban report: %s", report)
	})
	for _, count := range []int{1, 3} {
		t.Run(fmt.Sprintf("%d-node-cluster", count), func(t *testing.T) {
			root := t.TempDir()
			plugins := filepath.Join(root, "plugins")
			sandbox := filepath.Join(root, "sandbox", "sendban")
			require.NoError(t, os.MkdirAll(plugins, 0755))
			require.NoError(t, os.MkdirAll(sandbox, 0755))
			socketRoot, err := os.MkdirTemp("/tmp", "wk-ban-")
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.RemoveAll(socketRoot) })
			_, source, _, ok := runtime.Caller(0)
			require.True(t, ok)
			build := exec.Command("go", "build", "-o", filepath.Join(plugins, "sendban.wkp"), "./test/e2e/plugin/send_ban/testdata/sendban")
			build.Dir = filepath.Clean(filepath.Join(filepath.Dir(source), "../../../.."))
			output, err := build.CombinedOutput()
			require.NoError(t, err, string(output))
			var options []suite.Option
			for node := 1; node <= count; node++ {
				conf := map[string]string{"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_GATEWAY_TOKEN_AUTH_ON": "false", "WK_MESSAGE_PERMISSION_CACHE_TTL": "1h"}
				if node == count {
					for key, value := range map[string]string{"WK_PLUGIN_ENABLE": "true", "WK_PLUGIN_DIR": plugins, "WK_PLUGIN_SOCKET_PATH": filepath.Join(socketRoot, "host.sock"), "WK_PLUGIN_SANDBOX_DIR": filepath.Join(root, "sandbox"), "WK_PLUGIN_STATE_DIR": filepath.Join(root, "state"), "WK_PLUGIN_TIMEOUT": "5s", "WK_PLUGIN_HOT_RELOAD": "false"} {
						conf[key] = value
					}
				}
				options = append(options, suite.WithNodeConfigOverrides(uint64(node), conf))
			}
			s := suite.New(t)
			var ingress, admin *suite.StartedNode
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			if count == 1 {
				ingress = s.StartSingleNodeCluster(options...)
				admin = ingress
			} else {
				cluster := s.StartThreeNodeCluster(append(options, suite.WithManagerHTTP())...)
				require.NoError(t, cluster.WaitClusterReady(ctx), cluster.DumpDiagnostics())
				_, err := cluster.WaitSlotLeadersStable(ctx, time.Second)
				require.NoError(t, err)
				ingress = cluster.MustNode(uint64(count))
				admin = cluster.MustNode(1)
			}
			require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(sandbox, "ready")); return err == nil }, 10*time.Second, 20*time.Millisecond, ingress.DumpDiagnostics())
			const sender, receiver, room = "plugin-ban-sender", "plugin-ban-receiver", "plugin-ban-room"
			require.NoError(t, suite.PostChannel(ctx, admin.APIAddr(), map[string]any{"channel_id": room, "channel_type": 2, "subscribers": []string{sender, receiver}}))
			recv, err := suite.NewWKProtoClientWithTimeout(2 * time.Second)
			require.NoError(t, err)
			defer recv.Close()
			require.NoError(t, recv.Connect(admin.GatewayAddr(), receiver, "plugin-ban-device"))
			received := func(label string) {
				t.Helper()
				packet, err := recv.ReadRecv()
				require.NoError(t, err)
				require.Equal(t, sender, packet.FromUID)
				require.Equal(t, room, packet.ChannelID)
				require.Equal(t, "hook:"+label, string(packet.Payload))
				require.NoError(t, recv.RecvAck(packet.MessageID, packet.MessageSeq))
			}
			warm, err := suite.PostMessageSendEventually(ctx, ingress.APIAddr(), map[string]any{"from_uid": sender, "channel_id": room, "channel_type": 2, "client_msg_no": "http-warm", "payload": base64.StdEncoding.EncodeToString([]byte("http-warm"))})
			require.NoError(t, err)
			require.Equal(t, uint8(frame.ReasonSuccess), warm.Reason)
			received("http-warm")
			next := 0
			send := func(label, from, target string, kind uint32, noPersist bool, allowed bool) {
				t.Helper()
				req := &pluginproto.SendReq{FromUid: from, ChannelId: target, ChannelType: kind, ClientMsgNo: label, Payload: []byte(label), Header: &pluginproto.Header{NoPersist: noPersist}}
				raw, err := json.Marshal(req)
				require.NoError(t, err)
				command := filepath.Join(sandbox, fmt.Sprintf("command-%03d.json", next))
				result := filepath.Join(sandbox, fmt.Sprintf("result-%03d.json", next))
				next++
				require.NoError(t, os.WriteFile(command+".tmp", raw, 0600))
				require.NoError(t, os.Rename(command+".tmp", command))
				var obs observation
				require.Eventually(t, func() bool { raw, err := os.ReadFile(result); return err == nil && json.Unmarshal(raw, &obs) == nil }, 8*time.Second, 20*time.Millisecond, ingress.DumpDiagnostics())
				obs.Nodes = count
				obs.Label = label
				observations = append(observations, obs)
				if allowed {
					require.Equal(t, uint16(rpc.StatusOK), obs.Status, obs)
					require.Empty(t, obs.Error)
					require.NotZero(t, obs.MessageID)
				} else {
					require.Equal(t, uint16(rpc.StatusError), obs.Status, obs)
					require.Equal(t, "message send rejected: reason=25", obs.Error)
					require.Zero(t, obs.MessageID)
				}
			}
			setUser := func(uid string, value int) {
				t.Helper()
				_, err := suite.SetUserSendBan(ctx, admin.APIAddr(), uid, value)
				require.NoError(t, err)
			}
			setChannel := func(value int) {
				t.Helper()
				_, err := suite.SetChannelSendBan(ctx, admin.APIAddr(), room, 2, value)
				require.NoError(t, err)
			}
			send("plugin-before", sender, room, 2, false, true)
			received("plugin-before")
			setUser(sender, 1)
			send("user-group", sender, room, 2, false, false)
			send("user-person", sender, receiver, 1, false, false)
			send("user-transient", sender, room, 2, true, false)
			setChannel(1)
			setUser(sender, 0)
			send("channel-group", sender, room, 2, false, false)
			send("channel-transient", sender, room, 2, true, false)
			setChannel(0)
			setUser("____system", 1)
			send("system-default", "", room, 2, false, false)
			setUser("____system", 0)
			// No rejected packet may appear; a later accepted control also detects queued leakage.
			packet, err := recv.ReadRecv()
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Nil(t, packet)
			setChannel(0)
			send("plugin-after", sender, room, 2, false, true)
			received("plugin-after")
			type httpResult struct {
				result suite.MessageSendResponse
				err    error
			}
			inflight := make(chan httpResult, 1)
			inflightStarted := time.Now().UTC()
			go func() {
				result, err := suite.PostMessageSendEventually(ctx, ingress.APIAddr(), map[string]any{"from_uid": sender, "channel_id": room, "channel_type": 2, "client_msg_no": "inflight-before-ban", "payload": base64.StdEncoding.EncodeToString([]byte("inflight-before-ban"))})
				inflight <- httpResult{result, err}
			}()
			require.Eventually(t, func() bool { _, err := os.Stat(filepath.Join(sandbox, "hook-entered")); return err == nil }, 3*time.Second, 10*time.Millisecond)
			hookEntered := time.Now().UTC()
			setUser(sender, 1)
			banCompleted := time.Now().UTC()
			require.NoError(t, os.WriteFile(filepath.Join(sandbox, "hook-release"), []byte("release"), 0600))
			select {
			case done := <-inflight:
				require.NoError(t, done.err)
				require.Equal(t, uint8(frame.ReasonSuccess), done.result.Reason)
				require.NotZero(t, done.result.MessageID)
				boundaries = append(boundaries, map[string]any{"nodes": count, "send_started": inflightStarted, "hook_entered": hookEntered, "ban_completed": banCompleted, "send_completed": time.Now().UTC(), "message_id": done.result.MessageID})
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			received("inflight-before-ban")
			send("new-after-inflight-ban", sender, room, 2, false, false)
			packet, err = recv.ReadRecv()
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Nil(t, packet)
			var history struct {
				More     int `json:"more"`
				Messages []struct {
					ClientMsgNo string `json:"client_msg_no"`
					FromUID     string `json:"from_uid"`
					Payload     string `json:"payload"`
				} `json:"messages"`
			}
			require.Eventually(t, func() bool {
				_, err := suite.PostJSON(ctx, "http://"+admin.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": sender, "channel_id": room, "channel_type": 2, "limit": 100}, &history)
				return err == nil && len(history.Messages) >= 4
			}, 10*time.Second, 50*time.Millisecond)
			require.Zero(t, history.More)
			var names []string
			for _, message := range history.Messages {
				names = append(names, message.ClientMsgNo)
				require.Equal(t, sender, message.FromUID)
				payload, err := base64.StdEncoding.DecodeString(message.Payload)
				require.NoError(t, err)
				require.Equal(t, "hook:"+message.ClientMsgNo, string(payload))
			}
			require.ElementsMatch(t, []string{"http-warm", "plugin-before", "plugin-after", "inflight-before-ban"}, names)
		})
	}
}
