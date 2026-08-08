---
status: accepted
---

# Retain one Lease across deployment repair

After chat-lifecycle procurement returns a valid active Lease Receipt, a
deployment or pre-clock readiness failure will not release those hosts merely
to buy replacements. The top-level orchestrator retains the exact Lease and
waits for a distinct protected-`main` control revision, then re-invokes the
dedicated Deployment Action with the same immutable Lease Receipt, bundle, and
sealed per-Lease SSH identity. The measured workload clock still starts only
after the complete readiness gate.

The repair window remains bounded by operator stop, the aggregate CNY 1,350
operational stop, the rehearsal's 12-hour immutable AutoRelease ceiling (96
hours for formal), and the time required for deployment, readiness, measured
execution, and release reserve. Each control revision is
attempted at most once. If repair requires changing the product source or
content-addressed bundle, the current Lease is released to authenticated zero
inventory and that paid run ends; a new Lease requires a new explicit start.
Deployment retains no cloud lifecycle permission, and workflow failure still
prefers exact Release with AutoRelease and the scheduled sweeper as backstops.
The longer rehearsal expiry is a safety ceiling, not a planned hold: PostPaid
hosts are released immediately after success or terminal failure.
