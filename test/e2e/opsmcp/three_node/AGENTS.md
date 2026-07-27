# Three-node Operations MCP AGENTS

This scenario proves one Controller-backed MCP configuration works through
every Manager ingress in a real three-node cluster.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/opsmcp/three_node -count=1 -timeout 3m -p=1
```

## Rules

- Use node 1 as owner, node 2 as the pprof request ingress, and node 3 as the
  pprof target.
- Use the same opaque MCP token through node 2 and node 3.
- Stop the owner long enough to prove requests fail closed, then restart it and
  prove durable desired state resumes.
- Keep the profile kind non-blocking (`goroutine`) in this acceptance test.
