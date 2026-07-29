# Issue Agent Codex Action Bootstrap Design

**Date:** 2026-07-30

## Status

Proposed for maintainer review.

## Summary

The Issue Agent will replace its hand-written Codex installation and direct
`CODEX_API_KEY` injection with a narrowly scoped bootstrap through the official
[`openai/codex-action`](https://github.com/openai/codex-action). The Action will
install the pinned Codex CLI and Responses API proxy, pass the OpenAI API key to
that proxy, write a loopback-only provider configuration, and permanently drop
`sudo` for the remainder of the Codex Worker job.

The Action will not receive a prompt, inspect the target workspace through
Codex, execute tools, produce a patch, publish GitHub state, or replace any
Issue Agent control-plane component. The existing repository-owned Codex
Adapter will continue to drive bounded JSON tool rounds against the closed
Broker. The credential-free, digest-pinned Docker sandbox will continue to own
all source inspection, edits, builds, and E2E execution. Signed checkpoints,
sanitized Artifacts, the trusted Publisher, exact-SHA validation, and the
DeepSeek Adapter remain unchanged.

This is a hybrid integration: use the official Action for the OpenAI-specific
bootstrap and secret boundary, while retaining WuKongIM's provider-neutral and
stateless Issue Agent protocol.

## Context

The existing `codex-worker` job:

1. installs `@openai/codex@0.145.0` directly with npm;
2. exposes `CODEX_API_KEY` to `wkissueagent`;
3. gives the key to every ephemeral Codex CLI subprocess;
4. invokes Codex with native tools disabled and an empty temporary workspace;
5. routes every useful operation through the repository-owned closed Broker.

This already prevents the model from receiving GitHub write credentials or
direct access to the real checkout. However, WuKongIM owns the OpenAI secret
transport and CLI bootstrap details itself.

The official Action provides maintained behavior for:

- installing a selected Codex CLI and matching Responses API proxy;
- feeding the API key to the proxy through standard input and removing the
  environment copy from the proxy process;
- writing a local `responses` provider that targets
  `http://127.0.0.1:<port>/v1`;
- checking the triggering actor;
- preparing GitHub-hosted Linux user namespaces for Codex sandboxing; and
- dropping the runner's `sudo` privilege before later Codex invocations.

The Action explicitly supports a bootstrap-only invocation: when an API key is
provided but both `prompt` and `prompt-file` are omitted, it starts the proxy
and installs Codex without running `codex exec`. Its documentation permits
subsequent workflow steps to invoke the installed CLI.

## Goals

1. Stop exposing `CODEX_API_KEY` to `wkissueagent` and its Codex subprocesses.
2. Use the official OpenAI-maintained CLI and Responses proxy bootstrap.
3. Preserve the Issue Agent's closed Broker and credential-free execution
   sandbox.
4. Preserve a new, isolated Codex home for every stateless tool round.
5. Treat the Action-generated configuration as untrusted input and accept only
   an exact loopback Responses provider.
6. Permanently remove `sudo` before any model-controlled round starts.
7. Keep the Action and Codex CLI versions reproducibly pinned.
8. Preserve the existing Codex JSON envelope, usage accounting, budgets, and
   failure behavior.
9. Keep DeepSeek as an independent provider Adapter.
10. Keep rollout changes separate from capability-code changes.

## Non-goals

- Replacing the Issue Agent state machine with a general coding Action.
- Giving Codex direct workspace, shell, Git, Docker, network, or GitHub tools.
- Letting the Action create commits, branches, Issue comments, or pull
  requests.
- Sending Issue text or repository content directly to an Action prompt.
- Sharing a persistent Codex home between runs or tool rounds.
- Using a moving Action tag such as `v1` in a protected Workflow.
- Allowing arbitrary bots or users to bypass the Action's actor check.
- Adding Azure OpenAI or another Responses endpoint in this change.
- Routing DeepSeek through an OpenAI-specific proxy.
- Changing the signed checkpoint, Worker Artifact, Publisher, or validation
  protocols.
- Promoting the Issue Agent beyond the current `intake` rollout in the
  capability PR.

## Options Considered

### 1. Let `openai/codex-action` run the entire coding task

The Workflow could pass a prompt, a checkout, a writable permission profile,
and an output schema directly to the Action.

This is rejected. It would bypass the provider-neutral round protocol, the
closed Broker, command and path policy, credential-free Docker execution,
immutable reproduction evidence, and the existing Artifact/Publisher trust
boundary.

### 2. Keep the current direct CLI and API-key integration

WuKongIM could continue installing Codex with npm and passing
`CODEX_API_KEY` to each CLI subprocess.

This is rejected. It works, but duplicates maintained OpenAI bootstrap and
secret-proxy behavior and leaves the API key in every Codex child environment.

### 3. Use the Action only as a secure bootstrap

The Workflow invokes the official Action without a prompt, then the
repository-owned Adapter calls the installed CLI through the Action's local
Responses proxy.

This is selected. It narrows custom OpenAI integration code without weakening
the existing Issue Agent trust boundaries.

## Selected Architecture

```text
CODEX_API_KEY secret
  -> pinned openai/codex-action bootstrap
       -> pinned Codex CLI
       -> loopback Responses proxy
       -> empty bootstrap CODEX_HOME/config.toml
       -> drop runner sudo
  -> trusted wkissueagent Supervisor
       -> validate and canonicalize loopback provider
       -> one empty CODEX_HOME per round
       -> Codex CLI with no API key and no native tools
  -> closed Broker
  -> no-network, digest-pinned Docker sandbox
  -> sanitized Worker Artifact
  -> separate trusted Publisher job
```

The Action is an OpenAI transport bootstrap, not a new Agent layer. Model
decisions still pass through:

```text
Codex Adapter -> typed tool call -> Broker policy -> sandbox operation
```

No model output becomes a GitHub mutation until the existing Publisher validates
the complete Artifact and current signed checkpoint.

## Workflow Design

### Pinned bootstrap

The `codex-worker` job will use:

```yaml
- name: Bootstrap the pinned Codex CLI and Responses proxy
  uses: openai/codex-action@52fe01ec70a42f454c9d2ebd47598f9fd6893d56 # v1.11
  with:
    openai-api-key: ${{ secrets.CODEX_API_KEY }}
    codex-version: 0.145.0
    codex-home: ${{ runner.temp }}/issue-agent-codex-bootstrap
    safety-strategy: drop-sudo
    allow-bot-users: wukongim-issue-agent
```

The `v1.11` annotated tag resolves to the pinned commit above at design time.
The Workflow uses the full commit SHA, not either tag. The Codex CLI remains
fixed at the currently tested `0.145.0`; upgrading the Action or CLI requires a
separate reviewed change.

The bootstrap home must not exist before this step. No repository or runner
configuration is copied into it. This avoids merging Action output with
personal, project, or previously generated Codex configuration.

The step deliberately omits:

- `prompt` and `prompt-file`;
- `working-directory`;
- `model` and `effort`;
- `codex-args`;
- `output-file` and output schema inputs;
- `sandbox` and `permission-profile`; and
- `allow-bots`, wildcard users, or wildcard bot users.

Because no prompt is present, the Action does not invoke `codex exec`. The
repository-owned Adapter remains the only component that starts model rounds.

### Actor authorization

Maintainer-triggered manual runs pass the Action's normal repository-write
access check. Runs dispatched by the installed GitHub App use the exact bot
allowlist entry `wukongim-issue-agent`, which also matches the corresponding
`[bot]` login form accepted by the Action.

The broad `allow-bots: true` switch is forbidden. A Workflow contract test
locks the exact allowlist value and rejects wildcards or additional entries.

### Step ordering

Every operation that may need network setup or `sudo` occurs before the
bootstrap:

1. protected and exact-revision checkouts;
2. Go setup;
3. trusted Worker build;
4. immutable dependency prefetch;
5. exact reproduction binary builds when required;
6. digest-pinned sandbox image pull;
7. official Codex bootstrap and irreversible `sudo` removal;
8. repository-owned bounded Worker execution;
9. sanitized Artifact upload.

No later step may require `sudo`. The Action's own verification that `sudo`
fails is supplemented by a Workflow contract that keeps the bootstrap after
all setup and immediately before Worker execution.

### Secret flow

`CODEX_API_KEY` appears only as the Action's `openai-api-key` input in the
`issue-agent-codex` Environment. It is removed from:

- the bounded Worker step environment;
- `cmd/wkissueagent`;
- `IssueAgentWorkerConfig`;
- `CodexCLIConfig`; and
- every Codex subprocess environment.

The Action passes the key to the proxy on standard input and unsets the proxy
environment copy. The proxy remains local to the job. The Worker receives only
the bootstrap-home path through
`ISSUE_AGENT_CODEX_BOOTSTRAP_HOME`.

Publisher credentials remain absent from the Codex job. DeepSeek credentials
remain absent from the Codex job. The Publisher and DeepSeek jobs must not
contain either the bootstrap-home variable or Codex API key.

## Codex Adapter Design

### Configuration boundary

`CodexCLIConfig` changes from:

```go
type CodexCLIConfig struct {
    Binary     string
    APIKey     string
    MinVersion string
    TempRoot   string
}
```

to:

```go
type CodexCLIConfig struct {
    Binary        string
    BootstrapHome string
    MinVersion    string
    TempRoot      string
}
```

`IssueAgentWorkerConfig.CodexAPIKey` becomes
`IssueAgentWorkerConfig.CodexBootstrapHome`.
`cmd/wkissueagent` reads only `ISSUE_AGENT_CODEX_BOOTSTRAP_HOME` for the Codex
transport.

Construction remains provider-selective: a DeepSeek task does not require or
inspect a Codex bootstrap home, and a Codex task does not require or inspect a
DeepSeek key.

### Strict proxy validation

`NewCodexCLIRunner` validates
`<BootstrapHome>/config.toml` with the repository's pinned TOML parser. The
decoded document must contain exactly:

```toml
model_provider = "codex-action-responses-proxy"

[model_providers.codex-action-responses-proxy]
name = "Codex Action Responses Proxy"
base_url = "http://127.0.0.1:<port>/v1"
wire_api = "responses"
```

Comments and whitespace are immaterial. Every other top-level key, provider,
provider field, table, or value is rejected. In particular, the validator
rejects:

- non-loopback hostnames and every IPv6, wildcard, or alternate IPv4 address;
- HTTPS, WebSocket, Unix-socket, relative, user-info, query, and fragment URLs;
- paths other than `/v1`;
- ports outside `1..65535`;
- `env_key`, API keys, headers, query parameters, credentials, or TLS options;
- unknown model, tool, MCP, skill, notification, hook, telemetry, or project
  configuration; and
- symlinked, non-regular, oversized, group-writable, or world-writable config
  files.

The bootstrap home itself is not used as a Codex home during a round. The
validator extracts only the approved loopback URL into an in-memory immutable
configuration.

### Ephemeral rounds

Every `RunRound` continues to:

- create a unique mode-`0700` temporary root;
- create an empty workspace beneath it;
- set both `HOME` and `CODEX_HOME` to that temporary root;
- remove the complete root after the round;
- use `--ephemeral`, `--ignore-user-config`, `--ignore-rules`,
  `--strict-config`, and `--skip-git-repo-check`;
- keep the legacy `read-only` Codex sandbox and `approval_policy="never"`;
- disable shell, unified exec, apps, browser, computer use, and image
  generation;
- bound stdout, stderr, final-envelope size, wall time, and round count; and
- parse authoritative usage from Codex's JSON event stream.

Instead of loading the Action's file, the runner supplies the validated values
as canonical `-c` overrides for the one approved model provider. This retains
`--ignore-user-config` and prevents the CLI from loading personal state,
project state, credentials, or additional providers. The child environment
contains only the fixed executable `PATH`, temporary `HOME`, and temporary
`CODEX_HOME`; it contains no API key, proxy credential, GitHub token, or
publisher data.

The model name and reasoning behavior continue to come from the signed task and
existing provider policy. The Action does not select them.

## Failure Handling

The integration fails closed before any model round when:

- the Action cannot authorize the actor;
- either the Action or matching proxy package cannot be installed;
- the proxy does not start or write server information;
- `sudo` is not removed;
- the bootstrap config is missing, malformed, unsafe, or contains extra data;
- the Codex binary is missing or older than the configured minimum;
- the loopback proxy becomes unavailable;
- Codex emits an invalid envelope or exceeds a bound; or
- any existing Broker, sandbox, Artifact, checkpoint, or budget validation
  fails.

These failures produce the existing bounded Worker failure result. They do not
fall back to direct API-key injection, a public OpenAI endpoint, an alternate
provider, native Codex tools, or a less restrictive sandbox.

Logs may report the selected provider, CLI version, Action commit, bootstrap
validation result, round number, token usage, and normalized failure class.
They must not print the API key, raw proxy server-info file, process
environment, full prompt, reasoning content, or unredacted model response.

## DeepSeek Compatibility

The DeepSeek Adapter and `deepseek-worker` job retain their existing direct API
contract and credential-separated Environment. The provider-neutral task,
Broker, Artifact, and Publisher contracts remain shared.

The official Action is intentionally not abstracted into a universal provider
bootstrap. OpenAI and DeepSeek may use different transport mechanisms behind
the same narrow `ModelRunner` boundary. Adding another provider requires its
own reviewed Adapter and credential boundary, not compatibility shims in the
Codex Action integration.

## Test Strategy

Implementation follows test-driven development.

### Codex runner tests

Tests first demonstrate that the current direct-key runner violates the new
contract, then cover:

- accepting the exact Action-generated loopback configuration;
- rejecting every unknown top-level key, provider, and provider field;
- rejecting unsafe schemes, hosts, paths, ports, URL components, file modes,
  symlinks, file types, and oversized input;
- preserving minimum Codex version validation;
- producing only canonical provider overrides;
- retaining `--ignore-user-config`, `--ignore-rules`, strict config, read-only
  sandbox, never-approval, disabled native tools, and an empty workspace;
- omitting `CODEX_API_KEY` and all inherited environment variables;
- using a unique temporary home for every round and deleting it afterward;
- preserving bounded output and usage parsing; and
- returning a generic failure without leaking proxy or secret material.

A fake Codex executable records arguments and environment into test-owned
files. Tests do not call OpenAI or start a real model.

### Wiring tests

Application and CLI tests cover:

- Codex tasks requiring a valid bootstrap home rather than an API key;
- DeepSeek tasks remaining independent of Codex configuration;
- Codex tasks remaining independent of DeepSeek credentials;
- Publisher secrets still rejecting Worker construction; and
- `cmd/wkissueagent` reading the new environment variable and ignoring no
  compatibility fallback for `CODEX_API_KEY`.

### Workflow contract tests

Static Workflow tests require:

- the exact full Action SHA and reviewed version comment;
- exact Codex CLI version `0.145.0`;
- a runner-temporary, initially empty bootstrap home;
- `safety-strategy: drop-sudo`;
- exact `allow-bot-users: wukongim-issue-agent`;
- no prompt, prompt file, working directory, model, effort, extra arguments,
  output, sandbox, permission profile, wildcard, or broad bot allowance;
- the API key only on the official Action input;
- no `CODEX_API_KEY` in the bounded Worker command or application wiring;
- the bootstrap-home variable only in the Codex Worker;
- all setup, build, prefetch, and image-pull steps preceding the bootstrap;
- the bounded Worker and Artifact upload following it; and
- the DeepSeek and Publisher secret boundaries remaining unchanged.

Mutation-style fixtures must prove that moving the step, using a tag, adding a
prompt, broadening actors, forwarding the key, or changing the safety strategy
fails the contract.

### Required verification

The implementation must pass:

```text
GOWORK=off go test ./internal/contracts/issueagent \
  ./internal/usecase/issueagent \
  ./internal/runtime/issueagentworker \
  ./internal/infra/issueagentgithub \
  ./internal/infra/issueagentmodel \
  ./internal/access/issueagentcli \
  ./internal/app \
  ./scripts -count=1
```

It must also parse all three Issue Agent Workflows as YAML and pass `actionlint`
for:

- `.github/workflows/issue-agent-control.yml`;
- `.github/workflows/issue-agent-run.yml`; and
- `.github/workflows/issue-agent-reconcile.yml`.

No live OpenAI request is part of PR CI.

## Documentation Changes

The implementation updates:

- `.github/workflows/README.md` with the Action authorization, validation plan,
  secret boundary, irreversible step ordering, and monitoring contract;
- `docs/agents/issue-agent.md` with the Codex Environment setup and bootstrap
  behavior;
- `internal/infra/issueagentmodel/FLOW.md` with the validated-proxy Adapter
  boundary;
- `internal/app/FLOW.md` if its Worker composition description becomes
  inaccurate; and
- `docs/development/PROJECT_KNOWLEDGE.md` with the stable operational rule that
  Codex uses the official Action only as a bootstrap.

The stable directory structure does not change.

## Rollout

1. Merge the capability PR while keeping the repository rollout at `intake`.
2. Store `CODEX_API_KEY` only in the protected `issue-agent-codex`
   Environment. No repository, organization, DeepSeek, or Publisher secret is
   added.
3. Confirm a trusted manual dispatch and an exact GitHub App dispatch both pass
   the Action actor check without broadening the allowlist.
4. Submit a separate reviewed rollout PR that promotes only the intended
   reproduction phase.
5. Run one authorized canary Issue pinned to an exact known revision. Verify
   Action bootstrap, proxy validation, bounded Codex rounds, E2E reproduction,
   signed checkpoint publication, Artifact sanitation, and absence of Worker
   GitHub writes.
6. Keep later diagnosis and remediation promotions separate. Roll back to
   `intake` on any unexpected failure or boundary violation.

No rollout promotion, GitHub Environment mutation, secret creation, live model
call, or canary execution belongs to the capability PR.

## Acceptance Criteria

- The Codex Worker uses the official Action at the reviewed full commit SHA and
  never a moving tag.
- The Action receives the OpenAI API key and no prompt.
- `wkissueagent` and Codex subprocesses receive no OpenAI API key.
- Every Codex round uses a fresh temporary home and empty workspace.
- Only the exact Action-generated loopback Responses provider is accepted.
- Native Codex tools remain disabled and all useful operations still cross the
  closed Broker into the no-network Docker sandbox.
- The runner cannot use `sudo` once model execution begins.
- DeepSeek, signed state, Artifacts, Publisher writes, validation, and rollout
  semantics remain unchanged.
- Static mutation tests reject weakened Action pins, actor checks, secret flow,
  step ordering, or Codex restrictions.
- Focused Issue Agent tests, Workflow YAML parsing, and `actionlint` pass.
- The capability PR leaves rollout at `intake`.
