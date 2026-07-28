# Scheduled Full Backup and Manager Restore Design

Status: implemented
Date: 2026-07-29
Supersedes:

- `docs/superpowers/specs/2026-07-21-cluster-automatic-backup-design.md`
- `docs/superpowers/plans/2026-07-21-cluster-automatic-backup.md`
- `docs/development/BACKUP_AND_RESTORE.md`

## 1. Summary

WuKongIM replaces continuous incremental backup with one cluster-scoped
scheduled full-backup feature managed entirely from Manager.

Each successful backup is a self-contained, compressed, checksummed,
point-in-time logical snapshot of all 256 Hash Slots. It has no dependency on
earlier backups. A single repository stores the backup. Repository redundancy,
versioning, object lock, and storage-side encryption are deployment concerns,
not WuKongIM coordination concerns.

Backup remains online. Restore is an explicitly destructive maintenance
operation against the current deployment: it stages restored data separately,
verifies all current replicas, switches only after successful validation, and
automatically rolls back if activation fails.

There is no compatibility requirement for artifacts or Controller state
created by the current continuous-backup implementation. The replacement
starts with backup format `v1`.

## 2. Goals

The new feature MUST:

1. let an authenticated administrator configure, enable, disable, monitor, and
   run backup from Manager without changing TOML or restarting nodes;
2. let an administrator with an explicit restore grant restore a selected
   backup through one guided Manager workflow;
3. create independent full backups online without pausing message traffic;
4. restore into the current cluster under maintenance mode, using staging and
   automatic rollback;
5. support a single-node cluster and multi-node clusters without topology
   bypasses;
6. organize artifacts by the configured 256 logical Hash Slots so a backup can
   restore after node-placement changes within the same cluster identity;
7. keep Controller state bounded independently of channel count, message count,
   backup size, and repository history;
8. use fixed, conservative resource bounds suitable for high message rates,
   many channels, and 100,000-member groups;
9. remove the continuous-capture, dual-repository, Generation, audit, repair,
   source-fence, permanent-erasure, CLI, and release-qualification systems;
10. keep the implementation inside the documented
    `access -> usecase/runtime -> infra/pkg` dependency directions.

## 3. Non-goals

The first version will not provide:

- continuous capture or incremental backup chains;
- five-minute recovery points;
- multiple active backup plans;
- multiple application-managed repository copies;
- application-level backup encryption;
- signed manifests or protection against an attacker who can rewrite both
  data and manifests;
- provider-specific WORM, cross-region repair, or object-version repair;
- periodic full repository audits;
- permanent-erasure replay across historical restore points;
- partial backups;
- degraded restore activation with missing replicas;
- backup archive download as one large file;
- restore-triggered replay of Webhooks, plugins, pushes, or notifications;
- backup or restore commands in `wkcli`;
- compatibility with the existing continuous-backup artifact or Controller
  state formats;
- provider-specific release qualification or commit-bound binaries.

## 4. Operator Model

### 4.1 One plan per cluster

Each cluster has at most one `BackupPlan`. It contains:

- enabled/disabled scheduling state;
- one repository configuration;
- one five-field Cron expression;
- one explicit IANA time zone;
- successful-backup retention count;
- per-node throughput limit;
- per-node Slot-worker count;
- maximum job duration.

There is no TOML or environment override for this plan.

### 4.2 Manager authentication

Manager authentication MUST be enabled before any backup mutation is allowed.
When Manager authentication is disabled, backup routes are read-only.

Permissions are:

- `cluster.backup:r`: status, plan, job history, archive inventory, and archive
  detail;
- `cluster.backup:w`: plan mutation, connection test, immediate backup,
  cancellation, verification, hold/release, and deletion;
- `cluster.restore:w`: restore start and cancellation, with password
  reauthentication and typed archive-name confirmation. A wildcard permission
  does not imply this capability.

Every mutation is written to the existing management audit system without
credentials or object payloads.

### 4.3 Enable and disable

Enabling a plan performs:

