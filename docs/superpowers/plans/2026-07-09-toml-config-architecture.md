# TOML Config Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `wukongim.conf` startup files with schema-backed TOML files while keeping strict `WK_*` environment variable overrides.

**Architecture:** Add `internal/config` as the product startup configuration layer. It owns TOML decoding, `WK_*` env overlay, unknown-key checks, source metadata, and mapping into `app.Config`; `internal/app` stays format-agnostic and serves a bounded safe startup-config snapshot supplied on `app.Config`.

**Tech Stack:** Go 1.25, `github.com/pelletier/go-toml/v2`, existing `internal/app.Config`, existing `pkg/cluster` and `pkg/gateway` config DTOs, Go table tests, Docker Compose config files.

---

## Preconditions

The repository may already contain unrelated local changes. Each task stages only the files named in that task. Do not run broad formatting or cleanup across unrelated files.

Read the approved spec before starting:

```bash
sed -n '1,260p' docs/superpowers/specs/2026-07-09-toml-config-architecture-design.md
```

## File Structure

- Create `internal/config/types.go`
  - Public loader API, source metadata types, normalized snapshot DTO helpers.
- Create `internal/config/schema.go`
  - One schema list binding TOML paths to `WK_*` env keys, type, group, label, sensitivity, nullable/required flags, and removed-key replacements.
- Create `internal/config/toml.go`
  - Strict TOML decoding into flat TOML paths, unknown TOML key validation, and conversion from TOML values to canonical string values.
- Create `internal/config/env.go`
  - Strict `WK_*` environment scan and overlay.
- Create `internal/config/build.go`
  - App config defaults, scalar parsing helpers, JSON list parsing helpers, and mapping into `app.Config`. This file starts by moving the existing `cmd/wukongim/config.go` builder logic, then replacing the old file reader with normalized values from schema.
- Create `internal/config/snapshot.go`
  - Builds a bounded, redacted `management.NodeConfigSnapshot` from schema metadata and normalized effective values.
- Create `internal/config/example_test.go`
  - Verifies `wukongim.toml.example` paths exist in schema and the example loads.
- Create `internal/config/loader_test.go`
  - Covers lookup order, explicit config path, TOML parsing, env-only startup, env override, unknown keys, removed keys, empty required values, and list JSON overrides.
- Modify `cmd/wukongim/config.go`
  - Shrink to flag parsing and delegation to `internal/config.Load`.
- Modify `cmd/wukongim/config_test.go`
  - Remove `.conf`-specific tests or move them to `internal/config`; keep command-level smoke tests.
- Modify `internal/app/config.go`
  - Add a format-agnostic startup config snapshot field to `Config`.
- Modify `internal/app/node_config.go`
  - Return the loader-supplied bounded snapshot when present; keep node ID validation and generated timestamp inside app.
- Modify `internal/app/node_config_test.go`
  - Assert snapshot returns supplied schema-derived groups and still redacts secrets.
- Rename `wukongim.conf.example` to `wukongim.toml.example`.
- Rename `cmd/wukongim/wukongim.conf.example` to `cmd/wukongim/wukongim.toml.example` if it is still needed by tests.
- Rename `docker/conf/node1.conf`, `docker/conf/node2.conf`, and `docker/conf/node3.conf` to `.toml`.
- Modify `Dockerfile`
  - Default config path becomes `/etc/wukongim/wukongim.toml`.
- Modify `docker-compose.yml`
  - Mount `.toml` files.
- Modify `docker/compose_defaults_test.go`
  - Load TOML through the real loader and assert effective values.
- Modify `scripts/wukongim/*`
  - Replace `.conf` paths with `.toml` paths.
- Modify `AGENTS.md`, `internal/app/FLOW.md`, and `docs/development/PROJECT_KNOWLEDGE.md`
  - Update the configuration convention.

### Key Boundary

`internal/config` may import `internal/app` and `internal/usecase/management`. `internal/app` must not import `internal/config`.

## Task 1: Add Loader API Skeleton

**Files:**
- Create: `internal/config/types.go`
- Create: `internal/config/loader_test.go`
- Modify: `go.mod`

- [ ] **Step 1: Add failing tests for default path names**

Create `internal/config/loader_test.go` with:

```go
package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfigPathsUseTOML(t *testing.T) {
	got := DefaultPaths()
	want := []string{"./wukongim.toml", "./conf/wukongim.toml", "/etc/wukongim/wukongim.toml"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("DefaultPaths() = %v, want %v", got, want)
	}
}

func TestLoadExplicitMissingFileReportsPath(t *testing.T) {
	_, err := Load(Options{Args: []string{"-config", filepath.Join(t.TempDir(), "missing.toml")}})
	if err == nil {
		t.Fatal("Load() error = nil, want missing explicit config error")
	}
	if !strings.Contains(err.Error(), "missing.toml") {
		t.Fatalf("Load() error = %v, want explicit path", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./internal/config -run 'TestDefaultConfigPathsUseTOML|TestLoadExplicitMissingFileReportsPath' -count=1
```

Expected: FAIL because `internal/config` does not exist.

- [ ] **Step 3: Add the loader API skeleton**

Create `internal/config/types.go` with:

```go
package config

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/WuKongIM/WuKongIM/internal/app"
)

var defaultConfigPaths = []string{
	"./wukongim.toml",
	"./conf/wukongim.toml",
	"/etc/wukongim/wukongim.toml",
}

// Options configures product startup config loading.
type Options struct {
	// Args are command-line arguments after the binary name.
	Args []string
	// Environ overrides the process environment for tests. Empty uses os.Environ.
	Environ []string
}

// DefaultPaths returns the implicit TOML config lookup order.
func DefaultPaths() []string {
	out := make([]string, len(defaultConfigPaths))
	copy(out, defaultConfigPaths)
	return out
}

// Load reads TOML config and WK_* environment overrides into app.Config.
func Load(opts Options) (app.Config, error) {
	configPath, err := parseConfigPath(opts.Args)
	if err != nil {
		return app.Config{}, err
	}
	if configPath != "" {
		if _, err := os.Stat(configPath); err != nil {
			return app.Config{}, fmt.Errorf("load config: read %s: %w", configPath, err)
		}
		return app.Config{}, fmt.Errorf("load config: TOML parser not wired")
	}
	return app.Config{}, fmt.Errorf("load config: no default config file found (tried %s)", strings.Join(defaultConfigPaths, ", "))
}

func parseConfigPath(args []string) (string, error) {
	fs := flag.NewFlagSet("wukongim", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "path to wukongim.toml file")
	if err := fs.Parse(args); err != nil {
		return "", fmt.Errorf("parse flags: %w", err)
	}
	return strings.TrimSpace(*configPath), nil
}
```

Move `github.com/pelletier/go-toml/v2` to the direct `require` block when it is first imported in Task 2.

- [ ] **Step 4: Run tests to verify skeleton behavior**

Run:

```bash
GOWORK=off go test ./internal/config -run 'TestDefaultConfigPathsUseTOML|TestLoadExplicitMissingFileReportsPath' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/types.go internal/config/loader_test.go go.mod go.sum
git commit -m "feat(config): add toml loader api skeleton"
```

## Task 2: Add Schema And TOML Flattening

**Files:**
- Create: `internal/config/schema.go`
- Create: `internal/config/toml.go`
- Modify: `internal/config/loader_test.go`
- Modify: `internal/config/types.go`
- Modify: `go.mod`

- [ ] **Step 1: Add failing tests for TOML parsing and unknown keys**

Append to `internal/config/loader_test.go`:

```go
func TestLoadMinimalTOMLConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.toml")
	writeFile(t, path, `
[node]
id = 1
data_dir = "`+dir+`/node1"

