# Chat-lifecycle operator workflow

Use this procedure from the WuKongIM repository root. It is the operational detail behind the four Skill intents; repository workflows remain authoritative.

## Identity and local state

Resolve the exact GitHub repository with `gh repo view --json nameWithOwner`. Require `gh auth status`, `git`, `jq`, `ssh-keygen`, and Go. Fetch `origin/main` without tags and freeze the 40-character SHA at `origin/main`; never dispatch a working-tree or pull-request SHA.

Store request state under the absolute OS-resolved `~/wukongim-leases/chat-lifecycle/<request_id>/` directory, outside every repository and worktree. Create the request directory with mode 0700. Store the Ed25519 private key, decrypted `access.json`, and state metadata with mode 0600. Never print private file contents. Validate the resolved cleanup target is one exact child of the chat-lifecycle state root before deleting it.

Generate request IDs as `chat-<UTC basic timestamp>-<8 lowercase hex characters>`. Generate a new unencrypted OpenSSH Ed25519 key named `diagnostic_ed25519`; the filesystem mode and local state boundary protect it. Pass only the normalized `.pub` line to GitHub.
Create it through `scripts/chat-lifecycle/local-request-state.sh init <request_id> <source_sha>`. The helper resolves the account state root without
using an unresolved home-directory deletion target, creates 0700 directories,
creates the key and state metadata as 0600, and refuses an existing request.

Retain this diagnostic key across rehearsal and formal because the formal transition binds the same request identity. Delete a released rehearsal UI credential when it is superseded, but keep the diagnostic key until the entire request has exact zero-inventory proof.

## Non-billable preflight

Before opening paid authority:

1. Confirm `chat-lifecycle-rehearsal.yml`, `chat-lifecycle-repair.yml`, both finalizers, `chat-lifecycle-formal.yml`, `chat-lifecycle-stop.yml`, generic Cloud Lease workflows, and Deployment workflows exist on `origin/main`.
2. Query recent runs and bounded chat-lifecycle Artifacts. Reject start if any repair, rehearsal, or formal handoff lacks an authenticated cleanup Artifact with an exact zero-inventory receipt.
3. Run the repository identity plan. If OIDC or the four unattended Environments need setup, run `scripts/cloud-lease/setup-identity.sh <owner/repo>` only under the explicit start authority. This setup creates no Lease. If both bootstrap AccessKey Secrets are absent, or only one exists, stop and tell the operator the exact two Secret names required.
4. Require the live OIDC verification jobs for Provisioner, Observer, and Releaser to pass. Do not accept the presence of Variables alone.
5. Do not quote or acquire before these checks pass.

## Start

Create the tracking Issue before dispatch. Use title `[chat-lifecycle] <request_id>` and include source SHA, operator, CNY 1,500 envelope, CNY 1,350 operational stop, request creation time in UTC and Asia/Shanghai, and `state=preflight_passed`. Do not include credentials.

Record the current maximum rehearsal workflow run ID, dispatch `chat-lifecycle-rehearsal.yml` on `main` with the four fixed inputs, then poll for exactly one newer run whose display title is `Chat Lifecycle Rehearsal <request_id>`. Treat zero, multiple, wrong-branch, or wrong-workflow matches as ambiguous; do not dispatch again. Persist the run ID and URL locally and comment the transition on the Issue.

Create a persistent run-scoped monitoring goal when the product supports it. Poll at most every 30 minutes and on meaningful workflow transitions. The monitor must:

- authenticate the repository, protected-main workflow path, request ID, source SHA, and Artifact producer before trusting evidence;
- follow rehearsal, rehearsal finalization and release, formal transition, fresh formal procurement, formal finalization, and cleanup;
- report only changed stage/checkpoint/cost/cleanup facts rather than noisy unchanged polls;
- add `@tangtaoit` only to a failure, capacity/resource warning, disk warning, budget event, or final outcome;
- show action times in UTC and Asia/Shanghai;
- invoke diagnosis while a qualifying failure is still live; and
- continue until the exact Cloud Lease selector has an authenticated zero-inventory proof.

