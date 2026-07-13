---
status: accepted
---

# Run cloud workloads with existing wkbench phases

Cloud simulation will run a persistent wkbench worker and one non-restarting `wkbench run` systemd unit on the simulator, reusing the existing coordinator phases and typed outcomes for the selected `wkbench/v1` Scenario Profile. When the run command completes or fails, Prometheus, the Analysis MCP, and surviving nodes remain available through Analysis Grace, and the gateway derives phase state from worker status, coordinator output, and service status. The local compact, retrying `wkbench dev-sim` supervisor remains a Docker Compose development facility. No new cloud runtime daemon will duplicate workload phases, cloud lifecycle, or spot detection.
