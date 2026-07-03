# cmd_sync AGENTS

This scenario proves `cmd/wukongim` keeps ordinary conversations and CMD
sync state isolated inside the unified conversation projection in a single-node
cluster.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2ev2/message/cmd_sync -count=1 -timeout 2m
```

## Rules

- Keep assertions black-box through public WKProto, channel-management,
  `/message/send`, `/message/sync`, `/message/syncack`, and
  `/conversation/list` APIs.
- Use WKProto for the ordinary SEND path and public HTTP for `sync_once`
  command messages.
- Keep scenario-local helpers here unless another e2ev2 message scenario
  starts sharing the same helper shape.
