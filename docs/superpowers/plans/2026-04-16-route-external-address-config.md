# Route External Address Config Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let `/route` and `/route/batch` publish explicit externally configured TCP/WS/WSS addresses without changing actual listener bindings.

**Architecture:** Add three explicit fields to `app.APIConfig`, parse them from `WK_EXTERNAL_*` environment/config keys, and merge them into the legacy route address injection during app build. The merge logic prefers explicit external addresses and falls back to gateway-listener-derived values when a field is not configured.

**Tech Stack:** Go, Viper config loading, Gin API server, existing app/config tests.

---

### Task 1: Parse the new external route config keys

**Files:**
- Modify: `internal/app/config.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Write the failing test**
Add a config test that sets `WK_EXTERNAL_TCPADDR`, `WK_EXTERNAL_WSADDR`, and `WK_EXTERNAL_WSSADDR` and asserts the fields are present in `cfg.API`.

- [ ] **Step 2: Run test to verify it fails**
Run: `go test ./cmd/wukongim -run 'TestLoadConfigParsesExternalRouteAddresses'`
Expected: FAIL because `APIConfig` does not yet expose those fields.

- [ ] **Step 3: Write minimal implementation**
Add the fields to `APIConfig`, parse the three config keys into `cfg.API`, and document them in `wukongim.conf.example`.

- [ ] **Step 4: Run test to verify it passes**
Run: `go test ./cmd/wukongim -run 'TestLoadConfigParsesExternalRouteAddresses'`
Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/app/config.go cmd/wukongim/config.go cmd/wukongim/config_test.go wukongim.conf.example docs/superpowers/specs/2026-04-16-route-external-address-config-design.md docs/superpowers/plans/2026-04-16-route-external-address-config.md
git commit -m "feat: parse route external address config"
```

### Task 2: Prefer explicit external addresses in app build

**Files:**
- Modify: `internal/app/build.go`
- Modify: `internal/app/config_test.go` or `internal/app/build_test.go`

- [ ] **Step 1: Write the failing test**
Add a unit test for the legacy route address merge logic proving explicit `API.External*` values override listener-derived values while unspecified fields still fall back.

- [ ] **Step 2: Run test to verify it fails**
Run: `go test ./internal/app -run 'TestLegacyRouteAddressesPreferExplicitExternalConfig'`
Expected: FAIL because build currently only derives addresses from listeners.

- [ ] **Step 3: Write minimal implementation**
Extract or update the merge helper so it combines explicit API external address config with listener-derived route addresses.

- [ ] **Step 4: Run test to verify it passes**
Run: `go test ./internal/app -run 'TestLegacyRouteAddressesPreferExplicitExternalConfig'`
Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/app/build.go internal/app/config_test.go docs/superpowers/specs/2026-04-16-route-external-address-config-design.md docs/superpowers/plans/2026-04-16-route-external-address-config.md
git commit -m "feat: use explicit route external addresses"
```

### Task 3: Verify the integrated behavior

**Files:**
- Test: `cmd/wukongim/config_test.go`
- Test: `internal/app/config_test.go`
- Test: `internal/access/api/server_test.go`

- [ ] **Step 1: Run focused config verification**
Run: `go test ./cmd/wukongim -run 'TestLoadConfigParsesExternalRouteAddresses'`
Expected: PASS.

- [ ] **Step 2: Run focused app verification**
Run: `go test ./internal/app -run 'TestLegacyRouteAddressesPreferExplicitExternalConfig'`
Expected: PASS.

- [ ] **Step 3: Run broader regression verification**
Run: `go test ./internal/access/api ./cmd/wukongim ./internal/app -run 'TestRoute|TestLoadConfig|TestLegacyRouteAddresses|TestNewBuildsOptionalAPIServerWhenConfigured'`
Expected: PASS for the impacted areas.
