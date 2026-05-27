# wukongimv2 Entry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a standalone `cmd/wukongimv2` verification entry that starts `internalv2/app` and proves single-node-cluster SEND -> SENDACK without routing the old `cmd/wukongim` through `internalv2`.

**Architecture:** Keep the old and new binaries separate: `cmd/wukongim` continues to compose `internal/app`, while `cmd/wukongimv2` composes `internalv2/app`. Before adding the binary, extend `internalv2/app` smoke coverage so a first send uses the default `pkg/clusterv2` metadata path instead of static channel metadata. Then add minimal config loading, lifecycle wiring, and a single-node black-box e2e.

**Tech Stack:** Go 1.23, `internalv2/app`, `pkg/clusterv2`, `pkg/gateway`, `pkg/gateway/binding`, `pkg/protocol/frame`, `pkg/protocol/codec`, `spf13/viper`, existing `test/e2e/suite` process and WKProto helpers.

---

## Reference Material

- Design spec: `docs/superpowers/specs/2026-05-27-wukongimv2-entry-design.md`
- Prior internalv2 design: `docs/superpowers/specs/2026-05-27-internalv2-sendack-design.md`
- Internalv2 flow: `internalv2/FLOW.md`
- App flow: `internalv2/app/FLOW.md`
- Cluster flow: `pkg/clusterv2/FLOW.md`
- Existing main entry: `cmd/wukongim/main.go`
- Existing config loader patterns: `cmd/wukongim/config.go`
- Existing e2e message rules: `test/e2e/message/AGENTS.md`

Use `GOWORK=off` for Go commands when running from project-local `.worktrees/*`. It is also acceptable on the main checkout.

## File Structure

Create:

- `cmd/wukongimv2/main.go` - thin process entry, flag parsing, signal wait, app lifecycle.
- `cmd/wukongimv2/config.go` - minimal `wukongim.conf` reader and mapper to `internalv2/app.Config`.
- `cmd/wukongimv2/config_test.go` - config loading, env override, default path, invalid config tests.
- `cmd/wukongimv2/main_test.go` - flag registration and injected fake app lifecycle tests.
- `cmd/wukongimv2/README.md` - short purpose and local run notes.
- `test/e2e/message/wukongimv2_single_node_send/AGENTS.md` - scenario-specific e2e guidance.
- `test/e2e/message/wukongimv2_single_node_send/wukongimv2_single_node_send_test.go` - black-box process smoke.

Modify:

- `internalv2/app/sendack_smoke_test.go` - add a default-clusterv2 first-send smoke that does not inject static channel metadata.
- `AGENTS.md` - add `cmd/wukongimv2` and the e2e scenario directory when those files are created.
- `test/e2e/message/AGENTS.md` - add the new scenario and run command.
- `docs/development/PROJECT_KNOWLEDGE.md` - add concise knowledge only if implementation reveals a new operational rule.
- `wukongim.conf.example` - update only if new `WK_` keys are introduced. This plan should avoid new keys.

Do not modify:

- `cmd/wukongim`.
- Existing `internal` packages.
- Production routing in the old app root.

## Task 0: Preflight And Baseline

**Files:**
- Read: `docs/superpowers/specs/2026-05-27-wukongimv2-entry-design.md`
- Read: `internalv2/app/FLOW.md`
- Read: `pkg/clusterv2/FLOW.md`
- Read: `test/e2e/message/AGENTS.md`

- [ ] **Step 1: Check branch state**

Run:

```bash
git status --short
```

Expected: no unrelated dirty files. If unrelated files exist, do not edit or stage them.

- [ ] **Step 2: Run focused baseline**

Run:

```bash
GOWORK=off go test ./internalv2/... -count=1
GOWORK=off go test ./pkg/channelv2 ./pkg/clusterv2 -run 'TestPublicAPICompile|TestNodeInitializesDefaultChannelsWhenOptionMissing|TestNodeAppendChannelDelegatesToService' -count=1
```

Expected: PASS. If this fails, stop and diagnose the existing baseline before adding `cmd/wukongimv2`.

- [ ] **Step 3: Confirm old entry boundary**

Run:

```bash
sed -n '1,120p' cmd/wukongim/main.go
```

