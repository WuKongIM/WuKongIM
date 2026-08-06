---
status: accepted
---

# Extract a reusable Cloud Lease boundary

Temporary cloud procurement, inventory, access grants, expiry, release, and sweeping will live behind a provider-neutral Cloud Lease contract rather than inside a WuKongIM workload or Simulation Run state machine. The first adapter remains Alibaba Cloud, but Lease Plans and Lease Receipts contain only generic infrastructure concepts so other repository consumers can reuse the capability without inheriting chat-lifecycle semantics or workflow-local state.
