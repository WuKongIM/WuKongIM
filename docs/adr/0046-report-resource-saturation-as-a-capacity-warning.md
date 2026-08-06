---
status: accepted
---

# Report resource saturation as a capacity warning

The automated chat-lifecycle flow starts every host at 4 vCPU and 8 GiB and treats sustained declared CPU, memory, queue, or load-generation saturation as infrastructure_capacity evidence rather than immediately calling it a product defect or silently resizing hardware. A functionally correct run may finish as passed_with_capacity_warning; latency with clear hardware headroom remains a product failure, and ambiguous attribution remains insufficient_evidence. Correctness errors, crashes, cluster unavailability, unsafe disk, budget, and expiry conditions are still fatal.