1. request validation;
2. repository connection and shared-path validation;
3. repository identity validation;
4. durable plan publication through Controller CAS;
5. immediate first full backup.

If the first backup fails, the plan remains enabled and visibly unhealthy so
the next Cron occurrence can try again.

Disabling a plan only stops future Cron triggers. It does not:

- cancel an active job;
- remove repository credentials;
- delete archives;
- disable immediate manual backup;
- disable verification or restore.

An active job has a separate explicit cancel action.

## 5. Scheduling

### 5.1 Cron

The UI provides presets for common schedules, including daily and every 12
hours, plus an advanced five-field Cron editor:

```text
minute hour day-of-month month day-of-week
```

Seconds are unsupported. The server validates across a five-year horizon that
consecutive occurrences are at least 12 hours apart. Every plan stores an IANA
time zone such as `Asia/Shanghai`; node operating-system time zones never
affect scheduling. Manager shows the next occurrence for the saved plan.

### 5.2 Trigger ownership

Only the Controller Leader evaluates Cron. A scheduled occurrence has a stable
idempotency key derived from the plan revision and its UTC occurrence.
Controller CAS admits at most one job for that occurrence across Leader
changes.

The scheduler:

- does not catch up occurrences missed while the cluster was stopped;
- does not queue an occurrence when another backup is active;
- records a bounded `skipped` task result for an overlapping occurrence;
- disables the immediate-backup action while a backup is active;
- resumes one already-active backup after Controller Leader failover.

### 5.3 Health

A failed occurrence creates an immediate warning. Failure to produce a
successful backup across two consecutive expected occurrences creates a
critical stale-backup warning.

## 6. Repository Model

### 6.1 Exactly one repository

One active plan targets exactly one of:

1. a fixed file repository; or
2. an S3-compatible repository.

WuKongIM does not coordinate a secondary repository. Operators may configure
replication, versioning, WORM, and storage-side encryption in the storage
service.

### 6.2 File repository

The file repository path is fixed:

```text
<node.data_dir>/backup-repository
```

It is never supplied through Manager.

For a single-node cluster, this path SHOULD be a separately protected
persistent volume. For a multi-node cluster, every node MUST mount the same
shared NAS/NFS repository at its own fixed path.

Before enablement, every active node participates in a nonce-based probe that
proves all nodes observe the same repository. The probe creates, reads, and
deletes bounded test objects. Enablement fails if visibility or permissions
differ.

The repository directory is excluded from data-directory activation and
cleanup. Restore switches only product database directories, never the
repository mount.

### 6.3 S3-compatible repository

Manager configures:

- endpoint;
- region when required;
- bucket;
- prefix;
- access key;
- secret key;
- path-style addressing when required.

The UI exposes the secret only while it is entered. Manager responses, audit
records, logs, task errors, and diagnostics MUST NOT return it.

Non-secret configuration and an encrypted credential blob are stored in
Controller state. The credential-encryption key is derived from the existing
Manager installation secret and the cluster identity. If that installation
secret changes and the credential can no longer be decrypted, backup becomes
disabled-by-error until an administrator replaces and retests the credential.

There is no workload-role, repair-role, garbage-role, or provider-specific RAM
model in `v1`.

### 6.4 Repository binding

One repository prefix is bound to one source cluster identity. Its root marker
contains:

- repository format and version;
- source cluster identity;
- Hash Slot count;
- creation time.

A cluster with another identity cannot write to or restore from the prefix.
Repository identity adoption by a separately bootstrapped cluster is outside
`v1`; replacement deployments must be bootstrapped with the same cluster
identity before using the repository.

Different clusters require different S3 prefixes or different shared
directories.

## 7. Artifact Format

### 7.1 Visibility

Manager presents one archive, but the repository stores bounded per-Slot
parts:

```text
repository.json
catalog/<backup-id>
pending/<backup-id>
backups/<backup-id>/
  manifest.json
  slots/000/attempts/<attempt>/manifest.json
  slots/000/attempts/<attempt>/meta-000001.zst
  slots/000/attempts/<attempt>/messages-000001.zst
  ...
  slots/255/attempts/<attempt>/manifest.json
  ...
  COMPLETE
  HOLD
  CORRUPT
```

