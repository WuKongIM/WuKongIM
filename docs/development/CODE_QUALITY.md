# Code Quality Notes

- `TestSendStressThreeNode` can finish all sends and durable verification, then fail during `t.Cleanup` because `App.Stop()` uses one shared 5s stop context for every lifecycle component; under stress, late cluster RPC retries can consume the budget and surface as `context deadline exceeded`. Consider per-component shutdown budgets or clearer cleanup diagnostics.
