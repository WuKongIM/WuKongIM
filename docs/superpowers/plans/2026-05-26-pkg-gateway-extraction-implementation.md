# Package Gateway Extraction Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the reusable client gateway runtime from `internal/gateway` to `pkg/gateway` without changing gateway behavior or WuKongIM server business boundaries.

**Architecture:** Perform a mechanical package move, rewrite imports from `internal/gateway` to `pkg/gateway`, then add import-boundary tests that keep the new public package independent from all `internal/...` packages. Keep `internal/access/gateway` as the WuKongIM-specific adapter that maps gateway frames to message, presence, online, and other usecases.

**Tech Stack:** Go 1.23, `go test`, `go list -json`, existing gateway packages, existing import-boundary tests, Markdown project flow docs.

---

## Starting Constraints

- Run this in an isolated worktree or clean branch. The current workspace may contain unrelated edits; do not mix this package move with existing dirty files.
- If this plan or its design spec is untracked in the original workspace, commit it first or copy it into the isolated worktree before running the implementation steps.
- Follow `AGENTS.md`: before editing a package, read `FLOW.md` if present. `internal/gateway/FLOW.md` exists and must move with the package.
- Keep the move behavior-preserving. Do not tune performance, change SEND batching, change wire protocol behavior, or add single-node bypass branches.
- Keep `internal/access/gateway` server-specific; do not move it into `pkg/gateway`.
- Preserve every current exported top-level gateway identifier during the first move.
- Do not keep compatibility aliases under `internal/gateway`.
- Do not move local artifacts such as `internal/gateway/.DS_Store`.
- Use targeted tests after each task. Run `go test ./...` only after all targeted checks pass and the workspace is suitable.

## File Structure

### Moved Files

- Move: `internal/gateway/**` -> `pkg/gateway/**`
  - Includes `FLOW.md`, root package files, `binding`, `core`, `protocol`, `session`, `testkit`, `transport`, `types`, and `wkprotoenc`.
  - Excludes local filesystem artifacts such as `.DS_Store`.

### Modified Go Files

- `internal/architecture_imports_test.go`
  - Add a `pkg/gateway` boundary test that rejects any normal or test-only import of `github.com/WuKongIM/WuKongIM/internal/...`.
  - Extend the `go list -json` model with `TestImports` and `XTestImports` for this new test.
- `cmd/wkbench/import_boundary_test.go`
  - Remove obsolete `internal/gateway` from the server-internal forbidden list after the package is moved.
  - Keep forbidding `internal/app`, `internal/access`, `internal/usecase`, and `internal/runtime`.
  - Keep `pkg/gateway` allowed.
- All Go files that currently import `github.com/WuKongIM/WuKongIM/internal/gateway...`
  - Rewrite imports to `github.com/WuKongIM/WuKongIM/pkg/gateway...`.
  - Known direct consumers include `cmd/wukongim`, `internal/app`, `internal/access/gateway`, `test/e2e/suite`, and gateway tests.

### Modified Docs

- `pkg/gateway/FLOW.md`
  - Retitle and reword from `internal/gateway` to `pkg/gateway`.
  - Keep references to `internal/access/gateway` as the server business adapter.
  - Update dependency rules and test commands.
- `AGENTS.md`
  - Move gateway infrastructure from the `internal/` directory description to `pkg/gateway`.
  - Update layering text so `pkg/gateway/*` is the generic gateway infrastructure and `internal/access/gateway` is the frame-to-usecase adapter.
- `internal/FLOW.md`
  - Update gateway infrastructure path references and test commands from `internal/gateway` to `pkg/gateway`.
- `docs/superpowers/specs/2026-05-26-pkg-gateway-extraction-design.md`
  - Leave as the design source; only adjust if implementation discovers a spec inconsistency.

---

### Task 0: Isolate Worktree And Capture Baseline

**Files:**
- Read: `AGENTS.md`
- Read: `internal/gateway/FLOW.md`
- Read: `docs/superpowers/specs/2026-05-26-pkg-gateway-extraction-design.md`

- [ ] **Step 1: Check current worktree state**

Run:

```bash
git status --short
```

Expected: Either a clean isolated worktree or only files related to this gateway extraction. If unrelated dirty files are present, stop and create/use a separate worktree before continuing.

- [ ] **Step 2: Create a dedicated worktree if needed**

Run from the original repository only if the current worktree is dirty with unrelated changes:

```bash
git worktree add ../WuKongIM-pkg-gateway-extraction -b pkg-gateway-extraction
cd ../WuKongIM-pkg-gateway-extraction
```

