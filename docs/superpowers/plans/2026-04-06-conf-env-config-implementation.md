# Conf And Environment Config Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `cmd/wukongim` JSON-only startup config with `wukongim.conf` `KEY=value` loading, default-path discovery, and environment variable overrides.

**Architecture:** Keep all file-format and environment loading logic inside `cmd/wukongim/config.go`. Replace the JSON wire-config path with an explicit Viper-backed loader that reads known `WK_*` keys, parses list-valued fields from JSON strings, and still hands a plain `app.Config` to `internal/app`. Parsing stays in `buildAppConfig()`, while defaults and semantic validation stay in `cfg.ApplyDefaultsAndValidate()` at the `loadConfig()` boundary.

**Tech Stack:** Go, `github.com/spf13/viper`, `testify/require`, existing `internal/app` config validation, existing gateway bindings

---

## File Map

- Modify: `cmd/wukongim/main.go`
  - keep the startup flow intact
  - update the `-config` help text so it no longer refers to JSON
- Replace: `cmd/wukongim/config.go`
  - remove JSON decoder and JSON-specific wire structs
  - add Viper-based `.conf` loading
  - add default-path search and env-only fallback
  - parse known `WK_*` keys into `app.Config`
- Rewrite: `cmd/wukongim/config_test.go`
  - stop testing JSON payloads
  - add `.conf`, env override, env-only, and default-path cases
- Create: `wukongim.conf.example`
  - provide the canonical sample config at repo root
- Modify: `go.mod`
  - add `github.com/spf13/viper`
- Modify: `go.sum`
  - record new module checksums

Do not modify `internal/app/config.go` for file-format concerns. That package should stay unaware of Viper and `.conf`.

### Task 1: Replace JSON tests with `.conf` parsing tests

**Files:**
- Modify: `cmd/wukongim/config_test.go`
- Test: `cmd/wukongim/config_test.go`

- [ ] **Step 1: Add a test helper for writing `.conf` files**

```go
func writeConf(t *testing.T, dir, name string, lines ...string) string {
    t.Helper()
    path := filepath.Join(dir, name)
    body := strings.Join(lines, "\n") + "\n"
    require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
    return path
}
```

- [ ] **Step 2: Write the failing parse test for a direct `-config` path**

