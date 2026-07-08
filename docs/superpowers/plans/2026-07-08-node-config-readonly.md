# Node Config Readonly Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a read-only, per-node, redacted effective configuration view under cluster node operations.

**Architecture:** `internal/app` owns a local effective-config snapshot provider. `internal/usecase/management` validates node existence and delegates selected-node reads. `internal/infra/cluster` routes local reads directly and remote reads through a new `internal/access/node` manager RPC. `internal/access/manager` exposes `GET /manager/nodes/:node_id/config`, and `web` renders the snapshot in the existing node detail sheet.

**Tech Stack:** Go manager/access/usecase/infra/app packages, cluster node RPC, React + TypeScript manager UI, Vitest/Testing Library, Go unit tests.

---

## File Structure

- Create `internal/usecase/management/node_config.go`: DTOs, errors, reader port, and selected-node validation usecase.
- Create `internal/usecase/management/node_config_test.go`: red tests for validation, missing nodes, and unavailable readers.
- Create `internal/app/node_config.go`: local allowlisted effective-config snapshot builder and redaction helpers.
- Create `internal/app/node_config_test.go`: red tests proving useful keys render and secrets are redacted.
- Modify `internal/usecase/management/nodes.go`: add `NodeConfig` to `Options` and `App`.
- Modify `internal/access/manager/server.go`: extend `Management` interface and register `GET /manager/nodes/:node_id/config`.
- Create `internal/access/manager/node_config.go`: HTTP handler, DTO pass-through, and error mapping.
- Create `internal/access/manager/node_config_test.go`: route, auth, invalid node ID, not found, unavailable, and redaction assertions.
- Create `internal/access/node/manager_node_config_codec.go`: magic-prefixed JSON codec for node-config RPC payloads.
- Create `internal/access/node/manager_node_config_rpc.go`: RPC handler and client method.
- Create `internal/access/node/manager_node_config_rpc_test.go`: RPC handler/client/status tests.
- Modify `internal/access/node/presence_rpc.go`: add `ManagerNodeConfig` option and adapter field.
- Modify `pkg/cluster/net/ids.go`: add `RPCManagerNodeConfig` and service alias.
- Create `internal/infra/cluster/management_node_config.go`: local/remote selected-node reader.
- Create `internal/infra/cluster/management_node_config_test.go`: local and remote routing tests.
- Modify `internal/app/wiring.go`: register manager node-config RPC and wire the management reader.
- Modify `internal/app/app.go`: call `wireManagerNodeConfigRPC`.
- Modify `internal/app/app_test.go`: assert RPC registration.
- Modify `internal/access/manager/FLOW.md`, `internal/usecase/management/FLOW.md`, `internal/infra/cluster/FLOW.md`, `internal/access/node/FLOW.md`, and `internal/app/FLOW.md`: document new read-only route/RPC.
- Modify `web/src/lib/manager-api.types.ts`: add node-config response types.
- Modify `web/src/lib/manager-api.ts`: add `getNodeConfig(nodeId)`.
- Modify `web/src/lib/manager-api.test.ts`: client endpoint test.
- Modify `web/src/pages/nodes/page.tsx`: load/render config groups in detail sheet.
- Modify `web/src/pages/nodes/page.test.tsx`: render and config-error tests.
- Modify `web/src/i18n/messages/en.ts` and `web/src/i18n/messages/zh-CN.ts`: localized node config labels.

---

### Task 1: Management Usecase Contract

**Files:**
- Create: `internal/usecase/management/node_config.go`
- Create: `internal/usecase/management/node_config_test.go`
- Modify: `internal/usecase/management/nodes.go`

- [ ] **Step 1: Write the failing usecase tests**

Add `internal/usecase/management/node_config_test.go`:

```go
package management

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestNodeConfigSnapshotReadsSelectedExistingNode(t *testing.T) {
	cluster := &nodeConfigControlReader{
		nodeID: 1,
		snapshot: control.Snapshot{
			Nodes: []control.Node{
				{NodeID: 1, Roles: []control.NodeRole{control.RoleData}, JoinState: control.NodeJoinStateActive},
				{NodeID: 2, Roles: []control.NodeRole{control.RoleData}, JoinState: control.NodeJoinStateActive},
			},
		},
	}
	reader := &nodeConfigReaderStub{
		snapshot: NodeConfigSnapshot{
			GeneratedAt:     time.Unix(10, 0).UTC(),
			NodeID:          2,
			Source:          NodeConfigSnapshotSourceEffectiveStartup,
			RequiresRestart: true,
			Groups: []NodeConfigGroup{{
				ID:    "cluster",
				Title: "Cluster",
				Items: []NodeConfigItem{{
					Key:   "WK_CLUSTER_HASH_SLOT_COUNT",
					Label: "Hash slot count",
					Value: "256",
				}},
			}},
		},
	}
	app := New(Options{Cluster: cluster, NodeConfig: reader})

	got, err := app.NodeConfigSnapshot(context.Background(), 2)
	if err != nil {
		t.Fatalf("NodeConfigSnapshot() error = %v", err)
	}
	if reader.nodeID != 2 {
		t.Fatalf("reader nodeID = %d, want 2", reader.nodeID)
	}
	if got.NodeID != 2 || got.Source != NodeConfigSnapshotSourceEffectiveStartup || len(got.Groups) != 1 {
		t.Fatalf("snapshot = %#v, want node 2 effective snapshot", got)
	}
}

func TestNodeConfigSnapshotRejectsMissingNodeBeforeReader(t *testing.T) {
	reader := &nodeConfigReaderStub{}
	app := New(Options{
		Cluster: &nodeConfigControlReader{
			nodeID:   1,
			snapshot: control.Snapshot{Nodes: []control.Node{{NodeID: 1}}},
		},
		NodeConfig: reader,
	})

	_, err := app.NodeConfigSnapshot(context.Background(), 9)
	if !errors.Is(err, metadb.ErrNotFound) {
		t.Fatalf("NodeConfigSnapshot() error = %v, want metadb.ErrNotFound", err)
	}
	if reader.calls != 0 {
		t.Fatalf("reader calls = %d, want 0", reader.calls)
	}
}

func TestNodeConfigSnapshotRejectsInvalidNodeID(t *testing.T) {
	app := New(Options{Cluster: &nodeConfigControlReader{nodeID: 1}, NodeConfig: &nodeConfigReaderStub{}})

	_, err := app.NodeConfigSnapshot(context.Background(), 0)
	if !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("NodeConfigSnapshot(0) error = %v, want metadb.ErrInvalidArgument", err)
	}
}

func TestNodeConfigSnapshotUnavailableWithoutReader(t *testing.T) {
	app := New(Options{
		Cluster: &nodeConfigControlReader{
			nodeID:   1,
			snapshot: control.Snapshot{Nodes: []control.Node{{NodeID: 1}}},
		},
	})

	_, err := app.NodeConfigSnapshot(context.Background(), 1)
	if !errors.Is(err, ErrNodeConfigUnavailable) {
		t.Fatalf("NodeConfigSnapshot() error = %v, want ErrNodeConfigUnavailable", err)
	}
}

type nodeConfigControlReader struct {
	nodeID   uint64
	snapshot control.Snapshot
}

func (r *nodeConfigControlReader) NodeID() uint64 { return r.nodeID }

func (r *nodeConfigControlReader) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return r.snapshot, nil
}

type nodeConfigReaderStub struct {
	calls    int
	nodeID   uint64
	snapshot NodeConfigSnapshot
	err      error
}

func (r *nodeConfigReaderStub) NodeConfigSnapshot(_ context.Context, nodeID uint64) (NodeConfigSnapshot, error) {
	r.calls++
	r.nodeID = nodeID
	if r.err != nil {
		return NodeConfigSnapshot{}, r.err
	}
	return r.snapshot, nil
}
```

- [ ] **Step 2: Run the usecase tests and verify RED**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run NodeConfig -count=1
```

Expected: FAIL because `NodeConfigSnapshot`, `NodeConfigGroup`, `NodeConfigItem`, `NodeConfigSnapshotSourceEffectiveStartup`, `ErrNodeConfigUnavailable`, and `Options.NodeConfig` do not exist.

- [ ] **Step 3: Implement the usecase contract**

Add `internal/usecase/management/node_config.go`:

```go
package management