Expected: `cmd/wukongim` imports `internal/app`. Do not change this file.

This task is read-only.

## Task 1: Default clusterv2 Metadata SEND Smoke

**Files:**
- Modify: `internalv2/app/sendack_smoke_test.go`
- Test: `internalv2/app/sendack_smoke_test.go`

- [ ] **Step 1: Add the failing default metadata smoke**

Append this test and helper to `internalv2/app/sendack_smoke_test.go`:

```go
func TestSingleNodeClusterFirstSendCreatesChannelMetaAndSendack(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	channelID := channelv2.ChannelID{ID: "room-default-meta", Type: 1}
	app, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	startCtx, startCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer startCancel()
	if err := app.Start(startCtx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})

	first := sendDefaultMetaSmokePacket(t, app, channelID, 1, "client-default-meta-1")
	if first.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("first sendack reason = %v, want %v", first.ReasonCode, frame.ReasonSuccess)
	}
	if first.MessageID != int64(uint64(cfg.NodeID<<48)+1) {
		t.Fatalf("first message id = %d, want %d", first.MessageID, uint64(cfg.NodeID<<48)+1)
	}
	if first.MessageSeq != 1 {
		t.Fatalf("first message seq = %d, want 1", first.MessageSeq)
	}

	second := sendDefaultMetaSmokePacket(t, app, channelID, 2, "client-default-meta-2")
	if second.ReasonCode != frame.ReasonSuccess {
		t.Fatalf("second sendack reason = %v, want %v", second.ReasonCode, frame.ReasonSuccess)
	}
	if second.MessageID != int64(uint64(cfg.NodeID<<48)+2) {
		t.Fatalf("second message id = %d, want %d", second.MessageID, uint64(cfg.NodeID<<48)+2)
	}
	if second.MessageSeq != 2 {
		t.Fatalf("second message seq = %d, want 2", second.MessageSeq)
	}
}

func sendDefaultMetaSmokePacket(t *testing.T, app *App, channelID channelv2.ChannelID, clientSeq uint64, clientMsgNo string) *frame.SendackPacket {
	t.Helper()
	writes := &sendackSmokeSessionWrites{}
	sess := newSendackSmokeSession(writes)
	sess.SetValue(coregateway.SessionValueUID, "u1")
	sess.SetValue(coregateway.SessionValueProtocolVersion, uint8(frame.LatestVersion))
	send := &frame.SendPacket{
		ClientSeq:   clientSeq,
		ClientMsgNo: clientMsgNo,
		ChannelID:   channelID.ID,
		ChannelType: channelID.Type,
		Payload:     []byte("hello through default metadata"),
	}
	if err := app.Handler().OnFrame(coregateway.Context{
		Session:        sess,
		RequestContext: context.Background(),
	}, send); err != nil {
		t.Fatalf("OnFrame() error = %v", err)
	}
	ack := writes.requireOnlySendack(t)
	if ack.ClientSeq != send.ClientSeq || ack.ClientMsgNo != send.ClientMsgNo {
		t.Fatalf("sendack client mapping = seq:%d msgNo:%q, want seq:%d msgNo:%q", ack.ClientSeq, ack.ClientMsgNo, send.ClientSeq, send.ClientMsgNo)
	}
	return ack
}
```

This test must call `New(cfg)` without `WithCluster` and without a static `channels.MetaSource`.

- [ ] **Step 2: Run the smoke**

Run:

```bash
GOWORK=off go test ./internalv2/app -run TestSingleNodeClusterFirstSendCreatesChannelMetaAndSendack -count=1
```

Expected: PASS. If it fails with a clusterv2/channelv2 metadata error, continue to Step 3. If it passes, skip Step 3.

- [ ] **Step 3: Fix only the default metadata path if the smoke fails**

If the smoke fails, inspect the failure and make the smallest fix in one of these areas:

- `pkg/clusterv2/channels/*` if the append path is not ensuring channel metadata.
- `pkg/clusterv2/node_defaults.go` or related default wiring if the default ChannelV2 service is missing a Slot-backed metadata source.
- `internalv2/app/app.go` only if `internalv2` is not using the default cluster appender created by `clusterv2.New`.

