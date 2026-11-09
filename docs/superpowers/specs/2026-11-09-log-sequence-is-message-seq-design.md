# The distributed log sequence number is `message_seq`

Status: proposed (design direction confirmed by the maintainer; implementation
awaits approval of this document).

## Principle

The maintainer stated it on 2026-11-09: **a Channel's distributed log sequence
number is `message_seq`**. Log indexes and message sequences are one numbering,
so every log index is a real message's `message_seq`. Sequences are strictly
ordered and contiguous across messages. No authority, control or replication
bookkeeping entry may occupy a log index.

## Current violations

Every durable-quorum log entry carries a proposal manifest version
(`pkg/quorumlog`):

| Version | Meaning | Real message? |
| --- | --- | --- |
| 1, 2, 3 | ordinary / with lifetime / with publication metadata | yes |
| 4, 5, 6 | MQTT source activation / replay anchor / replay retirement | **no** |
| 7 | current-term authority barrier | **no** |

### 1. Authority barrier (default path, every multi-replica Channel)

Since `cccafaff0` (v3.0.0-beta.1), `quorumLog.Install` writes a standalone
barrier entry at `N+1` when it recovers a non-empty frontier under a different
authority (`pkg/channel/replication/recovery_barrier.go`).

- **When it is written:** every Channel leader change on a non-empty Channel.
- **Evidence:** in the message retention E2E, messages 1–3 were followed by
  barrier 4, and the next message got seq 5. Client `/channel/messagesync`
  hides the barrier (it is `SyncOnce`), but the gap 3 → 5 is visible.

### 2. MQTT internal proposals (default-off preview)

Versions 4–6 are appended to the same Channel log, including WK group channels
with MQTT subscribers, so they also consume `message_seq`.

## Why the barrier exists, and why it cannot simply be dropped

The barrier is the new authority's only fence against a stale old leader:

- **No other check at followers:** a follower accepts a proposal when its
  predecessor matches (`validateDurableProposalPredecessor` checks previous
  index, term and digest). The follower exchange path (`ExchangeServer`) checks
  neither authority nor metadata, and appends are not gated by a leader lease.
- **How the barrier fences:** once the barrier is durable on a write quorum
  at `N+1`, an old leader's proposal at `N+1` conflicts on at least one voter
  of any quorum it could use, so it cannot commit.

Dropping the barrier and letting the first business message carry the
authority (option A) is therefore **unsafe**. Example with voters A (stale old
leader, cut off from metadata but able to reach C), B (new leader) and C:

1. Until B's first client write, `N+1` stays free.
2. A writes `N+1` on {A, C}, reaches quorum and acknowledges its client.
3. B's reads stop at its HW `N`, so the acknowledged message is invisible.
4. B's first write conflicts on A and C, cannot reach quorum, and the Channel
   becomes non-writable.

The barrier limits this window to milliseconds. Option A widens it to "until
the next write". Option A is rejected.

## Design: a durable authority promise outside the log

Replace the barrier entry with a **promise record** that each Channel voter
persists in its own Channel storage. The promise travels over the existing
Channel replication exchange. It is not a log append and never gets a log index.

### Where it is stored

- The promise is stored on **each Channel replica node, in that Channel's
  partition of the message DB**. It is **not** in Slot metadata. A Channel
  replica need not host the Channel's Slot, and enforcement must happen inside
  the follower's own append commit.
- **Key:** new system ID `messageSystemIDAuthorityPromise` (19) in the Channel's
  `SpaceSystem`. It sits beside checkpoint (1), retention (4), proposal manifests
  (8, 9) and entry identity (10), and never in the message table
  (`TableIDMessage`).
- **Value:** one record per Channel per replica, `{authority (ChannelEpoch,
  LeaderTerm, FenceVersion), prefix end N, tail digest D}`. A newer authority
  overwrites it, and a lower one is rejected.
- **What Slot still decides:** Slot metadata (`ChannelRuntimeMeta`) still
  decides who the leader is and what the new authority is. The promise only
  enforces that decision at the Channel replicas.

### Install flow

1. **Learn the authority:** the new leader learns its authority from Slot
   metadata, as today.
2. **Recover the prefix:** recovery probes and repairs the quorum-identical
   prefix `1..N`, as today. Probes now also return each voter's promise. Recovery
   selection uses the highest promised authority among the observed voters
   together with the tail identities.
3. **Promise round:** the leader sends `Promise{authority, N, D}` to every
   voter. Each voter, inside one serialized Channel store commit:
   - rejects the promise if it already holds a higher promise or a log tail that
     is not the exact prefix `(N, D)`;
   - otherwise durably writes (fsyncs) the promise, then replies.
