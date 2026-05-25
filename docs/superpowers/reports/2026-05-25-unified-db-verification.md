# Unified DB Verification Report

## Scope

Task 27 final verification for `feature/unified-db` in `.worktrees/unified-db`.

The branch consolidates local message and metadata storage under `pkg/db`, migrates callers away from `pkg/channel/store` and `pkg/slot/meta`, removes those legacy packages, and updates the project/FLOW documentation.

## Commands

| Command | Result | Notes |
| --- | --- | --- |
| `files=$(git diff --name-only -- '*.go'); if [ -n "$files" ]; then gofmt -w $files; fi` | PASS | No uncommitted Go diffs required formatting. |
| `git diff --check` | PASS | No whitespace errors. |
| `GOWORK=off go test ./pkg/db/... -count=1` | PASS | Storage package tests passed. |
| `GOWORK=off go test ./pkg/channel/... ./pkg/channelv2/... ./pkg/slot/... ./internal/app -count=1` | PASS | Migrated package tests passed. |
| `GOWORK=off go test ./... -count=1` | FAIL | One transient-looking failure in `pkg/controllerv2/raft`; see broad test issue. |
| `GOWORK=off go test ./pkg/controllerv2/raft -run '^TestThreeControllerVotersCommitStateFile$' -count=1` | PASS | Focused rerun of the failing test passed. |
| `GOWORK=off go test ./pkg/controllerv2/raft -count=1` | PASS | Focused package rerun passed. |
| `GOWORK=off go test -run '^$' -bench . -benchmem ./pkg/db/message ./pkg/db/meta` | PASS | Benchmark smoke completed and reported `allocs/op`. |

## Broad Test Issue

The broad unit sweep failed once in `pkg/controllerv2/raft`:

```text
--- FAIL: TestThreeControllerVotersCommitStateFile (3.79s)
    service_test.go:82:
        Error Trace: .../pkg/controllerv2/raft/service_test.go:299
        Error: Received unexpected error:
            context deadline exceeded
        Test: TestThreeControllerVotersCommitStateFile
FAIL
github.com/WuKongIM/WuKongIM/pkg/controllerv2/raft
```

Follow-up investigation:

- `rg -n 'pkg/db|pkg/channel/store|pkg/slot/meta' pkg/controllerv2/raft` returned no references, so the failing package is not part of the unified DB migration surface.
- `GOWORK=off go test ./pkg/controllerv2/raft -run '^TestThreeControllerVotersCommitStateFile$' -count=1` passed in `1.415s`.
- `GOWORK=off go test ./pkg/controllerv2/raft -count=1` passed in `3.223s`.

This looks like an existing timing-sensitive controller Raft test, not a unified DB regression. No code changes were made for this unrelated failure.

## Benchmark Smoke

Key benchmark lines from the storage smoke:

```text
pkg: github.com/WuKongIM/WuKongIM/pkg/db/message
BenchmarkChannelLogAppend/records=1-10              4118576 ns/op     2449 B/op      75 allocs/op
BenchmarkChannelLogAppend/records=32-10             4558245 ns/op    76202 B/op    2218 allocs/op
BenchmarkChannelLogAppend/records=256-10            5986052 ns/op   611793 B/op   17680 allocs/op
BenchmarkChannelLogAppendParallel-10                4054959 ns/op     2446 B/op      73 allocs/op
BenchmarkChannelLogRead-10                            71245 ns/op    91365 B/op    2928 allocs/op
BenchmarkChannelLogGetByMessageID-10                   1728 ns/op      799 B/op      35 allocs/op
BenchmarkChannelLogRetentionTrim-10                 8122134 ns/op     6619 B/op     167 allocs/op
PASS
ok github.com/WuKongIM/WuKongIM/pkg/db/message 13.394s

pkg: github.com/WuKongIM/WuKongIM/pkg/db/meta
BenchmarkUserCreate-10                              4082029 ns/op      301 B/op      15 allocs/op
BenchmarkUserGet-10                                     393.4 ns/op    121 B/op       7 allocs/op
BenchmarkUserUpdate-10                              4135507 ns/op      273 B/op      15 allocs/op
BenchmarkChannelCachedGet-10                            107.6 ns/op    144 B/op       6 allocs/op
BenchmarkSubscriberAddPage/add-10                   4030577 ns/op      777 B/op      29 allocs/op
BenchmarkSubscriberAddPage/page-10                     4533 ns/op     7170 B/op     141 allocs/op
BenchmarkRuntimeMetaUpsert-10                       4034055 ns/op     2129 B/op      64 allocs/op
BenchmarkMixedBatch-10                              4033857 ns/op     5914 B/op     106 allocs/op
BenchmarkHashSlotSnapshotExportImport/export-10       18236 ns/op    60463 B/op     320 allocs/op
BenchmarkHashSlotSnapshotExportImport/import-10     4491223 ns/op    72114 B/op    3119 allocs/op
PASS
ok github.com/WuKongIM/WuKongIM/pkg/db/meta 25.875s
```

## Review Checklist

- `rg -n 'pkg/channel/store|pkg/slot/meta' -g'*.go' .` returned no Go references.
- Pebble references under `pkg/db` are confined to `pkg/db/internal/engine`.
- Message storage stores durable rows by message sequence; the offset names that remain in `pkg/db/message` are compatibility surfaces and `seq-1` conversions.
- Slot metadata callers use `pkg/db/meta` compatibility APIs after migration.
- Meta writes use per-hash-slot locks (`MetaDB.lockForHashSlot`) instead of one package-wide write lock.
- Hash-slot snapshot export/import/delete traverses row, index, and system spans.
- Channel cache publish/delete happens only after batch commit success.
- Updated docs include `AGENTS.md`, `pkg/channel/FLOW.md`, `pkg/channelv2/FLOW.md`, `pkg/db/message/FLOW.md`, `pkg/db/meta/FLOW.md`, and `docs/development/PROJECT_KNOWLEDGE.md`.

## Follow-up Risks

- `pkg/controllerv2/raft.TestThreeControllerVotersCommitStateFile` still has timing-sensitive behavior under full-suite load and should be tracked separately from the unified DB migration.
- The message package intentionally keeps a compatibility layer with offset-addressed methods to keep migrated callers stable; future cleanup can move remaining callers to the typed sequence-first APIs.
