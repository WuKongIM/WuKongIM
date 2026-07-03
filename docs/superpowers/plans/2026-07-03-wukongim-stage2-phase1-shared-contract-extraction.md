# WuKongIM Stage 2 Phase 1 Shared Contract Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move v2-used shared contracts out of old `internal` server-kernel paths so later `internalv2 -> internal` package promotion can be a mechanical ownership move.

**Architecture:** Treat channel ID helpers and plugin wire contracts as stable shared contracts, not old server runtime. Move channel ID helpers to `pkg/protocol/channelid`, plugin protobuf contracts to `pkg/plugin/pluginproto`, and plugin process hosting to `pkg/plugin/pluginhost` while preserving `internal/bench` as black-box tooling. Add an `internalv2` import-boundary test so v2 server code cannot reintroduce old helper imports.

**Tech Stack:** Go, protobuf generated with `/usr/local/include/protoc/bin/protoc` and `/Users/tt/go/bin/protoc-gen-go`, `rg` import audits, `perl` mechanical import rewrites, focused `GOWORK=off go test`.

---

## File Structure

- Move: `internal/runtime/channelid/` -> `pkg/protocol/channelid/`
  - Owns protocol-facing channel ID derivation helpers for person, command, agent, and request-scoped subscriber channels.
  - Package name remains `channelid`.
- Move: `internal/usecase/plugin/pluginproto/` -> `pkg/plugin/pluginproto/`
  - Owns legacy-compatible plugin protobuf messages and Go helper methods.
  - `plugin.proto` `go_package` must become `github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto;pluginproto`.
- Move: `internal/runtime/plugin/` -> `pkg/plugin/pluginhost/`
  - Owns node-local plugin process runtime, socket server, invoker, scanner, watcher, and desired-state store.
  - Package name becomes `pluginhost`; import aliases should use `pluginhost`.
- Keep: `internal/bench/`
  - Preserve as black-box wkbench tooling. Do not move it in this phase.
- Create: `internalv2/import_boundary_test.go`
  - Scans all `internalv2` Go files and rejects old helper imports.
- Modify docs:
  - `AGENTS.md`
  - `internal/FLOW.md`
  - `internalv2/FLOW.md`
  - `pkg/slot/FLOW.md`
  - `docs/development/PROJECT_KNOWLEDGE.md`
  - `docs/superpowers/specs/2026-07-03-wukongim-stage2-package-promotion-design.md` only if implementation reveals another Stage 2 design clarification.

## Scope Guards

- Do not rename `internalv2`, `pkg/clusterv2`, `pkg/controllerv2`, or `pkg/channelv2` in this phase.
- Do not move `internal/bench`; it remains canonical black-box tooling until the final `internalv2 -> internal` merge plan.
- Do not change plugin protobuf field numbers, JSON tags, marshaling behavior, socket protocol, or plugin hook ordering.
- Do not change foreground SEND, append, delivery, presence, or manager query behavior.
- Do not update historical design docs under `docs/superpowers` except the active Stage 2 design if a clarification is required.

### Task 0: Baseline And Audit Snapshot

**Files:**
- Read-only.

- [ ] **Step 1: Confirm branch and clean state**

Run:

```sh
git status --short --branch
```

Expected: branch is `codex/wukongim-stage2-package-promotion-plan` and there are no unrelated modified files. If unrelated files exist, stop and inspect before continuing.

- [ ] **Step 2: Capture old helper import inventory**

Run:

```sh
rg -n 'github.com/WuKongIM/WuKongIM/internal/(runtime/channelid|runtime/plugin|usecase/plugin/pluginproto)' --glob '*.go' --glob '*.proto'
```

Expected: matches in old `internal`, `internalv2`, and plugin testdata. Keep this output as the before snapshot.

- [ ] **Step 3: Run the focused pre-change baseline**

Run:

```sh
GOWORK=off go test ./internal/runtime/channelid ./internal/usecase/plugin/pluginproto ./internal/runtime/plugin -count=1
```

Expected: all three packages pass. If this fails before any move, stop and record the existing failure.

- [ ] **Step 4: Run the v2 packages that consume these helpers**

Run:

