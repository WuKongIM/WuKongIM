# Cluster Automatic Backup Implementation Plan

**Goal:** Deliver a disabled-by-default, cluster-coordinated automatic backup
and fresh-cluster restore workflow matching the approved design.

**Architecture:** Add a reusable `pkg/backup` artifact/repository contract, an
entry-independent `internal/usecase/backup` coordinator, node-local capture and
restore workers under `internal/runtime/backup`, S3/KMS adapters under
`internal/infra/backup`, Manager/node protocol adapters under
`internal/access`, and composition only in `internal/app`. Controller stores
only bounded job summaries and restore-point references. Large manifests and
data remain in object storage.

**Testing seams:** `pkg/backup`, `internal/usecase/backup`, Manager API plus
`wkcli backup`, restore mode lifecycle, real three-node E2E, and qualified
nightly/manual capacity gates.

## Global Constraints

- Treat one node as a single-node cluster; never add a cluster bypass.
- Keep backup reads, compression, encryption, and uploads outside SEND/SENDACK
  synchronous work.
- Preserve current `wkdb export/import` semantics and bundle format.
- Keep source and restore writes on cluster-authoritative Slot/Channel seams.
- Keep secrets out of TOML snapshots, Manager DTOs, logs, diagnostics, and
  manifests.
- Use `GOWORK=off` in this worktree and explicit repository roots for full Go
  gates.
- Keep long-running capacity and failure tests out of the unit suite.
- Read and update each touched package's `FLOW.md`.
- Do not modify or commit the dirty main worktree.

## Task 1: Freeze the artifact and repository contract

**Files:**

- Add `pkg/backup/FLOW.md`
- Add `pkg/backup/types.go`
- Add `pkg/backup/manifest.go`
- Add `pkg/backup/object_codec.go`
- Add `pkg/backup/repository.go`
- Add public-seam tests in `pkg/backup/*_test.go`

**Behavior:**

- Canonical, strict, versioned manifest decoding.
- Logical Slot/channel cuts and oldest-watermark effective time.
- Immutable repository keys and all-or-nothing primary/secondary publication.
- Compress-before-encrypt object codec with injected KMS wrap/unwrap and signer
  ports.
- Signature verification occurs before object trust or restore writes.
- Unknown fields, path escapes, duplicate partitions/objects, unsupported
  versions, bad signatures, AEAD failures, and hash mismatches fail closed.

**TDD order:** valid manifest round trip; strict rejection; encrypted object
round trip; corruption rejection; signed publish/load; secondary failure blocks
publication.

## Task 2: Add production repository and KMS adapters

**Files:**

- Add `internal/infra/backup/FLOW.md`
- Add local filesystem adapter for development/tests
- Add S3-compatible adapter using the official AWS SDK default credential chain
- Add KMS envelope/signing adapter
- Add adapter contract tests and MinIO-tagged integration tests

**Behavior:**

- Conditional immutable put, bounded streaming reads, stat/head, and list by
  repository prefix.
- Explicit primary and secondary clients; no implicit provider replication
  assumption.
- Workload identity/default credential chain; no credentials in application
  structs or serialized snapshots.
- Object-lock/versioning/KMS/signing doctor checks.

## Task 3: Define the backup usecase state machine

**Files:**

- Add `internal/usecase/backup/FLOW.md`
- Add `internal/usecase/backup/types.go`, `ports.go`, `app.go`, and tests

**Behavior:**

- States: disabled, idle, preparing, capturing, publishing, completed,
  degraded, failed, canceled.
- Exactly one active cluster job; idempotent trigger/cancel/report operations.
- Per-logical-partition completion with no channel IDs in Controller state.
- Restore point publication only when every required partition and both
  repositories verify.
- RPO calculated from the oldest logical cut.
- Daily synthetic full, monthly materialized full, and five-minute restore-point
  schedule decisions are pure/testable.
- Backup failures never alter message readiness.

## Task 4: Persist bounded coordination in Controller

**Files:**

- Update `pkg/controller/FLOW.md`
- Extend Controller state/command/FSM/runtime with backup job summaries and
  restore-point references
- Add state normalization/validation, codec, FSM, failover, and snapshot tests

**Behavior:**

- Controller Leader starts one backup epoch.
- Partition reports are fenced by job ID, epoch, and source cut.
- A new Leader resumes unfinished work idempotently.
- State stores object references/checksums only; it never stores manifests or
  channel-level lists.
- Mixed protocol versions and configuration fingerprint drift block publish.

## Task 5: Implement node-local capture workers

**Files:**

- Add `internal/runtime/backup/FLOW.md`
- Add capture scheduler, bounded spool, throttling, and worker tests
- Extend `pkg/db/meta` with backup-specific logical Slot export
- Extend `pkg/db/message` with committed bounded channel segment export
- Update affected storage FLOW files

**Behavior:**

- Metadata export includes business-authoritative and recovery-critical tables,
  while excluding migration/runtime ownership rows except semantic retention
  fences.
- Message export reads only through committed HW and includes idempotency,
  checkpoint, retention, and allocator boundaries needed by restore.
- Source selection may use a caught-up verified replica to avoid leader load.
- Compression/encryption/upload stream without a full local copy.
- Staging quota and disk waterline stop backup work before the business disk is
  endangered.
