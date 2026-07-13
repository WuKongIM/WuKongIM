---
status: accepted
---

# Start workload only after cluster readiness

Active workload time begins only after a 20-minute bootstrap gate verifies one Deployment Bundle digest on all four hosts, active services and readiness on every WuKongIM node, exactly three converged cluster members, all 256 slots and replicas healthy, no pending controller convergence, complete Prometheus target coverage, a healthy Analysis MCP, and successful wkbench validation and doctor checks. Failure prevents workload generation and fails provisioning. When the simulator and MCP remain useful, only the selected Analysis Grace is retained for diagnosis before early release; otherwise the Cloud Control Plane cleans the run immediately. The provisioning summary exposes the failed gate, run identity, remaining diagnostic time, and cleanup state.
