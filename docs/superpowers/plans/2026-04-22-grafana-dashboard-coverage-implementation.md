# Grafana Dashboard Coverage Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expand Grafana assets so all exported `wukongim_*` business metrics are covered across a focused overview dashboard and two deep-dive dashboards.

**Architecture:** Keep Grafana provisioning unchanged while splitting dashboards by operational purpose: `overview`, `transport-rpc`, and `runtime-storage`. Protect the asset set with an automated validation test that parses dashboard JSON and checks metric coverage against `pkg/metrics/*.go`.

**Tech Stack:** Grafana dashboard JSON, PromQL, Go tests (`encoding/json`, file IO), existing Prometheus metrics under `pkg/metrics`.

---

## File Structure

### Production files

- Modify: `docker/observability/grafana/dashboards/wukongim-overview.json`
  Responsibility: keep only first-look operational panels and add one transport/RPC timeout summary signal.
- Create: `docker/observability/grafana/dashboards/wukongim-transport-rpc.json`
  Responsibility: deep-dive dashboard for transport and RPC throughput, latency, failures, congestion, and pool state.
- Create: `docker/observability/grafana/dashboards/wukongim-runtime-storage.json`
  Responsibility: deep-dive dashboard for gateway, channel, slot, controller, and storage behavior.
- Modify (only if needed): `docker/observability/grafana/provisioning/dashboards/wukongim.yml`
  Responsibility: keep provisioning loading all dashboards from the same folder.
- Create: `docker/observability/grafana/dashboard_assets_test.go`
  Responsibility: validate dashboard JSON assets and metric coverage.
- Create: `docs/superpowers/specs/2026-04-22-grafana-dashboard-coverage-design.md`
  Responsibility: record approved design decisions.

## Task 1: Add a failing dashboard coverage test

**Files:**
- Create: `docker/observability/grafana/dashboard_assets_test.go`

- [ ] **Step 1: Write the failing asset validation test**

Test responsibilities:
- Parse every JSON dashboard file under `docker/observability/grafana/dashboards`.
- Assert the dashboard set contains `wukongim-overview`, `wukongim-transport-rpc`, and `wukongim-runtime-storage`.
- Extract all `wukongim_*` metric names referenced in dashboard JSON.
- Compare that set against metric names defined in `pkg/metrics/*.go`.

- [ ] **Step 2: Run the focused dashboard asset test and verify it fails**

Run: `go test ./docker/observability/grafana -run 'TestDashboardAssetsCoverAllExportedMetrics' -count=1`

Expected: FAIL because the two new dashboards do not exist yet and several metrics are not referenced.

## Task 2: Implement the dashboard split

**Files:**
- Modify: `docker/observability/grafana/dashboards/wukongim-overview.json`
- Create: `docker/observability/grafana/dashboards/wukongim-transport-rpc.json`
- Create: `docker/observability/grafana/dashboards/wukongim-runtime-storage.json`

- [ ] **Step 1: Update overview to match landing-page scope**

Keep only high-signal first-look panels and add the RPC client timeout rate summary panel.

- [ ] **Step 2: Create the transport/RPC deep-dive dashboard**

Include panels for:
- RPC server/client volume
- RPC timeout/queue_full/dial_error summary
- RPC server/client latency P95/P99
- Result breakdown by service/result/target
- Inflight and pool connection state
- Transport throughput

- [ ] **Step 3: Create the runtime/storage deep-dive dashboard**

Include panels for:
- Gateway lifecycle/auth/frame handling/bytes
- Channel append/fetch/activity
- Slot proposals/apply/election/top slots
- Controller decisions/tasks/migrations/node health
- Storage totals and per-store usage

- [ ] **Step 4: Keep variables and styling consistent**

Use the existing `node_name` variable style and add only the approved low-cardinality variables.

## Task 3: Verify dashboards and coverage

**Files:**
- Modify if needed: `docker/observability/grafana/provisioning/dashboards/wukongim.yml`
- Modify if needed: `docker/observability/grafana/dashboard_assets_test.go`

- [ ] **Step 1: Re-run the dashboard asset test**

Run: `go test ./docker/observability/grafana -run 'TestDashboardAssetsCoverAllExportedMetrics' -count=1`

Expected: PASS.

- [ ] **Step 2: Run an extra parse-only check across the dashboard JSON files**

Run: `python3 - <<'PY'
import json, pathlib
for path in sorted(pathlib.Path('docker/observability/grafana/dashboards').glob('*.json')):
    json.loads(path.read_text())
    print(path.name, 'ok')
PY`

Expected: all dashboard files print `ok`.

- [ ] **Step 3: Confirm provisioning still points at the dashboards directory**

Run: `sed -n '1,120p' docker/observability/grafana/provisioning/dashboards/wukongim.yml`

Expected: provider still loads `/etc/grafana/dashboards` and therefore picks up all three dashboards.
