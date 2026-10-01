---
scope: package
summary: Persists bounded exact Will reservation/admission/seal transitions under the existing MQTT generation lock.
---

# Will Dispatch Journal Flow

## Responsibility

Persist body-free exact dispatch attempts; arbitrate Reserved -> Admitted -> AppendIssued versus
unissued -> Sealed without a worker, queue or publication policy.

## Boundaries

App owns the directory and generation lock. Usecases own authorization, Slot CAS,
positive receipts and exact successor/terminal cleanup. Node RPC carries identities.

## Main Flows

1. Open after owner retirement recovery, inventory at most 1,024 attempt records
   and eight interrupted staging files, then remove staging files under the lock.
2. Prepare a checksummed version-2 Reserved record before Started/successor CAS.
3. Serialize turn admission, append permission and sealing: fsync a temporary file, rename, sync directory.
   Only a definite durable winner grants dispatch or non-dispatch proof.
4. Another boot additionally requires the owning node's retirement fact. AppendIssued, version-1 Admitted,
   absent, corrupt, unsupported or oversized records never grant negative proof.
5. Remove only captured tuples after a definite successor/terminal decision.
   Unknown writes/cleanup retain bounded capacity; cap exhaustion fails closed.
   Capacity pressure offers at most 16 checksummed identities; overlapping filename
   pages make each record first within one wrap. Authority reads run outside locks.
   Temporary-copy gofail may pin cursor advancement for actual-page calibration;
   its progress observer fires only after the cursor really changes.
6. Close after Will workers join and before releasing the generation lock.

## Invariants and Failure Semantics

- No body, UID, token, lease inference or retained per-client map exists.
- Point reads reject symlinks and are capped at 2,342 bytes.
- Missing records block late admission; removed tuples cannot be recycled by an
  earlier boot. Directory-sync/cancellation errors never grant proof or dispatch.
- Version-2 Admitted can be sealed only before append permission is issued.
  AppendIssued and version-1 Admitted cannot prove an accepted append stopped.
- Reclamation requires a fresh exact terminal/newer-execution Will row in usecases.
  Missing/current/uncertain records stay; no age-based GC or background worker.

## Read First

- [Attempts](attempts.go)
- [Recovery contract](../../../docs/specs/mqtt-will-started-recovery.md)

## Update Triggers

Update for changes to transition, proof, capacity, persistence or lock lifetime.
