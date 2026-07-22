# Local Medium revision-neutral microbenchmark fixtures

This directory contains the version-controlled seam for comparing the Cloud
Medium recipient hot path across revisions without copying candidate-only test
fixtures into the baseline.

- `workload_test.go.overlay` is the single common workload and equivalence
  contract. It fixes 256 physical hash slots, 10 logical Slot groups, 512
  recipients, 221 exact authority groups, 55 online routes, and 55 RECVACKs.
- `adapter_baseline_test.go.overlay` binds that workload to the baseline
  `RouteKeys` resolver and app-local owner pusher.
- `adapter_candidate_test.go.overlay` binds the same workload to the candidate
  scalar `RouteAuthoritiesPartial` resolver and infra owner pusher.
- `baseline-lock.json` freezes the released failed Cloud Medium run and exact
  source SHA used as the baseline. The lock is part of the candidate commit;
  build, run, and evaluation all reject a caller-selected alternate ancestor.
- `build-smoke.sh` compiles each adapter only against its declared revision in
  an isolated local clone, runs the same equivalence test in both binaries, and
  records the common workload hash plus separate adapter and binary hashes.
  The clones share immutable Git objects with the source repository; unlike
  linked worktrees, they let Go stamp exact revision/modified metadata into the
  test binaries. The script also records and hashes the Go binary selected from
  the exact candidate clone, canonical GOROOT, and the actual compile, link,
  and asm tools used by the build. Once
  selected, the build uses `GOTOOLCHAIN=local` so no different toolchain can be
  chosen underneath either revision. Because Go overlays only replace files
  already discovered by the package loader, the script creates two fixed empty
  test placeholders inside each isolated clone, compiles through the overlay,
  removes the placeholders, and then proves the clone is clean.
  The temporary clone root is canonicalized with `pwd -P` before overlay
  keys are generated so macOS `/var` -> `/private/var` aliases cannot bypass
  replacement.
  Placeholders are created with a host-specific no-dereference hard-link
  operation that rejects existing files and symlinks. The output directory is
  atomically reserved before isolated clones are created, so concurrent
  invocations cannot nest or overwrite a completed result. Failed or
  interrupted build/equivalence artifacts are retained at `OUTPUT_DIR.failed`.
  Long-running build and equivalence commands run in isolated process groups;
  signal cleanup terminates and reaps the whole group before moving evidence.
  Test compilation requires Go VCS stamping. The build hides only the two
  untracked placeholder files from Git status while stamping; the subsequent
  clean-worktree check and manifest-bound overlay hashes remain authoritative.
- `run-micro-abba.sh` consumes only that manifest and those two binaries. It
  runs the exact `A1/B1/B2/A2` timing order after a clean-host check before
  every leg and a minimum 120-second cooldown between legs.
- `evaluate-micro-abba.sh` independently revalidates commits, toolchain and
  binary hashes/provenance, benchmark names, raw output hashes, host evidence,
  sample counts, and cooldown timestamps before applying the checked-in jq
  policy.

The two common benchmark names are:

```text
BenchmarkLocalMediumRCRevisionNeutralAuthorityResolve512x256
BenchmarkLocalMediumRCRevisionNeutralOwnerPushAck512x221x55
```

Both benchmarks keep common equivalence summaries and conservation scans
outside the timed region. The timed authority case stores each revision's
native result as a sink. The timed owner-push case contains only plan
processing, result checks, and the 55 RECVACK mutations. Every timed iteration
must expose exactly 55 pending ACKs before RECVACK and zero afterwards, so a
revision that skips pending-ACK binding fails instead of appearing faster.
Write-count conservation is checked once after `StopTimer`.

Run the compile/equivalence gate after the harness itself is committed in the
candidate revision:

```bash
scripts/cloud-sim/local-medium-rc/build-smoke.sh \
  6e419a3cbd7b7ec203026ee7323a8dc09b4291fe CANDIDATE_SHA \
  /tmp/wukongim-local-medium-rc-smoke
```