- Persisted cursors bound log compaction delay; a gap requests a new baseline.

## Task 6: Add node RPC and app composition

**Files:**

- Update `internal/access/node/FLOW.md`
- Add backup node RPC codecs/handlers/clients
- Add `internal/infra/cluster` backup adapters and update its FLOW
- Wire coordinator/workers only in `internal/app`; update `internal/app/FLOW.md`

**Behavior:**

- The Controller coordinator captures a bounded topology barrier and dispatches
  logical partition work to current authoritative replicas.
- RPC inputs carry job/epoch/cut/config fences and bounded object summaries.
- Stale owners, leader changes, and retries return typed errors; they never
  publish partial state.
- Disabled backup creates no workers, timers, repository clients, or hot-path
  observers.

## Task 7: Add configuration and startup doctor

**Files:**

- Update `internal/config` schema/build/snapshot tests
- Update `internal/app` backup config validation
- Update `wukongim.toml.example` and relevant Docker/script examples

**Behavior:**

- Add documented `[backup]` snake_case keys and `WK_BACKUP_*` overlays.
- Default disabled; enabled mode requires two repositories, repository identity,
  staging dir, KMS encryption key, signing key, schedules, and quotas.
- Startup snapshot redacts endpoints marked sensitive and never carries
  credential values.
- Doctor checks repositories, KMS, signatures, Object Lock/versioning, staging
  capacity, UTC skew, and non-secret cluster config fingerprints before the
  scheduler starts.
- Backup doctor failure leaves the message application ready but backup failed.

## Task 8: Add Manager and wkcli control surfaces

**Files:**

- Add management usecase DTOs/ports
- Add Manager routes/tests and permission catalog entries
- Add `cmd/wkcli/internal/backupops` client/commands/tests
- Register `wkcli backup`
- Update Manager and wkcli documentation/FLOW where present

**Behavior:**

- Read surface: status, effective RPO, active job, restore points, policy, and
  drill/verification state.
- Write surface: trigger, cancel, verify, hold, release.
- Require `cluster.backup:r` or `cluster.backup:w` as designed.
- Never return repository credentials, KMS material, raw object keys, channel
  IDs, or plaintext data.
- CLI uses explicit restore-point IDs; `--latest-verified` is explicit.

## Task 9: Implement restore mode

**Files:**

- Add restore types/state machine to `internal/usecase/backup`
- Add restore workers to `internal/runtime/backup`
- Add restore-only Slot/Channel snapshot installation adapters
- Add `cmd/wukongim` restore-mode configuration/lifecycle tests
- Add `wkcli backup restore plan/start/status/verify/activate`

**Behavior:**

- Refuse a non-empty target and mismatched hash-slot count before writes.
- Capacity plan includes data, MinISR replica, staging, network, and time
  estimates.
- Install semantic Slot snapshots and committed Channel segments through
  cluster-authoritative restore seams.
- Resume idempotently; never auto-delete an abandoned target.
- Fully verify signature, AEAD, hashes, counts, ownership, sequences, retention,
  allocator fences, and permanent erasures.
- Activation requires Slot quorum, Channel MinISR, old-cluster fencing evidence,
  and explicit operator confirmation.
- Restore mode keeps Gateway, ordinary writes, webhook, and plugin effects off.

## Task 10: Add retention, audit, metrics, and drills

**Files:**

- Add reference-safe retention/hold logic
- Add bounded audit events and Manager readers
- Add low-cardinality metrics to `pkg/metrics` and app observers
- Add `wkcli backup drill` and signed drill reports
- Add Alertmanager/runbook documentation

**Behavior:**

- Enforce five-minute/hourly/daily/monthly retention tiers.
- Separate uploader and garbage-collector authority.
- Expose recovery-point age, incremental lag, full-baseline age, upload state,
  verification, drill, and bounded failure categories.
- Daily remote audit and weekly externally provisioned restore drill.

## Task 11: End-to-end and performance qualification

**Files:**

- Add `test/e2e/backup` scenarios and suite helpers
- Add scripts for local MinIO primary/secondary restore drills
- Add nightly/manual workflow gates and artifact-safe reports
- Add operations/runbook documentation

**Behavior:**

- Three-node online full/incremental backup and different-topology restore.
- Controller Leader failure, data-node failure, primary repository outage, and
  secondary-only recovery.
- Tombstone/retention/permanent-erasure correctness.
- Corrupt signatures, ciphertext, missing objects, version mismatch, non-empty
  targets, and absent fencing fail closed.
- Restored clients retain intended tokens, sync history, and send new messages
  without ID or sequence collision.
- Qualified 1 TB run proves RTO and foreground performance budgets.

## Task 12: Close out

1. Update `AGENTS.md` directory structure and `PROJECT_KNOWLEDGE.md` with concise
   durable rules learned during implementation.
2. Run focused tests after every vertical slice and explicit-root unit gates at
   the end.
3. Run integration/e2e only after the local unit gate is green.
4. Run Standards and Spec review through the repository code-review workflow.
5. Fix all actionable findings, rerun gates, and commit only the isolated
   automatic-backup worktree changes to `codex/automatic-backup`.
