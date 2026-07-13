---
status: accepted
---

# Simulate only trusted main revisions

The first version will start Provisioning Workflows only from the default branch and accept a source commit only when it belongs to trusted `main` history. The build job will not receive cloud credentials, while protected-environment approval gates the provisioning job that creates billable resources. Workflow definitions, the Analysis Skill, and Analysis MCP tooling will always come from trusted `main`, and remediation Draft PRs will target current `main`. Arbitrary branches and fork pull-request commits are excluded until a separately approved untrusted-source mode is designed.
