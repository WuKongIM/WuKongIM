# Backup Runtime Flow

## Responsibility

`internal/runtime/backup` runs bounded node-local capture work and the
Controller-Leader background loop. The loop executes scheduling decisions
injected by `internal/app`, resumes cluster jobs, and dispatches logical
partitions; policy rules remain in `internal/usecase/backup` and top-level
restore-point publication remains behind the use-case port.

```text
fenced CaptureRequest
  -> PartitionSource.OpenPartition
  -> one pinned logical-partition session and committed cut
  -> metadata stream -> bounded chunk replicator -> primary and secondary
  -> committed-message stream -> bounded chunk replicator -> both repositories
  -> cumulative record counts and message-ID fence
  -> strict partition manifest -> immutable publication in both repositories
  -> bounded PartitionReport returned to the coordinator
```

The source owns consistency and retention pins. Payload streams are never
accumulated as a whole logical partition: only the injected replicator's
bounded chunk, object-reference list, and a compact Channel-fence plan capped
at `maxBackupChannelsPerHashSlot` are resident. A partition manifest is
published only after both logical streams have replicated successfully.
Before recapturing a missing Controller report, the worker loads the fixed
partition-manifest key. A verified existing copy repairs its missing repository
replica and reconstructs the same bounded report without reopening the source.

The Controller Leader coordinator resumes missing partition reports from
Controller state, runs backup doctor checks without changing message
readiness, publishes only complete jobs, applies reference retention, retries
dual-repository garbage collection while forwarding the one Controller-pending
erasure reference into its protected mark set, and performs a daily remote audit. The
restore coordinator similarly resumes missing installs only on the Controller
Leader and bounds concurrent logical partitions.