4. **Ready:** after WriteQuorum durable acknowledgements, the local one
   included, the Channel becomes ready. The next business message is `N+1`.
   Without a quorum, Install fails and the Channel stays not ready, as with a
   failed barrier today.
5. **Missed voters:** the leader keeps re-sending the promise to them in the
   background, like trailing deferred writes.

The promise round reuses the durable round machinery: foreground dispatch to a
quorum, hedging, retry, and the per-voter durable proofs added for retention in
PR #1116. Only the request kind differs.

### Append-side enforcement

In the same serialized store commit as the predecessor check, a follower
rejects an append whose authority is lower than its promise (`ReplicateStaleFence`).

An accepted append with a higher authority atomically raises the promise to
that authority. A voter that missed the promise therefore catches up with the
first new-authority write it accepts.

### Why a quorum is enough

- An old-authority write needs a write quorum.
- Any two quorums share at least one voter, and that voter holds the new
  promise and rejects the write. This is the same intersection argument the
  barrier and every commit rely on.
- Voters that missed the promise may still accept old writes, but they cannot
  form a quorum on their own.

### Edge cases

- **Leader crashes after partial promises:** the next leader has a strictly
  higher authority from Slot metadata and re-runs the round. Leftover promises
  reject only lower authorities.
- **Promise and old append race at one follower:** both use the same
  per-Channel serialized store path, and check-then-write is one commit. The old
  append is either durable first, so the probe sees it and it is part of
  recovery, or it is rejected.
- **Restart before any write:** the promise is durable, so no state is lost.
  Reinstalling the same authority finds promises already present and is
  idempotent.
- **Learners** (non-voting replicas) do not need promises: they never count
  toward a quorum.

## MQTT internal proposals

MQTT already keeps its state in system records (IDs 12–15). Versions 4–6 must
stop being log appends and become system-record updates replicated with the
same promise-fenced exchange.

This is a separate follow-up design. **MQTT must not leave preview until it
ships**, because enabling MQTT adds non-message entries to business channels.

## Compatibility, upgrade, rollback

These are persisted-format and protocol changes, so they need maintainer
approval (AGENTS breaking-change rules).

- **Persisted format:** one additive system record. It must be verified that
  older binaries ignore an unknown system ID, and whether `DATA-FORMAT.json`
  needs a version bump. A bump would block rollback and requires a migration
  plan instead.
- **Protocol:** a new exchange request kind, `Promise`, plus the promise in
  recovery probe replies.
- **Rolling upgrade:** a Channel uses promises only when every voter
  advertises the capability, through the existing node capability exchange.
  Until then it keeps writing barriers, so mixed-version clusters behave as
  today.
- **Rollback:** an older binary ignores promise records and writes barriers
  again on leader changes, which is today's safety. To verify: an older node
  receiving appends whose predecessor chain crosses a former promise boundary
  behaves correctly, because there is no log entry for it to miss.
- **Existing data:** logs written since v3.0.0-beta.1 keep their barrier
  entries and therefore one gap per past leader change. They cannot be
  renumbered without rewriting every replica's log, client caches and
  conversation cursors. Client history keeps hiding them. Manager reads route
  through the leader and must hide internal entries the same way. Release
  notes state that new writes no longer create gaps.
- **Downtime:** none.

## Verification plan

Failure cases are written first. The tests cover:

1. A leader change on a non-empty Channel consumes no seq: the next message is
   `previous + 1`.
2. The stale-old-leader scenario above: A can never acknowledge an entry at
   `N+1` once B's promise is on a quorum.
3. Partial promise, leader crash, then a new leader.
4. A promise and an old append racing at one follower.
5. Restart before the first write, and reinstalling the same authority.
6. A voter that missed the promise upgraded by the first new-authority write.
7. A mixed-version Channel falls back to barriers.
8. Rollback reads.

Levels:

- **Unit tests:** deterministic crash and race matrices in
  `pkg/channel/replication` and `pkg/db/message`.
- **gofail E2E:** at the promise and first-write boundaries.
- **E2E:**
  - `message_retention`, with contiguous seqs across a leader restart;
  - the `channel_failover` suites;
  - a new client-visible contiguity assertion after forced leader changes.

## Decisions requested

1. Approve the promise design and its persisted-format and protocol additions.
2. Accept that historical gaps remain and are documented.
3. Track MQTT internal entries as a follow-up design that blocks MQTT GA.
