# Issue Agent Usecase Flow

`internal/usecase/issueagent` owns deterministic Bug-form admission,
authorization, permission and risk classification, lifecycle tracking,
command parsing, task identity, status rendering, and candidate publication
plans. It performs no HTTP, Git, shell, filesystem, environment, or model
calls.

```text
fresh Issue/PR/Verifier facts + signed IssueAgentState + protected policy
  -> ReconcileIssue
  -> one decision
  -> BuildIssueState

signed task + exact ContextBundle + advisory EngineerResult
  + CandidateSnapshot + clean CandidateEvidence
  -> PlanCandidatePublication
  -> exact branch, commit, Draft PR, and state effect
```

Events are wake-up hints. Duplicate, reordered, or missed events converge from
fresh facts and the signed state ref. Only exact first-line `/agent fix`,
`/agent retry`, `/agent cancel`, and `/agent take-over` commands from a freshly
authorized actor are trusted. A human remains the only merge authority.
