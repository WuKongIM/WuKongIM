---
status: accepted
---

# Deliver one cloud provider end to end before the next

The cloud-simulation lifecycle will use provider-neutral contracts for run creation, inventory, temporary network access, interruption state, and teardown, while the first complete implementation targets Alibaba Cloud. Tencent Cloud support will be added as a separate adapter only after the Alibaba Cloud path has passed an end-to-end Simulation Run and Analysis Run. This preserves a multi-cloud boundary without multiplying IAM, spot-capacity, networking, and cleanup failure modes before one full lifecycle is proven.