Do not inject static metadata into this test. The purpose is to prove the default single-node-cluster metadata path.

- [ ] **Step 4: Run app package tests**

Run:

```bash
GOWORK=off go test ./internalv2/app -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add internalv2/app/sendack_smoke_test.go
git commit -m "test(internalv2): cover default metadata sendack smoke"
```

If Step 3 required production fixes, include the exact fixed files in the same commit.

## Task 2: `cmd/wukongimv2` Config Loader

**Files:**
- Create: `cmd/wukongimv2/config.go`
- Create: `cmd/wukongimv2/config_test.go`
- Test: `cmd/wukongimv2/config_test.go`

- [ ] **Step 1: Create the package directory**

Run:

```bash
mkdir -p cmd/wukongimv2
```

Expected: directory exists.

- [ ] **Step 2: Write config tests first**

Create `cmd/wukongimv2/config_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeConf(t *testing.T, dir, name string, lines ...string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	return path
}

func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(cwd))
	})
}

func TestLoadConfigParsesMinimalSingleNodeCluster(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "node-1")
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+dataDir,
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_CLUSTER_INITIAL_SLOT_COUNT=1",
		"WK_CLUSTER_HASH_SLOT_COUNT=4",
		"WK_CLUSTER_SLOT_REPLICA_N=1",
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"gnet","protocol":"wkproto"}]`,
		"WK_GATEWAY_SEND_TIMEOUT=2s",
	)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cfg.NodeID)
	require.Equal(t, dataDir, cfg.DataDir)
	require.Equal(t, uint64(1), cfg.Cluster.NodeID)
	require.Equal(t, "127.0.0.1:7000", cfg.Cluster.ListenAddr)
	require.Equal(t, dataDir, cfg.Cluster.DataDir)
	require.Equal(t, uint32(1), cfg.Cluster.Slots.InitialSlotCount)
	require.Equal(t, uint16(4), cfg.Cluster.Slots.HashSlotCount)
	require.Equal(t, uint16(1), cfg.Cluster.Slots.ReplicaCount)
	require.Len(t, cfg.Gateway.Listeners, 1)
	require.Equal(t, "127.0.0.1:5100", cfg.Gateway.Listeners[0].Address)
	require.Equal(t, 2*time.Second, cfg.Gateway.SendTimeout)
}

func TestLoadConfigUsesDefaultPathWhenConfigFlagEmpty(t *testing.T) {
	dir := t.TempDir()
	chdirForTest(t, dir)
	dataDir := filepath.Join(dir, "node-1")
	writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+dataDir,
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"gnet","protocol":"wkproto"}]`,
	)

	cfg, err := loadConfig("")
	require.NoError(t, err)
	require.Equal(t, dataDir, cfg.DataDir)
}

func TestLoadConfigEnvironmentOverridesFile(t *testing.T) {
	dir := t.TempDir()
	fileDataDir := filepath.Join(dir, "file-node")
	envDataDir := filepath.Join(dir, "env-node")
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+fileDataDir,
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"gnet","protocol":"wkproto"}]`,
	)
	t.Setenv("WK_NODE_DATA_DIR", envDataDir)

	cfg, err := loadConfig(configPath)
	require.NoError(t, err)
	require.Equal(t, envDataDir, cfg.DataDir)
	require.Equal(t, envDataDir, cfg.Cluster.DataDir)
}

func TestLoadConfigRejectsMissingRequiredKeys(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
	)

	_, err := loadConfig(configPath)
	require.ErrorContains(t, err, "WK_NODE_ID")
}

func TestLoadConfigRejectsInvalidGatewayListenersJSON(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		"WK_GATEWAY_LISTENERS=not-json",
	)

	_, err := loadConfig(configPath)
	require.ErrorContains(t, err, "WK_GATEWAY_LISTENERS")
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./cmd/wukongimv2 -run TestLoadConfig -count=1
```

Expected: FAIL because `loadConfig` is undefined.

- [ ] **Step 4: Add config loader implementation**

Create `cmd/wukongimv2/config.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	internalapp "github.com/WuKongIM/WuKongIM/internalv2/app"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
	"github.com/spf13/viper"
)

var defaultConfigPaths = []string{
	"./wukongim.conf",
	"./conf/wukongim.conf",
	"/etc/wukongim/wukongim.conf",
}

