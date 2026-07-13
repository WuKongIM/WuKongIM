---
status: accepted
---

# Let diagnostic verdicts control workflow status

An Analysis Workflow succeeds only for a `healthy` Diagnosis Result. Confirmed `product_defect`, `infrastructure_interrupted`, `scenario_invalid`, `insufficient_evidence`, and provider-confirmed `released` outcomes all fail the workflow, even when Codex completed normally or a Draft PR was created. The job summary must present exactly one verdict with confidence, supporting observations, affected time range, and any remediation link. A released run terminates before Codex, while infrastructure, scenario, and evidence failures remain ineligible for production-code remediation.
