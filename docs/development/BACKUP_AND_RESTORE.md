# Continuous Backup and Restore

WuKongIM backup is a cluster-semantic continuous-capture system. A single-node
deployment is handled as a single-node cluster. There is no periodic backup-job
API, legacy full-backup job, or compatibility manifest chain.

The feature is disabled by default. Production startup accepts
`backup.enabled=true` only when the exact release qualification identity is
present:

```toml
[backup]
enabled = true
qualification_gate = "backup-vnext-production-v3"
```

The equivalent environment variables are:

```sh
export WK_BACKUP_ENABLED=true
export WK_BACKUP_QUALIFICATION_GATE=backup-vnext-production-v3
```

Missing or different qualification values fail startup. This is a release
fence, not a secret. The string is not a qualification result by itself:
operators must keep `backup.enabled=false` until the exact deployed commit has
a successful recorded verdict from the release workflow below.

## Release qualification

Run `.github/workflows/backup-qualification.yml` manually for the exact release
commit. It is fail-closed and publishes a final verdict only after all four
independent jobs pass:

1. portable artifact, failure-injection, audit, rebase, restore, and application
   wiring tests;
2. a local real-process three-node recovery drill with Controller, Slot/data,
   and restore-leader failures plus opaque repository corruption;
3. the 256-Hash-Slot, 5,000-channel, 100,000-member scale gate with an executable
   SENDACK p99 threshold under injected repository/key-authority latency, a 1.2-second
   continuous-backup foreground p99 ceiling, and bounded process allocation
   and heap ceilings; it records capture catchup separately before enforcing
   two already-caught-up checkpoint publications below ten seconds each;
4. the same source-stop, fresh-target restore, activation, and post-write drill
   through real cross-region Alibaba OSS repositories, a protected deployment
   key package, RAM roles, versioning, and COMPLIANCE ObjectWorm in the protected
   `backup-production` environment. It injects a corrupt read into each
   repository in turn and requires the corresponding repair role to publish
   and revalidate a new protected version. Each garbage role must also create,
   list, and remove an OSS delete marker at startup, proving list,
   `DeleteObject`, and exact-version delete permissions without touching a
   data-object version. The probe uses one stable slot per node and clears any
   stale marker before creating another, so failed startups cannot grow an
   unbounded marker history.

The first three jobs use e2e-only file/key substitutes where appropriate. They
cannot satisfy the fourth job. The production job rejects
`WUKONGIM_BACKUP_E2E_FILE_ROOT`, uses a unique object prefix and source/target
generation for each run, and emits a bounded machine evidence line without
endpoints, bucket names, role ARNs, key IDs, credentials, or catalog tokens.

The final `backup-release-qualification.json` artifact exists only when all
dependencies passed. Retain it with the production evidence artifact and the
release commit SHA as the recorded recovery drill. A missing, failed, skipped,
different-commit, or older-schema verdict leaves automatic backup disabled.

## Runtime model

Each logical Hash Slot continuously captures two ordered streams:

- metadata commands;
- committed message rows plus cursor sidecars.

The current Slot Leader owns a fenced capture lease and advances one durable
Slot frontier. Payloads, Channel cursors, and object identities stay in the
repositories; Controller state stores only bounded frontiers, leases, catalog
head, audit state, Generation GC cursors, and permanent-erasure heads.

The Controller Leader periodically publishes one immutable checkpoint that
contains exactly one healthy frontier for every configured Hash Slot. A
checkpoint is discoverable only through the signed hash-linked catalog. If any
Slot is fenced, degraded, awaiting rebase, or missing a complete frontier,
publication fails closed.

A Slot replaces only its own Generation when source-log pin limits or
Generation limits are reached. Replacement creates one materialized baseline
and one complete message-cursor baseline; it does not create a cluster-wide
full backup.

## Required configuration

Use two distinct cross-region provider-native repositories with versioning and
COMPLIANCE retention enabled. The qualified Alibaba path uses OSS ObjectWorm
with a default retention period no shorter than `object_lock_days`; ObjectWorm
is not silently replaced by BucketWorm. A protected deployment key package
performs AES-256-GCM envelope encryption and Ed25519 signing locally. The
package is not represented by TOML fields and is never accepted through a
Manager request. Base cloud credentials come from the Alibaba provider chain
and may only assume the separately configured repository RAM roles. The
ordinary, repair, and garbage roles for both repositories must be six distinct
role ARNs. Enable bucket versioning before ObjectWorm. ObjectWorm is
irreversible, must use the default `COMPLIANCE` mode, and cannot coexist with
BucketWorm. Qualification fails until both buckets report the required
ObjectWorm default retention.