```sh
GOWORK=off go test ./internalv2/usecase/message ./internalv2/usecase/cmdsync ./internalv2/usecase/plugin ./internalv2/runtime/channelappend ./internalv2/access/plugin ./internalv2/app -count=1
```

Expected: packages pass before the move. If an existing test is flaky or failing, capture the exact failure and do not mix a behavioral fix into this extraction.

### Task 1: Move Channel ID Helpers To `pkg/protocol/channelid`

**Files:**
- Move: `internal/runtime/channelid/agent.go` -> `pkg/protocol/channelid/agent.go`
- Move: `internal/runtime/channelid/command.go` -> `pkg/protocol/channelid/command.go`
- Move: `internal/runtime/channelid/person.go` -> `pkg/protocol/channelid/person.go`
- Move: `internal/runtime/channelid/request_subscribers.go` -> `pkg/protocol/channelid/request_subscribers.go`
- Move matching tests from `internal/runtime/channelid/*_test.go` to `pkg/protocol/channelid/`.
- Modify imports in every Go file returned by:

```sh
rg -l 'github.com/WuKongIM/WuKongIM/internal/runtime/channelid' --glob '*.go'
```

- Modify docs: `AGENTS.md`, `internal/FLOW.md`, `internalv2/FLOW.md`, `docs/development/PROJECT_KNOWLEDGE.md`.

- [ ] **Step 1: Move the package**

Run:

```sh
mkdir -p pkg/protocol
git mv internal/runtime/channelid pkg/protocol/channelid
```

Expected: `git status --short` shows deletes under `internal/runtime/channelid` and adds under `pkg/protocol/channelid`.

- [ ] **Step 2: Rewrite Go imports mechanically**

Run:

```sh
rg -l -0 'github.com/WuKongIM/WuKongIM/internal/runtime/channelid' --glob '*.go' \
  | xargs -0 perl -0pi -e 's#github.com/WuKongIM/WuKongIM/internal/runtime/channelid#github.com/WuKongIM/WuKongIM/pkg/protocol/channelid#g'
```

Expected: `rg -n 'github.com/WuKongIM/WuKongIM/internal/runtime/channelid' --glob '*.go'` prints no Go import matches.

- [ ] **Step 3: Keep import aliases stable**

Run:

```sh
gofmt -w $(rg -l 'github.com/WuKongIM/WuKongIM/pkg/protocol/channelid' --glob '*.go') pkg/protocol/channelid/*.go
```

Expected: files compile with existing aliases such as `runtimechannelid`. Do not rename aliases in this task; alias cleanup would add noise without changing ownership.

- [ ] **Step 4: Update current docs for the new location**

Edit `AGENTS.md` directory structure:

```text
pkg/
  protocol/              协议对象、编解码与协议级 helper
    channelid/           个人频道、命令频道、agent 频道、请求级临时频道等 channel id 派生
```

Edit `internal/FLOW.md` and `internalv2/FLOW.md` so any current reference to old `internal/runtime/channelid` says `pkg/protocol/channelid`.

Append this concise note to `docs/development/PROJECT_KNOWLEDGE.md`:

```markdown
- Stage 2 package promotion extracted protocol-facing channel ID helpers to `pkg/protocol/channelid`; v1 and v2 server packages must not add new imports of old `internal/runtime/channelid`.
```

- [ ] **Step 5: Verify channelid move**

Run:

```sh
GOWORK=off go test ./pkg/protocol/channelid -count=1
GOWORK=off go test ./internalv2/usecase/message ./internalv2/usecase/cmdsync ./internalv2/runtime/channelappend ./internalv2/access/api ./internalv2/infra/cluster -count=1
```

Expected: both commands pass.

- [ ] **Step 6: Commit channelid extraction**

Run:

```sh
git add pkg/protocol/channelid internal internalv2 AGENTS.md docs/development/PROJECT_KNOWLEDGE.md
git add -u internal/runtime/channelid
git commit -m "refactor: move channel id helpers to protocol package"
```

Expected: commit contains only the channelid move, import rewrites, gofmt, and current docs for this move.

### Task 2: Move Plugin Wire Contracts To `pkg/plugin/pluginproto`