import (
	"context"
	"errors"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const NodeConfigSnapshotSourceEffectiveStartup = "effective_startup_config"

// ErrNodeConfigUnavailable reports that selected-node config inspection is not wired.
var ErrNodeConfigUnavailable = errors.New("internal/usecase/management: node config reader unavailable")

// NodeConfigReader reads one selected node's redacted effective startup config.
type NodeConfigReader interface {
	// NodeConfigSnapshot returns one selected node's allowlisted, redacted config snapshot.
	NodeConfigSnapshot(ctx context.Context, nodeID uint64) (NodeConfigSnapshot, error)
}

// NodeConfigSnapshot is the manager-facing redacted startup config view for one node.
type NodeConfigSnapshot struct {
	// GeneratedAt records when this snapshot was built.
	GeneratedAt time.Time `json:"generated_at"`
	// NodeID is the node whose effective config was read.
	NodeID uint64 `json:"node_id"`
	// Source names the config source class.
	Source string `json:"source"`
	// RequiresRestart reports whether changing these startup settings requires restart.
	RequiresRestart bool `json:"requires_restart"`
	// Groups contains stable, bounded config sections.
	Groups []NodeConfigGroup `json:"groups"`
}

// NodeConfigGroup contains a stable set of related config items.
type NodeConfigGroup struct {
	// ID is the stable group identifier used by the web UI.
	ID string `json:"id"`
	// Title is the operator-facing group title.
	Title string `json:"title"`
	// Items contains allowlisted config values.
	Items []NodeConfigItem `json:"items"`
}

// NodeConfigItem contains one allowlisted config value.
type NodeConfigItem struct {
	// Key is the canonical WK_* config key.
	Key string `json:"key"`
	// Label is a concise operator-facing label.
	Label string `json:"label"`
	// Value is the already-formatted effective value.
	Value string `json:"value"`
	// Sensitive reports whether the underlying config is sensitive.
	Sensitive bool `json:"sensitive"`
	// Redacted reports whether Value is a fixed redaction token.
	Redacted bool `json:"redacted"`
}

// NodeConfigSnapshot returns one selected node's redacted effective startup configuration.
func (a *App) NodeConfigSnapshot(ctx context.Context, nodeID uint64) (NodeConfigSnapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return NodeConfigSnapshot{}, err
	}
	if nodeID == 0 {
		return NodeConfigSnapshot{}, metadb.ErrInvalidArgument
	}
	if a == nil || a.nodeConfig == nil {
		return NodeConfigSnapshot{}, ErrNodeConfigUnavailable
	}
	if err := a.ensureManagedNodeExists(ctx, nodeID); err != nil {
		return NodeConfigSnapshot{}, err
	}
	snapshot, err := a.nodeConfig.NodeConfigSnapshot(ctx, nodeID)
	if err != nil {
		return NodeConfigSnapshot{}, err
	}
	if snapshot.NodeID == 0 {
		snapshot.NodeID = nodeID
	}
	return snapshot, nil
}

