# Issue Agent Codex Action Bootstrap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route Issue Agent Codex rounds through the official Action's loopback Responses proxy without giving the API key or new capabilities to `wkissueagent`.

**Architecture:** A pinned bootstrap-only `openai/codex-action` step installs Codex, starts the proxy, and drops `sudo`. The repository-owned Codex runner strictly parses the Action's otherwise empty bootstrap home, converts the one accepted loopback provider into canonical CLI overrides, and continues to run each round in a fresh empty home with native tools disabled. DeepSeek, the closed Broker, Docker sandbox, signed state, Artifact, Publisher, validation, and `intake` rollout do not change.

**Tech Stack:** Go 1.25, `github.com/pelletier/go-toml/v2`, GitHub Actions YAML, `testify/require`, `actionlint`.

---

## File Map

- Create `internal/infra/issueagentmodel/codex_proxy.go`: parse and validate only the Action-generated loopback Responses provider.
- Create `internal/infra/issueagentmodel/codex_proxy_test.go`: exhaustive TOML, URL, mode, size, and symlink rejection tests.
- Create `internal/infra/issueagentmodel/codex_cli_test.go`: fake-process assertions for CLI arguments, environment, usage, and ephemeral-home cleanup.
- Modify `internal/infra/issueagentmodel/codex.go`: replace the direct API key with validated proxy configuration and canonical CLI overrides.
- Modify `internal/infra/issueagentmodel/FLOW.md`: document the bootstrap proxy and retained ephemeral-round boundary.
- Create `internal/app/issue_agent_worker_test.go`: provider-separation and Publisher-credential wiring tests.
- Modify `internal/app/issue_agent.go`: pass the bootstrap home into the Codex runner.
- Modify `cmd/wkissueagent/main.go`: read `ISSUE_AGENT_CODEX_BOOTSTRAP_HOME` and remove `CODEX_API_KEY` wiring.
- Modify `cmd/wkissueagent/main_test.go`: lock the environment-to-config boundary.
- Modify `scripts/github_workflows_test.go`: approve the reviewed full Action SHA.
- Modify `scripts/issue_agent_workflows_test.go`: lock the bootstrap inputs, actor allowlist, secret flow, and step ordering.
- Modify `.github/workflows/issue-agent-run.yml`: replace direct npm/key setup with the bootstrap-only official Action.
- Modify `.github/workflows/README.md`: document authorization, validation, secret, ordering, and monitoring contracts.
- Modify `docs/agents/issue-agent.md`: update operator setup and local verification guidance.
- Modify `docs/development/PROJECT_KNOWLEDGE.md`: record the stable bootstrap-only rule and correct the rollout statement to `intake`.

### Task 1: Strict Action proxy configuration boundary

**Files:**
- Create: `internal/infra/issueagentmodel/codex_proxy_test.go`
- Create: `internal/infra/issueagentmodel/codex_proxy.go`

- [ ] **Step 1: Write the failing parser tests**

Create table-driven tests in package `issueagentmodel` with this valid document
and explicit invalid mutations:

```go
const validCodexActionProxyConfig = `
# Added by codex-action.
model_provider = "codex-action-responses-proxy"