func loadConfig(path string) (internalapp.Config, error) {
	cfgv, foundFile, attemptedPaths, err := readConfig(path)
	if err != nil {
		return internalapp.Config{}, err
	}
	cfg, err := buildAppConfig(cfgv)
	if err != nil {
		if !foundFile {
			return internalapp.Config{}, missingDefaultConfigError(attemptedPaths, err)
		}
		return internalapp.Config{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

func readConfig(path string) (*viper.Viper, bool, []string, error) {
	v := viper.New()
	v.SetConfigType("env")
	v.AutomaticEnv()

	path = strings.TrimSpace(path)
	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, false, nil, fmt.Errorf("load config: read %s: %w", path, err)
		}
		return v, true, nil, nil
	}

	attemptedPaths := append([]string(nil), defaultConfigPaths...)
	for _, candidate := range attemptedPaths {
		if _, err := os.Stat(candidate); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, false, attemptedPaths, fmt.Errorf("load config: stat %s: %w", candidate, err)
		}
		v.SetConfigFile(candidate)
		if err := v.ReadInConfig(); err != nil {
			return nil, false, attemptedPaths, fmt.Errorf("load config: read %s: %w", candidate, err)
		}
		return v, true, attemptedPaths, nil
	}
	return v, false, attemptedPaths, nil
}

func buildAppConfig(v *viper.Viper) (internalapp.Config, error) {
	nodeID, err := parseRequiredUint64(v, "WK_NODE_ID")
	if err != nil {
		return internalapp.Config{}, err
	}
	dataDir, err := requiredString(v, "WK_NODE_DATA_DIR")
	if err != nil {
		return internalapp.Config{}, err
	}
	listenAddr, err := requiredString(v, "WK_CLUSTER_LISTEN_ADDR")
	if err != nil {
		return internalapp.Config{}, err
	}
	initialSlotCount, err := parseUint32(v, "WK_CLUSTER_INITIAL_SLOT_COUNT")
	if err != nil {
		return internalapp.Config{}, err
	}
	hashSlotCount, err := parseUint16(v, "WK_CLUSTER_HASH_SLOT_COUNT")
	if err != nil {
		return internalapp.Config{}, err
	}
	slotReplicaN, err := parseUint16(v, "WK_CLUSTER_SLOT_REPLICA_N")
	if err != nil {
		return internalapp.Config{}, err
	}
	listeners, err := parseListeners(v)
	if err != nil {
		return internalapp.Config{}, err
	}
	sendTimeout, err := parseDuration(v, "WK_GATEWAY_SEND_TIMEOUT")
	if err != nil {
		return internalapp.Config{}, err
	}

	cfg := internalapp.Config{
		NodeID:  nodeID,
		DataDir: dataDir,
		Cluster: clusterv2.Config{
			NodeID:     nodeID,
			ListenAddr: listenAddr,
			DataDir:    dataDir,
			Slots: clusterv2.SlotConfig{
				InitialSlotCount: initialSlotCount,
				HashSlotCount:    hashSlotCount,
				ReplicaCount:     slotReplicaN,
			},
		},
		Gateway: internalapp.GatewayConfig{
			Listeners:   listeners,
			SendTimeout: sendTimeout,
		},
	}
	return cfg, nil
}

func defaultGatewayListeners() []gateway.ListenerOptions {
	return []gateway.ListenerOptions{
		binding.TCPWKProto("tcp-wkproto", "0.0.0.0:5100"),
		binding.WSMux("ws-gateway", "0.0.0.0:5200"),
	}
}

func parseListeners(v *viper.Viper) ([]gateway.ListenerOptions, error) {
	raw := stringValue(v, "WK_GATEWAY_LISTENERS")
	if raw == "" {
		return defaultGatewayListeners(), nil
	}
	var listeners []gateway.ListenerOptions
	if err := jsonUnmarshalString(raw, &listeners); err != nil {
		return nil, fmt.Errorf("parse WK_GATEWAY_LISTENERS as JSON: %w", err)
	}
	return listeners, nil
}

