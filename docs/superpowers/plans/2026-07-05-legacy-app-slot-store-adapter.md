# Legacy App Slot Store Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move direct `pkg/slot/proxy` construction and type exposure in `internal/legacy/app` behind a legacy app-local adapter boundary without changing runtime behavior.

**Architecture:** Keep `pkg/slot/proxy.Store` as the real implementation, but introduce a local `slotProxyStore` alias plus constructor in `internal/legacy/app/slot_proxy_store.go`. `app.go`, `build.go`, and `channelmigration.go` should refer to the local boundary type only, while `slot_proxy_store.go` and `slot_proxy_rpc.go` remain the only production files allowed to import `pkg/slot/proxy`.

**Tech Stack:** Go, standard `go/parser` import-boundary test, existing legacy app unit/integration tests, `GOWORK=off` for focused verification.

---

## File Structure

- Create `internal/legacy/app/import_boundary_test.go`
  - Verifies production imports in `internal/legacy/app` only touch `github.com/WuKongIM/WuKongIM/pkg/slot/proxy` through adapter files.
- Create `internal/legacy/app/slot_proxy_store.go`
  - Owns `metastore.New(...)`, RPC handler registration, and the app-local `slotProxyStore` name.
- Modify `internal/legacy/app/slot_proxy_rpc.go`
  - Accepts `*slotProxyStore` instead of directly exposing `*metastore.Store` in the function signature.
- Modify `internal/legacy/app/app.go`
  - Replaces the direct `metastore` import, field type, and `Store()` return type with `*slotProxyStore`.
- Modify `internal/legacy/app/build.go`
  - Replaces direct `metastore.New(...)` and direct RPC registration with `newSlotProxyStore(...)`.
- Modify `internal/legacy/app/channelmigration.go`
  - Embeds `*slotProxyStore` instead of `*metastore.Store`.

## Task 1: Add Import Boundary Test

**Files:**
- Create: `internal/legacy/app/import_boundary_test.go`

- [ ] **Step 1: Write the failing boundary test**

Create `internal/legacy/app/import_boundary_test.go`:

```go
package app

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestLegacyAppImportsSlotProxyOnlyThroughAdapter(t *testing.T) {
	const slotProxyImport = "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	allowedFiles := map[string]struct{}{
		"slot_proxy_rpc.go":   {},
		"slot_proxy_store.go": {},
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob app go files: %v", err)
	}

	var offenders []string
	for _, file := range files {
		name := filepath.Base(file)
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse imports for %s: %v", file, err)
		}

		for _, imported := range parsed.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("unquote import %s in %s: %v", imported.Path.Value, file, err)
			}
			if path != slotProxyImport {
				continue
			}
			if _, ok := allowedFiles[name]; !ok {
				offenders = append(offenders, name)
			}
		}
	}

	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("pkg/slot/proxy import must stay behind legacy app adapter files; offenders: %s", strings.Join(offenders, ", "))
	}
}
```

- [ ] **Step 2: Run the boundary test and confirm it fails**

Run:

```bash
GOWORK=off go test ./internal/legacy/app -run TestLegacyAppImportsSlotProxyOnlyThroughAdapter -count=1
```

Expected: FAIL with offenders including `app.go`, `build.go`, and `channelmigration.go`.

## Task 2: Add Slot Proxy Store Adapter

**Files:**
- Create: `internal/legacy/app/slot_proxy_store.go`
- Modify: `internal/legacy/app/slot_proxy_rpc.go`

- [ ] **Step 1: Create the app-local store constructor**

Create `internal/legacy/app/slot_proxy_store.go`:

```go
package app

import (
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
	metastore "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
)

// slotProxyStore is the legacy app-local name for the slot proxy store boundary.
type slotProxyStore = metastore.Store

// newSlotProxyStore constructs the slot-backed metadata facade and registers its RPC handlers.
func newSlotProxyStore(cluster raftcluster.API, db *metadb.DB) *slotProxyStore {
	store := metastore.New(cluster, db)
	if cluster != nil {
		registerLegacySlotProxyRPCHandlers(store, cluster.RPCMux())
	}
	return store
}
```

- [ ] **Step 2: Route RPC registration through the local type**

In `internal/legacy/app/slot_proxy_rpc.go`, change the function signature:

```go
func registerLegacySlotProxyRPCHandlers(store *slotProxyStore, mux *transport.RPCMux) {
	if store == nil || mux == nil {
		return
	}
	store.RegisterRPCHandlers(func(serviceID uint8, handler func(context.Context, []byte) ([]byte, error)) {
		mux.Handle(serviceID, transport.RPCHandler(func(ctx context.Context, payload []byte) ([]byte, error) {
			body, err := handler(ctx, payload)
			return body, mapSlotProxyErrorToLegacy(err)
		}))
	})
}
```

Keep the existing `metastore` import in this file because `mapSlotProxyErrorToLegacy` still maps `metastore.ErrNoLeader`, `metastore.ErrNotLeader`, and `metastore.ErrSlotNotFound`.

- [ ] **Step 3: Run the boundary test and confirm remaining failures**

Run:

```bash
GOWORK=off go test ./internal/legacy/app -run TestLegacyAppImportsSlotProxyOnlyThroughAdapter -count=1
```

