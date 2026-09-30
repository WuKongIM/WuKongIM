---
scope: package
summary: Persists immutable node-local proofs of gracefully retired or crashed MQTT owner boots.
---

# internal/infra/mqttowner Flow

## Responsibility

Persist runtime-minted retirement proofs and answer exact older-boot isolation
queries. App selects the node data directory and joins producers before recording.

## Boundaries

No Session policy, lease inference, worker, RPC routing or client identity is
owned here. Runtime decides when terminal owner quiescence is proved.

## Main Flows

1. Record one versioned bounded fact with NodeID, BootID and maximum issued ID.
2. Sync its temporary file, publish an immutable hard link, then sync the directory.
   Equal retries succeed; conflicts never replace facts.
3. Recover runs before any owner, RPC, listener or worker: it holds an exclusive
   `LOCK` flock for the active Owner generation lifetime, mints MaxUint64-bound receipts for
   started boots without receipts (proven dead by the lock), removes proven
   markers, then writes the current boot's started marker. Record removes that
   marker after its receipt; Close releases the lock after Record.
4. Hash the exact node/boot for a point lookup capped at 1 KiB, verify format,
   checksum and identity/bound, then return proof or fail closed.

## Invariants and Failure Semantics

- Current-boot queries remain in the live registry, never this fallback.
- Missing, corrupt, unsupported or oversized receipts supply no proof.
- No UID, ClientID, token, payload, per-owner cache or past-boot scan exists.
- One file per nonempty graceful boot remains indefinitely; time is not authority
  to delete receipts still referenced by old durable Sessions.
- Crash proofs are node-local and lock-based. App may release/reacquire only after
  joined generation retirement during restore; no restored-row or unavailable-node
  isolation is inferred. Corrupt or foreign markers fail
  Recover closed, so MQTT does not start.

## Read First

- [Storage adapter](retirements.go)
- [Contract and failure inventory](../../../docs/specs/mqtt-owner-retirement.md)
- [Crashed-boot retirement](../../../docs/specs/mqtt-crashed-boot-retirement.md)

## Update Triggers

Update for changes to persistence format, proof coverage, bounded reads or lifetime.