func parseRequiredUint64(v *viper.Viper, key string) (uint64, error) {
	value, err := parseUint64(v, key)
	if err != nil {
		return 0, err
	}
	if value == 0 {
		return 0, fmt.Errorf("load config: %s is required", key)
	}
	return value, nil
}

func requiredString(v *viper.Viper, key string) (string, error) {
	value := strings.TrimSpace(stringValue(v, key))
	if value == "" {
		return "", fmt.Errorf("load config: %s is required", key)
	}
	return value, nil
}

func parseUint64(v *viper.Viper, key string) (uint64, error) {
	raw := strings.TrimSpace(stringValue(v, key))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func parseUint32(v *viper.Viper, key string) (uint32, error) {
	raw := strings.TrimSpace(stringValue(v, key))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return uint32(value), nil
}

func parseUint16(v *viper.Viper, key string) (uint16, error) {
	raw := strings.TrimSpace(stringValue(v, key))
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return uint16(value), nil
}

func parseDuration(v *viper.Viper, key string) (time.Duration, error) {
	raw := strings.TrimSpace(stringValue(v, key))
	if raw == "" {
		return 0, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", key, err)
	}
	return value, nil
}

func stringValue(v *viper.Viper, key string) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(v.GetString(key))
}

func jsonUnmarshalString(raw string, out any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}

func missingDefaultConfigError(attemptedPaths []string, err error) error {
	return fmt.Errorf(
		"load config: no config file found in default paths %s: %w",
		strings.Join(attemptedPaths, ", "),
		err,
	)
}
```

- [ ] **Step 5: Run config tests**

Run:

```bash
GOWORK=off go test ./cmd/wukongimv2 -run TestLoadConfig -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add cmd/wukongimv2/config.go cmd/wukongimv2/config_test.go
git commit -m "feat(wukongimv2): add minimal config loader"
```

## Task 3: `cmd/wukongimv2` Main Entrypoint

**Files:**
- Create: `cmd/wukongimv2/main.go`
- Create: `cmd/wukongimv2/main_test.go`
- Test: `cmd/wukongimv2/main_test.go`

- [ ] **Step 1: Write lifecycle tests first**

Create `cmd/wukongimv2/main_test.go`:

```go
package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"testing"

	internalapp "github.com/WuKongIM/WuKongIM/internalv2/app"
	"github.com/stretchr/testify/require"
)

func TestRegisterConfigFlagUsesConfUsage(t *testing.T) {
	fs := flag.NewFlagSet("wukongimv2", flag.ContinueOnError)

	configPath := registerConfigFlag(fs)

	require.NotNil(t, configPath)
	require.Equal(t, "path to wukongim.conf file", fs.Lookup("config").Usage)
}

func TestRunStartsAndStopsInjectedApp(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"gnet","protocol":"wkproto"}]`,
	)
	ctx, cancel := context.WithCancel(context.Background())
	app := &fakeRuntimeApp{started: make(chan struct{}), stopped: make(chan struct{})}

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, []string{"-config", configPath}, func(cfg internalapp.Config) (runtimeApp, error) {
			require.Equal(t, uint64(1), cfg.NodeID)
			return app, nil
		})
	}()

	<-app.started
	cancel()
	require.NoError(t, <-errCh)
	<-app.stopped
}

func TestMainUsesExplicitConfigFlagParsing(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=2",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-2"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7002",
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5102","transport":"gnet","protocol":"wkproto"}]`,
	)
	ctx, cancel := context.WithCancel(context.Background())
	app := &fakeRuntimeApp{started: make(chan struct{}), stopped: make(chan struct{})}

	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, []string{"-config=" + configPath}, func(cfg internalapp.Config) (runtimeApp, error) {
			require.Equal(t, uint64(2), cfg.NodeID)
			return app, nil
		})
	}()

	<-app.started
	cancel()
	require.NoError(t, <-errCh)
	<-app.stopped
}

func TestRunReturnsConfigErrorBeforeCreatingApp(t *testing.T) {
	dir := t.TempDir()
	configPath := writeConf(t, dir, "wukongim.conf",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
	)
	created := false

	err := run(context.Background(), []string{"-config", configPath}, func(cfg internalapp.Config) (runtimeApp, error) {
		created = true
		return nil, nil
	})

	require.Error(t, err)
	require.False(t, created)
}

