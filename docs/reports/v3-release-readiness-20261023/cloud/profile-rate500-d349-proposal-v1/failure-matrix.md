# Preparation failure matrix

- Wrong full revision/tree, worktree `.git` file, dirty source or changed assets: fixed build guard rejects before any product launch.
- Missing/altered build receipt or member, binary SHA or embedded compiler/platform/VCS mismatch: reject, do not rebuild or fall back to caches.
- Configuration drift or changed LoadConfig/validation/identity functions: stop preparation, do not reuse prior planned identity.
- Sampler helper/watcher drift: stop; retain old bounds and cancellation/join contract, no new request or retry.
- Old PGID/product process or occupied selected port: deny launch. Static snapshot expires; supervisor repeats checks before launch.
- Startup/barrier>360s, runtime>480s, evidence error, foreign workload: original stop behavior. Total600s, independent570s TERM then5s KILL remain.
- First-attempt/expiry failure, finalize8, partial/missing sampling: retain original gate and stage evidence; do not rerun, change timeout/capacity, or manufacture final verdict.
- CPU sample can overlap SEND stop/drain; nominal+5s is local scheduling, not exact server offset. Native sharedMac diagnosis is not formal/Linux performance acceptance.

This is temporary fixed-source wiring, not a general tool or production change. Offline preflight does not start the load.
