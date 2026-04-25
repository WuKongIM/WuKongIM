# Manager Channel Runtime Meta Detail Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a JWT-protected `GET /manager/channel-runtime-meta/:channel_type/:channel_id` endpoint that returns one authoritative channel runtime meta object enriched with `slot_id`, `hash_slot`, `features`, and `lease_until_ms`.

**Architecture:** Keep the detail read path narrow. Reuse `slot/proxy.Store.GetChannelRuntimeMeta(...)` for the single authoritative record read, and let `internal/usecase/management` enrich the returned runtime meta with `cluster.SlotForKey(channelID)` and `cluster.HashSlotForKey(channelID)`. Expose the new HTTP route under the existing `cluster.channel:r` permission group and keep error mapping fail-closed: invalid arguments as `400`, missing auth as `401`, permission failures as `403`, missing records as `404`, and slot-leader authoritative read failures as `503`.

**Tech Stack:** Go, `gin`, `testing`, `testify`, `internal/usecase/management`, `internal/access/manager`, `internal/app`, `pkg/cluster`, `pkg/slot/proxy`.

---

## References

- Spec: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md`
- Follow `@superpowers:test-driven-development` for every code change.
- Run `@superpowers:verification-before-completion` before claiming execution is done.
- Before editing a package, re-check whether it has a `FLOW.md`; if behavior changes, update it.
- `internal/usecase/management`, `internal/access/manager`, and `internal/app` currently have no `FLOW.md`.
- Add English comments on new exported methods, DTOs, and helper types to satisfy `AGENTS.md`.

## File Structure

- Modify: `internal/usecase/management/app.go` — extend the channel-runtime-meta reader boundary with single-record authoritative lookup.
- Modify: `internal/usecase/management/channel_runtime_meta.go` — add detail DTO and usecase method while preserving the existing list API.
- Modify: `internal/usecase/management/channel_runtime_meta_test.go` — add detail-focused unit tests and fake reader support.
- Modify: `internal/access/manager/server.go` — expand the manager dependency interface with the detail usecase method.
- Modify: `internal/access/manager/routes.go` — register `GET /manager/channel-runtime-meta/:channel_type/:channel_id` under the existing `cluster.channel:r` permission group.
- Modify: `internal/access/manager/channel_runtime_meta.go` — add path parsing, detail DTO mapping, and HTTP error translation.
- Modify: `internal/access/manager/server_test.go` — add auth, validation, not-found, unavailable, and success contract tests for the detail endpoint.
- Modify: `internal/app/build.go` — keep compile-time wiring aligned once the management interface grows.
- Modify: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md` — only if implementation discovers drift.

### Task 1: Add management usecase detail lookup

**Files:**
- Modify: `internal/usecase/management/app.go`
- Modify: `internal/usecase/management/channel_runtime_meta.go`
- Modify: `internal/usecase/management/channel_runtime_meta_test.go`

- [ ] **Step 1: Write the failing usecase tests**

Add focused tests that lock the new detail behavior, for example:

```go
func TestGetChannelRuntimeMetaRejectsInvalidChannelType(t *testing.T) { /* channel_type <= 0 -> ErrInvalidArgument */ }
func TestGetChannelRuntimeMetaReturnsDetailWithSlotAndHashSlot(t *testing.T) { /* slot_id/hash_slot/features/lease_until_ms mapped */ }
func TestGetChannelRuntimeMetaPropagatesNotFound(t *testing.T) { /* metadb.ErrNotFound passthrough */ }
func TestGetChannelRuntimeMetaPropagatesAuthoritativeReadErrors(t *testing.T) { /* raftcluster.ErrNoLeader passthrough */ }
```

Use a fake runtime-meta reader that returns a single `metadb.ChannelRuntimeMeta` record so the tests exercise real DTO mapping instead of HTTP serialization.

- [ ] **Step 2: Run the focused usecase tests to verify they fail**

Run:

```bash
go test ./internal/usecase/management -run 'TestGetChannelRuntimeMeta' -count=1
```

Expected: FAIL because the detail DTO and method do not exist yet.

- [ ] **Step 3: Implement the minimal usecase detail path**

Add a dedicated detail DTO and method, for example:

```go
type ChannelRuntimeMetaDetail struct {
    ChannelRuntimeMeta
    HashSlot     uint16
    Features     uint64
    LeaseUntilMS int64
}

func (a *App) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (ChannelRuntimeMetaDetail, error)
```

Rules:
- Reject non-positive `channel_type` with `metadb.ErrInvalidArgument`.
- Keep the single-record read on `a.channelRuntimeMeta.GetChannelRuntimeMeta(...)`; do not scan pages or add a new `pkg/cluster` read API for this.
- Derive `slot_id` via `a.cluster.SlotForKey(channelID)` and `hash_slot` via `a.cluster.HashSlotForKey(channelID)`.
- Reuse the existing stable status mapping used by the list DTO.
- Preserve `features` and `lease_until_ms` exactly from `metadb.ChannelRuntimeMeta`.
- Add English comments on the new exported DTO and method.

