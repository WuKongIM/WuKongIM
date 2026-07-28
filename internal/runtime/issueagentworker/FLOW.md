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
  -> typed tool calls
  -> bounded command/file evidence
  -> workspace diff + provider usage -> AgentResult Artifact
  -> destroy workspace and model home
```

The model returns only a semantic proposal. It must leave `ChangeSet`, command
evidence, Artifact digest, diagnosis evidence digest, and token counts empty.
The trusted Worker derives those values from the workspace, broker transcript,
and Adapter response and validates the completed Artifact again. The Publisher
replays the same validations before any GitHub write.

On Linux CI the workspace is placed in a digest-pinned, no-network container
with a read-only root, PID/memory/CPU constraints, temporary build caches, and
a read-only pre-fetched Go module cache. The command workspace is a
size-capped per-job tmpfs Docker volume, not a host bind, and stdout/stderr are
capped while the process runs. The Supervisor has only one selected
provider credential and no GitHub write credential. The tool container has
target code but no model, GitHub, Docker-socket, or host credentials.
