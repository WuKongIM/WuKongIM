# single_node_send AGENTS

This scenario proves `cmd/wukongim` can boot a single-node cluster and
complete one real WKProto `SEND -> SENDACK` closure, then expose the sender and
receiver conversation rows through the public `/conversation/list` HTTP API.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/message/single_node_send -count=1
```

## Rules

- Keep assertions black-box through the public WKProto gateway and HTTP API.
- Use `test/e2ev2/suite` for process startup, config rendering, readiness,
  WKProto, and HTTP API helpers.
- Validate `SENDACK` and conversation projection for this scenario. Delivery
  and `RECV` belong to later internalv2 e2e coverage.
