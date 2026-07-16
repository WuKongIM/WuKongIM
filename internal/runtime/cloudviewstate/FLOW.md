# Cloud View State Flow

`cloudviewstate` owns the simulator-local, monotonic state that distinguishes a
pure benchmark from a run affected by public interaction.

```text
Cloud View entry decision
  -> Recorder.MarkInteractive / MarkOperatorModified
  -> atomically replace the run-owned JSON state
  -> atomically replace the node_exporter textfile projection
  -> annotate the final wkbench report before service shutdown completes
```

State is restored only for the exact Run Identity. A failed persistence attempt
keeps the conservative transition in memory, exposes degraded persistence, and
retries in the background until every projection is durable. Final report
annotation reads this live state and fails closed if it is unavailable or still
degraded. The runtime does not interpret HTTP routes or Manager permissions.
