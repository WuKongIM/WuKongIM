# Scheduled Full Backup Acceptance

Date: 2026-07-29

Candidate branch: `codex/scheduled-full-backup`

## Decision

**GO for merging the scheduled full-backup redesign and continuing
pre-production deployment work.**

The Manager workflow, daily/12-hour/custom scheduling, shared-file and
S3-compatible repositories, full verification, point-in-time restore, session
invalidation, retention safety, and failure recovery passed local acceptance.
Review and multi-node acceptance defects were fixed and covered by regression
tests.

This is not a production RPO/RTO certification. Before launch, repeat the
capacity measurement with the expected production data volume, node count,
network, and target S3 service. The local measurements below prove functional
behavior and catch large regressions; they do not predict production duration.

## Environment

- Apple silicon development machine, macOS, local loopback networking.
- One real `cmd/wukongim` process configured as a single-node cluster.
- One real static three-node cluster using a shared-file repository, including
  Controller Leader failure and restart.
- 256 physical Hash Slots and 10 logical Slot Raft groups.
- Manager authentication enabled with explicit `cluster.backup:r/w` and
  `cluster.restore:w` permissions.
- Shared-file repository under the isolated node data directory.
- MinIO S3-compatible service in an isolated Docker container with a 512 MiB
  `tmpfs` data volume.
- Backup schedule exercised with `@every 12h`; the daily 01:00 Cron preset is
  covered by UI and use-case tests.

## Operator Workflow Results

The complete workflow was exercised through the real Manager web application:

1. sign in and open **Cluster → Backups**;
2. test shared-file storage;
3. select **Every 12 hours**, enable, and save;
4. observe the initial full backup process all 256 Hash Slots;
5. verify the published archive;
6. restore the archive and observe the old Manager session become invalid;
7. sign in again and confirm cluster health;
8. switch to S3-compatible storage, test it, save it, and confirm credentials
   are not returned to the browser;
9. run, verify, and restore an S3 archive.

The first file archive was healthy and independently restorable. The first S3
archive was also healthy and its restore completed successfully. Manager
remained reachable during restore while business admission was closed.

## Bounded Data-Volume Evidence

The local capacity sample used one group channel and 2,000 messages with
4 KiB payloads. Half used compressible content and half used independent
random content.

| Observation | Result |
| --- | ---: |
| Message ingestion | 2,000 messages in 13 seconds |
| Published archive records | 2,007 |
| Logical archive bytes | 8,496,093 |
| Stored S3 bytes | 4,180,015 |
| Full backup task duration | 32.027 seconds |
| Full restore task duration | 111.833 seconds |
| Hash Slots processed | 256 |

After the archive was published, another marker and 1,000 regression messages
were written. The pre-restore channel contained 3,002 messages. Restore
completed successfully and the channel returned to exactly 2,001 messages;
the marker and all 1,000 post-cut messages were absent.

An independent process-level E2E test also wrote state before and after a
backup cut, restored the archive, and proved only the pre-cut state survived.
It passed in 226.29 seconds.

A second process-level E2E used three real nodes and 256 Hash Slots. It stopped
the current Controller Leader after backup progress was durable, observed a
survivor resume and publish a healthy archive, restarted the old Leader,
restored every current replica, and proved that only the pre-cut message
remained. It passed in 261.83 seconds.

## Failure Evidence And Repairs

### S3 minimum-free-space rejection

The first MinIO probe used a host bind mount while the host filesystem had only
about 4.5 GiB available. MinIO deterministically rejected the probe because its
storage backend had reached its minimum-free-drive threshold. WuKongIM returned
only a generic `service_unavailable`, which did not tell the operator what to
check.

The use case now classifies repository open/probe failures as
`ErrStoreUnreachable`. Manager returns HTTP 503 with the stable code
`backup_store_unreachable`, and the web application gives an actionable
localized message covering endpoint, credentials, permissions, and free
space. Domain, HTTP, and UI regression tests cover the mapping. The exact
probe passed after MinIO moved to its isolated `tmpfs`.

