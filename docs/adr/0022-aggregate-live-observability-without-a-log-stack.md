---
status: accepted
---

# Aggregate live observability without a log stack

The simulator will host Prometheus for WuKongIM and host metrics, while structured application logs remain bounded and rotated on their originating nodes. The Analysis MCP will aggregate Prometheus queries with the existing private manager log, diagnostics, task-audit, workqueue, and profiling surfaces; it will query simulator services from local journald, and the Provisioning Workflow will retain bootstrap failures in its own summary. The first version will not deploy Grafana, Loki, ELK, or cross-run storage. Metrics retention, log quotas, and disk preflight will be sized to the workload duration plus Analysis Grace.
