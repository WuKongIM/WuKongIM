# Pprof cancellation: tests-first repair

Frozen source is 2cc562fa39f7b430c61a4494636f6ee0ec0d631a. The prior read-only diagnosis and first CI failure are sealed separately. Allowed repository scope is the threshold-pprof helper, its integration tests, CHANGELOG, and necessary PROJECT_KNOWLEDGE. No policy/workflow/Skill/FLOW/limit changes.

## Existing seam and failure matrix

Use the existing real helper/curl/Go HTTP server boundary, integration tagged. Preserve the original probabilistic interrupt regression and its original three-second helper/two-second request deadlines. Add six deterministic cases before the helper edit: first child and node2 first CPU child, each interrupted by HUP, INT, TERM after curl spawn and before PID registration. BASH_ENV DEBUG timing hooks live solely in each test's temporary fixture; no test branch is added to production.

Each case must establish the chosen HTTP request actually started, the signal boundary has the expected existing registered-PID count, helper exits successfully in the existing time bound with partial/interrupted metadata, all requests end, the actual spawned child is gone, and no .next file remains. Save the first artifact even on test failure. Test cleanup signals only its own isolated process group after recording the failed lifecycle, avoiding leaked requests from the deliberately broken original helper.

Failure modes: initial empty Bash3 arrays must not raise set-u errors; a signal between spawn and PID append must not orphan curl; a signal between PID and temp/metadata registration must not lose cleanup ownership; pending signal must be handled immediately after complete registration and before another request is started; all HUP/INT/TERM must preserve metadata and terminate/join exactly registered children; normal/context capture must retain existing profile validity, auth secrecy, byte/time bounds, and first-trigger idempotency.

## Minimal implementation after RED

Mark the launch plus all PID/tmp/metadata registration as one narrow signal-deferral region. The existing handler records pending signal while registration is in progress; immediately after registration it performs the existing bounded termination/join and metadata cleanup. Do not kill an unowned last-background PID or expand process selection. Make empty-array termination/temp cleanup safe on Bash3 before the first child is launched. No product server or collection threshold changes.

## Validation

First run only the new six-case regression on unchanged helper and preserve RED. After implementation, run the original related threshold pprof static+integration tests once, including original random interrupt and all new cases. Produce the repeatable JSON artifact through a non-product environment variable. Preserve every first result and source hash. Do not run a Mac full suite or CI; the next reviewed head's first original Linux named check remains required. Freeze final source for dual independent review before any commit/push.

The prelaunch empty-array path also has three prior HUP/INT/TERM RED cases (node0, before any curl). Total added cases: nine. The initial six and the subsequent three were run separately before modifying the helper, and both first failure receipts are preserved.
