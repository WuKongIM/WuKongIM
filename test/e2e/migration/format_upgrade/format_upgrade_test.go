//go:build e2e

package format_upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

type receipt struct {
	Case             string              `json:"case"`
	Passed           bool                `json:"passed"`
	Phase            string              `json:"phase"`
	LeaderBefore     uint64              `json:"leader_before"`
	LeaderAfter      uint64              `json:"leader_after"`
	SnapshotInstalls int                 `json:"snapshot_installs"`
	HistoryChecks    int                 `json:"history_checks"`
	Backups          []map[string]string `json:"backups"`
}

func TestFormat1ThreeNodeStartupUpgrade(t *testing.T) {
	old := os.Getenv("WK_E2E_FORMAT1_BINARY")
	if old == "" {
		t.Skip("requires unchanged beta.21: set WK_E2E_FORMAT1_BINARY and WK_E2E_BINARY; see AGENTS.md")
	}
	candidate := os.Getenv("WK_E2E_BINARY")
	require.NotEmpty(t, candidate, "provide the candidate binary")
	report := struct {
		OldSHA string     `json:"old_binary_sha256"`
		NewSHA string     `json:"new_binary_sha256"`
		Cases  []*receipt `json:"cases"`
	}{OldSHA: digest(t, old), NewSHA: digest(t, candidate)}
	defer func() {
		path := os.Getenv("WK_E2E_FORMAT_UPGRADE_REPORT")
		if path == "" {
			path = filepath.Join(os.TempDir(), "wukongim-format-upgrade.json")
		}
		body, err := json.MarshalIndent(report, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, body, 0600))
		t.Logf("receipt: %s", path)
	}()
	cases := []string{"normal"}
	if os.Getenv("WK_E2E_FORMAT_UPGRADE_FAULTS") == "1" {
		cases = append(cases, "formatUpgradeBackupFile", "formatUpgradeBatch", "formatUpgradePublish")
	}
	for _, name := range cases {
		out := &receipt{Case: name}
		report.Cases = append(report.Cases, out)
		out.Passed = t.Run(name, func(t *testing.T) { runUpgrade(t, old, candidate, out) })
	}
}

