# Restore admission response diagnosis

The operator approved diagnosing the intermittent generic restore HTTP 503 after
first-subscription repair. Existing evidence does not identify whether those
requests admitted a restore. Reuse real-process authenticated Manager HTTP,
backup dashboard and Paho through the established restore scenario. Do not retry
an ambiguous restore mutation. Retain fixed-phase external probes and public
state observations, never raw secrets, paths or provider causes.

## Ranked hypotheses and falsification

1. A confirmed ActiveRestore CAS succeeds, then deferred archive-lease release
   fails. Predict a returned nonempty job at the service seam with a release
   error and HTTP 503. That positive returned job proves admission.
2. Repository, archive, health/topology or node preflight fails before ActiveRestore
   CAS. Predict its exact failed phase with no admission call; public absence
   alone is insufficient evidence because Controller mirrors can lag.
3. Admission CAS rejects a changed revision or returns an unknown transport
   outcome. Predict a failed admission phase, distinguish typed definite state
   conflict from unknown effect. Never equate empty returned job with non-write.

## Failure inventory before any repair

- Confirmed durable admission must not be presented as generic failure because
  later cleanup races revision advancement or maintenance.
- Failed preflight must not enter maintenance or create a new restore; preserving
  an archive lease after uncertain cleanup cannot justify another operation.
- Unknown admission outcomes remain unknown even if one mirror has no job.
- Concurrent restore/backup/plan/archive changes must remain fenced; any atomic
  lease transfer must verify the exact token, kind, archive and plan before CAS,
  preserve newer unrelated Controller state and avoid clearing a foreign lease.
- Cancellation, failed CAS and archive errors must preserve their original error;
  cleanup cannot convert unknown results to safe mutation retries.
- A successful response must identify the same admitted job and allow the
  existing runner, maintenance fence and two-cycle MQTT restoration to complete.

## Diagnostic scope

Record fixed service phase, positive admission indication, cleanup error category,
and bounded node preflight capacity counts. `admitted=false` in an external probe
means no positively returned committed job; for an admission-CAS error it does
not prove non-admission. Read public dashboard after a failed response while
keeping that original failure red. Full raw workspace artifacts remain local.

## Controlled response contract before repair

The normal three-node diagnostic completes both restore/MQTT cycles. Both service
receipts positively admit a job and release their lease without error; historical
503 causality remains unknown. Enable an inert temporary-build release failure
after backup. The first erroneous HTTP response must remain red while read-only
Dashboard observes the newly active restore in this fresh single-request fixture.
This establishes the late-cleanup response defect without claiming its injected
cause occurred in the historical requests.

Before implementation, isolated admission contracts cover one atomic job/lease
proposal; unavailable post-admission cleanup; preservation of newer history;
missing/changed token, kind, archive, coordinator, term or expiry; elapsed lease;
failed preflight and cancellation; definite rejected admission; unknown admission
with and without application; unknown applied admission followed by a foreign
lease; and unknown acquisition. Never clear changed authority or turn an unknown
write into a definite conflict through a secondary cleanup error. The successful
process case must complete two restore/MQTT cycles while the old cleanup fault
remains enabled and uncalled.