`HOLD` and `CORRUPT` are optional control objects and are not covered by the
immutable payload manifest. The bounded operator note is stored in `HOLD`.

An archive is discoverable only after `COMPLETE` exists and matches the
SHA-256 of `manifest.json`. Objects below a backup ID without a valid
`COMPLETE` marker are incomplete job output and MUST NOT be offered for
restore.

`catalog/<backup-id>` is a compact internal index whose body is the immutable
`COMPLETE` marker. It keeps S3 inventory listing bounded without becoming a
Manager-facing domain concept. `pending/<backup-id>` records incomplete
publication so terminal failures and 72-hour orphan cleanup can delete only
the exact job prefix. Publication retry verifies immutable archive identity
and repairs a missing index entry without rewriting a completed manifest.

Neither S3 nor file publication depends on directory rename. Producers write
attempt-scoped immutable objects, the top-level manifest selects one attempt
per Hash Slot, and readers use `COMPLETE` as the visibility contract.

### 7.2 Top-level manifest

The versioned top-level manifest records:

- backup ID and trigger kind;
- format version and source WuKongIM version;
- source cluster identity and Hash Slot count;
- start, completion, and effective cut range;
- total stored bytes, logical bytes, objects, and records;
- one entry for each of the 256 Slot manifests;
- required schema versions and allocator high-water marks;
- compression and checksum algorithms.

The top-level manifest contains only 256 Slot references. It does not contain
per-channel or per-record entries.

### 7.3 Slot manifest and chunks

Each Slot manifest records:

- Hash Slot ID;
- the exact logical cut used for metadata and committed messages;
- source Slot term/configuration evidence needed to reject stale capture;
- logical record and byte totals;
- channel/message-boundary summaries needed for restore validation;
- ordered metadata and message chunk descriptors;
- compressed size, logical size, and SHA-256 for every chunk.

Chunks use a fixed maximum uncompressed size of 64 MiB and fixed Zstandard
compression. Compression level and algorithm are not Manager settings.

Chunk readers and writers stream. A worker MUST NOT buffer a whole Slot or
archive in memory. The memory budget is bounded by a small fixed number of
chunks per worker.

### 7.4 Integrity boundary

SHA-256 detects missing, truncated, corrupted, and misordered data. It does not
authenticate the repository against a malicious writer. There is no signing
key, key package, root pin, or signature chain.

Backup payloads are not encrypted by WuKongIM. Manager MUST show that fact near
repository configuration and recommend storage access control and storage-side
encryption.

### 7.5 Compatibility

The replacement starts at `backup format v1` and rejects every existing
continuous-backup artifact.

A newer binary may read older artifacts within the same supported backup-format
major version through explicit readers. An older binary never reads a newer
format. Unsupported format or schema combinations fail during preflight before
maintenance or target writes.

## 8. Online Full-backup Flow

### 8.1 Cluster cut

The Controller Leader creates one durable `BackupJob` and dispatches every Hash
Slot to its current Slot Leader. Each Slot Leader:

1. validates current Slot authority;
2. opens storage-engine snapshots for the Slot's business metadata and
   committed message state;
3. freezes the Slot's logical cut in its job attempt;
4. streams metadata and message chunks through Zstandard and SHA-256;
5. writes or idempotently replaces deterministic chunk names;
6. publishes the Slot manifest;
7. reports the verified Slot result through a fenced Controller transition;
8. closes the storage snapshots promptly.

The 256 Slots do not need one wall-clock instant. The archive is a valid
vector cut containing one immutable cut per logical Slot. Cross-Slot
transactions are not introduced by backup.

Business reads and writes remain online. Backup must not route around Raft or
read uncommitted state.

### 8.2 Job publication

The Controller Leader publishes the top-level manifest only after all 256 Slot
results are complete and healthy. It then verifies the referenced objects and
writes `COMPLETE`.

