# Debug API Config Unification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the split debug route configuration with one `WK_DEBUG_API_ENABLE` switch for every HTTP `/debug...` endpoint.

**Architecture:** Keep diagnostics collection, metrics, and health detail independent from debug route exposure. Rename app/API options to `DebugAPIEnabled`, route every `/debug...` registration through that option, and remove `WK_HEALTH_DEBUG_ENABLE`, `WK_PPROF_ENABLE`, and `WK_DIAGNOSTICS_DEBUG_API_ENABLE` from config parsing and examples.

**Tech Stack:** Go, Gin HTTP adapters, Viper-backed legacy config, strict env-file config parser for `cmd/wukongimv2`, existing `go test` package tests.

---

## File Structure

- Modify `cmd/wukongim/config.go` and `cmd/wukongim/config_test.go`: parse `WK_DEBUG_API_ENABLE`, remove deleted keys, and update explicit flag tests.
- Modify `cmd/wukongimv2/config.go` and `cmd/wukongimv2/config_test.go`: update strict supported keys, parsing, env override, example config assertions, and invalid-value tests.
- Modify `internal/app/config.go`, `internal/app/build.go`, `internal/app/observability.go`, and related tests: rename `HealthDebugEnabled` to `DebugAPIEnabled` and remove diagnostics debug API flags.
- Modify `internalv2/app/config.go`, `internalv2/app/wiring.go`, `internalv2/app/debug.go`, and related tests: rename fields and wire one debug switch into the API adapter.
- Modify `internal/access/api/server.go`, `internal/access/api/routes.go`, and tests: remove `DiagnosticsDebugEnabled`; all debug routes register only when `DebugAPIEnabled` is true.
- Modify `internalv2/access/api/server.go` and tests: remove `PProfEnabled` and `DiagnosticsDebugEnabled`; all debug routes register only when `DebugAPIEnabled` is true.
- Modify `wukongim.conf.example`, `docker/conf/*.conf`, `scripts/wukongimv2/*.conf`, focused docs, and script tests: replace removed keys with `WK_DEBUG_API_ENABLE`.

## Task 1: v2 Config Parser

**Files:**
- Modify: `cmd/wukongimv2/config.go`
- Modify: `cmd/wukongimv2/config_test.go`

- [ ] **Step 1: Write failing v2 config tests**

In `cmd/wukongimv2/config_test.go`, update the observability parsing tests so the input uses:

```go
"WK_DEBUG_API_ENABLE=true",
```

and assertions use:

```go
if !cfg.Observability.DebugAPIEnabled {
	t.Fatalf("Observability.DebugAPIEnabled = false, want true")
}
```

Remove assertions for `PProfEnabled`, `HealthDebugEnabled`, and `Diagnostics.DebugAPIEnabled`.

- [ ] **Step 2: Run v2 config tests and confirm failure**

Run:

```bash
go test ./cmd/wukongimv2 -run 'TestLoadConfig|TestExampleConfig|TestConfigRejectsInvalidValues' -count=1
```

Expected: FAIL because `WK_DEBUG_API_ENABLE` is not supported and the new `DebugAPIEnabled` field does not exist.

- [ ] **Step 3: Implement v2 parser changes**

In `cmd/wukongimv2/config.go`, replace supported keys:

```go
"WK_DEBUG_API_ENABLE",
```

and remove:

```go
"WK_PPROF_ENABLE",
"WK_HEALTH_DEBUG_ENABLE",
"WK_DIAGNOSTICS_DEBUG_API_ENABLE",
```

Replace the old parsing blocks with:

```go
if raw := configValue(values, "WK_DEBUG_API_ENABLE"); raw != "" {
	debugAPIEnable, err := parseBool("WK_DEBUG_API_ENABLE", raw)
	if err != nil {
		return app.Config{}, err
	}
	cfg.Observability.DebugAPIEnabled = debugAPIEnable
}
```

Keep diagnostics explicit flags to three booleans:

```go
cfg.Observability.SetDiagnosticsExplicitFlags(diagnosticsEnabledSet, diagnosticsSampleRateSet, diagnosticsErrorSampleRateSet)
```

- [ ] **Step 4: Run v2 config tests and confirm pass**

Run:

```bash
go test ./cmd/wukongimv2 -run 'TestLoadConfig|TestExampleConfig|TestConfigRejectsInvalidValues' -count=1
```

Expected: PASS.

## Task 2: Legacy Config Parser

