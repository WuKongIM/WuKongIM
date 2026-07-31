# Issue Agent Contracts Flow

`internal/contracts/issueagent` owns the bounded JSON objects shared across
the Issue Agent roles. It contains no GitHub, lifecycle, filesystem, process,
or model logic.

```text
fresh GitHub reads + protected policy
  -> ContextBundle (trusted authority + exact-source instruction blobs
     + untrusted conversation)
  -> Codex Engineer
  -> advisory EngineerResult

immutable baseline + Engineer workspace
  -> CandidateSnapshot
  -> clean Verifier
  -> CandidateEvidence

fresh GitHub fences + exact candidate/evidence digests
  -> canonical IssueAgentState
```

All decoded JSON objects reject unknown fields, oversized input, malformed
identities, and trailing JSON. `EngineerResult` may unwrap exactly one
Markdown `json` fence from a model's final message before applying that strict
JSON contract; ambiguous or multiple fences fail closed. `EngineerResult`
never grants publication authority. Only a low-risk, passing
`CandidateEvidence` bound to the exact task and candidate can enter a
Publisher plan.
