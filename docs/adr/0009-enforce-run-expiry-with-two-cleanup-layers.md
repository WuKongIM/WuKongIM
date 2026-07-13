---
status: accepted
---

# Enforce run expiry with two cleanup layers

Every Simulation Run will receive an immutable expiry when it is created. Each compute instance will be configured with the provider's native scheduled-release mechanism, while an independent scheduled Cleanup Workflow periodically discovers expired runs by tags and removes residual compute, disks, addresses, and run-specific network rules. Simulation hosts will not receive cloud credentials or be responsible for deleting themselves. Cleanup reports must identify residual billable resources and fail visibly when the provider cannot prove their removal.
