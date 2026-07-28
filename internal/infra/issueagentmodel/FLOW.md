# Issue Agent Model Adapter Flow

`internal/infra/issueagentmodel` translates the provider-neutral
`TaskEnvelope`, fixed prompt policy, closed tool declarations, and strict
`AgentResult` into one selected provider protocol.

```text
TaskEnvelope + protected prompt
  -> Codex Adapter OR DeepSeek Adapter
  -> bounded tool-call envelope
  -> local schema validation
  -> credential-free Tool Broker
  -> provider transcript continuation
  -> strict AgentResult + normalized usage
```

Provider credentials stay in Adapter memory and are never placed in tool
arguments, target workspaces, evidence, logs, or errors. Unknown tools,
malformed arguments, provider redirects, response overflows, excessive tool
rounds, and silent model/provider changes fail closed. The state machine and
Worker do not interpret provider-specific messages.
