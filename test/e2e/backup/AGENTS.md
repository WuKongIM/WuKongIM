# backup E2E AGENTS

This domain proves cluster-semantic backup and fresh-cluster restore through
real `cmd/wukongim` processes and public Manager, HTTP, and WKProto entrypoints.

## Rules

- Keep repository and key-provider substitutes outside the product data path;
  they are selected only by the e2e-tagged binary.
- Treat repository files as external black-box artifacts. Scenario assertions
  use Manager or client APIs and never decode manifests or query node databases.
- Local integrity scenarios may activate e2e-only provider read corruption for
  opaque segment payloads, but must not select or decode manifest contents and
  must assert recovery through Manager state and public metrics.
- Stop the source cluster before restore activation and use a distinct target
  cluster ID and generation.
- Production storage qualification must use the normal Alibaba OSS/RAM loaders
  and protected deployment-key credential. It must reject the e2e file-root
  override and emit only bounded, non-secret evidence.
- Keep every scenario in its own directory with a local `AGENTS.md`.
