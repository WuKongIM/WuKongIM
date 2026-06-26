# test/e2ev2/cluster AGENTS

This domain owns black-box multi-node cluster lifecycle coverage for
`cmd/wukongimv2`.

## Rules

- Keep scenarios process-level and black-box: use real `cmd/wukongimv2`
  processes, public manager HTTP endpoints, and public readiness probes.
- Do not import `internalv2/app`, `internalv2/usecase`, or storage internals.
- Put reusable harness behavior in `test/e2ev2/suite`; scenario tests should
  describe lifecycle assertions only.
- Dynamic join coverage must prove the joining node calls seed join itself; do
  not shortcut by calling manager `JoinNode` directly from the test.

## Scenario Catalog

- `dynamic_node_join`: dynamic data-node seed join, activation, delivery,
  onboarding, scale-in drain, safety gates, negative join/activation paths, and
  concurrent task guards.