[cluster]
listen_addr = "127.0.0.1:7001"
hash_slot_count = 256
`)

	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.NodeID != 1 || cfg.Cluster.NodeID != 1 {
		t.Fatalf("NodeID = %d/%d, want 1", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.DataDir != dir+"/node1" || cfg.Cluster.ListenAddr != "127.0.0.1:7001" {
		t.Fatalf("loaded config = %#v", cfg)
	}
}

func TestLoadRejectsUnknownTOMLKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.toml")
	writeFile(t, path, `
[node]
id = 1
data_dir = "`+dir+`/node1"

[cluster]
listen_addr = "127.0.0.1:7001"
hash_slots_count = 256
`)

	_, err := Load(Options{Args: []string{"-config", path}, Environ: cleanEnv()})
	if err == nil {
		t.Fatal("Load() error = nil, want unknown key")
	}
	if !strings.Contains(err.Error(), "cluster.hash_slots_count") {
		t.Fatalf("Load() error = %v, want unknown TOML path", err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func cleanEnv() []string {
	return []string{"PATH=" + os.Getenv("PATH")}
}
```

Add imports to `internal/config/loader_test.go`:

```go
import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./internal/config -run 'TestLoadMinimalTOMLConfig|TestLoadRejectsUnknownTOMLKey' -count=1
```

Expected: FAIL with `TOML parser not wired`.

- [ ] **Step 3: Add schema primitives**

Create `internal/config/schema.go` with:

```go
package config

type fieldKind string

const (
	kindString     fieldKind = "string"
	kindBool       fieldKind = "bool"
	kindInt        fieldKind = "int"
	kindUint64     fieldKind = "uint64"
	kindUint32     fieldKind = "uint32"
	kindUint16     fieldKind = "uint16"
	kindFloat      fieldKind = "float"
	kindDuration   fieldKind = "duration"
	kindStringList fieldKind = "string_list"
	kindObjectList fieldKind = "object_list"
)

type fieldSpec struct {
	TOMLPath    string
	EnvKey      string
	Kind        fieldKind
	Group       string
	Label       string
	Description string
	Required    bool
	Nullable    bool
	Sensitive   bool
}

var removedConfigKeyReplacements = map[string]string{
	"WK_CLUSTER_GROUP_COUNT":                 "WK_CLUSTER_INITIAL_SLOT_COUNT",
	"WK_CLUSTER_GROUP_REPLICA_N":             "WK_CLUSTER_SLOT_REPLICA_N",
	"WK_CLUSTER_HASH_SLOT_MIGRATION_ENABLED": "WK_CHANNEL_MIGRATION_*",
}

var schemaFields = []fieldSpec{
	{TOMLPath: "node.id", EnvKey: "WK_NODE_ID", Kind: kindUint64, Group: "node", Label: "Node ID", Required: true},
	{TOMLPath: "node.data_dir", EnvKey: "WK_NODE_DATA_DIR", Kind: kindString, Group: "node", Label: "Data directory", Required: true},
	{TOMLPath: "cluster.listen_addr", EnvKey: "WK_CLUSTER_LISTEN_ADDR", Kind: kindString, Group: "cluster", Label: "Cluster listen address", Required: true},
	{TOMLPath: "cluster.id", EnvKey: "WK_CLUSTER_ID", Kind: kindString, Group: "cluster", Label: "Cluster ID"},
	{TOMLPath: "cluster.seeds", EnvKey: "WK_CLUSTER_SEEDS", Kind: kindStringList, Group: "cluster", Label: "Seed addresses"},
	{TOMLPath: "cluster.advertise_addr", EnvKey: "WK_CLUSTER_ADVERTISE_ADDR", Kind: kindString, Group: "cluster", Label: "Cluster advertise address"},
	{TOMLPath: "cluster.join_token", EnvKey: "WK_CLUSTER_JOIN_TOKEN", Kind: kindString, Group: "cluster", Label: "Join token", Sensitive: true},
	{TOMLPath: "cluster.nodes", EnvKey: "WK_CLUSTER_NODES", Kind: kindObjectList, Group: "cluster", Label: "Static cluster nodes"},
	{TOMLPath: "cluster.initial_slot_count", EnvKey: "WK_CLUSTER_INITIAL_SLOT_COUNT", Kind: kindUint32, Group: "cluster", Label: "Initial slot count"},
	{TOMLPath: "cluster.hash_slot_count", EnvKey: "WK_CLUSTER_HASH_SLOT_COUNT", Kind: kindUint16, Group: "cluster", Label: "Hash slot count"},
	{TOMLPath: "cluster.slot_replica_n", EnvKey: "WK_CLUSTER_SLOT_REPLICA_N", Kind: kindUint16, Group: "cluster", Label: "Slot replica count"},
	{TOMLPath: "cluster.channel_replica_n", EnvKey: "WK_CLUSTER_CHANNEL_REPLICA_N", Kind: kindUint16, Group: "cluster", Label: "Channel replica count"},
}

func schemaByTOMLPath() map[string]fieldSpec {
	out := make(map[string]fieldSpec, len(schemaFields))
	for _, field := range schemaFields {
		out[field.TOMLPath] = field
	}
	return out
}

func schemaByEnvKey() map[string]fieldSpec {
	out := make(map[string]fieldSpec, len(schemaFields))
	for _, field := range schemaFields {
		out[field.EnvKey] = field
	}
	return out
}
```

This first schema is intentionally small enough to make the failing tests pass. Task 4 expands it to every current `WK_*` key before `cmd/wukongim` switches to the package.

- [ ] **Step 4: Add TOML flattening**

Create `internal/config/toml.go` with:

```go
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type sourceValues struct {
	values  map[string]string
	sources map[string]string
}

func readTOMLValues(path string) (sourceValues, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return sourceValues{}, fmt.Errorf("read %s: %w", path, err)
	}
	var raw map[string]any
	if err := toml.Unmarshal(body, &raw); err != nil {
		return sourceValues{}, fmt.Errorf("parse %s as TOML: %w", path, err)
	}
	flat := map[string]any{}
	flattenTOML("", raw, flat)
	known := schemaByTOMLPath()
	values := map[string]string{}
	sources := map[string]string{}
	unknown := make([]string, 0)
	for path, value := range flat {
		field, ok := known[path]
		if !ok {
			unknown = append(unknown, path)
			continue
		}
		text, err := tomlValueToString(field, value)
		if err != nil {
			return sourceValues{}, err
		}
		values[field.EnvKey] = text
		sources[field.EnvKey] = "toml"
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return sourceValues{}, fmt.Errorf("unknown config key: %s", strings.Join(unknown, ", "))
	}
	return sourceValues{values: values, sources: sources}, nil
}

func flattenTOML(prefix string, value any, out map[string]any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			flattenTOML(next, child, out)
		}
	case []any:
		out[prefix] = typed
	default:
		out[prefix] = typed
	}
}

func tomlValueToString(field fieldSpec, value any) (string, error) {
	switch field.Kind {
	case kindString, kindDuration:
		text, ok := value.(string)
		if !ok {
			return "", fmt.Errorf("parse %s / %s: value must be a string", field.TOMLPath, field.EnvKey)
		}
		return strings.TrimSpace(text), nil
	case kindBool:
		value, ok := value.(bool)
		if !ok {
			return "", fmt.Errorf("parse %s / %s: value must be a bool", field.TOMLPath, field.EnvKey)
		}
		return strconv.FormatBool(value), nil
	case kindInt, kindUint64, kindUint32, kindUint16:
		switch n := value.(type) {
		case int64:
			return strconv.FormatInt(n, 10), nil
		case int:
			return strconv.Itoa(n), nil
		default:
			return "", fmt.Errorf("parse %s / %s: value must be an integer", field.TOMLPath, field.EnvKey)
		}
	case kindFloat:
		switch n := value.(type) {
		case float64:
			return strconv.FormatFloat(n, 'f', -1, 64), nil
		case int64:
			return strconv.FormatInt(n, 10), nil
		default:
			return "", fmt.Errorf("parse %s / %s: value must be a number", field.TOMLPath, field.EnvKey)
		}
	case kindStringList, kindObjectList:
		data, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("parse %s / %s: %w", field.TOMLPath, field.EnvKey, err)
		}
		return string(data), nil
	default:
		return "", fmt.Errorf("parse %s / %s: unsupported field kind %s", field.TOMLPath, field.EnvKey, field.Kind)
	}
}
```

