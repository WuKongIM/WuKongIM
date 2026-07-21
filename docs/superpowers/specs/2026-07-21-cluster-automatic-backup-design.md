# Cluster Automatic Backup Design

**Date:** 2026-07-21

## Status

Approved through an explicit design interview. Implementation is gated behind
`backup.enabled = false` until the restore path, three-node end-to-end tests,
and performance gates are complete.

## Problem

WuKongIM has node-local storage snapshots and the offline `wkdb export/import`
bundle, but it does not have a cluster-coordinated production backup. The WKDB
bundle intentionally scans one stopped node, omits cluster/runtime state, has no
online consistency boundary, and cannot satisfy a five-minute recovery point
objective.

A production backup must survive loss of every WuKongIM node and its disks. It
must remain outside the SEND/SENDACK synchronous path, preserve cluster
semantics for single-node and multi-node deployments, and restore business data
into a new cluster whose node identities and topology may differ from the
source.

## Goals

1. Publish an all-or-nothing cluster restore point no more than five minutes
   behind the oldest included logical partition.
2. Restore 1 TB of durable business data into a documented three-node target in
   no more than 60 minutes on the qualified hardware/network profile.
3. Run full and incremental capture online without a global write pause.
4. Keep backup work lower priority than foreground message work and report an
   explicit RPO violation rather than blocking SEND/SENDACK.
5. Restore into a fresh cluster with the same `hash_slot_count`, while allowing
   different node IDs, node count, Slot placement, and Channel leadership.
6. Store encrypted, signed, immutable copies in explicit primary and secondary
   S3-compatible repositories in different failure domains.
7. Prove recoverability through cryptographic verification, semantic checks,
   and scheduled restore drills.

## Non-goals

- Reusing WKDB Import Bundle v1 as the production backup format.
- Copying old Pebble directories, Raft WALs, Controller state, runtime leases,
  migration tasks, caches, online connections, or node identities.
- Backing up `NoPersist` messages, logs, Prometheus TSDB data, diagnostics,
  plugin executables, plugin-owned external state, or externally stored media.
- Replaying historical webhook, online-status, or plugin side effects after a
  restore.
- Merging or overwriting a non-empty target cluster.
- Changing `hash_slot_count` as part of restore.
- Providing FIPS or national-cryptography certification in v1.
- Provisioning cloud infrastructure for restore drills from WuKongIM itself.
- Dynamically editing backup policy or credentials through Manager.

## Recovery Contract

The restored cluster is a new cluster generation. It restores durable business
semantics, not the old runtime image.

The backup includes:

- users and devices, including application login tokens by default;
- channels, subscribers, allow/deny lists, membership, and plugin bindings;
- durable messages, message idempotency state, committed sequence boundaries,
  retention fences, and deletion tombstones;
- conversations, read positions, channel-latest records, and durable message
  event projections required to meet the RTO;
- allocator fences needed to keep new message IDs and sequence numbers above
  every restored value;
- an independent permanent-erasure ledger that is applied after every selected
  historical restore point.

The backup excludes runtime leadership, leases, write fences that belong to the
old generation, in-flight migrations, queues, ephemeral stream buffers, and
external effects. Absolute business expiry timestamps remain absolute; downtime
does not extend them.

Application tokens remain valid after restore unless the operator selects the
explicit token-invalidation recovery option. Operational credentials, JWT
signing secrets, TLS keys, repository credentials, KMS credentials, plugin
packages, and deployment configuration are restored from external secret/IaC
systems. The manifest records only redacted compatibility metadata and artifact
digests.

## Consistency Model

A restore point is a logical cluster cut:

- every metadata partition records a committed Slot Raft index;
- every included channel records a committed HW and retention boundary;
- only data at or before those cuts is included;
- the restore point time is the oldest included partition watermark, never the
  manifest publication time;
- every required logical partition and channel object must be present before
  the top-level manifest is published.

Publishing a restore point may acquire a control-plane topology barrier for at
most five seconds. The barrier prevents migration phase changes, membership
changes, and intentional leader transfers while the ownership map is captured;
it never pauses ordinary message writes.

Incremental capture consumes existing committed Slot and Channel logs. It does
not synchronously write a second backup log on the message hot path. Backup
cursors may delay compaction only within a configured time and disk budget. If
the cursor falls behind the retained log, the incremental chain is invalidated,
normal compaction proceeds, and the coordinator requires a new full baseline.

Deletion tombstones and `RetentionThroughSeq` advances are first-class changes.
They remain available until no retained restore point depends on the older
state. Explicit permanent erasures are applied from the independent signed
ledger even when restoring a point created before the erasure.

## Backup Lifecycle

The Controller Leader is the single backup coordinator. Controller-replicated
state contains the backup epoch, job state, per-partition completion summaries,
and published restore-point reference; it does not embed large data manifests.
Leader failover resumes work using immutable, idempotent object keys.

The default lifecycle is:

1. Continuously read committed changes and seal upload segments after one minute
   or the configured size limit.
2. Publish a verified restore point at least every five minutes.
3. Build one synthetic full manifest per day, reusing immutable verified chunks
   while remaining independent of older incremental manifests.
4. Re-read and re-materialize all data once per month with current encryption
   keys.
5. Defer a new full baseline while topology migrations, scale operations,
   Controller work, or required quorum health are unstable. Incremental capture
   continues.

One failed node does not prevent publication when every logical partition has a
healthy authoritative replica. A missing logical partition does. Mixed backup
protocol versions during a rolling upgrade pause publication until all required
nodes agree on one supported format.

Uploaded but unpublished objects are harmless orphans and are reclaimed only
after an object-lock-safe grace period. Cancellation never publishes a partial
manifest.

