# CPU window sampler failure inventory (before implementation)

Approved seams: pure proc/stat/schedstat parsing, identity-paired counter differencing and bounded schedule decisions; Linux integration through real multi-thread child and the start/stop API. No product internals or load.

- Reject invalid PID/starttime/affinity, duplicate PIDs, >4 processes, unsafe/too many cgroup paths, zero/overlong window.
- Proc comm may contain spaces/parentheses; reject missing fields, negative/overflow numeric fields, wrong PID, truncated/malformed schedstat; do not misclassify sched_schedstats=0.
- Identity includes process and TID starttime; stat→schedstat→stat must agree. Never subtract reused TID or regressed counters. New/vanished threads retain coverage gaps, not synthetic zero.
- Every observed thread and process affinity must equal its supplied expected mask; mismatch invalidates the receipt.
- Sample actual monotonic times; skip missed seconds rather than catch up. Record own locked-thread CPU, read span/bytes, missing files/errors, coverage counts and lifecycle.
- Retain explicit invalid evidence at >256 threads/process, >70 points, >64MiB receipt bound. Stop at 75 seconds; cancellation joins once, and repeated stop returns the same completed result.
- Four known processes only, cgroup ancestor counters deduplicated; no HTTP, metrics scrape, profiles, external commands or proc environment/cmdline reads.
- Integration uses same executable as a real multi-thread child, checks clock progress, thread coverage, correct affinity/identity, invalid affinity behavior and completed join. Non-Linux stub is an explicit unsupported error; Linux integration must execute rather than skip.

## Follow-up capability contract (before implementation)

The Linux preflight can falsely accept parseable but nonfunctional schedstat
counters if it only counts valid thread files. The control must observe runtime
or timeslice progress on at least two distinct matched thread lifetimes; wait
need not grow. All-zero observations, wait-only changes, new threads without a
matched interval, invalid observations, and repeated positive intervals from the
same lifetime must not satisfy that capability. Production window zero values
remain raw observed values and are not rewritten or inferred from sysctl state.

## Follow-up resource and lifecycle contracts (before implementation)

A blanket six-byte JSON charge per raw ASCII byte can exhaust the 64 MiB
receipt budget during a normal 60-second run with about 55 observed threads.
Retain the same hard limit but count the JSON string escaped width without
serializing in the load window. Quotes, controls, HTML escapes, invalid UTF-8,
and U+2028/U+2029 must never be undercounted.

A prior valid thread may naturally exit between samples. If it is absent from
both current directory inventories, retain the lifetime as a coverage gap but
do not invalidate identity/affinity control for currently observed threads.
A reused TID, an unreadable still-present thread, or a racing disappearance
within this sample must still invalidate the sample. No total thread wait may
be reported as complete across a retained exit gap.