[model_providers.codex-action-responses-proxy]
name = "Codex Action Responses Proxy"
base_url = "http://127.0.0.1:43123/v1"
wire_api = "responses"
`

func TestLoadCodexActionProxyConfigAcceptsExactLoopbackProvider(t *testing.T) {
	home := writeCodexBootstrapHome(t, validCodexActionProxyConfig, 0o644)
	config, err := loadCodexActionProxyConfig(home)
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:43123/v1", config.baseURL)
}

func TestLoadCodexActionProxyConfigRejectsUnsafeDocuments(t *testing.T) {
	tests := map[string]string{
		"unknown top-level key": validCodexActionProxyConfig + "\nmodel = \"gpt-5\"\n",
		"second provider": validCodexActionProxyConfig + "\n[model_providers.other]\nname=\"x\"\nbase_url=\"http://127.0.0.1:1/v1\"\nwire_api=\"responses\"\n",
		"secret field": strings.Replace(validCodexActionProxyConfig, "wire_api = \"responses\"", "wire_api = \"responses\"\nenv_key = \"CODEX_API_KEY\"", 1),
		"https": strings.Replace(validCodexActionProxyConfig, "http://", "https://", 1),
		"non-loopback": strings.Replace(validCodexActionProxyConfig, "127.0.0.1", "localhost", 1),
		"wrong path": strings.Replace(validCodexActionProxyConfig, "/v1", "/v1/responses", 1),
		"query": strings.Replace(validCodexActionProxyConfig, "/v1", "/v1?token=x", 1),
		"fragment": strings.Replace(validCodexActionProxyConfig, "/v1", "/v1#x", 1),
		"missing port": strings.Replace(validCodexActionProxyConfig, ":43123", "", 1),
		"zero port": strings.Replace(validCodexActionProxyConfig, "43123", "0", 1),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := loadCodexActionProxyConfig(
				writeCodexBootstrapHome(t, body, 0o644),
			)
			require.EqualError(t, err, "Codex Action proxy configuration is invalid")
		})
	}
}
```

Add dedicated tests that reject a relative/empty home, missing file, symlink,
directory, zero-byte file, a file larger than 16 KiB, and modes `0664` and
`0646`.

- [ ] **Step 2: Run the parser tests and verify RED**

Run:

```bash
GOWORK=off go test ./internal/infra/issueagentmodel \
  -run '^TestLoadCodexActionProxyConfig' -count=1
```

Expected: compile failure because `loadCodexActionProxyConfig` is undefined.

- [ ] **Step 3: Implement the closed parser**

Implement these concrete types and functions:

```go
const (
	codexActionProviderName = "codex-action-responses-proxy"
	codexActionDisplayName  = "Codex Action Responses Proxy"
	maxCodexProxyConfigSize = 16 << 10
)

type codexActionProxyConfig struct {
	baseURL string
}

func loadCodexActionProxyConfig(home string) (codexActionProxyConfig, error) {
	invalid := func() (codexActionProxyConfig, error) {
		return codexActionProxyConfig{},
			errors.New("Codex Action proxy configuration is invalid")
	}
	if home == "" || !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return invalid()
	}
	homeInfo, err := os.Lstat(home)
	if err != nil || !homeInfo.IsDir() || homeInfo.Mode()&os.ModeSymlink != 0 {
		return invalid()
	}
	file, err := openCodexActionProxyConfig(home)
	if err != nil {
		return invalid()
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() ||
		info.Mode().Perm()&0o022 != 0 ||
		info.Size() <= 0 || info.Size() > maxCodexProxyConfigSize {
		return invalid()
	}
	body, err := io.ReadAll(io.LimitReader(file, maxCodexProxyConfigSize+1))
	if err != nil || int64(len(body)) != info.Size() {
		return invalid()
	}
	var document map[string]any
	if toml.Unmarshal(body, &document) != nil ||
		!hasExactKeys(document, "model_provider", "model_providers") ||
		document["model_provider"] != codexActionProviderName {
		return invalid()
	}
	providers, ok := document["model_providers"].(map[string]any)
	if !ok || !hasExactKeys(providers, codexActionProviderName) {
		return invalid()
	}
	provider, ok := providers[codexActionProviderName].(map[string]any)
	if !ok || !hasExactKeys(provider, "name", "base_url", "wire_api") ||
		provider["name"] != codexActionDisplayName ||
		provider["wire_api"] != "responses" {
		return invalid()
	}
	baseURL, ok := provider["base_url"].(string)
	if !ok || !validCodexProxyURL(baseURL) {
		return invalid()
	}
	return codexActionProxyConfig{baseURL: baseURL}, nil
}
```

