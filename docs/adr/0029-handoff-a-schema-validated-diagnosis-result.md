---
status: accepted
---

# Handoff a schema-validated Diagnosis Result

The diagnosis job will emit a JSON-Schema-validated Diagnosis Result containing exact run and source identity, analyzed window, one verdict, severity, confidence, root-cause scope, compact Observation references, supporting and contradictory signals, unresolved facts, remediation eligibility, proposed regression coverage, and cloud-revalidation requirements. It will not copy raw logs or metric series. The result may be retained for one day only as an intra-workflow handoff to remediation and rendered separately as a human-readable summary. Invalid results become `insufficient_evidence`; remediation requires high-confidence repository attribution and a regression-test path, and must stop when code inspection contradicts the diagnosis. Performance patches that still require cloud A/B remain Draft and explicitly unverified.