- [ ] **Step 5: Wire explicit TOML path loading**

Replace the explicit path branch in `internal/config/types.go` `Load` with:

```go
	if configPath != "" {
		values, err := readTOMLValues(configPath)
		if err != nil {
			return app.Config{}, fmt.Errorf("load config: %w", err)
		}
		return buildConfig(values.values)
	}
```

Add `github.com/pelletier/go-toml/v2 v2.2.4` to the direct `require` block in `go.mod` after the first import.

- [ ] **Step 6: Run tests**

Run:

```bash
GOWORK=off go test ./internal/config -run 'TestLoadMinimalTOMLConfig|TestLoadRejectsUnknownTOMLKey' -count=1
```

Expected: FAIL because `buildConfig` has not moved yet.

- [ ] **Step 7: Leave this task uncommitted until Task 3**

Do not commit this task alone because `internal/config` intentionally does not
compile until Task 3 moves `buildConfig`. Keep the edits in the worktree and
start Task 3 immediately.

## Task 3: Move App Config Mapping Into internal/config

**Files:**
- Create: `internal/config/build.go`
- Modify: `internal/config/types.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/main.go`

- [ ] **Step 1: Move the existing builder**

Move these declarations from `cmd/wukongim/config.go` into `internal/config/build.go`:

```text
clusterNodeConfig
requiredConfigKeys
defaultBenchAPIMaxBatchSize
defaultBenchAPIMaxPayloadBytes
buildConfig
defaultGatewayListeners
defaultGatewayGnetOptions
adaptiveGatewayGnetEventLoops
parseListeners
parseClusterNodes
parseClusterSeeds
parseStringList
parseDiagnosticsDebugMatches
parseManagerUsers
clusterVoters
deriveStaticClusterID
parseUint64
parseUint32
parseUint16
parseBool
parseInt
parseInt64
parseFloat
validSampleRate
parseDuration
validateClusterHealthReportConfig
configValue
configKeyPresent
requiredConfigValue
missingRequiredConfigKeys
```

Use package name `config` and keep imports exact for the moved code. The moved file must import:

```go
import (
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/app"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/gateway"
	"github.com/WuKongIM/WuKongIM/pkg/gateway/binding"
)
```

- [ ] **Step 2: Keep cmd/wukongim loader as a delegate**

Replace the contents of `cmd/wukongim/config.go` with:

```go
package main

import (
	"github.com/WuKongIM/WuKongIM/internal/app"
	productconfig "github.com/WuKongIM/WuKongIM/internal/config"
)

func loadConfig(args []string) (app.Config, error) {
	return productconfig.Load(productconfig.Options{Args: args})
}
```

- [ ] **Step 3: Run targeted tests**

Run:

```bash
GOWORK=off go test ./internal/config ./cmd/wukongim -run 'TestLoadMinimalTOMLConfig|TestDefaultConfigPathsUseTOML|TestLoadExplicitMissingFileReportsPath' -count=1
```

Expected: PASS for the new tests. Existing `cmd/wukongim` `.conf` tests may fail in this run if they are selected accidentally; do not update them in this task.

- [ ] **Step 4: Commit Tasks 2 and 3 together**

```bash
git add go.mod go.sum internal/config cmd/wukongim/config.go
git commit -m "feat(config): load toml into app config"
```

## Task 4: Expand Schema To Current Config Surface

**Files:**
- Modify: `internal/config/schema.go`
- Modify: `internal/config/loader_test.go`

- [ ] **Step 1: Add coverage that every builder key has schema**

Add this test to `internal/config/loader_test.go`:

```go
func TestSchemaCoversBuilderKeys(t *testing.T) {
	for _, key := range supportedConfigKeysForBuilder() {
		if _, ok := schemaByEnvKey()[key]; !ok {
			t.Fatalf("schema missing %s", key)
		}
	}
}
```

Add this helper to `internal/config/schema.go` temporarily near the schema:

```go
func supportedConfigKeysForBuilder() []string {
	keys := make([]string, 0, len(schemaFields))
	for _, field := range schemaFields {
		keys = append(keys, field.EnvKey)
	}
	return keys
}
```

Run:

```bash
GOWORK=off go test ./internal/config -run TestSchemaCoversBuilderKeys -count=1
```

Expected: PASS because the helper currently mirrors schema. This test is a guard location; the real check happens in Step 2.

- [ ] **Step 2: Replace helper with the full current key list**

Extract the current key list from the commit before Task 3:

```bash
git show HEAD^:cmd/wukongim/config.go | sed -n '/var supportedConfigKeys = \\[\\]string{/,/^}/p'
```

Replace `supportedConfigKeysForBuilder` with that exact key list, changing only
the function name and wrapper. Include every current key exactly once and do not
include removed keys from `removedConfigKeyReplacements`. The function must
begin like this:

```go
func supportedConfigKeysForBuilder() []string {
	return []string{
		"WK_NODE_ID",
		"WK_NODE_DATA_DIR",
		"WK_CLUSTER_LISTEN_ADDR",
		"WK_CLUSTER_ID",
		"WK_CLUSTER_SEEDS",
		"WK_CLUSTER_ADVERTISE_ADDR",
		"WK_CLUSTER_JOIN_TOKEN",
		"WK_CLUSTER_NODES",
		"WK_CLUSTER_INITIAL_SLOT_COUNT",
		"WK_CLUSTER_HASH_SLOT_COUNT",
		"WK_CLUSTER_SLOT_REPLICA_N",
		"WK_CLUSTER_CHANNEL_REPLICA_N",
	}
}
```

After editing, verify the helper has no duplicate keys:

```bash
go test ./internal/config -run TestSchemaCoversBuilderKeys -count=1
```

Expected: FAIL on a missing schema entry, not on duplicate or malformed helper
code.

- [ ] **Step 3: Run schema coverage and observe failure**

Run:

```bash
GOWORK=off go test ./internal/config -run TestSchemaCoversBuilderKeys -count=1
```

Expected: FAIL on the first missing key after `WK_CLUSTER_CHANNEL_REPLICA_N`.

- [ ] **Step 4: Add schema entries for every current key**

Append `fieldSpec` entries to `schemaFields` until `TestSchemaCoversBuilderKeys` passes. Use these TOML path groups:

