---
status: superseded
superseded_by: 0037-run-codex-locally-with-an-encrypted-analysis-session
---

# Bound each Analysis Access Window

Each diagnosis job has a 45-minute hard timeout, its network access window may remain open for at most 50 minutes, and its non-renewable Analysis Token expires at the earlier of 45 minutes after issue or five minutes before the Run Lease. An Analysis Run refuses to start with less than 30 minutes remaining. Normal completion, failure, cancellation, and timeout all execute unconditional ingress cleanup, with the scheduled sweeper as a second layer. Further investigation requires a separately triggered Analysis Run with a fresh identity, token, access window, and Diagnostic Budget; remediation never extends the live-access interval.
