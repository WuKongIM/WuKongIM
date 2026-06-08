# wukongimv2 single-node send AGENTS

This scenario proves `cmd/wukongimv2` can boot a single-node cluster and
complete one real WKProto `SEND -> SENDACK` closure, then expose the sender and
receiver conversation rows through the public `/conversation/list` HTTP API.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/wukongimv2_single_node_send -count=1
```

## Rules

- Keep assertions black-box through the public WKProto gateway and HTTP API.
- Build `cmd/wukongimv2` inside the test; do not use the default e2e binary
  cache because it targets `cmd/wukongim`.
- Validate `SENDACK` and conversation projection for this scenario. Delivery
  and `RECV` belong to later internalv2 e2e coverage.