```toml
[backup]
enabled = true
provider = "aliyun"
qualification_gate = "backup-vnext-production-v3"
restore_mode = false
repository_id = "prod-im-backup"
source_generation = "prod-2026-07"
staging_dir = "/var/lib/wukongim/backup-staging"
capture_reconcile_interval = "30s"
checkpoint_interval = "5m"
baseline_chunk_bytes = 8388608
target_segment_bytes = 67108864
max_segment_open_duration = "30s"
staging_max_bytes = 10737418240
worker_count = 4
source_pin_max_age = "1h"
max_source_pinned_bytes = 8589934592
audit_interval = "1s"
audit_scrub_interval = "24h"
garbage_collection_interval = "1h"
garbage_safety_window = "168h"
garbage_max_requests_per_repository = 256
garbage_max_bytes_per_repository = 1073741824
retention_monthly_months = 0
object_lock_days = 7

[backup.primary]
endpoint = "https://oss-cn-hangzhou.aliyuncs.com"
region = "cn-hangzhou"
bucket = "wukongim-backup-primary"
prefix = "prod"
access_role_arn = "acs:ram::1234567890123456:role/wukongim-backup-primary"
repair_role_arn = "acs:ram::1234567890123456:role/wukongim-backup-primary-repair"
garbage_role_arn = "acs:ram::1234567890123456:role/wukongim-backup-primary-garbage"

[backup.secondary]
endpoint = "https://oss-cn-beijing.aliyuncs.com"
region = "cn-beijing"
bucket = "wukongim-backup-secondary"
prefix = "prod"
access_role_arn = "acs:ram::1234567890123456:role/wukongim-backup-secondary"
repair_role_arn = "acs:ram::1234567890123456:role/wukongim-backup-secondary-repair"
garbage_role_arn = "acs:ram::1234567890123456:role/wukongim-backup-secondary-garbage"
```

Keep `wukongim.toml.example` aligned when fields change. TOML keys use grouped
snake_case; environment keys use the `WK_BACKUP_` prefix.
Ordinary repository access, repair, and garbage capabilities use separate RAM
roles. The application refreshes one-hour STS sessions and never gives
ordinary capture credentials a delete capability. All three repository roles
are required for each Alibaba copy when automatic backup is enabled; restore
requires only ordinary repository access plus the same deployment key package.
Each ordinary access role needs `oss:GetObjectVersion` in addition to
`oss:GetObject`: reads first obtain authoritative current-version metadata and
then pin the body request to that exact `versionId`, preventing a concurrent
repair from mixing metadata and bytes from different versions.
Garbage-role startup
qualification uses a stable per-node delete-marker slot, which ObjectWorm does
not retain, clears any stale marker first, and removes the new exact marker
before startup completes.

## Deployment key package

Developers do not configure a key ID, signing key, KMS region, or KMS role.
Every node discovers one protected credential with the fixed name
`wukongim-backup-key-package`. The package is bound to
`backup.repository_id`, contains one active AES-256 wrapping key, one active
Ed25519 signing seed, an independent HMAC-SHA256 package-integrity key, and the
retained keyring needed to read historical artifacts. The HMAC detects partial
writes and edits to package metadata or key material before any key is
accepted. Startup fails closed when the credential is missing, is a symlink,
is not a private regular file, changes while being opened, is malformed, fails
package authentication, or is bound to another repository. The deployment
secret store protects package confidentiality; the immutable repositories
anchor its identity and freshness. Replacing only the deployment credential
cannot establish a different trust root.

The two immutable repositories provide the external identity and freshness
anchor that the self-contained package cannot provide by itself. On the first
revision, after both repositories pass versioning/ObjectWorm qualification,
only the configured Controller voter with the lowest stable node ID may create
the signed root pin for the package ID in each repository. Other nodes wait and
verify; they never race a root write. An implicit single-node cluster is
normalized to its local Controller voter. Seed-join mirrors have no admitted
voter identity, never publish pins, and remain read-only until their admitted
configuration is persisted. The same deterministic voter publishes
odd activation revisions, so a Raft term change cannot create a second writer
between OSS `HEAD` and `PUT`. Staging does not advance the signed immutable
chain. Startup checks both copies and rejects a second bootstrap package, a
superseded staged package, or recovery from an older kit. The pin objects are
permanent control records and are never generation-GC candidates. This adds no
TOML or environment setting.

