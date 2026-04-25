# MultiISR Pressure Testing Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first `pkg/multiisr` pressure-testing suite with lightweight benchmarks, opt-in stress tests, shared harness helpers, and operator docs for mixed scheduling, peer-queue recovery, and snapshot interference.

**Architecture:** Keep all changes inside test and docs files around `pkg/multiisr` so runtime semantics stay unchanged. Build a focused pressure harness in `pressure_testutil_test.go` for environment parsing, runtime setup, fake peer sessions, queue metrics, and mixed-scenario driving, then layer targeted benchmarks in `benchmark_test.go` and gated long-running stability tests in `stress_test.go` on top. Document commands and knobs in `docs/multiisr-stress.md`.

**Tech Stack:** Go 1.23, Go testing/benchmark framework, existing `pkg/multiisr` and `pkg/isr` test helpers, repository docs

---

## File Structure

### Test files

- Create: `pkg/multiisr/pressure_testutil_test.go`
  Responsibility: shared pressure config parsing, default scale constants, fake peer-session metrics, runtime fixture creation, mixed-driver helpers, and reusable assertions.
- Create: `pkg/multiisr/benchmark_test.go`
  Responsibility: repeatable `BenchmarkRuntime...` coverage for replication scheduling, peer queue drain, and mixed replication/snapshot interference.
- Create: `pkg/multiisr/stress_test.go`
  Responsibility: opt-in `TestRuntimeStress...` coverage for mixed scheduling, peer queue recovery, and snapshot interference.

### Documentation files

- Create: `docs/multiisr-stress.md`
  Responsibility: benchmark/stress commands, environment variables, default scale, heavy-scale examples, and how to interpret queue/scheduler results.

### Reference files

- Reference: `docs/superpowers/specs/2026-04-02-multiisr-pressure-design.md`
  Responsibility: approved scope, scenario matrix, defaults, and success criteria.
- Reference: `pkg/multiisr/session_test.go`
  Responsibility: existing fake session / runtime fixture patterns already used by `pkg/multiisr` tests.
- Reference: `pkg/isr/benchmark_test.go`
  Responsibility: benchmark naming, helper structure, and `b.Run(...)` style already used in the repository.

## Implementation Notes

- Follow `@superpowers:test-driven-development`: write the failing benchmark shell or failing stress test first, run it red, implement the minimum helper/harness, then run it green.
- Keep default `go test ./...` lightweight. Long-running stress tests must stay behind `MULTIISR_STRESS=1`.
- Do not modify `pkg/multiisr` production semantics for convenience. Pressure harness code belongs in `_test.go` files only.
- Reuse existing same-package test access to unexported runtime helpers instead of exposing new public API.
- Pressure assertions must focus on stable final state and progress metrics, not machine-sensitive timing thresholds.
- Avoid random nondeterminism. Use deterministic seeds and make all scale knobs overrideable by environment variables.

## Task 1: Add shared pressure config and harness helpers

**Files:**
- Create: `pkg/multiisr/pressure_testutil_test.go`
- Test: `pkg/multiisr/pressure_testutil_test.go`

- [ ] **Step 1: Write the failing helper tests**

```go
func TestPressureConfigDefaultsAndOverrides(t *testing.T) {
	t.Setenv("MULTIISR_STRESS", "")
	cfg, err := loadPressureConfig()
	if err != nil {
		t.Fatalf("loadPressureConfig() error = %v", err)
	}
	if cfg.groups != 256 || cfg.peers != 8 {
		t.Fatalf("defaults = %+v, want groups=256 peers=8", cfg)
	}

	t.Setenv("MULTIISR_STRESS_GROUPS", "512")
	t.Setenv("MULTIISR_STRESS_PEERS", "12")
	cfg, err = loadPressureConfig()
	if err != nil {
		t.Fatalf("loadPressureConfig() error = %v", err)
	}
	if cfg.groups != 512 || cfg.peers != 12 {
		t.Fatalf("overrides = %+v, want groups=512 peers=12", cfg)
	}
}

func TestPressureConfigRejectsInvalidDuration(t *testing.T) {
	t.Setenv("MULTIISR_STRESS", "1")
	t.Setenv("MULTIISR_STRESS_DURATION", "bad")

	if _, err := loadPressureConfig(); err == nil {
		t.Fatal("loadPressureConfig() error = nil, want invalid duration error")
	}
}
```

- [ ] **Step 2: Run the helper tests and confirm they fail**

Run: `go test ./pkg/multiisr -run 'TestPressureConfig(DefaultsAndOverrides|RejectsInvalidDuration)' -count=1`

Expected: FAIL because the pressure config helpers do not exist yet.

- [ ] **Step 3: Implement the minimal helper/config layer**

```go
type pressureConfig struct {
	stressEnabled        bool
	duration             time.Duration
	groups               int
	peers                int
	seed                 int64
	snapshotInterval     int
	backpressureInterval int
}
```