func (a *App) ensureManagedNodeExists(ctx context.Context, nodeID uint64) error {
	if a == nil || a.cluster == nil {
		return metadb.ErrInvalidArgument
	}
	snapshot, err := a.cluster.LocalControlSnapshot(ctx)
	if err != nil {
		return err
	}
	for _, node := range snapshot.Nodes {
		if node.NodeID == nodeID {
			return nil
		}
	}
	return metadb.ErrNotFound
}
```

Modify `internal/usecase/management/nodes.go`:

```go
type Options struct {
	// Cluster reads cluster control state.
	Cluster ControlSnapshotReader
	// NodeConfig reads selected-node redacted effective startup configuration.
	NodeConfig NodeConfigReader
	// RuntimeSummary reads node runtime counters for the manager node list.
	RuntimeSummary RuntimeSummaryReader
```

```go
type App struct {
	cluster                      ControlSnapshotReader
	nodeConfig                   NodeConfigReader
	runtimeSummary               RuntimeSummaryReader
```

```go
return &App{
	cluster:                      opts.Cluster,
	nodeConfig:                   opts.NodeConfig,
	runtimeSummary:               opts.RuntimeSummary,
```

- [ ] **Step 4: Run the usecase tests and verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/usecase/management -run NodeConfig -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 1**

```bash
git add internal/usecase/management/node_config.go internal/usecase/management/node_config_test.go internal/usecase/management/nodes.go
git commit -m "feat(manager): add node config usecase"
```

---

### Task 2: App Effective Config Snapshot And Redaction

**Files:**
- Create: `internal/app/node_config.go`
- Create: `internal/app/node_config_test.go`

- [ ] **Step 1: Write the failing app snapshot tests**

Add `internal/app/node_config_test.go`:

```go
package app

import (
	"context"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestNodeConfigSnapshotReturnsEffectiveGroupsAndRedactsSecrets(t *testing.T) {
	app := &App{cfg: Config{
		NodeID: 2,
		DataDir: "/var/lib/wukongim/node2",
		Cluster: clusterpkg.Config{
			NodeID:     2,
			ListenAddr: "127.0.0.1:12002",
			DataDir:    "/var/lib/wukongim/node2/cluster",
			Control: clusterpkg.ControlConfig{
				ClusterID: "cluster-a",
			},
			Join: clusterpkg.JoinConfig{
				AdvertiseAddr: "node2.example:12002",
				Token:         "join-secret",
			},
			Slots: clusterpkg.SlotConfig{
				InitialSlotCount: 1,
				HashSlotCount:    256,
				ReplicaCount:     3,
			},
			Channel: clusterpkg.ChannelConfig{
				ReplicaCount: 3,
			},
			HealthReport: clusterpkg.HealthReportConfig{
				Interval: 5 * time.Second,
				TTL:      30 * time.Second,
			},
		},
		Manager: ManagerConfig{
			ListenAddr: ":5300",
			AuthOn:    true,
			JWTSecret: "super-secret",
			JWTIssuer: "wk-manager",
			JWTExpire: time.Hour,
			Users:     []ManagerUserConfig{{Username: "admin", Password: "admin-password"}},
		},
		Gateway: GatewayConfig{SendTimeout: 3 * time.Second},
		Log:     LogConfig{Level: "debug", Dir: "/var/log/wukongim"},
		Plugin: PluginConfig{Enable: true, Timeout: 5 * time.Second},
		Webhook: WebhookConfig{
			Enabled:          true,
			HTTPAddr:         "http://127.0.0.1:19090/webhook",
			QueueSize:        1024,
			Workers:          16,
			RequestTimeout:   5 * time.Second,
			RetryMaxAttempts: 3,
		},
	}}

	snapshot, err := app.NodeConfigSnapshot(context.Background(), 2)
	if err != nil {
		t.Fatalf("NodeConfigSnapshot() error = %v", err)
	}
	if snapshot.NodeID != 2 || snapshot.Source != "effective_startup_config" || !snapshot.RequiresRestart {
		t.Fatalf("snapshot metadata = %#v", snapshot)
	}
	if !containsNodeConfigItem(snapshot, "WK_NODE_ID", "2") {
		t.Fatalf("snapshot missing WK_NODE_ID=2: %#v", snapshot.Groups)
	}
	if !containsNodeConfigItem(snapshot, "WK_MANAGER_JWT_SECRET", "******") {
		t.Fatalf("snapshot missing redacted JWT secret: %#v", snapshot.Groups)
	}
	raw := nodeConfigSnapshotText(snapshot)
	for _, forbidden := range []string{"super-secret", "admin-password"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("snapshot leaked secret %q: %s", forbidden, raw)
		}
	}
}

func containsNodeConfigItem(snapshot managementusecase.NodeConfigSnapshot, key, value string) bool {
	for _, group := range snapshot.Groups {
		for _, item := range group.Items {
			if item.Key == key && item.Value == value {
				return true
			}
		}
	}
	return false
}

func nodeConfigSnapshotText(snapshot managementusecase.NodeConfigSnapshot) string {
	var builder strings.Builder
	for _, group := range snapshot.Groups {
		builder.WriteString(group.ID)
		builder.WriteString(" ")
		for _, item := range group.Items {
			builder.WriteString(item.Key)
			builder.WriteString("=")
			builder.WriteString(item.Value)
			builder.WriteString(" ")
		}
	}
	return builder.String()
}
```

- [ ] **Step 2: Run app tests and verify RED**

Run:

```bash
GOWORK=off go test ./internal/app -run NodeConfigSnapshot -count=1
```

Expected: FAIL because `(*App).NodeConfigSnapshot` does not exist in `internal/app`.

- [ ] **Step 3: Implement app snapshot builder**

Add `internal/app/node_config.go`:

```go
package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

const nodeConfigRedactedValue = "******"

// NodeConfigSnapshot returns this process's allowlisted effective startup configuration.
func (a *App) NodeConfigSnapshot(context.Context, uint64) (managementusecase.NodeConfigSnapshot, error) {
	if a == nil {
		return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
	}
	cfg := a.cfg
	clusterCfg := defaultClusterConfig(cfg)
	nodeID := cfg.NodeID
	if nodeID == 0 {
		nodeID = clusterCfg.NodeID
	}
	return managementusecase.NodeConfigSnapshot{
		GeneratedAt:     time.Now().UTC(),
		NodeID:          nodeID,
		Source:          managementusecase.NodeConfigSnapshotSourceEffectiveStartup,
		RequiresRestart: true,
		Groups: []managementusecase.NodeConfigGroup{
			nodeConfigGroup("node", "Node", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_NODE_ID", "Node ID", fmt.Sprintf("%d", nodeID)),
				nodeConfigItem("WK_NODE_DATA_DIR", "Data directory", cfg.DataDir),
			}),
			nodeConfigGroup("cluster", "Cluster", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_CLUSTER_ID", "Cluster ID", clusterCfg.Control.ClusterID),
				redactedNodeConfigItem("WK_CLUSTER_JOIN_TOKEN", "Join token", clusterCfg.Join.Token),
				nodeConfigItem("WK_CLUSTER_LISTEN_ADDR", "Cluster listen address", clusterCfg.ListenAddr),
				nodeConfigItem("WK_CLUSTER_ADVERTISE_ADDR", "Cluster advertise address", clusterCfg.Join.AdvertiseAddr),
				nodeConfigItem("WK_CLUSTER_HASH_SLOT_COUNT", "Hash slot count", fmt.Sprintf("%d", clusterCfg.Slots.HashSlotCount)),
				nodeConfigItem("WK_CLUSTER_INITIAL_SLOT_COUNT", "Initial slot count", fmt.Sprintf("%d", clusterCfg.Slots.InitialSlotCount)),
				nodeConfigItem("WK_CLUSTER_SLOT_REPLICA_N", "Slot replica count", fmt.Sprintf("%d", clusterCfg.Slots.ReplicaCount)),
				nodeConfigItem("WK_CLUSTER_CHANNEL_REPLICA_N", "Channel replica count", fmt.Sprintf("%d", clusterCfg.Channel.ReplicaCount)),
				nodeConfigItem("WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL", "Node health report interval", durationConfigValue(clusterCfg.HealthReport.Interval)),
				nodeConfigItem("WK_CLUSTER_NODE_HEALTH_REPORT_TTL", "Node health report TTL", durationConfigValue(clusterCfg.HealthReport.TTL)),
			}),
			nodeConfigGroup("gateway", "Gateway", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_EXTERNAL_TCPADDR", "External TCP address", cfg.API.ExternalTCPAddr),
				nodeConfigItem("WK_EXTERNAL_WSADDR", "External WS address", cfg.API.ExternalWSAddr),
				nodeConfigItem("WK_EXTERNAL_WSSADDR", "External WSS address", cfg.API.ExternalWSSAddr),
				nodeConfigItem("WK_GATEWAY_LISTENERS", "Gateway listeners", fmt.Sprintf("%d configured listeners", len(cfg.Gateway.Listeners))),
				nodeConfigItem("WK_GATEWAY_SEND_TIMEOUT", "Gateway send timeout", durationConfigValue(cfg.Gateway.SendTimeout)),
			}),
			nodeConfigGroup("message", "Message / Channel", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_MESSAGE_PERSON_WHITELIST_ENABLED", "Person whitelist", boolConfigValue(cfg.Message.PersonWhitelistEnabled)),
				nodeConfigItem("WK_MESSAGE_PERMISSION_CACHE_TTL", "Permission cache TTL", durationConfigValue(cfg.Message.PermissionCacheTTL)),
				nodeConfigItem("WK_CHANNEL_LARGE_GROUP_SUBSCRIBER_THRESHOLD", "Large group subscriber threshold", fmt.Sprintf("%d", cfg.Channel.LargeGroupSubscriberThreshold)),
				nodeConfigItem("WK_CHANNEL_APPEND_SHARD_COUNT", "Channel append shard count", fmt.Sprintf("%d", cfg.ChannelAppend.AuthorityShardCount)),
				nodeConfigItem("WK_CONVERSATION_AUTHORITY_FLUSH_INTERVAL", "Conversation flush interval", durationConfigValue(cfg.Conversation.AuthorityFlushInterval)),
				nodeConfigItem("WK_CONVERSATION_AUTHORITY_ADMIT_CONCURRENCY", "Conversation admit concurrency", fmt.Sprintf("%d", cfg.Conversation.AuthorityAdmitConcurrency)),
			}),
			nodeConfigGroup("delivery", "Delivery", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_DELIVERY_ENABLE", "Delivery enabled", boolConfigValue(cfg.Delivery.Enabled)),
				nodeConfigItem("WK_DELIVERY_FANOUT_PAGE_SIZE", "Fanout page size", fmt.Sprintf("%d", cfg.Delivery.FanoutPageSize)),
				nodeConfigItem("WK_DELIVERY_PUSH_BATCH_SIZE", "Push batch size", fmt.Sprintf("%d", cfg.Delivery.PushBatchSize)),
				nodeConfigItem("WK_DELIVERY_PENDING_ACK_TTL", "Pending ack TTL", durationConfigValue(cfg.Delivery.PendingAckTTL)),
				nodeConfigItem("WK_DELIVERY_EVENT_QUEUE_SIZE", "Event queue size", fmt.Sprintf("%d", cfg.Delivery.EventQueueSize)),
			}),
			nodeConfigGroup("webhook", "Webhook", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_WEBHOOK_ENABLE", "Webhook enabled", boolConfigValue(cfg.Webhook.Enabled)),
				nodeConfigItem("WK_WEBHOOK_HTTP_ADDR", "Webhook endpoint", endpointPresenceValue(cfg.Webhook.HTTPAddr)),
				nodeConfigItem("WK_WEBHOOK_FOCUS_EVENTS", "Focus events", stringListSummary(cfg.Webhook.FocusEvents, "all supported events")),
				nodeConfigItem("WK_WEBHOOK_QUEUE_SIZE", "Queue size", fmt.Sprintf("%d", cfg.Webhook.QueueSize)),
				nodeConfigItem("WK_WEBHOOK_WORKERS", "Workers", fmt.Sprintf("%d", cfg.Webhook.Workers)),
				nodeConfigItem("WK_WEBHOOK_REQUEST_TIMEOUT", "Request timeout", durationConfigValue(cfg.Webhook.RequestTimeout)),
				nodeConfigItem("WK_WEBHOOK_RETRY_MAX_ATTEMPTS", "Retry max attempts", fmt.Sprintf("%d", cfg.Webhook.RetryMaxAttempts)),
			}),
			nodeConfigGroup("plugin", "Plugin", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_PLUGIN_ENABLE", "Plugin enabled", boolConfigValue(cfg.Plugin.Enable)),
				nodeConfigItem("WK_PLUGIN_HOT_RELOAD", "Plugin hot reload", boolConfigValue(cfg.Plugin.HotReload)),
				nodeConfigItem("WK_PLUGIN_FAIL_OPEN", "Plugin fail open", boolConfigValue(cfg.Plugin.FailOpen)),
				nodeConfigItem("WK_PLUGIN_TIMEOUT", "Plugin timeout", durationConfigValue(cfg.Plugin.Timeout)),
				nodeConfigItem("WK_PLUGIN_PERSIST_AFTER_QUEUE_SIZE", "PersistAfter queue size", fmt.Sprintf("%d", cfg.Plugin.PersistAfterQueueSize)),
				nodeConfigItem("WK_PLUGIN_PERSIST_AFTER_WORKERS", "PersistAfter workers", fmt.Sprintf("%d", cfg.Plugin.PersistAfterWorkers)),
			}),
			nodeConfigGroup("log", "Log", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_LOG_LEVEL", "Log level", cfg.Log.Level),
				nodeConfigItem("WK_LOG_DIR", "Log directory", cfg.Log.Dir),
				nodeConfigItem("WK_LOG_MAX_SIZE", "Log max size MB", fmt.Sprintf("%d", cfg.Log.MaxSize)),
				nodeConfigItem("WK_LOG_MAX_AGE", "Log max age days", fmt.Sprintf("%d", cfg.Log.MaxAge)),
				nodeConfigItem("WK_LOG_MAX_BACKUPS", "Log max backups", fmt.Sprintf("%d", cfg.Log.MaxBackups)),
				nodeConfigItem("WK_LOG_COMPRESS", "Log compression", boolConfigValue(cfg.Log.Compress)),
				nodeConfigItem("WK_LOG_CONSOLE", "Console logging", boolConfigValue(cfg.Log.Console)),
				nodeConfigItem("WK_LOG_FORMAT", "Log format", cfg.Log.Format),
			}),
			nodeConfigGroup("observability", "Observability", []managementusecase.NodeConfigItem{
				nodeConfigItem("WK_API_LISTEN_ADDR", "API listen address", cfg.API.ListenAddr),
				nodeConfigItem("WK_MANAGER_LISTEN_ADDR", "Manager listen address", cfg.Manager.ListenAddr),
				nodeConfigItem("WK_MANAGER_AUTH_ON", "Manager auth enabled", boolConfigValue(cfg.Manager.AuthOn)),
				redactedNodeConfigItem("WK_MANAGER_JWT_SECRET", "Manager JWT secret", cfg.Manager.JWTSecret),
				nodeConfigItem("WK_MANAGER_JWT_ISSUER", "Manager JWT issuer", cfg.Manager.JWTIssuer),
				nodeConfigItem("WK_MANAGER_JWT_EXPIRE", "Manager JWT expiry", durationConfigValue(cfg.Manager.JWTExpire)),
				redactedNodeConfigItem("WK_MANAGER_USERS", "Manager users", fmt.Sprintf("%d configured users", len(cfg.Manager.Users))),
				nodeConfigItem("WK_METRICS_ENABLE", "Metrics enabled", boolConfigValue(cfg.Observability.MetricsEnabled)),
				nodeConfigItem("WK_PROMETHEUS_ENABLE", "Prometheus enabled", boolConfigValue(cfg.Observability.Prometheus.Enabled)),
				nodeConfigItem("WK_TOP_API_ENABLE", "Top API enabled", boolConfigValue(cfg.Top.APIEnabled)),
				nodeConfigItem("WK_DEBUG_API_ENABLE", "Debug API enabled", boolConfigValue(cfg.Observability.DebugAPIEnabled)),
				nodeConfigItem("WK_DIAGNOSTICS_ENABLE", "Diagnostics enabled", boolConfigValue(cfg.Observability.Diagnostics.Enabled)),
			}),
		},
	}, nil
}