If any Slot is missing, unhealthy, authority-stale, or checksum-invalid, the
whole job fails. No partial archive is published.

### 8.3 Concurrency and pressure

Defaults are:

- one Slot export worker per node;
- maximum four workers per node;
- 50 MiB/s read/upload limit per node;
- 12-hour maximum job duration;
- configurable maximum duration from 1 through 48 hours.

These values are Manager plan settings except the hard worker maximum.

Backup uses low-priority bounded queues. It may temporarily yield when existing
storage/queue overload signals cross hard safety waterlines, but `v1` has no
closed-loop SENDACK latency controller or continuous performance state
machine.

An active backup and an operator-initiated topology mutation are mutually
exclusive. Natural Leader failover is allowed. A failed Slot attempt restarts
on the new Leader under a new fence.

### 8.4 Resumption

Controller state contains at most one fixed record per Hash Slot for the active
job. After Leader or process restart, the new coordinator:

1. reloads the active job;
2. validates already-published Slot manifests and chunks;
3. preserves valid completed Slots;
4. redispatches missing or invalid Slots;
5. continues publication.

There are no durable per-channel cursors, source-log pins, Generations, or
incremental segment chains.

### 8.5 Cancellation and failure

Upload, download, and Slot snapshot requests retry transient failure at most
three times. If a Slot remains unsuccessful, the job fails rather than
automatically queueing a new full job.

Backup may be canceled at any time. Failure, cancellation, and timeout:

- prevent `COMPLETE` publication;
- release storage snapshots and workers;
- clean incomplete objects best-effort;
- record one bounded task result;
- leave the next Cron occurrence eligible.

Incomplete prefixes that survive a process failure are removed by a small
age-based orphan cleanup performed before a later job. This cleanup is not a
Generation garbage collector.

## 9. Retention and Archive Operations

### 9.1 Retention

The default is the seven most recent successful, unheld archives.

Retention runs only after a new archive successfully publishes. It:

1. lists complete archives from the repository;
2. excludes held archives and an archive used by an active restore;
3. sorts remaining archives by completion time;
4. deletes archives beyond the configured count.

The newest valid archive is never automatically deleted. Failed jobs do not
create retained archives.

### 9.2 Holds and notes

An administrator may add a short note and create or remove `HOLD`. Held
archives are excluded from automatic retention and do not count toward the
retention number.

### 9.3 Manual deletion

Manager prevents deletion when an archive:

- is the last complete usable archive;
- is held;
- is being verified;
- is selected by an active restore.

Other deletion requires `cluster.backup:w` and explicit confirmation.

### 9.4 Verification

There is no periodic repository audit.

Verification occurs:

1. before initial `COMPLETE` publication;
2. on an explicit Manager verification job;
3. while restore streams every object into staging.

Manual verification reads every referenced object. On failure it writes a
bounded `CORRUPT` marker when possible and marks the archive unusable.
WuKongIM does not repair it from another repository. Restore always verifies
again and does not trust a historical verification result.

## 10. Restore

### 10.1 User experience

Restore is one guided operation:

1. select an archive;
2. view its identity, source cluster, effective cut range, size, and data-loss
   warning;
3. reenter the current administrator password;
4. type the exact confirmation;
5. confirm; the server completes preflight before admitting maintenance;
6. observe one continuous progress view.

The administrator does not separately plan, start, verify, or activate.

### 10.2 Preflight

Preflight runs before maintenance and verifies:

- Manager authentication and super-administrator identity;
- no active backup, restore, Rebalance, expansion, or shrink operation;
- all current Controller and desired data replicas are reachable and healthy;
- repository identity and access;
- a valid `COMPLETE` marker and top-level manifest;
- `backup format v1`, supported schemas, and exactly 256 Hash Slots;
- target topology capacity;
- enough free space for the current business data, twice the archive logical
  bytes, and 1 GiB of headroom on every active data node;