Implementation details:
- define defaults for `groups=256`, `peers=8`, `duration=10s`
- parse:
  - `MULTIISR_STRESS`
  - `MULTIISR_STRESS_DURATION`
  - `MULTIISR_STRESS_GROUPS`
  - `MULTIISR_STRESS_PEERS`
  - `MULTIISR_STRESS_SEED`
  - `MULTIISR_STRESS_SNAPSHOT_INTERVAL`
  - `MULTIISR_STRESS_BACKPRESSURE_INTERVAL`
- add a `pressureHarness` with:
  - runtime fixture creation
  - deterministic group and peer setup
  - fake peer-session metrics
  - queue high-watermark tracking
  - group progress counters
- add helpers such as:
  - `newPressureHarness(t, cfg pressureConfig) *pressureHarness`
  - `runSchedulingTicks(rounds int)`
  - `assertAllGroupsMadeProgress(t *testing.T)`

- [ ] **Step 4: Re-run the helper tests and confirm they pass**

Run: `go test ./pkg/multiisr -run 'TestPressureConfig(DefaultsAndOverrides|RejectsInvalidDuration)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/multiisr/pressure_testutil_test.go
git commit -m "test: add multiisr pressure helpers"
```

## Task 2: Add repeatable multiisr benchmarks

**Files:**
- Create: `pkg/multiisr/benchmark_test.go`
- Modify: `pkg/multiisr/pressure_testutil_test.go`
- Test: `pkg/multiisr/benchmark_test.go`

- [ ] **Step 1: Write the failing benchmark shells**

```go
func BenchmarkRuntimeReplicationScheduling(b *testing.B) {
	b.Run("groups=256/peers=8", func(b *testing.B) {
		b.Fatal("not implemented")
	})
}

func BenchmarkRuntimePeerQueueDrain(b *testing.B) {
	b.Run("groups=256/peers=8", func(b *testing.B) {
		b.Fatal("not implemented")
	})
}

func BenchmarkRuntimeMixedReplicationAndSnapshot(b *testing.B) {
	b.Run("groups=256/peers=8", func(b *testing.B) {
		b.Fatal("not implemented")
	})
}
```

- [ ] **Step 2: Run the targeted benchmarks and confirm they fail**

Run: `go test ./pkg/multiisr -run '^$' -bench 'BenchmarkRuntime(ReplicationScheduling|PeerQueueDrain|MixedReplicationAndSnapshot)$' -benchmem -count=1`

Expected: FAIL because the benchmark shells intentionally call `b.Fatal`.

- [ ] **Step 3: Implement the benchmark suite**

Implementation details:
- add `BenchmarkRuntimeReplicationScheduling`
  - preload `groups=256`, `peers=8`
  - in each iteration enqueue replication over a deterministic group/peer pattern
  - run scheduler ticks outside nonessential setup
- add `BenchmarkRuntimePeerQueueDrain`
  - force `MaxFetchInflightPeer=1`
  - build queued requests, then feed matching fetch responses so drain path is measured
- add `BenchmarkRuntimeMixedReplicationAndSnapshot`
  - replication is the main loop
  - every N operations enqueue snapshot work
  - verify queue high-watermarks do not explode during the run
- keep harness resets outside timed regions with `b.StopTimer()` / `b.StartTimer()`
- end each sub-benchmark with light correctness checks:
  - at least one group progressed
  - peer queue not stuck non-empty

- [ ] **Step 4: Re-run the targeted benchmarks and confirm they pass**

Run: `go test ./pkg/multiisr -run '^$' -bench 'BenchmarkRuntime(ReplicationScheduling|PeerQueueDrain|MixedReplicationAndSnapshot)$' -benchmem -count=1`

Expected: PASS with benchmark output for all sub-benchmarks and no correctness failures.

- [ ] **Step 5: Commit**

```bash
git add pkg/multiisr/pressure_testutil_test.go pkg/multiisr/benchmark_test.go
git commit -m "test: add multiisr pressure benchmarks"
```

## Task 3: Add gated mixed scheduling and peer queue recovery stress tests

**Files:**
- Create: `pkg/multiisr/stress_test.go`
- Modify: `pkg/multiisr/pressure_testutil_test.go`
- Test: `pkg/multiisr/stress_test.go`

- [ ] **Step 1: Write the failing gated stress tests**

```go
func TestRuntimeStressMixedScheduling(t *testing.T) {
	cfg, err := loadPressureConfig()
	if err != nil {
		t.Fatalf("loadPressureConfig() error = %v", err)
	}
	if !cfg.stressEnabled {
		t.Skip("set MULTIISR_STRESS=1 to enable")
	}
	t.Fatal("not implemented")
}

func TestRuntimeStressPeerQueueRecovery(t *testing.T) {
	cfg, err := loadPressureConfig()
	if err != nil {
		t.Fatalf("loadPressureConfig() error = %v", err)
	}
	if !cfg.stressEnabled {
		t.Skip("set MULTIISR_STRESS=1 to enable")
	}
	t.Fatal("not implemented")
}
```

- [ ] **Step 2: Run the targeted stress tests and confirm they fail**

