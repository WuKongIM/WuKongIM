# App Readiness Test Stability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate the contention-sensitive conversation API test failure while preserving the production Channel data-plane lease and fail-closed append semantics.

**Architecture:** Make lease rejection evidence explicit from one atomic timestamp/time sample, then make the shared single-node-cluster test fixture use the production-equivalent 30-second lease budget and explicitly disable its unrelated plugin runtime. The pagination test continues to use the real Controller, Slot, Channel, message DB, and HTTP composition, but no longer relies on an arbitrary sleep.

**Tech Stack:** Go 1.25.11, standard `errors` wrapping, WuKongIM Cluster/Channel runtime, Go testing and race detector.

## Global Constraints

- A one-node test remains a single-node cluster; do not add a local append bypass or test-mode production branch.
- Keep the production health-report defaults and fail-closed behavior unchanged.
- Do not add append retries, retry backoff, or a longer foreground SEND timeout.
- Keep all three failure reasons low-cardinality and internal; do not add channel IDs or UIDs to metrics.
- Dedicated lease tests use an injected clock. They must not sleep.
- Use `GOWORK=off` and explicit Go package roots; never run repository-root `./...`.
- Read and update `pkg/cluster/FLOW.md` when changing lease semantics.

---

## File Map

| Path | Responsibility |
| --- | --- |
| `pkg/cluster/channel_lease.go` | Classify one sampled lease as missing, expired, invalid-clock, or ready. |
| `pkg/cluster/channel_lease_test.go` | Prove stable reasons, `errors.Is`, and same-sample admission behavior. |
| `pkg/cluster/FLOW.md` | Document data-plane lease rejection evidence. |
| `internal/app/sendack_smoke_test.go` | Define the shared real-cluster smoke fixture and its contract test. |
| `internal/app/conversation_api_smoke_test.go` | Exercise pagination without scheduler-based ordering. |

### Task 1: Add typed data-plane lease failure evidence

**Files:**
- Modify: `pkg/cluster/channel_lease_test.go`
- Modify: `pkg/cluster/channel_lease.go`
- Modify: `pkg/cluster/FLOW.md`

**Interfaces:**

```go
type channelDataPlaneLeaseFailureReason string

const (
	channelDataPlaneLeaseReasonMissing      channelDataPlaneLeaseFailureReason = "data_plane_lease_missing"
	channelDataPlaneLeaseReasonExpired      channelDataPlaneLeaseFailureReason = "data_plane_lease_expired"
	channelDataPlaneLeaseReasonClockInvalid channelDataPlaneLeaseFailureReason = "data_plane_lease_clock_invalid"
)

type channelDataPlaneLeaseError struct {
	reason channelDataPlaneLeaseFailureReason
}

func (e *channelDataPlaneLeaseError) Error() string
func (e *channelDataPlaneLeaseError) Unwrap() error
```

- [ ] **Step 1: Add a failing table test for all rejection reasons**

Add `TestChannelDataPlaneLeaseGuardReportsStableFailureReasons` to `pkg/cluster/channel_lease_test.go`. Cover a missing mark, a mark 31 seconds old with a 30-second TTL, and a mark one second in the future. Each case must assert:

```go
if !errors.Is(err, ch.ErrNotReady) {
	t.Fatalf("AllowChannelAppend() error = %v, want ErrNotReady", err)
}
var leaseErr *channelDataPlaneLeaseError
if !errors.As(err, &leaseErr) {
	t.Fatalf("AllowChannelAppend() error = %T, want *channelDataPlaneLeaseError", err)
}
if leaseErr.reason != test.wantReason {
	t.Fatalf("reason = %q, want %q", leaseErr.reason, test.wantReason)
}
```

Run:

```bash
GOWORK=off go test ./pkg/cluster \
  -run '^TestChannelDataPlaneLeaseGuardReportsStableFailureReasons$' -count=1
```

Expected: FAIL because the guard currently returns bare `channel.ErrNotReady` and the typed error does not exist.

- [ ] **Step 2: Implement classification from one observation**

In `pkg/cluster/channel_lease.go`, add the reason constants and typed error. `Error` must return `channel: not ready: <reason>` and `Unwrap` must return `ch.ErrNotReady`.

Add a pure helper:

```go
func classifyChannelDataPlaneLease(last *time.Time, ttl time.Duration, now time.Time) channelDataPlaneLeaseFailureReason {
	if last == nil {
		return channelDataPlaneLeaseReasonMissing
	}
	age := now.Sub(*last)
	if age < 0 {
		return channelDataPlaneLeaseReasonClockInvalid
	}
	if ttl <= 0 || age > ttl {
		return channelDataPlaneLeaseReasonExpired
	}
	return ""
}
```

Change `AllowChannelAppend` so context cancellation remains first, then exactly one `lastOK.Load()` and one `g.now()` determine both the admission result and returned reason. Refactor `snapshot` to compute `ready` from the same loaded timestamp and current-time sample; retain the existing same-observation snapshot test.

- [ ] **Step 3: Run the complete lease unit set**

```bash
GOWORK=off go test ./pkg/cluster -run '^TestChannelDataPlaneLeaseGuard' -count=1
```

Expected: PASS, including fresh-at-TTL equality, missing, expired, rollback, cancellation, and same-observation tests.

- [ ] **Step 4: Document the error contract**

