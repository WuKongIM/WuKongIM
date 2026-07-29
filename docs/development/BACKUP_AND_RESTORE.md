# Scheduled Full Backup and Restore

WuKongIM backup is configured and operated from Manager. There are no
`wukongim.toml` or `WK_BACKUP_*` settings. The Controller stores one
cluster-wide plan, the current task, bounded task history, and restore
maintenance state.

## Operator workflow

Open **Cluster → Backups** in Manager:

1. Select a repository, enter its settings, and click **Save settings**.
   Saving is durable and the non-secret settings reload after a refresh; it
   does not contact the repository.
2. Click **Test storage**. The test always uses the exact saved plan revision,
   not unsaved form values.
3. Choose **Daily at 01:00**, **Every 12 hours**, or a custom Cron expression
   and time zone.
4. Keep the default retention of seven archives or choose another value.
5. Enable automatic backup and save the plan.

Enabling a new plan starts one initial full backup. Later runs follow the
schedule. A missed occurrence is not replayed after downtime. If another
backup or a restore is active, the occurrence is recorded as skipped instead
of overlapping. Enabling, **Back up now**, and scheduled execution are blocked
until an explicitly unverified repository passes **Test storage**. Changing the
provider, endpoint, region, bucket, prefix, addressing mode, or credential
marks it unverified again. Schedule-only changes preserve verification.

The page shows the current task, progress across all 256 Hash Slots, task
history, and every published archive. An operator can also start one immediate
backup, cancel the active backup, verify an archive, place or remove a
retention hold, and delete an unheld archive. The archive list refreshes every
30 seconds and also has an explicit refresh button, avoiding continuous
full-prefix scans of large repositories.

## Repository choices

### Shared file repository

The file option uses:

```text
<node data_dir>/backup-repository
```

Every active data node must see the same filesystem contents at that path.
Use a shared mount for a multi-node cluster. **Test storage** makes every
active data node independently open the repository, read a coordinator marker,
and write its receipt; the coordinator verifies all receipts, lists the probe,
and removes it.

### Object-storage repositories

Alibaba OSS and Tencent COS use Region, Bucket, Prefix, AccessKey, and secret
fields. COS Bucket must include its APPID suffix. Their Endpoint may remain
blank to use the provider's standard public endpoint, and both use virtual-host
addressing. Generic S3-compatible storage also accepts its endpoint and
addressing choice.

Credentials are encrypted before publication to Controller state and are
never returned by Manager APIs. After save or page refresh, non-secret fields
are restored while the AccessKey and secret inputs remain blank. Re-entering
both credential fields rotates them; leaving both blank keeps the saved
same-provider credential.

Backup archives themselves are not encrypted by WuKongIM. Use storage-side
encryption and access policy when required.

**Test storage** performs real create-only write, read, list, and delete
operations and proves visibility from every active data node before binding the
repository to this cluster. OSS uses its native forbid-overwrite request and
COS uses `x-cos-forbid-overwrite`, preserving immutable archive publication
semantics rather than relying on a non-atomic read-before-write check.

If the test fails, Manager returns a specific stable reason such as invalid
AccessKey, signature mismatch, permission denied, missing bucket, region
mismatch, unreachable endpoint, TLS failure, timeout, failed read/write/list/
delete, repository identity conflict, or an inaccessible cluster node. The
page displays this feedback immediately below the **Save settings** and
**Test storage** buttons, so no scroll to the page header is required. Safe
details may include provider, failed stage, provider code, request ID, and node
ID; credentials and raw provider bodies are never returned.

Some S3-compatible services reject writes before a volume is completely full,
so check the provider's minimum-free-space threshold as well as its reported
free bytes.

## Archive format and retention

Each run creates one independent full archive:

```text
catalog/<archive-id>             compact published-archive index
pending/<archive-id>             incomplete-job marker
backups/<archive-id>/
  manifest.json
  COMPLETE
  HOLD                          optional
  CORRUPT                       optional
  slots/<hash-slot>/
    attempts/<attempt>/
      manifest.json
      meta-<sequence>.zst
      messages-<sequence>.zst
```

