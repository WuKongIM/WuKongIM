# Raftstore Pebble Stress Suite Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a repeatable benchmark and opt-in stress test suite for the Pebble-backed `raftstore` backend without making default `go test` runs expensive.

**Architecture:** Keep all changes inside test and docs files under `raftstore` so production storage behavior remains unchanged. Build a small shared test harness for entry generation, environment-driven sizing, reopen helpers, and end-state verification, then layer benchmarks and explicit long-running stress tests on top of it.

**Tech Stack:** Go 1.23, Go testing/benchmark framework, Pebble-backed `raftstore`, `go.etcd.io/raft/v3/raftpb`, repository docs

---

## File Structure

### Test files

- Create: `raftstore/pebble_test_helpers_test.go`
  Responsibility: shared benchmark/stress helpers, config parsing, test model state, reopen utilities, and end-state verification helpers.
- Create: `raftstore/pebble_benchmark_test.go`
  Responsibility: repeatable `BenchmarkPebble...` coverage for `Save`, `Entries`, `MarkApplied`, and reopen flows.
- Create: `raftstore/pebble_stress_test.go`
  Responsibility: opt-in `TestPebbleStress...` coverage for concurrent writers, mixed read/write/reopen, and snapshot recovery.

### Documentation files

- Create: `docs/raftstore-stress.md`
  Responsibility: explain commands, environment variables, default behavior, and how to interpret benchmark/stress results.

### Reference files

- Reference: `docs/superpowers/specs/2026-03-27-raftstore-pebble-stress-suite-design.md`
  Responsibility: approved scope, test matrix, helper boundaries, and success criteria.
- Reference: `raftstore/pebble_test.go`
  Responsibility: existing correctness semantics and helper patterns for Pebble-backed storage tests.
- Reference: `multiraft/benchmark_test.go`
  Responsibility: benchmark naming and sub-benchmark structure already used in the repository.

## Implementation Notes

- Follow `@superpowers:test-driven-development`: write the failing test or failing benchmark/stress shell first, run it red, implement the minimum code, then run it green.
- Keep default `go test ./...` lightweight. Any long-running pressure coverage must remain behind an explicit environment variable gate.
- Do not modify production `raftstore` code unless a helper gap is impossible to address from tests alone. The primary deliverable is reusable test infrastructure.
- Use `b.ResetTimer()` / `b.StopTimer()` so non-reopen benchmarks measure storage work rather than setup noise.
- Long-running stress tests must quiesce workers before `Close()` / reopen cycles so expected shutdown interruptions are not misreported as storage correctness bugs.
- Reopen-based validation must always use clean close/reopen and then verify `InitialState`, `FirstIndex`, `LastIndex`, sampled `Entries`, and `AppliedIndex`.

### Task 1: Add shared config and verification helpers

**Files:**
- Create: `raftstore/pebble_test_helpers_test.go`
- Test: `raftstore/pebble_test_helpers_test.go`

- [ ] **Step 1: Write the failing helper tests**

```go
func TestPebbleBenchScaleConfigDefaultsAndOverrides(t *testing.T) {
	t.Setenv("WRAFT_RAFTSTORE_BENCH_SCALE", "")
	cfg := loadPebbleBenchConfig(t)
	if cfg.mode != "default" {
		t.Fatalf("mode = %q, want %q", cfg.mode, "default")
	}

	t.Setenv("WRAFT_RAFTSTORE_BENCH_SCALE", "heavy")
	cfg = loadPebbleBenchConfig(t)
	if cfg.mode != "heavy" {
		t.Fatalf("mode = %q, want %q", cfg.mode, "heavy")
	}
}

func TestPebbleStressConfigRejectsInvalidValues(t *testing.T) {
	t.Setenv("WRAFT_RAFTSTORE_STRESS", "1")
	t.Setenv("WRAFT_RAFTSTORE_STRESS_DURATION", "not-a-duration")

	if _, err := loadPebbleStressConfig(); err == nil {
		t.Fatal("loadPebbleStressConfig() error = nil, want invalid duration error")
	}
}
```

- [ ] **Step 2: Run the helper tests and confirm they fail**

Run: `go test ./raftstore -run 'TestPebble(BenchScaleConfigDefaultsAndOverrides|StressConfigRejectsInvalidValues)' -count=1`

Expected: FAIL because the shared config helpers do not exist yet.

- [ ] **Step 3: Implement the minimal helper/config layer**

