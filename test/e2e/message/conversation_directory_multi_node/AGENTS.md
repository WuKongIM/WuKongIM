# conversation_directory_multi_node AGENTS

This scenario proves membership-backed conversation hydration keeps cluster
semantics in a real static three-node cluster.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_directory_multi_node -count=1 -timeout 3m -p=1
```

## Rules

- Keep assertions black-box through public channel-management,
  `/message/send`, `/conversation/list`, `/conversation/retry`, Manager HTTP,
  and `/metrics` entrypoints.
- Enable Manager HTTP on all nodes and wait for stable actual Slot leaders
  before selecting channels.
- Select channels by their publicly reported Channel Leader; do not inspect
  internal stores or import `internal` packages.
- Prove batching with low-cardinality public metrics and prove partial failure
  through `unresolved` results without timing-based latency assertions.
