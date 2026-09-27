---
scope: package
summary: Persists immutable node-local proofs of gracefully retired MQTT owner boots.
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
3. Hash the exact node/boot for a point lookup capped at 1 KiB, verify format,
   checksum and identity/bound, then return proof or fail closed.

## Invariants and Failure Semantics

- Current-boot queries remain in the live registry, never this fallback.
- Missing, corrupt, unsupported or oversized receipts supply no proof.
- No UID, ClientID, token, payload, per-owner cache or past-boot scan exists.
- One file per nonempty graceful boot remains indefinitely; time is not authority
  to delete receipts still referenced by old durable Sessions.
- This is not abrupt-crash, unavailable-node, restore or uncertain-effect fencing.

## Read First

- [Storage adapter](retirements.go)
- [Contract and failure inventory](../../../docs/specs/mqtt-owner-retirement.md)

## Update Triggers

Update for changes to persistence format, proof coverage, bounded reads or lifetime.
