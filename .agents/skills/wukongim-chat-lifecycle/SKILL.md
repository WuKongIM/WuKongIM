---
name: wukongim-chat-lifecycle
description: Operate the repository's fully automated, native-Ubuntu WuKongIM chat-lifecycle cloud test or its reusable-Lease repair short run. Use when the operator explicitly asks to start either paid flow, requests status for one active request, asks to stop and clean up that request, asks to diagnose its exact live run, or asks what to do next. Only the corresponding exact start intent authorizes billable procurement; status, diagnose, stop, explanations, and next-step questions never do.
---

# WuKongIM Chat Lifecycle

Route one operator intent into the fixed protected-main workflows. Keep this Skill thin: do not reproduce provider API calls, SSH deployment, workload configuration, diagnosis logic, or cleanup algorithms here.

## Safety boundary

- Treat the exact phrase `开始聊天生命周期全流程压测`, or an explicit invocation of this Skill that unambiguously says to start the complete flow, as authority for one aggregate Cost Envelope capped at CNY 1,500.
- Treat the exact phrase `开始聊天生命周期修复短跑`, or an explicit invocation of this Skill that unambiguously says to start the repair short run, as authority for one reusable repair Lease capped at CNY 300. This authority does not start an official rehearsal or formal run.
- Never infer paid authority from `继续`, approval of implementation work, status, diagnose, stop, an explanation, `下一步建议？`, or any other conversational context.
- Reject a second start while any earlier chat-lifecycle request lacks authenticated zero-inventory proof.
- Do not change the reviewed 4-vCPU/8-GiB hosts, 500-GiB service disks, 200-GiB load disk, workload rate, 12 workload groups, or 256 physical hash slots.
- Ask the operator only for a genuinely missing prerequisite that cannot be repaired through the approved setup workflow.
- Never put a private key, password, AccessKey, bearer token, or decrypted access document in a command log, GitHub Issue, run summary, Artifact, or repository file.

Before start or stop, read [references/operator-workflow.md](references/operator-workflow.md) completely. Also read `.github/workflows/README.md` before invoking a workflow.

## Route the intent

### Start

Require the explicit paid authority above, then execute the `Start` procedure in the operator reference. The only paid entrypoint is `.github/workflows/chat-lifecycle-rehearsal.yml` on protected `main`, with exactly these four inputs:

- `source_sha`
- `operator=tangtaoit`
- `codex_diagnostic_pubkey`
- `request_id`

Create one request-scoped tracking Issue and one request-scoped local state directory outside the repository. Generate a fresh Ed25519 diagnostic identity there. Dispatch only after all read-only and identity prerequisites pass. Register a run-scoped 30-minute monitor that remains active until authenticated provider inventory is zero.
Use `scripts/chat-lifecycle/local-request-state.sh init` for the local identity;
do not reproduce its path validation or permissions with ad hoc deletion logic.

When a successful Deployment handoff appears, authenticate its producer, download `encrypted-access.json`, and use `wkchatlifecycle open-access` with the local request identity. Give the operator the exact Manager and Demo HTTP URLs plus their shared temporary username and password only in the local conversation. Never copy them to the tracking Issue.

### Repair short run

Require the exact repair authority above, then execute the `Repair short run`
procedure in the operator reference. The only paid entrypoint is
`.github/workflows/chat-lifecycle-repair.yml` on protected `main`, with the
same four identity inputs as Start plus
`paid_authorization=create-paid-cloud-lease`.

The workflow may reuse that one Lease across immutable protected-main candidate
generations. It must stop a generation when active online sessions or SEND / SENDACK
progress stalls, preserve the typed diagnosis, and wait for the exact
`Chat-Lifecycle-Repair: <request_id>` revision rather than buying replacement
hosts. A passing short run releases the repair Lease to exact zero inventory.
The paid Acquire Receipt, immediate selector-bound handoff, and scheduled repair
finalizer form the cleanup recovery chain if the paid runner exits; the
Provision Artifact binds the exact authenticated parent, and the finalizer
derives the selector from its active Receipt when the handoff was not reached.
Cleanup-pending is never zero proof.
Its result is explicitly ineligible for official rehearsal/formal evidence;
starting the complete flow later requires the complete-flow paid authority.

### Status

Status is read-only and never authorizes setup, dispatch, Acquire, Release, cancellation, Issue comments, or any other mutation. Resolve an explicit request ID or the sole active local request; do not guess between multiple requests. Reauthenticate GitHub run and Artifact provenance, then summarize:

- current stage and workflow run;
- source SHA, Lease identity, and expected stage end;
- latest health/checkpoint, warnings, and failure classification;
- conservative aggregate cost against CNY 1,350 operational stop and CNY 1,500 hard limit;
- cleanup state and whether exact zero-inventory proof exists; and
- both UTC and Asia/Shanghai timestamps.

Return Manager/Demo credentials only when explicitly requested and only while the matching Lease is still live.

### Stop

Require an explicit request-scoped stop intent, but do not ask for a second confirmation. Dispatch `.github/workflows/chat-lifecycle-stop.yml` on `main` with the exact request ID and `stop_authorization=operator-stop-chat-lifecycle`.

The Stop Action seals the durable stop marker, blocks future formal procurement, and requests bounded finalization for both possible stages. Before handoff, the cleanup-owning orchestrator observes the marker, cancels only its exact current child, waits for that child to become terminal, and performs exact Release; the Stop Action never hard-cancels that owner. After handoff, every scheduled finalizer reauthenticates the marker before performing the graceful workload stop and Release, so a handoff publication race cannot lose the intent. Continue monitoring cleanup. Do not report successful stop, close the Issue, or delete local request credentials until an authenticated zero-inventory proof covers the exact selector.

### Diagnose

Diagnose is read-only and never authorizes procurement or cleanup. Resolve and authenticate one exact live request and stage. If provider inventory is already zero, report that live diagnosis is unavailable. Otherwise delegate the exact live run to `$wukongim-cloud-analysis` and preserve its evidence-based classification. Do not substitute old workflow logs for a live Analysis session.
Establish that session through
`scripts/chat-lifecycle/analyze.sh <request_id> <lease_id>`; do not send the
Lease identity to the legacy Cloud Simulation locator workflow.

## Tracking and completion

Use the tracking Issue as the human control record, never as provider authority. Record stage transitions, immutable source and Lease identities, checkpoint verdicts, cost, warnings, and cleanup evidence. Mention `tangtaoit` only for failures, capacity/resource or disk warnings, budget events, and the final outcome.

Keep local diagnostic and UI credentials until the complete request has terminal evidence and exact zero inventory. Then remove only the fully resolved request-scoped files and mark the monitor complete. A workflow conclusion, accepted delete request, empty Issue, or elapsed expiry is not cleanup proof.
Pass the authenticated `zero-inventory.json` to
`scripts/chat-lifecycle/local-request-state.sh cleanup`; never recursively
remove the request directory.