The runtime cryptographic gate remains closed until both repository controls,
both pins, staging capacity, and UTC checks are healthy. Any later Doctor
failure closes it again. This applies to every data-key and signature call,
including permanent-erasure paths outside the continuous coordinator.

Generate the package once from a trusted operator workstation:

```sh
umask 077
wkcli backup keys bootstrap \
  --repository-id prod-im-backup \
  --out-dir /secure/offline/wukongim-backup-bootstrap-2026-07
```

The command refuses an existing output directory and creates only private
files:

```text
wukongim-backup-key-package  # runtime credential; deploy to every source/target node
wukongim-backup-recovery.wkr # encrypted exact-package recovery kit
wukongim-backup-recovery.key # independent 256-bit recovery key
```

Standard output contains only package ID, repository ID, revision, and active
key IDs. It never contains secret material. Before production use, put the
runtime package in the deployment secret store and move the recovery kit and
recovery key to two separate offline locations with separate access control.
Do not leave all three bootstrap files on the workstation.

### Minimal node deployment

For systemd, encrypt the raw package to the host or TPM and use the standard
credential name:

```sh
sudo systemd-creds encrypt \
  --name=wukongim-backup-key-package \
  /secure/runtime/wukongim-backup-key-package \
  /etc/credstore.encrypted/wukongim-backup-key-package
```

Add only this line to the service unit:

```ini
[Service]
LoadCredentialEncrypted=wukongim-backup-key-package
```

systemd supplies `CREDENTIALS_DIRECTORY`; WuKongIM discovers the named file
without any backup key configuration. Restrict the encrypted credential and
unit to the WuKongIM service identity.

For Kubernetes, use an encrypted-at-rest Secret provider and mount the key
under the standard directory:

```yaml
apiVersion: v1
kind: Pod
spec:
  containers:
    - name: wukongim
      volumeMounts:
        - name: backup-keys
          mountPath: /run/secrets/wukongim
          readOnly: true
  volumes:
    - name: backup-keys
      secret:
        secretName: wukongim-backup-keys
        defaultMode: 0400
```

The Secret must expose a key named `wukongim-backup-key-package`. Limit RBAC
read access to the workload service account and enable Secret encryption at
rest; a plain Secret manifest in Git is not acceptable.

For Docker or another container runtime, bind the private file read-only at:

```text
/run/secrets/wukongim/wukongim-backup-key-package
```

As a last-resort integration fallback, set
`WK_BACKUP_KEY_PACKAGE_FILE=/absolute/private/path`. The file receives the same
regular-file, anti-symlink, size, and permission checks. The standard systemd
or container locations are preferred because they need no application key
setting.

The protected GitHub `backup-production` environment stores the runtime
package as one masked base64 secret named `BACKUP_KEY_PACKAGE_B64`; the
qualification workflow materializes it as a `0600` credential in the runner's
temporary directory. For example:

```sh
base64 < /secure/runtime/wukongim-backup-key-package | tr -d '\n'
```

Paste that single line into the environment secret. Never print it in CI logs
or save it as a repository variable.

### Safe rolling rotation

Rotation is deliberately two-phase so mixed revisions remain readable:

```sh
wkcli backup keys rotate stage \
  --package /secure/runtime/wukongim-backup-key-package \
  --recovery-key /offline-b/wukongim-backup-recovery.key \
  --out-dir /secure/rotation/staged-r2
```

Deploy `staged-r2/wukongim-backup-key-package` to every node and roll all
processes. The old keys remain active while every node learns the pending
keys. After every node is on the staged revision:

```sh
wkcli backup keys rotate activate \
  --package /secure/rotation/staged-r2/wukongim-backup-key-package \
  --recovery-key /offline-b/wukongim-backup-recovery.key \
  --out-dir /secure/rotation/active-r3
```

Deploy the active revision with another rolling restart. Nodes still on the
staged revision already know the new keys, so they can read and verify objects
written by an activated node. The first activated node publishes the new
revision pin in both repositories; a later restart with the staged or older
package then fails closed. The activated package retains the old wrapping key
for historical decryption and only the old signing public key for historical
verification; the old signing seed is removed.

