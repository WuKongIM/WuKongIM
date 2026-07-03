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

	appv2 "github.com/WuKongIM/WuKongIM/internal/app"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/stretchr/testify/require"
)

const (
	lifecyclePluginNo     = "lifecycle"
	lifecycleJSONL        = "lifecycle.jsonl"
	lifecycleReady        = "lifecycle.ready"
	lifecyclePollInterval = 20 * time.Millisecond
)

func TestPluginLifecycleManagerConfigRestartAndUninstall(t *testing.T) {
	if testing.Short() {
		t.Skip("real .wkp plugin lifecycle test skipped in short mode")
	}
	h := startLifecycleHarness(t)

	h.updateConfig(t, `{"api_key":"real-secret","mode":"fast"}`)
	updates := h.waitRecords(t, func(records []lifecycleRecord) bool {
		return lifecycleEventCount(records, "config_update") >= 1
	})
	firstUpdate := lifecycleLastEvent(updates, "config_update")
	require.Equal(t, "real-secret", firstUpdate.Config["api_key"])
	require.Equal(t, "fast", firstUpdate.Config["mode"])

	detail := h.pluginDetail(t)
	require.Equal(t, "running", detail.Status)
	require.True(t, detail.Enabled)
	require.Equal(t, "******", detail.Config["api_key"])
	require.Equal(t, "fast", detail.Config["mode"])
	require.True(t, lifecycleTemplateHasField(detail.ConfigTemplate, "api_key", "secret"))

	h.updateConfig(t, `{"api_key":"******","mode":"slow"}`)
	updates = h.waitRecords(t, func(records []lifecycleRecord) bool {
		return lifecycleEventCount(records, "config_update") >= 2
	})
	secondUpdate := lifecycleLastEvent(updates, "config_update")
	require.Equal(t, "real-secret", secondUpdate.Config["api_key"])
	require.Equal(t, "slow", secondUpdate.Config["mode"])

	h.restart(t)
	records := h.waitRecords(t, func(records []lifecycleRecord) bool {
		return lifecycleEventCount(records, "startup") >= 2
	})
	restarted := lifecycleLastEvent(records, "startup")
	require.Equal(t, "real-secret", restarted.Config["api_key"])
	require.Equal(t, "slow", restarted.Config["mode"])

	h.uninstall(t)
	require.Eventually(t, func() bool {
		detail := h.pluginDetail(t)
		return detail.Status == "disabled" && !detail.Enabled
	}, 3*time.Second, lifecyclePollInterval)
	_, err := os.Stat(h.pluginPath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

type lifecycleHarness struct {
	app         *appv2.App
	node        *cluster.Node
	managerAddr string
	pluginPath  string
	recordPath  string
}

type lifecycleRecord struct {
	Event  string         `json:"event"`
	PID    int            `json:"pid"`
	Config map[string]any `json:"config,omitempty"`
}

type lifecyclePluginDetail struct {
	NodeID         uint64                  `json:"node_id"`
	PluginNo       string                  `json:"plugin_no"`
	Status         string                  `json:"status"`
	Enabled        bool                    `json:"enabled"`
	Config         map[string]any          `json:"config"`
	ConfigTemplate lifecycleConfigTemplate `json:"config_template"`
}

type lifecycleConfigTemplate struct {
	Fields []lifecycleConfigField `json:"fields"`
}

type lifecycleConfigField struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Label string `json:"label"`
}

func startLifecycleHarness(tb testing.TB) *lifecycleHarness {
	tb.Helper()
	root := tb.TempDir()
	pluginDir := filepath.Join(root, "plugins")
	require.NoError(tb, os.MkdirAll(pluginDir, 0o755))
	pluginPath := filepath.Join(pluginDir, lifecyclePluginNo+".wkp")
	buildLifecyclePlugin(tb, pluginPath)

	nodeID := uint64(1)
	clusterAddr := freeTCPAddr(tb)
	clusterCfg := cluster.Config{
		NodeID:     nodeID,
		ListenAddr: clusterAddr,
		DataDir:    filepath.Join(root, "cluster"),
		Control: cluster.ControlConfig{
			ClusterID:      "internalv2-plugin-lifecycle",
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
	node, err := cluster.New(clusterCfg)
	require.NoError(tb, err)

	cfg := appv2.Config{
		NodeID:  nodeID,
		DataDir: filepath.Join(root, "app"),
		Cluster: clusterCfg,
		Gateway: appv2.GatewayConfig{SendTimeout: time.Second},
		Manager: appv2.ManagerConfig{
			ListenAddr: freeTCPAddr(tb),
		},
		Plugin: appv2.PluginConfig{
			Enable:                true,
			Dir:                   pluginDir,
			SocketPath:            shortPluginSocketPath(tb),
			SandboxDir:            filepath.Join(root, "plugin-sandbox"),
			StateDir:              filepath.Join(root, "plugin-state"),
			Timeout:               5 * time.Second,
			HotReload:             false,
			PersistAfterQueueSize: 16,
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
	h := &lifecycleHarness{
		app:         app,
		node:        node,
		managerAddr: cfg.Manager.ListenAddr,
		pluginPath:  pluginPath,
		recordPath:  filepath.Join(cfg.Plugin.SandboxDir, lifecyclePluginNo, lifecycleJSONL),
	}
	startCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(tb, app.Start(startCtx))
	started = true
	h.waitPluginReady(tb)
	return h
}

func buildLifecyclePlugin(tb testing.TB, outputPath string) {
	tb.Helper()
	cmd := exec.Command("go", "build", "-o", outputPath, "./test/e2ev2/plugin/testdata/lifecycle")
	cmd.Dir = repoRoot(tb)
	cmd.Env = goBuildEnv(tb)
	output, err := cmd.CombinedOutput()
	require.NoErrorf(tb, err, "go build lifecycle plugin:\n%s", output)
}

func (h *lifecycleHarness) waitPluginReady(tb testing.TB) {
	tb.Helper()
	readyPath := filepath.Join(filepath.Dir(h.recordPath), lifecycleReady)
	require.Eventuallyf(tb, func() bool {
		_, err := os.Stat(readyPath)
		return err == nil
	}, 3*time.Second, lifecyclePollInterval, "plugin ready marker %s was not written", readyPath)
}

func (h *lifecycleHarness) updateConfig(tb testing.TB, body string) {
	tb.Helper()
	req, err := http.NewRequest(http.MethodPut, "http://"+h.managerAddr+"/manager/nodes/1/plugins/"+lifecyclePluginNo+"/config", bytes.NewBufferString(body))
	require.NoError(tb, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(tb, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(tb, err)
	require.Equalf(tb, http.StatusOK, resp.StatusCode, "manager config update response: %s", respBody)
}

func (h *lifecycleHarness) restart(tb testing.TB) {
	tb.Helper()
	resp, err := http.Post("http://"+h.managerAddr+"/manager/nodes/1/plugins/"+lifecyclePluginNo+"/restart", "application/json", nil)
	require.NoError(tb, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(tb, err)
	require.Equalf(tb, http.StatusOK, resp.StatusCode, "manager restart response: %s", respBody)
}

func (h *lifecycleHarness) uninstall(tb testing.TB) {
	tb.Helper()
	req, err := http.NewRequest(http.MethodDelete, "http://"+h.managerAddr+"/manager/nodes/1/plugins/"+lifecyclePluginNo, nil)
	require.NoError(tb, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(tb, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(tb, err)
	require.Equalf(tb, http.StatusOK, resp.StatusCode, "manager uninstall response: %s", respBody)
}

func (h *lifecycleHarness) pluginDetail(tb testing.TB) lifecyclePluginDetail {
	tb.Helper()
	resp, err := http.Get("http://" + h.managerAddr + "/manager/nodes/1/plugins/" + lifecyclePluginNo)
	require.NoError(tb, err)
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(tb, err)
	require.Equalf(tb, http.StatusOK, resp.StatusCode, "manager detail response: %s", respBody)
	var detail lifecyclePluginDetail
	require.NoError(tb, json.Unmarshal(respBody, &detail))
	return detail
}

func (h *lifecycleHarness) waitRecords(tb testing.TB, accept func([]lifecycleRecord) bool) []lifecycleRecord {
	tb.Helper()
	var records []lifecycleRecord
	var lastErr error
	require.Eventuallyf(tb, func() bool {
		var err error
		records, err = readLifecycleRecords(h.recordPath)
		if err != nil {
			lastErr = err
			return false
		}
		return accept(records)
	}, 5*time.Second, lifecyclePollInterval, "lifecycle records path=%s records=%#v lastErr=%v", h.recordPath, records, lastErr)
	return records
}

func readLifecycleRecords(path string) ([]lifecycleRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var records []lifecycleRecord
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
		var record lifecycleRecord
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

func lifecycleEventCount(records []lifecycleRecord, event string) int {
	count := 0
	for _, record := range records {
		if record.Event == event {
			count++
		}
	}
	return count
}

func lifecycleLastEvent(records []lifecycleRecord, event string) lifecycleRecord {
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Event == event {
			return records[i]
		}
	}
	return lifecycleRecord{}
}

func lifecycleTemplateHasField(template lifecycleConfigTemplate, name, typ string) bool {
	for _, field := range template.Fields {
		if field.Name == name && field.Type == typ {
			return true
		}
	}
	return false
}
