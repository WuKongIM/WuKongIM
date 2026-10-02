# MQTT bounded shared replay transfer

This storage step exports and imports bounded contiguous pages of message-domain
table 2. It adds no table, Slot command, proposal format, or product listener.
The runtime must independently establish current Channel authority and the
accepted complete replay prefix before calling import. Transport, quorum copy
receipts, learner readiness and consumer-proof GC remain separate required work.

`MQTTReplayTransfer` owns `Before`, `After` prefix states and at most 256 records,
with at most 16 MiB of original row envelopes. Export reads a pinned view of
activation, checkpoint, replay rows and counters. A requested range may be
shortened by the row/byte budget; its actual ending prefix is explicit.

`ImportMQTTReplay` requires a separate expected ending prefix obtained from a
current-authority verified copy/recovery decision. It MUST NOT be derived from
the untrusted page being imported. The existing log identity binds business
payload, sender, settings and version-dependent publication/lifetime semantics,
but does not bind every native row field (for example RedDot and stream fields).
Consequently log identities alone cannot authenticate a complete replay row
after original-body removal. The independently accepted replay digest binds the
entire canonical envelope and counters; local committed entry/proposal proofs
provide an additional mandatory check, never a substitute for that anchor.

Import requires an already installed committed log activation, matching source
generation/start, and a checkpoint covering the page and its complete proposals.
It verifies local entry identities, paired proposal indexes, proposal tails and
adjacent entry links, then validates canonical content, prefix hashes and totals.
It does not install log proofs, activation, checkpoint or source-release progress.
The append and checkpoint locks fence all evidence through synchronous commit.

An extension starts exactly at local coverage; an absent frontier starts at the
activation boundary. Complete retries verify existing primary rows and meters,
including historical retries after further progress. Gaps, partial overlaps,
orphan target rows/meters and different retries fail. All rows, meters and the
new local frontier commit together. Source copied-through may already be ahead
while recovery imports pages; partial local coverage is not delivery/readiness
authority. Return values describe only the accepted page, never quorum durability.

## Failure inventory before implementation

1. Self-consistent forged content/hashes bypass the independent expected prefix;
   valid-looking payloads disagree with committed entry identities. Legacy log
   formats omit some semantics and must not be presented as a full-row proof.
2. Missing or inconsistent activation/source/checkpoint, wrong incarnation/start,
   future HW, missing/foreign entry, incomplete paired proposal indexes or broken
   predecessor/tail proofs are accepted or silently repaired.
3. Gaps, extending overlaps, changed duplicate rows, corrupt local meters or
   orphan rows are overwritten; a bad last record leaves a partially written page.
4. Row/byte bounds, position/counter overflow, empty pages, wrong versions,
   noncanonical envelopes, cancellation or closed leases escape validation.
5. Export exceeds budgets, mixes generations/frontiers, depends on deleted
   original bodies or returns buffers aliasing storage. Retry/reopen loses state.
6. Recovery cannot refill a missing replay table after original trim although
   independently accepted content and installed committed log identities exist;
   import instead advances source release or ordinary unique indexes.
7. Local import is mistaken for a quorum receipt, complete learner recovery,
   subscription readiness, MQTT acceptance or permission to release sources.

Tests precede implementation. Storage isolation is used for deliberate evidence
corruption and atomicity checks; it is not full process-level MQTT acceptance.

## Frozen context

Source `c71e102be`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/message/FLOW.md`: `5d584988a22d53849f0c6ffddbcbed390b690c9f5c76136be70551f534f4c380`
