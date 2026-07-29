# Business Channel Member Management Implementation Plan

> **For Codex:** Execute this plan with `implement`, `test-driven-development`, and `verification-before-completion`. Keep every read and mutation on ordinary cluster semantics, including a single-node cluster.

**Goal:** Let Manager users view and manage subscribers, denylist members, and allowlist members from the business-channel list while preserving channel create/edit behavior.

**Architecture:** Extend the existing `internal/usecase/management` channel inventory module with narrow channel/member ports. Back those ports with the channel use case for orchestration and a Slot-proxy-backed authoritative metadata path for exact reads and mutation results. Restore Manager HTTP routes with explicit create and patch semantics, validate all boundary inputs, and emit privacy-bounded structured audit logs. Refactor the existing React side sheet into detail/member modes with URL persistence, exact search, bounded cursor pages, permission-aware mutations, and authoritative refreshes after writes.

**Tech Stack:** Go, Gin, Slot Multi-Raft metadata FSM, React 19, TypeScript, Vite/Vitest, Testing Library, TanStack Router.

---

## Task 1: Make subscriber mutations return an exact durable change count

**Files:**

- Modify: `pkg/db/meta/table_subscriber.go`
- Modify: `pkg/db/meta/compat.go`
- Modify: `pkg/slot/fsm/command.go`
- Modify: `pkg/slot/fsm/statemachine.go`
- Modify: `pkg/slot/proxy/store.go`
- Test: `pkg/db/meta/subscriber_test.go`
- Test: `pkg/slot/fsm/state_machine_test.go`
- Test: `pkg/slot/proxy/integration_test.go`

**Steps:**

1. Add failing DB tests proving add-existing and remove-absent return zero changes while real inserts/deletes return exact distinct counts.
2. Add a mutation-result type and counting variants in the metadata batch without changing existing compatibility methods.
3. Add failing FSM tests proving replicated add/remove apply results encode the committed changed count.
4. Stage a result pointer from subscriber commands and finalize result-command bytes after the batch commit.
5. Add failing proxy tests for `AddChannelSubscribersCounted` and `RemoveChannelSubscribersCounted`, then submit with `ProposeWithHashSlotResult` and decode the FSM result.
6. Run the three focused package test suites.

## Task 2: Expose authoritative channel/member operations through cluster infrastructure

**Files:**

- Modify: `pkg/slot/proxy/promoted_cluster.go`
- Modify: `pkg/cluster/node.go`
- Modify: `pkg/cluster/default_slots.go`
- Modify: `pkg/cluster/node_slot_proxy_port.go`
- Modify: `internal/infra/cluster/channel_metadata.go`
- Test: `pkg/cluster/node_slot_proxy_port_test.go`
- Test: `internal/infra/cluster/channel_metadata_test.go`

**Steps:**

1. Add failing tests proving metadata point reads, subscriber pages, exact membership checks, and non-emptiness use the authoritative Slot owner and fail when authority is unavailable.
2. Break the current proxy-to-cluster registration dependency with a function-handler registration port so the default cluster node can own a proxy store without an import cycle.
3. Initialize the Slot proxy when the default Slot metadata DB is created and clear it during shutdown/restore teardown.
4. Add authoritative Node methods plus counted mutation methods, and make `ChannelMetadataStore` prefer those capabilities for business reads/mutations while retaining restore-only local reads.
5. Preserve UID reverse-membership projection behavior for ordinary subscribers only.
6. Run focused cluster and infrastructure tests.

## Task 3: Add management use cases for detail, create/edit, lists, exact search, and mutations

**Files:**

- Modify: `internal/usecase/management/nodes.go`
- Modify: `internal/usecase/management/channels_biz.go`
- Modify: `internal/usecase/channel/app.go`
- Modify: `internal/usecase/channel/types.go`
- Test: `internal/usecase/management/channels_biz_test.go`
- Test: `internal/usecase/channel/app_test.go`

**Steps:**

1. Add failing tests for authoritative detail reads and `has_*` flags.
2. Add failing tests for create-only conflict, edit-only not-found, patch preservation of unrelated metadata, new-ID restrictions, and readability/mutability of valid legacy IDs.
3. Add failing tests for subscriber/allowlist/denylist page ordering, exact UID hits/misses, `uid`/cursor exclusivity, parent-channel validation, personal-subscriber read-only behavior, 500-distinct-UID bound, and strict UID validation.
4. Add explicit management ports and result types rather than depending on access-layer DTOs.
5. Ensure the first allowlist/denylist add creates only the internal derived channel after authoritative parent validation.
6. Return `requested_count` and exact committed `changed_count`; ordinary subscriber mutations continue the current reverse-index projection contract and allow/deny mutations do not touch it.
7. Run focused use-case tests.

