# MQTT replay anchor cluster admission

The existing neutral anchor committer is exposed by the cluster Channel service
and the foreground Node facade. Both origin and serving node require a fresh
Slot read before and after the reactor-owned operation. The copy receipt must
match exact epochs, route, leader, ordered placement, status and strict-majority
quorum. The serving reactor receives freshly resolved metadata, never caller
lease or retention values. A stale post-commit route withholds the proof; it does
not undo the durable control. Exact retries retain their original proof.

RPC 96 carries only a bounded copy receipt and server-allocated control identity.
Membership is read from Slot authority on the receiving node. The body-free
version-1 request/reply uses an 8 KiB cap, at most 256 acknowledgements, exact
request echo and the existing closed MQTT error catalog. It rejects unknown
versions, truncations, trailing bytes, invalid counts and mismatched proofs.
Forwarding binds one serving leader and never recurses. The stable gateway
survives runtime replacement; foreground transport admission preserves maintenance
and independently executing started mutation semantics. Calls have a five-second
deadline and use existing bounded transport/reactor queues, without new workers.

## Failure inventory before implementation

1. Cached metadata or a caller's Meta authorizes admission; membership, quorum,
   status, write fence, leader or route changes are missed before/after commit.
2. Remote forwarding reaches the wrong node, recurses, captures an obsolete
   service across restore, or bypasses foreground/maintenance admission.
3. Invalid copy evidence, canceled calls or missing capabilities reach mutation;
   a malformed, foreign-prefix or future-authority proof appears successful.
4. Wire requests allocate unbounded data, lose identity fields, tolerate unknown
   versions/statuses, accept another request's reply or retain proof on error.
5. An anchor-only idle retry or an older committed proof is rejected; a lost
   reply, restart or leader change appends another control instead of reusing it.
6. An isolated node returns accepted authority; ordinary messages lose ordering
   relative to remotely admitted controls, or source protection is released.

Tests use the already approved Channel service, transport codec and Node seams.
Deterministic fault cases supplement real three-node TCP/disk integration with
256 hash slots. Product MQTT process acceptance remains separate. Accepted-prefix
planning, source release and accepted-anchor repair remain required follow-up work.

## Frozen context

Source `0e039062a3a207aaa8b0d31ace929bfaa524c48c`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/channel/FLOW.md`: `77b3e13c0ea25aa637d0d7db409c2a120ca0f0ed6348e4354dc4fcf1e62a1465`
- `pkg/cluster/FLOW.md`: `211deb63334c56f41268c189b0d77305122e82b084df7f6b23b2ffb7d9783d4d`

## Restart regression discovered during validation

The initial three-node race run reached concurrent retry and idle suppression,
then detected `controller.Runtime.Step` reading the Raft service pointer while
`startVoter` published it. This is existing startup behavior exposed by a live
peer delivering messages during restart, not an MQTT worker race. The same run
also revealed that the test's post-election sequence omitted the native term
barrier; the expected next business position is 7, after control 5/barrier 6.

Before repair, failure cases are unsynchronized pointer publication, failed-start
pointer reset, and voter-promotion cleanup racing inbound Step. Reuse the existing
runtime mutex only to publish/capture that pointer, releasing it before stepping
or stopping Raft. No runtime I/O is held under that lock. Other lifecycle facade
operations retain their existing serialized ownership. The exact Node integration
is the regression seam; a separate shallow mock would omit the real TCP restart.
The race detector already names the conflicting accesses, so additional logging
and a separate timing harness are unnecessary. Evidence is retained in
`/tmp/mqtt-anchor-routing-integration.log`.

Additional frozen context from the same source: `pkg/controller/FLOW.md`, SHA-256
`5424229c2167504715b089bf04339bc6c6831fbf5d65318b5eefe28d7c13a0ea`.
