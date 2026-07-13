---
status: accepted
---

# Bound active diagnostics in the Analysis MCP

The Analysis MCP will combine read-only access to run identity, topology, effective scenario, redacted configuration, metrics, service logs, manager diagnostics, task audits, and workqueue state with a small set of bounded active diagnostics. It may capture CPU profiles for at most 30 seconds each and 60 seconds total per Analysis Run, take heap and goroutine snapshots, and install expiring message or send-trace rules, one node at a time. Every active diagnostic records its target and time window so later reasoning can account for perturbation. Tool-level limits must bound time ranges, log lines, series, and response size. Shell execution, service restart, configuration or log-level changes, cloud operations, and data deletion are excluded.