func TestMainFunctionCanBeCalledWithNoConfigWhenDefaultFileExists(t *testing.T) {
	dir := t.TempDir()
	chdirForTest(t, dir)
	writeConf(t, dir, "wukongim.conf",
		"WK_NODE_ID=3",
		"WK_NODE_DATA_DIR="+filepath.Join(dir, "node-3"),
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7003",
		`WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5103","transport":"gnet","protocol":"wkproto"}]`,
	)
	oldArgs := os.Args
	os.Args = []string{"wukongimv2"}
	t.Cleanup(func() { os.Args = oldArgs })

	ctx, cancel := context.WithCancel(context.Background())
	app := &fakeRuntimeApp{started: make(chan struct{}), stopped: make(chan struct{})}
	errCh := make(chan error, 1)
	go func() {
		errCh <- run(ctx, os.Args[1:], func(cfg internalapp.Config) (runtimeApp, error) {
			require.Equal(t, uint64(3), cfg.NodeID)
			return app, nil
		})
	}()

	<-app.started
	cancel()
	require.NoError(t, <-errCh)
	<-app.stopped
}

type fakeRuntimeApp struct {
	started chan struct{}
	stopped chan struct{}
}

func (a *fakeRuntimeApp) Start(context.Context) error {
	close(a.started)
	return nil
}

func (a *fakeRuntimeApp) Stop(context.Context) error {
	close(a.stopped)
	return nil
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./cmd/wukongimv2 -run 'Test(Register|Run|Main)' -count=1
```

Expected: FAIL because `run`, `runtimeApp`, and `registerConfigFlag` are undefined.

- [ ] **Step 3: Add main implementation**

Create `cmd/wukongimv2/main.go`:

```go
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	internalapp "github.com/WuKongIM/WuKongIM/internalv2/app"
)

// runtimeApp is the lifecycle surface required by the wukongimv2 entry.
type runtimeApp interface {
	Start(context.Context) error
	Stop(context.Context) error
}

type appFactory func(internalapp.Config) (runtimeApp, error)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], newInternalV2App); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string, newApp appFactory) error {
	fs := flag.NewFlagSet("wukongimv2", flag.ContinueOnError)
	configPath := registerConfigFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	application, err := newApp(cfg)
	if err != nil {
		return err
	}
	if err := application.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()

	stopCtx, cancel := context.WithTimeout(context.Background(), stopTimeout(cfg))
	defer cancel()
	return application.Stop(stopCtx)
}

func newInternalV2App(cfg internalapp.Config) (runtimeApp, error) {
	return internalapp.New(cfg)
}

func registerConfigFlag(fs *flag.FlagSet) *string {
	return fs.String("config", "", "path to wukongim.conf file")
}

func stopTimeout(cfg internalapp.Config) time.Duration {
	if cfg.Cluster.Timeouts.Stop > 0 {
		return cfg.Cluster.Timeouts.Stop
	}
	return 5 * time.Second
}
```

- [ ] **Step 4: Run main package tests**

Run:

```bash
GOWORK=off go test ./cmd/wukongimv2 -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

Run:

```bash
git add cmd/wukongimv2/main.go cmd/wukongimv2/main_test.go
git commit -m "feat(wukongimv2): add standalone entrypoint"
```

## Task 4: Documentation And Directory Metadata

**Files:**
- Create: `cmd/wukongimv2/README.md`
- Modify: `AGENTS.md`
- Test: documentation consistency checks by reading diff.

- [ ] **Step 1: Add README**

Create `cmd/wukongimv2/README.md`:

```markdown
# wukongimv2

`cmd/wukongimv2` is the standalone verification entry for the `internalv2`
architecture.

It intentionally does not replace `cmd/wukongim` and does not add a runtime
config switch to the old entry. The old binary continues to compose
`internal/app`; this binary composes `internalv2/app`.

Minimal local run:

```bash
go run ./cmd/wukongimv2 -config ./wukongim.conf
```

The first milestone supports the single-node-cluster send-to-sendack path only.
Delivery, conversation, CMD, manager APIs, and full production compatibility are
outside this entry until those capabilities migrate into `internalv2`.
```

- [ ] **Step 2: Update `AGENTS.md` directory structure**

