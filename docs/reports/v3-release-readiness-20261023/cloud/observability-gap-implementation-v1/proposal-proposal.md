# Minimal Online Delivery observability proposal

Status: read-only review complete; implementation and test execution require the
root agent's next instruction. No product source, worktree, workload, queue,
permission, timeout, retry, or delivery behavior was changed for this review.

Source: `79a7177032a6ae1ec7015cefe0e6f94eb0f19f06`, compared with
`origin/main` at `25a4765be7bed6696e1c9748142bdd61543b508c`. All reviewed
production and test files match both revisions byte for byte. Only the reviewed
PROJECT_KNOWLEDGE document differs. `source-hashes.json` freezes 34 finite inputs,
including applicable AGENTS/FLOW instructions and root CONTEXT vocabulary.

## Findings and limits

1. **Definite observability classification omission:** `ReasonSystemBusy` is an
   existing entry-independent SEND result. `internal/access/gateway/error_map.go`
   intentionally maps `ErrBackpressured` ahead of route errors to this result and
   maps the result to the existing wire busy reason. However,
   `internal/app/observability.go:429` omits it from
   `gatewaySendackReasonLabel`, so the metric observer receives `unknown`.
   There is a second omission at `pkg/metrics/gateway.go:273`:
   `normalizeGatewaySendackReason` would reject `system_busy` even if app alone
   were corrected. Both layers need the single fixed `system_busy` value.
   This changes only the metric partition; total SENDACK counts and wire reasons
   stay unchanged. It does not prove why a permission cohort saturated.
2. **Definite diagnostic join gap, not a delivery correctness bug by itself:**
   `internal/app/online_delivery.go:59` logs one `plan_incomplete` warning with
   result, phase, mode, recipient count, one representative recipient UID,
   owner node, and original error. It carries neither MessageID nor sequence.
   A roster/time coincidence cannot join this warning to one acknowledged
   publication. The immutable `RecipientDeliveryPlan.Event` already contains
   MessageID and MessageSeq, but `PlanTerminalEvent` currently contains neither.
   Therefore it is **not possible to fix this solely by adding two field reads
   in the app logger**. A narrow observation DTO projection is necessary.

Neither finding demonstrates that the original cloud expiry and the different
local first-attempt failures share a root cause. Missing historical telemetry
cannot be reconstructed by changing a later candidate.

## Necessary implementation only

* Add `ReasonSystemBusy -> "system_busy"` to the app classifier and add exactly
  `"system_busy"` to the existing metrics reason allowlist. Preserve all existing
  unknown-input normalization, source labels, error-class labels and counters.
* Add two documented `uint64` scalar fields, `MessageID` and `MessageSeq`, to
  `runtimedelivery.PlanTerminalEvent`. Populate both from the same immutable
  `plan.Event` in the existing single deferred `runPlan` terminal callback.
  This covers success, ordinary failure, cancellation and recovered panic at one
  owner, without adding another callback or retaining an envelope/payload slice.
* On the existing non-OK warning only, add
  `wklog.Uint64("messageID", event.MessageID)` and
  `wklog.MessageSeq(event.MessageSeq)`. These use existing structured-log key
  conventions. Do not change the public helper `wklog.MessageID`: it accepts
  `int64`, whereas the canonical envelope uses `uint64`; a cast could corrupt
  values above MaxInt64. Do not add snake_case aliases or duplicate fields.
* Do not add channel, sender, UID, session, client message number, payload,
  publication metadata, raw envelope, route arrays, trace spans, or per-message
  metric labels. The pre-existing representative UID remains an existing
  private-log field; these two numeric additions do not make raw logs public-safe.
* Add one concise operator-facing CHANGELOG entry and, if useful, one concise
  PROJECT_KNOWLEDGE note documenting busy classification and body-free exact
  terminal identities. No FLOW boundary, ownership or lifecycle change is
  proposed; only update FLOW if the actual implementation makes it inaccurate.

## Failures to write down and test before implementation

The change is a deterministic observer projection. The existing narrow seams
are appropriate; a new pressure E2E is unnecessary to prove a classifier or
structured-field mapping. Keep evidence of tests failing on the frozen source
before production edits. Future pressure reproduction and GA evidence remain
separate obligations.

