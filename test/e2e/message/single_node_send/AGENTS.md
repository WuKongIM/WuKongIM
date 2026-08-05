# single_node_send AGENTS

This scenario proves `cmd/wukongim` can boot a single-node cluster and
complete one real WKProto `SEND -> SENDACK` closure, then construct sender and
receiver conversation views from membership through `/conversation/list`.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/single_node_send -count=1
```

## Rules

- Keep assertions black-box through the public WKProto gateway and HTTP API.
- Use `test/e2e/suite` for process startup, config rendering, readiness,
  WKProto, and HTTP API helpers.
- Validate `SENDACK` and membership-backed conversation construction. Delivery
  and `RECV` belong to later internal e2e coverage.