An Issue state ending in `deployment_repair_pending` is also an immediate
monitor wake-up. Inspect the exact typed Deployment Action failure and its
bounded logs, fix only Deployment Action/workflow control code that can safely
reuse the authenticated original Lease and bundle, run the relevant tests, and
push one protected-`main` commit whose message has the exact trailer
`Chat-Lifecycle-Repair: <request_id>`. Do not dispatch Acquire, Release, or a
second paid rehearsal: the still-running top-level orchestrator recognizes the
request-bound trailer and re-invokes `cloud-deployment-activate.yml` with the
same Lease, bundle, and sealed identity. Each control SHA is attempted at most
once. If the failure requires a new product source or bundle, do not weaken
provenance; let the exact Lease release to zero inventory and report that a new
explicit start is required.

An authenticated `diagnosis-window.json` with `state=diagnosis_pending` is an
immediate monitor wake-up, not a reason to wait for the next 30-minute poll.
Invoke `$wukongim-cloud-analysis` while its exact Lease remains live and record
the evidence-based classification on the tracking Issue. The scheduled
finalizer releases at the marker deadline even if the local monitor is absent.

If persistent desktop monitoring is unavailable, say so explicitly. The paid
orchestrators, scheduled finalizers, and stop workflow still append idempotent
stage/cleanup states to the exact tracking Issue, while the scheduled sweeper
remains the provider-cleanup fallback. Do not imply that a local monitor is
active or that workflow comments replace live diagnosis.

## Repair short run

Use this path only for the exact paid repair authority. Create the same
request-scoped local identity and tracking Issue, but record the CNY 300 hard
limit, CNY 250 operational stop, and `state=repair_preflight_passed`. Freeze the
protected `origin/main` source SHA and dispatch `chat-lifecycle-repair.yml` with:

- `source_sha=<protected-main SHA>`
- `operator=tangtaoit`
- `codex_diagnostic_pubkey=<request public key>`
- `request_id=<exact request>`
- `paid_authorization=create-paid-cloud-lease`

Correlate exactly one newer run titled `Chat Lifecycle Repair <request_id>`.
The workflow owns one six-hour repair Lease, starts a bounded rehearsal-shaped
process, and samples exactly three workers. Once traffic is active, fifteen
seconds without online, SEND, or SENDACK progress is terminal for that
generation. Every adjacent active observation window must sustain at least
1,900 logical SEND/s, keep the
SEND-to-SENDACK backlog at or below 4,000, and retain zero send rejection or
receive-ACK failure; do not keep polling a stopped workload. The workflow stops the
unit, retains the Lease, publishes the typed low-cardinality diagnosis, and
waits for a distinct protected-main commit with the exact trailer
`Chat-Lifecycle-Repair: <request_id>`. Fix and test locally first, then push that
candidate; do not dispatch another paid workflow. The orchestrator builds and
deploys the candidate to the same hosts as the next generation. Later
generations reset only the fixed workload and product data roots after service
quiescence.

Authenticate the immediate `chat-lifecycle-repair-handoff-<request_id>`
Artifact. Its parent run and exact release selector let the scheduled repair
finalizer release a crashed, stopped, or expiring owner. A cleanup-pending
Artifact is not zero proof and continues to block another paid start. If the
parent exits before that handoff is published, authenticate the paid Provision
Artifact's active repair Receipt and its exact authenticated parent owner, then
let the finalizer derive the same exact selector; never infer that the Lease or
owner is absent. Each failed generation's checkpoint must retain all three
terminal worker status/snapshot pairs. An Acquire-only recovery owner retains
its exact active parent; a terminal parent is released.

Qualification requires two continuous minutes of healthy active progress. It
does not authorize official evidence: require
`repair-qualified.json.official_evidence_eligible == false`, then authenticate
the exact cleanup and zero-inventory Artifacts. Only after cleanup may the
operator separately authorize `开始聊天生命周期全流程压测`, which starts the
official flow on a fresh Lease.