| Seam | Failing / regression cases | Existing test location |
| --- | --- | --- |
| App reason classifier | Busy maps to `system_busy`; all current mappings stay stable; 255 and an unrecognized value stay `unknown` | `internal/app/composition_classification_contract_test.go`, existing bounded taxonomy table |
| Actual app-to-Prometheus observer | Feed `gatewayadapter.SendackEvent{Reason: ReasonSystemBusy, Source: "batch_result_error", ErrorClass: "route_not_ready"}`; gather exactly one `wukongim_gateway_sendacks_total` busy sample with the original source/class; preserve success/error/unknown totals; no identity labels | `internal/app/observability_test.go` using `gatewayMetricsObserver` and existing `requireAppMetricFamily` / `findAppMetricByLabels` helpers |
| Metrics normalization independently | Direct `Gateway.Sendack("system_busy",...)` remains busy; arbitrary identity-like reason/source/class still collapse to unknown; only fixed label names appear | `pkg/metrics/registry_test.go`, existing `TestGatewayMetricsTrackConnectionAndTraffic` pattern |
| Runtime terminal projection | Use different MessageID and MessageSeq so accidentally copying ID into both fails; verify exact scalar identity once for success, immediate presence failure, pre-canceled context, owner-push failure/retry exhaustion and recovered panic; check original result/phase/error/representative target unchanged | `internal/runtime/delivery/runtime_test.go`, synchronous `runtime.runPlan` with existing recording observer/fake ports; extend existing bounded target and panic coverage rather than copying runtime logic |
| App failed warning | Synthetic terminal events for presence, owner_push, context and panic produce exactly one existing warning with original fields plus `messageID`/`messageSeq` as Uint64 fields. Non-OK transient mode may have sequence zero: preserve zero, never invent persistence. OK produces no failure warning. Nil observer / nil app / nil logger remain safe | `internal/app/online_delivery_test.go`, existing `recordingAppLogger`, `appLogEntriesForEvent`, `requireAppLogField` helpers |
| Exactness / no identity leakage into metrics | Include IDs above 2^53 and MaxInt64, distinct sequence and uint64 zero boundary. Keep exact uint64 through DTO and log field; inspect fields rather than `float64` JSON decoding. Gather delivery metrics after two different IDs and verify same series/count partition with no ID labels, UID or payload | Same runtime and app observer tests; existing metrics gather helpers |

New runtime cases should execute `runPlan` synchronously with immediate fake
ports and no real network, builds, sleep, process, or elapsed-time simulation.
For canceled/deadline result coverage, use an already canceled/expired context,
not a real timeout. Use RetryMaxAttempts=1 for newly introduced owner-exhaustion
cases so they do not wait through backoff. Do not replicate pre-existing timer
integration behavior in a new fast test. Existing runtime retry/panic cases can
gain identity assertions without changing their original execution semantics.

The initial runtime/app identity RED can be a compile failure because the DTO
fields do not exist yet; explicitly retain that fact rather than claiming a
runtime assertion failed. The reason-classification tests should fail assertions
against current source. After implementation, run directly related tests and
required repository gates in a separate quiet interval, followed by independent
Standards and Spec review. No tests have been executed in this review.

## Privacy, capacity and reporting constraints

* Root AGENTS requires bounded observability and evidence-led diagnosis.
  `internal/app/FLOW.md` forbids runtime identity labels;
  `internal/runtime/delivery/FLOW.md` explicitly says identity samples never
  become metric labels. Existing `PlanFailureSample` is at most one target and
  recipient, not an unbounded collection.
* `runPlan` emits exactly one terminal observation per accepted plan and isolates
  observer panic. Preserve this path. The proposal adds two scalar values
  (16 bytes of field data in the DTO; actual struct layout is compiler-defined),
  no map, slice, goroutine, registry, cache or persistent state.
* The existing non-OK warning is emitted once per failed plan, not per recipient.
  Add only the two scalar fields to that record. Do not add success logs or
  additional records. With two maximum-width decimal uint64 JSON values, the
  incremental compact JSON body is at most 67 bytes per existing warning
  (`messageID` and `messageSeq` keys, delimiters and two 20-digit values).
  Actual console formatting can differ; do not present this bound as a measured
  end-to-end log-byte rate.
* Existing logger routes warnings into app.log and warn.log, with configured
  rotation. Defaults are 100 MB per rolling file, ten backups and 30 days;
  these are retention bounds, not a warning frequency bound. `zap.go` contains
  no added sampler on this path. Do not claim the current warning stream is
  globally rate-limited or suppress/retry failure logs as part of this change.
* This proposal does not add a reason for retaining broader raw logs. Current
  raw warnings contain a representative UID and arbitrary original error text;
  retain private originals and publish only finite allowlisted derivations.
* Top's SENDACK observer only counts success versus error. App classification
  gains a fixed busy string, but Top totals and threshold logic must stay
  unchanged. No Top reason-series expansion is needed.
* The metrics normalizer already folds several named business reasons to
  unknown. That is a separate pre-existing taxonomy question; do not widen this
  minimal busy fix into a redesign without evidence and an explicit scope.

## Acceptance and remaining diagnosis

Accept the observability patch only when the documented REDs become GREEN,
existing mapping/unknown/privacy contracts remain GREEN, source diffs show no
permission/delivery/timeout/retry policy changes, and independent reviews have no
open finding. This would permit a later candidate to retain exact joins and busy
counts. It would not repair message loss, certify full workload performance,
prove historical joins, or satisfy the missing cloud long-run release gates.
