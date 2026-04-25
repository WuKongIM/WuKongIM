# Legacy Default Ports Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

> Historical note: this plan was written against the earlier JSON loader. The current startup path uses `wukongim.conf` plus environment-variable overrides in `cmd/wukongim/config.go`.

**Goal:** Make `cmd/wukongim` default its API, TCP, and WebSocket listener ports to the legacy WuKongIM values when those JSON fields are omitted.

**Architecture:** Keep compatibility isolated to the JSON config loading layer in `cmd/wukongim/config.go`. Do not change `internal/app` validation semantics; instead, detect omitted config fields during decode and synthesize the legacy defaults there.

**Tech Stack:** Go, `encoding/json`, existing gateway listener bindings, `testify/require`

---

### Task 1: Add failing tests for omitted-field defaults

**Files:**
- Modify: `cmd/wukongim/config_test.go`
- Test: `cmd/wukongim/config_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestLoadConfigDefaultsLegacyPortsWhenFieldsOmitted(t *testing.T) {
    // omit api.listenAddr and gateway.listeners
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/wukongim -run 'TestLoadConfigDefaultsLegacyPortsWhenFieldsOmitted'`
Expected: FAIL because omitted fields currently remain empty and validation/load assertions do not match legacy defaults.

- [ ] **Step 3: Write a preservation test for explicit values**

```go
func TestLoadConfigPreservesExplicitListenAddresses(t *testing.T) {
    // explicit api.listenAddr and gateway.listeners should remain unchanged
}
```

- [ ] **Step 4: Run focused tests**

Run: `go test ./cmd/wukongim -run 'TestLoadConfig(Default|Preserves)'`
Expected: legacy-default test fails, explicit-values test passes or stays green.

- [ ] **Step 5: Commit**

```bash
git add cmd/wukongim/config_test.go
git commit -m "test(cmd): cover legacy default ports"
```

### Task 2: Implement omitted-field defaulting in config loading

**Files:**
- Modify: `cmd/wukongim/config.go`
- Test: `cmd/wukongim/config_test.go`

- [ ] **Step 1: Add omitted-field tracking in wire config**

```go
type wireAPIConfig struct {
    ListenAddr *string `json:"listenAddr"`
}

type wireGatewayConfig struct {
    TokenAuthOn    bool                       `json:"tokenAuthOn"`
    DefaultSession wireSessionOptions         `json:"defaultSession"`
    Listeners      *[]gateway.ListenerOptions `json:"listeners"`
}
```

- [ ] **Step 2: Add minimal defaulting logic**

```go
if c.API.ListenAddr == nil {
    cfg.API.ListenAddr = "0.0.0.0:5001"
}
if c.Gateway.Listeners == nil {
    cfg.Gateway.Listeners = []gateway.ListenerOptions{
        binding.TCPWKProto("tcp-wkproto", "0.0.0.0:5100"),
        binding.WSJSONRPC("ws-jsonrpc", "0.0.0.0:5200"),
    }
}
```

- [ ] **Step 3: Preserve explicit values exactly**

```go
if c.API.ListenAddr != nil {
    cfg.API.ListenAddr = *c.API.ListenAddr
}
if c.Gateway.Listeners != nil {
    cfg.Gateway.Listeners = append([]gateway.ListenerOptions(nil), (*c.Gateway.Listeners)...)
}
```

- [ ] **Step 4: Run focused tests to verify green**

Run: `go test ./cmd/wukongim -run 'TestLoadConfig'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/wukongim/config.go cmd/wukongim/config_test.go
git commit -m "feat(cmd): default legacy listener ports"
```

### Task 3: Verify compatibility boundaries

**Files:**
- Test: `cmd/wukongim/config_test.go`
- Test: `internal/app/config_test.go`

- [ ] **Step 1: Run cmd config tests**

Run: `go test ./cmd/wukongim`
Expected: PASS

- [ ] **Step 2: Run app config regression tests**

Run: `go test ./internal/app/...`
Expected: PASS, including the existing empty-API behavior tests.

- [ ] **Step 3: Run related access and gateway tests**

Run: `go test ./internal/access/api/... ./internal/gateway/...`
Expected: PASS

- [ ] **Step 4: Review diff for scope control**

Run: `git diff -- cmd/wukongim/config.go cmd/wukongim/config_test.go`
Expected: only config-loading and tests changed for this feature.

- [ ] **Step 5: Commit**

```bash
git add cmd/wukongim/config.go cmd/wukongim/config_test.go docs/superpowers/specs/2026-04-06-legacy-default-ports-design.md docs/superpowers/plans/2026-04-06-legacy-default-ports.md
git commit -m "docs: record legacy default ports plan"
```