The gate preflights Bash plus the external commands it uses: `awk`, `basename`,
`cmp`, `dirname`, `env`, Git, `grep`, jq, `ln`, `mkdir`, `mktemp`, `mv`,
`ps`, `rm`, `sleep`, `uname`, and either `sha256sum` or `shasum`. It also needs
a Go launcher. It disables any parent `go.work`, selects the exact `toolchain`
declared by the candidate `go.mod`, then pins that resolved toolchain for both
revisions. If `go` is not on `PATH`, pass its absolute path with
`WK_LOCAL_RC_GO=/path/to/go`. Temporary clones use `${TMPDIR}` when set and the
portable `/tmp` default otherwise.

This command never starts a server or a cloud workflow. It intentionally does
not perform timing. A release-candidate ABBA executor must consume the two
recorded binaries and hashes, keep the exact `A1/B1/B2/A2` order, apply clean
host and inter-leg idle gates, fail on external noise or excessive pair drift,
and preserve the raw benchmark output. A build-smoke PASS alone is never a
capacity GO and never authorizes Alibaba Cloud provisioning.

## Run the revision-neutral micro A-B-B-A gate

Timing is an explicit local action and requires a clean host. The default is
six 5-second samples for each of the two benchmarks in every leg, 4
`GOMAXPROCS`, at least 85% CPU idle before each leg, no unrelated process at or
above 20% CPU, and a 120-second inter-leg cooldown:

```bash
WK_LOCAL_RC_CONFIRM_MICRO=RUN_REVISION_NEUTRAL_MICRO_ABBA \
WK_LOCAL_RC_EXPECTED_BASELINE_SHA=6e419a3cbd7b7ec203026ee7323a8dc09b4291fe \
WK_LOCAL_RC_EXPECTED_CANDIDATE_SHA=FINAL_CANDIDATE_SHA \
scripts/cloud-sim/local-medium-rc/run-micro-abba.sh \
  /path/to/build-smoke /path/to/new-micro-output
```

Both expected revisions are mandatory full lowercase commit IDs. The expected
baseline must also match `baseline-lock.json`, whose bytes must match the exact
candidate commit. This prevents substituting an older, artificially slower
ancestor for the agreed baseline while still allowing the candidate to be the
exact merge commit rebuilt from `main` after PR merge.

`WK_LOCAL_RC_MICRO_BENCHTIME_SECONDS` may increase the per-sample duration but
cannot reduce it below 5 seconds. `WK_LOCAL_RC_MICRO_COUNT` may increase the
sample count but cannot reduce it below 6. Cooldown and host thresholds can
only be made stricter. The runner never kills a noisy process; it stops and
retains the raw evidence instead.

The build-smoke producer, common workload, both revision adapters, runner,
evaluator, jq policies, and static policy test must byte-match their versions
in the candidate commit recorded by build-smoke. The baseline lock is included
in the same contract. Their individual hashes and a bundle hash are written
into the run manifest and independently rechecked.
Both recorded equivalence outputs are hashed, checked for one exact semantic
marker, and rerun from the frozen binaries before timing and re-evaluation. If
the runner is interrupted, its trap sends `TERM` only when the current
benchmark PID is still its own child and still names the exact test binary; it
waits at most ten seconds after `TERM`, rechecks the exact captured child
identity before `KILL`, and never signals an unrelated process. Cooldown sleeps
use the same tracked-child cleanup, so an interrupt does not leave one behind.

The strict parser does not depend on `benchstat`: it reparses all `ns/op`,
`B/op`, and `allocs/op` samples with checked-in awk/jq logic and fails closed
if any required tool is unavailable. It requires per-leg CV and same-variant
pair drift at or below 5%, candidate authority `ns/op` at or below 55.74% of
baseline, and no more than 5% regression in the remaining timing/allocation
comparisons. Re-evaluate an existing completed run with:

```bash
scripts/cloud-sim/local-medium-rc/evaluate-micro-abba.sh \
  /path/to/build-smoke /path/to/micro-output
```

The same two `WK_LOCAL_RC_EXPECTED_*_SHA` values are required when invoking the
evaluator directly.

The evaluator deletes any prior verdict and writes a fail-closed sentinel before
validation. Its `ERR` and `EXIT` traps preserve a `micro_fail` result for any
unexpected exit, so stale success can never survive a failed re-evaluation.

The machine-readable result is `micro-verdict.json`. Its strongest successful
result is only `decision: "micro_pass"` with `cloud_authorized: false`; it is
one input to the later local three-node and review gates, never permission to
start a paid cloud run.