Expected: New branch/worktree is created. Do not copy unrelated changes into it. If `docs/superpowers/plans/2026-05-26-pkg-gateway-extraction-implementation.md` or `docs/superpowers/specs/2026-05-26-pkg-gateway-extraction-design.md` is untracked in the original workspace, copy only those files into the new worktree before Step 3.

- [ ] **Step 3: Read the package flow and spec**

Run:

```bash
sed -n '1,220p' internal/gateway/FLOW.md
sed -n '1,280p' docs/superpowers/specs/2026-05-26-pkg-gateway-extraction-design.md
```

Expected: Confirm `internal/gateway` is generic infrastructure and `internal/access/gateway` remains the business adapter.

- [ ] **Step 4: Run narrow baseline tests**

Run:

```bash
go test ./internal/gateway/... -count=1
go test ./internal/access/gateway -count=1
go test ./internal -run TestInternalImportBoundaries -count=1
go test ./cmd/wkbench -run 'Test(WkbenchDoesNotImportServerInternals|ForbiddenBenchImportAllowsPrefixLookalikes)' -count=1
```

Expected: PASS before migration. If any fail, record the failure and decide whether it is pre-existing before changing code.

---

### Task 1: Add The Public Gateway Boundary Test First

**Files:**
- Modify: `internal/architecture_imports_test.go`

- [ ] **Step 1: Extend `listedPackage` for test imports**

Modify `internal/architecture_imports_test.go` so `listedPackage` includes test import fields:

```go
type listedPackage struct {
    ImportPath    string
    Imports       []string
    TestImports   []string
    XTestImports  []string
}
```

- [ ] **Step 2: Add a package-list helper that accepts patterns**

Replace `listInternalPackages` with a generic helper and keep the old wrapper:

```go
func listInternalPackages(t *testing.T) []listedPackage {
    t.Helper()
    return listPackages(t, "./internal/...")
}

func listPackages(t *testing.T, patterns ...string) []listedPackage {
    t.Helper()
    _, file, _, ok := runtime.Caller(0)
    if !ok {
        t.Fatal("runtime.Caller failed")
    }
    repoRoot := filepath.Dir(filepath.Dir(file))
    args := append([]string{"list", "-json"}, patterns...)
    cmd := exec.Command("go", args...)
    cmd.Dir = repoRoot
    cmd.Env = append(os.Environ(), "GOWORK=off")
    out, err := cmd.Output()
    if err != nil {
        if exitErr, ok := err.(*exec.ExitError); ok {
            t.Fatalf("go %s failed: %v\n%s", strings.Join(args, " "), err, exitErr.Stderr)
        }
        t.Fatalf("go %s failed: %v", strings.Join(args, " "), err)
    }

    decoder := json.NewDecoder(bytes.NewReader(out))
    var packages []listedPackage
    for decoder.More() {
        var pkg listedPackage
        if err := decoder.Decode(&pkg); err != nil {
            t.Fatalf("decode go list output: %v", err)
        }
        packages = append(packages, pkg)
    }
    return packages
}
```

- [ ] **Step 3: Remove the obsolete internal gateway boundary from the internal test**

In `TestInternalImportBoundaries`, remove `modulePath + "/internal/gateway/"` from the `internal/runtime` forbidden prefixes. After the move, active docs and tests should not describe `internal/gateway` as a live infrastructure package.

- [ ] **Step 4: Add an all-imports helper for the public gateway test**

Add:

```go
func allImports(pkg listedPackage) []string {
    imports := make([]string, 0, len(pkg.Imports)+len(pkg.TestImports)+len(pkg.XTestImports))
    imports = append(imports, pkg.Imports...)
    imports = append(imports, pkg.TestImports...)
    imports = append(imports, pkg.XTestImports...)
    return imports
}
```

- [ ] **Step 5: Add the failing `pkg/gateway` boundary test**

Add:

```go
func TestPkgGatewayDoesNotImportInternalPackages(t *testing.T) {
    packages := listPackages(t, "./pkg/gateway/...")
    var violations []string
    for _, pkg := range packages {
        for _, imported := range allImports(pkg) {
            if matchesImportPrefix(imported, modulePath+"/internal/") {
                violations = append(violations, fmt.Sprintf("%s imports %s", pkg.ImportPath, imported))
            }
        }
    }
    sort.Strings(violations)
    if len(violations) > 0 {
        t.Fatalf("pkg/gateway import boundary violations:\n%s", strings.Join(violations, "\n"))
    }
}
```

- [ ] **Step 6: Run the new test and verify it fails before the move**

Run:

```bash
go test ./internal -run TestPkgGatewayDoesNotImportInternalPackages -count=1
```

Expected: FAIL because `./pkg/gateway/...` does not exist yet. This proves the boundary test will protect the extracted package.

- [ ] **Step 7: Commit the failing-test change only if your workflow allows red commits; otherwise keep it unstaged until Task 2 passes**

Recommended for this repo: do not commit the red test alone. Keep it in the working tree and commit together with Task 2 once green.

---

### Task 2: Move `internal/gateway` To `pkg/gateway` And Rewrite Imports

**Files:**
- Move: `internal/gateway/**` -> `pkg/gateway/**`
- Modify: all `.go` files importing `github.com/WuKongIM/WuKongIM/internal/gateway...`

- [ ] **Step 1: Move the gateway tree without local artifacts**

Run:

```bash
mkdir -p pkg/gateway
find internal/gateway -mindepth 1 -maxdepth 1 ! -name '.DS_Store' -exec mv {} pkg/gateway/ \;
rm -f internal/gateway/.DS_Store
rmdir internal/gateway
```

Expected: `pkg/gateway` contains `FLOW.md`, root package files, and all gateway subdirectories. `internal/gateway` no longer exists.

- [ ] **Step 2: Rewrite Go import paths mechanically**

Run:

```bash
python3 - <<'PY'
from pathlib import Path
old = 'github.com/WuKongIM/WuKongIM/internal/gateway'
new = 'github.com/WuKongIM/WuKongIM/pkg/gateway'
for path in Path('.').rglob('*.go'):
    if '.git' in path.parts:
        continue
    text = path.read_text()
    if old in text:
        path.write_text(text.replace(old, new))
PY
```

Expected: No Go file imports `github.com/WuKongIM/WuKongIM/internal/gateway...`.

- [ ] **Step 3: Format modified Go files**

Run:

```bash
{ git diff --name-only --diff-filter=ACMRT -- '*.go'; git ls-files --others --exclude-standard -- '*.go'; } | while IFS= read -r file; do gofmt -w "$file"; done
```

Expected: `gofmt` completes with no output. The command excludes deleted paths and includes untracked moved files under `pkg/gateway`.

- [ ] **Step 4: Check for stale gateway imports in Go files**

Run:

```bash
rg 'github\.com/WuKongIM/WuKongIM/internal/gateway' --glob '*.go'
```

Expected: No matches.

- [ ] **Step 5: Run gateway package tests under the new path**

Run:

```bash
go test ./pkg/gateway/... -count=1
go test -tags=integration ./pkg/gateway/transport/gnet ./internal/access/gateway -run '^$' -count=1
```

Expected: PASS. The integration command is compile-only and catches build-tagged import rewrites without running listener integration scenarios. Failures should be import-path or mechanical move mistakes only; do not change behavior to fix them.

- [ ] **Step 6: Run the new boundary test and existing internal boundary test**

Run:

```bash
go test ./internal -run 'Test(PkgGatewayDoesNotImportInternalPackages|InternalImportBoundaries)' -count=1
```

Expected: PASS. If `TestPkgGatewayDoesNotImportInternalPackages` reports `pkg/gateway` importing an internal package, rewrite that dependency to the moved `pkg/gateway` path or identify an actual boundary violation.

- [ ] **Step 7: Commit the mechanical move and boundary test**

Run:

```bash
git add internal/architecture_imports_test.go pkg/gateway
git add -u internal/gateway
git add $(git diff --name-only -- '*.go')
git commit -m "refactor: extract gateway package"
```

Expected: Commit contains only the gateway move, import rewrites, and the new boundary test. If there are unrelated files in `git diff --cached --name-only`, unstage them before committing.

---

### Task 3: Update Wkbench Boundary And Direct Consumer Tests

**Files:**
- Modify: `cmd/wkbench/import_boundary_test.go`
- Verify direct consumers: `cmd/wukongim`, `internal/access/gateway`, `internal/app`, `test/e2e/suite`

- [ ] **Step 1: Remove obsolete gateway infrastructure from wkbench forbidden imports**

In `cmd/wkbench/import_boundary_test.go`, remove the gateway infrastructure entry from `forbiddenBenchImport`. If Task 2's mechanical rewrite has already changed it, remove the rewritten public path instead. The forbidden list must contain neither of these entries:

```go
"github.com/WuKongIM/WuKongIM/internal/gateway",
"github.com/WuKongIM/WuKongIM/pkg/gateway",
```

Keep forbidding server internals such as `internal/app`, `internal/access`, `internal/usecase`, and `internal/runtime`.