**Files:**
- Move: `internal/usecase/plugin/pluginproto/README.md` -> `pkg/plugin/pluginproto/README.md`
- Move: `internal/usecase/plugin/pluginproto/plugin.proto` -> `pkg/plugin/pluginproto/plugin.proto`
- Move: `internal/usecase/plugin/pluginproto/plugin.go` -> `pkg/plugin/pluginproto/plugin.go`
- Move: `internal/usecase/plugin/pluginproto/plugin.pb.go` -> `pkg/plugin/pluginproto/plugin.pb.go`
- Move tests from `internal/usecase/plugin/pluginproto/*_test.go` to `pkg/plugin/pluginproto/`.
- Modify imports in every Go file returned by:

```sh
rg -l 'github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto' --glob '*.go'
```

- Modify plugin testdata under `test/e2ev2/plugin/**/main.go`.
- Modify docs: `AGENTS.md`, `internal/FLOW.md`, `internalv2/FLOW.md`, `docs/development/PROJECT_KNOWLEDGE.md`.

- [ ] **Step 1: Move the package**

Run:

```sh
mkdir -p pkg/plugin
git mv internal/usecase/plugin/pluginproto pkg/plugin/pluginproto
```

Expected: `git status --short` shows the package moved into `pkg/plugin/pluginproto`.

- [ ] **Step 2: Update `plugin.proto` go package**

Edit `pkg/plugin/pluginproto/plugin.proto`:

```proto
option go_package = "github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto;pluginproto";
```

Do not change any message names or field numbers.

- [ ] **Step 3: Regenerate protobuf Go code**

Run:

```sh
PATH="/Users/tt/go/bin:$PATH" /usr/local/include/protoc/bin/protoc \
  --go_out=. \
  --go_opt=paths=source_relative \
  pkg/plugin/pluginproto/plugin.proto
```

Expected: `pkg/plugin/pluginproto/plugin.pb.go` begins with `// source: pkg/plugin/pluginproto/plugin.proto` and package name `pluginproto`.

- [ ] **Step 4: Rewrite Go imports mechanically**

Run:

```sh
rg -l -0 'github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto' --glob '*.go' \
  | xargs -0 perl -0pi -e 's#github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto#github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto#g'
```

Expected: `rg -n 'github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto' --glob '*.go'` prints no Go import matches.

- [ ] **Step 5: Update pluginproto README**

Replace the regeneration command in `pkg/plugin/pluginproto/README.md` with:

```sh
PATH="/Users/tt/go/bin:$PATH" /usr/local/include/protoc/bin/protoc --go_out=. --go_opt=paths=source_relative pkg/plugin/pluginproto/plugin.proto
```

Keep the compatibility wording that says field numbers must remain compatible with `github.com/WuKongIM/go-pdk`.

- [ ] **Step 6: Update current docs**

Edit `AGENTS.md` directory structure:

```text
pkg/
  plugin/
    pluginproto/          插件 RPC protobuf wire contract，与 go-pdk 字段号兼容
```

Edit `internal/FLOW.md` and `internalv2/FLOW.md` so current plugin wire-contract references point to `pkg/plugin/pluginproto`.

Append this concise note to `docs/development/PROJECT_KNOWLEDGE.md`:

```markdown
- Plugin wire contracts live in `pkg/plugin/pluginproto`; keep protobuf field numbers compatible with `github.com/WuKongIM/go-pdk` and do not add new imports of old `internal/usecase/plugin/pluginproto`.
```

- [ ] **Step 7: Verify pluginproto move**

Run:

```sh
GOWORK=off go test ./pkg/plugin/pluginproto -count=1
GOWORK=off go test ./internalv2/usecase/plugin ./internalv2/access/plugin ./internalv2/access/manager ./internalv2/access/node ./internalv2/app -count=1
```

Expected: both commands pass.

- [ ] **Step 8: Commit pluginproto extraction**

Run:

```sh
git add pkg/plugin/pluginproto internal internalv2 test/e2ev2 AGENTS.md docs/development/PROJECT_KNOWLEDGE.md
git add -u internal/usecase/plugin/pluginproto
git commit -m "refactor: move plugin wire contracts to pkg plugin"
```

