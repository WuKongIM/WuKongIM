---
status: accepted
supersedes:
  - 0003-analyze-live-simulation-runs-only
  - 0005-exchange-github-identity-for-run-scoped-analysis-tokens
  - 0018-separate-diagnosis-from-repository-remediation
  - 0019-use-a-dedicated-codex-api-credential
  - 0023-let-diagnostic-verdicts-control-workflow-status
  - 0027-discover-analysis-guidance-from-a-repository-skill
  - 0029-handoff-a-schema-validated-diagnosis-result
  - 0030-bound-each-analysis-access-window
---

# Run Codex locally with an encrypted Analysis Session

Cloud Simulation analysis will use the operator's local Codex CLI authenticated
through a ChatGPT subscription. GitHub will not store or execute with an OpenAI
API key, Codex `auth.json`, or ChatGPT session. The Analysis Session Workflow is
only a cloud-identity broker: it proves the exact retained Run Locator against
provider inventory, exchanges GitHub OIDC for one short-lived run-scoped
Analysis Token, encrypts that token with RSA-OAEP SHA-256 to an ephemeral local
public key, and admits only the requesting client's public `/32` until token
expiry.

The local process retains the private key, pins the run certificate, checks out
the exact deployed source SHA, and invokes that revision's Analysis Skill
through an allowlisted read-only MCP. Codex tool subprocesses inherit none of
the caller environment. A strict least-privilege Codex permission profile lets
them read only the detached source worktree and minimal tool runtime, denies
tool network access, and prevents reads of the token or Codex auth home. The
command ignores project execution rules and fails closed if the deployed source
contains project Codex configuration or hooks that could extend that profile.
It validates the Diagnosis Result identity and explicitly
closes live access. Provider-confirmed
release prints a terminal notice and exits successfully before Codex runs;
unknown identity and unreachable live evidence remain distinct failures.

A valid identity-matched Diagnosis Result is a completed command regardless of
verdict; only the structured verdict establishes health. Session preflight,
timeout, invalid schema, and identity mismatch failures remain nonzero exits.

Provisioning also publishes a minimal non-diagnostic Finalization Schedule.
The local `finalize.sh` command waits until the first bounded analysis time,
invokes the same encrypted Analysis Session, and retains the run for another
attempt when the validated `workload_inspect` state is still `in_progress` and
the lease can safely admit another session. It then requests exact Cleanup and
calls the provider-backed released gate again. Diagnosis or optional
remediation failure does not suppress cleanup, while a provider-confirmed
already-released run terminates without issuing another cleanup request.

Optional remediation begins only after confirmed access closure, in a separate
local worktree and Codex process with an isolated home directory and no GitHub
credential, cloud identity, MCP configuration, or Analysis Token. It requires a
high-confidence repository-attributable product defect and regression coverage.
Its strict permission profile writes only the remediation worktree and isolated
Go cache/temp roots, reads only the Go toolchain/module cache required by tests,
and denies tool network access.
Local orchestration never executes generated tests with the operator's host
privileges: it pushes a guarded branch, waits for the exact commit to pass the
existing read-only CI Workflow, and only then opens a correctly labeled Draft
PR whose body contains no model-authored diagnosis text. Automatic merge
remains prohibited.
