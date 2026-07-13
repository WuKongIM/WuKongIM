---
status: accepted
---

# Run cloud services as native systemd units

Cloud hosts will run the immutable WuKongIM, wkbench, Analysis MCP, Prometheus, and host-observation binaries as native systemd services. The deployment bundle will include their generated configuration and unit definitions, with three independent WuKongIM data directories and journald integration. Docker Compose remains the local development and topology reference, and contract tests will keep its relevant defaults aligned with cloud scenario generation. Avoiding a container runtime reduces bootstrap dependencies, transfer size, disk pressure, and unrelated runtime variables in performance evidence.
