---
status: accepted
---

# Host one Analysis MCP Gateway per Simulation Run

Each Simulation Run will host one Analysis MCP Gateway on its simulator node. The gateway aggregates simulator status and the existing private diagnostic surfaces of all three WuKongIM nodes, giving Codex one run-scoped tool endpoint while keeping MCP ports and protocol logic off the cluster nodes. Losing the simulator node makes the workload invalid as well as the gateway unavailable, so the run is failed rather than introducing a second MCP control-plane host.
