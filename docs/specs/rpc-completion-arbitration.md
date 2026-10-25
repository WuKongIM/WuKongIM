# RPC completion arbitration

## Decision and scope

Define one request-owned completion decision for transport RPCs. A complete
response committed before the caller's monotonic deadline remains available
when its caller resumes later. A genuinely late response cannot become timely
because a `select` chose its branch. This makes an unspecified race consistent;
the previous `Call` contract did not promise timely-response dominance.

This is a node-local transport contract for every cluster topology. It changes
neither remote authority nor wire budgets, cancellation of started mutations,
admission limits, service concurrency, retries, configuration or release gates.
An enclosing usecase may still reject subsequent work when its own budget has
expired; accepting an RPC result does not renew that budget.

## Evidence

On source `8cb5ef37b7d94b4994f0a4f36e7d81b9c3e61d9d`, the existing
admission-observer and post-response-notification test barriers can hold a
caller while a response is already buffered. In one fixed 32-trial experiment,
all responses were published before their actual 500ms deadlines, then callers
were released after those deadlines. Seventeen returned responses and fifteen
returned deadline errors; no setup was invalid. All fifteen deadline outcomes
also omitted the three response-receive trace phases.

This establishes the controlled race. It does not prove that the unsampled R8
authentication failure or its sampled client/server timeout used that race.
R8 remains failed and formal Linux qualification remains outstanding.

## Completion ownership

- One bounded owner contains the response candidate, deadline eligibility and
  final result. The existing 16-shard pending index locates owners; removing an
  index entry is not proof that response copying or completion has finished.
- Copy and decode finish before the response's deadline decision. Frame-ready,
  pending-table removal, and the sampled pre-notification timestamp are not
  substitute completion points.
- A short owner transition uses the same monotonic clock as the caller deadline.
  A decision at or after the deadline is late. Waiter scheduling and timer
  delivery do not turn an already committed timely response into a timeout.
- Send success remains a prerequisite. A Send failure keeps its existing error
  and cleanup even if a response candidate arrived before Send returned.
- Preserve explicit cancellation errors, remote business errors and the existing
  preference for an already delivered response when a connection closes. Define
  concurrent transitions explicitly; do not use random `select` readiness as the
  final completion decision.
- Caller, reader, connection failure and late/duplicate frames must settle one
  outcome and release pending, payload and observation ownership exactly once.
  No owner is pooled or reused while another path can still reference it.
- Do not copy payloads, call observers or perform I/O while holding an owner or
  index lock. Allocate request owners before index admission; terminal owner
  transitions must not allocate. Map growth remains serialized by its shard.
  Maintain a consistent lock order and do not wait for an owner while holding
  an index lock.

The implementation should use the smallest existing internal module boundary.
It adds no global service, per-request goroutine, per-request timer, unbounded
queue or speculative compatibility layer. Keep pending counts tied to real
insertions/removals and preserve shutdown's rejection of later registrations.

## Observations and costs

Receive evidence follows its actual completed owner, independently of which
wakeup the waiter observes. Preserve frozen snapshots and explicit unknowns;
local receipt never proves remote business success. Document any timing boundary
that must move when replacing channel publication with an owner transition.

Measure allocation count and actual bytes for equivalent unsampled RPC
lifecycles before and after the change, as well as retained owner sizes. A new
owner or mandatory deadline clock is not free. Report those costs separately
from throughput; keep all existing observation, call and byte bounds.

## Validation before implementation

Write the failure matrix and tests first. Cover timely completion with delayed
caller; truly late response; copy spanning a deadline; timeout while copying;
both wakeups ready; explicit cancellation; response and connection shutdown;
remote errors; response before Send returns; later Send failure; duplicate/late
response; pending registration after shutdown; cancellation-frame ownership;
and exactly-once payload/pending/trace cleanup. Include observed and unobserved
paths. Real clocks, listeners and deadline waits belong in integration tests.

Reuse the public TCP and existing connection/observer seams. Retain the fixed
32-trial original RED result and setup evidence. After related integration and
race validation plus independent review, build a fresh exact candidate and
repeat 10,000 online / 2,000 SEND/s with the original local gates. Completion
tests alone do not establish the R8 root cause, formal Linux acceptance or
permission to publish v3.

## Implemented validation

The request-owned completion implementation passes the complete fixed 32-trial
fixture with 32 replies and no deadline or setup failures. The original
17-reply/15-deadline RED result remains separate. Related transport units,
focused integration and owner/index/compatibility race×20 pass; the 32-trial
real-deadline fixture is not multiplied twenty times.

Equivalent full `Conn.Call` fixtures measure 713→793 B/request without a deadline
and 712→792 B/request with a reused future deadline. Both retain 16 allocations
per request. The 80-byte increase is an actual fixture cost, not evidence of a
throughput gain. The original-scale local rerun and formal Linux qualification
remain required.
