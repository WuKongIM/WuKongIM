# InternalV2 Dynamic Node Stage 10B Readiness Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the validated Stage 9D and Stage 10A dynamic-node checks into a repeatable local readiness gate script with deterministic profiles and failure evidence.

**Architecture:** Add a small Bash orchestrator under `scripts/e2ev2/` that wraps existing Go tests and `scripts/build-gofail-binary.sh` instead of duplicating test logic. The script writes per-command logs, a summary, and environment metadata to one evidence directory, supports `quick` and `full` profiles, and keeps all e2ev2 execution serial with `-p=1`.

**Tech Stack:** Bash, Go script tests, `go test -tags=e2e`, `scripts/build-gofail-binary.sh`, etcd-io/gofail binary, existing e2ev2 harness.

---

## Scope

Stage 10B is a gate automation stage, not a new failure-injection stage.

In scope:

- Add `scripts/e2ev2/dynamic-node-readiness-gate.sh`.
- Add dry-run and failure evidence tests under `scripts/`.
- Update the dynamic-node lifecycle master plan to link Stage 10A and Stage 10B.
- Document the exact command developers should run before merging dynamic-node changes.

Out of scope:

- New production failpoints.
- New e2ev2 scenarios.
- Random chaos, soak matrices, or unbounded retry loops.
- CI provider wiring. This plan only creates a local/portable gate script.

## File Structure

- Create: `scripts/e2ev2/dynamic-node-readiness-gate.sh`
  - Owns argument parsing, profile selection, evidence directory layout, command logging, and failure tails.
  - Delegates gofail binary creation to `scripts/build-gofail-binary.sh`.
  - Delegates actual correctness proof to existing Go/e2e tests.

- Create: `scripts/dynamic_node_readiness_gate_script_test.go`
  - Verifies `--dry-run` command expansion for `quick` and `full`.
  - Verifies a failing command writes a command log, summary, and failure tail marker.
  - Uses fake `go` and fake build script paths through explicit environment overrides so tests stay fast.

- Modify: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`
  - Add Stage 10A and Stage 10B to the source plan list.
  - Add Stage 10A/10B rows to the execution order table.
  - Add a Stage 10 completion gate that points at the new script.

## Script Contract

`scripts/e2ev2/dynamic-node-readiness-gate.sh` must expose:

```bash
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile quick
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile full
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile full --out-dir data/dynamic-node-readiness-gate/manual
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile quick --reuse-binary --binary /tmp/wukongimv2-gofail
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile full --dry-run
```

Profiles:

- `quick`
  - `GOWORK=off go test ./pkg/controllerv2 -count=1`
  - `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 2m -p=1`
  - gofail binary build unless `--reuse-binary` is set
  - Stage 10A opt-in gofail aggregate:
    `WK_E2EV2_BINARY=<binary> WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run 'TestStage10A|TestGofailDynamicNodeBinaryExposesFailpoints' -count=1 -timeout 15m -p=1`
  - `git diff --check`

- `full`
  - Everything in `quick`
  - Stage 9D real-traffic smoke:
    `GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -run TestDynamicNodeLifecycleWithContinuousTraffic -count=1 -timeout 12m -p=1`

Default paths:

- `OUT_DIR=data/dynamic-node-readiness-gate/<YYYYMMDD-HHMMSS>`
- `BINARY=$OUT_DIR/wukongimv2-gofail`
- `SUMMARY=$OUT_DIR/summary.md`
- `COMMAND_LOG=$OUT_DIR/commands.log`
- Per-command logs:
  - `controllerv2.log`
  - `dynamic-node-faults-default.log`
  - `build-gofail.log`
  - `stage10a-gofail.log`
  - `stage9d-real-traffic.log`
  - `diff-check.log`

Environment overrides for script tests:

- `WK_DYNAMIC_NODE_GATE_GO_BIN`: defaults to `go`.
- `WK_DYNAMIC_NODE_GATE_BUILD_GOFAIL_SCRIPT`: defaults to `scripts/build-gofail-binary.sh`.
- `WK_DYNAMIC_NODE_GATE_TIMESTAMP`: optional deterministic timestamp for tests.

## Task 1: Add Gate Script Dry-Run Tests

**Files:**

- Create: `scripts/dynamic_node_readiness_gate_script_test.go`
- Create later in Task 2: `scripts/e2ev2/dynamic-node-readiness-gate.sh`

- [ ] **Step 1: Write the dry-run tests first**

Add a new Go test file with these tests:

```go
package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDynamicNodeReadinessGateDryRunQuickProfile(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	binary := filepath.Join(outDir, "wukongimv2-gofail")

	cmd := exec.Command("bash", "scripts/e2ev2/dynamic-node-readiness-gate.sh",
		"--dry-run",
		"--profile", "quick",
		"--out-dir", outDir,
		"--binary", binary,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"profile=quick",
		"out_dir=" + outDir,
		"gofail_binary=" + binary,
		"summary=" + filepath.Join(outDir, "summary.md"),
		"command_log=" + filepath.Join(outDir, "commands.log"),
		"controllerv2_cmd=GOWORK=off go test ./pkg/controllerv2 -count=1",
		"faults_default_cmd=GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 2m -p=1",
		"build_gofail_cmd=scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/controllerv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out " + binary,
		"stage10a_cmd=WK_E2EV2_BINARY=" + binary + " WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run 'TestStage10A|TestGofailDynamicNodeBinaryExposesFailpoints' -count=1 -timeout 15m -p=1",
		"diff_check_cmd=git diff --check",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "stage9d_cmd=") {
		t.Fatalf("quick profile should not include Stage9D command:\n%s", text)
	}
}