In the `cmd/` block of `AGENTS.md`, add:

```text
  wukongimv2/            新架构实验入口；读取配置并启动 internalv2/app，不通过旧 wukongim 配置开关分流
```

In the `test/e2e/message` scenario list, the top-level `AGENTS.md` update happens in Task 5 after the scenario is created.

- [ ] **Step 3: Inspect diff**

Run:

```bash
git diff -- cmd/wukongimv2/README.md AGENTS.md
```

Expected: only README and directory structure docs changed.

- [ ] **Step 4: Commit**

Run:

```bash
git add cmd/wukongimv2/README.md AGENTS.md
git commit -m "docs(wukongimv2): describe standalone entry"
```

## Task 5: Binary-Level Single-Node Sendack E2E

**Files:**
- Create: `test/e2e/message/wukongimv2_single_node_send/AGENTS.md`
- Create: `test/e2e/message/wukongimv2_single_node_send/wukongimv2_single_node_send_test.go`
- Modify: `test/e2e/message/AGENTS.md`
- Test: new e2e scenario.

- [ ] **Step 1: Create scenario directory**

Run:

```bash
mkdir -p test/e2e/message/wukongimv2_single_node_send
```

- [ ] **Step 2: Add scenario AGENTS**

Create `test/e2e/message/wukongimv2_single_node_send/AGENTS.md`:

```markdown
# wukongimv2 single-node send AGENTS

This scenario proves the standalone `cmd/wukongimv2` binary can start a
single-node cluster, accept a WKProto SEND, and return a success SENDACK.

Run:

```bash
go test -tags=e2e ./test/e2e/message/wukongimv2_single_node_send -count=1
```

Keep this scenario focused on sendack only. Do not assert message delivery until
delivery runtime has migrated into `internalv2`.
```

- [ ] **Step 3: Write the e2e test**

Create `test/e2e/message/wukongimv2_single_node_send/wukongimv2_single_node_send_test.go`:

```go
//go:build e2e

package wukongimv2_single_node_send

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/test/e2e/suite"
	"github.com/stretchr/testify/require"
)

func TestWukongIMV2SingleNodeSendack(t *testing.T) {
	binaryPath := buildWukongIMV2Binary(t)
	workspace := suite.NewWorkspace(t)
	ports := suite.ReserveLoopbackPorts(t)
	spec := suite.NodeSpec{
		ID:          1,
		Name:        "node-1",
		RootDir:     workspace.NodeRootDir(1),
		DataDir:     workspace.NodeDataDir(1),
		ConfigPath:  workspace.NodeConfigPath(1),
		StdoutPath:  workspace.NodeStdoutPath(1),
		StderrPath:  workspace.NodeStderrPath(1),
		ClusterAddr: ports.ClusterAddr,
		GatewayAddr: ports.GatewayAddr,
		LogDir:      workspace.NodeLogDir(1),
	}
	require.NoError(t, os.MkdirAll(spec.DataDir, 0o755))
	require.NoError(t, os.MkdirAll(spec.LogDir, 0o755))
	require.NoError(t, os.WriteFile(spec.ConfigPath, []byte(renderWukongIMV2SingleNodeConfig(spec)), 0o644))

	process := &suite.NodeProcess{Spec: spec, BinaryPath: binaryPath}
	require.NoError(t, process.Start())
	t.Cleanup(func() {
		require.NoError(t, process.Stop())
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, suite.WaitWKProtoReady(ctx, spec.GatewayAddr), process.DumpDiagnostics())

	client, err := suite.NewWKProtoClient()
	require.NoError(t, err)
	defer func() { _ = client.Close() }()
	require.NoError(t, client.Connect(spec.GatewayAddr, "u1", "u1-device"))

	require.NoError(t, client.SendFrame(&frame.SendPacket{
		ChannelID:   "room-e2e-v2",
		ChannelType: frame.ChannelTypeGroup,
		ClientSeq:   1,
		ClientMsgNo: "wukongimv2-e2e-msg-1",
		Payload:     []byte("hello from wukongimv2 e2e"),
	}))
	sendack, err := client.ReadSendAck()
	require.NoError(t, err)
	require.Equal(t, frame.ReasonSuccess, sendack.ReasonCode)
	require.NotZero(t, sendack.MessageID)
	require.Equal(t, uint64(1), sendack.MessageSeq)
	require.Equal(t, uint64(1), sendack.ClientSeq)
	require.Equal(t, "wukongimv2-e2e-msg-1", sendack.ClientMsgNo)
}

func renderWukongIMV2SingleNodeConfig(spec suite.NodeSpec) string {
	return fmt.Sprintf(`WK_NODE_ID=%d
