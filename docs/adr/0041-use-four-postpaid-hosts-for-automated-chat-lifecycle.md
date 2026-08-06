---
status: accepted
---

# Use four PostPaid hosts for automated chat lifecycle

The automated chat-lifecycle flow will use four same-type Alibaba PostPaid hosts in one cn-hangzhou zone: three 4-vCPU, 8-GiB WuKongIM nodes and one 4-vCPU, 8-GiB host carrying three workers, coordination, monitoring, analysis, and public proxy duties. This scoped topology replaces the earlier seven-host formal layout and Spot assumption to bound operational complexity and interruption risk; saturation is measured and reported instead of triggering automatic resize. The rehearsal and formal Cloud Leases have immutable six-hour and 96-hour expiries, while workload clocks begin only after readiness.