### Restore quiescence ordering

The first data-volume restore failed in 39.421 seconds with
`maintenance_failed`. Logs proved that 512 dirty conversation-active rows
needed a final flush, but the local cluster maintenance fence had already
started rejecting the flush with `cluster: restore maintenance`.

Local maintenance enablement now closes app entries and drains accepted work
before installing the cluster write fence. Disablement keeps the reverse
ordering: remove the fence before restarting app runtimes. A focused ordering
test failed before the change and passes afterward. Repeating the same restore
with 1,001 additional post-cut writes succeeded, and no new side-effect
transition or conversation flush error was logged.

### Restore control RPC maintenance admission

The first three-node restore entered Controller maintenance but then failed
before node-local prepare with `cluster: restore maintenance`. The coordinator
used the ordinary foreground `Node.CallRPC` path, so its own maintenance fence
blocked the only RPC that could advance or roll back restore.

`RPCScheduledBackupRestore` is now the only typed node RPC allowed through
maintenance. The receiving restore service still validates the durable
coordinator, phase, topology, and per-Slot attempt fences. Ordinary business
RPCs remain rejected. A focused regression test proves both sides of that
boundary, and the original three-node restore now passes.

The conversation-active flush worker was also made lifecycle-idempotent:
repeated quiescence after a successful final drain does not issue a second
write under maintenance, while timed-out or failed drains remain retryable.

### Auxiliary history validation

Archive verification and retention cleanup now appear in the same bounded task
history as backup and restore. The first real backup reached cleanup, but
Controller rejected the new `verification` and `retention` record kinds with
`invalid_state`. Controller validation and FLOW documentation now explicitly
accept those two bounded terminal kinds. A State Machine regression test and
the multi-node backup both pass.

### Operator health and restore progress

The Manager dashboard now reports `healthy`, `warning`, or `critical` backup
coverage. A latest failed attempt is a warning; two expected schedule
occurrences without a successful archive is critical. Manager backup failures
use stable `backup_*` codes. The Web UI shows those health warnings plus
localized restore phases, restored bytes, stable failure code, rollback state,
and per-node replica verification progress.

### Other fault paths

Focused tests passed for:

- canceling an active backup into bounded history;
- cancel/expiry before restore maintenance;
- archive verification failure without entering maintenance;
- corrupt compressed content and corrupt archive health;
- topology changes before the destructive switch;
- preflight failure on an unavailable active node;
- stale Controller fencing;
- rollback after an interrupted destructive switch.

## Validation Matrix

- Relevant Go packages:
  `internal/access/manager`, `internal/usecase/backup`,
  `internal/infra/backup`, `internal/app`, `pkg/backup`, and `pkg/cluster`:
  passed.
- Focused race tests for use case, infrastructure, and cluster maintenance:
  passed.
- Real MinIO `Put`/conditional `Put`/`Open`/`List`/`Delete` integration:
  passed.
- Scheduled backup point-in-time process E2E: passed.
- Three-node backup Leader-failover and all-replica restore E2E: passed in
  261.83 seconds.
- Full Go unit gate across `cmd`, `internal`, `pkg`, `scripts`, and `docker`:
  passed with package concurrency limited to two.
- Manager web tests: 539 passed.
- Manager ESLint baseline: passed.
- Manager TypeScript/Vite production build: passed.
- Focused Go vet: passed.
- `git diff --check`: passed.

The linker printed the existing macOS `LC_DYSYMTAB` warning during race-test
linking; all affected binaries linked and all tests passed.

## Launch Follow-up

Before production launch:

1. run the same backup and restore measurement on the intended production
   topology and target S3 service;
2. use a data snapshot representative of expected launch size and compression;
3. set the backup timeout and per-node rate/worker limits from the measured
   result;
4. record the accepted RPO/RTO and rehearse one node outage during backup and
   one before restore switch;
5. enable repository-side encryption and least-privilege lifecycle policy if
   the deployment requires encrypted archives.

No compatibility migration is required because the system has not launched
and this redesign intentionally replaces the previous backup implementation.
