---
status: accepted
supersedes:
  - 0008-use-github-oidc-with-separate-cloud-roles
  - 0035-bootstrap-cloud-identity-once-from-cloudshell
---

# Allow AccessKey onboarding with OIDC fallback

Cloud Simulation supports two Alibaba authentication modes behind the same
provider boundary. When both `ALIBABA_CLOUD_ACCESS_KEY_ID` and
`ALIBABA_CLOUD_ACCESS_KEY_SECRET` exist as GitHub Repository Secrets, cloud
jobs use that pair directly. A partial pair fails before any cloud API call.
When neither secret exists, the established GitHub OIDC roles and non-secret
role variables remain a compatible, hardened fallback.

AccessKey mode is the simplest onboarding path and requires no CloudShell or
OIDC bootstrap. Provision discovers the current account binding, audited
Alibaba Cloud Linux image, ESSD-capable zone, and bounded live spot candidates
before creating billable resources. It persists only that non-secret provider
configuration in a Run-Identity-scoped GitHub Artifact. Analysis and Cleanup
reuse the exact retained configuration; the scheduled sweeper deduplicates all
unexpired Artifact metadata by account and region before downloading one
configuration per binding and reconciling tagged resources.

The AccessKey pair is never written to source, artifacts, summaries, cloud
hosts, or logs. It remains available only to the protected GitHub cloud jobs
and must not be removed until cleanup proves zero residual resources. A
dedicated least-privilege RAM user is preferred; an Alibaba root-account
AccessKey is explicitly discouraged. OIDC remains the preferred long-term
hardening path because it replaces the stored credential with short-lived,
workflow-conditioned STS credentials.