- [ ] **Step 2: Add a regression assertion that public gateway is allowed**

Extend `TestForbiddenBenchImportAllowsPrefixLookalikes` with:

```go
"github.com/WuKongIM/WuKongIM/pkg/gateway",
"github.com/WuKongIM/WuKongIM/pkg/gateway/testkit",
```

Expected: The test documents that benchmark tooling may use public gateway helpers.

- [ ] **Step 3: Run wkbench boundary test**

Run:

```bash
go test ./cmd/wkbench -run 'Test(WkbenchDoesNotImportServerInternals|ForbiddenBenchImportAllowsPrefixLookalikes)' -count=1
```

Expected: PASS.

- [ ] **Step 4: Run direct consumer tests**

Run:

```bash
go test ./internal/access/gateway -count=1
go test ./cmd/wukongim -count=1
go test ./test/e2e/suite -count=1
go test ./internal/app -run Gateway -count=1
```

Expected: PASS. These catch import rewrites in the server adapter, config entrypoint, e2e client helpers, and app composition root before committing this task.

- [ ] **Step 5: Commit boundary and direct-consumer fixes**

Run:

```bash
git add cmd/wkbench/import_boundary_test.go
git add $(git diff --name-only -- '*.go')
git commit -m "test: update gateway import boundaries"
```

Expected: Commit only includes wkbench boundary updates and any missed Go import rewrites required by the direct-consumer tests.

---

### Task 4: Update Gateway FLOW And Project Documentation

**Files:**
- Modify: `pkg/gateway/FLOW.md`
- Modify: `AGENTS.md`
- Modify: `internal/FLOW.md`
- Optionally modify: `docs/development/PROJECT_KNOWLEDGE.md` only if a concise durable rule is useful

- [ ] **Step 1: Retitle `pkg/gateway/FLOW.md`**

Change the title from:

```markdown
# internal/gateway 流程文档
```

to:

```markdown
# pkg/gateway 流程文档
```

- [ ] **Step 2: Rewrite the FLOW responsibility wording**

In `pkg/gateway/FLOW.md`, update text so it says:

```markdown
`pkg/gateway` 是公开可复用的客户端接入基础设施包。它负责监听器绑定、底层传输适配、协议编解码、会话生命周期、认证握手、帧分发、transport 异步写、空闲关闭，以及网关级观测事件。
```

Keep `internal/access/gateway` as the business adapter in the “不负责” and flow sections.

- [ ] **Step 3: Update dependency rules in FLOW**

Replace the old dependency bullets with:

