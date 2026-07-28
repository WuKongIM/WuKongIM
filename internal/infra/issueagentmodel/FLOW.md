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

Provider credentials stay in Adapter memory and are never placed in tool
arguments, target workspaces, evidence, logs, or errors. Unknown tools,
malformed arguments, provider redirects, response overflows, excessive tool
rounds, and silent model/provider changes fail closed. The state machine and
Worker do not interpret provider-specific messages.

Codex runs as one ephemeral, user-config-free `codex exec` per round and uses
only the authoritative `turn.completed` usage record. DeepSeek uses the
OpenAI-compatible tool-call protocol with strict bounded JSON or SSE decoding.
Neither Adapter permits the model to supply a repository `ChangeSet`, command
evidence, Artifact digest, diagnosis evidence digest, or token count. There is
no automatic provider fallback: a provider change requires a new signed
attempt.