```go
func TestLoadConfigParsesConfFileIntoAppConfig(t *testing.T) {
    dir := t.TempDir()
    dataDir := filepath.Join(dir, "node-1")
    configPath := writeConf(t, dir, "wukongim.conf",
        "WK_NODE_ID=1",
        "WK_NODE_NAME=node-1",
        "WK_NODE_DATA_DIR="+dataDir,
        "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
        `WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
        `WK_CLUSTER_GROUPS=[{"id":1,"peers":[1]}]`,
        `WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"stdnet","protocol":"wkproto"}]`,
        "WK_API_LISTEN_ADDR=127.0.0.1:8080",
    )

    cfg, err := loadConfig(configPath)
    require.NoError(t, err)
    require.Equal(t, uint64(1), cfg.Node.ID)
    require.Equal(t, "node-1", cfg.Node.Name)
    require.Equal(t, dataDir, cfg.Node.DataDir)
    require.Equal(t, "127.0.0.1:7000", cfg.Cluster.ListenAddr)
    require.Equal(t, "127.0.0.1:8080", cfg.API.ListenAddr)
    require.Len(t, cfg.Cluster.Nodes, 1)
    require.Len(t, cfg.Cluster.Groups, 1)
    require.Len(t, cfg.Gateway.Listeners, 1)
}
```

- [ ] **Step 3: Run the focused parse test and verify RED**

Run: `go test ./cmd/wukongim -run 'TestLoadConfigParsesConfFileIntoAppConfig'`
Expected: FAIL because `loadConfig` still treats the file as JSON.

- [ ] **Step 4: Write the failing default-value test for omitted optional keys**

```go
func TestLoadConfigUsesBuiltInDefaultsWhenOptionalConfKeysAreMissing(t *testing.T) {
    dir := t.TempDir()
    configPath := writeConf(t, dir, "wukongim.conf",
        "WK_NODE_ID=1",
        "WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
        "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
        `WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
        `WK_CLUSTER_GROUPS=[{"id":1,"peers":[1]}]`,
    )

    cfg, err := loadConfig(configPath)
    require.NoError(t, err)
    require.Equal(t, "0.0.0.0:5001", cfg.API.ListenAddr)
    require.Len(t, cfg.Gateway.Listeners, 2)
}
```

- [ ] **Step 5: Run both config-file tests and verify RED**

Run: `go test ./cmd/wukongim -run 'TestLoadConfig(ParsesConfFileIntoAppConfig|UsesBuiltInDefaultsWhenOptionalConfKeysAreMissing)'`
Expected: FAIL because the loader is still JSON-only.

- [ ] **Step 6: Commit the failing tests**

```bash
git add cmd/wukongim/config_test.go
git commit -m "test(cmd): cover conf file config loading"
```

### Task 2: Add failing tests for env overrides and default-path startup

**Files:**
- Modify: `cmd/wukongim/config_test.go`
- Test: `cmd/wukongim/config_test.go`

- [ ] **Step 1: Write the failing env-override test**

```go
func TestLoadConfigPrefersEnvironmentVariablesOverConfValues(t *testing.T) {
    dir := t.TempDir()
    configPath := writeConf(t, dir, "wukongim.conf",
        "WK_NODE_ID=1",
        "WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
        "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
        `WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
        `WK_CLUSTER_GROUPS=[{"id":1,"peers":[1]}]`,
        "WK_API_LISTEN_ADDR=127.0.0.1:8080",
    )
    t.Setenv("WK_API_LISTEN_ADDR", "127.0.0.1:9090")

    cfg, err := loadConfig(configPath)
    require.NoError(t, err)
    require.Equal(t, "127.0.0.1:9090", cfg.API.ListenAddr)
}
```

- [ ] **Step 2: Run the env-override test and verify RED**

Run: `go test ./cmd/wukongim -run 'TestLoadConfigPrefersEnvironmentVariablesOverConfValues'`
Expected: FAIL because the current loader does not read environment variables.

- [ ] **Step 3: Write the failing default-path discovery test**

```go
func TestLoadConfigUsesDefaultSearchPathsWhenFlagPathIsEmpty(t *testing.T) {
    dir := t.TempDir()
    confDir := filepath.Join(dir, "conf")
    require.NoError(t, os.MkdirAll(confDir, 0o755))
    writeConf(t, confDir, "wukongim.conf",
        "WK_NODE_ID=1",
        "WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
        "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
        `WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
        `WK_CLUSTER_GROUPS=[{"id":1,"peers":[1]}]`,
    )

    cwd, err := os.Getwd()
    require.NoError(t, err)
    require.NoError(t, os.Chdir(dir))
    t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })

    cfg, err := loadConfig("")
    require.NoError(t, err)
    require.Equal(t, uint64(1), cfg.Node.ID)
}
```

- [ ] **Step 4: Write the failing env-only startup test**

```go
func TestLoadConfigAcceptsEnvironmentOnlyConfigurationWhenNoFileExists(t *testing.T) {
    dir := t.TempDir()
    cwd, err := os.Getwd()
    require.NoError(t, err)
    require.NoError(t, os.Chdir(dir))
    t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })

    t.Setenv("WK_NODE_ID", "1")
    t.Setenv("WK_NODE_DATA_DIR", filepath.Join(dir, "node-1"))
    t.Setenv("WK_CLUSTER_LISTEN_ADDR", "127.0.0.1:7000")
    t.Setenv("WK_CLUSTER_NODES", `[{"id":1,"addr":"127.0.0.1:7000"}]`)
    t.Setenv("WK_CLUSTER_GROUPS", `[{"id":1,"peers":[1]}]`)

    cfg, err := loadConfig("")
    require.NoError(t, err)
    require.Equal(t, uint64(1), cfg.Node.ID)
}
```

- [ ] **Step 5: Write the failing missing-config error test**

```go
func TestLoadConfigReportsAttemptedDefaultPathsWhenConfigIsMissing(t *testing.T) {
    dir := t.TempDir()
    cwd, err := os.Getwd()
    require.NoError(t, err)
    require.NoError(t, os.Chdir(dir))
    t.Cleanup(func() { require.NoError(t, os.Chdir(cwd)) })

    _, err = loadConfig("")
    require.ErrorContains(t, err, "./wukongim.conf")
    require.ErrorContains(t, err, "./conf/wukongim.conf")
    require.ErrorContains(t, err, "/etc/wukongim/wukongim.conf")
}
```

- [ ] **Step 6: Run the source-selection tests and verify RED**

Run: `go test ./cmd/wukongim -run 'TestLoadConfig(PrefersEnvironmentVariablesOverConfValues|UsesDefaultSearchPathsWhenFlagPathIsEmpty|AcceptsEnvironmentOnlyConfigurationWhenNoFileExists|ReportsAttemptedDefaultPathsWhenConfigIsMissing)'`
Expected: FAIL because `loadConfig("")` currently errors immediately and the loader ignores env vars.

- [ ] **Step 7: Commit the failing source-selection tests**

```bash
git add cmd/wukongim/config_test.go
git commit -m "test(cmd): cover config env overrides and default paths"
```

### Task 3: Replace the JSON loader with an explicit Viper-backed config parser

**Files:**
- Modify: `cmd/wukongim/config.go`
- Modify: `go.mod`
- Modify: `go.sum`
- Test: `cmd/wukongim/config_test.go`

- [ ] **Step 1: Add Viper to module dependencies**

```go
require (
    github.com/spf13/viper v1.x.x
)
```

- [ ] **Step 2: Remove the JSON wire-config types and decoder**

```go
func loadConfig(path string) (app.Config, error) {
    cfgv, foundFile, attempted, err := readConfig(path)
    if err != nil {
        return app.Config{}, err
    }

    cfg, err := buildAppConfig(cfgv)
    if err != nil {
        return app.Config{}, err
    }
    if err := cfg.ApplyDefaultsAndValidate(); err != nil {
        if !foundFile {
            return app.Config{}, fmt.Errorf("load config: no config file found in default paths %v: %w", attempted, err)
        }
        return app.Config{}, fmt.Errorf("load config: %w", err)
    }
    return cfg, nil
}
```

- [ ] **Step 3: Add a Viper reader that treats `.conf` as envfile syntax**

```go
func readConfig(path string) (*viper.Viper, bool, []string, error) {
    v := viper.New()
    v.SetConfigType("env")
    v.AutomaticEnv()

    if strings.TrimSpace(path) != "" {
        v.SetConfigFile(path)
        if err := v.ReadInConfig(); err != nil {
            return nil, false, nil, fmt.Errorf("load config: read %s: %w", path, err)
        }
        return v, true, nil, nil
    }

    attempted := []string{
        "./wukongim.conf",
        "./conf/wukongim.conf",
        "/etc/wukongim/wukongim.conf",
    }
    for _, candidate := range attempted {
        if _, err := os.Stat(candidate); err == nil {
            v.SetConfigFile(candidate)
            if err := v.ReadInConfig(); err != nil {
                return nil, false, attempted, fmt.Errorf("load config: read %s: %w", candidate, err)
            }
            return v, true, attempted, nil
        }
    }
    return v, false, attempted, nil
}
```

- [ ] **Step 4: Add explicit key parsers instead of full-struct unmarshal**

```go
func buildAppConfig(v *viper.Viper) (app.Config, error) {
    nodeID, err := parseUint64(v, "WK_NODE_ID")
    if err != nil {
        return app.Config{}, err
    }
    nodes, err := parseJSONSlice[[]app.NodeConfigRef](v, "WK_CLUSTER_NODES")
    if err != nil {
        return app.Config{}, err
    }
    groups, err := parseJSONSlice[[]app.GroupConfig](v, "WK_CLUSTER_GROUPS")
    if err != nil {
        return app.Config{}, err
    }
    listeners, err := parseListenersOrDefault(v)
    if err != nil {
        return app.Config{}, err
    }
    // continue with scalar and duration parsing, then build app.Config directly
}
```

- [ ] **Step 5: Keep defaults local to the loader**

```go
cfg := app.Config{
    Node: app.NodeConfig{
        ID:      nodeID,
        Name:    strings.TrimSpace(v.GetString("WK_NODE_NAME")),
        DataDir: strings.TrimSpace(v.GetString("WK_NODE_DATA_DIR")),
    },
    API: app.APIConfig{
        ListenAddr: defaultAPIListenAddr,
    },
    Gateway: app.GatewayConfig{
        TokenAuthOn: defaultFalse(v, "WK_GATEWAY_TOKEN_AUTH_ON"),
        Listeners:   defaultGatewayListeners(),
    },
}
```

- [ ] **Step 6: Run the `.conf` parsing tests and verify GREEN**

Run: `go test ./cmd/wukongim -run 'TestLoadConfig(ParsesConfFileIntoAppConfig|UsesBuiltInDefaultsWhenOptionalConfKeysAreMissing)'`
Expected: PASS

- [ ] **Step 7: Commit the loader rewrite**

```bash
git add cmd/wukongim/config.go cmd/wukongim/config_test.go go.mod go.sum
git commit -m "feat(cmd): load config from conf and env"
```

### Task 4: Finish source-resolution behavior and startup wording

**Files:**
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/main.go`
- Test: `cmd/wukongim/config_test.go`

- [ ] **Step 1: Tighten the no-file error behavior**

```go
if err := cfg.ApplyDefaultsAndValidate(); err != nil {
    if !foundFile {
        return app.Config{}, fmt.Errorf(
            "load config: no config file found in default paths %v: %w",
            attempted,
            err,
        )
    }
    return app.Config{}, fmt.Errorf("load config: %w", err)
}
```

- [ ] **Step 2: Make env values override file values explicitly where helper functions read keys**

```go
v.AutomaticEnv()

func stringValue(v *viper.Viper, key string) string {
    return strings.TrimSpace(v.GetString(key))
}
```

- [ ] **Step 3: Update the `-config` help text**

```go
configPath := flag.String("config", "", "path to wukongim.conf file")
```

- [ ] **Step 4: Run the source-selection tests and verify GREEN**

Run: `go test ./cmd/wukongim -run 'TestLoadConfig(PrefersEnvironmentVariablesOverConfValues|UsesDefaultSearchPathsWhenFlagPathIsEmpty|AcceptsEnvironmentOnlyConfigurationWhenNoFileExists|ReportsAttemptedDefaultPathsWhenConfigIsMissing)'`
Expected: PASS

- [ ] **Step 5: Commit the source-resolution behavior**

```bash
git add cmd/wukongim/main.go cmd/wukongim/config.go cmd/wukongim/config_test.go
git commit -m "feat(cmd): add default config path discovery"
```

### Task 5: Add the canonical sample config and run regressions

**Files:**
- Create: `wukongim.conf.example`
- Modify: `cmd/wukongim/config_test.go`
- Test: `cmd/wukongim/config_test.go`
- Test: `internal/app/config_test.go`

- [ ] **Step 1: Add the sample config at repository root**

```conf
WK_NODE_ID=1
WK_NODE_NAME=node-1
WK_NODE_DATA_DIR=./data/node1

WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000
WK_CLUSTER_FORWARD_TIMEOUT=2s
WK_CLUSTER_TICK_INTERVAL=200ms
WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]
WK_CLUSTER_GROUPS=[{"id":1,"peers":[1]}]

WK_API_LISTEN_ADDR=0.0.0.0:5001
```

If listeners are shown in the sample, phrase the deployment shape as a single-node cluster rather than a standalone node.

- [ ] **Step 2: Add one malformed-list regression test if not already covered**

```go
func TestLoadConfigRejectsMalformedClusterGroupsJSON(t *testing.T) {
    dir := t.TempDir()
    configPath := writeConf(t, dir, "wukongim.conf",
        "WK_NODE_ID=1",
        "WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
        "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
        `WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
        `WK_CLUSTER_GROUPS=[{"id":1,"peers":[1]}`,
    )

    _, err := loadConfig(configPath)
    require.ErrorContains(t, err, "WK_CLUSTER_GROUPS")
}
```

- [ ] **Step 3: Run cmd config tests**

Run: `go test ./cmd/wukongim`
Expected: PASS

- [ ] **Step 4: Run app config regressions**

Run: `go test ./internal/app/...`
Expected: PASS

- [ ] **Step 5: Review the scope**

Run: `git diff -- cmd/wukongim/main.go cmd/wukongim/config.go cmd/wukongim/config_test.go go.mod go.sum wukongim.conf.example`
Expected: only loader, startup wording, sample config, and dependency updates are present.

- [ ] **Step 6: Commit the sample config and final verification**

```bash
git add cmd/wukongim/main.go cmd/wukongim/config.go cmd/wukongim/config_test.go go.mod go.sum wukongim.conf.example
git commit -m "feat(cmd): switch startup config to conf and env"
```