- [ ] **Step 4: Re-run the focused usecase tests to verify they pass**

Run:

```bash
go test ./internal/usecase/management -run 'TestGetChannelRuntimeMeta' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the usecase slice**

```bash
git add internal/usecase/management/app.go internal/usecase/management/channel_runtime_meta.go internal/usecase/management/channel_runtime_meta_test.go
git commit -m "feat: add manager channel runtime meta detail usecase"
```

### Task 2: Add manager HTTP detail endpoint

**Files:**
- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/channel_runtime_meta.go`
- Modify: `internal/access/manager/server_test.go`

- [ ] **Step 1: Write the failing HTTP tests**

Add focused tests for the new route contract, for example:

```go
func TestManagerChannelRuntimeMetaDetailRejectsMissingToken(t *testing.T) { /* 401 */ }
func TestManagerChannelRuntimeMetaDetailRejectsInsufficientPermission(t *testing.T) { /* 403 */ }
func TestManagerChannelRuntimeMetaDetailRejectsInvalidChannelType(t *testing.T) { /* 400 */ }
func TestManagerChannelRuntimeMetaDetailReturnsNotFound(t *testing.T) { /* 404 */ }
func TestManagerChannelRuntimeMetaDetailReturnsServiceUnavailableWhenAuthoritativeReadUnavailable(t *testing.T) { /* 503 */ }
func TestManagerChannelRuntimeMetaDetailReturnsObject(t *testing.T) { /* exact JSON body */ }
```

Extend the existing `managementStub` with one detail method so the tests stay close to the real route wiring and permission middleware.

- [ ] **Step 2: Run the focused HTTP tests to verify they fail**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerChannelRuntimeMetaDetail' -count=1
```

Expected: FAIL because the route, dependency method, and handler do not exist yet.

- [ ] **Step 3: Implement the minimal HTTP endpoint**

Add the route and handler with behavior like:

```go
channelRuntimeMeta.GET("/channel-runtime-meta/:channel_type/:channel_id", s.handleChannelRuntimeMetaDetail)
```

Handler rules:
- Parse `channel_type` from the path and reject invalid or non-positive values with `400 bad_request`.
- Pass `channel_id` exactly as provided in the path.
- Map `metadb.ErrInvalidArgument` to `400`, `metadb.ErrNotFound` to `404`, slot-leader availability errors to `503`, and unexpected errors to `500`.
- Return a single JSON object, not an `items` wrapper.
- Reuse the existing `cluster.channel:r` permission group and JWT middleware without introducing a second auth path.

- [ ] **Step 4: Re-run the focused HTTP tests to verify they pass**

Run:

```bash
go test ./internal/access/manager -run 'TestManagerChannelRuntimeMetaDetail' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the HTTP slice**

```bash
git add internal/access/manager/server.go internal/access/manager/routes.go internal/access/manager/channel_runtime_meta.go internal/access/manager/server_test.go
git commit -m "feat: add manager channel runtime meta detail endpoint"
```

### Task 3: Align app wiring and run focused verification

**Files:**
- Modify: `internal/app/build.go`
- Modify: `docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md` (only if drift appears)

- [ ] **Step 1: Write the minimal compile/wiring adjustment**

Keep `managementusecase.New(...)` wired to `app.store` after the reader interface grows. If `build.go` already satisfies the new interface structurally, keep this step to a compile-only confirmation and avoid unnecessary edits.

- [ ] **Step 2: Run focused verification**

Run:

```bash
go test ./internal/usecase/management ./internal/access/manager ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 3: Review docs for drift**

Only update the spec if the final implementation differs from the approved route, DTO fields, or error semantics.

- [ ] **Step 4: Commit the wiring / verification slice**

```bash
git add internal/app/build.go docs/superpowers/specs/2026-04-21-manager-api-foundation-design.md
git commit -m "chore: align manager channel runtime meta detail wiring"
```

## Verification Checklist

- `go test ./internal/usecase/management -run 'TestGetChannelRuntimeMeta' -count=1`
- `go test ./internal/access/manager -run 'TestManagerChannelRuntimeMetaDetail' -count=1`
- `go test ./internal/usecase/management ./internal/access/manager ./internal/app -count=1`
- Optional broader sanity check if needed: `go test ./internal/... -count=1`

## Expected Outcome

After these tasks, the repo should expose a JWT-protected manager detail endpoint:

- `GET /manager/channel-runtime-meta/:channel_type/:channel_id`

with the approved stable JSON shape, slot-leader authoritative read semantics, and error mapping consistent with the rest of the manager API foundation.
