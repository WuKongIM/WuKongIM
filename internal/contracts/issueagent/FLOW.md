# Issue Agent Contracts Flow

`internal/contracts/issueagent` owns the bounded JSON objects shared across
the Issue Agent roles. It contains no GitHub, lifecycle, filesystem, process,
or model logic.

```text
fresh GitHub reads + protected policy
  -> ContextBundle (trusted authority + exact-source context-document blobs
     + untrusted conversation)
  -> Codex Engineer
  -> advisory EngineerResult

immutable baseline + Engineer workspace
  -> CandidateSnapshot
  -> clean Verifier
  -> CandidateEvidence

fresh GitHub fences + exact candidate/evidence digests
  -> canonical IssueAgentState

exact Publisher-owned base synchronization
  -> new source/head identities + incremented base-sync budget
  -> prior Review authority cleared
```

All decoded JSON objects reject unknown fields, oversized input, malformed
identities, and trailing JSON. `EngineerResult` may unwrap exactly one
unambiguous JSON object from bounded model prose or one Markdown `json` fence
before applying that strict JSON contract; competing containers and
brace-bearing prose fail closed. `EngineerResult` never grants publication
authority. Only a low-risk, passing `CandidateEvidence` bound to the exact task
and candidate can enter a Publisher plan.