func nodeConfigGroup(id, title string, items []managementusecase.NodeConfigItem) managementusecase.NodeConfigGroup {
	filtered := make([]managementusecase.NodeConfigItem, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Key) == "" {
			continue
		}
		filtered = append(filtered, item)
	}
	return managementusecase.NodeConfigGroup{ID: id, Title: title, Items: filtered}
}

func nodeConfigItem(key, label, value string) managementusecase.NodeConfigItem {
	return managementusecase.NodeConfigItem{Key: key, Label: label, Value: value}
}

func redactedNodeConfigItem(key, label, value string) managementusecase.NodeConfigItem {
	item := managementusecase.NodeConfigItem{Key: key, Label: label, Sensitive: true}
	if strings.TrimSpace(value) != "" {
		item.Value = nodeConfigRedactedValue
		item.Redacted = true
	}
	return item
}

func boolConfigValue(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func durationConfigValue(value time.Duration) string {
	if value == 0 {
		return "0s"
	}
	return value.String()
}

func endpointPresenceValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "configured"
}

func stringListSummary(values []string, empty string) string {
	if len(values) == 0 {
		return empty
	}
	return strings.Join(values, ", ")
}
```

- [ ] **Step 4: Run app tests and verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/app -run NodeConfigSnapshot -count=1
```

Expected: PASS and no leaked raw secret values.

- [ ] **Step 5: Commit Task 2**

```bash
git add internal/app/node_config.go internal/app/node_config_test.go
git commit -m "feat(app): expose redacted node config snapshot"
```

---

### Task 3: Manager HTTP Route

**Files:**
- Modify: `internal/access/manager/server.go`
- Create: `internal/access/manager/node_config.go`
- Create: `internal/access/manager/node_config_test.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write the failing HTTP route tests**

Add `internal/access/manager/node_config_test.go`:

```go
package manager

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestManagerNodeConfigReturnsSelectedNodeSnapshot(t *testing.T) {
	snapshot := managementusecase.NodeConfigSnapshot{
		GeneratedAt:     time.Unix(10, 0).UTC(),
		NodeID:          2,
		Source:          managementusecase.NodeConfigSnapshotSourceEffectiveStartup,
		RequiresRestart: true,
		Groups: []managementusecase.NodeConfigGroup{{
			ID:    "cluster",
			Title: "Cluster",
			Items: []managementusecase.NodeConfigItem{{
				Key:   "WK_CLUSTER_HASH_SLOT_COUNT",
				Label: "Hash slot count",
				Value: "256",
			}, {
				Key:       "WK_MANAGER_JWT_SECRET",
				Label:     "Manager JWT secret",
				Value:     "******",
				Sensitive: true,
				Redacted:  true,
			}},
		}},
	}
	stub := managerNodesStub{nodeConfig: snapshot}
	srv := New(Options{Management: stub})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/config", nil)
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"node_id":2`) ||
		!strings.Contains(rec.Body.String(), `"WK_CLUSTER_HASH_SLOT_COUNT"`) ||
		!strings.Contains(rec.Body.String(), `"******"`) {
		t.Fatalf("body missing config fields: %s", rec.Body.String())
	}
}

func TestManagerNodeConfigErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		path   string
		mgmt   Management
		status int
		body   string
	}{
		{name: "missing management", path: "/manager/nodes/2/config", status: http.StatusServiceUnavailable, body: `{"error":"service_unavailable","message":"management not configured"}`},
		{name: "invalid node id", path: "/manager/nodes/not-a-node/config", mgmt: managerNodesStub{}, status: http.StatusBadRequest, body: `{"error":"bad_request","message":"invalid node_id"}`},
		{name: "not found", path: "/manager/nodes/9/config", mgmt: managerNodesStub{nodeConfigErr: metadb.ErrNotFound}, status: http.StatusNotFound, body: `{"error":"not_found","message":"node not found"}`},
		{name: "unavailable", path: "/manager/nodes/2/config", mgmt: managerNodesStub{nodeConfigErr: managementusecase.ErrNodeConfigUnavailable}, status: http.StatusServiceUnavailable, body: `{"error":"service_unavailable","message":"node config unavailable"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := New(Options{Management: tt.mgmt})
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			srv.Engine().ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.status, rec.Body.String())
			}
			if !jsonEqual(rec.Body.String(), tt.body) {
				t.Fatalf("body = %s, want %s", rec.Body.String(), tt.body)
			}
		})
	}
}