func runUpgrade(t *testing.T, old, candidate string, out *receipt) {
	ctx, cancel := context.WithTimeout(context.Background(), 210*time.Second)
	defer cancel()
	out.Phase = "old-start"
	t.Setenv("WK_E2E_BINARY", old)
	opts := []suite.Option{suite.WithConfigFileOnly(), suite.WithManagerHTTP()}
	for n := uint64(1); n <= 3; n++ {
		opts = append(opts, suite.WithNodeConfigOverrides(n, map[string]string{
			"WK_CLUSTER_HASH_SLOT_COUNT": "256", "WK_CLUSTER_INITIAL_SLOT_COUNT": "12", "WK_CLUSTER_CHANNEL_REPLICA_N": "3", "WK_CLUSTER_SLOT_LOG_COMPACTION_TRIGGER_ENTRIES": "16", "WK_CLUSTER_SLOT_LOG_COMPACTION_CHECK_INTERVAL": "1s",
		}))
	}
	cluster := suite.New(t).StartStaticCluster(3, opts...)
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(cluster.DumpDiagnostics())
		}
	})
	require.NoError(t, cluster.WaitClusterReady(ctx))
	addr := cluster.Nodes[0].APIAddr()
	for _, uid := range []string{"alice", "bob", "blocked"} {
		_, err := suite.PostJSON(ctx, "http://"+addr+"/user/token", map[string]any{"uid": uid, "token": "original-" + uid, "device_flag": 0, "device_level": 0}, nil)
		require.NoError(t, err)
	}
	for i := 0; i < 128; i++ {
		_, err := suite.PostJSON(ctx, "http://"+addr+"/user/token", map[string]any{"uid": fmt.Sprintf("resume-user-%03d", i), "token": "fixture-token", "device_flag": 0, "device_level": 0}, nil)
		require.NoError(t, err)
	}
	require.NoError(t, suite.PostChannel(ctx, addr, map[string]any{"channel_id": "upgrade-group", "channel_type": 2, "subscribers": []string{"alice", "bob", "blocked"}}))
	require.NoError(t, suite.PostChannel(ctx, addr, map[string]any{"channel_id": "banned-group", "channel_type": 2, "send_ban": 1, "subscribers": []string{"alice"}}))
	// beta.21 stores UID-wide bans in a person Channel row, including absent users.
	for _, uid := range []string{"blocked", "not-yet-registered"} {
		require.NoError(t, suite.PostChannel(ctx, addr, map[string]any{"channel_id": uid, "channel_type": 1, "send_ban": 1}))
	}
	var facts []suite.MessageSendResponse
	for i := 0; i < 8; i++ {
		facts = append(facts, send(t, ctx, addr, fmt.Sprintf("old-%d", i)))
	}
	stop(t, cluster)
	out.Phase = "upgrade"
	before := make([]map[string]string, 3)
	for i, n := range cluster.Nodes {
		before[i] = treeHashes(t, n.Spec.DataDir)
	}
	// Faults are set before startup; no test configuration enters product TOML.
	if out.Case != "normal" {
		ep := suite.ReserveGofailEndpoint(t)
		spec := cluster.Nodes[0].Spec
		spec.Env = append(spec.Env, ep.Env(), "GOFAIL_FAILPOINTS="+out.Case+"=sleep(60000)")
		p := &suite.NodeProcess{Spec: spec, BinaryPath: candidate}
		cluster.Nodes[0].Process = p
		require.NoError(t, p.Start())
		require.Eventually(t, func() bool { n, err := ep.Count(ctx, out.Case); return err == nil && n >= 1 }, 15*time.Second, 20*time.Millisecond)
		contenderSpec := cluster.Nodes[0].Spec
		contenderSpec.StdoutPath += ".contender"
		contenderSpec.StderrPath += ".contender"
		contender := &suite.NodeProcess{Spec: contenderSpec, BinaryPath: candidate}
		require.NoError(t, contender.Start())
		_, contenderErr := contender.WaitHTTPReady(ctx, contenderSpec.APIAddr, "/readyz")
		require.Error(t, contenderErr)
		require.NoError(t, contender.Stop())
		require.NoError(t, p.Cmd.Process.Kill())
		require.NoError(t, p.Stop())
		require.Equal(t, float64(1), marker(t, spec.DataDir)["format_version"])
		if out.Case == "formatUpgradePublish" {
			backupMarker := filepath.Join(spec.DataDir, "format-upgrade-v1-v2", "backup", "data", "DATA-FORMAT.json")
			original, err := os.ReadFile(backupMarker)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(backupMarker, []byte("tampered"), 0600))
			badSpec := spec
			badSpec.Env = cluster.Nodes[0].Spec.Env
			badSpec.StdoutPath += ".corrupt"
			badSpec.StderrPath += ".corrupt"
			bad := &suite.NodeProcess{Spec: badSpec, BinaryPath: candidate}
			require.NoError(t, bad.Start())
			_, err = bad.WaitHTTPReady(ctx, badSpec.APIAddr, "/readyz")
			require.Error(t, err)
			require.NoError(t, bad.Stop())
			require.Equal(t, float64(1), marker(t, spec.DataDir)["format_version"])
			require.NoError(t, os.WriteFile(backupMarker, original, 0600))
		}
	}
	start(t, cluster, candidate)
	require.NoError(t, cluster.WaitClusterReady(ctx))
	for i, n := range cluster.Nodes {
		m := marker(t, n.Spec.DataDir)
		require.Equal(t, float64(2), m["format_version"])
		backupIdentity := marker(t, filepath.Join(n.Spec.DataDir, "format-upgrade-v1-v2", "backup", "data"))
		require.Equal(t, backupIdentity["created_by"], m["created_by"])
		require.Equal(t, backupIdentity["created_at"], m["created_at"])
		backup := filepath.Join(n.Spec.DataDir, "format-upgrade-v1-v2", "backup", "data")
		hashes := treeHashes(t, backup)
		for path, hash := range before[i] {
			require.Equal(t, hash, hashes[path], "backup changed: %s", path)
		}
		log, err := os.ReadFile(n.Spec.StdoutPath)
		require.NoError(t, err)
		out.SnapshotInstalls += strings.Count(string(log), "snapshot_installed")
		out.Backups = append(out.Backups, hashes)
	}
	require.Positive(t, out.SnapshotInstalls, "must exercise old Slot snapshot installation")
	verify(t, ctx, cluster, facts, out)
	duplicate := send(t, ctx, addr, "old-0")
	require.Equal(t, facts[0].MessageID, duplicate.MessageID)
	require.Equal(t, facts[0].MessageSeq, duplicate.MessageSeq)
	blocked, err := wkclient.New(wkclient.Config{Addr: cluster.Nodes[1].GatewayAddr(), OperationTimeout: 5 * time.Second})
	require.NoError(t, err)
	_, err = blocked.Connect(ctx, wkclient.ConnectOptions{UID: "blocked", Token: "original-blocked", DeviceID: "blocked-login", DeviceFlag: frame.APP})
	require.NoError(t, err)
	_, err = blocked.Send(ctx, wkclient.Message{ClientMsgNo: "blocked-protocol", ChannelID: "upgrade-group", ChannelType: 2, Payload: []byte("denied")})
	require.ErrorContains(t, err, frame.ReasonSendBan.String())
	require.NoError(t, blocked.Close())
	// A first post-upgrade business connection uses the original credential.
	client, err := wkclient.New(wkclient.Config{Addr: cluster.Nodes[2].GatewayAddr(), OperationTimeout: 5 * time.Second})
	require.NoError(t, err)
	_, err = client.Connect(ctx, wkclient.ConnectOptions{UID: "alice", Token: "original-alice", DeviceID: "retained", DeviceFlag: frame.APP})
	require.NoError(t, err)
	bob, err := wkclient.New(wkclient.Config{Addr: cluster.Nodes[1].GatewayAddr(), OperationTimeout: 5 * time.Second})
	require.NoError(t, err)
	_, err = bob.Connect(ctx, wkclient.ConnectOptions{UID: "bob", Token: "original-bob", DeviceID: "retained-bob", DeviceFlag: frame.APP})
	require.NoError(t, err)
	defer bob.Close()
	ack, err := client.Send(ctx, wkclient.Message{ClientMsgNo: "new-protocol", ChannelID: "upgrade-group", ChannelType: 2, Payload: []byte("new-protocol")})
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, ack.ReasonCode)
	packet, err := bob.Recv(ctx)
	require.NoError(t, err)
	require.Equal(t, ack.MessageID, packet.MessageID)
	require.Equal(t, ack.MessageSeq, packet.MessageSeq)
	require.Equal(t, []byte("new-protocol"), packet.Payload)
	require.NoError(t, bob.Close())
	require.NoError(t, client.Close())
	facts = append(facts, suite.MessageSendResponse{MessageID: ack.MessageID, MessageSeq: ack.MessageSeq})
	_, err = suite.PostJSON(ctx, "http://"+addr+"/user/token", map[string]any{"uid": "not-yet-registered", "token": "new-token", "device_flag": 0, "device_level": 0}, nil)
	require.NoError(t, err)
	out.Phase = "restart"
	stop(t, cluster)
	start(t, cluster, candidate)
	require.NoError(t, cluster.WaitClusterReady(ctx))
	verify(t, ctx, cluster, facts, out)
	facts = append(facts, send(t, ctx, cluster.Nodes[1].APIAddr(), "after-restart"))
	verify(t, ctx, cluster, facts, out)
	out.Phase = "leader-failover"
	runtime := suite.RequireChannelRuntimeMetaEventually(t, cluster, &cluster.Nodes[0], "upgrade-group", 2, 20*time.Second)
	out.LeaderBefore = runtime.Leader
	failed := cluster.MustNode(runtime.Leader)
	require.NoError(t, failed.Stop())
	live := cluster.MustNode(runtime.Leader%3 + 1)
	require.Eventually(t, func() bool {
		m, e := suite.GetChannelRuntimeMeta(ctx, live, "upgrade-group", 2)
		if e == nil && m.Status == "active" && m.Leader != 0 && m.Leader != out.LeaderBefore {
			out.LeaderAfter = m.Leader
			return true
		}
		return false
	}, 40*time.Second, 100*time.Millisecond)
	facts = append(facts, send(t, ctx, live.APIAddr(), "after-failover"))
	failed.Process = &suite.NodeProcess{Spec: failed.Spec, BinaryPath: candidate}
	require.NoError(t, failed.Process.Start())
	require.NoError(t, cluster.WaitClusterReady(ctx))
	verify(t, ctx, cluster, facts, out)
	out.Phase = "backup-rollback"
	stop(t, cluster)
	// Restore the entire cold generation, including Controller and Raft history.
	for _, n := range cluster.Nodes {
		backup := filepath.Join(n.Spec.DataDir, "format-upgrade-v1-v2", "backup", "data")
		restored := n.Spec.DataDir + "-restored"
		copyTree(t, backup, restored)
		require.NoError(t, os.Rename(n.Spec.DataDir, n.Spec.DataDir+"-upgraded"))
		require.NoError(t, os.Rename(restored, n.Spec.DataDir))
	}
	start(t, cluster, old)
	require.NoError(t, cluster.WaitClusterReady(ctx))
	verifyHistory(t, ctx, cluster, facts[:8], out)
	out.Phase = "complete"
}

