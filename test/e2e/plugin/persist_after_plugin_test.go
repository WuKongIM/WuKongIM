//go:build e2e

package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	appv2 "github.com/WuKongIM/WuKongIM/internal/app"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	channelv2 "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

const (
	persistAfterPluginNo     = "persistafter"
	persistAfterChannelID    = "room-plugin-persist"
	persistAfterJSONL        = "persist_after.jsonl"
	persistAfterReady        = "persist_after.ready"
	persistAfterPollInterval = 20 * time.Millisecond
	persistAfterBenchPoll    = time.Millisecond
)

func TestPersistAfterPluginReceivesDurableCommittedMessage(t *testing.T) {
	if testing.Short() {
		t.Skip("real .wkp plugin compatibility test skipped in short mode")
	}
	h := startPersistAfterHarness(t)

	payload := "hello persist after"
	result := h.send(t, "test-client-1", []byte(payload))
	require.NotZero(t, result.MessageID)
	require.Equal(t, uint64(1), result.MessageSeq)

	records := h.waitRecords(t, 1)
	require.Equal(t, int64(result.MessageID), records[0].MessageID)
	require.Equal(t, persistAfterChannelID, records[0].ChannelID)
	require.Equal(t, payload, records[0].Payload)
}

func BenchmarkPersistAfterRealWKP(b *testing.B) {
	if testing.Short() {
		b.Skip("real .wkp plugin benchmark skipped in short mode")
	}
	h := startPersistAfterHarness(b)
	payload := []byte(strings.Repeat("x", 1024))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := h.send(b, "bench-client-"+strconv.Itoa(i), payload)
		if result.MessageID == 0 {
			b.Fatalf("message id = 0")
		}
		h.waitRecordCount(b, i+1)
	}
}

// persistAfterHarness owns the real app and plugin sandbox used by the compatibility test.
type persistAfterHarness struct {
	// app is the internal composition root under test.
	app *appv2.App
	// node is the public cluster runtime used for metadata seeding and route checks.
	node *cluster.Node
	// channelID is the deterministic group channel used for all sends.
	channelID channelv2.ChannelID
	// sandboxDir is the configured plugin sandbox root.
	sandboxDir string
	// storeFactory owns the injected ChannelV2 message store.
	storeFactory *channelstore.MessageDBFactory
	// recordPath is the JSONL file written by the real plugin process.
	recordPath string
	// recordOffset tracks the last scanned byte offset for benchmark polling.
	recordOffset int64
	// recordCount tracks the number of records observed from recordOffset scanning.
	recordCount int
}

type persistAfterRecord struct {
	MessageID int64  `json:"message_id"`
	ChannelID string `json:"channel_id"`
	Payload   string `json:"payload"`
}

