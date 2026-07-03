# dynamic_node_faults AGENTS

This package contains opt-in gofail-backed internalv2 dynamic-node fault tests.

## Scenario Contract

- Tests must use a gofail-enabled `cmd/wukongim` binary supplied through `WK_E2E_BINARY`.
- Tests must be skipped unless `WK_E2EV2_GOFAIL_DYNAMIC_NODE=1`.
- Each node gets its own loopback `GOFAIL_HTTP` endpoint through `suite.WithNodeEnv`.
- Faults are controlled through the gofail HTTP endpoint and disabled before test cleanup when possible.
- Tests remain black-box: use manager HTTP, WKProto, and process handles only.
- Stage 8 scale-in coverage must prove scale-in Slot drain remains fail-closed while Slot replica movement is delayed and recovers after the delay clears.
- Stage 8 scale-in coverage must prove Channel drain inventory and manager runtime-summary faults keep `safe_to_remove=false` and final remove bounded-conflict.
- Stage 8 scale-in coverage must prove final remove is idempotent when the `removed` commit succeeds but the response is lost.
- Stage 8 scale-in coverage must prove a leaving node restart during scale-in Slot drain keeps the durable task recoverable and reaches `removed` after drain.
- Stage 10A coverage must prove health report faults make active nodes fail closed for new placement and recover after reports resume.
- Stage 10A coverage must prove a dropped ControllerV2 state event does not permanently hide a lifecycle transition after later health/report wakeups.
- Stage 10A coverage must prove concurrent scale-in advancement under delayed Slot drain produces at most one task and bounded conflicts, not 500s.
- Runtime summary faults use `wkClusterNetCallShardFault` with the `manager_connections` alias. The alias is plural because it comes from the manager connection service alias.
- Restart-during-scale-in proves durable Slot task recovery. Gateway drain admission is runtime state, so the scenario may re-apply drain after restart before final remove.

## Running

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongim --package internalv2/usecase/management --package pkg/controller --package pkg/clusterv2/tasks --package pkg/clusterv2/net --out /tmp/wukongim-gofail
WK_E2E_BINARY=/tmp/wukongim-gofail WK_E2EV2_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2ev2/cluster/dynamic_node_faults -count=1 -timeout 15m -p=1
```
