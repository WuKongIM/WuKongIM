# terminal_disband AGENTS

This scenario proves a terminal channel disband is enforced by the message
usecase's source-channel permission gate, including sender classes that bypass
ordinary nonterminal permission checks.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/terminal_disband -count=1 -timeout 2m
```

## Rules

- Keep assertions black-box through public channel, user, message-send,
  conversation, and message-sync HTTP APIs.
- Cover an ordinary subscriber, a system UID, and the configured system device.
- At least one bypass send must use `sync_once` so the command-channel append
  path proves it resolves the terminal state of the source channel.
- A disbanded channel must remain discoverable as a conversation delete and
  must reject ordinary and CMD message pulls.
- Snapshot ordinary and CMD membership-mutation counters immediately before
  disband and prove the terminal mutation does not synchronously fan out rows.