Expected: commit contains the package move, generated protobuf update, import rewrites, and current docs for this move.

### Task 3: Move Plugin Host Runtime To `pkg/plugin/pluginhost`

**Files:**
- Move: `internal/runtime/plugin/` -> `pkg/plugin/pluginhost/`
- Modify package declarations in `pkg/plugin/pluginhost/*.go` and `pkg/plugin/pluginhost/*_test.go` from `package plugin` to `package pluginhost`.
- Modify imports in:
  - `internal/access/plugin/server_test.go`
  - `internal/app/app.go`
  - `internal/app/plugin.go`
  - `internalv2/app/plugin.go`
  - `internalv2/app/plugin_test.go`
- Modify docs: `AGENTS.md`, `internal/FLOW.md`, `internalv2/FLOW.md`, `pkg/slot/FLOW.md`, `docs/development/PROJECT_KNOWLEDGE.md`.

- [ ] **Step 1: Move the package**

Run:

```sh
git mv internal/runtime/plugin pkg/plugin/pluginhost
```

Expected: `git status --short` shows the plugin runtime package moved into `pkg/plugin/pluginhost`.

- [ ] **Step 2: Rename package declarations**

Run:

```sh
perl -0pi -e 's/^package plugin$/package pluginhost/m' pkg/plugin/pluginhost/*.go
gofmt -w pkg/plugin/pluginhost
```

Expected: `rg -n '^package plugin$' pkg/plugin/pluginhost` prints no matches.

- [ ] **Step 3: Rewrite imports**

Run:

```sh
rg -l -0 'github.com/WuKongIM/WuKongIM/internal/runtime/plugin' --glob '*.go' \
  | xargs -0 perl -0pi -e 's#github.com/WuKongIM/WuKongIM/internal/runtime/plugin#github.com/WuKongIM/WuKongIM/pkg/plugin/pluginhost#g'
```

Expected: `rg -n 'github.com/WuKongIM/WuKongIM/internal/runtime/plugin' --glob '*.go'` prints no Go import matches.

- [ ] **Step 4: Rename import aliases in direct users**

Edit the direct users so aliases and selectors read `pluginhost`:

```go
pluginhost "github.com/WuKongIM/WuKongIM/pkg/plugin/pluginhost"
```

Then replace selectors such as `runtimeplugin.NewRuntime`, `runtimeplugin.NewStore`, and `runtimeplugin.ObservedPlugin` with `pluginhost.NewRuntime`, `pluginhost.NewStore`, and `pluginhost.ObservedPlugin`.

Run:

```sh
gofmt -w internal/access/plugin/server_test.go internal/app/app.go internal/app/plugin.go internalv2/app/plugin.go internalv2/app/plugin_test.go
```

Expected: `rg -n 'runtimeplugin|internal/runtime/plugin' internal internalv2 pkg/plugin/pluginhost --glob '*.go'` prints no matches.

- [ ] **Step 5: Update current docs**

Edit `AGENTS.md` directory structure:

```text
pkg/
  plugin/
    pluginhost/           节点本地插件进程、Unix socket、热重载和期望状态运行时
    pluginproto/          插件 RPC protobuf wire contract，与 go-pdk 字段号兼容
```

Edit `internal/FLOW.md` so the plugin runtime row points to `pkg/plugin/pluginhost` rather than `internal/runtime/plugin`.

Edit `internalv2/FLOW.md` so the v2 app dependency note says plugin host runtime is shared under `pkg/plugin/pluginhost`.

Edit `pkg/slot/FLOW.md` to replace the current `internal/runtime/plugin` mention with `pkg/plugin/pluginhost`.

Append this concise note to `docs/development/PROJECT_KNOWLEDGE.md`:

```markdown
- The node-local plugin process host lives in `pkg/plugin/pluginhost`; v2 app wiring adapts it to `internalv2/usecase/plugin` without depending on old `internal/runtime/plugin`.
```

- [ ] **Step 6: Verify pluginhost move**

Run:

```sh
GOWORK=off go test ./pkg/plugin/pluginhost -count=1
GOWORK=off go test ./internalv2/app ./internalv2/usecase/plugin ./internalv2/access/plugin -count=1
GOWORK=off go test ./internal/app ./internal/access/plugin -count=1
```

