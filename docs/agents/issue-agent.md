# GitHub Issue Agent

The Issue Agent turns a maintainer-authorized, deterministic Bug Issue into a
human-reviewed pull request. It runs entirely on GitHub-hosted Actions runners:
there is no control-plane server, self-hosted runner, external workflow
database, or persistent Worker disk.

The checked-in policy is intentionally `intake`. It permits only deterministic
classification, a bounded request for missing information, and
maintainer-triggered signed authorization; it does not run a model or create a
branch or pull request. An administrator must promote each higher rollout stage
in a separate protected-path change.

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
neither the authorization label nor the signed generation; edited text is
supplemental until a maintainer posts `/agent revise`. That command re-reads
the current four-field Bug form, current permission, and current `main`, then
starts a newly signed generation. Ordinary relabeling cannot replace frozen
input.

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
An invalid App-authored chain is projected once as an idempotent audit comment
and `ready-for-human` label while all ordinary signed writes remain fenced.
This alert is not a replacement checkpoint; only the explicit admin recovery
boundary described below can resume the signed chain.

The normal lifecycle is:

```text
intake -> authorized -> version_pinned -> reproducing -> reproduced
       -> Draft PR -> diagnosing -> diagnosed -> fixing -> validating
       -> Ready for Review
```

The Agent never merges a PR, closes the Bug Issue, writes `main`, or bypasses
branch protection. It never performs an unconstrained force-push; the sole
non-fast-forward operation is the typed moving-main transaction whose
`beforeOid` must equal the signed Agent head.

## Execution boundary

The three Actions workflows have separate responsibilities:

| Workflow | Responsibility | Credentials |
| --- | --- | --- |
| `issue-agent-control.yml` | Read current facts and publish one signed transition | App/checkpoint secrets only in Publisher jobs |
| `issue-agent-run.yml` | Recover an exact task, run one model and sandbox, then publish its Artifact | One provider key in its provider Environment; App/checkpoint keys only in the later Publisher job |
| `issue-agent-reconcile.yml` | Hourly bounded recovery and dispatch | App key only in the dispatcher |

All Issue-writing Publisher jobs in the control and Worker workflows share one
non-cancelling concurrency group per repository and Issue, with the maximum
bounded pending queue so later jobs do not replace earlier waiters. Model jobs
execute outside that Publisher group. A cancellation or new generation can
therefore append its checkpoint while a model is still running; the later
Worker Publisher acquires the same group, re-reads the chain, and rejects its
stale predecessor before writing.

The Worker checks out protected control code from `main` and target code at an
exact signed SHA with persisted credentials disabled. It prefetches Go modules,
then runs approved typed tools inside the digest-pinned Docker image from
`.github/issue-agent/policy.json` with no network, read-only module cache, and
no GitHub, model, host, or Docker-socket credential. The untrusted command
container receives a per-job size-capped tmpfs volume instead of a writable
host bind; trusted broker edits are refreshed into that volume before each
command, command output is capped while it is captured, and the volume is
removed when the Worker exits.

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
The Worker may propose only test files. The Publisher independently injects
the exact reviewed `.github/issue-agent/templates/e2e-scenario-AGENTS.md` as
the scenario directory's `AGENTS.md`, replacing model-authored content and
rejecting any other instruction-file write.

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
The frozen reproduction directory is immutable during every fix and review
cycle. A failed exact validation generation creates at most two new bounded
fix leases; the third failure moves the Issue to `ready_for_human`.

The validation workflow also builds the current exact `main` SHA and runs the
same frozen Issue E2E three consecutive times against that binary. Its commit
status binds the main SHA, binary digest, run count, PR, Gate, and validation
run. If all three pass, the Publisher records signed `already_fixed` state,
closes only the unmerged Agent Draft PR, and leaves the Bug Issue open. A
moving-main conflict permits one mechanical rebase of the Agent branch. The
protected Publisher independently computes the exact merge tree from the
signed head and current `main`, converts the bounded delta from `main` to that
tree into a typed ChangeSet, and creates an App-authored, GitHub-signed commit
on a deterministic staging ref keyed by the complete rebase effect whose sole
parent is current `main`. A
GraphQL `updateRefs` transaction then requires the original Agent ref's exact
`beforeOid`, swaps it to the signed commit, and deletes the staging ref
atomically. The returned commit must have the exact independently computed
tree, current-main parent, App Bot author, verified signature, and deterministic
message. This preserves the PR merge-base while preventing an unexpected head
from being overwritten. A
semantic conflict, stale head, unsupported tree shape, or later conflict
enters `ready_for_human`.

## Maintainer controls

Only an exact first-line command from a freshly re-checked user with `write`,
`maintain`, or `admin` permission is accepted. Every accepted command advances
the signed generation and revokes an old lease:

- `/agent revise` freezes the current Bug form and a new authorization-time
  `main`;
- `/agent cancel` terminates Agent work without merging or closing;
- `/agent address-review` freezes the complete unresolved GitHub review-thread
  ID set and starts one bounded review-fix lease;