Update the data-plane health/lease section of `pkg/cluster/FLOW.md`: the guard still exposes `errors.Is(channel.ErrNotReady)`, with stable details `data_plane_lease_missing`, `data_plane_lease_expired`, and `data_plane_lease_clock_invalid`; these are diagnostic details, not high-cardinality labels.

- [ ] **Step 5: Commit the lease evidence change**

```bash
git add pkg/cluster/channel_lease.go pkg/cluster/channel_lease_test.go pkg/cluster/FLOW.md
git commit -m "fix(cluster): classify data plane lease failures"
```

### Task 2: Stabilize the shared real-cluster app fixture

**Files:**
- Modify: `internal/app/sendack_smoke_test.go`

**Interface contract:** `singleNodeClusterAppConfig` keeps a 20-millisecond report interval, uses a 30-second lease TTL, and returns a `PluginConfig` with `Enable=false` marked explicit so default normalization cannot re-enable it.

- [ ] **Step 1: Add a failing fixture contract test**

Add `TestSingleNodeClusterAppConfigUsesStableLeaseAndExplicitlyDisablesPlugins` next to the fixture:

```go
func TestSingleNodeClusterAppConfigUsesStableLeaseAndExplicitlyDisablesPlugins(t *testing.T) {
	cfg := singleNodeClusterAppConfig(t)
	if cfg.Cluster.HealthReport.Interval != 20*time.Millisecond {
		t.Fatalf("health interval = %v, want 20ms", cfg.Cluster.HealthReport.Interval)
	}
	if cfg.Cluster.HealthReport.TTL != 30*time.Second {
		t.Fatalf("health TTL = %v, want 30s", cfg.Cluster.HealthReport.TTL)
	}
	app := &App{cfg: cfg}
	if err := app.applyConfigDefaults(); err != nil {
		t.Fatalf("applyConfigDefaults() error = %v", err)
	}
	if app.cfg.Plugin.Enable {
		t.Fatal("plugin Enable = true, want explicit false")
	}
}
```

Run:

```bash
GOWORK=off go test ./internal/app \
  -run '^TestSingleNodeClusterAppConfigUsesStableLeaseAndExplicitlyDisablesPlugins$' -count=1
```

Expected: FAIL because the TTL is 500 milliseconds and default normalization enables plugins.

- [ ] **Step 2: Set the fixture values explicitly**

At the start of `singleNodeClusterAppConfig`, construct:

```go
plugin := PluginConfig{Enable: false}
plugin.SetEnableExplicit(true)
plugin.SetExplicitFlags(true)
```

Assign `Plugin: plugin` in `Config`, retain the 20-millisecond health interval, and set `HealthReport.TTL` to `30 * time.Second`. Do not change production defaults.

- [ ] **Step 3: Verify the fixture and affected smoke surfaces**

```bash
GOWORK=off go test ./internal/app \
  -run '^(TestSingleNodeClusterAppConfigUsesStableLeaseAndExplicitlyDisablesPlugins|TestSingleNodeClusterSend.*|TestConversationListAPI.*)$' \
  -count=1
```

Expected: PASS with the real single-node cluster runtime and no plugin socket/process startup.

- [ ] **Step 4: Commit the fixture change**

```bash
git add internal/app/sendack_smoke_test.go
git commit -m "test(app): stabilize single node cluster fixture"
```

### Task 3: Remove scheduler-based pagination ordering

**Files:**
- Modify: `internal/app/conversation_api_smoke_test.go`

- [ ] **Step 1: Remove only the arbitrary delay**

Delete:

```go
time.Sleep(2 * time.Millisecond)
```

Keep both `/message/send` calls, the explicit `ActiveAt=1000` and `ActiveAt=2000` writes, cursor construction, and all page assertions unchanged. This is a test cleanup backed by the existing semantic assertions, so it does not require a manufactured red test.

- [ ] **Step 2: Repeat the exact flaky test in one process**

```bash
GOWORK=off go test ./internal/app \
  -run '^TestConversationListAPIPaginatesWithNextCursor$' -count=100 -timeout=10m
```

Expected: PASS 100/100 without a timing sleep.

- [ ] **Step 3: Repeat under bounded package-process contention**

```bash
seq 1 4 | xargs -P4 -I{} sh -c \
  'GOWORK=off go test ./internal/app -run "^TestConversationListAPIPaginatesWithNextCursor$" -count=10 -timeout=3m'
```

Expected: four zero exit codes and no `/message/send` HTTP 503 with `channel: not ready`.

- [ ] **Step 4: Commit the sleep removal**

```bash
git add internal/app/conversation_api_smoke_test.go
git commit -m "test(app): remove pagination timing sleep"
```

### Task 4: Run issue-level and repository verification

- [ ] **Step 1: Run the focused race detector**

```bash
GOWORK=off go test -race ./pkg/cluster ./internal/app \
  -run 'TestChannelDataPlaneLeaseGuard|TestSingleNodeClusterAppConfig|TestConversationListAPIPaginatesWithNextCursor' \
  -count=1
```

- [ ] **Step 2: Run complete affected packages**

```bash
GOWORK=off go test ./pkg/cluster ./internal/app -count=1
```

- [ ] **Step 3: Run the exact repository unit gate**

```bash
GOWORK=off go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/... -count=1
```

- [ ] **Step 4: Check the final patch**

```bash
git diff --check
git status --short
```

Expected: no whitespace errors; only intentionally uncommitted integration artifacts, if any, may remain.
