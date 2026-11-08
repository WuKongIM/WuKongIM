# dynamic_node_faults AGENTS

This package contains opt-in gofail-backed internal dynamic-node fault tests.

## Scenario Contract

- Tests must use a gofail-enabled `cmd/wukongim` binary supplied through `WK_E2E_BINARY`.
- Tests must be skipped unless `WK_E2E_GOFAIL_DYNAMIC_NODE=1`.
- Each node gets its own loopback `GOFAIL_HTTP` endpoint through `suite.WithNodeEnv`.
- Faults are controlled through the gofail HTTP endpoint and disabled before test cleanup when possible.
- Tests remain black-box: use manager HTTP, WKProto, and process handles only.
- Stage 8 scale-in coverage must prove scale-in Slot drain remains fail-closed while Slot replica movement is delayed and recovers after the delay clears.
- Stage 8 scale-in coverage must prove Channel drain inventory and manager runtime-summary faults keep `safe_to_remove=false` and final remove bounded-conflict.
- Stage 8 scale-in coverage must prove final remove is idempotent when the `removed` commit succeeds but the response is lost.
- Stage 8 scale-in coverage must prove a leaving node restart during scale-in Slot drain keeps the durable task recoverable and reaches `removed` after drain.
- Stage 10A coverage must prove health report faults make active nodes fail closed for new placement and recover after reports resume.
- Stage 10A coverage must prove a dropped Controller state event does not permanently hide a lifecycle transition after later health/report wakeups.
- Stage 10A coverage must prove concurrent scale-in advancement under delayed Slot drain produces at most one task and bounded conflicts, not 500s.
- Runtime summary faults use `wkClusterNetCallShardFault` with the `manager_connections` alias. The alias is plural because it comes from the manager connection service alias.
- Restart-during-scale-in proves durable Slot task recovery. A restarted leaving node is admitted by its seed join loop and does not wait for cluster write readiness at startup, because its own Slot may still be moving (issue #1097). Gateway drain admission is runtime state, so the scenario may re-apply drain after restart before final remove.

## Running

Nightly `ci.yml` runs this package through `go run ./cmd/wkcicheck gofail-e2e`, which builds one binary with every failpoint package. Locally:

```bash
scripts/build-gofail-binary.sh --cmd ./cmd/wukongim --package internal/usecase/management --package pkg/controller --package pkg/cluster/tasks --package pkg/cluster/net --out /tmp/wukongim-gofail
WK_E2E_BINARY=/tmp/wukongim-gofail WK_E2E_GOFAIL_DYNAMIC_NODE=1 GOWORK=off go test -tags=e2e ./test/e2e/cluster/dynamic_node_faults -count=1 -timeout 15m -p=1
```
