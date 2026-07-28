# Issue Agent Contracts Flow

`internal/contracts/issueagent` owns versioned, bounded DTOs shared by the
Issue Agent access, usecase, runtime, and infrastructure layers. It contains no
GitHub client, state transition, filesystem, process, or model-provider logic.

```text
GitHub facts
  -> typed Checkpoint
  -> canonical JSON signing bytes
  -> signed CheckpointEnvelope

trusted Control
  -> typed TaskEnvelope
  -> credential-free Worker
  -> typed AgentResult with ChangeSet and Evidence
  -> trusted Publisher validation
```

Every signed or cross-job payload rejects unknown JSON fields, oversized
inputs, unbounded strings or arrays, invalid object identities, and executable
free-form content. Checkpoint slices are sorted before signing and are required
to remain sorted when verified. `AgentResult` is a proposal only; it cannot
carry shell scripts, Git credentials, commits, refs, PR mutations, or Issue
mutations.
