# dynamic_node_faults AGENTS

This package contains opt-in gofail-backed internalv2 dynamic-node fault tests.

## Scenario Contract

- Tests must use a gofail-enabled `cmd/wukongimv2` binary supplied through `WK_E2EV2_BINARY`.
- Tests must be skipped unless `WK_E2EV2_GOFAIL_DYNAMIC_NODE=1`.
- Each node gets its own loopback `GOFAIL_HTTP` endpoint through `suite.WithNodeEnv`.
- Faults are controlled through the gofail HTTP endpoint and disabled before test cleanup when possible.
- Tests remain black-box: use manager HTTP, WKProto, and process handles only.

## Running

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongimv2 --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongimv2-gofail
WK_E2EV2_BINARY=/tmp/wukongimv2-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 10m -p=1
```
