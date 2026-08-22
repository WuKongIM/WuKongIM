---
name: wukongim-chat-lifecycle
description: Operate the Codex-owned WuKongIM chat-lifecycle laboratory directly from the local repository. Use when the operator explicitly asks to buy temporary Alibaba Cloud servers and start the repair short run, deploy a committed candidate to the same Lease, start or monitor the short workload, diagnose a stopped live run over SSH, inspect one request, or stop and destroy its exact resources. Only an exact start intent authorizes paid procurement; deploy, run, status, diagnose, stop, explanations, approvals, and next-step questions never buy servers.
---

# WuKongIM Chat Lifecycle Direct Lab

Codex owns this loop from the local machine. Use repository commands and SSH;
do not dispatch a GitHub Action, create a tracking Issue, wait for an Artifact,
or destroy healthy hosts between candidate generations.

## Safety boundary

- Treat the exact phrase `开始聊天生命周期修复短跑`, or an explicit invocation
  of this Skill that unambiguously asks to purchase temporary servers and start
  the direct repair lab, as authority for one CNY 300 repair Lease.
- Never infer paid authority from `继续`, `同意`, deploy, run, status, diagnose,
  stop, an explanation, `下一步建议？`, or prior paid runs.
- `start` additionally requires either short-lived Alibaba STS environment
  variables or a verified one-hour Alibaba Cloud Shell credential, plus both
  exact local authorization values. A tokenless credential is accepted only
  when it is absent from the account's registered AccessKey list and the exact
  Cloud Shell authorization marker is present. Never use or persist a
  long-lived AccessKey for this loop.
- Reject another paid start until every earlier request has an exact
  selector-bound zero-inventory proof.
- Keep one Lease while diagnosing and deploying committed candidate
  generations. A failed workload stops within the bounded monitor window but
  does not release the hosts. Only `stop` destroys the Lease.
- Never print or commit STS credentials, SSH private keys, Manager credentials,
  worker tokens, or runtime credential archives.

Before any operation, read
[references/operator-workflow.md](references/operator-workflow.md) completely.

## Route the intent

### Preflight

Run `scripts/chat-lifecycle/direct-lab.sh preflight`. It is local and read-only:
it must not call Alibaba APIs. Report every missing host tool, temporary
credential proof, or exact lifecycle authorization.

### Start

Require the paid authority above. Require a clean worktree whose exact candidate
is committed, generate a fresh request ID, and execute the reference `start`
procedure. This sequence builds and seals locally before a read-only Quote;
Acquire is the last step. Persist the pre-Acquire selector before the paid call.
If build, materialization, or Quote fails before that selector exists, finalize
the request as not acquired with a local zero-resource proof; do not leave a
phantom active request that blocks the next exact start.

### Deploy

`deploy` never authorizes procurement. Build the current committed candidate,
activate it as the next repair generation on the existing Lease, and require the
typed readiness gate. Preserve failed deployment evidence and the Lease for
diagnosis; do not purchase replacement hosts.

### Run

`run` never authorizes procurement. Start the fixed rehearsal-shaped systemd
unit and hand it to the ten-minute repair monitor. Once active, online loss or
missing SEND/SENDACK progress is terminal within 15 seconds. Stop the workload,
retain the terminal worker cuts, and mark the request `diagnosis_ready`; do not
keep polling a stopped workload.

### Diagnose

`diagnose` is read-only and uses the saved SSH config for the exact live Lease.
Collect bounded service state, stage journal, Prometheus targets, and all three
worker status/snapshot pairs. Diagnose from evidence, fix locally, commit, then
`deploy` and `run` again on the same hosts.

### Status

`status` is read-only. Resolve an explicit request ID or refuse ambiguity.
Report local state, generation, source SHA, Lease identity, last diagnosis,
expiry/cost when available, and whether exact zero inventory is proven.

### Stop

An explicit request-scoped stop needs no second confirmation. Execute the
reference `stop` procedure. It first signals any local monitor, stops the remote
workload best-effort, releases only the saved exact selector, and accepts
completion only with an authenticated zero-inventory proof for that selector.

## Completion

The repair short run is diagnostic and never official evidence. Keep the local
request directory until `stop` has stored `zero-inventory.json`. Report both UTC
and Asia/Shanghai timestamps for paid start, diagnosis, qualification, and
release. Do not claim cleanup from a successful API delete request alone.
