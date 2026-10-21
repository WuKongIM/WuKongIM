# MQTT source-drain cancellation diagnosis

Base: `5f3ecdf93e762a1b1551b44977a44f2e63c97802` in the shared recovery worktree. Only `internal/usecase/mqttsession` is changed. The original CI log is `/tmp/wukongim-v3-readiness-20261021/ci/final/artifact-12322262068/stdout-stderr.log`, lines 1612–1621.

## Confirmed cause

This is a product cancellation-boundary bug, not an assertion-only fixture problem. `Owners.Begin` creates a new context under its owner and forwards caller cancellation using `context.AfterFunc`. Each subscription operation already checks its direct parent synchronously, but `Unsubscribe → GroupProjection.project → SourceDrain.SealGroup` creates three nested scopes. Each inner scope sees the previous derived context, so delayed callback scheduling can hide original caller cancellation from all inner checks.

The existing canceled case passed 30/30 local attempts. A test-first addition uses the package's pre-existing delayed-cancellation fixture through the same real progress/CAS/storage path. It failed 3/3 with the exact CI chain: `ErrSubscriptionUnconfirmed + ErrSourceDrainPending`. A bounded pre-fix probe showed the original parent was already canceled, one source CAS had been rejected, and three additional qualification reads still occurred. Thus the inner drain produced pending before recognizing cancellation; merely replacing the outer error would not fence those effects.

The fix carries one private ancestor scope pointer through the derived context. Before nested admission and at effect checks, it checks every ancestor's parent cancellation and owner fence, plus its own. Deadline minimum, owner admission/capacity, scope Done/cancel, storage rules and retry counts remain unchanged. It does not copy arbitrary business context values, spawn a goroutine, or add a retry. The concrete failing chain has three scopes; construction can only point to an older scope, and active same-owner operations remain bounded by the existing MaxOperations limit.

The final regression retains all old modes and assertions, adds held-callback cancellation, and verifies no fresh qualification reads after cancellation, exactly one rejected source CAS, unchanged progressed source binding with `EndKnown=false`, committed Removing intent, and zero retained operations.

## Evidence and commands

All commands used `GOWORK=off GOTOOLCHAIN=go1.25.11 GOCACHE=/tmp/wukongim-v3-readiness-20261021/mqtt-diagnosis/gocache` and `go test -p=2` on `./internal/usecase/mqttsession`. Platform is darwin/arm64. Logs are JSONL; final outcomes are recorded in `test-summary.json`.

| Log | Selection | Expected outcome |
| --- | --- | --- |
| `baseline-original.jsonl` | original `/canceled$`, count=30 | historical local pass; did not reproduce scheduler race |
| `red-delayed.jsonl` | new `/canceled-delayed$`, count=3, before product edit | exact reported failure, 3/3 |
| `red-instrumented.jsonl` | same, count=1 | root canceled / CAS=1 / fresh reads=3; temporary probe removed |
| `red-strict-final.jsonl` | final stricter test against restored original product, count=1 | exact failure before reapplying repair |
| `green-contention-matrix.jsonl` | entire 19-mode contention matrix, count=3 | pass |
| `green-full-package.jsonl` | entire default package, count=1, timeout=5m | pass, 112.707s |
| `green-race-related.jsonl` | SourceDrain / Subscriptions / Group tests, race, count=1, timeout=4m | pass, 14.248s |

To reproduce the red in an isolated checkout of the base, apply only the test file from `mqtt-cancellation.patch`, leaving `subscriptions.go` unchanged, then run:

```sh
GOWORK=off GOTOOLCHAIN=go1.25.11 GOCACHE=/path/to/isolated-cache \
  go test -p=2 ./internal/usecase/mqttsession \
  -run '^TestSourceDrainProgressBeforeSealDoesNotClose/canceled-delayed$' \
  -count=3 -timeout=2m
```

Apply the product repair and rerun the same command for green. The final source patch, original instruction/source digests and all red logs remain preserved. Full-packet E2E or Linux CI has not been rerun in this subtask; parent owns final candidate verification.

## Delivery obligations

`FLOW.md` gets one sentence clarifying synchronous original-caller cancellation across nested projection/source scopes. Parent owns the required policy-defined `flow-doc-contracts` named check. Parent also owns the root Unreleased entry; suggested text: “Fixed MQTT unsubscribe cancellation being classified as pending source cleanup when nested operation cancellation propagation is delayed.” The new product code means prior ef798 product-tree equivalence cannot cover the final head.

No commit, push, Actions dispatch or cloud operation occurred. The task-local 820 MiB Go cache was removed after all tests completed; all evidence is retained.
