---
status: accepted
---

# Transfer builds through a Deployment Access Window

The Provisioning Workflow will build one immutable deployment bundle, generate an ephemeral SSH key, and temporarily admit only its GitHub runner address to the simulator's SSH port. It will transfer the bundle through the simulator to the three private cluster nodes, verify the same content digest on all four hosts, and then remove the temporary authorized key and ingress rule in unconditional cleanup. This avoids cloud object storage, a registry, and permanent outbound infrastructure. SSH remains a provisioning transport only and is never exposed to Codex or the Analysis MCP.