## Artifact Format

The cluster backup format is separate from WKDB Import Bundle v1. It is a
versioned binary/framed format organized by logical hash slot and channel, not
by source node directory.

Each immutable object is compressed before encryption. A fresh data key and
AEAD nonce protect each object; the data key is wrapped by an external KMS. The
signed canonical manifest records:

- format and application compatibility versions;
- source cluster/repository identity and generation;
- `hash_slot_count`;
- restore point effective time and backup epoch;
- logical partition cuts, Channel HW/retention summaries, and allocator fences;
- encrypted object locations, sizes, content hashes, encryption metadata, and
  primary/secondary verification status;
- plugin/config compatibility digests and required external dependencies;
- permanent-erasure ledger boundary;
- signature algorithm, key ID, and signature.

The latest binary restores its current format and the immediately preceding
format through explicit forward migrations. Newer formats never restore into an
older binary. Unsupported combinations fail before target writes begin.

## Repository And Key Security

Production requires two explicit S3-compatible repositories in different
regions/failure domains. Provider-native replication may optimize transfer, but
the coordinator independently verifies the secondary object and manifest before
advancing the cross-region recovery watermark.

Production repositories require TLS, versioning, immutable object names, and at
least seven days of Object Lock/WORM. Upload identities cannot delete. Retention
garbage collection uses a separate restricted identity. A restore point may be
placed on audited hold; releasing a hold never bypasses an active object lock.

Credentials use workload identity, instance roles, or OIDC where available.
Static credentials may come only from environment variables or mounted secret
files. TOML, Manager responses, logs, diagnostics, and manifests never contain
credential values.

KMS key rotation affects only newly created objects. Old decrypt and signature
key versions remain available until no retained restore point references them.
The top-level canonical manifest is digitally signed so an attacker with object
write permission cannot replace both data and checksums undetected.

## Restore Lifecycle

Restore is initiated only through `wkcli backup` against a cluster started in
explicit restore mode.

Restore mode starts Controller/Slot/Channel recovery, restricted Manager APIs,
metrics, and audit. Gateway traffic, ordinary business writes, webhook delivery,
and plugin side effects remain disabled.

The lifecycle is:

1. `restore plan` requires an explicit restore-point ID or explicit
   `--latest-verified`, validates signature/compatibility, confirms the target is
   empty, and estimates disk, network, replica, and time requirements.
2. `restore start` records an immutable plan and installs metadata through Slot
   Raft snapshots and message segments through restore-only Channel snapshot
   installation. It never copies old storage directories or proposes every row
   through ordinary business APIs.
3. Failed work resumes idempotently by plan and partition. An incompatible or
   untrusted target is marked abandoned; WuKongIM never automatically erases the
   target.
4. `restore verify` fully verifies signatures, AEAD, hashes, record counts,
   ownership, message sequence continuity, committed HW, retention boundaries,
   allocator fences, and permanent erasures.
5. Activation requires healthy Slot quorum and verified Channel data at least to
   configured MinISR. Remaining replicas may catch up after activation while the
   cluster reports degraded redundancy.
6. The operator must prove the old cluster is fenced, review the report, and run
   `restore activate`. DNS changes alone do not prove fencing.

## Retention

The default restore-point policy is:

- five-minute points for 24 hours;
- hourly points for seven days;
- daily points for 30 days;
- monthly points disabled by default, configurable to 12 months.

Garbage collection operates from signed manifest references and never deletes
an object still referenced by a retained or held restore point.

## Control And Observability

`wkcli backup` is the single operational CLI. Online commands include
`status`, `list`, `trigger`, `cancel`, `verify`, `hold`, and `release`. Restore
commands include `plan`, `start`, `status`, `verify`, and `activate`.

Manager provides status, RPO, restore-point inventory, job progress, failure
reason, manual backup/verify/cancel actions, and read-only policy display. It
never edits credentials or initiates restore.

Permissions are:

- `cluster.backup:r` for status and reports;
- `cluster.backup:w` for trigger, cancel, and verify;
- repository deletion and restore activation remain outside ordinary Manager
  permissions.

Every manual action, job transition, retention action, recovery plan, verify,
and activation is audited without secret or plaintext data. Prometheus exposes
bounded labels only. `backup_recovery_point_age_seconds > 300` is an RPO
violation. Missing evidence is `unknown`, never healthy.

Backup failure does not make the message service unready. When repository or KMS
access fails, foreground service continues, backup becomes degraded/failed, and
the RPO breach is visible through Manager, metrics, logs, and Alertmanager.

## Performance Budget

- Incremental capture: no more than 3% throughput loss and 5% SENDACK P99
  increase.
- Full capture: no more than 5% throughput loss and 10% SENDACK P99 increase.
- No new SEND errors, queue-full disconnects, or durability weakening.
- Backup automatically reduces concurrency and bandwidth when the budget is
  exceeded.
- Full data streams directly through compression/encryption/multipart upload.
  `node.data_dir` never receives a complete backup copy. Production uses a
  separate bounded `backup.staging_dir` with a hard safety waterline.

## Verification And Drills

Every restore point verifies both repositories, manifest signature, encryption
metadata, and object hashes before publication. A daily remote audit checks
object availability. A weekly external job provisions an isolated cluster and
runs `wkcli backup drill`; WuKongIM owns restore/verification/reporting but not
cloud resource creation.

The implementation is complete only after real three-node E2E covers online
full/incremental capture, leader failover, repository interruption, deletion and
retention, different target topology, corruption rejection, and post-restore
client sync/send. A nightly/manual qualified environment enforces the 1 TB RTO
and foreground performance budgets.

All scheduling, manifests, audit timestamps, retention calculations, and RPO
watermarks use UTC.
