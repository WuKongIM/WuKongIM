# Channel Runtime Meta Bootstrap Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the durable send refresh path bootstrap missing `ChannelRuntimeMeta` for any channel type while keeping generic metadata reads side-effect free.

**Architecture:** Keep `GetChannelRuntimeMeta()` pure and add an explicit bootstrap coordinator in `internal/app` that derives initial runtime metadata from the current slot topology, writes it authoritatively, re-reads it, and then applies it locally through `channelMetaSync.RefreshChannelMeta()`. Scope the first rollout to the send refresh path only, add a configurable bootstrap default `MinISR` with single-node clamping, and update the send/integration/docs surface to match the new behavior.

**Tech Stack:** Go, existing `internal/app` composition root, `pkg/cluster` topology APIs, `pkg/slot/proxy` metadata store, Go test, Viper config loading, repo docs/FLOW files.

---

### File Structure Map

**New focused file:**
- Create: `internal/app/channelmeta_bootstrap.go` — encapsulates runtime-meta bootstrap policy, topology lookup, candidate-meta derivation, `MinISR` clamping, retryable topology-failure handling, authoritative write/re-read, and structured bootstrap logs.

**Existing files that own the rest of the behavior:**
- Modify: `internal/app/channelmeta.go` — `RefreshChannelMeta()` becomes ensure-capable on authoritative miss while preserving sync/start/stop behavior and logging the missing/ensure path.
- Modify: `internal/app/build.go` — wires the bootstrap coordinator into `channelMetaSync`.
- Modify: `internal/app/config.go` — adds bootstrap `MinISR` config, explicit-set tracking, and default/validation rules.
- Modify: `cmd/wukongim/config.go` — parses `WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR` and marks it explicit when present.
- Modify: `wukongim.conf.example` — documents the new config key.
- Modify: `internal/app/channelmeta_test.go` — bootstrap-specific unit coverage, including retryable topology errors and structured log assertions.
- Modify: `internal/usecase/message/send_test.go` — send-path regression coverage for missing runtime metadata.
- Modify: `internal/app/integration_test.go` — single-node first-send regression without pre-seeded runtime metadata.
- Modify: `internal/app/comm_test.go` — add any small helper needed by integration tests without duplicating setup logic.
- Modify: `internal/FLOW.md` — document the new refresh/bootstrap path.
- Modify: `docs/wiki/architecture/05-message-sending-flow.md` — align architecture docs with the approved bootstrap behavior.

### Task 1: Add bootstrap `MinISR` config plumbing

**Files:**
- Modify: `internal/app/config.go`
- Modify: `internal/app/config_test.go`
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Write the failing app-config tests**
Add `internal/app/config_test.go` coverage that asserts:
- the field defaults to `2` when it is unset/not explicitly provided
- an explicit non-positive value is rejected instead of being silently defaulted

```go
func TestConfigDefaultsChannelBootstrapMinISR(t *testing.T) {
    cfg := validConfig()
    cfg.Cluster.ChannelBootstrapDefaultMinISR = 0
    cfg.Cluster.SetExplicitFlags(false)

    require.NoError(t, cfg.ApplyDefaultsAndValidate())
    require.Equal(t, 2, cfg.Cluster.ChannelBootstrapDefaultMinISR)
}

func TestConfigRejectsExplicitNonPositiveChannelBootstrapMinISR(t *testing.T) {
    cfg := validConfig()
    cfg.Cluster.ChannelBootstrapDefaultMinISR = 0
    cfg.Cluster.SetExplicitFlags(true)

    require.ErrorContains(t, cfg.ApplyDefaultsAndValidate(), "channel bootstrap default min isr")
}
```

- [ ] **Step 2: Run the failing app-config tests**
Run: `go test ./internal/app -run 'TestConfigDefaultsChannelBootstrapMinISR|TestConfigRejectsNonPositiveChannelBootstrapMinISR'`
Expected: FAIL because `ClusterConfig` does not yet expose the field/default or explicit-set handling.

- [ ] **Step 3: Write the failing loader tests**
Add `cmd/wukongim/config_test.go` coverage that:
- sets `WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR=3` and asserts `cfg.Cluster.ChannelBootstrapDefaultMinISR == 3`
- sets `WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR=0` and asserts `loadConfig()` fails because the value was explicitly provided and is invalid