```text
cluster.slot_tick_interval
cluster.slot_election_tick
cluster.slot_heartbeat_tick
cluster.slot_log_compaction_enabled
cluster.slot_log_compaction_trigger_entries
cluster.slot_log_compaction_check_interval
cluster.channel_reactor_count
cluster.channel_store_append_workers
cluster.channel_store_append_batch_max_wait
cluster.channel_store_apply_workers
cluster.channel_rpc_workers
cluster.max_channels
cluster.channel_append_batch_max_records
cluster.channel_append_batch_max_wait
cluster.channel_append_batch_adaptive_flush
cluster.channel_append_batch_cold_max_wait
cluster.channel_follower_recovery_probe_interval
cluster.channel_follower_recovery_probe_jitter
cluster.node_health_report_interval
cluster.node_health_report_ttl
channel_migration.enable
channel_migration.scan_interval
channel_migration.scan_limit
channel_migration.max_pages_per_tick
channel_migration.max_tasks_per_tick
channel_migration.task_limit
channel.message_retention_physical_gc_enable
channel.message_retention_scan_interval
channel.message_retention_channel_batch_size
channel.message_retention_max_trim_messages
channel.message_retention_max_trim_bytes
cluster.commit_coordinator_sync
cluster.commit_coordinator_flush_window
cluster.commit_coordinator_max_requests
cluster.commit_coordinator_max_records
cluster.commit_coordinator_max_bytes
cluster.commit_coordinator_shards
api.listen_addr
manager.listen_addr
manager.auth_on
manager.jwt_secret
manager.jwt_issuer
manager.jwt_expire
manager.users
bench.api_enable
bench.api_max_batch_size
bench.api_max_payload_bytes
observability.metrics_enable
prometheus.enable
prometheus.binary_path
prometheus.listen_addr
prometheus.data_dir
prometheus.retention_time
prometheus.retention_size
prometheus.scrape_interval
prometheus.scrape_targets
observability.debug_api_enable
top.api_enable
top.collect_interval
top.history_window
diagnostics.enable
diagnostics.buffer_size
diagnostics.sample_rate
diagnostics.slow_threshold_ms
diagnostics.error_sample_rate
diagnostics.deep_sample_rate
diagnostics.deep_slow_threshold_ms
diagnostics.deep_max_items_per_batch
diagnostics.debug_matches
api.external_tcp_addr
api.external_ws_addr
api.external_wss_addr
gateway.gnet_multicore
gateway.gnet_num_event_loop
gateway.runtime_async_send_workers
gateway.runtime_async_send_queue_capacity
gateway.runtime_async_auth_workers
gateway.runtime_async_auth_queue_capacity
gateway.runtime_async_pool_release_timeout
gateway.default_session_async_send_batch_max_wait
gateway.default_session_async_send_batch_max_records
gateway.default_session_async_send_batch_max_bytes
gateway.listeners
gateway.send_timeout
message.person_whitelist_enabled
message.system_device_id
message.permission_cache_ttl
presence.activation_timeout
presence.touch_flush_interval
presence.touch_batch_size
presence.route_ttl
conversation.max_last_message_concurrency
conversation.authority_cache_max_rows_per_uid
conversation.authority_cache_max_rows
conversation.authority_list_db_window_max
conversation.authority_handoff_timeout
conversation.authority_active_cooldown
conversation.authority_flush_interval
conversation.authority_flush_timeout
conversation.authority_flush_batch_rows
conversation.authority_admit_batch_rows
conversation.authority_admit_concurrency
channel.large_group_subscriber_threshold
channel_append.shard_count
channel_append.advance_pool_size
channel_append.effect_pool_size
channel_append.recipient_authority_dispatch_concurrency
delivery.enable
delivery.fanout_page_size
delivery.push_batch_size
delivery.pending_ack_ttl
delivery.pending_ack_max_per_session
delivery.event_queue_size
webhook.http_addr
webhook.focus_events
webhook.queue_size
webhook.workers
webhook.msg_notify_batch_max_items
webhook.msg_notify_batch_max_wait
webhook.online_status_batch_max_items
webhook.online_status_batch_max_wait
webhook.offline_uid_batch_size
webhook.request_timeout
webhook.retry_max_attempts
plugin.enable
plugin.dir
plugin.socket_path
plugin.sandbox_dir
plugin.state_dir
plugin.timeout
plugin.hot_reload
plugin.fail_open
plugin.persist_after_queue_size
plugin.persist_after_workers
log.level
log.dir
log.max_size
log.max_age
log.max_backups
log.compress
log.console
log.format
```

Mark these fields sensitive:

```text
cluster.join_token
manager.jwt_secret
manager.users
```

Use `kindObjectList` for:

```text
cluster.nodes
gateway.listeners
manager.users
diagnostics.debug_matches
```

Use `kindStringList` for:

```text
cluster.seeds
prometheus.scrape_targets
webhook.focus_events
```

- [ ] **Step 5: Run coverage**

Run:

```bash
GOWORK=off go test ./internal/config -run TestSchemaCoversBuilderKeys -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/schema.go internal/config/loader_test.go
git commit -m "feat(config): define toml schema for startup keys"
```

## Task 5: Add Strict Environment Overlay

**Files:**
- Create: `internal/config/env.go`
- Modify: `internal/config/types.go`
- Modify: `internal/config/loader_test.go`

- [ ] **Step 1: Add failing env tests**

Append to `internal/config/loader_test.go`:

```go
func TestLoadAllowsEnvOnlyStartup(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(Options{Args: nil, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=9",
		"WK_NODE_DATA_DIR=" + dir + "/node9",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7009",
	}})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.NodeID != 9 || cfg.Cluster.ListenAddr != "127.0.0.1:7009" {
		t.Fatalf("env-only config = %#v", cfg)
	}
}

func TestLoadEnvOverridesTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.toml")
	writeFile(t, path, `
[node]
id = 1
data_dir = "`+dir+`/node1"

[cluster]
listen_addr = "127.0.0.1:7001"
`)
	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=2",
	}})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.NodeID != 2 || cfg.Cluster.NodeID != 2 {
		t.Fatalf("NodeID = %d/%d, want env override 2", cfg.NodeID, cfg.Cluster.NodeID)
	}
}

func TestLoadRejectsUnknownWKEnv(t *testing.T) {
	_, err := Load(Options{Args: nil, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=/tmp/node",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		"WK_CLUSTER_HASH_SLOTS_COUNT=256",
	}})
	if err == nil {
		t.Fatal("Load() error = nil, want unknown env")
	}
	if !strings.Contains(err.Error(), "WK_CLUSTER_HASH_SLOTS_COUNT") {
		t.Fatalf("Load() error = %v, want unknown env name", err)
	}
}

func TestLoadRejectsRemovedWKEnvWithReplacement(t *testing.T) {
	_, err := Load(Options{Args: nil, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=/tmp/node",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		"WK_CLUSTER_GROUP_COUNT=16",
	}})
	if err == nil {
		t.Fatal("Load() error = nil, want removed env")
	}
	if !strings.Contains(err.Error(), "WK_CLUSTER_GROUP_COUNT") || !strings.Contains(err.Error(), "WK_CLUSTER_INITIAL_SLOT_COUNT") {
		t.Fatalf("Load() error = %v, want removed key replacement", err)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

Run:

```bash
GOWORK=off go test ./internal/config -run 'TestLoadAllowsEnvOnlyStartup|TestLoadEnvOverridesTOML|TestLoadRejectsUnknownWKEnv|TestLoadRejectsRemovedWKEnvWithReplacement' -count=1
```

Expected: FAIL because env overlay is not wired.

- [ ] **Step 3: Implement env overlay**

Create `internal/config/env.go`:

```go
package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

func environ(opts Options) []string {
	if opts.Environ != nil {
		return opts.Environ
	}
	return os.Environ()
}

