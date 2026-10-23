# Width16 local experiment candidate

Exact head `4cb45b3409b155ca6a0eeb3d72d1b05bbee817ac`, tree `1f2826553a264053e954229ac2581bcf48afe6ad`, base9e5d8eea. The worktree is clean. No push, PR, merge, workload, cloud action or profile was started.

Production changes are limited to16 recovery cohorts, the derived8192-key limit, a matching16-round owner-read gate, and an explicit independent128MiB owned-UID-clone budget. The32MiB/512UID/256target page bounds, owner250ms deadline, delivery5s plan budget, retries, cancellation, identity/fences, Directory rereads, parser, reports and gates remain unchanged. The byte ledger is not a process RSS cap.

Four frozen fixtures first produced real behavioral RED on oldC4; the auxiliary128MiB/32MiB guard passed. With the minimal candidate, the same four commands passed:16 groups and8192 keys enter, the seventeenth waits/cancels, adapter permits recover after cancellation, and the128MiB ledger blocks another byte with free cohorts and wakes on release. The heavy fixture ran once GREEN without race. Two-package default normal/race and bounded related integration normal/race all passed. All test hashes stayed byte-identical to the tests-first pins.

The first go-format check passed. The first flow-doc-contracts check failed because the canonical index was stale; that result is retained. After an explicitly approved canonical render (only the cluster FLOW line-count row changed), the original policy flow-doc-contracts check passed:88 compliant,0 invalid,20 preexisting line-count warnings. No already-passing Go or format command was rerun. All recorded command PGIDs exited; no outer/output cap fired.

This candidate tests the width hypothesis. It does not establish that width caused or fixes the observed message_loss. Parent owns independent reviews, exact builds, controlled local A/B admission and release decisions.
