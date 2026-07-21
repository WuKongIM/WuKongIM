# Cluster Backup And Restore

## Status

Cluster-semantic backup is disabled by default. The implementation provides the
artifact, repository, coordination, capture, restore, retention, Manager, CLI,
and metrics seams, but it is not production-qualified until the three-node
failure matrix and the 1 TB RTO/performance gate documented in the design spec
have passed.

Do not enable it merely because a node starts successfully. A deployment must
also prove repository immutability, key retention, restore drills, capacity,
and Alertmanager coverage in its own environment.

## Safety Model

- A single-node deployment is still a single-node cluster.
- A published restore point represents every configured hash slot and the
  oldest committed cut among them. Missing partitions block publication.
- Payload objects are compressed, encrypted with a fresh envelope data key,
  and copied to both repositories before the signed top-level manifest becomes
  discoverable.
- Upload credentials do not delete. Garbage collection assumes the separately
  configured restricted role and deletes exact object versions only after the
  Object Lock-safe grace period.
- Backup failure does not make message traffic unready. Backup health and RPO
  become degraded or failed independently.
- Restore accepts only a fresh cluster generation with the same
  `hash_slot_count`. Normal APIs, Gateway traffic, webhooks, and plugins remain
  off in restore mode.
- Missing operational evidence is `unknown`; it must never be treated as zero
  age or healthy.

## Required External Controls

Before setting `backup.enabled = true`, provide:

1. Two distinct HTTPS S3-compatible repositories in different regions, with
   versioning and Object Lock/WORM enabled for at least
   `backup.object_lock_days`.
2. A KMS encryption key and an asymmetric signing key in
   `backup.kms_region`. Old decrypt and verification key versions must remain
   available while retained restore points reference them.
3. Default-chain workload credentials that can put, get, list, and head but
   cannot delete repository objects.
4. `backup.garbage_collector_role_arn`, assumed only by the garbage collector,
   with version-list and exact-version delete permissions scoped to both
   configured prefixes.
5. A dedicated absolute `backup.staging_dir` that does not overlap
   `node.data_dir` and has at least `backup.staging_max_bytes` free.
6. A stable `backup.repository_id` and a unique `backup.source_generation` for
   the live cluster incarnation.

Credentials are supplied through the AWS-compatible default credential chain;
they must not be written into TOML. Keep the three shipped example configs
aligned when changing the backup config surface.

## Enable And Observe

Set the `[backup]`, `[backup.primary]`, and `[backup.secondary]` values in
`wukongim.toml`, then start every node with the same non-secret backup policy.
The Controller leader runs the coordinator. Doctor failure is retried and is
reported as backup failure without failing message startup.

Use Manager credentials with the narrow backup permissions:

```bash
wkcli backup status --server https://manager.example --token "$WK_MANAGER_TOKEN"
wkcli backup list --server https://manager.example --token "$WK_MANAGER_TOKEN"
wkcli backup trigger --kind materialized_full --server https://manager.example --token "$WK_MANAGER_TOKEN"
```

`cluster.backup:r` permits status and restore-point reads.
`cluster.backup:w` permits trigger, cancel, hold, release, and verification.
Restore activation requires the separate explicit
`cluster.restore.activation:w` grant; wildcard grants deliberately do not
authorize it.
Repository deletion remains outside Manager permissions.

Manual mutations emit structured `internal.app.backup_audit` log events. Do
not include config fingerprints, object keys, credentials, plaintext, or old
cluster fencing evidence in audit fields.

## Metrics And Alerts

The following metrics use only bounded labels:

- `wukongim_backup_recovery_point_age_seconds`: newest verified recovery point
  age; `NaN` means unknown.
- `wukongim_backup_verification_age_seconds`: latest successful dual-repository
  audit age; `NaN` means unknown.
- `wukongim_backup_controller_leader`: one on the active coordinator node.
- `wukongim_backup_doctor_health{state}`: one-hot `unknown`, `healthy`, or
  `failed`.