## Task 4: Restore and harden Manager HTTP routes and audit logging

**Files:**

- Modify: `internal/access/manager/server.go`
- Modify: `internal/access/manager/channels_biz.go`
- Modify: `internal/access/manager/server_test.go`
- Modify: `internal/app/wiring.go`
- Test: `internal/app/wiring_test.go` or a focused new wiring test

**Steps:**

1. Replace the obsolete “routes stay unmigrated” test with failing route, auth, validation, status mapping, and response-contract tests.
2. Register read routes under `cluster.channel:r` and create/patch/member mutations under `cluster.channel:w`.
3. Implement:
   - `GET /manager/channels/:channel_type/:channel_id`
   - `POST /manager/channels`
   - `PATCH /manager/channels/:channel_type/:channel_id`
   - `GET /manager/channels/:channel_type/:channel_id/{subscribers|allowlist|denylist}`
   - `POST /manager/channels/:channel_type/:channel_id/{list}/add`
   - `POST /manager/channels/:channel_type/:channel_id/{list}/remove`
4. Enforce default member limit 100, max 500, opaque cursor validation, exact `uid`, mutually exclusive `uid` and `cursor`, and status mapping (400/404/409/503/500).
5. Emit one structured mutation audit log containing operator, channel, list kind, operation, requested/changed count, result, timestamp, and at most one redacted UID sample; never log the full UID set.
6. Wire the existing channel use case and authoritative channel store into management.
7. Run Manager and app wiring tests.

## Task 5: Update the Manager API client contract

**Files:**

- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Test: `web/src/lib/manager-api.test.ts`

**Steps:**

1. Add failing tests for separate POST create and PATCH edit paths.
2. Add failing tests for `uid` list query and unchanged opaque cursor behavior.
3. Add response assertions for `requested_count` and `changed_count`.
4. Implement the typed client changes and run the focused Vitest file.

## Task 6: Implement the channel list member-data experience

**Files:**

- Modify: `web/src/pages/channels-biz/page.tsx`
- Modify: `web/src/pages/channels-biz/page.test.tsx`
- Modify: `web/src/i18n/messages.ts`
- Modify as needed: shared dialog/table primitives already used by the page

**Steps:**

1. Add failing UI tests for separate Detail and Member Data actions sharing one sheet.
2. Add failing URL tests for `channel_id`, `channel_type`, and `member_list`, including deep links and browser-back close behavior.
3. Add failing tests for tab order Subscribers → Denylist → Allowlist; tab changes clear exact UID search and reset paging.
4. Add failing tests for exact hit/miss, clear-to-page-one, UID detail link, manual refresh, preserved current page on next-page failure, and full error on first-page failure.
5. Replace append/load-more with one 100-row page and a client-side cursor stack for Previous/Next.
6. Add failing permission tests: `cluster.channel:r` can read; `cluster.channel:w` alone controls create/edit/add/remove visibility; personal-channel subscribers remain read-only.
7. Add failing mutation tests for strict paste validation, max 500 distinct UIDs, add from exact miss, remove from exact hit, single/bulk removal confirmation, processed-vs-changed feedback, uncertainty warning plus refresh on mutation failure, first-page return after add, previous-page fallback after empty remove, and query preservation in exact mode.
8. Implement with no totals, no cross-page selection, no auto polling, no export, and no clear-list action.
9. Run page and API Vitest suites plus web typecheck/build.

## Task 7: Documentation, regression verification, review, and commit

**Files:**

- Modify: `internal/access/manager/FLOW.md`
- Modify: `internal/usecase/management/FLOW.md`
- Modify: `internal/usecase/channel/FLOW.md`
- Modify: `internal/infra/cluster/FLOW.md`
- Modify if applicable: `pkg/cluster/FLOW.md`, `pkg/db/meta/FLOW.md`

**Steps:**

1. Update applicable flow documentation for restored routes, authoritative reads, internal derived-list creation, counted set mutations, and reverse-index projection.
2. Run `gofmt` and frontend formatting/lint commands supported by the repository.
3. Run focused Go tests for every changed package.
4. Run `GOWORK=off go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/... -count=1`.
5. Run frontend tests, typecheck, and production build.
6. Inspect the diff for secrets, unrelated edits, generated files, API terminology, and all confirmed UX exclusions.
7. Run the `code-review` workflow against the branch base, fix findings, and repeat fresh verification.
8. Use `finishing-a-development-branch` to confirm the integration path, then create one intentional commit on `codex/channel-member-management`.
