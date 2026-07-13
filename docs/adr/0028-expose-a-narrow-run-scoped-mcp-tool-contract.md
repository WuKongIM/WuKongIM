---
status: accepted
---

# Expose a narrow run-scoped MCP tool contract

The Analysis MCP will expose only run inspection, cluster snapshots, bounded metrics ranges, bounded log search and context, diagnostics and task-audit queries, expiring trace capture, budgeted symbolic profile capture and inspection, and redacted effective configuration. Every result will identify its run, node, observation time, data window, completeness, and warnings. Metrics queries will enforce range, step, series, sample, and timeout limits; profile tools will return server-side symbolic views rather than arbitrary files. Tool inputs cannot name arbitrary URLs, files, commands, or processes, and the server will not provide bulk collection or generic remote-operation tools.
