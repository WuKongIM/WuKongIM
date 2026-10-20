# Migration restart repair failure inventory (before implementation)

The original black-box failure remains in docs/reports/v3-rc-20261020/logs/08-ordinary-history.jsonl.gz. Product b469 returned HTTP400 recovery-probe-incomplete on the first history read after full restart; no warming append is allowed.

A deterministic in-memory replica adapter test is necessary to hold the exact Install probe-round fault without production instrumentation or timing sleeps. It exercises real Install/recovery selection/state fencing and storage adapters, not just a new classifier helper. The process E2E remains the final acceptance.

Failures to catch before changing product code:
1. Successful frontier probes followed by loss of both remote identity-page responses leak private probe-incomplete without public ErrNotReady.
2. Loss of both remote frontier responses leaks private quorum-unavailable without public ErrNotReady.
3. Any failed installation grants a frontier/readiness or allows a business Commit, mutates the acknowledged records, or causes a later successful install to discard/duplicate them.
4. Invalid recovery proof/log conflict is incorrectly made retryable; invalid authority or caller cancellation acquires ErrNotReady.
5. After temporary faults clear, the same durable state cannot recover exact original command/content and append at the next sequence without a warming write.
6. Local error identity is corrected but the actual forwarded read API still emits HTTP400; full multi-ingress migration E2E must verify recovery under the documented strict HTTP503 envelope and unchanged 20s/100ms budget.
7. Any permanent/unknown HTTP error is hidden by broad fixture retry, or a retry budget increase masks a persistent recovery failure.

Ranked hypotheses and predictions are in the original migration-diagnosis.json and were restated to the user before this experiment. No performance experiment starts until migration regressions are complete.
