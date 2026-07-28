# GitHub Issue Agent

The Issue Agent turns a maintainer-authorized, deterministic Bug Issue into a
human-reviewed pull request. It runs entirely on GitHub-hosted Actions runners:
there is no control-plane server, self-hosted runner, external workflow
database, or persistent Worker disk.

The checked-in policy is intentionally `shadow`. Merging the implementation
does not enable writes or spend model quota; an administrator must complete the
setup and promote each rollout stage in a separate protected-path change.

## User intake

The Bug Issue Form asks for only four required facts:

1. exact affected version;
2. environment, cluster topology, and client version;
3. reproduction steps;
4. expected and actual result.

Frequency and redacted logs or configuration are optional. Intake is
deterministic and does not call a model. It accepts an exact release tag, full
40-character commit SHA, or immutable image digest. An image digest also needs
a trusted source-commit lookup; without that reviewed metadata integration,
version pinning fails closed. Moving references such as `latest`, branch names,
or abbreviated SHAs are rejected.

Editing an authorized Issue does not alter the frozen input. Intake removes
`ready-for-agent`, and a maintainer must review the changed report and add the
label again. The fresh label event starts a new signed generation.

## Authority and durable state

Execution begins only when a GitHub user with current `write`, `maintain`, or
`admin` permission adds `ready-for-agent`. Issue text, comments, Reviews, PR
text, model output, and Workflow event payloads are untrusted data.

Signed, append-only checkpoint comments authored by the configured GitHub App
are the sole workflow state authority. Every stateless run:

1. re-reads the open Issue and complete bounded comment history;
2. verifies App author, Ed25519 signature, key epoch, predecessor digest,
   sequence, generation, and referenced GitHub objects;
3. derives at most one legal operation;
4. re-reads the same fences immediately before publishing.

GitHub events only wake the controller. Duplicate, reordered, or missed events
therefore converge through the hourly Sweeper. A corrupt, edited, deleted,
ambiguous, paginated, or saturated history fails closed.

The normal lifecycle is:

```text
intake -> authorized -> version_pinned -> reproducing -> reproduced
       -> Draft PR -> diagnosing -> diagnosed -> fixing -> validating
       -> Ready for Review
```

The Agent never merges a PR, closes the Bug Issue, force-pushes, writes
`main`, or bypasses branch protection.

## Execution boundary

The three Actions workflows have separate responsibilities:

| Workflow | Responsibility | Credentials |
| --- | --- | --- |
| `issue-agent-control.yml` | Read current facts and publish one signed transition | App/checkpoint secrets only in Publisher jobs |
| `issue-agent-run.yml` | Recover an exact task, run one model and sandbox, then publish its Artifact | One provider key in its provider Environment; App/checkpoint keys only in the later Publisher job |
| `issue-agent-reconcile.yml` | Hourly bounded recovery and dispatch | App key only in the dispatcher |

The Worker checks out protected control code from `main` and target code at an
exact signed SHA with persisted credentials disabled. It prefetches Go modules,
then runs approved typed tools inside the digest-pinned Docker image from
`.github/issue-agent/policy.json` with no network, read-only module cache, and
no GitHub, model, host, or Docker-socket credential.

The model may return only a semantic proposal. The trusted Worker derives the
complete file set, command transcript digests, diagnosis evidence digest, and
provider-metered token use. The Publisher validates the sanitized Artifact
again, publishes one expected-head GraphQL commit to
`agent/issue-<number>`, requires a verified GitHub signature, and re-reads the
exact ref and tree.

## Reproduction and remediation

Version pinning records both the exact affected commit and the `main` commit at
authorization time. Reproduction creates the smallest process-level black-box
E2E under `test/e2e/issue_agent/issue_<number>`, then runs that same assertion
three times against each exact binary. Product reproduction succeeds only when
all six runs fail the same named business assertion. Harness, startup,
topology, infrastructure, and provider failures remain separate failure
classes.

After reproduction, the Agent immediately opens a Draft PR containing the
frozen E2E. Diagnosis is read-only and must name the external symptom, causal
path, violated invariant, evidence, intended paths, cluster semantics, test
suites, and risk classes before production code can change.

Protected Agent, Workflow, schema, policy, and instruction paths are always
human-only. Other high-risk diagnoses pause until a maintainer posts this exact
first-line command:

```text
/agent approve-risk
```

The fix Worker may change only diagnosis-approved paths. It builds the exact
candidate, runs at least one approved related test, and passes the frozen E2E
three times. The Publisher always requests the existing `go-fast` and `go-e2e`
Agent PR suites, adds risk-selected suites, and marks the Draft Ready only
after the exact-head, exact-test-merge `Agent Validation Gate` succeeds.

## Capacity, retries, and recovery

The protected defaults allow at most:

- three active Issue Workers repository-wide;
- one active heavy E2E or multi-node Worker;
- 24 worker-hours reserved in a rolling 24-hour window;
- six cumulative Worker hours per Issue;
- three reproduction attempts, three remediation attempts, two CI-repair
  attempts, and three infrastructure retries per Issue.

Admission verifies the complete signed state of every bounded
`ready-for-agent` Issue. Incomplete inventory or any invalid chain blocks new
leases.

The Sweeper runs hourly and may also be started manually. It redispatches an
unexpired operation by deterministic operation ID, rejects late output, and
returns an expired lease to its last durable boundary:

- `reproducing` to `version_pinned`;
- `diagnosing` to `draft_pr_open`;
- `fixing` to `diagnosed`.

After the infrastructure retry budget is exhausted, the Issue becomes
`ready_for_human`. No recovery path trusts runner disk or an event payload.

For emergency shutdown, set `.github/issue-agent/policy.json` `enabled` to
`false` in a reviewed PR. This prevents new work; existing GitHub records remain
auditable. Keep the App out of every branch-protection/ruleset bypass list, and
revoke its installation or Environment access if credential compromise is
suspected.

## Administrator setup

Perform these steps only after the implementation is on the protected default
branch.

1. Create a GitHub App with exactly repository `Actions: write`,
   `Contents: write`, `Issues: write`, `Metadata: read`, and
   `Pull requests: write`. Disable organization permissions and webhooks.
2. Install it only on `WuKongIM/WuKongIM` and confirm it has no branch/ruleset
   bypass.
3. Create protected Environments:
   `issue-agent-publisher`, `issue-agent-codex`, and
   `issue-agent-deepseek`.
4. Store `ISSUE_AGENT_APP_PRIVATE_KEY` and
   `ISSUE_AGENT_CHECKPOINT_PRIVATE_KEY` only in
   `issue-agent-publisher`.
5. Store `CODEX_API_KEY` only in `issue-agent-codex` and
   `DEEPSEEK_API_KEY` only in `issue-agent-deepseek`. Configure independent
   provider spend limits.
6. Add repository variables:
   `ISSUE_AGENT_APP_ID`, `ISSUE_AGENT_INSTALLATION_ID`,
   `ISSUE_AGENT_APP_LOGIN`, `ISSUE_AGENT_CHECKPOINT_KEY_ID`,
   `ISSUE_AGENT_CODEX_MODEL`, and `ISSUE_AGENT_DEEPSEEK_MODEL`.
7. Create or verify `needs-triage`, `needs-info`, `ready-for-agent`,
   `agent-priority/high`, and the Agent PR validation labels documented in
   `.github/workflows/README.md`.

Generate a checkpoint key offline into a new explicit path:

```bash
GOWORK=off go run ./cmd/wkissueagent generate-checkpoint-key \
  --private-key-file /secure/new-issue-agent-checkpoint.key
```

The command prints only the key ID and public record. Put the private file's
base64 value in the Publisher secret, set the key-ID variable, insert the
public record into `.github/issue-agent/checkpoint-public-keys.json` in
strict key-ID order, and merge that protected-path change before enabling
Intake.

For rotation, add the new overlapping public epoch first, update the
Environment secret and key-ID variable second, verify new signed checkpoints,
and retain the old public key until every checkpoint signed during its validity
window remains verifiable. Never rewrite old checkpoint comments.

## Rollout and evidence

`rollout_mode` is a reviewed capability ceiling:

| Mode | Maximum behavior |
| --- | --- |
| `disabled` | No intake or execution |
| `shadow` | Read and report only; no App/model secret is consumed |
| `intake` | Deterministic form labels and signed authorization only |
| `reproduction` | Version pin, E2E Worker, branch, and Draft PR |
| `remediation` | Complete path only for `remediation_issue_allowlist` |
| `general` | Complete path for every explicitly authorized eligible Bug |

Promote one mode per separate PR. Before `general`, record real immutable
Issue, PR, Workflow-run, commit, Artifact, and Gate links for: low-risk success,
`already_fixed`, `needs_info`, budget exhaustion, duplicate/missed event
recovery, expired result rejection, broken-chain fail-closed behavior, and one
no-publish smoke for each enabled provider. This repository intentionally ships
with an empty public-key set, empty remediation allowlist, and `shadow` mode;
those pilot references do not yet exist.

Worker Artifacts retain bounded sanitized evidence for 90 days. The permanent
audit record is the signed Issue chain, frozen regression, verified Git commit,
Draft PR history, diagnosis, and exact Validation Gate.

## Local verification

```bash
GOWORK=off go test ./internal/contracts/issueagent \
  ./internal/usecase/issueagent ./internal/runtime/issueagentworker \
  ./internal/infra/issueagentgithub ./internal/infra/issueagentmodel \
  ./internal/access/issueagentcli ./internal/app ./scripts -count=1

go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.9 \
  .github/workflows/issue-agent-control.yml \
  .github/workflows/issue-agent-reconcile.yml \
  .github/workflows/issue-agent-run.yml
```
