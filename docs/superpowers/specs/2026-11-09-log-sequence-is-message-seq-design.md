# The distributed log sequence number is `message_seq`

Status: proposed (awaiting maintainer approval).

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
| 1 `ProposalManifestVersion` | ordinary message | yes |
| 2 `ExpirationProposalManifestVersion` | message with immutable lifetime | yes |
| 3 `PublicationProposalManifestVersion` | message with publication metadata | yes |
| 4 `MQTTSourceProposalManifestVersion` | MQTT source activation | **no** |
| 5 `MQTTReplayAnchorProposalManifestVersion` | MQTT replay anchor | **no** |
| 6 `MQTTReplayRetirementProposalManifestVersion` | MQTT replay retirement | **no** |
| 7 `RecoveryBarrierProposalManifestVersion` | current-term authority barrier | **no** |

### 1. Authority barrier (default path, every multi-replica Channel)

Since `cccafaff0` (v3.0.0-beta.1), `quorumLog.Install` writes a standalone
barrier entry when it recovers a non-empty frontier under a different
authority (`pkg/channel/replication/recovery_barrier.go`).

- **When it is written:** every Channel leader change on a non-empty Channel.
  This includes the same node reinstalling under a new route fence after a
  restart.
- **Evidence:** in the message retention E2E, messages 1–3 were followed by
  barrier 4 after the leader restarted, and the next message got seq 5.
- **Client impact:** client `/channel/messagesync` hides the barrier because it
  is `SyncOnce`, but the gap 3 → 5 is visible. A client that treats a seq gap
  as a missing message will resync or report loss.
- **Manager impact:** the Manager message list showed the barrier as an empty
  message.

### 2. MQTT internal proposals (default-off preview)

`sealAppendProposal` gives MQTT source activation, replay anchor and replay
retirement their own manifest versions. These records are appended to the same
Channel log, including WK group channels that MQTT subscribers attach to, so
they also consume `message_seq`.

## Why the barrier exists

The original design (`2026-08-15-channel-durable-quorum-log-design.md`) uses
the barrier as the new authority's durable current-term proof. It explicitly
avoided "a separate persisted-promise protocol". A recovered prefix becomes
ready only after one entry under the new authority is durable on a quorum.
That entry:

- fences concurrent old-authority work at that index on those voters;
- makes `Installed` report a frontier proven under the current authority.

`Install` already skips the barrier for an empty log. The first business
proposal carries the complete current authority and makes that proof durable in
the same quorum round (`TestQuorumLogInstallDefersEmptyAuthorityBarrierIntoFirstProposal`).

## Options

### A. Carry the authority proof in the first business message (recommended)

Generalize the empty-log rule to every frontier. `Install` recovers and repairs
the quorum-proven prefix exactly as today. It becomes ready to *read* that
prefix without writing anything. The first business proposal under the new
authority is the current-term proof: it chains from the recovered tail through
`PreviousTerm/Index/Digest`, as the barrier does today.

Safety argument, to be verified before implementation:

- **Same structure as the barrier:** the first business proposal has the same
  position, authority and prefix chain as the barrier. A voter that already
  holds a conflicting old-authority entry at that index rejects it exactly as
  it would reject the barrier. Conflicts stay non-writable, as today.
- **Reading the recovered prefix:** the prefix is identical on an intersecting
  quorum. Recovery never truncates observed suffixes, so it cannot be lost. This
  is the same proof the empty-log path and the current `Installed.HW` already
  rely on.
- **Concurrent old work:** stale old-leader appends are rejected by follower
  metadata fences (`ReplicateStaleFence`: `ErrStaleMeta` / `ErrWriteFenced`).
  This is not done by the barrier: today a barrier written before an in-flight
  old proposal arrives does not revoke it either ("read-only probes are not
  durable promises").
- **What changes:** an old-authority/new-authority conflict surfaces at the
  first write instead of at `Install`. Before that, the Channel is readable but
  that conflict has not been detected yet. The verification below must prove
  this is no weaker than today.

Cost and compatibility:

- **Compatibility:** no wire, persisted-format or configuration change.
  Rolling upgrades are safe, and mixed-version clusters keep working, because
  an older node still writes barriers.
- **Rollback:** direct. Rolled-back nodes write barriers again on later leader
  changes.
- **Write latency:** the first write after a leader change carries the
  authority proof in its own quorum round, with no extra round trip, as for an
  empty log today.

### B. Persisted per-voter authority promise

Add durable replica metadata, a Paxos phase-1-style promise of "accepted
authority ≥ A, prefix through N with tail digest D". Write it on a quorum during
`Install` instead of a log entry, and make voters reject lower authorities from
that record.

- **Pros:** the Channel is fully fenced before the first write.
- **Cons:**
  - a new persisted record and a new exchange message, so mixed-version clusters
    need capability gating;
  - rollback after activation is not direct, because older binaries ignore the
    promise;
  - recovery selection must also compare promises.

Larger and riskier than A for the same principle.

### MQTT internal proposals (both options)

Move source, anchor and retirement state out of the message log into
Channel-scoped side records with their own durability. MQTT is a default-off
preview, so this is a separate follow-up design.

**Until it ships, MQTT must not leave preview.** Enabling it adds non-message
entries to business channels.

## Existing data

Data written by v3.0.0-beta.1 through the current release already contains
barrier (and, with MQTT enabled, MQTT) entries at fixed sequences. These cannot
be renumbered without rewriting every replica's log, client caches and
conversation cursors. Plan:

- **Keep reading them:** client history keeps hiding them (`SyncOnce`).
- **Manager reads:** these will route through the Channel leader (separate PR).
  They should hide internal entries the same way, so operators do not see
  phantom rows.
- **Release notes:** document that sequences written before the upgrade may
  contain one gap per past leader change, and that new writes no longer create
  gaps.

## Verification plan (option A)

1. **Failure cases first:**
   - a leader change on a non-empty Channel consumes no seq, so the next message
     gets `previous + 1`;
   - an old-authority proposal racing the new leader's first write can never
     produce two acknowledged entries at one index;
   - a crash at every step of `Install` and the first write;
   - a restart before the first write;
   - a write fence during transfer.
2. **Tests:** deterministic crash/race tests in `pkg/channel/replication`,
   updating the barrier tests, and gofail E2E at the first-write boundary.
3. **E2E:** `message_retention` (contiguous seqs across a leader restart), the
   `channel_failover` suites, and a new client-visible contiguity assertion
   after a forced leader change.

## Decisions requested

1. Adopt option A.
2. Accept that historical gaps remain and are documented, not rewritten.
3. Treat MQTT internal entries as a follow-up design that blocks MQTT GA.
