# Issue Agent Worker Runtime Flow

`internal/runtime/issueagentworker` owns the credential-free, provider-neutral
Worker runtime. It validates one immutable `TaskEnvelope`, exposes a closed
typed tool catalog, confines repository paths, runs approved argv commands
without a shell or inherited environment, records bounded redacted evidence,
and derives the final `ChangeSet` from the workspace.

```text
TaskEnvelope + clean source snapshot
  -> temporary writable workspace
  -> no-secret Tool Broker
  -> selected model Adapter
  -> typed tool calls (monotonic IDs)
  -> bounded command/file evidence
  -> workspace diff -> AgentResult Artifact
  -> destroy workspace and model home
```

On Linux CI the workspace is placed in a no-network container with CPU,
memory, PID, disk, and wall-time limits. The Supervisor has model credentials
but no target-code execution capability; the tool container has target code
but no model, GitHub, Docker, or host credentials.
