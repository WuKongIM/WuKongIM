# MQTT source activation in the Channel log

Source protection is established by a typed exact Channel proposal, using the
existing local-plus-voter-quorum durability and authority recovery protocol.
Proposal format 4 has its own digest domain and permits exactly one internal
SyncOnce record with the fixed activation payload, no sender/client identity,
settings, expiry or publication properties. Business format selection never
infers activation from payload bytes. An explicit replication Proposal flag is
required; changing that flag on an exact retry conflicts.

The first activation in a log lifetime wins. Its command identity determines
the bounded source generation and its BaseOffset determines StartAfter. Later
activations preserve that generation and boundary. The control record itself is
protected; subscription admission must choose its own start cursor at or beyond
the committed activation record. This does not grant historical subscriptions.

Message System 13 stores a key-bound checksummed version-1 activation manifest.
It is an atomic projection of the exact append, not a second consensus protocol.
Before local HW covers it, the pending manifest conservatively clamps physical
trim at BaseOffset. The same commit that advances HW across the activation
materializes System 12 revision 1. Source state is never published solely because
an uncommitted record arrived. Both records require an explicit checkpoint.
Only an uncommitted suffix replacement may remove a pending activation; it must
atomically install any replacement manifest. Committed activation cannot reset.
The reserved `mqtt-log-v1:` generation can only be created by that control
projection. Local CAS cannot fabricate it or install another source over a
pending activation. Activated checkpoint reads use one pinned marker/source/HW
view; native reads add a bounded marker lookup without allocating a snapshot.

Ordinary exact replication, append-only recovery and bounded learner repair
carry the format-4 record. Quorum success protects the prefix even when follower
checkpoint notification lags. A future authority must recover that exact prefix
before serving. Initial protection retains the control and all subsequent source
records. Advancing copied-through still requires the separate distributed replay
and transfer protocol, which is not enabled by this change.

Binary backup excludes pending activation above its cut and preserves committed
activation together with source state and exact identities. Preflight rejects
missing/mismatched projections and cuts that reinterpret an active source as
pending. Matched runtimes/tools are required; old format readers reject format 4.
No message body enters Slot metadata; no new metadata table is introduced.

This slice exposes the replication primitive. Product reactor admission,
projection receipts, source routing and SUBACK remain required. A local read or
the mere presence of System 13 is never sufficient authorization for SUBACK.

## Failure inventory before implementation

1. Activation can be forged by native payload, batched with business records,
   changed on retry, or reinterpreted under an old hash/codec version.
2. A follower acknowledges durability before its pending trim fence is durable;
   HW passes activation without atomically materializing protection; an absent
   checkpoint/projection is silently repaired.
3. Duplicate activation changes the source lifetime or start; an uncommitted
   rejected proposal permanently blocks the correct recovered incarnation.
4. Suffix replacement clears committed protection, retains the wrong pending
   generation, or publishes checkpoint/source/records in separate commits.
5. Backup omits committed activation, includes an uncommitted one, accepts a
   conflicting manifest/source pair, or restores without the physical trim fence.
6. Restart, authority replacement, codec transfer or learner repair loses the
   activation. Local-only success or absence of quorum is mistaken for durability.
7. Physical cleanup removes the control before future replica recovery can
   reconstruct protection. Shared-copy advancement remains separately gated.
8. A local source CAS fabricates the reserved generation or bypasses a pending
   control, leaving checkpoint and activation evidence contradictory.

Bounded point state and one existing log proposal are used; there is no per-source
goroutine, history scan on append, fanout across subscribers or new consensus
group. Replica-storage tests deliberately inject exact proposals and corrupt
state. A real-storage multi-runtime integration test validates quorum recovery;
neither replaces the still-required full product process E2E.

## Frozen context

Source `9625f6ba3`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `ba75a7574d91cdbd3dc4f41dd1103a158506559d485ca3cd237c04b6d0d3bc02`
- `pkg/channel/FLOW.md`: `b0a75b5ea37558cffa0c67c01a032b61ae0aa0636dbaa0f1baa117ee94b264bc`
