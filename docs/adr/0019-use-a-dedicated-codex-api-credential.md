---
status: superseded
superseded_by: 0037-run-codex-locally-with-an-encrypted-analysis-session
---

# Use a dedicated Codex API credential

Automated diagnosis and remediation require a provider API credential because the current official Codex GitHub Action cannot reuse the repository's interactive Codex integration. A project-specific API key with an independent usage limit will be stored in a protected GitHub Environment and exposed only to the isolated Codex steps in the diagnosis and remediation jobs, using the Action's privilege-reduction strategy. The key will never reach cloud hosts, the Analysis MCP, provisioning code, or unrelated workflow steps. Provisioning may operate without this credential, while Analysis Runs must fail preflight clearly when it is absent.