func overlayEnv(values sourceValues, env []string) (sourceValues, error) {
	if values.values == nil {
		values.values = map[string]string{}
	}
	if values.sources == nil {
		values.sources = map[string]string{}
	}
	known := schemaByEnvKey()
	unknown := make([]string, 0)
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, "WK_") {
			continue
		}
		if replacement, ok := removedConfigKeyReplacements[key]; ok {
			return sourceValues{}, fmt.Errorf("%s is no longer supported; use %s", key, replacement)
		}
		if _, ok := known[key]; !ok {
			unknown = append(unknown, key)
			continue
		}
		values.values[key] = strings.TrimSpace(value)
		values.sources[key] = "env"
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return sourceValues{}, fmt.Errorf("unknown config env: %s", strings.Join(unknown, ", "))
	}
	return values, nil
}
```

- [ ] **Step 4: Wire env overlay and env-only startup**

Replace the body of `Load` in `internal/config/types.go` with:

```go
func Load(opts Options) (app.Config, error) {
	configPath, err := parseConfigPath(opts.Args)
	if err != nil {
		return app.Config{}, err
	}
	values := sourceValues{values: map[string]string{}, sources: map[string]string{}}
	var attempted []string
	if configPath != "" {
		values, err = readTOMLValues(configPath)
		if err != nil {
			return app.Config{}, fmt.Errorf("load config: %w", err)
		}
	} else {
		values, attempted, err = readDefaultTOMLValues()
		if err != nil {
			return app.Config{}, err
		}
	}
	values, err = overlayEnv(values, environ(opts))
	if err != nil {
		return app.Config{}, fmt.Errorf("load config: %w", err)
	}
	cfg, err := buildConfig(values.values)
	if err != nil {
		if len(attempted) > 0 {
			missing := missingRequiredConfigKeys(values.values)
			if len(missing) > 0 {
				return app.Config{}, fmt.Errorf(
					"load config: no default config file found (tried %s); missing required config keys: %s",
					strings.Join(attempted, ", "),
					strings.Join(missing, ", "),
				)
			}
		}
		return app.Config{}, fmt.Errorf("load config: %w", err)
	}
	return cfg, nil
}

func readDefaultTOMLValues() (sourceValues, []string, error) {
	attempted := make([]string, 0, len(defaultConfigPaths))
	for _, candidate := range defaultConfigPaths {
		attempted = append(attempted, candidate)
		if _, err := os.Stat(candidate); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return sourceValues{}, nil, fmt.Errorf("stat %s: %w", candidate, err)
		}
		values, err := readTOMLValues(candidate)
		return values, nil, err
	}
	return sourceValues{values: map[string]string{}, sources: map[string]string{}}, attempted, nil
}
```

Ensure `internal/config/types.go` imports `os` and `strings`.

- [ ] **Step 5: Run env tests**

Run:

```bash
GOWORK=off go test ./internal/config -run 'TestLoadAllowsEnvOnlyStartup|TestLoadEnvOverridesTOML|TestLoadRejectsUnknownWKEnv|TestLoadRejectsRemovedWKEnvWithReplacement' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/config/env.go internal/config/types.go internal/config/loader_test.go
git commit -m "feat(config): overlay strict wk environment"
```

## Task 6: Preserve List JSON Overrides And Required Empty Behavior

**Files:**
- Modify: `internal/config/loader_test.go`
- Modify: `internal/config/build.go`

- [ ] **Step 1: Add failing tests for JSON list env and empty required values**

Append to `internal/config/loader_test.go`:

```go
func TestLoadEnvJSONListOverridesTOMLNodes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wukongim.toml")
	writeFile(t, path, `
[node]
id = 1
data_dir = "`+dir+`/node1"

[cluster]
listen_addr = "127.0.0.1:7001"

[[cluster.nodes]]
id = 1
addr = "old-node:7000"
`)
	cfg, err := Load(Options{Args: []string{"-config", path}, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		`WK_CLUSTER_NODES=[{"id":1,"addr":"new-node:7000"}]`,
	}})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Cluster.Control.Voters) != 1 || cfg.Cluster.Control.Voters[0].Addr != "new-node:7000" {
		t.Fatalf("voters = %#v, want env replacement", cfg.Cluster.Control.Voters)
	}
}

func TestLoadRejectsEmptyRequiredEnvValue(t *testing.T) {
	_, err := Load(Options{Args: nil, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
	}})
	if err == nil {
		t.Fatal("Load() error = nil, want empty required value")
	}
	if !strings.Contains(err.Error(), "WK_NODE_DATA_DIR") {
		t.Fatalf("Load() error = %v, want required key", err)
	}
}
```

- [ ] **Step 2: Run tests**

Run:

```bash
GOWORK=off go test ./internal/config -run 'TestLoadEnvJSONListOverridesTOMLNodes|TestLoadRejectsEmptyRequiredEnvValue' -count=1
```

Expected: PASS if Task 5 uses existing builder correctly. If the empty required test fails because empty env is ignored, fix `configValue`/`requiredConfigValue` so present-but-empty required keys fail.

- [ ] **Step 3: Ensure malformed list errors name env key**

Add this test:

```go
func TestLoadRejectsMalformedJSONListEnv(t *testing.T) {
	_, err := Load(Options{Args: nil, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=/tmp/node",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		"WK_CLUSTER_NODES=[",
	}})
	if err == nil {
		t.Fatal("Load() error = nil, want malformed JSON list")
	}
	if !strings.Contains(err.Error(), "WK_CLUSTER_NODES") {
		t.Fatalf("Load() error = %v, want WK_CLUSTER_NODES", err)
	}
}
```

Run:

```bash
GOWORK=off go test ./internal/config -run TestLoadRejectsMalformedJSONListEnv -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/config/loader_test.go internal/config/build.go
git commit -m "test(config): cover list env overrides"
```

## Task 7: Attach Safe Startup Config Snapshot To app.Config

**Files:**
- Create: `internal/config/snapshot.go`
- Modify: `internal/app/config.go`
- Modify: `internal/app/node_config.go`
- Modify: `internal/app/node_config_test.go`
- Modify: `internal/config/types.go`
- Modify: `internal/config/loader_test.go`

- [ ] **Step 1: Add app config field**

Add to `internal/app/config.go` imports:

```go
managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
```

Add this field to `Config`:

```go
	// StartupConfigSnapshot is a bounded, redacted view of effective startup configuration.
	StartupConfigSnapshot managementusecase.NodeConfigSnapshot
```

- [ ] **Step 2: Add failing snapshot loader test**

Append to `internal/config/loader_test.go`:

```go
func TestLoadBuildsRedactedStartupConfigSnapshot(t *testing.T) {
	cfg, err := Load(Options{Args: nil, Environ: []string{
		"PATH=" + os.Getenv("PATH"),
		"WK_NODE_ID=1",
		"WK_NODE_DATA_DIR=/tmp/wukongim-node",
		"WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7001",
		"WK_CLUSTER_JOIN_TOKEN=join-secret",
		"WK_MANAGER_JWT_SECRET=jwt-secret",
		`WK_MANAGER_USERS=[{"username":"admin","password":"plain"}]`,
	}})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	snapshot := cfg.StartupConfigSnapshot
	if snapshot.NodeID != 1 || snapshot.Source != "effective_startup_config" || !snapshot.RequiresRestart {
		t.Fatalf("snapshot metadata = %#v", snapshot)
	}
	text := snapshotText(snapshot)
	for _, forbidden := range []string{"join-secret", "jwt-secret", "plain", "/tmp/wukongim-node"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("snapshot leaked %q: %s", forbidden, text)
		}
	}
	for _, want := range []string{"WK_NODE_ID=1", "WK_CLUSTER_JOIN_TOKEN=******", "WK_MANAGER_JWT_SECRET=******", "WK_MANAGER_USERS=******"} {
		if !strings.Contains(text, want) {
			t.Fatalf("snapshot text %q missing %q", text, want)
		}
	}
}

func snapshotText(snapshot managementusecase.NodeConfigSnapshot) string {
	var b strings.Builder
	for _, group := range snapshot.Groups {
		b.WriteString(group.ID)
		b.WriteByte(' ')
		for _, item := range group.Items {
			b.WriteString(item.Key)
			b.WriteByte('=')
			b.WriteString(item.Value)
			b.WriteByte(' ')
		}
	}
	return b.String()
}
```

Add import:

```go
managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
```

- [ ] **Step 3: Run snapshot test to verify failure**

Run:

```bash
GOWORK=off go test ./internal/config -run TestLoadBuildsRedactedStartupConfigSnapshot -count=1
```

Expected: FAIL because `StartupConfigSnapshot` is empty.

- [ ] **Step 4: Build schema-driven snapshot**

Create `internal/config/snapshot.go`:

```go
package config

