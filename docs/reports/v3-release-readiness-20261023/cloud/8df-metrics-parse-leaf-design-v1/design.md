# Typed metrics parser leaf diagnostic — design only

The source is remote-main8dfcd35b7d604fb5d3da3d75e36ab267e2f4b1bf, tree582e5bc024147b32aa68074d04c041391af89b96, identical to reviewed56. Both applicable valid FLOW files were read directly and their hashes are frozen. No production code/test/worktree was created here. Implementation must wait for root review and begin with the complete failure/control matrix.

The unique offline six-cut replay passed all nearby retained bodies. The actual failed HTTP response is unavailable, so its parse leaf remains unknown. This addition is intended to identify a future real parser failure safely; it is neither message_loss repair nor evidence that GaugeVec/Gather consistency caused the existing failure.

## Minimum mechanism

Add one small target-owned private parser-error implementation and a public closed MetricsParseReason vocabulary. All20 fixed parser errors.New families in reason-catalog.json return the genuine private type; its Error() returns the exact existing immutable text. One catalog may map the private enum to that text to avoid drift. No raw sample, labels, metric identity, endpoint or arbitrary message is added. Existing acquisition metricsError still has the same closed Error(), class=parse and original Unwrap chain. MetricsFailure behavior is unchanged.

MetricsParseFailure(err) uses errors.As to the private concrete type; no Error string comparison, substring inference or foreign interface method is accepted. Nil means absent; unknown/untyped/typed-nil/invalid code yields closed unknown when used for a new parse diagnostic. A forged errors.New with an exact old text remains unknown. Wrapped genuine causes remain identifiable. Actual Client.Metrics has one parser leaf; errors.As follows its existing chain and does not create source ordering or verdict authority.

Every existing parser condition and return order stays unchanged. Shared literals can share one reason: token/line limit, invalid labels/sample and aggregate overflow may be guard groups. invalid_queue_utilization deliberately keeps the current missing-pair-key or positive-capacity-overflow group and both queue families; do not split/change that condition prematurely. Defensive helpers not normally reachable through Metrics receive catalog/pure-helper coverage, explicitly without pretending a real HTTP branch was executed.

## Optional three-source wire projection

Retain metrics_fetch.source_mask and kinds exactly. Add optional parse_reasons containing exactly3 closed cells in declared observation.service_nodes order; use an optional pointer/fixed array representation so legacy absent and all-nonparse records omit the field. A new collection sets a reason only when that position's existing kind is parse. Genuine private leaf yields its enum; a new parse error lacking it yields unknown. Successful/nonparse cells are empty. All sources still join exactly once; parent context still wins before/after join.

Legacy records lacking the new field must retain absence even with kind=parse; do not invent historical unknown. New malformed optional vectors (wrong shape/length/cell type) are ignored independently, leaving valid existing mask/kinds/stage/outcome intact. Prefer a small field-specific bounded decoder with at most3 string cells, not a dynamic/unbounded diagnostic slice or a general decoder rewrite. Unknown strings become unknown only at parse positions; stray cells at nonparse positions are cleared. A valid bounded projection copies the array rather than aliasing caller memory. Writer output is always exact3; old supported unknown-field-tolerant readers ignore the addition.

Existing mask/kind invalidity still removes metrics_fetch; new reasons cannot rescue it. Stage projection remains only meta_checkpoint/metrics_fetch. Parent-context branches and unrelated stages omit it. productionMetaMetricsFailure retains its private causes and old harness-only Unwrap sentinel; never join child causes into that chain. Reason fields never drive retry, settle, snapshot preview, accounting, observed verdict, final report validity or CLI exit.

## Fixed contracts

Keep line/series/queue/label bounds100000/32768/128/32, current Scanner64KiB behavior, label key/value64/1024, HTTP8MiB, canonical metadata Slots1..12 and exact nonnegative counters<=2^53. Preserve float and metadata aggregate guards, unsupported-family filtering, partial metadata behavior, separate ValidateRequired, queue cap0 denominator semantics and existing paired-family conditions. Keep HTTP address fallback, deadlines/limits, acquisition kinds, retries, settle windows, masks, source order and all verdict logic untouched.

Intended implementation surfaces are target parser/error vocabulary, the existing chatlifecycle metrics-fetch diagnostic type/collection and tests, plus concise Unreleased Changelog and the two applicable FLOW updates if their descriptions become inaccurate. No pkg/metrics collector change, workload change, failed-body persistence, source-address/node identity or metric-label output is included. Pure target tests use in-memory HTTP; existing three-source/CLI tests pin precedence and exit/no-final behavior. No new live run is authorized by this design.

The complete matrix covers20 leaf families plus provenance/unknown/forged, masks and vectors, parent cancellation/deadline, stage guards, old/new reader compatibility, unchanged acquisition/settle/accounting/verdict and output privacy. Failure cases and controls must be written first; after root approval only the directly relevant checks and required named FLOW check should run. Passing synthetic tests will validate this diagnostic contract, not reconstruct the missing original response or explain the expired message.