- matching current-cluster and repository lineage;
- current Manager backup configuration and credentials remain outside the
  restored payload.

Preflight MUST fail before maintenance when any configured replica is missing.
There is no degraded or break-glass activation.

### 10.3 Maintenance mode

After confirmation, Controller durably enters restore maintenance mode.

Maintenance mode keeps available:

- Controller and internal recovery RPC;
- the Manager control plane;
- restore audit;
- liveness and restore metrics.

It stops or rejects:

- Gateway client traffic;
- all business HTTP writes and reads;
- delivery and push;
- Webhooks;
- plugin side effects;
- scheduled and manual backup starts.

All current client connections are disconnected. Business readiness reports
maintenance without causing process liveness failure.

### 10.4 Staging

Each target node uses a restore-specific staging root below `node.data_dir`
that cannot overlap active product data or the fixed backup repository.

For each logical Slot:

1. the current Slot Leader streams and verifies the portable Slot artifacts;
2. it decodes them into restore-scoped, immutable staged logical streams;
3. it produces a bounded installation receipt;
4. every current replica independently stages the exact repository objects;
5. every configured replica replays the staged streams through semantic
   validators and reports the same verified logical result.

The implementation SHOULD reuse generic streaming Hash-Slot snapshot readers,
writers, and Raft snapshot installation primitives. It MUST NOT replay rows
through ordinary business APIs.

Restore state contains a fixed record per Slot and is resumable after
Controller Leader or node process restart.

### 10.5 Verification

Before switching, restore verifies:

- every chunk size and SHA-256;
- metadata and message record totals;
- every Slot ID and manifest relationship;
- channel message ordering and committed boundaries;
- allocator high-water marks;
- all current desired replicas;
- absence of ordinary side-effect execution.

All configured replicas must finish. Quorum-only activation is unsupported.

### 10.6 Switch and rollback

Controller persists the `switching` phase before logical activation.
Every node then:

1. fsyncs staged streams and durable switching markers;
2. keeps a verified, restore-scoped rollback stream for each current replica;
3. imports the staged streams directly into the storage engines while
   maintenance fences all business visibility;
4. reloads durable runtimes without stopping Manager control;
5. reports health through a fenced transition.

If any node fails switching, startup, or health checks, Controller commands
every switched node to import its verified rollback streams and resumes the
original business state.

Cancellation is allowed only before `switching`. Once switching begins, the
operation must finish or roll back automatically.

After every configured replica and business runtime is healthy:

- the restore job succeeds;
- original and staging directories are deleted;
- the maintenance fence is removed;
- existing Manager sessions are invalidated;
- automatic backup resumes at the next Cron occurrence.

There is no long-lived local rollback copy. An administrator who later wants
another state performs another formal restore.

### 10.7 Restored semantics

Restore is exact point-in-time recovery:

- data deleted after the selected backup may reappear;
- data created after the selected backup is lost;
- client authentication tokens return to their backed-up values;
- current client connections are not restored;
- Manager users, JWT settings, repository credentials, and backup plan are not
  restored;
- deployment topology, node addresses, local paths, logs, metrics, plugin
  binaries, and TOML are not restored;
- historical Webhooks, plugin calls, pushes, and notifications are never
  replayed.

The current cluster must match the repository lineage. Node count and replica
placement may change before restore; separately bootstrapped cluster identity
adoption is outside `v1`.

## 11. Bounded Controller State

The replacement Controller model contains only:

```text
BackupPlan
ActiveBackupJob?
ActiveRestoreJob?
TaskHistory[<=100]
MaintenanceFence?
ActiveArchiveOperation?
```

`BackupPlan` includes a monotonically increasing revision and encrypted
credential revision.

`ActiveBackupJob` contains:

- ID, trigger, plan revision, scheduled occurrence, owner fence, and deadline;
- one fixed status per Hash Slot;
- aggregate byte/record progress;
- phase and sanitized last error.

`ActiveRestoreJob` contains:

- ID, archive identity, source lineage, initiator audit identity, and owner
  fence;
