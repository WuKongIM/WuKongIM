# WuKongIMv2 Embedded Prometheus Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users start `cmd/wukongimv2` and optionally get a local Prometheus server without deploying a separate Prometheus service.

**Architecture:** Keep internalv2 metrics generation unchanged and add an app-owned Prometheus child-process runtime. `cmd/wukongimv2` parses `WK_PROMETHEUS_*` keys, `internalv2/app` writes a minimal Prometheus config that scrapes v2 API `/metrics`, extracts the embedded Prometheus binary when `WK_PROMETHEUS_BINARY_PATH` is empty, and supervises that child process from the v2 lifecycle.

**Tech Stack:** Go, `os/exec`, `internalv2/app` lifecycle, existing internalv2 `/metrics` endpoint, official Prometheus binary.

---

### File Structure

- Modify `internalv2/app/config.go`: add `PrometheusConfig` under `ObservabilityConfig`, defaults, validation, and English comments.
- Modify `cmd/wukongimv2/config.go`: parse `WK_PROMETHEUS_*` keys from file/env.
- Create `internalv2/app/prometheus.go`: config rendering, target defaults, command args, and process supervisor.
- Add `internalv2/app/prometheus_embedded/`: build-time staging directory for ignored Prometheus binaries embedded by go:embed.
- Modify `internalv2/app/app.go` and lifecycle methods: store/start/stop the Prometheus runtime after the API is available.
- Add focused tests in `cmd/wukongimv2/config_test.go`, `internalv2/app/observability_test.go`, and `internalv2/app/prometheus_test.go`.
- Modify `scripts/wukongimv2/*.conf`: document default-off Prometheus config for local v2 runs.
- Leave the root `Dockerfile` unchanged for now because it currently targets the legacy `cmd/wukongim` entry, which is outside this task.

### Task 1: v2 Configuration

- [x] Add failing config parser tests for `WK_PROMETHEUS_ENABLE`, binary path, listen address, data dir, retention time, retention size, scrape interval, and JSON scrape targets.
- [x] Add failing internalv2 app config tests for defaults and invalid values.
- [x] Implement `PrometheusConfig` with English field comments and validation.
- [x] Parse `WK_PROMETHEUS_*` in `cmd/wukongimv2/config.go`.
- [x] Run focused v2 config/app tests.

### Task 2: v2 Runtime Generation

- [x] Add failing tests for generated `prometheus.yml` with explicit multi-target scrape.
- [x] Add failing tests for command args including config file, storage path, retention settings, listen address, and lifecycle flag.
- [x] Implement `internalv2/app/prometheus.go` without importing `github.com/prometheus/prometheus`.
- [x] Run focused v2 app tests.

### Task 3: v2 Lifecycle Wiring

- [x] Add failing lifecycle tests asserting Prometheus starts after API and stops before API.
- [x] Wire the runtime into `internalv2/app` lifecycle only when enabled.
- [x] Ensure Prometheus enablement requires metrics collection and an API listen address.
- [x] Run focused v2 app tests.

### Task 4: Distribution Defaults

- [x] Update `scripts/wukongimv2/*.conf` with default-off `WK_PROMETHEUS_*` keys.
- [x] Update `cmd/wukongimv2/*.conf.example` with default-off `WK_PROMETHEUS_*` keys.
- [x] Keep the root `Dockerfile` unchanged because it targets legacy `cmd/wukongim`.
- [x] Keep `docker-compose.yml` unchanged unless later requested; external Prometheus remains useful for multi-node dev stacks.
- [x] Run `go test ./cmd/wukongimv2 ./internalv2/app ./scripts`.

### Task 5: Final Verification

- [x] Run `gofmt` on changed Go files.
- [x] Run focused unit tests.
- [x] Report any tests not run, especially full `go test ./...` if it is too expensive for this turn.
