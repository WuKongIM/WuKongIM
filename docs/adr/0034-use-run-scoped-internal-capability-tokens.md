---
status: accepted
---

# Use run-scoped internal capability tokens

Each Simulation Run will generate independent 256-bit tokens for benchmark preparation, local worker control, and private diagnostic access instead of reusing a manager administrator identity or unauthenticated private endpoints. Tokens will authorize only their named API surfaces, be restricted again by Run Network source rules, and reside solely in root-readable systemd environment files on the hosts that need them. They will never enter GitHub artifacts, cloud tags, summaries, Diagnosis Results, or Codex context, and the Analysis MCP will translate its own tool contract to these internal capabilities without exposing them. Instance destruction ends their lifecycle.