- `/agent adopt-head <40-hex-sha>` adopts only the current exact Agent branch
  head and forces full validation again;
- `/agent backport <allowed-branch>` creates an idempotent, independent
  human-owned tracking Issue after the main fix is merged;
- `/agent recover-chain <comment-id> <sha256-digest> <quarantine-sha256>` is
  admin-only and signs the exact last-valid anchor, quarantined App-comment
  IDs, and matching quarantine digest before the verifier can skip that
  damaged chain segment.

`/agent approve-risk` remains the separate second-authorization command for a
signed high-risk diagnosis. Commands never grant merge, Issue-close, protected
path, or branch-protection bypass authority.

When a human merges the validated PR, the control workflow treats the
`pull_request.closed` event only as a wake-up signal. The trusted Publisher
re-reads the exact PR head, `base=main`, and merged state, then appends the
signed `merged` checkpoint. It never merges or closes the Issue itself. This terminal
checkpoint is what makes `/agent backport` eligible. If the close event is
missed, the hourly typed reconciliation re-reads the same exact PR facts and
records the merge without trusting the stale event payload. Terminal
checkpoints remove `ready-for-agent`, so the all-state recovery inventory does
not accumulate completed Issues. If checkpoint append succeeds but label
projection is interrupted, reconciliation detects the mismatch and repairs
only the labels without appending another state transition.
An unexpected head on any active Agent branch is checked when the Worker reads
its task, before Artifact publication, and during reconciliation. If a
lease-bound Artifact or validation effect exists, the Publisher first requires
the exact deterministic message, parent, content, configured App Bot author,
and GitHub signature so a commit/checkpoint crash can resume. Every other head
is recorded as signed `external_branch_update` state and handed to humans
without overwrite. A deleted, closed-unmerged, or retargeted work object is
recorded separately as `missing_or_changed_work_object`; a reversible Draft
projection mismatch is repaired from signed state.
`/agent adopt-head <sha>` remains usable and adopts only that exact current
head in a fresh generation. The adopted generation resumes at Draft-PR
creation, diagnosis, or complete validation according to the preserved work.

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

The Sweeper runs hourly and may also be started manually. For an unexpired
lease it enumerates the complete bounded set of completed Worker runs since
the signed lease timestamp. A unique operation-title and Artifact-name match
is downloaded from its exact run and sent through the same trusted Artifact
Publisher; if no Artifact exists, the deterministic operation ID prevents a
duplicate dispatch. Late output is rejected. An expired lease returns to its
last durable boundary:

- `reproducing` to `version_pinned`;
- `diagnosing` to `draft_pr_open`;
- `fixing` to `diagnosed`.

After the infrastructure retry budget is exhausted, the Issue becomes
`ready_for_human`. No recovery path trusts runner disk or an event payload.
Model-provider failures are emitted as sanitized signed Worker failures rather
than being counted as lease-expiry infrastructure retries.
An invalid chain on either an open or a just-closed Issue emits the same
idempotent App audit comment and `ready-for-human` label; closure never hides a
chain-integrity failure.
Worker file writes enforce file-count, per-file, and cumulative final-byte
limits online before each atomic host replacement; command output is streamed
into bounded buffers. A Worker cannot exceed its signed task budget and rely
on final Artifact validation to catch it later.

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
2. Install it only on `WuKongIM/WuKongIM`, confirm it has no branch/ruleset
   bypass, and keep `main` protected. Ensure rules for the dedicated
   `agent/issue-*` and deterministic rebase-staging refs permit the App's
   expected-OID update and staging deletion; otherwise mechanical recovery
   fails closed without modifying the PR and requires administrator action.
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
| `intake` | Deterministic form classification, bounded missing-information request, and signed authorization only |
| `reproduction` | Version pin, E2E Worker, branch, and Draft PR |
| `remediation` | Complete path only for `remediation_issue_allowlist` |
| `general` | Complete path for every explicitly authorized eligible Bug |

Promote one mode per separate PR. Before `general`, record real immutable
Issue, PR, Workflow-run, commit, Artifact, and Gate links for: low-risk success,
`already_fixed`, `needs_info`, budget exhaustion, duplicate/missed event
recovery, expired result rejection, broken-chain fail-closed behavior, and one
no-publish smoke for each enabled provider. This repository has its first
checkpoint public-key epoch configured while the remediation allowlist remains
empty and rollout remains in `intake` mode; higher-stage pilot references do
not yet exist.

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
  -ignore 'unexpected key "queue" for "concurrency" section' \
  .github/workflows/issue-agent-control.yml \
  .github/workflows/issue-agent-reconcile.yml \
  .github/workflows/issue-agent-run.yml
```

The narrow ignore covers GitHub's documented `concurrency.queue` field until
actionlint releases support for it. The parsed Workflow contract tests above
require `queue: max` on every scheduler, per-Issue run, and Publisher group;
all other actionlint findings remain fatal.
