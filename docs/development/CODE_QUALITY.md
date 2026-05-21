# Code Quality Notes

- `TestSendStressThreeNode` can finish all sends and durable verification, then fail during `t.Cleanup` because `App.Stop()` uses one shared 5s stop context for every lifecycle component; under stress, late cluster RPC retries can consume the budget and surface as `context deadline exceeded`. Consider per-component shutdown budgets or clearer cleanup diagnostics.
- `pkg/channel/runtime.TestSessionLongPollRPCTimeoutUsesRecoveryBackoff` failed once during a broad package run with `long poll timeout should not immediately retry, got 2 sends`, then passed with `-run ... -count=5` and a fresh package run. The test appears timing-sensitive and should use deterministic scheduler hooks instead of wall-clock assumptions.
- `internal/app/deliveryrouting.go:isSenderDeliveryRoute` contains a duplicated return statement. It is harmless but should be cleaned up in a separate readability pass.