WK_NODE_DATA_DIR=%s
WK_CLUSTER_LISTEN_ADDR=%s
WK_CLUSTER_INITIAL_SLOT_COUNT=1
WK_CLUSTER_HASH_SLOT_COUNT=4
WK_CLUSTER_SLOT_REPLICA_N=1
WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"%s","transport":"gnet","protocol":"wkproto"}]
WK_GATEWAY_SEND_TIMEOUT=2s
`, spec.ID, spec.DataDir, spec.ClusterAddr, spec.GatewayAddr)
}

func buildWukongIMV2Binary(t *testing.T) string {
	t.Helper()
	binaryPath := filepath.Join(t.TempDir(), "wukongimv2-e2e")
	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/wukongimv2")
	cmd.Dir = repoRoot()
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	return binaryPath
}

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}
```

- [ ] **Step 4: Update top-level message e2e AGENTS**

In `test/e2e/message/AGENTS.md`, add a row to the scenario table:

```markdown
| `wukongimv2_single_node_send` | Prove the standalone `cmd/wukongimv2` binary can complete one single-node-cluster WKProto SEND -> SENDACK closure. | `go test -tags=e2e ./test/e2e/message/wukongimv2_single_node_send -count=1` |
```

- [ ] **Step 5: Run the e2e scenario**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/wukongimv2_single_node_send -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

Run:

```bash
git add test/e2e/message/AGENTS.md test/e2e/message/wukongimv2_single_node_send
git commit -m "test(wukongimv2): add single-node sendack e2e"
```

## Task 6: Final Verification

**Files:**
- Read: all changed files.
- Modify only if verification exposes a real bug.

- [ ] **Step 1: Run unit/package verification**

Run:

```bash
GOWORK=off go test ./cmd/wukongimv2 ./internalv2/... -count=1
GOWORK=off go test ./pkg/channelv2 ./pkg/clusterv2 -run 'TestPublicAPICompile|TestNodeInitializesDefaultChannelsWhenOptionMissing|TestNodeAppendChannelDelegatesToService' -count=1
```

Expected: PASS.

- [ ] **Step 2: Run e2e verification**

Run:

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/wukongimv2_single_node_send -count=1
```

Expected: PASS.

- [ ] **Step 3: Run formatting and diff checks**

Run:

```bash
gofmt -w cmd/wukongimv2/*.go internalv2/app/*.go test/e2e/message/wukongimv2_single_node_send/*.go
git diff --check
git status --short
```

Expected: `git diff --check` prints nothing. `git status --short` shows only intentional changes if a final fix was made.

- [ ] **Step 4: Review boundaries**

Run:

```bash
rg -n "internalv2" cmd/wukongim internal || true
rg -n "internal/app" cmd/wukongimv2 || true
```

Expected:

- No new `internalv2` imports or references under `cmd/wukongim` or `internal`.
- No `internal/app` import under `cmd/wukongimv2`.

- [ ] **Step 5: Commit any final verification fix**

If Task 6 changed files, commit them:

```bash
git add <changed-files>
git commit -m "fix(wukongimv2): finalize standalone entry verification"
```

If Task 6 made no changes, do not create an empty commit.

## Final Review Checklist

- `cmd/wukongim` remains unchanged.
- `cmd/wukongimv2` imports `internalv2/app`, not `internal/app`.
- No config key routes old `cmd/wukongim` into `internalv2`.
- Default metadata smoke uses `internalv2/app.New(cfg)` without `WithCluster`.
- E2E asserts sendack only, not delivery.
- No new config keys were introduced. If implementation adds any, `wukongim.conf.example` is updated in the same commit.
- `AGENTS.md` and `test/e2e/message/AGENTS.md` mention the new directories.
