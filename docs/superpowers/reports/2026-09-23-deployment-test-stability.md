# Deployment test stability — 2026-09-23

## Scope and source

This follow-up starts at `effc2fb9f` (shared deployment execution) on
`codex/unified-deployment`. It repairs two script integration fixtures before
merging the deployment refactor. Product readiness and package publication
behavior are unchanged. No Docker container, cloud host, or deployment is used
by the focused fixtures.

## Failure modes identified before repair

- Native diagnostics may arrive after the old 250 ms child-test deadline.
  Buffered or discarded diagnostics must still fail the test; an early child
  exit, a missing marker, or a missing injected deadline must never pass.
- EasySDK's command budget may expire while its test waits for parallel
  admission. A reaped server must still produce the exit diagnostic and its
  log; a command timeout must retain the command output for diagnosis.
- Fixture descendants must be bounded and cleaned up after injected failure.

## Reproduction and cause

The original focused tests passed 30 repetitions, and the EasySDK exit test
passed another 500 repetitions in isolation. Isolation therefore did not prove
that their full-suite failures were fixed.

**Native package diagnostics:** adding 350 ms of fake Docker startup delay to
the original fixture reliably produced:

```text
TestNativePackageRepositoryKeepsTimeoutOutput
  test timeout discarded container diagnostics:
  panic: test timed out after 250ms
```

The repaired fixture observes the diagnostic through the real package-test
output-forwarding path before signaling a child-process panic. This models the
abrupt termination caused by a package deadline while the shell is still
running. The Go test timer is now a watchdog, not the stimulus. The deliberate
350 ms startup delay stays in the fixture. An owned process group bounds and
cleans up the fake Docker command.

As a negative control, temporarily replacing the real test's
`io.MultiWriter(os.Stdout)` with `io.Discard` made the repaired fixture fail:

```text
fixture did not stream diagnostics before termination: context deadline exceeded
```

The mutation was restored. This proves that the test still catches lost
streaming diagnostics, rather than accepting any child-process failure.

**EasySDK admission:** `repoRoot(t)` calls the integration scheduler, which
invokes `t.Parallel()` for top-level tests. The exit test created its five-second
command context before calling `repoRoot(t)`. A child-suite fixture that holds
the serial phase for six seconds reproduced the exact original symptom:

```text
=== PAUSE TestEasySDKReadinessDetectsExitedServer
--- PASS: TestEasySDKReadinessSerialDelayFixture (6.00s)
=== CONT TestEasySDKReadinessDetectsExitedServer
    context deadline exceeded
--- FAIL: TestEasySDKReadinessDetectsExitedServer (0.01s)
```

Resolving the root and passing admission before starting the context fixes the
ordering without increasing the five-second budget. The six-second scheduling
fixture remains as a regression test. PID reuse was an initial hypothesis,
but the controlled reproduction established scheduler wait as the cause.

## Repeatable checks

Focused process-level regression loop:

```sh
GOWORK=off go test -tags=integration ./scripts \
  -run 'TestEasySDK(Readiness|StableReadiness)|TestNativePackageRepository(KeepsTimeoutOutput|DeadlineFixture)' \
  -count=3 -timeout=1m -parallel=2
```

The focused loop passed three repetitions after repair. The same selection is
also checked with `-race -count=1`. Full script validation uses the canonical
`scripts-integration` check from `.github/review-agent/policy.json`, with its
existing arguments and bounds unchanged.

The focused race check passed (`ok scripts 11.977s`), with no race reports.
The canonical `scripts-integration` check passed on the final source tree:

```text
ok  github.com/WuKongIM/WuKongIM/scripts 503.582s
ok  github.com/WuKongIM/WuKongIM/scripts/docs-cdn/certificate-helper 3.010s
ok  github.com/WuKongIM/WuKongIM/scripts/flowcheck 1.654s
ok  github.com/WuKongIM/WuKongIM/scripts/skillcheck 1.157s
ok  github.com/WuKongIM/WuKongIM/scripts/skillcontracts 0.670s
ok  github.com/WuKongIM/WuKongIM/scripts/transport-perf/probe 2.677s
```

Both failures now have controlled reproductions and passing regression coverage.
The full check includes the shared deployment execution tests from `effc2fb9f`.
This report does not certify a live cloud deployment or native package release.
