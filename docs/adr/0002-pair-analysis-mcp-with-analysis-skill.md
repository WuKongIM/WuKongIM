---
status: accepted
---

# Pair the Analysis MCP with an Analysis Skill

Live diagnostic access and diagnostic reasoning have different security and evolution needs. WuKongIM will expose structured, run-scoped observability and bounded diagnostic operations through an Analysis MCP, while a separately maintained Analysis Skill tells Codex how to query those tools, cross-check evidence, classify root causes, and decide whether a code change is justified. Skills will not replace the MCP with direct SSH or arbitrary server commands.
