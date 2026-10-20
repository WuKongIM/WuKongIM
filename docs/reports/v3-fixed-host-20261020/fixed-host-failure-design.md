# Fixed-host MixedAttribution failure design

Scope: only `WK_E2E_CONVERSATION_FIXED_HOST=1` in MixedAttribution. No product,
workflow, profile or release-gate changes. Existing three 60-second windows,
warmups, HTTP limits, assertions, independent profiles and 12-minute deadline
remain intact.

Approved test seams: pure receipt/clock/procfs/cgroup/config contracts exercised
with literal input; E2E compilation. The actual Linux process experiment is
owned by root and is not executed by this subtask.

Failure cases written before implementation:

1. Preparation ends beyond the first-product-start + 180-second deadline:
   retain a failed fixed-control receipt and do not measure or extend the age.
2. Proc stat command names contain spaces or parentheses: read field 22 from
   the suffix after the final `)`, not whitespace token 22.
3. Missing/malformed starttime, affinity, uptime or nonpositive CLK_TCK:
   reject the fixed control; never synthesize zero or assume a tick rate.
4. Uptime/starttime conversion would imply a future process or nonfinite age:
   reject. Bind the estimate to the local monotonic read bracket and retain
   one uptime precision unit, one process-clock tick and read-bracket uncertainty.
5. Nested cgroup-v2 mount roots and escaped mount paths: resolve the process's
   actual unified membership relative to the matching mount root. Missing,
   inconsistent or traversal paths fail instead of reading an unrelated root.
6. Cgroup controls unavailable: preserve explicit per-file errors and resolved
   locations, never treat absence as unlimited/zero. Record visible ancestors;
   namespace visibility is not proof about inaccessible host ancestors.
7. Config cannot parse or contains secret-like keys at any nesting level:
   no raw config falls back into the receipt. Keep path/hash/error and redact
   sensitive leaves before TOML encoding.
8. Metric observations fail: retain a failed/partial boundary receipt before
   refusing control. Keep GC/storage/compaction series with original labels
   and the scrape's own SHA/time receipt; no periodic scraping is introduced.
9. Startup/readiness/prepare/eviction aborts: deferred stage completion and
   report save preserve elapsed stages and fixed failure state.
10. Wrong host/driver resources, missing prebuilt product/driver identity,
    procfs permission problems: fixed mode refuses explicitly. Normal mode
    does not acquire these dependencies or add any wait/scrape.
11. The 180-second release point is not the first request: existing per-window
    warmup and boundary scrapes follow it. Record actual window start ages and
    keep all rejected windows and independent profiles.
