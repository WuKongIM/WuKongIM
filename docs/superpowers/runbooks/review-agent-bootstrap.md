# Review Agent bootstrap

This runbook is for an authorized repository administrator. Repository code
does not create Apps, keys, Environments, Rulesets, or branch protection.

The replacement has no compatibility phase. Complete prerequisites before the
replacement pull request merges.

## 1. Create the Review Agent App

Create a repository-scoped App with exactly:

Use an App description that reflects its bounded authority, for example:
`Policy-gated code review, verdict, and exact-head merge publisher for WuKongIM.`
Do not describe the App as read-only after enabling Contents write.

| Permission | Level |
| --- | --- |
| Metadata | Read |
| Checks | Read and write |
| Contents | Read and write |
| Issues | Read and write |
| Pull requests | Read and write |

GitHub requires Contents write for the pull-request merge endpoint. Repository
code requests it only for the protected Publisher token and exposes only an
exact-head merge operation. Do not grant Actions administration,
Administration, Workflows, Secrets, Deployments, Members, Packages, or
organization-wide installation. Install it only on this repository.

Create Environment `review-agent-publisher` with:

- variable `REVIEW_AGENT_APP_ID`;
- variable `REVIEW_AGENT_APP_INSTALLATION_ID`;
- secret `REVIEW_AGENT_APP_PRIVATE_KEY`.

Restrict this Environment's deployment branches and tags to the protected
`main` branch only. Disable all tags and custom branch patterns. A job from a
pull-request branch must not be able to request this Environment.

Set repository variable `REVIEW_AGENT_APP_LOGIN` to the exact Bot login. Issue
Agent uses this identity to accept only Review Agent-authored repair findings.

## 2. Create the State Writer App

Create a separate repository-scoped App with exactly:

| Permission | Level |
| --- | --- |
| Metadata | Read |
| Contents | Read and write |

Do not grant Checks, Issues, Pull requests, Actions administration,
Administration, Workflows, Secrets, Deployments, or Packages. Install it only
on this repository.

Create Environment `review-agent-state-writer` with:

- variable `REVIEW_STATE_WRITER_APP_ID`;
- variable `REVIEW_STATE_WRITER_APP_INSTALLATION_ID`;
- secret `REVIEW_STATE_WRITER_APP_PRIVATE_KEY`.

Apply the same protected-`main`-only deployment policy: no tags and no custom
branch patterns.

The two Apps must use different identities and private keys.

## 3. Create the model Environment

Create Environment `review-agent-model` with secret `OPENAI_API_KEY` for the
dedicated OpenRouter key used by the pinned Codex Action. The State Writer and
Publisher keys must not exist in this Environment.
Restrict deployment to the protected `main` branch only, with tags and custom
branch patterns disabled.
Give that dedicated key organization-approved spend and revocation controls;
never reuse a developer or production application key. Repository policy
independently bounds concurrency, attempts, context, response size, and wall
time.

Candidate checks use namespace-local loopback for test servers. The runner
host, private networks, link-local targets, and metadata services remain
blocked; do not relax those routes to make a test pass.

If organization-private networks need an additional fence, set repository
variable `REVIEW_AGENT_ORG_BLOCKED_CIDRS` to a JSON array of trusted CIDRs, for
example `["203.0.113.0/24","2001:db8::/32"]`. Do not put credentials in
repository variables.

## 4. Protect state and control paths

Verify that CODEOWNERS records the active maintainer for Review Agent policy,
prompts, schemas, Workflows, code, tests, and documentation. It is maintenance
metadata, not a required human approval gate.

The State Writer App creates:

- `review-state/pr-<number>` with one canonical PR state file; and
- `review-state/scheduler` with one canonical scheduler file.

Do not permit humans or other Apps to write these refs. The runtime accepts
only canonical latest-plus-predecessor rolling checkpoints in GitHub-verified
commits authored by the configured State Writer Bot; older commits remain
append-only audit history.

## 5. Configure the Ruleset

For pull requests targeting `main`:

1. require pull requests;
2. require the single automated status `Review Agent Verdict`;
3. bind the required Check to the dedicated Review Agent App;
4. require branches to be up to date with `main` before merging;
5. do not require CODEOWNERS Approval; and
6. keep only named emergency administrators in the audited bypass list.

Do not retain any obsolete validation context or accept a same-named commit
status from GitHub Actions or another App.

## 6. Validate before cutover

Run local contract tests and `actionlint`, then prove:

- one same-repository ready pull request;
- one first-time Fork ready pull request;
- Draft-to-ready behavior;
- new-head invalidation;
- `approved`, `changes_required`, and `inconclusive`;
- status, explain, reconsider, retry, and cancel authorization;
- approved control-plane change without human Approval;
- approved repository-admin/member PR merges only at the reviewed head;
- approved external, write, maintain, Bot, and unknown-authority PRs remain
  open for human merge;
- strict up-to-date status-check enforcement after `main` advances;
- all three Environments reject a job whose workflow ref is not protected
  `main`;
- App identity on the Check and Review;
- no candidate/model access to either App key; and
- no periodic Review Agent Workflow.

## 7. Direct cutover

After the replacement commit is on `main`:

1. manually dispatch `review-agent.yml` with each open ready `main` pull
   request number;
2. confirm the Check is authored by the configured Review Agent App;
3. atomically require only `Review Agent Verdict`;
4. remove obsolete required contexts and obsolete PR-validation labels; and
5. confirm Issue Agent's `REVIEW_AGENT_APP_LOGIN` variable matches the Bot.

Old comments, statuses, and Reviews remain historical records only. They are
not imported into Review Agent state.

## Rollback

If the provider or Apps are unavailable, use only the audited named Ruleset
bypass with a written pull-request reason. Do not recreate the deleted
label/plan protocol. Repair the Review Agent, verify it on a fresh generation,
then remove the emergency bypass use.