Each phase emits a refreshed `wukongim-backup-recovery.wkr`. After activation,
replace the offline recovery kit with the active revision, verify its metadata,
and destroy superseded runtime copies according to the deployment secret
store's retention policy. Keep the recovery key separate; it does not rotate
implicitly.

### Recovery example

If the deployment secret is lost, restore it only on a trusted offline host:

```sh
mkdir -m 0700 /secure/recovered
wkcli backup keys recover \
  --recovery-kit /offline-a/wukongim-backup-recovery.wkr \
  --recovery-key /offline-b/wukongim-backup-recovery.key \
  --out /secure/recovered/wukongim-backup-key-package
wkcli backup keys inspect \
  --package /secure/recovered/wukongim-backup-key-package
```

Recovery authenticates the kit before writing and refuses to overwrite an
existing output. A wrong key, changed ciphertext, repository mismatch, or
invalid permission fails closed. The repository pins also reject a
cryptographically valid but superseded kit. Restore nodes must receive the
current package revision that can verify and open the selected checkpoint
history.

## Normal operations

Configure a `wkcli` context or pass `--server` and `--token` explicitly.

```sh
wkcli backup status --server "$MANAGER_URL" --token "$WK_MANAGER_TOKEN"
wkcli backup checkpoint list --limit 50 --server "$MANAGER_URL" --token "$WK_MANAGER_TOKEN"
wkcli backup checkpoint show "$CHECKPOINT_ID" --server "$MANAGER_URL" --token "$WK_MANAGER_TOKEN"
```

The continuous coordinator normally publishes on
`backup.checkpoint_interval`. An operator can request publication of the current
complete vector:

```sh
wkcli backup checkpoint publish --server "$MANAGER_URL" --token "$WK_MANAGER_TOKEN"
```

This does not start a full capture. It freezes the already durable current
frontiers and fails if the vector is incomplete or unhealthy.

For a restore, preserve the opaque catalog-head token returned with the
selected checkpoint and hold that checkpoint before the source can stop:

```sh
wkcli backup checkpoint list --id "$CHECKPOINT_ID" --json \
  --server "$SOURCE_MANAGER_URL" --token "$WK_MANAGER_TOKEN" > checkpoint-page.json
CATALOG_HEAD_TOKEN="$(jq -r '.catalog_head_token' checkpoint-page.json)"
test -n "$CATALOG_HEAD_TOKEN" && test "$CATALOG_HEAD_TOKEN" != "null"
wkcli backup checkpoint hold "$CHECKPOINT_ID" \
  --server "$SOURCE_MANAGER_URL" --token "$WK_MANAGER_TOKEN"
```

The token is immutable admission evidence but intentionally hides repository
object coordinates. Do not replace it with a later `latest` value. The hold is
also mandatory for drills that stop the source: automatic backup and explicit
restore mode cannot run in one process, so there is no live source-side restore
plan for Generation GC to discover.

## Restore runbook

Restore requires a fresh empty target cluster in explicit restore mode. Use a
new cluster ID and a `backup.target_generation` different from the source
generation. Restore mode keeps Gateway, business APIs, plugins, webhooks, and
ordinary workers closed. Its restricted Manager exposes restore operations and
the read-only node inventory under `cluster.backup:r`, allowing operators to
observe Controller leadership without reading process logs.

1. Start the empty target cluster with:

   ```toml
   [backup]
   enabled = false
   restore_mode = true
   target_generation = "successor-2026-07"
   ```

   Configure the same repository identity, repository endpoints, deployment
   key package, staging directory, and authenticated Manager.

2. Create one immutable target plan from the exact checkpoint and catalog-head
   token:

   ```sh
     wkcli backup restore plan \
       --checkpoint "$CHECKPOINT_ID" \
     --catalog-head "$CATALOG_HEAD_TOKEN" \
     --server "$TARGET_MANAGER_URL" \
     --token "$TARGET_MANAGER_TOKEN"
   ```

   Add `--invalidate-tokens` if restored client tokens must not remain valid.
   Record the returned `PLAN_ID`, target cluster ID, and target generation.

3. Irreversibly fence the old source generation and save its signed receipt:

   ```sh
   wkcli backup fence-source \
     --restore-plan "$PLAN_ID" \
     --checkpoint "$CHECKPOINT_ID" \
     --target-cluster "$TARGET_CLUSTER_ID" \
     --target-generation "$TARGET_GENERATION" \
     --server "$SOURCE_MANAGER_URL" \
     --token "$SOURCE_FENCE_TOKEN" \
     --json > source-fence-receipt.json
   ```

   Source fencing requires the exact
   `cluster.backup.source_fence:w` permission. Wildcard and ordinary backup
   grants do not satisfy this boundary. After convergence, the fenced source
   cannot reopen normal service.

