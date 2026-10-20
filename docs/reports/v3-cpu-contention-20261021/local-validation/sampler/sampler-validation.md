# CPU sampler validation

Five new cpu_window_sampler*.go files only; no existing harness files edited by this subtask.

- Pure contract red: sampler-pure-red.log (undefined new parser/types).
- Isolated Linux integration compile red: sampler-integration-isolated-red-compile.log (undefined Linux API before implementation).
- Pure contract green: sampler-pure-isolated-green.log.
- Package default tier: `GOWORK=off go test ./test/e2e/message/conversation_qps -count=1` passed (sampler-default-suite.log).
- E2E compile plus pure contracts: `GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps -run '^TestCPUWindowSampler' -count=1` passed (sampler-e2e-compile.log); no product was started.
- Linux integration compilation of sampler + shared pure parser passed: sampler-linux-integration-compile.log. This is compilation only on macOS, not a Linux execution claim.
- Whole-package Linux e2e+integration compile status is in sampler-linux-compile.log; early attempts were blocked by concurrent placement test-first work.

Required real Linux preflight: `GOWORK=off go test -tags=integration ./test/e2e/message/conversation_qps -run '^TestCPUWindowSamplerLinuxPreflight$' -count=1 -timeout=30s -v`. It must report PASS, not skip. It starts and reaps one owned multi-thread test child, reads actual proc/cgroup/task state, exercises thread CPU clock and idempotent stop, then observes wrong-affinity invalidation.

The collector marks a thread disappearance as incomplete final-interval coverage and invalidates the window while retaining both lifecycle and raw evidence. Matching-thread deltas are not full process off-CPU time. sched_schedstats=0 is never used to invalidate /task/TID/schedstat. No metrics/HTTP/pprof or JSON writes run inside the sampled load; only procfs/cgroup reads and bounded in-memory receipt updates occur.

## Final review follow-up

- `sampler-capability-red.log` and `sampler-budget-lifecycle-red.log` retain the
  first failing capability, exact JSON escaping, and lifecycle coverage tests.
- `sampler-followup-default-green.log`: default scenario package tests pass.
- `sampler-followup-e2e-green.log`: sampler pure tests with the E2E package pass.
- `sampler-followup-linux-compile.log`: complete Linux AMD64 e2e+integration
  package cross-compiles; this does not claim real Linux preflight execution.
- `git diff --check` passes.
- The Linux preflight now requires runtime or timeslice delta progress for at
  least two distinct matched child thread lifetimes. It does not require wait
  growth and does not change kernel settings.
- The 64 MiB receipt bound remains. Raw string charges now count exact JSON
  escaping without marshaling in the load window; conservative structure and
  error-path margin remain. A normal 55-thread/60-point capacity contract passes.
- A prior thread absent from both current task inventories is retained as an
  exited/unobserved lifetime coverage gap. It does not invalidate controls on
  current threads. Reuse, in-read disappearance, missing files, wrong masks,
  missed points and resource limits still invalidate the receipt.
- Root's final checkpoint callback retains the sampled window if after-window
  metrics fail; successful windows are appended once and profile windows clear
  both callbacks. No further integration blocker was found in the reviewed scope.
