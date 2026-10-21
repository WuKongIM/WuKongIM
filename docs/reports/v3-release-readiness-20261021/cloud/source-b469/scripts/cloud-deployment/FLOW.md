---
scope: package
summary: Executes shared local and Action deployment activation, bounded readiness, and typed outcomes through host adapters.
---

# Cloud Deployment Execution Flow

## Responsibility

`deploy.sh` is the shared execution entry for the Deployment Action and local
repair. Behavioral tests invoke this entry with fake process and SSH adapters.
Go `clouddeploy` owns the pure Plan, bundle, and readiness validation contracts.

## Boundaries

- Callers validate Lease/bundle provenance, prepare credentials and archives,
  and provide trusted gate tools before admitting host operations.
- The entry accepts `WK_CLOUD_*` Plan, Lease, manifest, credentials, transport,
  and output paths. Existing host adapters own SSH, transfer, and native units.
- Callers own credential retention, cloud lifecycle, and workload start. This
  entry never buys resources, releases a Lease, or starts a coordinator.

## Main Flows

1. Discard prior outcome/snapshot state, check inputs, and initialize SSH config.
2. Stage on load, relay to services, verify all bundles, prepare all hosts,
   activate services then load, prime the dormant stage, and clean remote
   credential staging through `activate-hosts.sh`.
3. Collect fresh evidence and invoke trusted `wkcloudgate deployment-gate` with
   the exact Lease, Plan, manifest, and snapshot until readiness or deadline.
4. Publish a matching v2 receipt and `ready`, or preserve the latest typed gate
   failure/current operation guard as the outcome; never publish raw stderr.

## Invariants and Failure Semantics

- Activation is never replayed by readiness retries. Cleanup failure prevents
  readiness admission. Frozen compatibility allowlists stay in host adapters.
- Activation uses the shared SSH deadline; only transport exit 255 retries.
- The readiness budget defaults to 1,200 seconds and includes commands and
  sleeps. Timeout/cancellation stops the active local process group with up to
  ten seconds of termination grace. Both callers use the repository Python
  supervisor; remote service rollback is not implied.
- Failed collection cannot reuse stale evidence. Success must match the Plan
  digest and arrive before the deadline. Typed outcome files have mode 0600.
- Repair generation resets remain confined to fixed, symlink-rejecting data
  roots on the exact retained Lease. Coordinators remain dormant.

## Read First

- [Shared entry](deploy.sh)
- [Host activation](activate-hosts.sh)
- [Readiness collection](collect-readiness.sh)
- [Failure guard](write-deployment-failure.sh)
- [Activation runbook](../../docs/superpowers/runbooks/cloud-deployment-activate.md)

## Update Triggers

Update when entry inputs, ownership, phase order, deadlines, cancellation,
compatibility, cleanup, or outcome semantics change.
