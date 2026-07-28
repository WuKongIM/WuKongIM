# Generated Issue Agent E2E Scenario

This directory is an Agent-generated scenario. Follow `test/e2e/AGENTS.md`
without exception.

- Test only public, process-level behavior through real WuKongIM binaries.
- Use `WK_E2E_BINARY`; never import product internals or read private stores.
- Describe deployment topology as a single-node cluster or multi-node cluster.
- Keep the frozen business assertion explicit and deterministic.
- Keep logs bounded and put reusable harness behavior in `test/e2e/suite`.
- Do not weaken, skip, replace, or delete the frozen assertion during a fix.