```go
type pebbleBenchConfig struct {
	mode string
}

type pebbleStressConfig struct {
	enabled  bool
	duration time.Duration
	groups   int
	writers  int
	payload  int
}
```

Implementation details:
- add environment parsing helpers with sane defaults
- add helper constructors for entries and batched persistent state
- add small model structs to track expected per-group terminal state
- add `openBenchDB`, `openStressDB`, and `verifyPebbleGroupState` helpers

- [ ] **Step 4: Re-run the helper tests and confirm they pass**

Run: `go test ./raftstore -run 'TestPebble(BenchScaleConfigDefaultsAndOverrides|StressConfigRejectsInvalidValues)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add raftstore/pebble_test_helpers_test.go
git commit -m "test: add raftstore pebble stress helpers"
```

### Task 2: Add repeatable Pebble benchmarks

**Files:**
- Create: `raftstore/pebble_benchmark_test.go`
- Modify: `raftstore/pebble_test_helpers_test.go`
- Test: `raftstore/pebble_benchmark_test.go`

- [ ] **Step 1: Write the failing benchmark shells**

```go
func BenchmarkPebbleSaveEntries(b *testing.B) {
	b.Run("single-group-append-small", func(b *testing.B) {
		b.Fatal("not implemented")
	})
}

func BenchmarkPebbleEntries(b *testing.B) {
	b.Run("windowed-read", func(b *testing.B) {
		b.Fatal("not implemented")
	})
}

func BenchmarkPebbleMarkApplied(b *testing.B) {
	b.Run("single-group", func(b *testing.B) {
		b.Fatal("not implemented")
	})
}

func BenchmarkPebbleInitialStateAndReopen(b *testing.B) {
	b.Run("single-group", func(b *testing.B) {
		b.Fatal("not implemented")
	})
}
```

- [ ] **Step 2: Run the targeted benchmarks and confirm they fail**

Run: `go test ./raftstore -run '^$' -bench 'BenchmarkPebble(SaveEntries|Entries|MarkApplied|InitialStateAndReopen)$' -count=1`

Expected: FAIL because the benchmark shells intentionally call `b.Fatal`.

- [ ] **Step 3: Implement the benchmark suite**

Implementation details:
- add `BenchmarkPebbleSaveEntries` cases for single-group append, tail replacement, and multi-group interleaved writes
- add `BenchmarkPebbleEntries` cases for small windows, large scans, `maxSize`-capped reads, and reopen reads
- add `BenchmarkPebbleMarkApplied` for single-group and concurrent multi-group updates
- add `BenchmarkPebbleInitialStateAndReopen` for metadata restore, snapshot restore, and multi-group reopen
- keep setup outside timed regions except for explicit reopen benchmarks
- end each sub-benchmark with minimal correctness checks using the shared helpers

- [ ] **Step 4: Re-run the targeted benchmarks and confirm they pass**

Run: `go test ./raftstore -run '^$' -bench 'BenchmarkPebble(SaveEntries|Entries|MarkApplied|InitialStateAndReopen)$' -count=1`

Expected: PASS with benchmark output for each sub-benchmark and no correctness failures.

- [ ] **Step 5: Commit**

```bash
git add raftstore/pebble_test_helpers_test.go raftstore/pebble_benchmark_test.go
git commit -m "test: add raftstore pebble benchmarks"
```

### Task 3: Add opt-in concurrent write and mixed read/write/reopen stress tests

**Files:**
- Create: `raftstore/pebble_stress_test.go`
- Modify: `raftstore/pebble_test_helpers_test.go`
- Test: `raftstore/pebble_stress_test.go`

- [ ] **Step 1: Write the failing opt-in stress tests**

```go
func TestPebbleStressConcurrentWriters(t *testing.T) {
	cfg, err := loadPebbleStressConfig()
	if err != nil {
		t.Fatalf("loadPebbleStressConfig() error = %v", err)
	}
	if !cfg.enabled {
		t.Skip("set WRAFT_RAFTSTORE_STRESS=1 to enable")
	}
	t.Fatal("not implemented")
}

func TestPebbleStressMixedReadWriteReopen(t *testing.T) {
	cfg, err := loadPebbleStressConfig()
	if err != nil {
		t.Fatalf("loadPebbleStressConfig() error = %v", err)
	}
	if !cfg.enabled {
		t.Skip("set WRAFT_RAFTSTORE_STRESS=1 to enable")
	}
	t.Fatal("not implemented")
}
```

