# single_node_send AGENTS

This scenario proves `cmd/wukongim` can boot a single-node cluster, complete a
real WKProto `SEND -> SENDACK` closure, construct both person conversation
views from membership, and avoid repeat membership writes once
`directory_ready` is established.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/single_node_send -count=1
```

## Rules

- Keep assertions black-box through the public WKProto gateway and HTTP API.
- Use `test/e2e/suite` for process startup, config rendering, readiness,
  WKProto, and HTTP API helpers.
- Validate `SENDACK`, membership-backed conversation construction, and actual
  membership mutation metrics around later person SENDs. Delivery and `RECV`
  belong to delivery-specific scenarios.