```go
func TestLoadConfigParsesChannelBootstrapDefaultMinISR(t *testing.T) {
    // build config with WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR=3
    require.Equal(t, 3, cfg.Cluster.ChannelBootstrapDefaultMinISR)
}

func TestLoadConfigRejectsExplicitZeroChannelBootstrapDefaultMinISR(t *testing.T) {
    // build config with WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR=0
    require.ErrorContains(t, err, "channel bootstrap default min isr")
}
```

- [ ] **Step 4: Run the failing loader tests**
Run: `go test ./cmd/wukongim -run 'TestLoadConfig(ParsesChannelBootstrapDefaultMinISR|RejectsExplicitZeroChannelBootstrapDefaultMinISR)'`
Expected: FAIL because the key is not parsed and explicit-set semantics do not yet exist.

- [ ] **Step 5: Write the minimal config implementation**
Add `ChannelBootstrapDefaultMinISR` plus an explicit-set flag/helper to `internal/app/config.go`, default it to `2` only when the key is absent, validate explicit values are `> 0`, parse `WK_CLUSTER_CHANNEL_BOOTSTRAP_DEFAULT_MIN_ISR` in `cmd/wukongim/config.go`, mark the field explicit when the key is present, and document the key in `wukongim.conf.example`.

- [ ] **Step 6: Run the focused config tests**
Run: `go test ./internal/app -run 'TestConfigDefaultsChannelBootstrapMinISR|TestConfigRejectsNonPositiveChannelBootstrapMinISR' && go test ./cmd/wukongim -run 'TestLoadConfig(ParsesChannelBootstrapDefaultMinISR|RejectsExplicitZeroChannelBootstrapDefaultMinISR)'`
Expected: PASS.

- [ ] **Step 7: Commit**
```bash
git add internal/app/config.go internal/app/config_test.go cmd/wukongim/config.go cmd/wukongim/config_test.go wukongim.conf.example docs/superpowers/specs/2026-04-17-channel-runtime-meta-bootstrap-design.md docs/superpowers/plans/2026-04-17-channel-runtime-meta-bootstrap.md
git commit -m "feat: add channel runtime bootstrap min isr config"
```

### Task 2: Implement the bootstrap coordinator in `internal/app`

**Files:**
- Create: `internal/app/channelmeta_bootstrap.go`
- Modify: `internal/app/channelmeta_test.go`
- Modify: `internal/app/build.go`

- [ ] **Step 1: Write the failing bootstrap-derivation tests**
Add `internal/app/channelmeta_test.go` cases covering:
- derived `Replicas`, `Leader`, and `ISR` come from slot topology
- `MinISR` defaults to `2`
- `MinISR` clamps to `1` when replica count is `1`
- empty peers returns an error
- `LeaderOf` / slot ownership instability propagates retryable cluster errors such as `raftcluster.ErrNoLeader` or `raftcluster.ErrSlotNotFound` unchanged
- bootstrap success/failure emits the required structured log events and fields

```go
func TestChannelMetaBootstrapperDerivesInitialMetaFromSlotTopology(t *testing.T) {
    bootstrapper := newChannelMetaBootstrapper(...)

    meta, created, err := bootstrapper.EnsureChannelRuntimeMeta(ctx, channel.ChannelID{ID: "u2@u1", Type: 1})
    require.NoError(t, err)
    require.True(t, created)
    require.Equal(t, []uint64{1, 2, 3}, meta.Replicas)
    require.Equal(t, int64(2), meta.MinISR)
}
```

- [ ] **Step 2: Run the failing bootstrap tests**
Run: `go test ./internal/app -run 'TestChannelMetaBootstrapper'`
Expected: FAIL because no bootstrap coordinator exists.

- [ ] **Step 3: Write the minimal bootstrap coordinator**
Create `internal/app/channelmeta_bootstrap.go` with a focused type that:
- asks the cluster for `SlotForKey`, `PeersForSlot`, and `LeaderOf`
- derives candidate `metadb.ChannelRuntimeMeta`
- clamps `MinISR` with `min(configuredDefault, len(replicas))`
- treats slot-topology instability (`ErrNoLeader`, `ErrSlotNotFound`) as retryable bootstrap failures by returning the original cluster errors unchanged
- writes through `UpsertChannelRuntimeMeta()`
- re-reads authoritative metadata and returns the authoritative result
- emits structured `missing`, `bootstrapped`, and `failed` logs with `channelID`, `channelType`, `slotID`, `leader`, `replicaCount`, `minISR`, and `created` when applicable

Keep bootstrap lease as an internal constant in this file.

- [ ] **Step 4: Wire the bootstrap coordinator into build**
Update `internal/app/build.go` so `channelMetaSync` receives the new bootstrap dependency and the configured default `MinISR`.

