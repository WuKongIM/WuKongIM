---
status: accepted
---

# Exchange GitHub identity for run-scoped Analysis Tokens

The Analysis Workflow will authenticate to the run's Analysis MCP Gateway with GitHub OIDC and exchange that identity for a short-lived Analysis Token bound to the selected run. The gateway validates the repository and workflow identity before issuing only the diagnostic capabilities required by the Analysis MCP, avoiding a fixed MCP token in repository configuration or GitHub Secrets. The token expires within the bounded Analysis Run and cannot manage cloud infrastructure.