func startPersistAfterHarness(tb testing.TB) *persistAfterHarness {
	tb.Helper()
	root := tb.TempDir()
	pluginDir := filepath.Join(root, "plugins")
	require.NoError(tb, os.MkdirAll(pluginDir, 0o755))
	buildPersistAfterPlugin(tb, filepath.Join(pluginDir, persistAfterPluginNo+".wkp"))

	nodeID := uint64(1)
	clusterAddr := freeTCPAddr(tb)
	channelID := channelv2.ChannelID{ID: persistAfterChannelID, Type: frame.ChannelTypeGroup}
	storeFactory := channelstore.NewMessageDBFactory(filepath.Join(root, "channellog"))
	tb.Cleanup(func() { require.NoError(tb, storeFactory.Close()) })
	channelSvc, err := channels.NewService(channels.Config{
		LocalNode:    channelv2.NodeID(nodeID),
		ReactorCount: 1,
		MailboxSize:  16,
		Store:        storeFactory,
		MetaSource: channels.NewStaticMetaSource([]channelv2.Meta{{
			Key:         channelv2.ChannelKeyForID(channelID),
			ID:          channelID,
			Epoch:       1,
			LeaderEpoch: 1,
			Leader:      channelv2.NodeID(nodeID),
			Replicas:    []channelv2.NodeID{channelv2.NodeID(nodeID)},
			ISR:         []channelv2.NodeID{channelv2.NodeID(nodeID)},
			MinISR:      1,
			Status:      channelv2.StatusActive,
		}}),
	})
	require.NoError(tb, err)
	clusterCfg := cluster.Config{
		NodeID:     nodeID,
		ListenAddr: clusterAddr,
		DataDir:    filepath.Join(root, "cluster"),
		Control: cluster.ControlConfig{
			ClusterID:      "internal-plugin-persist-after",
			Voters:         []cluster.ControlVoter{{NodeID: nodeID, Addr: clusterAddr}},
			AllowBootstrap: true,
		},
		Slots: cluster.SlotConfig{
			InitialSlotCount: 1,
			HashSlotCount:    4,
			ReplicaCount:     1,
		},
		Channel: cluster.ChannelConfig{TickInterval: time.Millisecond},
	}
	node, err := cluster.New(clusterCfg, cluster.WithChannels(channelSvc))
	require.NoError(tb, err)

	cfg := appv2.Config{
		NodeID:  nodeID,
		DataDir: filepath.Join(root, "app"),
		Cluster: clusterCfg,
		Gateway: appv2.GatewayConfig{SendTimeout: time.Second},
		Plugin: appv2.PluginConfig{
			Enable:                true,
			Dir:                   pluginDir,
			SocketPath:            shortPluginSocketPath(tb),
			SandboxDir:            filepath.Join(root, "plugin-sandbox"),
			StateDir:              filepath.Join(root, "plugin-state"),
			Timeout:               5 * time.Second,
			HotReload:             false,
			PersistAfterQueueSize: 64,
			PersistAfterWorkers:   1,
		},
	}
	cfg.Plugin.SetExplicitFlags(true)

	app, err := appv2.New(cfg, appv2.WithCluster(node))
	require.NoError(tb, err)
	started := false
	tb.Cleanup(func() {
		if !started {
			return
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		require.NoError(tb, app.Stop(stopCtx))
	})
	h := &persistAfterHarness{
		app:          app,
		node:         node,
		channelID:    channelID,
		sandboxDir:   cfg.Plugin.SandboxDir,
		storeFactory: storeFactory,
		recordPath:   filepath.Join(cfg.Plugin.SandboxDir, persistAfterPluginNo, persistAfterJSONL),
	}
	startCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(tb, app.Start(startCtx))
	started = true

	h.waitRouteLeader(tb)
	h.seedGroupSendPermission(tb, "u1")
	h.waitPluginReady(tb)
	return h
}

func buildPersistAfterPlugin(tb testing.TB, outputPath string) {
	tb.Helper()
	cmd := exec.Command("go", "build", "-o", outputPath, "./test/e2e/plugin/testdata/persistafter")
	cmd.Dir = repoRoot(tb)
	cmd.Env = goBuildEnv(tb)
	output, err := cmd.CombinedOutput()
	require.NoErrorf(tb, err, "go build persistafter plugin:\n%s", output)
}

func goBuildEnv(tb testing.TB) []string {
	tb.Helper()
	env := append(os.Environ(), "GOWORK=off")
	if os.Getenv("GOCACHE") == "" {
		env = append(env, "GOCACHE="+filepath.Join(tb.TempDir(), "gocache"))
	}
	return env
}

func repoRoot(tb testing.TB) string {
	tb.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(tb, ok)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func (h *persistAfterHarness) send(tb testing.TB, clientMsgNo string, payload []byte) message.SendResult {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := h.app.Messages().Send(ctx, message.SendCommand{
		FromUID:     "u1",
		ChannelID:   h.channelID.ID,
		ChannelType: h.channelID.Type,
		ClientMsgNo: clientMsgNo,
		Payload:     append([]byte(nil), payload...),
	})
	require.NoError(tb, err)
	require.Equal(tb, message.ReasonSuccess, result.Reason)
	return result
}

func (h *persistAfterHarness) waitRouteLeader(tb testing.TB) {
	tb.Helper()
	require.Eventually(tb, func() bool {
		route, err := h.node.RouteKey(h.channelID.ID)
		return err == nil && route.Leader == h.node.NodeID()
	}, 3*time.Second, time.Millisecond)
}

func (h *persistAfterHarness) seedGroupSendPermission(tb testing.TB, uid string) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(tb, h.node.UpsertChannelMetadata(ctx, metadb.Channel{
		ChannelID:   h.channelID.ID,
		ChannelType: int64(h.channelID.Type),
	}))
	require.NoError(tb, h.node.AddChannelSubscribers(ctx, h.channelID.ID, int64(h.channelID.Type), []string{uid}, 1))
	require.Eventually(tb, func() bool {
		ok, err := h.node.ContainsChannelSubscriber(context.Background(), h.channelID.ID, int64(h.channelID.Type), uid)
		return err == nil && ok
	}, 3*time.Second, time.Millisecond)
}

func (h *persistAfterHarness) waitPluginReady(tb testing.TB) {
	tb.Helper()
	readyPath := filepath.Join(h.sandboxDir, persistAfterPluginNo, persistAfterReady)
	require.Eventuallyf(tb, func() bool {
		_, err := os.Stat(readyPath)
		return err == nil
	}, 3*time.Second, persistAfterPollInterval, "plugin ready marker %s was not written", readyPath)
}

func (h *persistAfterHarness) waitRecords(tb testing.TB, want int) []persistAfterRecord {
	tb.Helper()
	var records []persistAfterRecord
	var lastErr error
	require.Eventuallyf(tb, func() bool {
		var err error
		records, _, err = h.readRecordsFromOffset(0)
		if err != nil {
			lastErr = err
			return false
		}
		return len(records) >= want
	}, 3*time.Second, persistAfterPollInterval, "persist after records path=%s got=%d want=%d lastErr=%v", h.recordPath, len(records), want, lastErr)
	return records
}

func (h *persistAfterHarness) waitRecordCount(tb testing.TB, want int) {
	tb.Helper()
	var lastErr error
	require.Eventuallyf(tb, func() bool {
		records, nextOffset, err := h.readRecordsFromOffset(h.recordOffset)
		if err != nil {
			lastErr = err
			return false
		}
		h.recordOffset = nextOffset
		h.recordCount += len(records)
		return h.recordCount >= want
	}, 3*time.Second, persistAfterBenchPoll, "persist after records path=%s got=%d want=%d lastErr=%v", h.recordPath, h.recordCount, want, lastErr)
}

func (h *persistAfterHarness) readRecordsFromOffset(offset int64) ([]persistAfterRecord, int64, error) {
	file, err := os.Open(h.recordPath)
	if err != nil {
		return nil, offset, err
	}
	defer file.Close()
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return nil, offset, err
		}
	}
	var records []persistAfterRecord
	reader := bufio.NewReader(file)
	nextOffset := offset
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			nextOffset += int64(len(line))
			line = bytes.TrimSpace(line)
		}
		if len(line) == 0 {
			if err == io.EOF {
				return records, nextOffset, nil
			}
			if err != nil {
				return nil, nextOffset, err
			}
			continue
		}
		var record persistAfterRecord
		if unmarshalErr := json.Unmarshal(line, &record); unmarshalErr != nil {
			return nil, nextOffset, fmt.Errorf("decode %s at offset %d: %w", h.recordPath, nextOffset-int64(len(line)), unmarshalErr)
		}
		records = append(records, record)
		if err == io.EOF {
			return records, nextOffset, nil
		}
		if err != nil {
			return nil, nextOffset, err
		}
	}
}

func freeTCPAddr(tb testing.TB) string {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(tb, err)
	defer ln.Close()
	return ln.Addr().String()
}

func shortPluginSocketPath(tb testing.TB) string {
	tb.Helper()
	dir, err := os.MkdirTemp("/tmp", "wk-v2-plugin-")
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "plugin.sock")
}