func TestDynamicNodeReadinessGateDryRunFullProfileIncludesStage9D(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	binary := filepath.Join(outDir, "wukongimv2-gofail")

	cmd := exec.Command("bash", "scripts/e2ev2/dynamic-node-readiness-gate.sh",
		"--dry-run",
		"--profile", "full",
		"--out-dir", outDir,
		"--binary", binary,
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, output)
	}
	text := string(output)
	for _, want := range []string{
		"profile=full",
		"stage9d_cmd=GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -run TestDynamicNodeLifecycleWithContinuousTraffic -count=1 -timeout 12m -p=1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, text)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
GOWORK=off go test ./scripts -run 'TestDynamicNodeReadinessGateDryRun' -count=1
```

Expected: fail because `scripts/e2ev2/dynamic-node-readiness-gate.sh` does not exist yet.

## Task 2: Implement The Readiness Gate Script

**Files:**

- Create: `scripts/e2ev2/dynamic-node-readiness-gate.sh`

- [ ] **Step 1: Add the script**

Implement a Bash script with:

- `set -euo pipefail`
- `usage`
- `log`
- `die`
- `print_plan`
- `run_step`
- profile validation for `quick` and `full`
- `--dry-run`, `--reuse-binary`, `--out-dir`, `--binary`
- evidence files under `OUT_DIR`

The command constants must match Task 1 exactly:

```bash
CONTROLLERV2_CMD=(env GOWORK=off "$GO_BIN" test ./pkg/controllerv2 -count=1)
FAULTS_DEFAULT_CMD=(env GOWORK=off "$GO_BIN" test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 2m -p=1)
STAGE10A_CMD=(env WK_E2EV2_BINARY="$BINARY" WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off "$GO_BIN" test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -run 'TestStage10A|TestGofailDynamicNodeBinaryExposesFailpoints' -count=1 -timeout 15m -p=1)
STAGE9D_CMD=(env GOWORK=off "$GO_BIN" test -tags=e2e ./test/e2ev2/cluster/dynamic_node_readiness -run TestDynamicNodeLifecycleWithContinuousTraffic -count=1 -timeout 12m -p=1)
DIFF_CHECK_CMD=(git diff --check)
```

The gofail build command must be:

```bash
BUILD_GOFAIL_CMD=("$BUILD_GOFAIL_SCRIPT" --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/controllerv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out "$BINARY")
```

`run_step` must append the command to `commands.log`, tee output to the step log, and write `PASS` or `FAIL` to `summary.md`.

- [ ] **Step 2: Run dry-run tests to verify they pass**

Run:

```bash
GOWORK=off go test ./scripts -run 'TestDynamicNodeReadinessGateDryRun' -count=1
```

Expected: pass.

- [ ] **Step 3: Run manual dry-runs**

Run:

```bash
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile quick --dry-run
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile full --dry-run
```

Expected: quick omits `stage9d_cmd`; full includes it.

## Task 3: Add Failure Evidence Test

**Files:**

- Modify: `scripts/dynamic_node_readiness_gate_script_test.go`

- [ ] **Step 1: Add a fake failing go test path**

Append this test:

```go
func TestDynamicNodeReadinessGateWritesEvidenceWhenStepFails(t *testing.T) {
	root := repoRoot(t)
	outDir := t.TempDir()
	binDir := t.TempDir()
	fakeGo := filepath.Join(binDir, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/usr/bin/env bash\nprintf 'fake go failure for %s\\n' \"$*\" >&2\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "scripts/e2ev2/dynamic-node-readiness-gate.sh",
		"--profile", "quick",
		"--out-dir", outDir,
		"--binary", filepath.Join(outDir, "wukongimv2-gofail"),
	)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"WK_DYNAMIC_NODE_GATE_GO_BIN="+fakeGo,
		"WK_DYNAMIC_NODE_GATE_BUILD_GOFAIL_SCRIPT=/bin/true",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("gate should fail when go test fails:\n%s", output)
	}
	text := string(output)
	for _, want := range []string{
		"step controllerv2 failed",
		"--- step log:",
		"fake go failure for test ./pkg/controllerv2 -count=1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("failure output missing %q:\n%s", want, text)
		}
	}
	summary := readFile(t, filepath.Join(outDir, "summary.md"))
	if !strings.Contains(summary, "- controllerv2: FAIL") {
		t.Fatalf("summary missing failure marker:\n%s", summary)
	}
	commands := readFile(t, filepath.Join(outDir, "commands.log"))
	if !strings.Contains(commands, "controllerv2") {
		t.Fatalf("commands log missing step name:\n%s", commands)
	}
}
```

- [ ] **Step 2: Run the failure evidence test**

Run:

```bash
GOWORK=off go test ./scripts -run TestDynamicNodeReadinessGateWritesEvidenceWhenStepFails -count=1
```

Expected: pass.

## Task 4: Update Dynamic-Node Plan Links

**Files:**

- Modify: `docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md`

- [ ] **Step 1: Add Stage 10A and Stage 10B to source plans**

Add:

```markdown
- Stage 10A plan: `docs/superpowers/plans/2026-06-30-internalv2-dynamic-node-stage10a-gofail-readiness-recovery.md`
- Stage 10B plan: `docs/superpowers/plans/2026-06-30-internalv2-dynamic-node-stage10b-readiness-gate.md`
```

- [ ] **Step 2: Add execution-order rows**

Add after Stage 9:

```markdown
| 10A | Gofail Readiness Recovery | `2026-06-30-internalv2-dynamic-node-stage10a-gofail-readiness-recovery.md` | Reproducible recovery proofs for health stale, controller watch drop, and bounded scale-in retry |
| 10B | Readiness Gate Automation | `2026-06-30-internalv2-dynamic-node-stage10b-readiness-gate.md` | One local command for Stage10A plus Stage9D release-gate verification |
```

- [ ] **Step 3: Add Stage 10B completion gate**

Add this new section:

````markdown
## Stage 10B Completion

- [ ] **Stage 10B readiness gate script merged locally**

Run on merged local `main`:

```bash
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile quick
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile full
git diff --check
```

Expected: the gate script tests pass, quick profile proves Stage10A gofail recovery, full profile additionally proves Stage9D continuous-traffic lifecycle, and the worktree remains clean.
````

## Task 5: Run Final Gate And Commit

**Files:**

- All files touched by Tasks 1-4.

- [ ] **Step 1: Run script tests**

Run:

```bash
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
```

Expected: pass.

- [ ] **Step 2: Run quick profile**

Run:

```bash
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile quick
```

Expected: pass; summary lists `controllerv2`, `dynamic-node-faults-default`, `build-gofail`, `stage10a-gofail`, and `diff-check` as `PASS`.

- [ ] **Step 3: Run full profile**

Run:

```bash
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile full
```

Expected: pass; summary additionally lists `stage9d-real-traffic` as `PASS`.

- [ ] **Step 4: Check formatting and diff hygiene**

Run:

```bash
git diff --check
git status --short
```

Expected: no whitespace errors; only Stage10B files are modified before commit.

- [ ] **Step 5: Commit**

Run:

```bash
git add scripts/e2ev2/dynamic-node-readiness-gate.sh scripts/dynamic_node_readiness_gate_script_test.go docs/superpowers/plans/2026-06-24-internalv2-dynamic-node-lifecycle.md docs/superpowers/plans/2026-06-30-internalv2-dynamic-node-stage10b-readiness-gate.md
git commit -m "test: add dynamic node readiness gate"
```

## Completion Gate

Stage 10B is complete only after this passes on the development branch:

```bash
GOWORK=off go test ./scripts -run DynamicNodeReadinessGate -count=1
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile quick
scripts/e2ev2/dynamic-node-readiness-gate.sh --profile full
git diff --check
```

After branch verification, fast-forward into local `main`, rerun the same focused gate on merged `main`, then delete the feature branch.

## Self-Review Notes

- Spec coverage: quick/full profiles cover Stage10A and Stage9D gate automation; no new production behavior is required.
- Placeholder scan: no placeholder markers or unspecified test steps are left in this plan.
- Type/command consistency: profile names, environment variables, command labels, and log file names are consistent across tasks.
