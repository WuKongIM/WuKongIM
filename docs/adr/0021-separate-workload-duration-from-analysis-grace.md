---
status: accepted
---

# Separate workload duration from analysis grace

The selectable `30m`, `2h`, `24h`, and `48h` durations describe active workload time after readiness and preparation, not total infrastructure lifetime; `2h` remains the default while `30m` supports a cheaper end-to-end acceptance run. A separate Analysis Grace of `30m`, `1h`, or `6h`, defaulting to `30m`, keeps the stopped workload's live metrics, logs, MCP, and cluster available for manual diagnosis. The immutable Run Lease includes a bounded bootstrap allowance, workload duration, and grace, and its full interval participates in cost estimation. An Analysis Run must refuse to start when the remaining lease is below its minimum diagnostic budget.