func TestManagerNodeConfigRequiresNodeReadPermission(t *testing.T) {
	var called bool
	srv := New(Options{
		Auth: testAuthConfig([]UserConfig{{
			Username: "viewer",
			Password: "secret",
			Permissions: []PermissionConfig{{Resource: "cluster.slot", Actions: []string{"r"}}},
		}}),
		Management: managerNodesStub{nodeConfigFunc: func(context.Context, uint64) (managementusecase.NodeConfigSnapshot, error) {
			called = true
			return managementusecase.NodeConfigSnapshot{}, nil
		}},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/manager/nodes/2/config", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, srv, "viewer"))
	srv.Engine().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if called {
		t.Fatalf("NodeConfigSnapshot called without cluster.node:r")
	}
}

var _ = errors.Is
```

- [ ] **Step 2: Run route tests and verify RED**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run NodeConfig -count=1
```

Expected: FAIL because the route and management interface method do not exist.

- [ ] **Step 3: Implement HTTP route and error mapping**

Modify `internal/access/manager/server.go`:

```go
// NodeConfigSnapshot returns one selected node's redacted effective startup config.
NodeConfigSnapshot(ctx context.Context, nodeID uint64) (managementusecase.NodeConfigSnapshot, error)
```

Register the route next to `GET /manager/nodes`:

```go
nodes.GET("/nodes", s.handleNodes)
nodes.GET("/nodes/:node_id/config", s.handleNodeConfig)
```

Add `internal/access/manager/node_config.go`:

```go
package manager

import (
	"errors"
	"net/http"
	"strconv"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/gin-gonic/gin"
)

func (s *Server) handleNodeConfig(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	nodeID, err := parseRequiredManagerNodeID(c.Param("node_id"))
	if err != nil {
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node_id")
		return
	}
	snapshot, err := s.management.NodeConfigSnapshot(c.Request.Context(), nodeID)
	if err != nil {
		writeNodeConfigError(c, err)
		return
	}
	c.JSON(http.StatusOK, snapshot)
}

func parseRequiredManagerNodeID(raw string) (uint64, error) {
	nodeID, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || nodeID == 0 {
		return 0, metadb.ErrInvalidArgument
	}
	return nodeID, nil
}

func writeNodeConfigError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, metadb.ErrInvalidArgument):
		jsonError(c, http.StatusBadRequest, "bad_request", "invalid node config request")
	case errors.Is(err, metadb.ErrNotFound):
		jsonError(c, http.StatusNotFound, "not_found", "node not found")
	case errors.Is(err, managementusecase.ErrNodeConfigUnavailable):
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "node config unavailable")
	default:
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
	}
}
```

Modify the shared `managerNodesStub` in `internal/access/manager/server_test.go` to include:

```go
nodeConfig     managementusecase.NodeConfigSnapshot
nodeConfigErr  error
nodeConfigFunc func(context.Context, uint64) (managementusecase.NodeConfigSnapshot, error)
```

and method:

```go
func (s managerNodesStub) NodeConfigSnapshot(ctx context.Context, nodeID uint64) (managementusecase.NodeConfigSnapshot, error) {
	if s.nodeConfigFunc != nil {
		return s.nodeConfigFunc(ctx, nodeID)
	}
	if s.nodeConfigErr != nil {
		return managementusecase.NodeConfigSnapshot{}, s.nodeConfigErr
	}
	if s.nodeConfig.NodeID == 0 {
		s.nodeConfig.NodeID = nodeID
	}
	return s.nodeConfig, nil
}
```

- [ ] **Step 4: Run route tests and verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run NodeConfig -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 3**

```bash
git add internal/access/manager/server.go internal/access/manager/node_config.go internal/access/manager/node_config_test.go internal/access/manager/server_test.go
git commit -m "feat(manager): add node config route"
```

---

### Task 4: Node RPC Codec And Client

**Files:**
- Modify: `pkg/cluster/net/ids.go`
- Modify: `internal/access/node/presence_rpc.go`
- Create: `internal/access/node/manager_node_config_codec.go`
- Create: `internal/access/node/manager_node_config_rpc.go`
- Create: `internal/access/node/manager_node_config_rpc_test.go`

- [ ] **Step 1: Write failing RPC tests**

Add `internal/access/node/manager_node_config_rpc_test.go`:

```go
package node

import (
	"context"
	"errors"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagerNodeConfigRPCReadsLocalProvider(t *testing.T) {
	expected := managementusecase.NodeConfigSnapshot{
		GeneratedAt: time.Unix(10, 0).UTC(),
		NodeID:      2,
		Source:      managementusecase.NodeConfigSnapshotSourceEffectiveStartup,
		Groups: []managementusecase.NodeConfigGroup{{
			ID: "cluster",
			Items: []managementusecase.NodeConfigItem{{
				Key:   "WK_CLUSTER_HASH_SLOT_COUNT",
				Label: "Hash slot count",
				Value: "256",
			}},
		}},
	}
	reader := &fakeManagerNodeConfigReader{snapshot: expected}
	adapter := New(Options{ManagerNodeConfig: reader})

	body, err := encodeManagerNodeConfigRequest(managerNodeConfigRPCRequest{NodeID: 2})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	respBody, err := adapter.HandleManagerNodeConfigRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerNodeConfigRPC() error = %v", err)
	}
	resp, err := decodeManagerNodeConfigResponse(respBody)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != rpcStatusOK || resp.Snapshot.NodeID != 2 {
		t.Fatalf("response = %#v, want ok node 2", resp)
	}
	if reader.nodeID != 2 {
		t.Fatalf("reader nodeID = %d, want 2", reader.nodeID)
	}
}

func TestManagerNodeConfigRPCClientCallsExpectedService(t *testing.T) {
	expected := managementusecase.NodeConfigSnapshot{NodeID: 2, Source: managementusecase.NodeConfigSnapshotSourceEffectiveStartup}
	adapter := New(Options{ManagerNodeConfig: &fakeManagerNodeConfigReader{snapshot: expected}})
	node := &fakeManagerNodeConfigRPCNode{handler: adapter.HandleManagerNodeConfigRPC}

	got, err := NewClient(node).GetManagerNodeConfig(context.Background(), 2)
	if err != nil {
		t.Fatalf("GetManagerNodeConfig() error = %v", err)
	}
	if got.NodeID != 2 {
		t.Fatalf("snapshot node = %d, want 2", got.NodeID)
	}
	if node.nodeID != 2 || node.serviceID != ManagerNodeConfigRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, ManagerNodeConfigRPCServiceID)
	}
}

func TestManagerNodeConfigRPCMapsUnavailable(t *testing.T) {
	adapter := New(Options{ManagerNodeConfig: &fakeManagerNodeConfigReader{err: managementusecase.ErrNodeConfigUnavailable}})
	node := &fakeManagerNodeConfigRPCNode{handler: adapter.HandleManagerNodeConfigRPC}

	_, err := NewClient(node).GetManagerNodeConfig(context.Background(), 2)
	if !errors.Is(err, managementusecase.ErrNodeConfigUnavailable) {
		t.Fatalf("GetManagerNodeConfig() error = %v, want ErrNodeConfigUnavailable", err)
	}
}

type fakeManagerNodeConfigReader struct {
	nodeID   uint64
	snapshot managementusecase.NodeConfigSnapshot
	err      error
}

func (f *fakeManagerNodeConfigReader) NodeConfigSnapshot(_ context.Context, nodeID uint64) (managementusecase.NodeConfigSnapshot, error) {
	f.nodeID = nodeID
	if f.err != nil {
		return managementusecase.NodeConfigSnapshot{}, f.err
	}
	return f.snapshot, nil
}

type fakeManagerNodeConfigRPCNode struct {
	nodeID    uint64
	serviceID uint8
	handler   func(context.Context, []byte) ([]byte, error)
}

func (f *fakeManagerNodeConfigRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	return f.handler(ctx, payload)
}
```

- [ ] **Step 2: Run RPC tests and verify RED**

Run:

```bash
GOWORK=off go test ./internal/access/node -run ManagerNodeConfig -count=1
```

Expected: FAIL because the RPC service, codec, and adapter field do not exist.

- [ ] **Step 3: Implement service ID, adapter field, codec, and client**

Modify `pkg/cluster/net/ids.go` by adding the service after `RPCManagerPlugins`:

```go
// RPCManagerNodeConfig serves internal node-local manager effective config reads.
RPCManagerNodeConfig
```

Add alias:

```go
case RPCManagerNodeConfig:
	return "manager node config"
```

Modify `internal/access/node/presence_rpc.go`:

```go
// ManagerNodeConfig handles node-local effective config snapshots.
ManagerNodeConfig ManagerNodeConfigReader
```

```go
// managerNodeConfig reads node-local effective config snapshots for manager pages.
managerNodeConfig ManagerNodeConfigReader
```

```go
managerNodeConfig: opts.ManagerNodeConfig,
```

Add `internal/access/node/manager_node_config_codec.go`:

```go
package node

import (
	"encoding/json"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

var (
	managerNodeConfigRequestMagic  = [...]byte{'W', 'K', 'V', 'C', 1}
	managerNodeConfigResponseMagic = [...]byte{'W', 'K', 'V', 'c', 1}
)

type managerNodeConfigRPCRequest struct {
	NodeID uint64 `json:"node_id"`
}

type managerNodeConfigRPCResponse struct {
	Status   string                               `json:"status"`
	Snapshot managementusecase.NodeConfigSnapshot `json:"snapshot"`
}

func encodeManagerNodeConfigRequest(req managerNodeConfigRPCRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerNodeConfigRequestMagic)+len(payload))
	dst = append(dst, managerNodeConfigRequestMagic[:]...)
	return append(dst, payload...), nil
}

func decodeManagerNodeConfigRequest(body []byte) (managerNodeConfigRPCRequest, error) {
	if !hasMagic(body, managerNodeConfigRequestMagic[:]) {
		return managerNodeConfigRPCRequest{}, fmt.Errorf("internal/access/node: invalid manager node config request codec")
	}
	var req managerNodeConfigRPCRequest
	if err := json.Unmarshal(body[len(managerNodeConfigRequestMagic):], &req); err != nil {
		return managerNodeConfigRPCRequest{}, err
	}
	return req, nil
}

func encodeManagerNodeConfigResponse(resp managerNodeConfigRPCResponse) ([]byte, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(managerNodeConfigResponseMagic)+len(payload))
	dst = append(dst, managerNodeConfigResponseMagic[:]...)
	return append(dst, payload...), nil
}

func decodeManagerNodeConfigResponse(body []byte) (managerNodeConfigRPCResponse, error) {
	if !hasMagic(body, managerNodeConfigResponseMagic[:]) {
		return managerNodeConfigRPCResponse{}, fmt.Errorf("internal/access/node: invalid manager node config response codec")
	}
	var resp managerNodeConfigRPCResponse
	if err := json.Unmarshal(body[len(managerNodeConfigResponseMagic):], &resp); err != nil {
		return managerNodeConfigRPCResponse{}, err
	}
	return resp, nil
}
```

Add `internal/access/node/manager_node_config_rpc.go`:

```go
package node

import (
	"context"
	"errors"
	"fmt"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ManagerNodeConfigRPCServiceID is the cluster RPC service for node-local effective config reads.
const ManagerNodeConfigRPCServiceID uint8 = clusternet.RPCManagerNodeConfig

// ManagerNodeConfigReader reads node-local effective config snapshots.
type ManagerNodeConfigReader interface {
	NodeConfigSnapshot(context.Context, uint64) (managementusecase.NodeConfigSnapshot, error)
}

// HandleManagerNodeConfigRPC handles one encoded manager node config RPC payload.
func (a *Adapter) HandleManagerNodeConfigRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeManagerNodeConfigRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("manager node config rpc decode failed",
			wklog.Event("internal.access.node.manager_node_config_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	if a == nil || a.managerNodeConfig == nil {
		return encodeManagerNodeConfigResponse(managerNodeConfigRPCResponse{Status: rpcStatusUnavailable})
	}
	snapshot, err := a.managerNodeConfig.NodeConfigSnapshot(ctx, req.NodeID)
	status := managerNodeConfigRPCStatusForError(err)
	a.logManagerNodeConfigError(req, status, err)
	return encodeManagerNodeConfigResponse(managerNodeConfigRPCResponse{Status: status, Snapshot: snapshot})
}

// GetManagerNodeConfig reads one node's effective startup config through RPC.
func (c *Client) GetManagerNodeConfig(ctx context.Context, nodeID uint64) (managementusecase.NodeConfigSnapshot, error) {
	resp, err := c.callManagerNodeConfig(ctx, nodeID, managerNodeConfigRPCRequest{NodeID: nodeID})
	if err != nil {
		return managementusecase.NodeConfigSnapshot{}, err
	}
	if err := managerNodeConfigRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.NodeConfigSnapshot{}, err
	}
	return resp.Snapshot, nil
}

func (c *Client) callManagerNodeConfig(ctx context.Context, nodeID uint64, req managerNodeConfigRPCRequest) (managerNodeConfigRPCResponse, error) {
	if c == nil || c.node == nil {
		return managerNodeConfigRPCResponse{}, fmt.Errorf("internal/access/node: manager node config rpc client not configured")
	}
	body, err := encodeManagerNodeConfigRequest(req)
	if err != nil {
		return managerNodeConfigRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, ManagerNodeConfigRPCServiceID, body)
	if err != nil {
		return managerNodeConfigRPCResponse{}, err
	}
	return decodeManagerNodeConfigResponse(respBody)
}

func managerNodeConfigRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, metadb.ErrInvalidArgument):
		return rpcStatusInvalidRequest
	case errors.Is(err, managementusecase.ErrNodeConfigUnavailable):
		return rpcStatusUnavailable
	default:
		return rpcStatusRejected
	}
}

func managerNodeConfigRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case rpcStatusInvalidRequest:
		return metadb.ErrInvalidArgument
	case rpcStatusUnavailable, rpcStatusRejected:
		return managementusecase.ErrNodeConfigUnavailable
	default:
		return fmt.Errorf("internal/access/node: unknown manager node config rpc status %q", status)
	}
}

func (a *Adapter) logManagerNodeConfigError(req managerNodeConfigRPCRequest, status string, err error) {
	if err == nil || status != rpcStatusRejected {
		return
	}
	a.rpcLogger().Warn("manager node config rpc rejected",
		wklog.Event("internal.access.node.manager_node_config_rejected"),
		wklog.String("status", status),
		wklog.Uint64("nodeID", req.NodeID),
		wklog.Error(err),
	)
}
```

- [ ] **Step 4: Run RPC tests and verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/access/node -run ManagerNodeConfig -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 4**

```bash
git add pkg/cluster/net/ids.go internal/access/node/presence_rpc.go internal/access/node/manager_node_config_codec.go internal/access/node/manager_node_config_rpc.go internal/access/node/manager_node_config_rpc_test.go
git commit -m "feat(node): add manager node config rpc"
```

---

### Task 5: Cluster Infra Routing

**Files:**
- Create: `internal/infra/cluster/management_node_config.go`
- Create: `internal/infra/cluster/management_node_config_test.go`

- [ ] **Step 1: Write failing infra routing tests**

Add `internal/infra/cluster/management_node_config_test.go`:

```go
package cluster

import (
	"context"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagementNodeConfigReaderUsesLocalProviderForLocalNode(t *testing.T) {
	local := &localNodeConfigReaderStub{snapshot: managementusecase.NodeConfigSnapshot{NodeID: 1}}
	node := &fakeManagementNodeConfigNode{nodeID: 1}
	reader := NewManagementNodeConfigReader(node, local)

	got, err := reader.NodeConfigSnapshot(context.Background(), 1)
	if err != nil {
		t.Fatalf("NodeConfigSnapshot(local) error = %v", err)
	}
	if got.NodeID != 1 || local.nodeID != 1 || node.called {
		t.Fatalf("local=%d got=%#v remoteCalled=%v", local.nodeID, got, node.called)
	}
}

func TestManagementNodeConfigReaderRoutesRemoteNode(t *testing.T) {
	expected := managementusecase.NodeConfigSnapshot{NodeID: 2}
	adapter := accessnode.New(accessnode.Options{ManagerNodeConfig: &localNodeConfigReaderStub{snapshot: expected}})
	node := &fakeManagementNodeConfigNode{nodeID: 1, handler: adapter.HandleManagerNodeConfigRPC}
	reader := NewManagementNodeConfigReader(node, &localNodeConfigReaderStub{snapshot: managementusecase.NodeConfigSnapshot{NodeID: 1}})

	got, err := reader.NodeConfigSnapshot(context.Background(), 2)
	if err != nil {
		t.Fatalf("NodeConfigSnapshot(remote) error = %v", err)
	}
	if got.NodeID != 2 || node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerNodeConfigRPCServiceID {
		t.Fatalf("remote got=%#v call=node:%d service:%d", got, node.calledNodeID, node.calledServiceID)
	}
}

type localNodeConfigReaderStub struct {
	nodeID   uint64
	snapshot managementusecase.NodeConfigSnapshot
}

func (r *localNodeConfigReaderStub) NodeConfigSnapshot(_ context.Context, nodeID uint64) (managementusecase.NodeConfigSnapshot, error) {
	r.nodeID = nodeID
	return r.snapshot, nil
}

type fakeManagementNodeConfigNode struct {
	nodeID          uint64
	called          bool
	calledNodeID    uint64
	calledServiceID uint8
	handler         func(context.Context, []byte) ([]byte, error)
}

func (f *fakeManagementNodeConfigNode) NodeID() uint64 { return f.nodeID }

func (f *fakeManagementNodeConfigNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.called = true
	f.calledNodeID = nodeID
	f.calledServiceID = serviceID
	return f.handler(ctx, payload)
}
```

- [ ] **Step 2: Run infra tests and verify RED**

Run:

```bash
GOWORK=off go test ./internal/infra/cluster -run ManagementNodeConfig -count=1
```

Expected: FAIL because `NewManagementNodeConfigReader` does not exist.

- [ ] **Step 3: Implement local/remote routing**

Add `internal/infra/cluster/management_node_config.go`:

```go
package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

// ManagementNodeConfigRPCNode exposes cluster node RPC for manager node config reads.
type ManagementNodeConfigRPCNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// LocalNodeConfigReader reads this node's redacted effective startup config.
type LocalNodeConfigReader interface {
	NodeConfigSnapshot(context.Context, uint64) (managementusecase.NodeConfigSnapshot, error)
}

// ManagementNodeConfigReader routes node config reads to local or remote nodes.
type ManagementNodeConfigReader struct {
	node   ManagementNodeConfigRPCNode
	local LocalNodeConfigReader
	remote *accessnode.Client
}

// NewManagementNodeConfigReader creates a selected-node config reader.
func NewManagementNodeConfigReader(node ManagementNodeConfigRPCNode, local LocalNodeConfigReader) *ManagementNodeConfigReader {
	return &ManagementNodeConfigReader{
		node:   node,
		local:  local,
		remote: accessnode.NewClient(node),
	}
}

// NodeConfigSnapshot returns one selected node's redacted effective startup config.
func (r *ManagementNodeConfigReader) NodeConfigSnapshot(ctx context.Context, nodeID uint64) (managementusecase.NodeConfigSnapshot, error) {
	if r == nil {
		return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
	}
	if r.isLocal(nodeID) {
		if r.local == nil {
			return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
		}
		return r.local.NodeConfigSnapshot(ctx, nodeID)
	}
	if r.remote == nil {
		return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
	}
	return r.remote.GetManagerNodeConfig(ctx, nodeID)
}

func (r *ManagementNodeConfigReader) isLocal(nodeID uint64) bool {
	return r.node == nil || nodeID == r.node.NodeID()
}
```

- [ ] **Step 4: Run infra tests and verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/infra/cluster -run ManagementNodeConfig -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 5**

```bash
git add internal/infra/cluster/management_node_config.go internal/infra/cluster/management_node_config_test.go
git commit -m "feat(cluster): route manager node config reads"
```

---

### Task 6: App Wiring

**Files:**
- Modify: `internal/app/app.go`
- Modify: `internal/app/wiring.go`
- Modify: `internal/app/app_test.go`

- [ ] **Step 1: Write failing app wiring test**

Add to `internal/app/app_test.go` near the other manager RPC registration tests:

```go
func TestNewRegistersManagerNodeConfigRPC(t *testing.T) {
	cluster := &fakeManagerCluster{nodeID: 1}

	_, err := newTestApp(t, Config{NodeID: 1}, WithCluster(cluster), WithGateway(nil))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, ok := cluster.registeredHandlers[accessnode.ManagerNodeConfigRPCServiceID]; !ok {
		t.Fatalf("manager node config rpc handler not registered")
	}
}
```

- [ ] **Step 2: Run app wiring test and verify RED**

Run:

```bash
GOWORK=off go test ./internal/app -run 'ManagerNodeConfigRPC|NodeConfigSnapshot' -count=1
```

Expected: FAIL because the app does not register `ManagerNodeConfigRPCServiceID`.

- [ ] **Step 3: Wire local provider, management reader, and RPC registration**

Modify `internal/app/app.go` in the startup wiring sequence:

```go
app.wireManagerNodeConfigRPC()
```

Place it near `wireManagerAppLogRPC()` and before `wireNodeLifecycleRPC()`.

Add to `internal/app/wiring.go`:

```go
func (a *App) wireManagerNodeConfigRPC() {
	registrar, hasRegistrar := a.cluster.(nodeRPCRegistrar)
	if !hasRegistrar {
		return
	}
	adapter := accessnode.New(accessnode.Options{ManagerNodeConfig: a, Logger: a.logger.Named("node")})
	registrar.RegisterRPC(accessnode.ManagerNodeConfigRPCServiceID, nodeRPCHandlerFunc(adapter.HandleManagerNodeConfigRPC))
}
```

Modify `newManagerManagement()` after the application-log wiring:

```go
opts.NodeConfig = a
if rpcNode, ok := a.cluster.(clusterinfra.ManagementNodeConfigRPCNode); ok {
	opts.NodeConfig = clusterinfra.NewManagementNodeConfigReader(rpcNode, a)
}
```

- [ ] **Step 4: Run app wiring tests and verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/app -run 'ManagerNodeConfigRPC|NodeConfigSnapshot' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 6**

```bash
git add internal/app/app.go internal/app/wiring.go internal/app/app_test.go
git commit -m "feat(app): wire manager node config reads"
```

---

### Task 7: Frontend API Client

**Files:**
- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing web API client test**

Add to `web/src/lib/manager-api.test.ts` near the node API tests:

```ts
it("fetches node config from the manager node config endpoint", async () => {
  const payload = {
    generated_at: "2026-07-08T10:00:00Z",
    node_id: 2,
    source: "effective_startup_config",
    requires_restart: true,
    groups: [{
      id: "cluster",
      title: "Cluster",
      items: [{
        key: "WK_CLUSTER_HASH_SLOT_COUNT",
        label: "Hash slot count",
        value: "256",
        sensitive: false,
        redacted: false,
      }],
    }],
  }
  fetchMock.mockResolvedValueOnce(new Response(JSON.stringify(payload), { status: 200 }))

  await expect(getNodeConfig(2)).resolves.toEqual(payload)

  expect(fetchMock).toHaveBeenCalledWith(
    "/manager/nodes/2/config",
    expect.objectContaining({ headers: expect.any(Headers) }),
  )
})
```

- [ ] **Step 2: Run web client test and verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/lib/manager-api.test.ts -t "fetches node config"
```

Expected: FAIL because `getNodeConfig` and the response type do not exist.

- [ ] **Step 3: Implement web types and client method**

Add to `web/src/lib/manager-api.types.ts`:

```ts
export type ManagerNodeConfigItem = {
  key: string
  label: string
  value: string
  sensitive: boolean
  redacted: boolean
}

export type ManagerNodeConfigGroup = {
  id: string
  title: string
  items: ManagerNodeConfigItem[]
}

export type ManagerNodeConfigResponse = {
  generated_at: string
  node_id: number
  source: string
  requires_restart: boolean
  groups: ManagerNodeConfigGroup[]
}
```

Import `ManagerNodeConfigResponse` in `web/src/lib/manager-api.ts` and add:

```ts
export function getNodeConfig(nodeId: number) {
  return jsonManagerFetch<ManagerNodeConfigResponse>(`/manager/nodes/${nodeId}/config`)
}
```

- [ ] **Step 4: Run web client test and verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/lib/manager-api.test.ts -t "fetches node config"
```

Expected: PASS.

- [ ] **Step 5: Commit Task 7**

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat(web): add node config api client"
```

---

### Task 8: Node Detail UI

**Files:**
- Modify: `web/src/pages/nodes/page.tsx`
- Modify: `web/src/pages/nodes/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing node detail UI tests**

Modify `web/src/pages/nodes/page.test.tsx`:

Add mock:

```ts
const getNodeConfigMock = vi.fn()
```

Add it to the `vi.mock("@/lib/manager-api")` return:

```ts
getNodeConfig: (...args: unknown[]) => getNodeConfigMock(...args),
```

Reset it in `beforeEach()`:

```ts
getNodeConfigMock.mockReset()
getNodeConfigMock.mockResolvedValue(nodeConfig)
```

Add fixture:

```ts
const nodeConfig = {
  generated_at: "2026-07-08T10:00:00Z",
  node_id: 1,
  source: "effective_startup_config",
  requires_restart: true,
  groups: [{
    id: "cluster",
    title: "Cluster",
    items: [
      { key: "WK_CLUSTER_HASH_SLOT_COUNT", label: "Hash slot count", value: "256", sensitive: false, redacted: false },
      { key: "WK_MANAGER_JWT_SECRET", label: "Manager JWT secret", value: "******", sensitive: true, redacted: true },
    ],
  }],
}
```

Add test:

```ts
test("loads and renders redacted node config in the detail sheet", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  getNodeMock.mockResolvedValueOnce(nodeDetail)
  getNodeConfigMock.mockResolvedValueOnce(nodeConfig)

  const user = userEvent.setup()
  renderNodesPage()

  await user.click(await screen.findByRole("button", { name: "Inspect node 1" }))

  expect(getNodeConfigMock).toHaveBeenCalledWith(1)
  expect(await screen.findByText("Effective configuration")).toBeInTheDocument()
  expect(screen.getByText("WK_CLUSTER_HASH_SLOT_COUNT")).toBeInTheDocument()
  expect(screen.getByText("256")).toBeInTheDocument()
  expect(screen.getByText("WK_MANAGER_JWT_SECRET")).toBeInTheDocument()
  expect(screen.getByText("******")).toBeInTheDocument()
  expect(screen.getByText("Redacted")).toBeInTheDocument()
  expect(screen.queryByText("super-secret")).not.toBeInTheDocument()
})
```

Add config error isolation test:

```ts
test("keeps node detail visible when node config request fails", async () => {
  getNodesMock.mockResolvedValueOnce({
    generated_at: "2026-04-23T08:00:01Z",
    controller_leader_id: 1,
    total: 1,
    items: [nodeRow],
  })
  getNodeMock.mockResolvedValueOnce(nodeDetail)
  getNodeConfigMock.mockRejectedValueOnce(new ManagerApiError(503, "service_unavailable", "node config unavailable"))

  const user = userEvent.setup()
  renderNodesPage()

  await user.click(await screen.findByRole("button", { name: "Inspect node 1" }))

  expect(await screen.findByText("Hosted IDs")).toBeInTheDocument()
  expect(await screen.findByText("Effective configuration")).toBeInTheDocument()
  expect(screen.getByText("node config unavailable")).toBeInTheDocument()
})
```

- [ ] **Step 2: Run node page tests and verify RED**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/nodes/page.test.tsx -t "node config"
```

Expected: FAIL because the page does not call `getNodeConfig` or render config.

- [ ] **Step 3: Implement config state and rendering**

Modify imports in `web/src/pages/nodes/page.tsx`:

```ts
import type {
  ManagerNode,
  ManagerNodeConfigResponse,
  ManagerNodeDetailResponse,
  ManagerNodesResponse,
} from "@/lib/manager-api.types"
```

Add `getNodeConfig` to manager-api imports.

Add state type:

```ts
type NodeConfigState = {
  config: ManagerNodeConfigResponse | null
  loading: boolean
  error: Error | null
}
```

Add state in `NodeClusterListPanel`:

```ts
const [configState, setConfigState] = useState<NodeConfigState>({
  config: null,
  loading: false,
  error: null,
})
```

Add loader:

```ts
const loadNodeConfig = useCallback(async (nodeId: number) => {
  setConfigState({ config: null, loading: true, error: null })
  try {
    const config = await getNodeConfig(nodeId)
    setConfigState({ config, loading: false, error: null })
  } catch (error) {
    setConfigState({
      config: null,
      loading: false,
      error: error instanceof Error ? error : new Error("node config failed"),
    })
  }
}, [])
```

Call it from `openDetail` alongside `loadNodeDetail`:

```ts
setSelectedNodeId(nodeId)
await Promise.all([loadNodeDetail(nodeId), loadNodeConfig(nodeId)])
```

Clear it in `closeDetail(false)`:

```ts
setConfigState({ config: null, loading: false, error: null })
```

Add render helper above `NodeClusterListPanel`:

```tsx
function NodeConfigPanel({ state }: { state: NodeConfigState }) {
  const intl = useIntl()
  return (
    <section className="mt-4 rounded-md border border-border bg-card p-3" data-node-surface="config">
      <div className="flex flex-col gap-1 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h3 className="text-sm font-semibold text-foreground">
            {intl.formatMessage({ id: "nodes.config.title" })}
          </h3>
          {state.config ? (
            <p className="text-xs text-muted-foreground">
              {intl.formatMessage(
                { id: "nodes.config.meta" },
                { source: state.config.source, nodeId: state.config.node_id },
              )}
            </p>
          ) : null}
        </div>
        {state.config?.requires_restart ? (
          <span className="rounded-md border border-border bg-muted px-2 py-1 text-xs text-muted-foreground">
            {intl.formatMessage({ id: "nodes.config.requiresRestart" })}
          </span>
        ) : null}
      </div>
      {state.loading ? <div className="mt-3"><ResourceState kind="loading" title={intl.formatMessage({ id: "nodes.config.title" })} /></div> : null}
      {!state.loading && state.error ? (
        <div className="mt-3">
          <ResourceState
            kind={mapErrorKind(state.error)}
            title={intl.formatMessage({ id: "nodes.config.title" })}
            description={state.error.message}
          />
        </div>
      ) : null}
      {!state.loading && !state.error && state.config ? (
        <div className="mt-3 space-y-4">
          {state.config.groups.map((group) => (
            <div className="overflow-hidden rounded-md border border-border" key={group.id}>
              <div className="border-b border-border bg-muted/30 px-3 py-2 text-xs font-medium uppercase tracking-[0.14em] text-muted-foreground">
                {group.title}
              </div>
              <table className="w-full border-collapse text-sm">
                <tbody>
                  {group.items.map((item) => (
                    <tr className="border-t border-border first:border-t-0" key={`${group.id}:${item.key}`}>
                      <td className="w-[38%] px-3 py-2 font-mono text-xs text-muted-foreground">{item.key}</td>
                      <td className="w-[28%] px-3 py-2 text-muted-foreground">{item.label}</td>
                      <td className="px-3 py-2 text-foreground">
                        <span className="font-mono text-xs">{item.value || "-"}</span>
                        {item.redacted ? (
                          <span className="ml-2 rounded-md border border-border bg-muted px-2 py-0.5 text-xs text-muted-foreground">
                            {intl.formatMessage({ id: "nodes.config.redacted" })}
                          </span>
                        ) : null}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ))}
        </div>
      ) : null}
    </section>
  )
}
```

Render it after the existing detail `KeyValueList` block:

```tsx
<NodeConfigPanel state={configState} />
```

Add i18n keys to `web/src/i18n/messages/en.ts`:

```ts
"nodes.config.title": "Effective configuration",
"nodes.config.meta": "Source {source} / node {nodeId}",
"nodes.config.requiresRestart": "Requires restart",
"nodes.config.redacted": "Redacted",
```

Add i18n keys to `web/src/i18n/messages/zh-CN.ts`:

```ts
"nodes.config.title": "有效配置",
"nodes.config.meta": "来源 {source} / 节点 {nodeId}",
"nodes.config.requiresRestart": "需要重启生效",
"nodes.config.redacted": "已脱敏",
```

- [ ] **Step 4: Run node page tests and verify GREEN**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/pages/nodes/page.test.tsx -t "node config"
```

Expected: PASS.

- [ ] **Step 5: Commit Task 8**

```bash
git add web/src/pages/nodes/page.tsx web/src/pages/nodes/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "feat(web): show node config in detail"
```

---

### Task 9: Flow Docs And Focused Verification

**Files:**
- Modify: `internal/access/manager/FLOW.md`
- Modify: `internal/usecase/management/FLOW.md`
- Modify: `internal/infra/cluster/FLOW.md`
- Modify: `internal/access/node/FLOW.md`
- Modify: `internal/app/FLOW.md`

- [ ] **Step 1: Update FLOW docs**

Apply these exact documentation updates:

- In `internal/access/manager/FLOW.md`, add route line:

```text
GET  /manager/nodes/:node_id/config (read-only redacted effective startup config; requires cluster.node:r when Auth.On=true)
```

Add paragraph near `/manager/nodes`:

```text
`/manager/nodes/:node_id/config` returns a selected node's allowlisted,
redacted effective startup configuration. The route validates a positive node
ID, delegates node existence and local/remote routing to the management
usecase, and never returns raw environment values, manager secrets, join tokens,
or manager user password payloads. It is read-only and all shown settings
require restart to change.
```

- In `internal/usecase/management/FLOW.md`, add node-config to the responsibility list and add:

```text
## Node Config Flow

```text
manager HTTP handler
  -> management.App.NodeConfigSnapshot
  -> ControlSnapshotReader.LocalControlSnapshot
  -> NodeConfigReader.NodeConfigSnapshot
  -> local app provider or selected-node RPC
```

The usecase verifies the selected node exists in the local control snapshot
before delegating. It returns only bounded, already-formatted DTO groups and
does not parse files, inspect environment variables, or expose secret material.
```
```

- In `internal/infra/cluster/FLOW.md`, add:

```text
Manager node config reads route local node IDs to the app-owned config snapshot
provider and remote node IDs through manager node-config RPC. The infra adapter
does not reshape or unredact values.
```

- In `internal/access/node/FLOW.md`, add:

```text
Manager node-config RPC exposes only the receiver node's local redacted
effective startup config provider. It is not a recursive forwarding endpoint.
```

- In `internal/app/FLOW.md`, add to construction flow:

```text
  -> register the manager node-config RPC handler when node RPC is available,
     exposing this node's allowlisted and redacted effective startup
     configuration to peer manager readers without reading raw config files or
     environment source values
```

- [ ] **Step 2: Run full focused backend verification**

Run:

```bash
GOWORK=off go test ./internal/app ./internal/access/manager ./internal/access/node ./internal/usecase/management ./internal/infra/cluster -run 'NodeConfig|ManagerNodeConfig|ConfigSnapshot' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run focused frontend verification**

Run:

```bash
cd web && /Users/tt/.bun/bin/bun run test -- src/lib/manager-api.test.ts src/pages/nodes/page.test.tsx
```

Expected: PASS.

- [ ] **Step 4: Run TypeScript check**

Run:

```bash
cd web && /Users/tt/.bun/bin/bunx tsc -b
```

Expected: PASS.

- [ ] **Step 5: Run diff checks**

Run:

```bash
git diff --check
git diff --cached --check
```

Expected: both commands produce no output.

- [ ] **Step 6: Commit docs and any verification-only fixups**

```bash
git add internal/access/manager/FLOW.md internal/usecase/management/FLOW.md internal/infra/cluster/FLOW.md internal/access/node/FLOW.md internal/app/FLOW.md
git commit -m "docs: document node config manager flow"
```

---

## Final Verification

Run this complete focused set before claiming the feature is done:

```bash
GOWORK=off go test ./internal/app ./internal/access/manager ./internal/access/node ./internal/usecase/management ./internal/infra/cluster -run 'NodeConfig|ManagerNodeConfig|ConfigSnapshot' -count=1
cd web && /Users/tt/.bun/bin/bun run test -- src/lib/manager-api.test.ts src/pages/nodes/page.test.tsx
cd web && /Users/tt/.bun/bin/bunx tsc -b
git diff --check
git status --short
```

Expected final state:

- Backend focused tests pass.
- Frontend focused tests pass.
- TypeScript build passes.
- `git diff --check` is clean.
- `git status --short` contains only intentional files, or is clean after task commits.
