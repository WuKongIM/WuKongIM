//go:build e2e

package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	appv2 "github.com/WuKongIM/WuKongIM/internalv2/app"
	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/require"
)

const (
	receiveHookPluginNo     = "receivehook"
	receiveHookChannelID    = "room-plugin-receive"
	receiveHookBotUID       = "receive-bot"
	receiveHookJSONL        = "receive_hook.jsonl"
	receiveHookReady        = "receive_hook.ready"
	receiveHookPollInterval = 20 * time.Millisecond
)

func TestReceivePluginReceivesDurableOfflineRecipient(t *testing.T) {
	if testing.Short() {
		t.Skip("real .wkp plugin compatibility test skipped in short mode")
	}
	h := startReceiveHookHarness(t)

	payload := "hello receive hook"
	result := h.send(t, "receive-client-1", []byte(payload))
	require.NotZero(t, result.MessageID)
	require.Equal(t, uint64(1), result.MessageSeq)

	records := h.waitRecords(t, 1)
	require.Equal(t, "u1", records[0].FromUID)
	require.Equal(t, "receive-bot", records[0].ToUID)
	require.Equal(t, receiveHookChannelID, records[0].ChannelID)
	require.Equal(t, uint32(frame.ChannelTypeGroup), records[0].ChannelType)
	require.Equal(t, payload, records[0].Payload)
}

// receiveHookHarness owns the real app and plugin sandbox used by the Receive compatibility test.
type receiveHookHarness struct {
	// app is the internalv2 composition root under test.
	app *appv2.App
	// node is the public clusterv2 runtime used for metadata seeding and route checks.
	node *clusterv2.Node
	// channelID is the deterministic group channel used for all sends.
	channelID channelv2.ChannelID
	// sandboxDir is the configured plugin sandbox root.
	sandboxDir string
	// managerAddr is the public manager HTTP address used for binding mutation.
	managerAddr string
	// storeFactory owns the injected ChannelV2 message store.
	storeFactory *channelstore.MessageDBFactory
	// recordPath is the JSONL file written by the real plugin process.
	recordPath string
}

type receiveHookRecord struct {
	FromUID     string `json:"from_uid"`
	ToUID       string `json:"to_uid"`
	ChannelID   string `json:"channel_id"`
	ChannelType uint32 `json:"channel_type"`
	Payload     string `json:"payload"`
}

