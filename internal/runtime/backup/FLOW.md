# internal/runtime/backup Flow

## Responsibility

`internal/runtime/backup` owns reusable node-local archive streaming and the
Controller-leader supervisor. Repository adapters, cluster routing, and
business policy remain outside this package.

## Full Slot export

`FullStreamWriter` accepts one metadata or message snapshot as a closeable
stream. It:

1. reads no more than 64 MiB logical bytes per part;
2. writes Zstandard output to a temporary file;
3. records stored/logical SHA-256 and byte counts;
4. uploads the exact stored bytes under the deterministic Slot path;
5. returns bounded chunk references to its local caller.

Large payloads never cross node RPC. Slot and Channel leaders write directly
to the shared repository. Message exporters write a bounded repository-resident
chunk index and RPC responses contain only its fixed-size key, digest, counts,
and totals. The Slot leader authenticates that index before composing the
bounded Slot manifest.

`FullExporter` requires metadata first, assigns deterministic stream/sequence
numbers, writes the Slot manifest last, and never publishes the archive-level
`COMPLETE` marker.

## Publication

`PublishArchive` loads and verifies every one of the 256 Slot artifacts, writes
the canonical top-level manifest, reads it back, and only then writes the
manifest-bound `COMPLETE` marker. `VerifyPublishedArchive` repeats complete
marker, manifest, and chunk verification without mutation.

Every retry writes under an attempt-scoped immutable artifact directory.
Only the winning attempt is referenced by the Slot manifest, so a late worker
cannot overwrite an already accepted artifact.

## Scheduled supervisor

`ScheduledRuntime` runs one managed singleton loop per node. Only the current
Controller leader advances work:

1. resume restore first;
2. if no restore is active, evaluate the next schedule occurrence;
3. resume the active full-backup job.

Leadership loss stops new advancement. All active state is Controller-owned,
so a new leader continues the recorded phase rather than creating a duplicate
job. Slot export goroutines use one fixed low-cardinality dynamic task identity;
Hash Slot and archive IDs never become labels.
