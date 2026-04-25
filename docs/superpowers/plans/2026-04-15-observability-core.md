# Observability Core Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the first production-ready observability slice for WuKongIM: Prometheus metrics, health/readiness endpoints, debug hooks, and initial gateway/channel/cluster instrumentation wired through the existing app composition root.

**Architecture:** Keep observability low-intrusion by introducing a reusable `pkg/metrics` registry, lightweight observer hooks at gateway and cluster boundaries, and an app-owned health snapshot provider for the HTTP API. Avoid a global service layer; compose metrics and health wiring inside `internal/app` and inject handlers into `internal/access/api`.

**Tech Stack:** Go 1.23, Gin, Prometheus client_golang, existing gateway/core server hooks, existing cluster observer hooks.

---

### Task 1: Configuration and API surface

**Files:**
- Modify: `internal/app/config.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `wukongim.conf.example`

- [ ] Add observability config fields and explicit-default handling for metrics/health/debug switches.
- [ ] Add failing config tests for explicit values and defaults.
- [ ] Parse `WK_METRICS_ENABLE`, `WK_HEALTH_DETAIL_ENABLE`, and `WK_HEALTH_DEBUG_ENABLE`.
- [ ] Update the example config so it stays aligned with runtime config behavior.

### Task 2: Metrics foundation package

**Files:**
- Create: `pkg/metrics/registry.go`
- Create: `pkg/metrics/gateway.go`
- Create: `pkg/metrics/channel.go`
- Create: `pkg/metrics/cluster.go`
- Create: `pkg/metrics/registry_test.go`

- [ ] Add failing tests that gather metrics from a custom registry with node labels.
- [ ] Implement a reusable metrics registry with Go/process collectors.
- [ ] Add gateway, channel, and cluster-facing collectors with helper methods used by observers.
- [ ] Keep registration isolated from global Prometheus state.

### Task 3: Gateway and channel instrumentation

**Files:**
- Modify: `internal/gateway/types/options.go`
- Create: `internal/gateway/types/observer.go`
- Modify: `internal/gateway/core/server.go`
- Modify: `internal/gateway/core/server_test.go`
- Modify: `internal/app/channelcluster.go`
- Create: `internal/app/channelcluster_test.go`

- [ ] Add failing tests for gateway observer callbacks and channel metric updates.
- [ ] Instrument gateway connection/auth/frame/outbound paths through an optional observer hook.
- [ ] Instrument channel append/fetch/active-channel state in `appChannelCluster` without changing channel semantics.
- [ ] Wire cluster observer hooks from `pkg/cluster.Config.Observer` into the same metrics registry where practical.

### Task 4: Health, readiness, metrics, and debug endpoints

**Files:**
- Modify: `internal/access/api/server.go`
- Modify: `internal/access/api/routes.go`
- Modify: `internal/access/api/health.go`
- Create: `internal/access/api/debug.go`
- Modify: `internal/access/api/server_test.go`
- Modify: `internal/access/api/integration_test.go`
- Create: `internal/app/observability.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/build.go`

- [ ] Add failing API tests for `/metrics`, `/healthz/details`, `/readyz`, and debug route gating.
- [ ] Build an app-owned observability snapshot provider for health/readiness/debug JSON.
- [ ] Inject metrics handler + health/debug closures into the API server from `internal/app/build.go`.
- [ ] Register `/metrics` when enabled, summary/detail health endpoints, readiness, and pprof/config debug endpoints when allowed.

### Task 5: Verification

**Files:**
- Test: `cmd/wukongim/config_test.go`
- Test: `pkg/metrics/...`
- Test: `internal/gateway/core/...`
- Test: `internal/access/api/...`
- Test: `internal/app/...`

- [ ] Run focused unit tests for config, metrics, gateway core, API, and app observability wiring.
- [ ] Run a broader regression sweep over touched packages.
- [ ] Review resulting `/metrics`-path behavior and ensure no AGENTS.md constraints were violated.
