# backup E2E AGENTS

This domain proves cluster-semantic backup and fresh-cluster restore through
real `cmd/wukongim` processes and public Manager, HTTP, and WKProto entrypoints.

## Rules

- Keep repository and key-provider substitutes outside the product data path;
  they are selected only by the e2e-tagged binary.
- Treat repository files as external black-box artifacts. Scenario assertions
  use Manager or client APIs and never decode manifests or query node databases.
- Stop the source cluster before restore activation and use a distinct target
  cluster ID and generation.
- Keep every scenario in its own directory with a local `AGENTS.md`.
