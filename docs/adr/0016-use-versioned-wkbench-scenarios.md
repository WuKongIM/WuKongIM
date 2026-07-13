---
status: accepted
---

# Use versioned wkbench scenarios

The existing `wkbench/v1` scenario schema will be the sole workload contract for cloud simulation. Repository-owned scenario files will define users, connections, channel shapes, traffic, verification, limits, and deterministic seeds; the Provisioning Workflow will select one scenario and allow only bounded overrides for duration, infrastructure preset, and cost. Before infrastructure activation, the exact effective scenario must pass wkbench validation and doctor checks, and its digest, run identity, seed, source commit, and the cluster's default 256 hash slots must remain queryable through the Analysis MCP. Specialized workloads will be reviewed as new scenario files instead of assembled from unrestricted workflow inputs.
