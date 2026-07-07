# message_event_stream Scenario

This scenario proves message event stream buffering, follower forwarding, and
Slot-leader-change fail-closed behavior through public `cmd/wukongim` HTTP APIs.

## Rules

- Start real single-node and static three-node clusters through `test/e2e/suite`.
- Use only public HTTP APIs and public `/metrics` samples.
- Do not import `internal/*` packages or inspect local storage directly.
- Before `stream.finish`, cache-only stream events must not advance the Slot FSM
  cursor; after a Slot leader change, missing cache must fail closed rather than
  complete a stream with dropped lanes.
- `/message/eventsync` is intentionally out of scope for this scenario.

## Run

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/message_event_stream -count=1 -timeout 2m
```
