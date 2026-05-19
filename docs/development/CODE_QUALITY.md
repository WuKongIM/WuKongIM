# Code Quality Notes

- `TestSendStressThreeNode` can finish all sends and durable verification, then fail during `t.Cleanup` because `App.Stop()` uses one shared 5s stop context for every lifecycle component; under stress, late cluster RPC retries can consume the budget and surface as `context deadline exceeded`. Consider per-component shutdown budgets or clearer cleanup diagnostics.
- `go test ./...` currently exposes unrelated non-gateway failures: `internal/bench/worker.TestWorkerDefaultRunnerCancelsOtherWorkloadsOnPhaseError` can hang until the 10m test timeout, and `scripts.TestDockerComposeDevSimDefaultsTargetHighTraffic` expects a missing dev-sim default `WK_SIM_USERS: ${WK_SIM_USERS:-500}`.