import (
	"fmt"
	"sort"
	"strings"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

const redactedValue = "******"

func buildStartupSnapshot(values sourceValues, nodeID uint64) managementusecase.NodeConfigSnapshot {
	groups := map[string][]managementusecase.NodeConfigItem{}
	for _, field := range schemaFields {
		raw, ok := values.values[field.EnvKey]
		if !ok {
			continue
		}
		value := formatSnapshotValue(field, raw)
		groups[field.Group] = append(groups[field.Group], managementusecase.NodeConfigItem{
			Key:       field.EnvKey,
			Label:     field.Label,
			Value:     value,
			Sensitive: field.Sensitive,
			Redacted:  field.Sensitive && strings.TrimSpace(raw) != "",
		})
	}
	order := []string{"node", "cluster", "gateway", "message", "channel", "channel_append", "delivery", "webhook", "plugin", "log", "observability", "manager", "bench", "prometheus", "diagnostics", "top", "presence", "conversation", "channel_migration", "api"}
	seen := map[string]bool{}
	out := make([]managementusecase.NodeConfigGroup, 0, len(groups))
	for _, id := range order {
		if items, ok := groups[id]; ok {
			out = append(out, managementusecase.NodeConfigGroup{ID: id, Title: groupTitle(id), Items: items})
			seen[id] = true
		}
	}
	extra := make([]string, 0)
	for id := range groups {
		if !seen[id] {
			extra = append(extra, id)
		}
	}
	sort.Strings(extra)
	for _, id := range extra {
		out = append(out, managementusecase.NodeConfigGroup{ID: id, Title: groupTitle(id), Items: groups[id]})
	}
	return managementusecase.NodeConfigSnapshot{
		GeneratedAt:     time.Now().UTC(),
		NodeID:          nodeID,
		Source:          managementusecase.NodeConfigSnapshotSourceEffectiveStartup,
		RequiresRestart: true,
		Groups:          out,
	}
}

func formatSnapshotValue(field fieldSpec, raw string) string {
	if field.Sensitive && strings.TrimSpace(raw) != "" {
		return redactedValue
	}
	if strings.Contains(strings.ToLower(field.TOMLPath), "data_dir") ||
		strings.Contains(strings.ToLower(field.TOMLPath), "log.dir") ||
		strings.Contains(strings.ToLower(field.TOMLPath), "plugin.dir") ||
		strings.Contains(strings.ToLower(field.TOMLPath), "plugin.sandbox_dir") ||
		strings.Contains(strings.ToLower(field.TOMLPath), "plugin.state_dir") ||
		strings.Contains(strings.ToLower(field.TOMLPath), "plugin.socket_path") {
		if strings.TrimSpace(raw) == "" {
			return ""
		}
		return "configured"
	}
	if field.Kind == kindObjectList {
		return "configured"
	}
	return raw
}

func groupTitle(id string) string {
	titles := map[string]string{
		"node":              "Node",
		"cluster":           "Cluster",
		"gateway":           "Gateway",
		"message":           "Message",
		"channel":           "Channel",
		"channel_append":    "Channel Append",
		"delivery":          "Delivery",
		"webhook":           "Webhook",
		"plugin":            "Plugin",
		"log":               "Log",
		"observability":     "Observability",
		"manager":           "Manager",
		"bench":             "Bench",
		"prometheus":        "Prometheus",
		"diagnostics":       "Diagnostics",
		"top":               "Top",
		"presence":          "Presence",
		"conversation":      "Conversation",
		"channel_migration": "Channel Migration",
		"api":               "API",
	}
	if title, ok := titles[id]; ok {
		return title
	}
	return fmt.Sprintf("%s", id)
}
```

- [ ] **Step 5: Attach snapshot in Load**

After `buildConfig(values.values)` succeeds in `internal/config/types.go`, set:

```go
	cfg.StartupConfigSnapshot = buildStartupSnapshot(values, cfg.NodeID)
```

- [ ] **Step 6: Use supplied snapshot in app provider**

At the top of `internal/app/node_config.go` after local node ID validation, add:

```go
	if len(cfg.StartupConfigSnapshot.Groups) > 0 {
		snapshot := cfg.StartupConfigSnapshot
		snapshot.GeneratedAt = time.Now().UTC()
		snapshot.NodeID = nodeID
		if snapshot.Source == "" {
			snapshot.Source = managementusecase.NodeConfigSnapshotSourceEffectiveStartup
		}
		snapshot.RequiresRestart = true
		return snapshot, nil
	}
```

Keep the existing hard-coded fallback for one commit so current tests and direct app construction still pass.

- [ ] **Step 7: Run tests**

Run:

```bash
GOWORK=off go test ./internal/config ./internal/app -run 'TestLoadBuildsRedactedStartupConfigSnapshot|TestNodeConfigSnapshotReturnsEffectiveGroupsAndRedactsSecrets' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/config/snapshot.go internal/config/types.go internal/config/loader_test.go internal/app/config.go internal/app/node_config.go internal/app/node_config_test.go
git commit -m "feat(config): attach redacted startup snapshot"
```

## Task 8: Convert Example Configs To TOML

**Files:**
- Delete: `wukongim.conf.example`
- Add: `wukongim.toml.example`
- Delete: `cmd/wukongim/wukongim.conf.example`
- Add: `cmd/wukongim/wukongim.toml.example`
- Modify: `internal/config/example_test.go`
- Modify: `cmd/wukongim/config_test.go`

- [ ] **Step 1: Add failing example tests**

Create `internal/config/example_test.go`:

```go
package config

import (
	"path/filepath"
	"testing"
)

func TestRootTOMLExampleLoads(t *testing.T) {
	cfg, err := Load(Options{Args: []string{"-config", filepath.Join("..", "..", "wukongim.toml.example")}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load(root example) error = %v", err)
	}
	if cfg.NodeID != 1 || cfg.Cluster.NodeID != 1 {
		t.Fatalf("NodeID = %d/%d, want 1", cfg.NodeID, cfg.Cluster.NodeID)
	}
	if cfg.Cluster.Slots.HashSlotCount != 256 {
		t.Fatalf("HashSlotCount = %d, want 256", cfg.Cluster.Slots.HashSlotCount)
	}
}

func TestCommandTOMLExampleLoads(t *testing.T) {
	cfg, err := Load(Options{Args: []string{"-config", filepath.Join("..", "..", "cmd", "wukongim", "wukongim.toml.example")}, Environ: cleanEnv()})
	if err != nil {
		t.Fatalf("Load(cmd example) error = %v", err)
	}
	if cfg.Cluster.Control.ClusterID != "wukongim-single" {
		t.Fatalf("ClusterID = %q, want wukongim-single", cfg.Cluster.Control.ClusterID)
	}
}
```

Run:

```bash
GOWORK=off go test ./internal/config -run 'TestRootTOMLExampleLoads|TestCommandTOMLExampleLoads' -count=1
```

Expected: FAIL because `.toml.example` files do not exist.

- [ ] **Step 2: Create root TOML example**

Convert `wukongim.conf.example` into `wukongim.toml.example`. The first section must be:

```toml
# WuKongIM single-node cluster example.
#
# This file uses the canonical cmd/wukongim TOML config surface. It starts one
# node as a single-node cluster.

[node]
# Stable non-zero node identity. Message IDs are seeded from this value.
id = 1

# Root data directory for cluster controller, slot, and channel data.
data_dir = "./data/wukongim-single-node-data"

[cluster]
# Node-to-node cluster RPC listen address. Keep it reachable by this node.
listen_addr = "127.0.0.1:7001"

# Stable cluster identity shared by every node in this cluster.
id = "wukongim-single"

# Shared join secret accepted by seed JoinNode RPC handlers.
join_token = "change-me"

# Physical Slot count created by the initial Controller snapshot.
initial_slot_count = 1

# Logical hash-slot count used for routing channel keys to physical Slots.
hash_slot_count = 256

# Desired Slot replica count for this single-node cluster.
slot_replica_n = 1

[[cluster.nodes]]
id = 1
addr = "127.0.0.1:7001"
```

Use this conversion rule for all remaining sections:

```text
TOML path from internal/config/schema.go = value from matching WK_* example key
```

Use these section headers so the result is deterministic:

```toml
[cluster]
[channel_migration]
[channel]
[api]
[manager]
[bench]
[observability]
[prometheus]
[top]
[diagnostics]
[log]
[gateway]
[message]
[presence]
[conversation]
[channel_append]
[delivery]
[webhook]
[plugin]
```

Use TOML arrays for these paths:

```text
cluster.nodes
gateway.listeners
manager.users
prometheus.scrape_targets
webhook.focus_events
diagnostics.debug_matches
```

After conversion, this command must return no `WK_` lines:

```bash
rg -n '^WK_' wukongim.toml.example cmd/wukongim/wukongim.toml.example
```

- [ ] **Step 3: Copy command example**

Make `cmd/wukongim/wukongim.toml.example` match the root example exactly:

```bash
cp wukongim.toml.example cmd/wukongim/wukongim.toml.example
```

- [ ] **Step 4: Remove old example files**

```bash
rm wukongim.conf.example cmd/wukongim/wukongim.conf.example
```

- [ ] **Step 5: Update command tests**

In `cmd/wukongim/config_test.go`, replace example test paths:

```go
loadConfig([]string{"-config", "wukongim.toml.example"})
loadConfig([]string{"-config", filepath.Join("..", "..", "wukongim.toml.example")})
```

Delete tests that assert `.conf` line syntax. Keep tests that assert effective config values after loading examples.

- [ ] **Step 6: Run example tests**

Run:

```bash
GOWORK=off go test ./internal/config ./cmd/wukongim -run 'Example|TOMLExample|RootExample' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add wukongim.toml.example cmd/wukongim/wukongim.toml.example internal/config/example_test.go cmd/wukongim/config_test.go
git rm wukongim.conf.example cmd/wukongim/wukongim.conf.example
git commit -m "docs(config): replace conf examples with toml"
```

## Task 9: Convert Docker Configs And Tests

**Files:**
- Delete: `docker/conf/node1.conf`
- Delete: `docker/conf/node2.conf`
- Delete: `docker/conf/node3.conf`
- Add: `docker/conf/node1.toml`
- Add: `docker/conf/node2.toml`
- Add: `docker/conf/node3.toml`
- Modify: `docker-compose.yml`
- Modify: `Dockerfile`
- Modify: `docker/compose_defaults_test.go`
- Modify: `cmd/wukongim/config_test.go`

- [ ] **Step 1: Add failing Docker loader test**

Update `cmd/wukongim/config_test.go` Docker test to:

```go
func TestDockerComposeNodeConfigsLoadWithCurrentConfigSurface(t *testing.T) {
	unsetLoadConfigEnv(t)
	for _, node := range []string{"node1.toml", "node2.toml", "node3.toml"} {
		t.Run(node, func(t *testing.T) {
			path := filepath.Join("..", "..", "docker", "conf", node)
			if _, err := loadConfig([]string{"-config", path}); err != nil {
				t.Fatalf("loadConfig(%s) error = %v", path, err)
			}
		})
	}
}
```

Run:

```bash
GOWORK=off go test ./cmd/wukongim -run TestDockerComposeNodeConfigsLoadWithCurrentConfigSurface -count=1
```

Expected: FAIL because Docker TOML files do not exist.

- [ ] **Step 2: Convert node configs**

Convert each `docker/conf/nodeN.conf` to TOML. `docker/conf/node1.toml` must start with:

```toml
[node]
id = 1
data_dir = "/var/lib/wukongim"

[cluster]
listen_addr = "0.0.0.0:7000"
hash_slot_count = 256
initial_slot_count = 10
slot_replica_n = 3
channel_append_batch_max_records = 128
channel_append_batch_max_wait = "250us"
commit_coordinator_flush_window = "1ms"
commit_coordinator_max_requests = 0
commit_coordinator_max_records = 0
commit_coordinator_max_bytes = 131072
commit_coordinator_shards = 8

[[cluster.nodes]]
id = 1
addr = "wk-node1:7000"

[[cluster.nodes]]
id = 2
addr = "wk-node2:7000"

[[cluster.nodes]]
id = 3
addr = "wk-node3:7000"
```

Use node IDs `2` and `3` in the other files. Preserve current manager settings on node1 and current hot-path tuning on all nodes.

- [ ] **Step 3: Update Dockerfile and Compose**

In `Dockerfile`, replace:

```dockerfile
ENTRYPOINT ["/usr/local/bin/wukongim", "-config", "/etc/wukongim/wukongim.conf"]
```

with:

```dockerfile
ENTRYPOINT ["/usr/local/bin/wukongim", "-config", "/etc/wukongim/wukongim.toml"]
```

In `docker-compose.yml`, replace each mount target and source:

```yaml
- ./docker/conf/node1.toml:/etc/wukongim/wukongim.toml:ro
```

Do the same for node2 and node3.

- [ ] **Step 4: Update Docker effective-value tests**

Replace string checks in `docker/compose_defaults_test.go` with real loading:

```go
func loadDockerNodeConfig(t *testing.T, node string) app.Config {
	t.Helper()
	repoRoot := dockerRepoRoot(t)
	cfg, err := productconfig.Load(productconfig.Options{
		Args:    []string{"-config", filepath.Join(repoRoot, "docker", "conf", node)},
		Environ: []string{"PATH=" + os.Getenv("PATH")},
	})
	if err != nil {
		t.Fatalf("load %s: %v", node, err)
	}
	return cfg
}
```

Add imports:

```go
import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/app"
	productconfig "github.com/WuKongIM/WuKongIM/internal/config"
)
```

Assert values from `app.Config`:

```go
cfg := loadDockerNodeConfig(t, node)
if cfg.Cluster.Channel.AppendBatchMaxRecords != 128 {
	t.Fatalf("%s AppendBatchMaxRecords = %d, want 128", node, cfg.Cluster.Channel.AppendBatchMaxRecords)
}
if cfg.Cluster.Storage.CommitFlushWindow != time.Millisecond {
	t.Fatalf("%s CommitFlushWindow = %s, want 1ms", node, cfg.Cluster.Storage.CommitFlushWindow)
}
if cfg.Gateway.Runtime.AsyncSendWorkers != 128 {
	t.Fatalf("%s AsyncSendWorkers = %d, want 128", node, cfg.Gateway.Runtime.AsyncSendWorkers)
}
```

Add `time` to imports for the commit flush assertion.

- [ ] **Step 5: Remove old Docker conf files**

```bash
git rm docker/conf/node1.conf docker/conf/node2.conf docker/conf/node3.conf
```

- [ ] **Step 6: Run Docker tests**

Run:

```bash
GOWORK=off go test ./cmd/wukongim ./docker -run 'Docker|Compose' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add Dockerfile docker-compose.yml docker/conf/node1.toml docker/conf/node2.toml docker/conf/node3.toml docker/compose_defaults_test.go cmd/wukongim/config_test.go
git commit -m "chore(config): migrate docker configs to toml"
```

## Task 10: Update Scripts And Docs

**Files:**
- Modify/Rename: `scripts/wukongim/*`
- Modify: `AGENTS.md`
- Modify: `internal/app/FLOW.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`
- Modify: any docs found by the search command below

- [ ] **Step 1: Search for `.conf` startup references**

Run:

```bash
rg -n "wukongim\\.conf|\\.conf.example|docker/conf/node[123]\\.conf|/etc/wukongim/wukongim\\.conf|WK_\\*.*KEY=value|KEY=value" AGENTS.md docs scripts docker cmd internal --glob '!web/**' --glob '!ui/assets/**'
```

Expected: shows all remaining docs/scripts references that need migration.

- [ ] **Step 2: Rename script configs**

For every runtime config under `scripts/wukongim/` ending in `.conf`, rename it to `.toml` and convert the body to TOML using the same schema paths as `wukongim.toml.example`.

Run after conversion:

```bash
rg -n "scripts/wukongim/.+\\.conf|wukongim\\.conf" scripts
```

Expected: no output for runtime config paths.

- [ ] **Step 3: Update AGENTS configuration convention**

In `AGENTS.md`, replace the configuration section with:

```markdown
## 配置约定

- 主配置文件使用 `wukongim.toml`，格式为 TOML。
- 文件配置以结构化 TOML path 表达；环境变量仍使用 `WK_` 前缀。
- 不传 `-config` 时，程序按 `./wukongim.toml`、`./conf/wukongim.toml`、`/etc/wukongim/wukongim.toml` 顺序查找。
- 环境变量优先级高于 TOML 文件；列表字段在环境变量中使用 JSON 字符串整体覆盖。
- 当配置发生变化时，需要把 `wukongim.toml.example` 对齐。
- 涉及到配置相关的字段必须有详细的英文注释。
```

- [ ] **Step 4: Update project knowledge**

In `docs/development/PROJECT_KNOWLEDGE.md`, replace:

```text
Runnable `wukongim` helper-script configs live under `scripts/wukongim/` as `.conf`; `.conf.example` files are samples only and should not be script defaults.
```

with:

```text
Runnable `wukongim` helper-script configs live under `scripts/wukongim/` as `.toml`; `wukongim.toml.example` is a sample only and should not be a script default.
```

- [ ] **Step 5: Update FLOW**

In `internal/app/FLOW.md`, replace references to phase-1 `.conf` loading with:

```text
`cmd/wukongim` loads schema-backed TOML plus WK_* environment overrides before
calling `internal/app.New(Config)`. `internal/app` remains format-agnostic and
receives only the normalized `Config` plus a bounded redacted startup-config
snapshot for manager reads.
```

- [ ] **Step 6: Re-run search**

Run:

```bash
rg -n "wukongim\\.conf|\\.conf.example|docker/conf/node[123]\\.conf|/etc/wukongim/wukongim\\.conf" AGENTS.md docs scripts docker cmd internal --glob '!web/**' --glob '!ui/assets/**'
```

Expected: no runtime startup references. Historical design docs under `docs/superpowers/specs/2026-04-06-conf-env-config-design.md` may remain because they describe prior design history.

- [ ] **Step 7: Commit**

```bash
git add AGENTS.md internal/app/FLOW.md docs/development/PROJECT_KNOWLEDGE.md scripts/wukongim docs docker cmd
git commit -m "docs(config): document toml startup convention"
```

## Task 11: Remove Hard-Coded App Snapshot Fallback

**Files:**
- Modify: `internal/app/node_config.go`
- Modify: `internal/app/node_config_test.go`
- Modify: `internal/config/loader_test.go`

- [ ] **Step 1: Add app fallback removal test**

Replace `TestNodeConfigSnapshotReturnsEffectiveGroupsAndRedactsSecrets` in `internal/app/node_config_test.go` with a test that supplies `StartupConfigSnapshot`:

```go
func TestNodeConfigSnapshotReturnsStartupSnapshot(t *testing.T) {
	app := &App{cfg: Config{
		NodeID: 2,
		StartupConfigSnapshot: managementusecase.NodeConfigSnapshot{
			NodeID:          2,
			Source:          managementusecase.NodeConfigSnapshotSourceEffectiveStartup,
			RequiresRestart: true,
			Groups: []managementusecase.NodeConfigGroup{{
				ID:    "node",
				Title: "Node",
				Items: []managementusecase.NodeConfigItem{{
					Key:   "WK_NODE_ID",
					Label: "Node ID",
					Value: "2",
				}},
			}},
		},
	}}
	snapshot, err := app.NodeConfigSnapshot(context.Background(), 2)
	if err != nil {
		t.Fatalf("NodeConfigSnapshot() error = %v", err)
	}
	if !containsNodeConfigItem(snapshot, "WK_NODE_ID", "2") {
		t.Fatalf("snapshot missing supplied node id: %#v", snapshot.Groups)
	}
}
```

- [ ] **Step 2: Remove fallback builder from app**

In `internal/app/node_config.go`, delete the hard-coded group construction after the supplied snapshot branch. The function should return `managementusecase.ErrNodeConfigUnavailable` when no supplied snapshot exists.

The function body should have this shape:

```go
func (a *App) NodeConfigSnapshot(_ context.Context, requestedNodeID uint64) (managementusecase.NodeConfigSnapshot, error) {
	if a == nil {
		return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
	}
	cfg := a.cfg
	nodeID := cfg.NodeID
	if nodeID == 0 {
		nodeID = cfg.Cluster.NodeID
	}
	if requestedNodeID != nodeID {
		return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
	}
	if len(cfg.StartupConfigSnapshot.Groups) == 0 {
		return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
	}
	snapshot := cfg.StartupConfigSnapshot
	snapshot.GeneratedAt = time.Now().UTC()
	snapshot.NodeID = nodeID
	if snapshot.Source == "" {
		snapshot.Source = managementusecase.NodeConfigSnapshotSourceEffectiveStartup
	}
	snapshot.RequiresRestart = true
	return snapshot, nil
}
```

Remove helper functions that become unused from `internal/app/node_config.go`.

- [ ] **Step 3: Run tests**

Run:

```bash
GOWORK=off go test ./internal/app ./internal/config -run 'NodeConfigSnapshot|StartupConfigSnapshot' -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/app/node_config.go internal/app/node_config_test.go internal/config/loader_test.go
git commit -m "refactor(app): use supplied startup config snapshot"
```

## Task 12: Final Verification

**Files:**
- No planned edits unless verification exposes a bug.

- [ ] **Step 1: Run focused config and Docker tests**

Run:

```bash
GOWORK=off go test ./internal/config ./cmd/wukongim ./internal/app ./internal/usecase/management ./internal/access/manager ./docker -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader unit tests if focused tests pass**

Run:

```bash
GOWORK=off go test ./...
```

Expected: PASS. If this is too slow or fails in unrelated packages, capture the failing package and rerun a narrower command that covers the migration.

- [ ] **Step 3: Check formatting and whitespace**

Run:

```bash
gofmt -w internal/config cmd/wukongim internal/app
git diff --check
```

Expected: no output from `git diff --check`.

- [ ] **Step 4: Verify no runtime `.conf` files remain**

Run:

```bash
rg -n "wukongim\\.conf|\\.conf.example|docker/conf/node[123]\\.conf|/etc/wukongim/wukongim\\.conf" AGENTS.md docs scripts docker cmd internal --glob '!docs/superpowers/specs/2026-04-06-conf-env-config-design.md'
```

Expected: no runtime references. If historical docs remain, leave them only when they clearly describe old design history.

- [ ] **Step 5: Record verification fixes only when verification changed files**

If verification required fixes:

```bash
git status --short
git add docs/superpowers/plans/2026-07-09-toml-config-architecture.md internal/config cmd/wukongim internal/app docker scripts AGENTS.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "fix(config): complete toml migration"
```

If no fixes were needed, do not create an empty commit.

## Execution Notes

- Keep `GOWORK=off` on Go test commands in this repository.
- Do not run integration tests unless a focused failure requires it.
- Preserve unrelated local changes. Stage only paths listed in each task.
- Keep list env overrides as JSON strings. Do not introduce indexed env keys.
- Treat `.conf` runtime compatibility as intentionally removed.