Expected: FAIL with offenders limited to `app.go`, `build.go`, and `channelmigration.go`.

## Task 3: Rewire App Store Construction

**Files:**
- Modify: `internal/legacy/app/app.go`
- Modify: `internal/legacy/app/build.go`

- [ ] **Step 1: Remove direct slot proxy import from app state**

In `internal/legacy/app/app.go`, remove this import:

```go
metastore "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
```

Change the `App` field:

```go
store                     *slotProxyStore
```

Change the accessor:

```go
func (a *App) Store() *slotProxyStore {
	if a == nil {
		return nil
	}
	return a.store
}
```

- [ ] **Step 2: Use the adapter constructor during build**

In `internal/legacy/app/build.go`, remove this import:

```go
metastore "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
```

Replace:

```go
app.store = metastore.New(app.cluster, app.db)
registerLegacySlotProxyRPCHandlers(app.store, app.cluster.RPCMux())
```

With:

```go
app.store = newSlotProxyStore(app.cluster, app.db)
```

- [ ] **Step 3: Run focused build/accessor tests**

Run:

```bash
GOWORK=off go test ./internal/legacy/app -run 'TestNewBuildsDBClusterStoreMessageAndGatewayAdapter|TestAccessorsExposeBuiltRuntime' -count=1
```

Expected: PASS.

## Task 4: Rewire Channel Migration Store Boundary

**Files:**
- Modify: `internal/legacy/app/channelmigration.go`
- Modify: `internal/legacy/app/build.go`

- [ ] **Step 1: Remove direct slot proxy import from channel migration**

In `internal/legacy/app/channelmigration.go`, remove this import:

```go
metastore "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
```

Replace the store wrapper:

```go
type appChannelMigrationStore struct {
	*slotProxyStore
}

func (s appChannelMigrationStore) ListRunnableTasksForLocalLeaderSlots(ctx context.Context, nowMS int64, limit int) ([]channelmigrationruntime.Task, error) {
	if s.slotProxyStore == nil {
		return nil, channel.ErrInvalidConfig
	}
	return s.slotProxyStore.ListRunnableChannelMigrationTasksForLocalLeaderSlots(ctx, nowMS, limit)
}

func (s appChannelMigrationStore) GarbageCollectTerminalTasks(ctx context.Context, beforeMS int64, limit int) (int, error) {
	if s.slotProxyStore == nil {
		return 0, channel.ErrInvalidConfig
	}
	return s.slotProxyStore.GarbageCollectTerminalChannelMigrationTasks(ctx, beforeMS, limit)
}
```

- [ ] **Step 2: Update channel migration build wiring**

In `internal/legacy/app/build.go`, replace:

```go
appChannelMigrationStore{Store: app.store},
```

With:

```go
appChannelMigrationStore{slotProxyStore: app.store},
```

- [ ] **Step 3: Run focused channel migration tests**

Run:

```bash
GOWORK=off go test ./internal/legacy/app -run 'TestBuildWiresChannelMigrationAfterChannelMeta|TestChannelMigration' -count=1
```

Expected: PASS.

## Task 5: Verify, Diff Check, and Commit

**Files:**
- Verify all files changed in Tasks 1-4.

- [ ] **Step 1: Run the import boundary test**

Run:

```bash
GOWORK=off go test ./internal/legacy/app -run TestLegacyAppImportsSlotProxyOnlyThroughAdapter -count=1
```

Expected: PASS.

- [ ] **Step 2: Run focused app tests**

Run:

```bash
GOWORK=off go test ./internal/legacy/app -run 'TestNewBuildsDBClusterStoreMessageAndGatewayAdapter|TestAccessorsExposeBuiltRuntime|TestBuildWiresChannelMigrationAfterChannelMeta' -count=1
```

Expected: PASS.

- [ ] **Step 3: Run package compile checks**

Run:

```bash
GOWORK=off go test ./internal/legacy/app ./internal/legacy/usecase/plugin ./pkg/slot/proxy -run '^$' -count=1
```

Expected: PASS.

- [ ] **Step 4: Run whitespace/conflict check**

Run:

```bash
git diff --check
```

Expected: no output.

- [ ] **Step 5: Commit**

Run:

```bash
git status --short
git add internal/legacy/app/import_boundary_test.go \
	internal/legacy/app/slot_proxy_store.go \
	internal/legacy/app/slot_proxy_rpc.go \
	internal/legacy/app/app.go \
	internal/legacy/app/build.go \
	internal/legacy/app/channelmigration.go
git commit -m "refactor: isolate legacy app slot proxy adapter"
```

Expected: commit created.

## Self-Review

- Spec coverage: This plan targets the next package-promotion cleanup after `pkg/slot/proxy` itself was made clean: keep legacy implementation behavior, reduce direct app-level coupling, and add a regression test.
- Placeholder scan: No task uses open-ended placeholders; each edit includes exact file paths, code, commands, and expected outcomes.
- Type consistency: `slotProxyStore` is introduced once as an alias in `slot_proxy_store.go`; `app.go`, `build.go`, `slot_proxy_rpc.go`, and `channelmigration.go` all use the same name.
- Risk boundary: This task intentionally does not rewrite `pkg/slot/proxy.Store` internals or replace every method with small interfaces. It only creates a local adapter boundary and locks the import rule.