Expected: all three commands pass. The old `internal` packages compile against the neutral shared plugin host path without importing `internalv2`.

- [ ] **Step 7: Commit pluginhost extraction**

Run:

```sh
git add pkg/plugin/pluginhost internal internalv2 AGENTS.md pkg/slot/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
git add -u internal/runtime/plugin
git commit -m "refactor: move plugin host runtime to pkg plugin"
```

Expected: commit contains only the plugin host move, package rename, import rewrites, and current docs for this move.

### Task 4: Add InternalV2 Shared-Contract Import Boundary

**Files:**
- Create: `internalv2/import_boundary_test.go`

- [ ] **Step 1: Add the import boundary test**

Create `internalv2/import_boundary_test.go`:

```go
package internalv2_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestInternalV2DoesNotImportLegacySharedContracts(t *testing.T) {
	forbidden := []string{
		"github.com/WuKongIM/WuKongIM/internal/runtime/channelid",
		"github.com/WuKongIM/WuKongIM/internal/runtime/plugin",
		"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto",
	}
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "bench":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		parsed, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range parsed.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			for _, blocked := range forbidden {
				if importPath == blocked || strings.HasPrefix(importPath, blocked+"/") {
					t.Fatalf("%s imports old shared contract %q; use pkg/protocol/channelid, pkg/plugin/pluginproto, or pkg/plugin/pluginhost", path, importPath)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir() error = %v", err)
	}
}
```

- [ ] **Step 2: Run the boundary test**

Run:

```sh
GOWORK=off go test ./internalv2 -run TestInternalV2DoesNotImportLegacySharedContracts -count=1
```

Expected: the test passes and catches any remaining old helper imports under `internalv2`.

- [ ] **Step 3: Commit the boundary test**

Run:

```sh
git add internalv2/import_boundary_test.go
git commit -m "test: guard internalv2 shared contract imports"
```

Expected: commit contains only `internalv2/import_boundary_test.go`.

### Task 5: Final Phase 1 Verification

**Files:**
- Read-only verification.

- [ ] **Step 1: Verify no old v2 helper imports remain**

Run:

```sh
rg -n 'github.com/WuKongIM/WuKongIM/internal/(runtime/channelid|runtime/plugin|usecase/plugin/pluginproto)' internalv2 test/e2ev2 --glob '*.go'
```

Expected: no matches.

- [ ] **Step 2: Verify old helper package paths are gone**

Run:

```sh
test ! -d internal/runtime/channelid
test ! -d internal/runtime/plugin
test ! -d internal/usecase/plugin/pluginproto
```

Expected: all three commands exit 0.

- [ ] **Step 3: Run focused package tests**

Run:

```sh
GOWORK=off go test ./pkg/protocol/channelid ./pkg/plugin/pluginproto ./pkg/plugin/pluginhost -count=1
GOWORK=off go test ./internalv2 ./internalv2/usecase/message ./internalv2/usecase/cmdsync ./internalv2/usecase/plugin ./internalv2/runtime/channelappend ./internalv2/access/plugin ./internalv2/app -count=1
GOWORK=off go test ./internal/bench/... ./cmd/wkbench ./cmd/wkcli/... -count=1
```

Expected: all focused tests pass. The `internal/bench` command proves black-box wkbench tooling still builds after shared package extraction.

- [ ] **Step 4: Run a broader compile gate**

Run:

```sh
GOWORK=off go test ./cmd/wukongim ./internal/... ./internalv2/... ./pkg/... -count=1
```

Expected: command passes. If it exposes unrelated existing flake, rerun the failing package once and record both outputs before deciding whether to fix or defer.

- [ ] **Step 5: Run whitespace and diff checks**

Run:

```sh
git diff --check
git status --short --branch
```

Expected: `git diff --check` exits 0 and `git status` shows only expected committed branch state.

## Execution Handoff

Plan complete when saved and committed. Two execution options:

1. Subagent-Driven (recommended): dispatch a fresh subagent per task, review between tasks, and keep commits small.
2. Inline Execution: execute tasks in this session using `superpowers:executing-plans`, with checkpoints after each task.
