---
status: accepted
---

# Analyze live Simulation Runs only

An Analysis Run will operate only while the temporary compute resources of its Simulation Run still exist; the system will not add a historical diagnostics backend for released runs. Before starting Codex, the Analysis Workflow must verify the run through cloud-provider resource inventory keyed by run identity. If the provider proves that the resources were released, the workflow writes the run identity and release evidence to its job summary, terminates before starting Codex, and reports a failed workflow result so the missing analysis cannot be mistaken for success. An unreachable MCP alone is treated as a distinct preflight failure rather than proof of destruction.