- maintenance/switch phase;
- one fixed installation status per Hash Slot;
- per-node switch/health acknowledgements;
- aggregate progress and sanitized last error.

`TaskHistory` keeps the latest 100 terminal scheduled-backup, manual-backup,
verification, retention, and restore records. Repository manifests, object
names, channel cursors, and archive history do not enter Controller state.

Repository inventory is rebuilt from complete repository manifests after
Controller loss.

## 12. Package Boundaries

### 12.1 `internal/access/manager`

Owns:

- request validation and JSON mapping;
- permission checks and restore reauthentication;
- plan, job, archive, verification, deletion, and restore routes;
- sanitized error mapping.

It contains no scheduling, retention, snapshot, or restore rules.

### 12.2 `internal/access/node`

Owns bounded internal RPC adapters for:

- repository probes;
- Slot snapshot/export dispatch;
- staged restore installation and replica transfer;
- switch, rollback, cleanup, and health acknowledgements.

RPC DTOs contain no UI or concrete S3 SDK types.

### 12.3 `internal/usecase/backup`

Owns:

- plan validation and revision transitions;
- one-job admission;
- scheduling decisions independent of the Cron runtime;
- archive publication admission;
- retention decisions;
- hold/delete/verify rules;
- restore preflight and lifecycle transitions;
- bounded status projection.

It depends only on narrow ports and `internal/contracts/backup`.

### 12.4 `internal/runtime/backup`

Owns:

- Controller-Leader Cron loop;
- active-job reconciliation;
- bounded per-node Slot worker execution;
- rate limiting and overload yielding;
- Leader-failover resumption;
- restore reconciliation.

It does not encode repository artifacts or own business policy.

### 12.5 `internal/infra/backup`

Owns:

- fixed file and S3-compatible store adapters;
- encrypted credential persistence adapter;
- logical metadata/message Slot snapshot adapters;
- staged import and replica installation;
- filesystem switch, rollback, and cleanup;
- Controller-state port adapters.

### 12.6 `pkg/backup`

Becomes a small portable artifact library containing only:

- format constants;
- repository and manifest types;
- canonical JSON/framing;
- chunk compression/checksum streams;
- validation helpers.

It contains no scheduler, Controller state, cloud provider, repair, audit,
Generation, key package, or restore coordinator.

### 12.7 `internal/app`

Remains the only composition root. It wires Manager, usecase, runtime, stores,
node RPC, snapshot adapters, lifecycle hooks, metrics, and maintenance
behavior. Cross-layer behavior receives an app wiring test.

## 13. Manager API

The target Manager surface is:

```text
GET    /manager/backups
GET    /manager/backups/archives/{backup_id}
PUT    /manager/backups/plan
POST   /manager/backups/repository/test

POST   /manager/backups/jobs
POST   /manager/backups/jobs/{job_id}/cancel

PUT    /manager/backups/archives/{backup_id}/hold
POST   /manager/backups/archives/{backup_id}/verify
DELETE /manager/backups/archives/{backup_id}

POST   /manager/backups/archives/{backup_id}/restore
POST   /manager/backups/restores/{job_id}/cancel
```

`POST /manager/backups/jobs` creates an immediate full backup. There is no
generic job kind supplied by the browser.

Plan mutation uses an expected revision. Controller CAS and active-job
identity make job admission retry-safe. Archive publication itself is
idempotent. Archive deletion carries JSON confirmation text that exactly
matches `DELETE <backup-id>`.

Errors use stable bounded codes such as:

```text
backup_auth_required
backup_permission_denied
backup_plan_conflict
backup_job_active
backup_restore_active
backup_store_unreachable
backup_repository_mismatch
backup_archive_not_found
backup_archive_incomplete
backup_archive_corrupt
backup_archive_held
backup_last_archive
backup_cluster_unhealthy
backup_topology_busy
backup_capacity_insufficient
backup_format_unsupported
backup_restore_not_cancelable
```

## 14. Manager UI