- [ ] **Step 5: Run the focused bootstrap tests**
Run: `go test ./internal/app -run 'TestChannelMetaBootstrapper'`
Expected: PASS.

- [ ] **Step 6: Commit**
```bash
git add internal/app/channelmeta_bootstrap.go internal/app/channelmeta_test.go internal/app/build.go docs/superpowers/specs/2026-04-17-channel-runtime-meta-bootstrap-design.md docs/superpowers/plans/2026-04-17-channel-runtime-meta-bootstrap.md
git commit -m "feat: add channel runtime meta bootstrap coordinator"
```

### Task 3: Make `RefreshChannelMeta()` ensure-capable on authoritative miss

**Files:**
- Modify: `internal/app/channelmeta.go`
- Modify: `internal/app/channelmeta_test.go`

- [ ] **Step 1: Write the failing refresh-path tests**
Add `internal/app/channelmeta_test.go` cases proving:
- `RefreshChannelMeta()` returns existing authoritative meta without bootstrap
- `RefreshChannelMeta()` handles `metadb.ErrNotFound` by calling bootstrap, re-reading authoritative metadata, and then applying it
- refresh never applies the locally generated candidate if the authoritative re-read returns different data
- bootstrap failure does not apply partial metadata locally
- refresh preserves current retry semantics by surfacing retryable bootstrap errors without applying local state

```go
func TestChannelMetaSyncRefreshBootstrapsOnAuthoritativeMiss(t *testing.T) {
    got, err := syncer.RefreshChannelMeta(ctx, channel.ChannelID{ID: "g1", Type: 2})
    require.NoError(t, err)
    require.Equal(t, uint64(1), got.Epoch)
    require.Equal(t, []channel.Meta{got}, cluster.applied)
}
```

- [ ] **Step 2: Run the failing refresh-path tests**
Run: `go test ./internal/app -run 'TestChannelMetaSyncRefresh'`
Expected: FAIL because refresh currently returns `metadb.ErrNotFound` directly.

- [ ] **Step 3: Write the minimal refresh integration**
Update `internal/app/channelmeta.go` so `RefreshChannelMeta()`:
- still starts with the current pure read
- recognizes `metadb.ErrNotFound`
- delegates to the bootstrap coordinator
- re-reads authoritative metadata
- projects and applies the authoritative value
- leaves retryable bootstrap errors untouched so send keeps its existing retry/fail behavior

Keep `syncOnce()`, `Start()`, and `Stop()` behavior unchanged.

- [ ] **Step 4: Run the focused refresh-path tests**
Run: `go test ./internal/app -run 'TestChannelMetaSyncRefresh'`
Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/app/channelmeta.go internal/app/channelmeta_test.go docs/superpowers/specs/2026-04-17-channel-runtime-meta-bootstrap-design.md docs/superpowers/plans/2026-04-17-channel-runtime-meta-bootstrap.md
git commit -m "feat: bootstrap missing runtime metadata during refresh"
```

### Task 4: Prove the durable send path heals missing runtime metadata

**Files:**
- Modify: `internal/usecase/message/send_test.go`
- Modify: `internal/app/integration_test.go`
- Modify: `internal/app/comm_test.go`

- [ ] **Step 1: Write the failing send unit tests**
Add `internal/usecase/message/send_test.go` coverage where:
- the first append returns `channel.ErrStaleMeta`, the refresher bootstraps metadata, and the second append succeeds
- the bootstrap path remains channel-type agnostic by covering at least one non-person channel case
- retryable bootstrap failures such as `raftcluster.ErrNoLeader` still fail the send after the single refresh attempt without applying a second append

```go
func TestSendRetriesOnceAfterBootstrappingMissingRuntimeMeta(t *testing.T) {
    // first append => ErrStaleMeta
    // refresh => authoritative bootstrap meta
    // second append => success
}
```

- [ ] **Step 2: Run the failing send unit tests**
Run: `go test ./internal/usecase/message -run 'TestSend(RetriesOnceAfterBootstrappingMissingRuntimeMeta|FailsWhenBootstrapReturnsRetryableTopologyError)'`
Expected: FAIL because the fake refresh path cannot currently model bootstrap-on-miss behavior or retryable bootstrap failures end to end.

- [ ] **Step 3: Write the failing integration regression**
Add two single-node integration tests in `internal/app/integration_test.go`:
- first send succeeds without pre-seeded runtime metadata
- a direct runtime-meta read miss stays pure and does not create metadata as a side effect

```go
func TestAppStartBootstrapsMissingRuntimeMetaOnFirstSend(t *testing.T) {
    // no seedChannelRuntimeMeta call
    // first send succeeds
}

