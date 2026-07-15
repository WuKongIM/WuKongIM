---
status: superseded
superseded_by: 0038-allow-accesskey-onboarding-with-oidc-fallback
---

# Bootstrap cloud identity once from CloudShell

Cloud account onboarding will run once from the provider's browser-authenticated CloudShell because GitHub cannot create its own trust relationship before one exists. An idempotent, reviewable bootstrap will plan, apply, and remove only the GitHub OIDC provider, narrowly conditioned role trust policies, and least-privilege Provisioner and Analyzer policies; it will not create billable Simulation Run infrastructure or export a long-lived credential. It will output only non-secret account, role, provider, and region identifiers for GitHub Variables. Removal must refuse while tagged runs remain active. Equivalent manual console setup remains supported, while ordinary workflows use only short-lived federated credentials after onboarding.
