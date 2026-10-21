# Replication saturation failure inventory (before edits)

Observed CI: TestRuntimeRetainsEveryFollowerRepairWhenExecutionQueueIsSaturated, runtime_test.go:415, retained repairs = 64, want 66; duration 2.08 s.

1. H1: single-item-only test router fault injection lets legal multi-item batches through; expected 66 distinct failed channels were never established. Prediction: a forced two-item batch bypasses configured faults; missing repair keys correspond to durable follower records.
2. H2: delayed asynchronous completions leave fewer recorded repairs at the observation deadline. Prediction: fault tokens/exchange completions remain outstanding and all exact keys arrive later without product changes.
3. H3: the runtime repair ledger loses valid repair evidence through coalescing, capacity or completion fencing. Prediction: all 66 exact channel/follower requests were rejected and remain missing on follower, yet ledger loses keys while repair execution is blocked.

Repair requirements: preserve 66 distinct channel/follower repair obligations and eventual durable follower catchup; no relaxed expected count, deadline-only extension, dropped assertion, or retry-to-green. Test fault injection must be independent of legal batch partitioning, conserve per-item tokens/results, preserve mixed non-replicate items and non-target requests, and remain race safe. Product changes require evidence of H3; fixture-only changes must leave product tree unchanged. Existing realistic-time test is retained; any new pure router contract uses in-memory stores without real sleeps or external processes.

Commands use GOWORK=off and independent GOCACHE under this evidence directory with -p=2. Initial disk availability: approximately 9.4 GiB. No commit/push/dispatch. All local outcomes are retained.
