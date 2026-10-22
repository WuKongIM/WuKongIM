# Proposed bounded metadata-checkpoint leaf diagnostics

Status: design only, not approved production implementation. No production file was edited. The complete failure matrix below precedes any tests for the proposed implementation. The earlier failure-matrix.md governed the separate completed structural replay.

## Evidence boundary

The retained d349 run proves only that Finalize reached ProductionMetaController.Checkpoint and returned a non-product error while the earlier frozen verdict was product_failure/message_loss/terminal=true. Finalize correctly retained that observation without certifying an incomplete report. No complete actual terminal WorkerSnapshot/MetaCreate vectors or exact failed three-source scrape round is retained. Nearby first-measured and terminal scrapes pass a real parser/accounting/controller replay only with explicitly synthetic empty expected vectors and worker shapes. They cannot identify the actual leaf or prove a metrics-fetch transient, worker-shape error, or vector-bound violation.

## Complete existing failure matrix and proposed closed reason mapping

All proposed leaf records retain stage=meta_checkpoint. Existing reason=invalid remains the fallback for injected/custom implementations and errors without a recognized envelope. No counts, vectors, worker IDs, source endpoints, or arbitrary error text are added.

| Existing branch | Proposed reason | Semantics preserved / test requirement |
| --- | --- | --- |
| nil controller or nil context | guard | Same ErrLifecycleHarnessInvalid; no metrics or accounting call |
| ctx.Err at entry, source collection, or settle wait | context_canceled or context_deadline | Same errors.Is context sentinel; never change deadline, retry or baseline |
| expectation run/config invalid (empty configured run, workerCount not3, groupLimit0), wrong snapshot count, wrong WorkerCount, out-of-range/duplicate/missing WorkerID, zero sequence or phase outside running/final | expectation_worker_shape | Same invalid sentinel; fail before any source fetch |
| worker RunID mismatch, empty AssignmentID, zero Generation, or disagreement between workers' assignment/generation | expectation_fence | Same invalid sentinel; never print identifiers/fence values |
| checked person/group per-hash aggregation overflow, per-worker group sum overflow, global group sum overflow or groupTotal over configured groupLimit | expectation_vector_bound | Same invalid sentinel; no synthetic truncation or count inference |
| initialized controller sees changed assignment/generation | checkpoint_fence | Same invalid sentinel; old baseline unchanged |
| initialized controller sees sequence not strictly greater | checkpoint_sequence | Same invalid sentinel; old baseline unchanged |
| initialized controller sees any per-worker person/group vector regression | checkpoint_vector_regression | Same invalid sentinel; old baseline unchanged |
| any of exactly3 joined source calls fails while context remains live | metrics_fetch | Same ErrLifecycleHarnessInvalid returned today; no node/URL/error-text field and no new retry |
| preview accounting rejects assignment fold, result presence/exactness, sums, scalar/slot consistency, or other invalid evidence | metrics_preview | Same error identity; preview never commits the real accounting |
| deficit preview with nil settle callback, failed non-context settle callback, or defensive exhausted-loop path | settle_wait | Same invalid/original callback error identity; 5 attempts and25ms waits unchanged |
| final real accounting rejects evidence, baseline regression or first-checkpoint reheat | accounting | Same error identity; malformed evidence never commits |
| preview product deficit while attempts remain | no new failure record | Existing bounded rescrape only; no additional attempts |
| stable deficit/error counter or sticky accounting product failure | no new failure classification | Preserve ErrLifecycleProductFailure and current metaProduct behavior |
| success | no record | Commit controller baseline exactly as before |

The preview/accounting groups intentionally stop at these existing reducer boundaries. They are sufficient to distinguish actual controller/worker/fetch/reducer stages without duplicating reducer logic. If a later valid run identifies one of those groups, that exact group can receive a separate tests-first diagnosis; this design does not guess which one failed.

## Smallest implementation seam proposed for approval

Reuse the existing package-private finalizationError, constant Error text, Unwrap cause chain, newFinalizationError and FinalizationFailure.Bounded allowlist. Add only the named meta_checkpoint reasons to Bounded. Wrap the existing error at its exact return boundary in production_meta.go; do not alter public interfaces, report schemas, reducer arithmetic or mutable state. Split compound expectation guards only where the existing guard combines shape and fence, retaining short-circuit order (range checks must precede array indexing). A tiny private wrapper may map only context.Canceled/context.DeadlineExceeded and otherwise keep the supplied closed leaf reason. Avoid a new diagnostic type, generic telemetry framework, snapshot serialization or error-string parsing.

MetaCreateAccounting itself need not change: the surrounding preview and final accounting call sites identify the relevant boundary. Existing collectProductionMetaMetrics semantics still join all3 calls before returning; wrapping must not early-return on the first failure. Do not recover private source details currently discarded by that collector. A typed arbitrary/custom Meta error still falls back to meta_checkpoint/invalid. Unknown stage/reason values still project to controller_unknown/invalid.

FinalizationFailure remains a diagnostic only. Finalize and CLI must still return harness_invalid/invalid_observation and omit final.json for incomplete final evidence, while preserving the frozen product observation. Qualification behavior, verdict precedence, completeness, retry counts, deadlines, metrics request count, accounting updates, report validation and exit mapping remain unchanged.

## Tests-first implementation plan (not yet written/run)

1. Table-driven private package tests for every branch above, with source call count and accounting/controller baseline snapshots before/after. Write these before editing production. Cover shape/fence split ordering with invalid worker IDs to prove no panic.
2. Focused context canceled/deadline and failed source/settle cases prove errors.Is(original) survives, all3 source calls join, bounds remain5 attempts/25ms and rejected rounds do not advance baselines. Use immediate fake callbacks rather than realistic sleeps.
3. Preview invalid versus final accounting invalid need distinct fixtures: malformed source counters for preview; previously initialized real reducer plus non-regressing controller vectors but regressing metric counters or initial-reheat for accounting. Product deficit remains product evidence, never a diagnostic harness reclassification.
4. Finalize with each known envelope preserves the exact closed stage/reason plus frozen product observation; diagnostic JSON and error string contain none of sentinel private UID/credential/path markers; errors.Is/As retain the original sentinel. Unknown/forged values collapse to existing unknown fallback.
5. Reuse existing CLI/hook test to prove observed product decision plus incomplete finalization keeps invalid_observation and no final report; no gate loosening or swallowing. Run only focused tests after root authorizes implementation and its test window; this completed replay is not an authorization for another invocation.

No live probe, new load, server binary, cloud resource, complete snapshot dump or service behavior change is proposed. This is a harness diagnostic gap; it is not a message-loss fix.