Data is split into 64 MiB logical chunks, compressed with Zstandard, and bound
to stored/logical SHA-256 digests. The top-level manifest selects exactly one
immutable attempt for every Hash Slot. An archive is invisible until its
manifest and all chunks have been verified and the `COMPLETE` marker has been
published. Verification reads and validates every compressed chunk.

`catalog/` keeps listing bounded on S3 and is not exposed as an operator
concept. A retry after `COMPLETE` publication checks the immutable identity and
repairs a missing catalog entry without changing the archive. Incomplete
`pending/` output is removed when a task fails and orphaned output older than
72 hours is pruned. Failed manual verification writes `CORRUPT`, preventing
later restore until the archive is deleted and replaced.

Automatic retention keeps the newest successful archives and every held
archive. The default count is seven. A held archive is never removed by
retention and cannot be deleted until its hold is released. Manager also
prevents deletion of an archive used by the active restore or the last healthy
archive. Deletion requires the exact confirmation `DELETE <archive-id>`.

## Scheduling and limits

The simple presets are:

- Daily at 01:00 in the selected time zone: `0 1 * * *`
- Every 12 hours: `@every 12h`

Custom five-field Cron expressions and `@every` intervals are accepted.
Intervals shorter than 12 hours are rejected. Per-node export defaults are
50 MiB/s, one worker, and a 12-hour task deadline. Manager allows one through
four workers and a deadline from one through 48 hours.

Only the Controller leader admits and coordinates work. Slot ownership is
resolved from current cluster state. A leader change resumes unfinished work
from Controller state; stale worker completions are fenced by term and
revision.

## Restore

Restore is an online administrative operation, not a special startup mode.
Manager remains available while business traffic is placed in cluster-wide
maintenance.

Starting restore requires all of the following:

- an authenticated Manager session;
- explicit `cluster.restore:w` permission (a wildcard alone is insufficient);
- the current administrator username and password;
- exact confirmation text `RESTORE <archive-id>`.

Before changing maintenance state, the coordinator fully verifies the archive
from every active data node, rejects stale or unhealthy topology, and checks
that each node has enough free space for the current business data, twice the
archive's logical size, and 1 GiB of headroom for staging and rollback.

The restore coordinator:

1. propagates durable maintenance; each node first stops new sessions,
   disconnects existing clients, drains accepted writes and dirty projections,
   suspends delivery, Webhooks, and plugin side effects, and then installs its
   local cluster write fence;
2. captures a local rollback image on every target replica;
3. stages and fully verifies all 256 Hash Slots on every current replica;
4. checks that physical Slot placement has not changed;
5. writes durable switching markers and imports the verified logical streams
   into every live replica while maintenance hides partial state;
6. rolls back every switched replica if any switch fails;
7. reloads durable Slot/Raft state, advances each node's message-ID allocator
   above the archive high-water mark, and resumes side effects;
8. exits maintenance only after success or completed rollback;
9. invalidates Manager sessions after success;
10. removes staging and rollback files.

Client authentication tokens are preserved because the restore reinstalls the
backup point-in-time metadata without generating a token invalidation
revision. Manager sessions are invalidated when restore succeeds, forcing
administrators to sign in again.

Canceling before the switch discards staged work. A timeout or execution
failure also enters the same durable rollback path. Do not terminate all
Controller voters during restore; the surviving leader resumes the recorded
phase.

Restore is intentionally limited to the current cluster identity. Portable
Hash Slot artifacts allow the current cluster's node placement to change, but
`v1` does not adopt a repository's identity into a separately bootstrapped
replacement cluster.

## Access control

Use these Manager resources:

- `cluster.backup:r` to view plans, tasks, and archives;
- `cluster.backup:w` to configure, run, verify, hold, or delete backups;
- `cluster.restore:w` to start or cancel restore.

Backup write routes require authentication even when the rest of Manager is
configured without authentication. Restore always requires explicit
permission and password reauthentication.

Every backup and restore mutation emits one structured audit event containing
the actor, action, target, result, and sanitized error. Repository credentials
and archive payloads are never included. Restore jobs and their terminal
history also retain the initiating Manager username.
