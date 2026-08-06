---
status: accepted
---

# Use separate deployment and Codex SSH identities

Each Chat Lifecycle Cloud Lease will authorize two independent ephemeral SSH identities: an encrypted GitHub-held identity for Deployment, monitoring, finalization, and evidence rescue, and a locally generated Ed25519 identity whose private key remains only with Codex for live diagnosis. Both expire with the Lease and are deleted only after zero inventory is proved. Separating them preserves auditability and prevents GitHub's deployment credential from becoming Codex's diagnostic secret while retaining the direct access the operator requested.
