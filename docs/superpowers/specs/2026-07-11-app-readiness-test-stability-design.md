# App Readiness Test Stability Design

**Date:** 2026-07-11

## Status

Approved for implementation planning.

## Problem

`TestConversationListAPIPaginatesWithNextCursor` intermittently receives HTTP
503 with `channel: not ready` during the repository-wide unit gate. Isolated
repetition normally passes, while concurrent multi-process repetition produces
the failure reliably.

The cold Channel runtime path is not the source: `ApplyMeta` waits for the
asynchronous store load and applies active metadata before append. The shared
app test configuration instead sets the Channel data-plane health lease to 500
milliseconds. Under broad CPU and I/O contention, the Controller health report
may not finish within that test-only lease. The append guard then correctly
fails closed with `channel.ErrNotReady`.

The same test configuration also starts the default plugin runtime even though
these tests do not exercise plugins. That adds gnet, Unix socket, filesystem,
and shutdown work to timing-sensitive cluster tests. A fixed two-millisecond
sleep in the pagination test is unrelated to the asserted ordering because the
test later writes explicit `ActiveAt` values.

## Goals

1. Make conversation and SEND smoke tests deterministic under normal
   repository-wide package contention.
2. Preserve the production data-plane lease and fail-closed behavior.
3. Make lease-based not-ready failures distinguishable from Channel loading or
   commit readiness failures.
4. Remove unrelated plugin runtime work from the shared cluster test fixture.
5. Remove arbitrary timing sleeps where ordering is already explicit.

## Non-goals

- Increasing Channel append or authority retry counts.
- Bypassing cluster semantics for a single node.
- Disabling or weakening the production health-report lease.
- Turning a failed readiness condition into a successful SEND.
- Changing conversation pagination, ordering, or cursor semantics.
- Adding mixed-version node RPC compatibility.

## Considered Approaches

### Increase append retries or backoff

This masks a deliberately expired safety lease, increases foreground latency,
and does not make the test fixture model production. It is rejected.

### Disable the data-plane lease in tests

This creates a single-node bypass and stops the smoke test from exercising the
real runtime. It conflicts with project deployment semantics and is rejected.

### Isolate the fixture and use a production-equivalent lease budget

This is selected. Tests unrelated to lease expiry use the production default
TTL and explicitly disable unrelated subsystems. Dedicated lease unit tests
continue to use a fake clock and short intervals to verify expiry precisely.

## Test Fixture Changes

The shared `singleNodeClusterAppConfig` fixture will:

- retain a short 20-millisecond health report interval so the node becomes
  schedulable quickly;
- use a 30-second health-report TTL, matching the production default safety
  budget;
- explicitly set `Plugin.Enable=false` through the existing explicit-config
  marker so default normalization cannot re-enable it;
- keep real Controller, Slot, message DB, Channel runtime, API, and
  channelappend composition.

No production runtime branch checks for test mode.

The pagination test removes the two-millisecond sleep between different
channel sends. Ordering remains determined by the explicitly written
`ActiveAt=1000` and `ActiveAt=2000` conversation states.

## Readiness Error Evidence

`channelDataPlaneLeaseGuard.AllowChannelAppend` continues to return an error
that satisfies:

```text
errors.Is(err, channel.ErrNotReady) == true
```

It additionally wraps a stable low-cardinality reason:

- `data_plane_lease_missing` when no successful visibility observation exists;
- `data_plane_lease_expired` when the latest observation is older than TTL;
- `data_plane_lease_clock_invalid` when the observed wall-clock age is
  negative.

Existing public error classification remains `not_ready` or
`route_not_ready`; the detailed reason appears in internal logs and tests and
does not become a UID/channel metric label.

The guard computes the reason from the same atomic timestamp and current-time
sample used for admission so diagnostics cannot report a different state from
the decision.

## Testing

Tests must be written before implementation and prove:

1. missing, expired, and invalid-clock lease failures all satisfy
   `errors.Is(ErrNotReady)` and carry the correct reason;
2. a fresh lease still admits append;
3. the shared app fixture explicitly disables plugins and uses the intended
   report interval/TTL;
4. conversation pagination still returns the two explicit ActiveAt pages
   without a sleep;
5. isolated repeated execution remains green;
6. concurrent repeated execution no longer produces SEND 503 failures under
   the same load that reproduced the original failure;
7. the exact repository unit gate is rerun after integration.

Dedicated short-TTL lease tests remain deterministic through an injected clock
and do not depend on scheduler sleeps.
