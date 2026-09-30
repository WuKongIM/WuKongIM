# Permission request timeline and tail repair

Authorized scope: bounded request-correlated diagnostics for Issue #977, one
fixed old/new diagnosis, evidence-supported repair and the original three-pair
non-regression acceptance. PRs 982 and 983 enter review independently; no merge,
release, paid resources, protected workflow change or manual failed-CI retry.

## Failure modes and predictions before implementation

1. Gateway admission/mailbox wait dominates: the existing async-dispatch span
   covers the long client interval before message preparation starts.
2. Permission collection/execution dominates: a per-request permission span
   covers the long interval after dispatch, before prepared append admission.
3. Prepared append waits behind overlapping Channel jobs: permission completes
   early and Channel append starts late. Individual durable stages alone cannot
   expose this wait; compare the gateway, permission and append boundaries.
4. Client enqueue, socket writer, decoding or bridge delay dominates: preserve
   existing pkg/client pending/write/decode timestamps and compare them with
   node-local events. A successful socket write is not server receipt.
5. Evidence can be missing, sampled, overwritten, miscorrelated or truncated.
   Keep every queried node and absent event explicitly; reject foreign client
   numbers, exposed sender/payload data, oversized replies and invalid timing.
   Nested spans and batch/range events must never be summed as disjoint waits.

## Fixed experiment

Reuse clean frozen products 424d03eb298b972ec572261d617972eecb5523c5 and
d1deec2b3bc916764699ad9fcf6b9a590c183d10. Keep the existing 32 sessions,
two 32-SEND waves, 256 Hash
Slots, 12 physical Slots, one metadata voter, three message replicas and
GOMAXPROCS=4. Diagnose the same remote Slot only, old then new, no CPU profile
or additional in-window scrape. Diagnostics are enabled identically with an
8192-event buffer, fixed retention/deep sampling and existing public HTTP
queries only after the measured window. Retain all 64 burst request timelines
from each of three nodes, capped at 32 events/64 KiB per query.

Write the timing and stage-coverage checks before changing their production
seams. The initial original-product run must preserve missing permission spans
as a coverage failure, while retaining ACK/policy/exact-history observations.
If another existing span already distinguishes the cause, do not add a duplicate
instrumentation service. New retained diagnostics use the existing bounded sink
and controls, never request/UID/Channel metric labels.

Diagnosis timings are not qualification. After a justified fix, run three
preselected unprofiled old/new pairs with the same original inputs and retain
all outcomes; enforce at most 5% p99/CPU regression without replacing previous
failed receipts. Missing CPU integrals remain unverified. Run directly related
correctness, race and named checks, and obtain separate Standards/Spec review.
