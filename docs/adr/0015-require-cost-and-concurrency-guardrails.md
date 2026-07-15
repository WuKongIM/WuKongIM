---
status: accepted
---

# Require cost and concurrency guardrails

Provisioning will accept only the bounded durations `30m`, `2h`, `24h`, and `48h`, and by default will refuse to create a second active Simulation Run for the same repository and cloud account. Instance and disk choices must come from a repository-maintained allowlist, and every request must declare a maximum total cost. Before creating any resource, the Cloud Control Plane must obtain provider pricing for the complete lease and refuse atomically when pricing is unavailable, the estimate exceeds the declared budget, quota is insufficient, or spot capacity cannot satisfy the request. The workflow summary will expose the estimate, chosen resources, expiry, and unbounded traffic-cost caveats.