func start(t *testing.T, c *suite.StartedCluster, binary string) {
	for i := range c.Nodes {
		p := &suite.NodeProcess{Spec: c.Nodes[i].Spec, BinaryPath: binary}
		c.Nodes[i].Process = p
		require.NoError(t, p.Start())
	}
}
func stop(t *testing.T, c *suite.StartedCluster) {
	for i := range c.Nodes {
		require.NoError(t, c.Nodes[i].Stop())
	}
}
func send(t *testing.T, ctx context.Context, addr, key string) suite.MessageSendResponse {
	r, e := suite.PostMessageSendEventually(ctx, addr, map[string]any{"from_uid": "alice", "channel_id": "upgrade-group", "channel_type": 2, "client_msg_no": key, "payload": []byte(key)})
	require.NoError(t, e)
	require.NotZero(t, r.MessageID)
	return r
}
func verify(t *testing.T, ctx context.Context, c *suite.StartedCluster, facts []suite.MessageSendResponse, out *receipt) {
	verifyHistory(t, ctx, c, facts, out)
	for _, n := range c.Nodes {
		for _, uid := range []string{"blocked", "not-yet-registered"} {
			p, e := suite.GetUserSendBan(ctx, n.APIAddr(), uid)
			require.NoError(t, e)
			require.Equal(t, 1, p.Value)
		}
	}
	rGroup, eGroup := suite.PostMessageSend(ctx, c.Nodes[0].APIAddr(), map[string]any{"from_uid": "alice", "channel_id": "banned-group", "channel_type": 2, "payload": []byte("denied-group")})
	require.NoError(t, eGroup)
	require.Equal(t, uint8(frame.ReasonSendBan), rGroup.Reason)
	r, e := suite.PostMessageSend(ctx, c.Nodes[0].APIAddr(), map[string]any{"from_uid": "blocked", "channel_id": "upgrade-group", "channel_type": 2, "payload": []byte("denied")})
	require.NoError(t, e)
	require.Equal(t, uint8(frame.ReasonSendBan), r.Reason)
}
func verifyHistory(t *testing.T, ctx context.Context, c *suite.StartedCluster, facts []suite.MessageSendResponse, out *receipt) {
	for _, n := range c.Nodes {
		var page struct {
			Messages []struct {
				ID      string `json:"message_idstr"`
				Seq     uint64 `json:"message_seq"`
				Payload []byte `json:"payload"`
			} `json:"messages"`
		}
		_, err := suite.PostJSON(ctx, "http://"+n.APIAddr()+"/channel/messagesync", map[string]any{"login_uid": "alice", "channel_id": "upgrade-group", "channel_type": 2, "start_message_seq": 0, "end_message_seq": 0, "limit": 100, "pull_mode": 0}, &page)
		require.NoError(t, err)
		require.Len(t, page.Messages, len(facts))
		for i, f := range facts {
			require.Equal(t, fmt.Sprint(f.MessageID), page.Messages[i].ID)
			require.Equal(t, f.MessageSeq, page.Messages[i].Seq)
			expected := fmt.Sprintf("old-%d", i)
			if i == 8 {
				expected = "new-protocol"
			}
			if i == 9 {
				expected = "after-restart"
			}
			if i == 10 {
				expected = "after-failover"
			}
			require.Equal(t, []byte(expected), page.Messages[i].Payload)
		}
		out.HistoryChecks++
	}
}
func marker(t *testing.T, dir string) map[string]any {
	b, e := os.ReadFile(filepath.Join(dir, "DATA-FORMAT.json"))
	require.NoError(t, e)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}
func digest(t *testing.T, path string) string {
	f, e := os.Open(path)
	require.NoError(t, e)
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	require.NoError(t, e)
	return hex.EncodeToString(h.Sum(nil))
}
func treeHashes(t *testing.T, root string) map[string]string {
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		if strings.Contains(rel, "format-upgrade-v1-v2") || d.Name() == ".format-upgrade.lock" {
			return nil
		}
		out[rel] = digest(t, p)
		return nil
	}))
	return out
}
func copyTree(t *testing.T, src, dst string) {
	require.NoError(t, filepath.WalkDir(src, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		in, e := os.Open(p)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.Create(target)
		if e != nil {
			return e
		}
		_, e = io.Copy(out, in)
		return joinClose(e, out)
	}))
}
func joinClose(err error, f *os.File) error {
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
