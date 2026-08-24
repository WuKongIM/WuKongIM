# Direct chat-lifecycle operator workflow

Run every command from the WuKongIM repository root. Codex is the controller;
GitHub Actions and GitHub Issues are not part of this repair loop.

## Local state and credentials

Request IDs use `chat-<UTC basic timestamp>-<8 lowercase hex>`, for example:

```bash
request_id="chat-$(date -u +%Y%m%dT%H%M%SZ)-$(openssl rand -hex 4)"
```

State defaults to the OS-resolved
`~/wukongim-leases/chat-lifecycle-direct/<request_id>/` directory. Directories
are 0700; private keys, access data, receipts, selectors, runtime archives, and
evidence are 0600. Never set the state root to `/`, a repository, a worktree,
or an unresolved variable. Never print a private file.

The preferred path is one short-lived Alibaba STS session received without
writing it to disk:

```bash
export ALIBABA_CLOUD_ACCESS_KEY_ID='temporary-id'
export ALIBABA_CLOUD_ACCESS_KEY_SECRET='temporary-secret'
export ALIBABA_CLOUD_SECURITY_TOKEN='temporary-session-token'
export WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION='create-and-delete-paid-cloud-lease'
```

The operator must supply these values through a secure local mechanism. Do not
copy GitHub Secrets, create a credential broker workflow, or fall back to a
long-lived AccessKey.

Alibaba Cloud Shell is the only tokenless alternative. Before using it, prove
that the current Cloud Shell AccessKey ID is absent from `ram ListAccessKeys`
for the account and that the console describes the shell as a one-hour
disposable environment. Download the credential once through the authenticated
browser, import it into the controlled local shell without printing it, and
delete the downloaded file before any provider call. Then set:

```bash
export WK_ALIBABA_CLOUD_SHELL_EPHEMERAL_AUTHORIZATION='unregistered-one-hour-cloud-shell'
export WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION='create-and-delete-paid-cloud-lease'
```

This marker is not permission to accept an arbitrary tokenless AccessKey. If
the unregistered-key proof cannot be repeated, stop and obtain STS instead.

## Non-billable preflight

```bash
scripts/chat-lifecycle/direct-lab.sh preflight
```

Preflight checks local tools and credential presence only. It reports
`provider_contacted=false`; it never Quote, Acquire, deploy, or Release.

Before `start`, confirm there is no unresolved request directory whose state is
not `released`. If any exists, inspect it and run exact `stop` first. Commit the
candidate and require `git status --porcelain` to be empty. The bundle builder
clones the exact HEAD locally, builds Linux/amd64 binaries and web assets,
downloads checksum-pinned native dependencies, and seals the offline bundle.

## Paid start

Only after the user gives exact paid authority:

```bash
export WK_CHAT_LAB_PAID_AUTHORIZATION='create-paid-cloud-lease'
scripts/chat-lifecycle/direct-lab.sh start "$request_id"
unset WK_CHAT_LAB_PAID_AUTHORIZATION
```

The fixed transaction is:

```text
committed source -> sealed local bundle -> materialized repair plan
  -> read-only Quote -> persisted release-selector.json
  -> paid Acquire -> active receipt -> selector equality -> active state
```

If Acquire returns an error, preserve the request directory and selector and
immediately inspect/stop the exact request. Never retry by generating another
request ID.

## Same-Lease repair loop

Deploy the current committed candidate:

```bash
scripts/chat-lifecycle/direct-lab.sh deploy "$request_id"
```

After and only after `deploy` exits successfully, validate the exact request's
0600 `access.json` against `state.json`, `receipt.json`, and the current
generation's `deployment-plan.json`. Its `request_id`, `lease_id`, `source_sha`,
and `deployment_plan_digest` must match; `manager_url`, `demo_url`, `username`,
`password`, and `lease_expires_at` must all be non-empty. Do not `cat`, source,
or copy this file into a command, log, diagnosis, status artifact, or repository
file. A missing or mismatched access receipt blocks the deployment handoff; do
not reconstruct credentials from runtime archives or an older generation.

Before suggesting or starting `run`, return this block in the final
operator-facing response using the receipt values verbatim:

```text
Manager: <manager_url>
Manager username: <username>
Manager password: <password>
Demo: <demo_url>
Demo HTTP Basic Auth: same username and password as Manager
Lease expires: <lease_expires_at>
```

This final deploy response is the only allowed disclosure of the Manager
credential. Do not include it in progress commentary, and disclose nothing when
the typed readiness gate has not passed.

Generation 1 reuses the already sealed pre-Acquire bundle. Later generations
build the new committed HEAD. Deployment uses the saved bootstrap key, transfers
through the load host, activates the three service nodes plus load node, and
requires the typed readiness gate. It does not contact the provider or purchase
hosts.

Start the bounded stability workload:

```bash
scripts/chat-lifecycle/direct-lab.sh run "$request_id"
```

The monitor samples all three workers every five seconds for at most 75
minutes. Qualification requires 60 continuous healthy active minutes with
10,000 target online sessions, at least 95% online, adjacent active-window SEND
progress of at least 1,900/s, backlog at most 4,000, and zero terminal
correctness failures. After activity has begun, online loss, SEND/SENDACK
stagnation, insufficient SEND rate, excessive backlog, active-phase loss, or
service exit is terminal within 15 seconds. Exit 10 means the workload was
stopped and the Lease is deliberately retained for diagnosis. Exit 0 means the
stability run qualified; it remains diagnostic and is not official evidence.

Collect a new bounded diagnosis while the Lease is live:

```bash
scripts/chat-lifecycle/direct-lab.sh diagnose "$request_id"
```

Read the returned evidence directory, classify the first causal failure, fix
and test it locally, commit the candidate, then repeat `deploy` and `run`. Do
not delete and repurchase servers between generations.

## Status

```bash
scripts/chat-lifecycle/direct-lab.sh status "$request_id"
```

Status must not mutate provider or remote state. When several request
directories exist, always pass the exact request ID.

## Stop and exact cleanup

An explicit user stop authorizes cleanup of that request, not a new purchase:

```bash
scripts/chat-lifecycle/direct-lab.sh stop "$request_id"
```

`stop` writes the local stop marker, stops the remote stability workload
best-effort, calls `wkcloudlease release --selector` with the saved selector,
and writes `zero-inventory.json` only if the returned proof contains the exact
same selector, zero residual resources, an account hash, observation time, and
provider scopes. A failure leaves state and credentials intact for a retry.

After exact zero proof, retain non-secret diagnosis long enough to report the
result. Remove request secrets only by explicit fixed filenames; never use a
recursive deletion rooted in an environment variable.
