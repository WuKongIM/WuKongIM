# Issue Agent Model Adapter Flow

`internal/infra/issueagentmodel` translates the provider-neutral
`TaskEnvelope`, fixed prompt policy, closed tool declarations, and strict model
proposal into one selected provider protocol.

```text
TaskEnvelope + protected prompt
  -> Codex Adapter OR DeepSeek Adapter
  -> bounded tool-call envelope
  -> strict local JSON and semantic-proposal validation
  -> credential-free Tool Broker
  -> provider transcript continuation
  -> semantic result + provider-metered usage
```

Provider credentials stay in the selected Supervisor transport boundary and
are never placed in tool arguments, target workspaces, evidence, logs, or
errors. DeepSeek keeps its key in Adapter memory. Codex uses the official
`openai/codex-action` only to install the pinned CLI, start its loopback
Responses proxy, and drop `sudo`; neither `wkissueagent` nor a Codex subprocess
receives `CODEX_API_KEY`. Unknown tools, malformed arguments, provider
redirects, response overflows, excessive tool rounds, and silent
model/provider changes fail closed. The state machine and Worker do not
interpret provider-specific messages.

The Codex Adapter accepts only the Action's exact
`http://127.0.0.1:<port>/v1` Responses provider from a regular bootstrap config
that is not group- or world-writable. It rejects every extra setting, then
passes canonical provider overrides to one ephemeral, user-config-free
`codex exec` per round. Each round receives a fresh empty home and workspace
while retaining the trusted Action-established executable path so the
npm-installed Codex launcher can reach its pinned Node runtime. It retains
read-only/never-approval policy with native tools disabled and uses
only the authoritative `turn.completed` usage record. DeepSeek uses the
OpenAI-compatible tool-call protocol with strict bounded JSON or SSE decoding.
Neither Adapter permits the model to supply a repository `ChangeSet`, command
evidence, Artifact digest, diagnosis evidence digest, or token count. There is
no automatic provider fallback: a provider change requires a new signed
attempt.