```markdown
- `pkg/gateway` 可以依赖 Go 标准库、`pkg/protocol/*`、`pkg/observability/sendtrace`、`pkg/wklog` 和外部 transport/runtime 依赖。
- `pkg/gateway` 不得依赖 `internal/*`。
- `internal/access/gateway` 可以依赖 `pkg/gateway`，负责把 `frame.Frame` 转为 message/presence 用例命令。
```

- [ ] **Step 4: Update FLOW test commands**

Replace old gateway test commands with:

```bash
GOWORK=off go test ./pkg/gateway -count=1
GOWORK=off go test ./pkg/gateway/... -count=1
GOWORK=off go test ./internal/access/gateway -count=1
```

- [ ] **Step 5: Update `AGENTS.md` directory structure**

In `AGENTS.md`, remove the `internal/gateway` directory entry from the `internal/` section and add this under `pkg/`:

```text
  gateway/               通用客户端网关基础设施，提供 listener、transport、protocol、session、auth、dispatch、testkit
```

Also update the layering bullet from:

```markdown
- `internal/gateway/*` 放网关通用基础设施，不放面向具体业务的用例编排。
```

to:

```markdown
- `pkg/gateway/*` 放可复用网关通用基础设施，不放面向具体业务的用例编排。
```

- [ ] **Step 6: Update `internal/FLOW.md` references**

Change gateway infrastructure path references and test commands in `internal/FLOW.md` from `internal/gateway` to `pkg/gateway`, while preserving `internal/access/gateway` references for the server adapter.

- [ ] **Step 7: Verify current docs no longer describe `internal/gateway` as active infrastructure**

Run:

```bash
rg 'internal/gateway' AGENTS.md internal/FLOW.md pkg/gateway/FLOW.md
```

Expected: No matches. References in historical specs/reports may remain outside these active docs.

- [ ] **Step 8: Commit docs**

Run:

```bash
git add AGENTS.md internal/FLOW.md pkg/gateway/FLOW.md
git commit -m "docs: document public gateway package"
```

Expected: Commit contains only active documentation updates.

---

### Task 5: Final Import Sweep And Targeted Verification

**Files:**
- Verify: entire repository
- Modify: only files with missed active import/path references

- [ ] **Step 1: Search for stale Go imports**

Run:

```bash
rg 'github\.com/WuKongIM/WuKongIM/internal/gateway' --glob '*.go'
```

Expected: No matches.

- [ ] **Step 2: Search for active stale path references**

Run:

```bash
rg 'internal/gateway' AGENTS.md internal/FLOW.md cmd internal test pkg --glob '*.go' --glob '*.md'
```

Expected: No active references except none. If matches are in historical docs under `docs/superpowers/specs` or `docs/superpowers/reports`, they may remain historical and should not be mass-rewritten.

- [ ] **Step 3: Run targeted test suite from the spec**

Run:

```bash
go test ./pkg/gateway/... -count=1
go test -tags=integration ./pkg/gateway/transport/gnet ./internal/access/gateway -run '^$' -count=1
go test ./internal/access/gateway -count=1
go test ./cmd/wukongim -count=1
go test ./test/e2e/suite -count=1
go test ./internal -run TestInternalImportBoundaries -count=1
go test ./internal -run TestPkgGatewayDoesNotImportInternalPackages -count=1
go test ./cmd/wkbench -run 'Test(WkbenchDoesNotImportServerInternals|ForbiddenBenchImportAllowsPrefixLookalikes)' -count=1
go test ./internal/app -run Gateway -count=1
```

Expected: PASS for all targeted tests.

- [ ] **Step 4: Run integration gateway tests only if listener/protocol behavior was touched beyond imports**

Run full integration scenarios only if anything besides package paths/docs/boundary tests changed gateway behavior. The compile-only integration check already ran in Step 3:

```bash
go test -tags=integration ./internal/access/gateway -count=1
```

Expected: PASS. If this was not run, record why it was not necessary.

- [ ] **Step 5: Run full unit suite when workspace is suitable**

Run:

```bash
go test ./... 
```

Expected: PASS. If the workspace has unrelated failing packages, capture the failure and separate it from this migration.

- [ ] **Step 6: Check final diff is scoped**

Run:

```bash
git status --short
git diff --stat
```

Expected: Only gateway move, import rewrites, boundary tests, and active docs changed.

- [ ] **Step 7: Commit final cleanup if needed**

Run only if Task 5 found and fixed missed references:

```bash
git add <fixed-files>
git commit -m "chore: finish gateway extraction cleanup"
```

Expected: No unrelated files staged.

---

## Verification Checklist

- [ ] `go test ./pkg/gateway/... -count=1` passes.
- [ ] `go test -tags=integration ./pkg/gateway/transport/gnet ./internal/access/gateway -run '^$' -count=1` passes.
- [ ] `go test ./internal/access/gateway -count=1` passes.
- [ ] `go test ./cmd/wukongim -count=1` passes.
- [ ] `go test ./test/e2e/suite -count=1` passes.
- [ ] `go test ./internal -run TestPkgGatewayDoesNotImportInternalPackages -count=1` passes.
- [ ] `go test ./internal -run TestInternalImportBoundaries -count=1` passes.
- [ ] `go test ./cmd/wkbench -run TestWkbenchDoesNotImportServerInternals -count=1` passes.
- [ ] `go test ./internal/app -run Gateway -count=1` passes.
- [ ] `rg 'github\.com/WuKongIM/WuKongIM/internal/gateway' --glob '*.go'` returns no matches.
- [ ] `rg 'internal/gateway' AGENTS.md internal/FLOW.md cmd internal test pkg --glob '*.go' --glob '*.md'` returns no active stale references.
- [ ] `pkg/gateway/...` has zero normal or test-only imports of `github.com/WuKongIM/WuKongIM/internal/...`.
- [ ] `internal/access/gateway` still owns SEND, RECVACK, PING, presence activation/deactivation, sendack mapping, encryption of inbound SEND payloads, sendtrace write events, and online session adaptation.

## Rollback Plan

- If the migration is still uncommitted, restore the isolated worktree by removing it or by reverting only the files touched by this plan. Do not run destructive commands in a dirty shared worktree.
- If committed, rollback with a normal revert of the extraction commits:

```bash
git revert <gateway-extraction-commit-range>
```

- After rollback, run:

```bash
go test ./internal/gateway/... -count=1
go test ./internal/access/gateway -count=1
go test ./internal -run TestInternalImportBoundaries -count=1
```

Expected: Pre-extraction package layout and tests are restored.