func startReceiveHookHarness(tb testing.TB) *receiveHookHarness {
	tb.Helper()
	root := tb.TempDir()
	pluginDir := filepath.Join(root, "plugins")
	require.NoError(tb, os.MkdirAll(pluginDir, 0o755))
	buildReceiveHookPlugin(tb, filepath.Join(pluginDir, receiveHookPluginNo+".wkp"))

	nodeID := uint64(1)
	clusterAddr := freeTCPAddr(tb)
	channelID := channelv2.ChannelID{ID: receiveHookChannelID, Type: frame.ChannelTypeGroup}
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
	clusterCfg := clusterv2.Config{
		NodeID:     nodeID,
		ListenAddr: clusterAddr,
		DataDir:    filepath.Join(root, "cluster"),
		Control: clusterv2.ControlConfig{
			ClusterID:      "internalv2-plugin-receive",
			Voters:         []clusterv2.ControlVoter{{NodeID: nodeID, Addr: clusterAddr}},
			AllowBootstrap: true,
		},
		Slots: clusterv2.SlotConfig{
			InitialSlotCount: 1,
			HashSlotCount:    4,
			ReplicaCount:     1,
		},
		Channel: clusterv2.ChannelConfig{TickInterval: time.Millisecond},
	}
	node, err := clusterv2.New(clusterCfg, clusterv2.WithChannels(channelSvc))
	require.NoError(tb, err)

	cfg := appv2.Config{
		NodeID:  nodeID,
		DataDir: filepath.Join(root, "app"),
		Cluster: clusterCfg,
		Gateway: appv2.GatewayConfig{SendTimeout: time.Second},
		Manager: appv2.ManagerConfig{
			ListenAddr: freeTCPAddr(tb),
		},
		Delivery: appv2.DeliveryConfig{
			Enabled:        true,
			EventQueueSize: 64,
			FanoutPageSize: 64,
			PushBatchSize:  64,
		},
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
	h := &receiveHookHarness{
		app:          app,
		node:         node,
		channelID:    channelID,
		sandboxDir:   cfg.Plugin.SandboxDir,
		managerAddr:  cfg.Manager.ListenAddr,
		storeFactory: storeFactory,
		recordPath:   filepath.Join(cfg.Plugin.SandboxDir, receiveHookPluginNo, receiveHookJSONL),
	}
	startCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(tb, app.Start(startCtx))
	started = true

	h.waitRouteLeader(tb)
	h.seedGroupSendPermission(tb, "u1", receiveHookBotUID)
	h.bindReceivePluginThroughManager(tb)
	h.waitPluginReady(tb)
	return h
}

func buildReceiveHookPlugin(tb testing.TB, outputPath string) {
	tb.Helper()
	cmd := exec.Command("go", "build", "-o", outputPath, "./test/e2ev2/plugin/testdata/receivehook")
	cmd.Dir = repoRoot(tb)
	cmd.Env = goBuildEnv(tb)
	output, err := cmd.CombinedOutput()
	require.NoErrorf(tb, err, "go build receivehook plugin:\n%s", output)
}

func (h *receiveHookHarness) send(tb testing.TB, clientMsgNo string, payload []byte) message.SendResult {
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

func (h *receiveHookHarness) waitRouteLeader(tb testing.TB) {
	tb.Helper()
	require.Eventually(tb, func() bool {
		route, err := h.node.RouteKey(h.channelID.ID)
		return err == nil && route.Leader == h.node.NodeID()
	}, 3*time.Second, time.Millisecond)
}

func (h *receiveHookHarness) seedGroupSendPermission(tb testing.TB, uids ...string) {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(tb, h.node.UpsertChannelMetadata(ctx, metadb.Channel{
		ChannelID:   h.channelID.ID,
		ChannelType: int64(h.channelID.Type),
	}))
	require.NoError(tb, h.node.AddChannelSubscribers(ctx, h.channelID.ID, int64(h.channelID.Type), uids, 1))
	for _, uid := range uids {
		require.Eventually(tb, func() bool {
			ok, err := h.node.ContainsChannelSubscriber(context.Background(), h.channelID.ID, int64(h.channelID.Type), uid)
			return err == nil && ok
		}, 3*time.Second, time.Millisecond)
	}
}

func (h *receiveHookHarness) bindReceivePluginThroughManager(tb testing.TB) {
	tb.Helper()
	body := bytes.NewBufferString(`{"uid":"` + receiveHookBotUID + `","plugin_no":"` + receiveHookPluginNo + `"}`)
	req, err := http.NewRequest(http.MethodPost, "http://"+h.managerAddr+"/manager/plugin-bindings", body)
	require.NoError(tb, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(tb, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(tb, err)
	require.Equalf(tb, http.StatusOK, resp.StatusCode, "manager binding response: %s", respBody)
	require.Eventually(tb, func() bool {
		bindings, err := h.node.ListPluginBindingsByUID(context.Background(), receiveHookBotUID)
		return err == nil && len(bindings) == 1 && bindings[0].PluginNo == receiveHookPluginNo
	}, 3*time.Second, time.Millisecond)
}

func (h *receiveHookHarness) waitPluginReady(tb testing.TB) {
	tb.Helper()
	readyPath := filepath.Join(h.sandboxDir, receiveHookPluginNo, receiveHookReady)
	require.Eventuallyf(tb, func() bool {
		_, err := os.Stat(readyPath)
		return err == nil
	}, 3*time.Second, receiveHookPollInterval, "plugin ready marker %s was not written", readyPath)
}

func (h *receiveHookHarness) waitRecords(tb testing.TB, want int) []receiveHookRecord {
	tb.Helper()
	var records []receiveHookRecord
	var lastErr error
	require.Eventuallyf(tb, func() bool {
		var err error
		records, err = readReceiveHookRecords(h.recordPath)
		if err != nil {
			lastErr = err
			return false
		}
		return len(records) >= want
	}, 3*time.Second, receiveHookPollInterval, "receive hook records path=%s got=%d want=%d lastErr=%v", h.recordPath, len(records), want, lastErr)
	return records
}

func readReceiveHookRecords(path string) ([]receiveHookRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var records []receiveHookRecord
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = bytes.TrimSpace(line)
		}
		if len(line) == 0 {
			if err == io.EOF {
				return records, nil
			}
			if err != nil {
				return nil, err
			}
			continue
		}
		var record receiveHookRecord
		if unmarshalErr := json.Unmarshal(line, &record); unmarshalErr != nil {
			return nil, fmt.Errorf("decode %s: %w", path, unmarshalErr)
		}
		records = append(records, record)
		if err == io.EOF {
			return records, nil
		}
		if err != nil {
			return nil, err
		}
	}
}