The existing continuous-checkpoint and recovery-plan screens are replaced by
one `Backup and Restore` area.

### 14.1 Overview

Shows:

- enabled state and health;
- latest successful backup;
- next Cron occurrence and time zone;
- active task progress;
- single-failure and stale-backup warnings;
- current repository type;
- retained archive count and stored size.

### 14.2 Setup wizard

Steps:

1. choose fixed file storage or S3;
2. enter S3 values when selected;
3. test repository;
4. choose preset or custom Cron and time zone;
5. choose retention, speed, workers, and timeout;
6. review the plaintext-backup warning;
7. enable and start the first backup.

### 14.3 Archive list

Each row shows:

- ID/name and note;
- scheduled, manual, or initial trigger;
- effective cut range and completion time;
- logical/stored sizes and duration;
- complete, held, corrupt, verifying, or restoring status;
- verify, hold/release, delete, and restore actions.

There is no checkpoint, Generation, internal catalog, audit-debt, repair,
erasure, KMS, or repository-copy terminology in Manager.

### 14.4 Restore progress

The progress page remains available in maintenance mode and shows:

- current phase in operator language;
- completed/total Slots;
- verified bytes;
- replica installation progress;
- switch/health state per node;
- cancel availability;
- sanitized error and automatic rollback state.

## 15. Metrics and Logs

Metrics use bounded labels only:

- plan enabled;
- current job phase and trigger;
- job duration and terminal result;
- bytes read/written and current rate;
- Slot totals/completed/failed;
- latest successful backup timestamp and age;
- skipped occurrences;
- repository operation failures by bounded category;
- restore phase, Slot progress, rollback count, and terminal result.

No metric label contains a backup ID, object key, endpoint, bucket, prefix,
channel ID, user ID, or credential.

Structured logs may include backup/job ID and Slot ID but never repository
credentials or payload data.

Backup failure does not make the messaging service unready. Restore maintenance
does make business readiness false while keeping liveness and Manager healthy.

## 16. Removal and Reuse Plan

This is a replacement, not a compatibility layer.

### 16.1 Delete

Delete the old semantics and their tests from:

- continuous/incremental artifacts, catalogs, Generations, segment chains,
  erasure ledgers, signatures, key packages, and dual-store repair in
  `pkg/backup`;
- continuous coordinator, rolling capture, rebase, audit, and old restore
  coordinator in `internal/runtime/backup`;
- checkpoint, catalog, Generation retention, source fence, permanent erasure,
  and old restore-plan usecases in `internal/usecase/backup`;
- OSS/RAM provider qualification, dual repository, audit, repair, Generation
  GC, source pins, old staging/activation evidence, and old restore adapters in
  `internal/infra/backup`;
- old continuous backup contracts in `internal/contracts/backup`;
- old Manager checkpoint/catalog/recovery endpoints and UI;
- old node continuous-capture and recovery RPC;
- backup-specific continuous capture, pin, cursor, and metadata-index paths in
  `pkg/cluster`;
- existing Controller backup/restore coordination state and commands;
- old backup metrics;
- `cmd/wkcli/internal/backup` and its command wiring;
- `[backup]` configuration schema, `WK_BACKUP_*`, and
  `wukongim.toml.example` backup sections;
- `.github/workflows/backup-qualification.yml` and its workflow contract tests;
- qualification stamps and backup-specific build gates in `internal/app`;
- backup qualification and old checkpoint flags in three-node scripts;
- old continuous-backup E2E suites and provider qualification fixtures;
- superseded backup design, plan, development guide, and FLOW descriptions.

Do not leave deprecated endpoints, hidden config fallbacks, format readers, or
dead state fields.

### 16.2 Rebuild in place

Rebuild the package directories rather than adding parallel `v2` packages:

- `pkg/backup`;
- `internal/contracts/backup`;
- `internal/usecase/backup`;
- `internal/runtime/backup`;
- `internal/infra/backup`;
- Manager and node backup adapters;
- Controller backup state;
- web backup pages;
- backup metrics and app wiring.

