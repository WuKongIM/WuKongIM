# Operations MCP E2E AGENTS

This domain owns process-level black-box coverage for the embedded Operations
MCP and its Manager administration surface.

## Rules

- Start real `cmd/wukongim` processes and use only public Manager HTTP and MCP
  endpoints.
- Enable Manager authentication explicitly on every node.
- Never import `internal/app`, `internal/usecase`, or runtime implementation
  packages from scenario tests.
- Prove cross-node behavior through distinct Manager ingress, owner, and target
  nodes rather than replacing them with stubs.