- [ ] **Step 2: Run the targeted stress tests and confirm they fail**

Run: `WRAFT_RAFTSTORE_STRESS=1 WRAFT_RAFTSTORE_STRESS_DURATION=3s go test ./raftstore -run 'TestPebbleStress(ConcurrentWriters|MixedReadWriteReopen)' -count=1 -v`

Expected: FAIL because the new stress tests intentionally call `t.Fatal`.

- [ ] **Step 3: Implement the minimal stress framework**

Implementation details:
- implement concurrent writer loops that drive `Save` and `MarkApplied` over multiple groups until the configured duration elapses
- add a shared barrier/quiesce mechanism so mixed read/write tests can cleanly close and reopen the DB across rounds
- add reader loops covering `Entries`, `Term`, `FirstIndex`, `LastIndex`, and `InitialState`
- maintain a lightweight expected-state model and validate terminal state after clean reopen

- [ ] **Step 4: Re-run the targeted stress tests and confirm they pass**

Run: `WRAFT_RAFTSTORE_STRESS=1 WRAFT_RAFTSTORE_STRESS_DURATION=3s go test ./raftstore -run 'TestPebbleStress(ConcurrentWriters|MixedReadWriteReopen)' -count=1 -v`

Expected: PASS with both tests completing without panic, read/write errors, or end-state mismatches.

- [ ] **Step 5: Commit**

```bash
git add raftstore/pebble_test_helpers_test.go raftstore/pebble_stress_test.go
git commit -m "test: add raftstore pebble stress coverage"
```

### Task 4: Add snapshot recovery stress coverage and operator docs

**Files:**
- Modify: `raftstore/pebble_stress_test.go`
- Create: `docs/raftstore-stress.md`
- Test: `raftstore/pebble_stress_test.go`

- [ ] **Step 1: Write the failing snapshot recovery stress test and docs stub**

```go
func TestPebbleStressSnapshotAndRecovery(t *testing.T) {
	cfg, err := loadPebbleStressConfig()
	if err != nil {
		t.Fatalf("loadPebbleStressConfig() error = %v", err)
	}
	if !cfg.enabled {
		t.Skip("set WRAFT_RAFTSTORE_STRESS=1 to enable")
	}
	t.Fatal("not implemented")
}
```

Create `docs/raftstore-stress.md` with initial sections for:
- benchmark commands
- stress commands
- environment variables
- expected runtime and result interpretation

- [ ] **Step 2: Run the snapshot stress test and confirm it fails**

Run: `WRAFT_RAFTSTORE_STRESS=1 WRAFT_RAFTSTORE_STRESS_DURATION=3s go test ./raftstore -run '^TestPebbleStressSnapshotAndRecovery$' -count=1 -v`

Expected: FAIL because the test intentionally calls `t.Fatal`.

- [ ] **Step 3: Implement snapshot stress validation and finish docs**

Implementation details:
- extend the shared model to track snapshot index, term, and conf state
- periodically persist snapshots, then append entries above the snapshot index
- verify `FirstIndex`, `Term(snapshotIndex)`, `LastIndex`, and `InitialState().ConfState` after clean reopen
- complete `docs/raftstore-stress.md` with exact commands, environment variables, and guidance on default versus heavy runs

- [ ] **Step 4: Re-run all targeted verification and confirm it passes**

Run: `go test ./raftstore -count=1`

Run: `go test ./raftstore -run '^$' -bench . -count=1`

Run: `WRAFT_RAFTSTORE_STRESS=1 WRAFT_RAFTSTORE_STRESS_DURATION=3s go test ./raftstore -run '^TestPebbleStress' -count=1 -v`

Expected:
- all regular `raftstore` tests PASS
- all `BenchmarkPebble...` cases run without correctness failures
- all opt-in `TestPebbleStress...` cases PASS for the short verification duration

- [ ] **Step 5: Commit**

```bash
git add raftstore/pebble_stress_test.go docs/raftstore-stress.md
git commit -m "docs: add raftstore stress suite guide"
```

## Review Notes

- Ensure benchmark subtest names remain stable so future result comparison is straightforward.
- Keep helper responsibilities narrow; do not turn the test harness into a second storage implementation.
- If a benchmark or stress test can only be verified through unstable timing assumptions, simplify the assertion to the final durable state instead.
