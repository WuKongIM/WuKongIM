---
status: superseded
superseded_by: 0037-run-codex-locally-with-an-encrypted-analysis-session
---

# Discover analysis guidance from a repository skill

WuKongIM will maintain its cloud-diagnostic method as a repository-scoped `.agents/skills/wukongim-cloud-analysis` skill that the diagnosis job invokes explicitly. Before Codex starts, that job will generate a run-specific project MCP configuration with a required Streamable HTTP server, an explicit tool allowlist, and an Analysis Token referenced only through an environment variable. The skill will define evidence ordering, cross-checks, active-diagnostic triggers, attribution, prompt-injection handling, verdicts, and remediation eligibility. The remediation job starts from a fresh checkout without the MCP configuration, token, or live tools.
