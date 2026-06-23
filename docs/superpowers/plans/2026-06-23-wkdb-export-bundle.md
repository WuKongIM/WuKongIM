# WKDB Export Bundle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `wkdb export` so a node-local current database can be streamed into the existing WKDB Import Bundle v1 format and round-tripped through `wkdb import`.

**Architecture:** Export is a read-only transfer operation over `inspect.OpenStore` handles. The transfer layer writes JSONL files plus `manifest.json`, reusing the import bundle file kinds and validation invariants; messages are paged by catalog channel and split into bounded files. The first version exports only the bundle v1 business data sets and intentionally excludes runtime, migration, controller, Raft, and cross-node aggregation state.

**Tech Stack:** Go, `pkg/db/inspect`, `pkg/db/meta`, `pkg/db/message`, `pkg/db/transfer`, shell smoke scripts.

---

### Task 1: Preserve Message Timestamp in Inspect

**Files:**
- Modify: `pkg/db/message/inspect.go`
- Modify: `pkg/db/inspect/execute.go`
- Test: `pkg/db/message/inspect_test.go`

- [ ] **Step 1: Write the failing test**

Add a test that appends a message with `ServerTimestampMS: 3000`, calls `message.InspectMessages`, and expects row field `server_timestamp_ms == int64(3000)`.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off /usr/local/go/bin/go test ./pkg/db/message -run TestInspectMessagesIncludesServerTimestampMS -count=1`

Expected: FAIL because `server_timestamp_ms` is missing.

- [ ] **Step 3: Write minimal implementation**

Add `server_timestamp_ms` to `inspectMessageRow`, `inspectColumns["message.message"]`, and `inspectColumnTypes`.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOWORK=off /usr/local/go/bin/go test ./pkg/db/message ./pkg/db/inspect -count=1`

- [ ] **Step 5: Commit**

Commit message: `fix(wkdb): expose message timestamp in inspect`

### Task 2: Add Streaming Export Writer

**Files:**
- Create: `pkg/db/transfer/exporter.go`
- Create: `pkg/db/transfer/exporter_test.go`
- Modify: `pkg/db/transfer/types.go`
- Modify: `pkg/db/inspect/store.go`

- [ ] **Step 1: Write the failing round-trip test**

Seed a real `db.NodeStore` with user, device, channel, subscriber, membership, conversation, channel_latest, and two messages. Reopen it through `inspect.OpenStore`, call `ExportBundle(ctx, exportRoot, inspectStore, ExportOptions{HashSlotCount: 16, PageSize: 1, MessageFileRows: 1})`, validate the bundle, import it into a fresh `NodeStore`, and assert the copied rows preserve message payloads and `ServerTimestampMS`.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off /usr/local/go/bin/go test ./pkg/db/transfer -run TestExportBundleRoundTripsCurrentStores -count=1`

Expected: FAIL because `ExportBundle` and `ExportOptions` are undefined.

- [ ] **Step 3: Add read-only store accessors**

Expose `Meta()` and `Messages()` on `pkg/db/inspect.Store` so transfer can use the existing read-only handles without opening writable storage.

- [ ] **Step 4: Implement minimal export**

Implement `ExportOptions`, `ExportStats`, `ExportBundle`, JSONL writers with SHA256 row accounting, manifest writing, metadata table paging via `meta.InspectScan`, message catalog paging via `message.InspectChannels`, and message row paging via `message.InspectMessages`.

- [ ] **Step 5: Run test to verify it passes**

Run: `GOWORK=off /usr/local/go/bin/go test ./pkg/db/transfer ./pkg/db/inspect -count=1`

- [ ] **Step 6: Commit**

Commit message: `feat(wkdb): export import bundle`

### Task 3: Add `wkdb export` CLI

**Files:**
- Create: `cmd/wkdb/export.go`
- Modify: `cmd/wkdb/main.go`
- Test: `cmd/wkdb/main_test.go`

- [ ] **Step 1: Write the failing CLI test**

Use a temp data dir with a small imported bundle or seeded store, invoke `runWithStreams([]string{"--data-dir", dir, "--hash-slot-count", "16", "export", "--output", out})`, and assert the command exits `exitOK`, writes a readable bundle, and prints exported row/file counts.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off /usr/local/go/bin/go test ./cmd/wkdb -run TestExportCommandWritesImportBundle -count=1`

Expected: FAIL with unknown command `export`.

- [ ] **Step 3: Implement CLI**

Add `runExport` with flags `--output`, `--overwrite`, `--page-size`, and `--message-file-rows`. Resolve config, open `inspect.OpenStore`, call `transfer.ExportBundle`, and print a compact summary.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOWORK=off /usr/local/go/bin/go test ./cmd/wkdb -count=1`

- [ ] **Step 5: Commit**

Commit message: `feat(wkdb): add export command`

### Task 4: Add Export Round-Trip Smoke Script and Docs

**Files:**
- Create: `scripts/wkdb-export-roundtrip-smoke.sh`
- Create: `scripts/wkdb_export_roundtrip_script_test.go`
- Modify: `cmd/wkdb/README.md`
- Modify: `docs/wiki/operations/wkdb-readonly-cli.md`

- [ ] **Step 1: Write the failing script test**

Build a wkdb binary in the test, run `scripts/wkdb-export-roundtrip-smoke.sh --work-dir <tmp>`, and assert output includes `export ok`, `roundtrip import ok`, and `wkdb export roundtrip smoke passed`.

- [ ] **Step 2: Run test to verify it fails**

Run: `GOWORK=off /usr/local/go/bin/go test ./scripts -run TestWKDBExportRoundTripSmokeScript -count=1`

Expected: FAIL because the script does not exist.

- [ ] **Step 3: Implement script**

Create a seed database via `scripts/wkdb-import-smoke.sh`, export it, dry-run validate the exported bundle, import into a second fresh store, and query copied rows.

- [ ] **Step 4: Update docs**

Document `wkdb export --output`, overwrite behavior, read-only semantics, same-format compatibility with `wkdb import`, and non-goals.

- [ ] **Step 5: Run focused verification**

Run:

```bash
GOWORK=off /usr/local/go/bin/go test ./pkg/db/message ./pkg/db/inspect ./pkg/db/transfer ./cmd/wkdb -count=1
GOWORK=off /usr/local/go/bin/go test ./scripts -run 'TestWKDB(ImportSmoke|ExportRoundTrip)' -count=1
GO=/usr/local/go/bin/go scripts/wkdb-export-roundtrip-smoke.sh --work-dir /tmp/wkdb-export-roundtrip-smoke-main
```

- [ ] **Step 6: Commit**

Commit message: `test(wkdb): add export roundtrip smoke`
