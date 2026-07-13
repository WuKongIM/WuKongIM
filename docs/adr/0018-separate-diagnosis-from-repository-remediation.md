---
status: accepted
---

# Separate diagnosis from repository remediation

The Analysis Workflow will isolate live diagnosis from repository mutation in separate jobs. The diagnosis job receives the Analyzer Role and Analysis MCP access but only read permission to repository contents; it produces a structured Diagnosis Result and then closes the Analysis Access Window. An optional remediation job receives no cloud identity or MCP access and runs only when the caller explicitly enables fixes and the diagnosis classifies a repository defect with sufficient evidence. It may create a tested Draft PR, but must not propose changes for infrastructure interruption, cloud capacity, scenario errors, or inconclusive evidence, and it may never merge automatically.
