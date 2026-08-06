---
status: accepted
---

# Expose the load node for the bounded Cloud Lease

For automated chat-lifecycle runs only, the load node will keep key-only SSH on port 22 and plain HTTP for Manager and Demo on port 80 open to 0.0.0.0/0 for the bounded Cloud Lease. The three service nodes remain private and offline, passwords and root password login remain disabled, and all rules and authorized keys are removed with the Lease. This deliberately trades stronger ingress restriction for direct operator and Codex access on temporary test servers and is a scoped exception to the general no-standing-ingress and Deployment Access Window decisions.
