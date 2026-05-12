# Request-Scoped Subscriber E2E Design

## Goal

Add a black-box e2e scenario that proves `/message/send` request-scoped `subscribers` works across a real three-node cluster through public HTTP API and WKProto gateway connections.

## Scope

The scenario covers the P2 request-scoped subscriber send path after the core implementation:

- durable `sync_once=1` request-scoped sends
- non-durable `sync_once=1 + no_persist=1` request-scoped sends
- exact subscriber snapshot delivery across nodes
- duplicate subscriber normalization observable as one delivery per online subscriber
- non-subscriber exclusion
- client-facing temp command channel view without leaking the internal `____cmd` suffix

Out of scope:

- mixed-version binary compatibility with an older executable
- direct store assertions or internal app imports
- large subscriber stress

## Architecture

Create a new e2e package under `test/e2e/message/request_scoped_subscriber_delivery`. The test starts a real three-node cluster using `test/e2e/suite`, connects WKProto clients to different nodes, sends request-scoped messages through `POST /message/send`, and validates only external packets and HTTP responses.

The test keeps helpers local to the scenario because the API shape is specific to request-scoped sends. Reusable helpers can be promoted later if another scenario needs them.

## Data Flow

1. Start a three-node cluster and wait for readiness.
2. Connect `subscriber-a`, `subscriber-b`, and `outsider` to different nodes.
3. Send a durable request-scoped command message through node 1 API:
   - no `channel_id`
   - `subscribers=[subscriber-a, subscriber-b, subscriber-a]`
   - `header.sync_once=1`
4. Verify subscriber A and B each receive one `Recv` with the payload and non-zero message ID/seq; outsider gets no message.
5. Send a non-durable request-scoped command message:
   - `header.sync_once=1`
   - `header.no_persist=1`
6. Verify subscribers receive the payload with `NoPersist` and `SyncOnce` set, and `MessageSeq=0`.

## Observable Assertions

Durable send:

- HTTP status is `200`.
- response `message_id` and `message_seq` are non-zero.
- each subscriber receives exactly one packet.
- packet `FromUID` is the API sender.
- packet payload matches the API payload.
- packet `ChannelType` is `ChannelTypeTemp`.
- packet `ChannelID` does not contain `____cmd`.
- packet has `SyncOnce` set.
- outsider read times out.

Non-durable send:

- HTTP status is `200`.
- response `message_id` is non-zero and `message_seq=0`.
- each subscriber receives one packet with `NoPersist` and `SyncOnce` set.
- outsider read times out.

## Error Handling And Diagnostics

On failures, assertions include `cluster.DumpDiagnostics()` so generated config, ready observations, process output, and node logs are visible. The no-recv checks only accept read deadline/timeouts as success.

## Documentation

Update:

- `test/e2e/AGENTS.md`
- `test/e2e/message/AGENTS.md`
- new scenario `AGENTS.md`

## Testing

Focused run:

```bash
GOWORK=off go test -tags=e2e ./test/e2e/message/request_scoped_subscriber_delivery -count=1 -v
```

Supporting smoke:

```bash
GOWORK=off go test -tags=e2e ./test/e2e/suite -count=1
GOWORK=off go test ./internal/access/api ./internal/usecase/message ./internal/app -count=1
```
