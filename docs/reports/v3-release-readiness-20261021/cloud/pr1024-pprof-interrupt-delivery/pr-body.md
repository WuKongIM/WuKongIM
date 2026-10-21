Cloud RC failures could lose worker failure samples and terminal status, discard oversized metric results, or query a window after the workload had already stopped. This change retains bounded terminal diagnostics, partitions fixed metric queries under shared time/byte limits, and binds their window to the terminal report. Missing or partial evidence stays explicit and never changes the workload verdict.

The full regression also exposed a profile-helper cancellation race: a signal between starting curl and registering its PID could leave an HTTP request alive after the helper reported interruption. The helper now defers signal cleanup through complete request registration, then cancels and joins its owned requests. Empty cleanup arrays also work with Bash 3 nounset.

Validation:

- Terminal collection: 20 Python methods / 79 process scenarios passed locally, including red-first reproduction and independent boundary probes. Both review axes have no open findings. A fixture-only optimization preserves all cases and their semantic results.
- Profile cancellation: 9 deterministic process scenarios (before launch, first request registration, second-node registration × HUP/INT/TERM) failed before the fix and passed afterward. The first focused validation passed all 15 top-level tests / 38 PASS records; original tests and time limits are unchanged. Both independent review axes report no findings.
- Original head c91e37b4: Linux default/E2E/performance passed, but scripts hit the original 600-second outer timeout. That failure remains retained.
- Head 2cc562fa: Linux default/E2E/performance passed; collector tests passed and the full scripts command completed in 575.954 seconds, but the original profile-interrupt assertion failed because server 2 still had an active request. That first failure is retained and led to the deterministic repair above.
- Current head 44de9452: its first complete Linux candidate and performance checks are pending. No failed CI was rerun, no deadline or concurrency limit was relaxed, and this PR remains draft until those checks pass.

The cloud message-loss root cause remains unconfirmed. This is diagnostic retention and cancellation cleanup, not a claimed repair of that historical loss. No cloud resources, production keys, stable activation, or release publication are included. Product cmd/internal/pkg and dependency objects remain unchanged.
