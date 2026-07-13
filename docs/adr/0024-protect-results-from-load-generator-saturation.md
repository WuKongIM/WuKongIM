---
status: accepted
---

# Protect results from load-generator saturation

Cloud simulation will offer provider-neutral `small`, `standard`, and `stress` infrastructure presets defined by minimum compute, memory, network, and independent-disk capabilities. A Scenario Profile may require a minimum preset, while a Cloud Provider Adapter chooses the lowest-cost allowlisted spot type that satisfies it and records the concrete selection. High-online scenarios may assign multiple private source addresses to the simulator's existing wkbench worker and TCP source pools. Sustained simulator CPU above 70 percent, memory above 80 percent, or source-port, queue, or sender saturation invalidates attribution as `insufficient_evidence`; high utilization on a WuKongIM node remains a valid subject of diagnosis.
