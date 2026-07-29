# internal/usecase/backup Flow

## Responsibility

`internal/usecase/backup` owns entry-independent policy for the single
cluster-wide backup plan, scheduled/manual job admission, bounded task state,
archive operations, and current-cluster restore. Durable DTOs are defined in
`internal/contracts/backup`; repositories and cluster execution are injected
ports.

## Plan and scheduling

Manager replaces one complete plan through revision-fenced compare-and-swap.
The plan contains enabled state, repository selection, Cron or `@every`
schedule, time zone, retention count, per-node rate, one through four workers,
and a one through 48 hour deadline. Saving publishes configuration only; it
does not open or test the repository. A changed effective repository is saved
as unverified, while schedule-only changes preserve the existing verification.
Object-storage credential rotation is an effective repository change.

Enabling a previously disabled plan admits one immediate initial full backup.
The Controller leader evaluates only the next occurrence after the durable
schedule cursor. Missed occurrences are not replayed. An occurrence that
overlaps a backup or restore becomes a bounded `skipped` history record.
Enabling, manual admission, scheduled admission, and resumed runner execution
all reject an explicitly unverified repository. A nil verification record is
legacy verified state until the effective repository changes.

## Backup execution

`JobRunner` resumes the only active job from Controller state:

1. Resolve current Slot authority and claim attempts serially through durable
   authority fences.
2. Export a bounded batch concurrently, capped by `workers_per_node` for each
   data node.
3. Accept completion only for the exact job, attempt, owner, and term.
4. After all 256 Slots complete, verify and publish the archive.
5. Apply retention under a durable operation lease owned by the current
   Controller node and term. A successor waits for the previous worker's
   token-fenced completion release (or lease expiry) before it starts repository
   side effects. Release the lease before moving the terminal result into the
   newest-first, 100-record history.

Cancellation and deadline expiry finish without publishing `COMPLETE`.
Process or Controller leader failover reuses the durable active job and retries
unfinished Slots.

## Manager operations

`ManagementService` returns one redacted dashboard containing plan, active
task, bounded history, and published archives. Configuration and repository
testing are separate operations. Configuration durably publishes the plan
without repository I/O and preserves existing object-storage credentials when
replacement fields are blank and the provider kind is unchanged. Testing
requires the exact saved plan revision, opens that repository, probes access
and shared visibility through every active data node, binds it to the source
cluster, and only then marks that same plan revision verified.

Alibaba OSS and Tencent COS derive their standard public endpoint from Region
when no override is supplied; the plan keeps that default selection as an
empty Endpoint so later Region changes cannot retain a stale derived address.
Repository failures retain a bounded provider, operation stage, stable reason,
provider code, request ID, and node ID for access adapters to present without
exposing credentials or raw provider bodies. Archive verify, hold/release, and
delete operate only through the current plan repository.

## Restore execution

`RestoreService.StartRestore` fully verifies the selected archive before
admitting one 48-hour restore job. `RestoreRunner` then advances one durable
transition per call:

```text
preparing
  -> maintenance
  -> stage all 256 Hash Slots on every current replica
  -> verify all staged replicas
  -> switching
  -> success
```

Every Slot attempt records sorted replica IDs and logical-byte evidence.
Switching is forbidden until all Slots are verified. Cancellation, timeout,
staging failure, verification failure, or switch failure enters the same
durable rollback phase. A successful restore increments
`manager_session_epoch`, invalidating existing Manager sessions while
preserving restored client authentication tokens.
