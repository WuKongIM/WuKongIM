---
status: accepted
---

# Open MCP ingress only for an Analysis Access Window

The Analysis MCP Gateway will not remain publicly reachable. For an Analysis Run, the workflow temporarily admits only its current GitHub runner egress address to the gateway's HTTPS port, verifies the run-specific TLS identity, and closes the rule in unconditional cleanup; the cloud-side run lease provides a second cleanup path. This avoids permanent ingress and extra tunnel infrastructure while retaining the GitHub OIDC and run-scoped Analysis Token checks above the network restriction.
