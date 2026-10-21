# Self-chat E2E

## Existing contract and scope

This scenario exercises real `cmd/wukongim` processes through public WKProto and
HTTP. It adds coverage, not a new release gate or a new messaging guarantee.
`internal/runtime/persondirectory/projector.go` explicitly supports self-channels
with one distinct UID. `TestRuntimeSuppressesOnlyExactSenderSession` in
`internal/runtime/delivery/runtime_test.go` retains delivery to the sender's other
sessions. The public Devices guide describes UID-scoped persistent history and
client-side receive deduplication. Do not require exactly-once wire delivery.

## Failure inventory (recorded before the test)

- A valid authenticated UID cannot SEND to its own person Channel, or a default
  auth fixture accidentally accepts unprovisioned credentials.
- A second device for the same UID is suppressed with the sending session, or
  the sending session receives its own WKProto message.
- WKProto or HTTP acknowledgments disagree with received identity, sequence,
  sender, Channel, payload, or persistent flags.
- Self-membership projection never converges, or complete public history omits,
  duplicates, reorders, or corrupts an acknowledged message.
- All processes stop and restart but retained history becomes readable only
  after another SEND, credentials disappear, or a later append loses old rows
  or fails to advance the sequence.
- A permissive retry masks a malformed response or successful wrong history;
  incomplete history pagination is mistaken for the full retained history.
- A failed case omits its bounded JSON receipt or leaves child processes alive.

## Execution

Use production-default Token authentication, separate APP and WEB credentials,
256 Hash Slots, and both a single-node cluster and a three-node cluster. The
three-node devices connect through different nodes. Send from each device and
through HTTP, then verify full ascending `/channel/messagesync` pagination on
every ingress using explicit `start_message_seq=1` and `pull_mode=1`; both range
bounds zero select the latest page rather than the beginning. Stop and join all
processes, restart the same data directories,
verify retained history before another SEND, and prove another authenticated
send in each direction after restart. Receivers ACK and deduplicate only exact
previously verified MessageIDs; unexpected messages or changed duplicates fail.

Only the explicit HTTP 503 `unavailable` envelope can be retried for history
recovery. Projection polling uses `/conversation/list`; a successful history
page is checked immediately without correctness polling. Recovery sequence
positions may contain internal barriers; require strictly increasing business
sequences, not gap-free allocation.

Run:

```sh
WK_E2E_SELF_CHAT_REPORT=/absolute/self-chat.json GOWORK=off go test -tags=e2e ./test/e2e/message/self_chat -count=1 -timeout=4m -p=1 -v
```

`WK_E2E_BINARY` may select a frozen product binary. The receipt records its
SHA-256, topology, stage, authenticated devices, acknowledged message facts,
bounded receive counts, and exact history observations. It includes no tokens.
The default receipt is `/tmp/wukongim-self-chat.json`; choose a unique path for
concurrent runs. Keep one-off helpers here and update both E2E catalogs when the
scenario contract or command changes.