## Manager and Demo access

The rehearsal and formal handoff Artifacts contain `encrypted-access.json`, never plaintext. Before decrypting:

1. authenticate the handoff run as the exact protected-main rehearsal or formal workflow for this request;
2. validate the envelope schema, request ID, source SHA, Lease ID, deployment Plan digest, recipient fingerprint, and algorithm;
3. build `wkchatlifecycle` from the frozen trusted source; and
4. run `wkchatlifecycle open-access --envelope <exact-file> --identity <diagnostic_ed25519> --request-id <request_id> --now <current-UTC-RFC3339> --output <new-access.json>`.

Validate the decrypted request, Lease, source, digest, expiry, `http://` URLs, and shared username/password against the authenticated Deployment Receipt. Present the credential only in the operator conversation. Do not use shell tracing while handling it.

## Live Analysis MCP

For an exact live rehearsal or formal Lease, run
`scripts/chat-lifecycle/analyze.sh <request_id> <lease_id>`. The helper routes
to `cloud-lease-analyze.yml`, not the legacy Cloud Simulation locator. The
workflow authenticates the exact stage handoff, proves its typed Selector
against current provider inventory, opens only expiring TCP/19444 runner and
local-client grants, pins the deployment-published TLS fingerprint, exchanges
the exact GitHub workflow identity for a short-lived MCP token, and revokes the
temporary grants through the Releaser role. A missing or ambiguous handoff is
`unknown_run`; it must not be guessed from another request or historical run.

## Status

Resolve status from an explicit request ID, otherwise from exactly one active local request. Read GitHub runs, Artifacts, the tracking Issue, and Cloud Lease Observer evidence only. Do not run an identity setup, workflow dispatch, Issue write, cancellation, or provider mutation.

An authenticated `cleanup.json` plus its exact `zero-inventory.json` is terminal provider truth. A completed workflow without that pair remains cleanup pending. If local state is missing, report what can be authenticated remotely and mark credentials unavailable rather than regenerating keys.

## Stop

After an explicit stop for one resolved request, record no extra confirmation. Dispatch `chat-lifecycle-stop.yml` once with:

- `request_id=<exact request>`
- `stop_authorization=operator-stop-chat-lifecycle`

Correlate exactly one newer run titled `Chat Lifecycle Stop <request_id>`. The durable stop marker prevents a released rehearsal transition from purchasing a formal Lease. Before handoff, the orchestrator cancels only its exact current child, waits until that child is terminal, and then owns exact Release; do not hard-cancel the orchestrator while its child may still mutate cloud inventory. For a live workload after handoff, every scheduled finalizer reauthenticates the marker, sends one graceful SIGTERM, waits up to 10 minutes for `operator_stop` evidence, then proceeds to exact Release; a missing report remains missing evidence and must not be invented. Keep monitoring scheduled cleanup when inventory remains.

## Diagnosis

Use the authenticated active handoff to resolve the current Lease/run identity, source SHA, expiry, and Analysis endpoint. Invoke `$wukongim-cloud-analysis` for that exact live identity. Preserve `product_defect`, `infrastructure_interrupted`, `scenario_invalid`, `healthy`, or `insufficient_evidence` according to its contract. If inventory is zero, stop without live calls.

## Issue close and local deletion

Close the tracking Issue only after final evidence and exact zero inventory. The final comment includes the terminal verdict, aggregate cost, Artifact link, cleanup proof identity, and both time zones; mention `@tangtaoit`.

After the close facts are persisted, delete only the exact request's private key, public key, decrypted access, encrypted download, and local metadata. Never recursively delete an unresolved variable, a state root, a repository, a worktree, a home directory, or a parent directory. Report local credential deletion in the final handoff.
Use `scripts/chat-lifecycle/local-request-state.sh cleanup <request_id> <authenticated-zero-inventory.json>`. It validates the exact request selector,
deletes only the fixed credential filenames, and refuses to remove a directory
that contains unexpected files.
