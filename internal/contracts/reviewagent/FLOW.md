# Review Agent Contracts Flow

`internal/contracts/reviewagent` owns the bounded canonical JSON exchanged
between Review Agent roles. It has no GitHub, lifecycle, filesystem, process,
environment, network, or model execution behavior.

```text
fresh pull-request facts + protected policy
  -> ReviewContext
  -> untrusted Codex ReviewResult

trusted named-check runner
  -> ReviewEvidence

fresh authority + validated result + trusted evidence
  -> canonical ReviewState
```

One immutable `GenerationIdentity` binds every document to the exact
repository, pull request, head, base, test-merge revision, intent, generation
number, and state parent. Decoders reject unknown fields, trailing JSON,
oversized input, malformed identities, and unbounded collections. A
model-authored result is advisory and contains no publication authority.