`openCodexActionProxyConfig` must open the absolute home directory with
`unix.Open(..., O_DIRECTORY|O_NOFOLLOW|O_CLOEXEC)`, then open only
`config.toml` relative to that descriptor with
`unix.Openat(..., O_NOFOLLOW|O_CLOEXEC)`. Convert that descriptor to one
`os.File` and perform both `Stat` and bounded read through it; never reopen the
validated pathname.

`validCodexProxyURL` must use `net/url`, require the literal scheme `http`,
hostname `127.0.0.1`, an explicit decimal port in `1..65535`, exact path `/v1`,
and empty user info, raw query, fragment, force-query, and opaque fields. It
must finally compare the raw string with
`fmt.Sprintf("http://127.0.0.1:%d/v1", port)` so alternate spellings fail.

- [ ] **Step 4: Run parser tests and verify GREEN**

Run:

```bash
GOWORK=off go test ./internal/infra/issueagentmodel \
  -run '^TestLoadCodexActionProxyConfig' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the parser boundary**

```bash
git add internal/infra/issueagentmodel/codex_proxy.go \
  internal/infra/issueagentmodel/codex_proxy_test.go
git commit -m "feat(agent): validate Codex Action proxy config"
```

### Task 2: Remove the API key from Codex CLI rounds

**Files:**
- Create: `internal/infra/issueagentmodel/codex_cli_test.go`
- Modify: `internal/infra/issueagentmodel/codex.go:173-274`

- [ ] **Step 1: Write the failing process-boundary test**

Create a test fake executable that returns `codex-cli 0.145.0` for
`--version`; for `exec` it records one argument per line, records the
environment and stdin, writes a strict final envelope to the
`--output-last-message` path, and emits:

```json
{"type":"turn.completed","usage":{"input_tokens":25,"output_tokens":7}}
```

Use it in:

```go
func TestCodexCLIRunnerUsesProxyWithoutCredentialOrPersistentHome(t *testing.T) {
	capture := t.TempDir()
	binary := writeFakeCodexCLI(t, capture, "0.145.0")
	bootstrap := writeCodexBootstrapHome(
		t, validCodexActionProxyConfig, 0o644,
	)
	runner, err := NewCodexCLIRunner(CodexCLIConfig{
		Binary: binary, BootstrapHome: bootstrap,
		MinVersion: "0.145.0", TempRoot: t.TempDir(),
	})
	require.NoError(t, err)

	response, err := runner.RunRound(context.Background(), CodexRoundRequest{
		Model: "gpt-5.6-sol", Prompt: "strict prompt", MaxBytes: 1 << 20,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(25), response.InputTokens)
	require.Equal(t, uint64(7), response.OutputTokens)

	args := readCapture(t, capture, "args")
	require.Contains(t, args, "--ignore-user-config\n")
	require.Contains(t, args, "model_provider=\"codex-action-responses-proxy\"\n")
	require.Contains(t, args, "model_providers.codex-action-responses-proxy.base_url=\"http://127.0.0.1:43123/v1\"\n")
	require.Contains(t, args, "approval_policy=\"never\"\n")
	for _, disabled := range []string{
		"shell_tool", "unified_exec", "apps",
		"browser_use", "computer_use", "image_generation",
	} {
		require.Contains(t, args, disabled+"\n")
	}
	environment := readCapture(t, capture, "env")
	require.NotContains(t, environment, "CODEX_API_KEY")
	require.NotContains(t, environment, "DEEPSEEK_API_KEY")
	require.NotContains(t, environment, "GITHUB_TOKEN")
	roundHome := captureValue(t, environment, "CODEX_HOME")
	require.NoDirExists(t, roundHome)
	require.Equal(t, "strict prompt", readCapture(t, capture, "stdin"))
}
```

Add cases for an old binary, missing bootstrap home, invalid request, generic
process failure, bounded final output, and two rounds receiving distinct homes.

- [ ] **Step 2: Run the CLI tests and verify RED**

Run:

```bash
GOWORK=off go test ./internal/infra/issueagentmodel \
  -run '^TestCodexCLIRunner' -count=1
```

Expected: compile failure because `CodexCLIConfig` still has `APIKey` and no
`BootstrapHome`.

- [ ] **Step 3: Implement proxy-backed CLI rounds**

Change the concrete configuration and runner:

```go
type CodexCLIConfig struct {
	Binary        string
	BootstrapHome string
	MinVersion    string
	TempRoot      string
}

type CodexCLIRunner struct {
	config CodexCLIConfig
	proxy  codexActionProxyConfig
}
```

`NewCodexCLIRunner` must load the strict proxy config and keep the existing
minimum-version check. Add these canonical overrides immediately after
`approval_policy`:

```go
"-c", `model_provider="codex-action-responses-proxy"`,
"-c", `model_providers.codex-action-responses-proxy.name="Codex Action Responses Proxy"`,
"-c", `model_providers.codex-action-responses-proxy.base_url=` + strconv.Quote(runner.proxy.baseURL),
"-c", `model_providers.codex-action-responses-proxy.wire_api="responses"`,
```

Keep `--ignore-user-config`, the empty workspace, strict config, read-only
sandbox, never approval, every disabled native tool, JSON events, bounds, and
cleanup. Replace the child environment with exactly:

```go
command.Env = []string{
	"PATH=/usr/local/bin:/usr/bin:/bin",
	"HOME=" + tempRoot,
	"CODEX_HOME=" + tempRoot,
}
```

- [ ] **Step 4: Run all model Adapter tests**

Run:

```bash
GOWORK=off go test ./internal/infra/issueagentmodel -count=1
```

Expected: PASS, including existing Codex usage and DeepSeek tests.

- [ ] **Step 5: Commit the credential removal**

```bash
git add internal/infra/issueagentmodel/codex.go \
  internal/infra/issueagentmodel/codex_cli_test.go
git commit -m "feat(agent): run Codex through bootstrap proxy"
```

### Task 3: Preserve provider-separated application wiring

**Files:**
- Create: `internal/app/issue_agent_worker_test.go`
- Modify: `internal/app/issue_agent.go:61-70,4273-4303`
- Modify: `cmd/wkissueagent/main.go:35-49`
- Modify: `cmd/wkissueagent/main_test.go`

- [ ] **Step 1: Write failing wiring tests**

Add an application test with two subtests:

```go
func TestComposeModelRunnerKeepsProviderInputsSeparated(t *testing.T) {
	t.Run("DeepSeek does not inspect Codex bootstrap", func(t *testing.T) {
		_, err := composeModelRunner(IssueAgentWorkerConfig{
			DeepSeekAPIKey: "deepseek-test-key",
			HTTPClient: &http.Client{Timeout: time.Second},
		}, issueagentcontract.TaskEnvelope{
			Provider: issueagentcontract.ProviderDeepSeek,
		})
		require.NoError(t, err)
	})

	t.Run("Codex does not require DeepSeek key", func(t *testing.T) {
		binary, bootstrap := writeAppTestCodexBootstrap(t)
		_, err := composeModelRunner(IssueAgentWorkerConfig{
			CodexBinary: binary,
			CodexBootstrapHome: bootstrap,
			CodexMinimumVersion: "0.145.0",
		}, issueagentcontract.TaskEnvelope{
			Provider: issueagentcontract.ProviderCodex,
		})
		require.NoError(t, err)
	})
}
```

Add `TestIssueAgentWorkerRejectsPublisherCredentialsBeforePayload` to prove the
existing `ForbiddenPublisherData` check still runs before payload parsing.

Refactor `cmd/wkissueagent/main.go` to expose an unexported
`issueAgentWorkerConfigFromEnv` helper, then test:

```go
func TestIssueAgentWorkerConfigUsesCodexBootstrapWithoutAPIKey(t *testing.T) {
	t.Setenv("CODEX_API_KEY", "must-not-be-read")
	t.Setenv("ISSUE_AGENT_CODEX_BOOTSTRAP_HOME", "/runner/temp/codex")
	config := issueAgentWorkerConfigFromEnv()
	require.Equal(t, "/runner/temp/codex", config.CodexBootstrapHome)
}
```

- [ ] **Step 2: Run wiring tests and verify RED**

Run:

```bash
GOWORK=off go test ./internal/app ./cmd/wkissueagent \
  -run 'TestComposeModelRunner|TestIssueAgentWorker|TestIssueAgentWorkerConfig' \
  -count=1
```

Expected: compile failures for the missing `CodexBootstrapHome` field/helper.

- [ ] **Step 3: Change application and CLI wiring**

Replace `IssueAgentWorkerConfig.CodexAPIKey` with:

```go
// CodexBootstrapHome contains only the official Action's local proxy config.
CodexBootstrapHome string
```

Pass `BootstrapHome: config.CodexBootstrapHome` to `CodexCLIConfig`.
Build the CLI config through:

```go
func issueAgentWorkerConfigFromEnv() app.IssueAgentWorkerConfig {
	return app.IssueAgentWorkerConfig{
		HTTPClient:          &http.Client{Timeout: 2 * time.Minute},
		DeepSeekAPIKey:      os.Getenv("DEEPSEEK_API_KEY"),
		CodexBootstrapHome: os.Getenv("ISSUE_AGENT_CODEX_BOOTSTRAP_HOME"),
		CodexBinary:         os.Getenv("ISSUE_AGENT_CODEX_BINARY"),
		CodexMinimumVersion: os.Getenv("ISSUE_AGENT_CODEX_MIN_VERSION"),
		SandboxImage:        os.Getenv("ISSUE_AGENT_SANDBOX_IMAGE"),
		ForbiddenPublisherData: os.Getenv("ISSUE_AGENT_GITHUB_TOKEN") != "" ||
			os.Getenv("ISSUE_AGENT_CHECKPOINT_PRIVATE_KEY") != "" ||
			os.Getenv("ISSUE_AGENT_APP_PRIVATE_KEY") != "",
	}
}
```

There is no `CODEX_API_KEY` compatibility fallback.

- [ ] **Step 4: Run wiring and model tests**

Run:

```bash
GOWORK=off go test ./internal/app ./cmd/wkissueagent \
  ./internal/infra/issueagentmodel -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit provider-separated wiring**

```bash
git add internal/app/issue_agent.go \
  internal/app/issue_agent_worker_test.go \
  cmd/wkissueagent/main.go cmd/wkissueagent/main_test.go
git commit -m "refactor(agent): wire Codex bootstrap home"
```

### Task 4: Lock the Workflow contract before changing YAML

**Files:**
- Modify: `scripts/github_workflows_test.go:87-112`
- Modify: `scripts/issue_agent_workflows_test.go:48-182`

- [ ] **Step 1: Add the approved immutable Action pin**

Add:

```go
"openai/codex-action": {
	sha:     "52fe01ec70a42f454c9d2ebd47598f9fd6893d56",
	release: "v1.11",
},
```

to `approvedActionPins`.

- [ ] **Step 2: Add a failing exact bootstrap contract test**

Parse `issue-agent-run.yml`, find the `codex-worker` steps by name, and assert:

```go
require.Equal(t,
	"openai/codex-action@52fe01ec70a42f454c9d2ebd47598f9fd6893d56",
	bootstrap.Uses,
)
require.Equal(t, map[string]any{
	"openai-api-key": "${{ secrets.CODEX_API_KEY }}",
	"codex-version": "0.145.0",
	"codex-home": "${{ runner.temp }}/issue-agent-codex-bootstrap",
	"safety-strategy": "drop-sudo",
	"allow-bot-users": "wukongim-issue-agent",
}, bootstrap.With)
require.Less(t, stepIndex(t, job, "Pull the digest-pinned sandbox without provider credentials"), stepIndex(t, job, "Bootstrap the pinned Codex CLI and Responses proxy"))
require.Less(t, stepIndex(t, job, "Bootstrap the pinned Codex CLI and Responses proxy"), stepIndex(t, job, "Run the bounded Codex Worker"))
require.Equal(t, 1, strings.Count(string(raw), "secrets.CODEX_API_KEY"))
require.NotContains(t, worker.Env, "ISSUE_AGENT_CODEX_API_KEY")
require.Equal(t,
	"${{ runner.temp }}/issue-agent-codex-bootstrap",
	worker.Env["ISSUE_AGENT_CODEX_BOOTSTRAP_HOME"],
)
```

Because `bootstrap.With` is compared exactly, prompt, workspace, model, effort,
args, output, sandbox, permission profile, broad bot allowance, wildcards, and
extra fields all fail.

Add mutation subtests that alter the decoded bootstrap step to use a tag,
`unsafe`, `allow-bots: true`, a prompt, or an additional bot. Each mutation
must fail the shared `validateCodexBootstrapStep` helper. Whole-job mutations
that place the bootstrap before the image pull or forward `CODEX_API_KEY` to
the Worker must fail `validateCodexWorkerBoundary`.

- [ ] **Step 3: Run the Workflow test and verify RED**

Run:

```bash
GOWORK=off go test ./scripts \
  -run 'TestIssueAgentCodexWorkerUsesOfficialBootstrap|TestIssueAgentWorkflowSecurityContracts' \
  -count=1
```

Expected: FAIL because the official Action step and bootstrap-home environment
do not exist.

- [ ] **Step 4: Commit no failing tests**

Do not commit at RED. Continue directly to Task 5 so the contract and Workflow
land in one green commit.

### Task 5: Bootstrap the official Action in the Codex Worker

**Files:**
- Modify: `.github/workflows/issue-agent-run.yml:136-236`
- Modify: `scripts/github_workflows_test.go`
- Modify: `scripts/issue_agent_workflows_test.go`

- [ ] **Step 1: Remove direct npm installation**

Delete `Install the pinned Codex Adapter binary`. The official Action installs
both `@openai/codex@0.145.0` and its matching Responses proxy.

- [ ] **Step 2: Add the bootstrap after all privileged setup**

Immediately after the digest-pinned Docker pull, reject every existing path,
including a dangling symlink:

```yaml
- name: Verify Codex bootstrap home is absent
  shell: bash
  run: |
    set -euo pipefail
    bootstrap_home="$RUNNER_TEMP/issue-agent-codex-bootstrap"
    if [[ -e "$bootstrap_home" || -L "$bootstrap_home" ]]; then
      echo "Codex bootstrap home already exists" >&2
      exit 1
    fi
```

Place the bootstrap directly after that check:

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

Do not add a prompt or any execution input.

- [ ] **Step 3: Pass only the bootstrap-home path to the Worker**

Change the bounded Worker environment to:

```yaml
env:
  ISSUE_AGENT_CODEX_BOOTSTRAP_HOME: ${{ runner.temp }}/issue-agent-codex-bootstrap
  ISSUE_AGENT_CODEX_BINARY: codex
  ISSUE_AGENT_CODEX_MIN_VERSION: 0.145.0
  TASK_BASE64: ${{ needs.planner.outputs.task_base64 }}
  PHASE: ${{ needs.planner.outputs.phase }}
```

Invoke `wkissueagent` directly with only `ISSUE_AGENT_SANDBOX_IMAGE`; remove
`ISSUE_AGENT_CODEX_API_KEY` and the `CODEX_API_KEY=...` prefix.

- [ ] **Step 4: Run static Workflow contracts**

Run:

```bash
GOWORK=off go test ./scripts \
  -run 'TestIssueAgentCodexWorkerUsesOfficialBootstrap|TestIssueAgentWorkflowSecurityContracts|TestIssueAgentWorkflowRunUsesSeparateReadOnlyCheckouts' \
  -count=1
```

Expected: PASS.

- [ ] **Step 5: Parse and lint the Workflow**

Run:

```bash
ruby -e 'require "yaml"; YAML.load_file(".github/workflows/issue-agent-run.yml", aliases: true)'
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.9 \
  .github/workflows/issue-agent-run.yml
```

Expected: YAML parse succeeds; actionlint has no new finding. The repository's
documented temporary `concurrency.queue` parser limitation may be filtered only
by the existing verification command, never by editing the Workflow.

- [ ] **Step 6: Commit the protected Workflow change**

```bash
git add .github/workflows/issue-agent-run.yml \
  scripts/github_workflows_test.go scripts/issue_agent_workflows_test.go
git commit -m "ci(agent): bootstrap Codex with official Action"
```

### Task 6: Update stable flow and operator documentation

**Files:**
- Modify: `internal/infra/issueagentmodel/FLOW.md`
- Modify: `.github/workflows/README.md`
- Modify: `docs/agents/issue-agent.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Update the model flow**

State that Codex transport is bootstrapped by the pinned official Action, the
Adapter accepts only its exact loopback Responses provider, every round uses
canonical overrides and a fresh empty home, and no Codex subprocess receives
the API key. Retain the no-fallback rule.

- [ ] **Step 2: Update the Workflow contract**

Document:

- the exact App bot allowlist;
- `drop-sudo` as irreversible job ordering;
- all build, dependency, and Docker setup before the Action;
- the key existing only on the Action input;
- the bootstrap home containing no user/project configuration;
- expected monitoring fields and prohibited secret logging; and
- Action/CLI upgrades requiring new full-SHA review and local contract tests.

- [ ] **Step 3: Update operator setup and project knowledge**

Keep `CODEX_API_KEY` only in `issue-agent-codex`, but explain that the official
Action owns it and `wkissueagent` receives only
`ISSUE_AGENT_CODEX_BOOTSTRAP_HOME`. Record the bootstrap-only rule in
`PROJECT_KNOWLEDGE.md` and change its stale `shadow` rollout wording to the
actual checked-in `intake`.

- [ ] **Step 4: Verify documentation consistency**

Run:

```bash
rg -n 'CodexAPIKey|ISSUE_AGENT_CODEX_API_KEY|rollout remains `shadow`' \
  internal cmd .github/workflows .github/workflows/README.md \
  docs/agents docs/development
```

Expected: no matches.

Run:

```bash
rg -n 'openai/codex-action|ISSUE_AGENT_CODEX_BOOTSTRAP_HOME|intake' \
  .github/workflows/README.md docs/agents/issue-agent.md \
  docs/development/PROJECT_KNOWLEDGE.md \
  internal/infra/issueagentmodel/FLOW.md
```

Expected: all four documents contain the applicable new contract.

- [ ] **Step 5: Commit documentation**

```bash
git add internal/infra/issueagentmodel/FLOW.md \
  .github/workflows/README.md docs/agents/issue-agent.md \
  docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs(agent): record Codex Action boundary"
```

### Task 7: Full local verification and design conformance

**Files:**
- Verify all files changed since `origin/main`

- [ ] **Step 1: Format and inspect**

Run:

```bash
gofmt -w internal/infra/issueagentmodel/codex.go \
  internal/infra/issueagentmodel/codex_proxy.go \
  internal/infra/issueagentmodel/codex_proxy_test.go \
  internal/infra/issueagentmodel/codex_cli_test.go \
  internal/app/issue_agent.go internal/app/issue_agent_worker_test.go \
  cmd/wkissueagent/main.go cmd/wkissueagent/main_test.go \
  scripts/github_workflows_test.go scripts/issue_agent_workflows_test.go
git diff --check
```

Expected: no formatting or whitespace error.

- [ ] **Step 2: Run focused Issue Agent verification**

Run:

```bash
GOWORK=off go test ./internal/contracts/issueagent \
  ./internal/usecase/issueagent \
  ./internal/runtime/issueagentworker \
  ./internal/infra/issueagentgithub \
  ./internal/infra/issueagentmodel \
  ./internal/access/issueagentcli \
  ./internal/app ./cmd/wkissueagent ./scripts -count=1
```

Expected: PASS.

- [ ] **Step 3: Parse and lint all Issue Agent Workflows**

Run:

```bash
ruby -e 'require "yaml"; ARGV.each { |path| YAML.load_file(path, aliases: true) }' \
  .github/workflows/issue-agent-control.yml \
  .github/workflows/issue-agent-run.yml \
  .github/workflows/issue-agent-reconcile.yml
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.9 \
  .github/workflows/issue-agent-control.yml \
  .github/workflows/issue-agent-run.yml \
  .github/workflows/issue-agent-reconcile.yml
```

Expected: YAML parsing succeeds and no new actionlint finding exists. Apply
only the documented `concurrency.queue` compatibility filter when comparing
with the repository's established local command.

- [ ] **Step 4: Run the full default unit suite**

Run:

```bash
GOWORK=off go test ./cmd/... ./internal/... ./pkg/... \
  ./scripts/... ./docker/... -count=1
```

Expected: PASS. Do not use root `./...`.

- [ ] **Step 5: Review exact scope**

Run:

```bash
git diff --stat origin/main...HEAD
git diff --name-only origin/main...HEAD
git status --short --branch
```

Expected: only the design, plan, implementation, tests, Workflow, and named
documentation files are present; the worktree is clean.

### Task 8: Publish through the protected PR protocol

**Files:**
- No additional repository files unless review or CI identifies a defect

- [ ] **Step 1: Push the intentional branch**

Run:

```bash
git push -u origin codex/issue-agent-codex-action
```

Expected: the remote branch points to the locally verified head.

- [ ] **Step 2: Open the PR with an exact validation plan**

The PR body must list:

```text
Validation plan:
- GOWORK=off go test ./internal/contracts/issueagent ./internal/usecase/issueagent ./internal/runtime/issueagentworker ./internal/infra/issueagentgithub ./internal/infra/issueagentmodel ./internal/access/issueagentcli ./internal/app ./cmd/wkissueagent ./scripts -count=1
- GOWORK=off go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/... -count=1
- YAML parse and actionlint for the three Issue Agent Workflows

Rollout: remains intake; no live provider call in this PR.
```

Expected: PR targets `main`, uses the feature branch, and the first Agent
Validation Gate attempt fails closed.

- [ ] **Step 3: Record the required Agent review**

Review the exact PR diff against both:

- `docs/superpowers/specs/2026-07-30-issue-agent-codex-action-design.md`;
- repository standards and applicable `FLOW.md` files.

Publish an explicit PR review comment containing findings or `No findings`,
the exact reviewed head SHA, and the exact validation plan. Do this before
adding `agent-ci/run`.

- [ ] **Step 4: Request the protected validation once**

Add `go-fast`, wait for invalidation/control completion, then add
`agent-ci/run` exactly once. Do not retry until the terminal result identifies
a retryable infrastructure failure and the retry contract permits it.

- [ ] **Step 5: Monitor and merge**

Wait for:

- generation-bound request status success;
- generation-bound evidence status success;
- terminal `Agent Validation Gate` success; and
- all other required checks success.

Merge without admin bypass. Confirm `main` contains the merge commit and the
feature branch PR is closed.

- [ ] **Step 6: Leave rollout and live credentials unchanged**

This capability PR does not change `.github/issue-agent/policy.json`, create or
rotate `CODEX_API_KEY`, or execute a model. Credential setup and a reproduction
canary occur only after merge under a separate reviewed rollout change.