func TestAppRuntimeMetaReadMissDoesNotBootstrap(t *testing.T) {
    // read miss stays ErrNotFound until the send path explicitly bootstraps it
}
```

- [ ] **Step 4: Run the failing integration regression**
Run: `go test -tags=integration ./internal/app -run 'TestApp(StartBootstrapsMissingRuntimeMetaOnFirstSend|RuntimeMetaReadMissDoesNotBootstrap)'`
Expected: the first-send case FAILS because the send path still dies on `metadb.ErrNotFound`; the pure-read case should already demonstrate no implicit bootstrap.

- [ ] **Step 5: Write the minimal test-support updates**
Adjust `internal/usecase/message/send_test.go` fakes and `internal/app/comm_test.go` helpers only as needed to support the new bootstrap-aware assertions without changing production semantics.

- [ ] **Step 6: Run the focused send and integration tests**
Run: `go test ./internal/usecase/message -run 'TestSend(RetriesOnceAfterBootstrappingMissingRuntimeMeta|FailsWhenBootstrapReturnsRetryableTopologyError)' && go test -tags=integration ./internal/app -run 'TestApp(StartBootstrapsMissingRuntimeMetaOnFirstSend|RuntimeMetaReadMissDoesNotBootstrap)'`
Expected: PASS.

- [ ] **Step 7: Commit**
```bash
git add internal/usecase/message/send_test.go internal/app/integration_test.go internal/app/comm_test.go docs/superpowers/specs/2026-04-17-channel-runtime-meta-bootstrap-design.md docs/superpowers/plans/2026-04-17-channel-runtime-meta-bootstrap.md
git commit -m "test: cover runtime meta bootstrap on first send"
```

### Task 5: Update docs/FLOW and verify impacted surfaces

**Files:**
- Modify: `internal/FLOW.md`
- Modify: `docs/wiki/architecture/05-message-sending-flow.md`
- Test: `internal/app/channelmeta_test.go`
- Test: `internal/usecase/message/send_test.go`
- Test: `cmd/wukongim/config_test.go`
- Test: `internal/app/config_test.go`
- Test: `internal/app/integration_test.go`

- [ ] **Step 1: Update `internal/FLOW.md`**
Document that the send refresh path now bootstraps missing authoritative runtime metadata before retrying append, and that runtime-meta bootstrap does not depend on business channel-info existence.

- [ ] **Step 2: Update architecture wiki**
Revise `docs/wiki/architecture/05-message-sending-flow.md` so the refresh/retry section explains `ErrNotFound -> bootstrap -> re-read -> apply -> retry`.

- [ ] **Step 3: Run focused non-integration verification**
Run: `go test ./internal/app -run 'TestChannelMeta(SyncRefresh|Bootstrapper)' && go test ./internal/app -run 'TestConfigDefaultsChannelBootstrapMinISR|TestConfigRejectsNonPositiveChannelBootstrapMinISR' && go test ./cmd/wukongim -run 'TestLoadConfig(ParsesChannelBootstrapDefaultMinISR|RejectsExplicitZeroChannelBootstrapDefaultMinISR)' && go test ./internal/usecase/message -run 'TestSend(RetriesOnceAfterBootstrappingMissingRuntimeMeta|FailsWhenBootstrapReturnsRetryableTopologyError)'`
Expected: PASS, including the structured bootstrap log assertions in `TestChannelMetaBootstrapper` / `TestChannelMetaSyncRefresh`.

- [ ] **Step 4: Run the integration verification**
Run: `go test -tags=integration ./internal/app -run 'TestApp(StartBootstrapsMissingRuntimeMetaOnFirstSend|RuntimeMetaReadMissDoesNotBootstrap)'`
Expected: PASS.

- [ ] **Step 5: Run a broader impacted-area regression sweep**
Run: `go test ./cmd/wukongim ./internal/usecase/message ./internal/app -run 'TestLoadConfig|TestConfig|TestChannelMeta|TestSend'`
Expected: PASS for the impacted packages.

- [ ] **Step 6: Commit**
```bash
git add internal/FLOW.md docs/wiki/architecture/05-message-sending-flow.md docs/superpowers/specs/2026-04-17-channel-runtime-meta-bootstrap-design.md docs/superpowers/plans/2026-04-17-channel-runtime-meta-bootstrap.md
git commit -m "docs: document runtime meta bootstrap flow"
```
