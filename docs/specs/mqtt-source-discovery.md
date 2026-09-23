# Bounded distinct-source discovery for MQTT replay scheduling

Background replay cannot depend on best-effort append callbacks or scan every
subscriber before reaching the next source. Reuse source-binding retention index
4: its `(owner kind, source identity, source generation)` prefix covers Preparing,
Active and Removing Channel obligations. Removed rows and UID inbox registrations
are excluded. Seek to the end of each owner prefix after validating one indexed
witness against its primary row in a pinned snapshot. Work is at most `limit + 1`
owner witnesses, independent of subscribers per source; malformed/missing witnesses
fail closed rather than trigger an unbounded search. This is discovery, not a
full index integrity audit or proof that all consumers completed.

`MQTTReadSourceOwners` appends read kind 16 to the existing closed MQTT catalog.
It accepts a limit of 1..64 and a complete source-owner cursor; the result contains
only unique owners in encoded key order, an exact last-returned cursor and `Done`.
It is a hash-Slot recovery read through Node/RPC 91's fresh barrier and pinned
snapshot, never an entity read or cache fallback. Empty pages preserve the input
cursor. Insertion before a process cursor is discovered on the next wrap; callers
revalidate source/Channel authority before work. Old peers reject the unknown
kind. No durable row, index, table or format changes are needed.

This is the durable discovery dependency for the background replay loop. It does
not activate protection, copy content, release sources or establish readiness.
The scheduler must retain bounded per-source continuation and treat stale/absent
candidates as hints, not consumer or content proof.

## Failure inventory before implementation

1. Large subscriber sets multiply source work, starving later sources; grouping
   combines distinct source generations or uses lexical rather than encoded order.
2. Preparing/Removing obligations disappear, Removed/UID records become Channel
   work, or a missing primary/bad index witness is silently treated as absence.
3. Pagination returns duplicates, regresses/skips a source, gives a wrong final
   cursor, or reports Done from a page limit rather than a next-owner probe.
4. Index and primary reads span different revisions; cancellation/closed storage
   leaks snapshots/iterators or publishes a partial successful page.
5. RPC accepts unknown fields, wrong-kind arrays, incomplete/foreign cursor,
   unordered/duplicate owners or stale routing; a warmed isolated leader serves
   either rows or authoritative absence without quorum.
6. Source discovery changes row/index layout, loses obligations after restart or
   bypasses the Node foreground/maintenance gates.

Tests precede code at storage, closed RPC and real three-node consensus seams.
Full product MQTT E2E and automatic scheduler composition remain required.

## Frozen context

Source `66675e290`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `8b723e6f1402c2361a872e3a6e4021ac4146782ebc9b416cb7b9cc9787bb9d75`
- `pkg/db/meta/FLOW.md`: `e05d11e22fc0c4afa0898a9e6498b45b9cc7f594fd4b8529587056cd784ecbb7`
- `pkg/slot/FLOW.md`: `435773f47c077f64a7b06208554359c97687e6c2fb7cd5878d3169fb163c6da1`
- `pkg/cluster/FLOW.md`: `756ea5d026cae3194d7ccdda2c778c8aa979b755a2a7bb8531b30cb4064ce877`
