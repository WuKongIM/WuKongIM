# V3 cancellation failure matrix (before implementation)

- Helper root exits on TERM while a registered, pre-TERM observed child ignores TERM: retain child ownership before root exits; KILL and confirm it is absent/non-running inside one shared 2+1 second budget.
- Helper root already exited abnormally before cancellation with an orphan child: lineage cannot be recovered reliably; explicitly mark descendant cleanup unconfirmed and partial, depend on enclosing whole-PGID cleanup, never equate direct-helper wait with all-child cleanup.
- Three helper roots and their children ignore TERM: one shared grace and KILL/join budget, never three serial budgets. The actual synthetic program and invocation remain in this scope.
- Snapshot/ps failure or budget expiry: closed unconfirmed result; no blind process-wide kill outside the registered helper descendants and owner PGID.
- V2 once-status safety, existing normal/stale/source/Slot cases remain valid; V1 and V2 files stay immutable; helper remains original V1 bytes.
