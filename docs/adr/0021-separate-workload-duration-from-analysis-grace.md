---
status: accepted
---

# Separate workload duration from analysis grace

The selectable `2h`, `24h`, and `48h` durations describe active workload time after readiness and preparation, not total infrastructure lifetime. A separate Analysis Grace of `0h`, `1h`, or `6h`, defaulting to `1h`, keeps the stopped workload's live metrics, logs, MCP, and cluster available for manual diagnosis. The immutable Run Lease includes a bounded bootstrap allowance, workload duration, and grace, and its full interval participates in cost estimation. An Analysis Run must refuse to start when the remaining lease is below its minimum diagnostic budget; choosing zero grace explicitly accepts analysis only during active load.
