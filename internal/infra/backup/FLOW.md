# internal/infra/backup Flow

## Responsibility

`internal/infra/backup` adapts scheduled backup ports to shared-file and object
storage, Controller state, current cluster routing, node RPC, online export,
and maintenance restore. Scheduling and state-machine policy remain in
`internal/usecase/backup`.

## Repository adapters

`RepositoryProvider` resolves one `ArchiveStore`:

- `file` uses `<data_dir>/backup-repository`; all active data nodes must see
  the same mount. A repository-root alias that already exists at process start
  is resolved once to its real directory; object operations remain anchored
  there and reject every symlink inside the repository.
- `oss` uses an Alibaba Cloud OSS region, bucket, prefix, and optional endpoint
  override. When the override is empty, the provider derives the standard
  public S3-compatible endpoint from Region. It forces virtual-host addressing.
  Atomic create-only writes use OSS's native `x-oss-forbid-overwrite` behavior.
- `cos` uses a Tencent Cloud COS region, full bucket name including APPID,
  prefix, and optional endpoint override. When the override is empty, the
  provider derives the standard public endpoint from Region. It forces
  virtual-host addressing. Atomic create-only writes add COS's native
  `x-cos-forbid-overwrite` behavior and map both 304 and 412 conflicts to the
  repository's immutable-object result.
- `s3` uses one generic S3-compatible endpoint, optional region, bucket, prefix,
  and automatic or path-style addressing.

All object-storage access/secret key pairs are authenticated-encrypted before
Controller publication and decrypted only while opening a client.

`SharedRepositoryProbe` writes one coordinator marker, asks every active data
node to open the independently configured repository, observe the marker, and
write a receipt, verifies every receipt plus the bounded listing, then removes
the probe subtree. This probe runs only for the separately requested test of an
exact saved plan revision; saving configuration performs no repository I/O.

Open, marker read/write, receipt read/write, list, delete, identity binding, and
verification-publication failures are classified into stable
`RepositoryAccessError` stages and reasons. Provider codes and request IDs are
retained only after bounded control-character filtering. Remote node failures
cross RPC as the secret-safe failure DTO and regain the exact node ID at the
coordinator; credentials and raw provider error bodies never cross that
boundary.

## Controller state

`ScheduledStateStore` maps the complete bounded backup state to the
`ScheduledBackup` section of Controller state and uses revision-fenced
compare-and-swap commands, including archive-operation Controller node and term
ownership. It never stores manifests, object lists, plaintext credentials, or
Channel identities.

## Online export

The Controller leader resolves each logical Hash Slot's current physical Slot
leader and term. `FullExportService` runs on that owner, captures a stable
metadata snapshot twice around the Channel metadata scan, and rejects the cut
if authority or applied index changes. Message snapshots are grouped into
bounded Channel-leader shards. Each producing node streams compressed chunks
directly to the repository and returns only byte/record receipts.

The archive finalizer verifies all 256 Slot artifacts, publishes `COMPLETE`,
and applies retention.

## Current-cluster restore

`DistributedRestoreExecutor` reads durable topology while ordinary foreground
routing is disabled by maintenance:

1. preflight waits a bounded interval for every active node health report to
   observe the archive-operation Controller revision, then every data node
   proves it observed maintenance;
2. every current physical Slot replica captures a rollback snapshot;
3. every replica stages and semantically verifies the selected Slot archive;
4. the executor rechecks physical Slot peers before switching;
5. each verified replica installs staged files while maintenance hides partial
   state;
6. any failure reinstalls rollback files on replicas that reached `SWITCHED`;
7. cleanup removes node-local staging only after success or rollback.

Node commands are idempotent and fenced by job ID, Hash Slot, and attempt.
Repository payloads never travel over node RPC.