Domain-facing names should describe scheduled full backup, not preserve
`continuous`, `checkpoint`, or `generation` vocabulary. A repository-internal
`catalog/` object prefix is allowed solely as the bounded publication index
defined in section 7.1.

### 16.3 Candidate reusable primitives

Retain only primitives that remain independently valid after focused review:

- streaming Hash-Slot metadata snapshot/export/import in `pkg/db/meta`;
- streaming message backup snapshot/import in `pkg/db/message`;
- generic Slot/Raft snapshot installation and replica transfer primitives;
- generic Manager authentication, permission, audit, and UI components;
- generic goroutine supervision and bounded work queues;
- the existing E2E process harness.

Reusable database methods may be renamed or narrowed. Backup-specific behavior
that invalidates tokens, assumes old manifests, accepts old evidence, or
depends on continuous cursors must be removed.

Every touched package FLOW must be rewritten in the same change that changes
its behavior.

## 17. Validation

### 17.1 Unit tests

Cover:

- Cron/time-zone parsing, next occurrences, 12-hour minimum, no catch-up, and
  overlap skip;
- plan revision and encrypted credential handling;
- repository binding and shared-file probes;
- manifest/chunk round trips, truncation, corruption, and missing objects;
- all-256-Slot publication admission;
- fixed Controller-state bounds;
- retention, holds, last-archive protection, and orphan cleanup;
- job retry, cancellation, timeout, and failover resumption;
- restore transitions, cancellation fence, switch failure, rollback, and
  cleanup;
- permission, reauthentication, redaction, and Manager API mappings;
- web setup, status, archive, and restore flows.

Tests use injected clocks and do not wait for real Cron time.

### 17.2 Integration tests

Integration-tagged tests cover:

- real filesystem snapshot/switch/rollback;
- S3-compatible storage through an isolated test service;
- multi-process Manager maintenance continuity;
- node and Controller Leader restarts during backup and restore;
- bounded streaming and cancellation cleanup.

### 17.3 E2E

Process-level E2E covers:

1. single-node cluster manual full backup and in-place restore;
2. three-node online backup while clients continue sending;
3. Slot Leader and Controller Leader failover with job resumption;
4. retention, hold, manual verification, corruption isolation, and deletion;
5. restore after a healthy node-placement change with the same cluster
   identity and 256 Hash Slots;
6. restore failure before switch;
7. node switch/start failure with automatic rollback;
8. post-restore client reconnect, sync, and send;
9. proof that restored historical data emits no Webhook, plugin, or push side
   effects;
10. rejection of a repository bound to another cluster identity.

### 17.4 Performance and safety gates

At representative 256-Slot load, validate:

- configured per-node rate and worker bounds;
- bounded heap proportional to worker count, not archive size;
- bounded Controller state independent of channel count;
- no backup-created SEND errors or queue-full disconnects;
- foreground SENDACK P99 regression within the agreed online-backup budget;
- staging capacity rejection before maintenance;
- no repository path deletion during restore cleanup.

There is no provider-specific 1-TB release qualification workflow, but
repeatable performance tests remain part of normal engineering validation.

## 18. Acceptance Criteria

The replacement is complete only when:

1. an authenticated administrator can configure and enable backup entirely in
   Manager;
2. enabling produces an immediate first full archive;
3. Cron runs in the selected time zone and never overlaps jobs;
4. business traffic remains available throughout backup;
5. every visible archive is independently restorable without predecessor
   artifacts;
6. repository loss/corruption produces bounded visible failures without
   affecting message-service readiness;
7. an administrator with explicit `cluster.restore:w` can restore through one
   Manager confirmation flow;
8. Manager remains available during maintenance;
9. switch failure automatically restores the original data;
10. successful restore activates only after every configured replica is
    healthy;
11. the old continuous-backup format, state, configuration, CLI, workflows,
    UI, tests, and dead code are absent;
12. applicable FLOW documents and the stable project knowledge are updated;
13. focused unit, integration, E2E, and web tests pass.
