---
status: accepted
---

# Stop on unplanned spot interruption

The first version will not replace an instance reclaimed by the spot market. Any unplanned reclaim terminates workload generation and classifies the Simulation Run as `infrastructure_interrupted`, so results from different physical node sets cannot be blended. If a cluster node is reclaimed, the simulator and surviving nodes remain available until the Run Lease expires for live diagnosis; if the simulator is reclaimed, the run is no longer analyzable and the Cleanup Workflow may release its survivors early. The Analysis Skill must not treat spot reclaim itself as a WuKongIM defect or a reason to propose a code fix. Planned resilience testing will use an explicit fault-injection scenario instead.