**Files:**
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`

- [ ] **Step 1: Write failing legacy config tests**

In `cmd/wukongim/config_test.go`, replace inputs:

```go
"WK_HEALTH_DEBUG_ENABLE=true",
"WK_DIAGNOSTICS_DEBUG_API_ENABLE=true",
```

with:

```go
"WK_DEBUG_API_ENABLE=true",
```

Assert:

```go
require.True(t, cfg.Observability.DebugAPIEnabled)
```

and remove assertions for `cfg.Observability.Diagnostics.DebugAPIEnabled`.

- [ ] **Step 2: Run legacy config tests and confirm failure**

Run:

```bash
go test ./cmd/wukongim -run 'TestLoadConfigParsesObservabilityFlags|TestLoadConfigParsesDiagnosticsConfig|TestConfig' -count=1
```

Expected: FAIL because `DebugAPIEnabled` is not defined and `WK_DEBUG_API_ENABLE` is not parsed.

- [ ] **Step 3: Implement legacy parser changes**

In `cmd/wukongim/config.go`, remove parsing of:

```go
WK_HEALTH_DEBUG_ENABLE
WK_DIAGNOSTICS_DEBUG_API_ENABLE
```

Add parsing for:

```go
debugAPIEnable, err := parseBool(v, "WK_DEBUG_API_ENABLE")
if err != nil {
	return app.Config{}, err
}
```

Assign:

```go
DebugAPIEnabled: debugAPIEnable,
```

and update explicit flags:

```go
cfg.Observability.SetExplicitFlags(
	stringValue(v, "WK_METRICS_ENABLE") != "",
	stringValue(v, "WK_HEALTH_DETAIL_ENABLE") != "",
	stringValue(v, "WK_DEBUG_API_ENABLE") != "",
)
cfg.Observability.SetDiagnosticsExplicitFlags(
	stringValue(v, "WK_DIAGNOSTICS_ENABLE") != "",
	stringValue(v, "WK_DIAGNOSTICS_SAMPLE_RATE") != "",
	stringValue(v, "WK_DIAGNOSTICS_ERROR_SAMPLE_RATE") != "",
)
```

- [ ] **Step 4: Run legacy config tests and confirm pass**

Run:

```bash
go test ./cmd/wukongim -run 'TestLoadConfigParsesObservabilityFlags|TestLoadConfigParsesDiagnosticsConfig|TestConfig' -count=1
```

Expected: PASS.

## Task 3: App Config And Wiring

**Files:**
- Modify: `internal/app/config.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/observability.go`
- Modify: `internal/app/observability_test.go`
- Modify: `internalv2/app/config.go`
- Modify: `internalv2/app/wiring.go`
- Modify: `internalv2/app/debug.go`
- Modify: `internalv2/app/observability_test.go`

- [ ] **Step 1: Write failing app wiring tests**

Update tests to configure:

```go
Observability: ObservabilityConfig{DebugAPIEnabled: true}
```

and expect diagnostics debug routes to be present only when diagnostics is also enabled and a diagnostics reader exists.

- [ ] **Step 2: Run app tests and confirm failure**

Run:

```bash
go test ./internal/app ./internalv2/app -run 'Debug|Diagnostics' -count=1
```

Expected: FAIL because the app config structs still use old field names.

- [ ] **Step 3: Rename app observability fields**

In both app config packages:

```go
// DebugAPIEnabled exposes local debug endpoints on the API listener.
DebugAPIEnabled bool
```

Remove:

```go
HealthDebugEnabled bool
PProfEnabled bool
Diagnostics.DebugAPIEnabled bool
debugAPIEnabledSet in DiagnosticsConfig
```

Keep diagnostics explicit flags limited to enabled, sample rate, and error sample rate.

- [ ] **Step 4: Wire the unified switch into API options**

In `internal/app/build.go` and `internalv2/app/wiring.go`, set:

```go
DebugAPIEnabled: cfg.Observability.DebugAPIEnabled,
```

Remove separate `PProfEnabled` and `DiagnosticsDebugEnabled` assignments.

- [ ] **Step 5: Update debug snapshots**

Use stable keys:

```go
"debug_api_enable": a.cfg.Observability.DebugAPIEnabled,
"diagnostics_enable": a.cfg.Observability.Diagnostics.Enabled,
```

Remove:

```go
"health_debug"
"pprof_enable"
"diagnostics_debug_api"
```

- [ ] **Step 6: Run app tests and confirm pass**

Run:

```bash
go test ./internal/app ./internalv2/app -run 'Debug|Diagnostics' -count=1
```

Expected: PASS.

## Task 4: API Route Registration

**Files:**
- Modify: `internal/access/api/server.go`
- Modify: `internal/access/api/routes.go`
- Modify: `internal/access/api/diagnostics_test.go`
- Modify: `internal/access/api/server_test.go`
- Modify: `internalv2/access/api/server.go`
- Modify: `internalv2/access/api/debug_test.go`
- Modify: `internalv2/access/api/server_test.go`
- Modify: `internalv2/access/api/FLOW.md`

- [ ] **Step 1: Write failing route tests**

Update tests to construct servers with:

```go
New(Options{DebugAPIEnabled: true, Diagnostics: reader})
```

and verify these are all disabled when `DebugAPIEnabled` is false:

```text
/debug/config
/debug/cluster
/debug/goroutines
/debug/pprof
/debug/diagnostics/events
```

- [ ] **Step 2: Run API tests and confirm failure**

Run:

```bash
go test ./internal/access/api ./internalv2/access/api -run 'Debug|Diagnostics|PProf' -count=1
```

Expected: FAIL because `DebugAPIEnabled` is not an API option yet.

- [ ] **Step 3: Implement API option consolidation**

In both API server option structs, use:

```go
// DebugAPIEnabled exposes local /debug endpoints when supporting callbacks are configured.
DebugAPIEnabled bool
```

Remove:

```go
PProfEnabled bool
DebugEnabled bool
DiagnosticsDebugEnabled bool
```

For route registration, use one block:

```go
if s.debugAPIEnabled {
	s.registerPProfRoutes()
	if s.debugConfig != nil {
		s.engine.GET("/debug/config", s.handleDebugConfig)
	}
	if s.debugCluster != nil {
		s.engine.GET("/debug/cluster", s.handleDebugCluster)
	}
	if s.diagnostics != nil {
		s.registerDiagnosticsRoutes()
	}
}
```

Use the equivalent old-API helper name `registerDebugRoutes()` where that package already owns pprof registration.

- [ ] **Step 4: Run API tests and confirm pass**

Run:

```bash
go test ./internal/access/api ./internalv2/access/api -run 'Debug|Diagnostics|PProf' -count=1
```

Expected: PASS.

## Task 5: Examples, Scripts, And Docs

**Files:**
- Modify: `wukongim.conf.example`
- Modify: `docker/conf/node1.conf`
- Modify: `docker/conf/node2.conf`
- Modify: `docker/conf/node3.conf`
- Modify: `scripts/wukongimv2/wukongimv2.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node1.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node2.conf`
- Modify: `scripts/wukongimv2/wukongimv2-node3.conf`
- Modify: `scripts/wukongimv2_three_node_bench_script_test.go`
- Modify focused docs that mention removed keys, excluding archived perf-run evidence snapshots.

- [ ] **Step 1: Replace config keys in active examples**

Replace:

```text
WK_HEALTH_DEBUG_ENABLE=true
WK_PPROF_ENABLE=true
WK_DIAGNOSTICS_DEBUG_API_ENABLE=true
```

with:

```text
WK_DEBUG_API_ENABLE=true
```

Use `false` in examples where the previous active debug exposure was false.

- [ ] **Step 2: Update script tests**

Replace expected snippets such as:

```go
`WK_PPROF_ENABLE="${WK_PPROF_ENABLE:-true}"`,
```

with:

```go
`WK_DEBUG_API_ENABLE="${WK_DEBUG_API_ENABLE:-true}"`,
```

Update env capture regexes from:

```text
WK_PPROF_
```

to:

```text
WK_DEBUG_API_
```

- [ ] **Step 3: Run script and config example tests**

Run:

```bash
go test ./scripts ./cmd/wukongimv2 -run 'Test.*Config|Test.*Script' -count=1
```

Expected: PASS.

## Task 6: Final Verification

**Files:**
- All modified files.

- [ ] **Step 1: Search for removed identifiers**

Run:

```bash
rg -n 'WK_HEALTH_DEBUG_ENABLE|WK_PPROF_ENABLE|WK_DIAGNOSTICS_DEBUG_API_ENABLE|HealthDebugEnabled|PProfEnabled|DiagnosticsDebugEnabled|DebugAPIEnabled' cmd internal internalv2 docker scripts wukongim.conf.example docs/development docs/wiki
```

Expected: only `WK_DEBUG_API_ENABLE` and `DebugAPIEnabled` remain in active code/docs; removed names may remain only in historical `docs/superpowers/*` design/plan artifacts.

- [ ] **Step 2: Run focused package tests**

Run:

```bash
go test ./cmd/wukongim ./cmd/wukongimv2 ./internal/access/api ./internalv2/access/api ./internal/app ./internalv2/app ./scripts -count=1
```

Expected: PASS.

- [ ] **Step 3: Run broad unit tests if focused tests pass**

Run:

```bash
go test ./internal/... ./internalv2/... ./cmd/wukongim ./cmd/wukongimv2 ./scripts
```

Expected: PASS or report the first unrelated pre-existing failure with evidence.
