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
- `dynamic_node_readiness`: Stage 9 dynamic-node production-readiness smoke
  with health freshness, manager/metrics evidence, and join/onboard/scale-in/
  remove while real WKProto traffic continues.
- `dynamic_node_faults`: opt-in gofail-backed dynamic-node join, onboarding,
  scale-in, and remove fault recovery through public manager and WKProto
  entrypoints.

  ```bash
  scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package internalv2/usecase/management --package pkg/controllerv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
  WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 15m -p=1
  ```
