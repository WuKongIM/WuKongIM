---
status: accepted
---

# Decouple cloud simulation execution from GitHub workflows

Simulation Runs may last from hours to days, beyond the execution limit of a GitHub-hosted job, and their spot instances may be interrupted independently. The Provisioning Workflow therefore only creates and records a run with a cloud-side deadline and cleanup lease; the workload continues on the temporary infrastructure, while a separate manually triggered Analysis Run examines its evidence. This keeps long-running execution, cloud-resource lifetime, and diagnostic permissions independent.