Run: `MULTIISR_STRESS=1 MULTIISR_STRESS_DURATION=3s go test ./pkg/multiisr -run 'TestRuntimeStress(MixedScheduling|PeerQueueRecovery)' -count=1 -v`

Expected: FAIL because the new stress tests intentionally call `t.Fatal`.

- [ ] **Step 3: Implement the minimal stress framework**

Implementation details:
- implement a shared driver loop that runs until the configured duration elapses
- mixed scheduling test must:
  - continuously enqueue replication across all groups
  - periodically toggle soft/hard backpressure on selected peers
  - periodically inject snapshot tasks at a lower frequency
- peer queue recovery test must:
  - focus on a few hot peers
  - alternate between hard backpressure and recovery windows
  - verify queued requests drain when pressure is lifted
- maintain pressure metrics:
  - per-group progress count
  - per-peer queue high-watermark
  - final queued count
  - scheduler rounds completed

- [ ] **Step 4: Re-run the targeted stress tests and confirm they pass**

Run: `MULTIISR_STRESS=1 MULTIISR_STRESS_DURATION=3s go test ./pkg/multiisr -run 'TestRuntimeStress(MixedScheduling|PeerQueueRecovery)' -count=1 -v`

Expected: PASS with:
- every group making progress
- no permanently growing peer queue
- queued requests draining after hard backpressure is lifted

- [ ] **Step 5: Commit**

```bash
git add pkg/multiisr/pressure_testutil_test.go pkg/multiisr/stress_test.go
git commit -m "test: add multiisr mixed scheduling stress coverage"
```

## Task 4: Add snapshot interference stress coverage and operator docs

**Files:**
- Modify: `pkg/multiisr/stress_test.go`
- Modify: `pkg/multiisr/pressure_testutil_test.go`
- Create: `docs/multiisr-stress.md`
- Test: `pkg/multiisr/stress_test.go`

- [ ] **Step 1: Write the failing snapshot stress test and docs stub**

```go
func TestRuntimeStressSnapshotInterference(t *testing.T) {
	cfg, err := loadPressureConfig()
	if err != nil {
		t.Fatalf("loadPressureConfig() error = %v", err)
	}
	if !cfg.stressEnabled {
		t.Skip("set MULTIISR_STRESS=1 to enable")
	}
	t.Fatal("not implemented")
}
```

Create `docs/multiisr-stress.md` with initial sections for:
- benchmark commands
- stress commands
- environment variables
- how to read queue/progress/snapshot results

- [ ] **Step 2: Run the targeted snapshot stress test and confirm it fails**

Run: `MULTIISR_STRESS=1 MULTIISR_STRESS_DURATION=3s go test ./pkg/multiisr -run 'TestRuntimeStressSnapshotInterference' -count=1 -v`

Expected: FAIL because the snapshot stress test intentionally calls `t.Fatal`.

- [ ] **Step 3: Implement snapshot interference validation and finish docs**

Implementation details:
- add a low-frequency snapshot injector to the pressure driver
- track:
  - snapshot waiting high-watermark
  - final waiting queue depth
  - group progress during snapshot interference
- finish `docs/multiisr-stress.md` with:
  - exact benchmark commands
  - exact stress commands
  - default scale (`groups=256, peers=8`)
  - heavy-scale example overrides
  - guidance on interpreting:
    - group progress
    - peer queue high-watermarks
    - snapshot waiting queue

- [ ] **Step 4: Re-run the targeted snapshot stress test and confirm it passes**

Run: `MULTIISR_STRESS=1 MULTIISR_STRESS_DURATION=3s go test ./pkg/multiisr -run 'TestRuntimeStressSnapshotInterference' -count=1 -v`

Expected: PASS with:
- snapshot waiting queue draining by test end
- no group starvation
- no permanent peer queue buildup caused by snapshot interference

- [ ] **Step 5: Commit**

```bash
git add pkg/multiisr/pressure_testutil_test.go pkg/multiisr/stress_test.go docs/multiisr-stress.md
git commit -m "docs: add multiisr pressure test guide"
```

## Final Verification

Before marking the work complete, follow `@superpowers:verification-before-completion`:

- Run: `go test ./pkg/multiisr -count=1`
- Run: `go test ./pkg/multiisr -run '^$' -bench 'BenchmarkRuntime(ReplicationScheduling|PeerQueueDrain|MixedReplicationAndSnapshot)$' -benchmem -count=1`
- Run: `MULTIISR_STRESS=1 MULTIISR_STRESS_DURATION=3s go test ./pkg/multiisr -run 'TestRuntimeStress(MixedScheduling|PeerQueueRecovery|SnapshotInterference)' -count=1 -v`
- Run: `go test ./...`

Expected:
- default package tests remain lightweight and green
- benchmark suite runs without correctness failures
- gated stress suite completes with queue/progress assertions green
- repository-wide tests still pass

## Deferred Follow-Ups

Do not expand first-version scope during execution. Track these separately if needed:

- heavier default stress presets (`groups=512`, `groups=1024`)
- automatic pprof collection for stress failures
- real transport integration stress beyond fake peer sessions
- CI automation or threshold gating for benchmark outputs