- `wukongim_backup_job_active`: whether a cluster backup job is active.
- `wukongim_backup_failures_total{category}`: bounded failure categories.
- `wukongim_backup_restore_partitions{phase}`: total, installed, and verified
  restore partitions.

At minimum, alert when recovery-point age is absent or greater than 300
seconds, doctor health is not healthy, verification evidence is absent or more
than 48 hours old, or the failure counter increases. `NaN` must be handled as a
separate missing-evidence alert, not filtered into a healthy value.

## Retention And Garbage Collection

The Controller retains all five-minute points for 24 hours, one hourly point
for seven days, and one daily point for 30 days. Optional monthly retention
keeps one materialized full per UTC month. The newest point, held points, and
the active incremental base remain protected.

Expiration first moves the restore-point reference into a durable pending-GC
queue. Collection authenticates the retained graphs in both repositories,
marks every reachable object, protects active job prefixes, and then removes
only old unreachable exact versions. An Object Lock denial leaves the queue
entry pending for a later retry.

## Restore Runbook

1. Fence the old cluster through the deployment control plane and retain a
   SHA-256 digest of the reviewed fencing evidence. DNS changes alone are not
   sufficient.
2. Provision a new empty cluster with the same `hash_slot_count`, a different
   cluster ID/generation, and enough data plus staging capacity.
3. Configure repository and KMS reads, set `backup.restore_mode = true`, set a
   new `backup.target_generation`, leave `backup.enabled = false`, and start
   all target nodes. Restore mode requires Manager authentication and at least
   one operator with an explicit `cluster.restore.activation:w` grant. Only
   restricted Manager, metrics, and restore internals start.
4. Create an immutable plan with an exact restore point or an explicit latest
   verified selection:

   ```bash
   wkcli backup restore plan --restore-point RESTORE_POINT_ID --repository primary --server https://restore-manager.example --token "$WK_MANAGER_TOKEN"
   wkcli backup restore start PLAN_ID --server https://restore-manager.example --token "$WK_MANAGER_TOKEN"
   wkcli backup restore status --server https://restore-manager.example --token "$WK_MANAGER_TOKEN"
   wkcli backup restore verify PLAN_ID --server https://restore-manager.example --token "$WK_MANAGER_TOKEN"
   wkcli backup restore activate PLAN_ID --old-cluster-fence-digest SHA256 --server https://restore-manager.example --token "$WK_MANAGER_TOKEN"
   ```

5. Verification authenticates the repository chain, checks every restored
   channel boundary, and compares a canonical semantic metadata digest on each
   target node. Activation remains impossible before every partition is
   installed and verified.
6. Stop the restore-mode processes. Set `backup.restore_mode = false`. If
   automatic backup will resume, set `backup.enabled = true` and set
   `backup.source_generation` to the activated target generation. Restart the
   cluster normally. Startup refuses an unactivated plan or a generation
   mismatch before ordinary writes are admitted.

Never reuse the source generation, restore into a non-empty target, change the
hash-slot count during recovery, or delete the target automatically after a
failed restore.

## Qualification Gates

Production enablement remains blocked until a real three-node environment
proves online baseline and incremental capture, Controller failover, data-node
failure, primary outage, secondary recovery, retention/tombstone correctness,
corruption rejection, different target topology, restored client sync/send,
and a weekly isolated restore drill. The qualified 1 TB profile must restore
within 60 minutes and stay inside the foreground throughput and SENDACK P99
budgets in the design spec.

The current implementation also needs explicit qualification or completion for
permanent-erasure ledger replay; source-log pin budget enforcement and public
pin controls; true synthetic-full chain flattening (the current scheduler emits
a source-materialized independent fallback and rejects manual
`synthetic_full`); signed record-count and allocator-fence evidence with
restore comparison; topology/quorum/migration/protocol-version publication
gates; topology-sized restore replica placement and MinISR validation; complete
disk/network/replica/time restore estimates; signed drill reports; a durable
queryable audit history; high-mutation partition-planner starvation and memory
qualification; and automatic throttling from live foreground latency. These
are release gates, not optional tuning work.
