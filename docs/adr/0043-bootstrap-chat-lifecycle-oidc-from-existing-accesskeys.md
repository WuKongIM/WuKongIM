---
status: accepted
---

# Bootstrap chat-lifecycle OIDC from existing AccessKeys

The first automated chat-lifecycle start may use the repository's complete Alibaba AccessKey Secret pair once to create and verify workflow-conditioned CloudLeaseProvisioner, CloudLeaseObserver, and CloudLeaseReleaser OIDC roles. Ordinary runs then use those short-lived OIDC identities, while the existing Secrets remain untouched for compatibility with other workflows. This narrows long-lived credential exposure without making the operator perform a separate CloudShell ceremony and is scoped to the new Cloud Lease flow.
