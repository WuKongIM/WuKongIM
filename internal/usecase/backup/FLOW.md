---
scope: package
summary: Owns cluster-wide backup plan policy, job admission, archive lifecycle, retention, and current-cluster restore orchestration.
---

# Backup Use Case Flow

## Responsibility

This package owns entry-independent policy for the single cluster-wide backup
plan, scheduled and manual jobs, archive operations, retention, and restore.
Durable DTOs come from `internal/contracts/backup`; storage and execution are
injected ports.
It does not own entry protocols, concrete repositories, or cluster transport.

## Boundaries

- Saving a revision-fenced plan publishes configuration only; repository I/O
  occurs in the separate exact-revision test operation.
- One-node deployment remains a single-node cluster and follows the same Slot,
  Controller, publication, and restore paths.
- Access adapters redact and present results; infrastructure implements
  repository, Controller state, routing, export, and restore ports.

## Main Flows

1. Plan management validates repository, schedule, time zone, retention, rate,
   one-to-four workers, and one-to-48-hour deadline. Effective repository
   changes become unverified; schedule-only changes retain verification.
2. `JobRunner` claims exact Slot attempts, exports bounded concurrent batches,
   accepts fenced completions, publishes only after all 256 Slots, then applies
   retention under a Controller-node-and-term operation lease.
3. Restore fully verifies an archive, enters maintenance, stages and verifies
   every current replica, switches only after all Slots pass, and shares one
   durable rollback path for cancellation, timeout, or failure.

## Invariants and Failure Semantics

- Enabling a disabled plan admits one initial backup; missed schedule times are
  not replayed, and overlap becomes a bounded skipped history record.
- Explicitly unverified repositories reject admission and resume. Legacy nil
  verification remains valid only until the effective repository changes.
- Cancellation or deadline expiry never publishes `COMPLETE`; failover resumes
  the single durable active job and unfinished attempts.
- Dashboard and errors are bounded and secret-safe. Blank replacement secrets
  preserve credentials only for the unchanged object provider.
- Restore success increments `manager_session_epoch` while preserving restored
  client authentication tokens.

## Read First

- [Plan management](management.go)
- [Scheduled service](scheduled_service.go)
- [Backup runner](job_runner.go)
- [Restore runner](restore_runner.go)
- [Archive catalog](archive_catalog.go)

## Update Triggers

Update this file when plan fields, verification, admission, Slot execution,
publication, retention leases, restore phases, or session invalidation changes.