4. Install and verify the target:

   ```sh
   wkcli backup restore start "$PLAN_ID" \
     --server "$TARGET_MANAGER_URL" --token "$TARGET_MANAGER_TOKEN"
   wkcli backup restore status \
     --server "$TARGET_MANAGER_URL" --token "$TARGET_MANAGER_TOKEN"
   wkcli backup restore verify "$PLAN_ID" \
     --server "$TARGET_MANAGER_URL" --token "$TARGET_MANAGER_TOKEN"
   ```

5. Activate with the exact signed source-fence receipt:

   ```sh
   wkcli backup restore activate "$PLAN_ID" \
     --source-fence-receipt ./source-fence-receipt.json \
     --server "$TARGET_MANAGER_URL" \
     --token "$RESTORE_ACTIVATION_TOKEN"
   ```

   Activation requires an authenticated principal with the exact
   `cluster.restore.activation:w` permission. It first persists immutable
   activation evidence, then removes plan-bound plaintext staging from every
   target replica, and only then publishes `activated`.

6. Keep the source checkpoint held until the drill or migration no longer
   depends on it and the checkpoint/token/plan/fence evidence set is archived.
   If the source Manager is intentionally still available, release it
   explicitly:

   ```sh
   wkcli backup checkpoint release "$CHECKPOINT_ID" \
     --server "$SOURCE_MANAGER_URL" --token "$WK_MANAGER_TOKEN"
   ```

   Never release merely because target installation started.

Break glass is reserved for a permanently unrecoverable source:

```sh
wkcli backup restore activate "$PLAN_ID" \
  --break-glass-reason "reviewed incident reference and reason" \
  --server "$TARGET_MANAGER_URL" \
  --token "$RESTORE_ACTIVATION_TOKEN"
```

The reason and authenticated operator identity become immutable audit evidence.

## Metrics

The public metrics intentionally expose the continuous model and bounded health
only:

- `wukongim_backup_checkpoint_age_seconds`
- `wukongim_backup_controller_leader`
- `wukongim_backup_doctor_health{state}`
- `wukongim_backup_failures_total{category}`
- `wukongim_backup_capture_owned_slots`
- `wukongim_backup_capture_lease_takeovers_total`
- `wukongim_backup_capture_lease_fenced_total`
- `wukongim_backup_source_pin_age_seconds{hash_slot}`
- `wukongim_backup_source_pinned_bytes{hash_slot}`
- `wukongim_backup_source_node_pinned_bytes`
- `wukongim_backup_slot_rebases_total{hash_slot,reason,outcome,failure_category}`
- `wukongim_backup_slot_rebase_duration_seconds`
- `wukongim_backup_audit_debt_objects`
- `wukongim_backup_audit_last_success_timestamp_seconds`
- `wukongim_backup_audit_corruptions_total{category}`
- `wukongim_backup_audit_repair_bytes_total`
- `wukongim_backup_audit_unrecoverable_failures_total`
- `wukongim_backup_restore_partitions{phase}`

`wukongim_backup_checkpoint_age_seconds` is `NaN` before the first checkpoint;
missing evidence is never reported as zero. Metrics do not expose repository
copy names, regions, object keys, key identifiers, Channel IDs, or credentials.

## Failure handling

- A stale Slot lease or leadership change fences the worker; periodic
  reconciliation resumes from the durable frontier.
- A failed checkpoint publication leaves immutable orphans unreachable; it
  does not advance the catalog head.
- One damaged repository copy is repaired only through the explicit integrity
  repair capability and must pass complete revalidation.
- Dual-copy loss freezes only the affected Slot. If live source data still
  exists, the Slot rebases into a new Generation; otherwise the audit state
  remains failed for operator action.
- Generation GC protects retained and held checkpoints, the active restore,
  current/pending frontiers, and audit-frozen Slots. Object Lock rejection
  defers only the affected repository cursor.
- A hold/release transition conflicts with any live Generation-GC delete guard.
  Each delete also compares the durable catalog-retention revision, so the
  operator can safely retry after the bounded collection step.

Never delete repository objects manually to resolve a failed audit or restore.
Preserve the catalog head, checkpoint ID, restore plan, and source-fence receipt
as one incident evidence set.
