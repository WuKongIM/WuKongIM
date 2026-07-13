---
status: accepted
---

# Isolate each run in a private network

Each Simulation Run will create a tagged, run-specific VPC, subnet, and security group in one region and availability zone. The three WuKongIM nodes will have private addresses only, and the simulator will reach their cluster, management, metrics, and profiling endpoints over the private network. Only the simulator receives a temporary public address, with no standing inbound access; its MCP HTTPS port is admitted solely during an Analysis Access Window. The first version deliberately avoids multi-zone placement so cross-zone latency and failure behavior can be introduced later as an explicit scenario.
