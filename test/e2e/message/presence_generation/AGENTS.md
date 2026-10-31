# Presence generation E2E

## Failure inventory recorded before implementation

This is process-level functional evidence for the full Presence wiring, not a
performance gate or a direct observation of private G/M state.

- Default authentication accepts an unregistered Token, or the first valid
  CONNECT fails and a retry hides it.
- An authenticated cross-node receiver loses delivery after its UID's physical
  Slot authority moves, unless it reconnects or first sends/touches its route.
- A receiver-owner process restart leaves the old boot's route authoritative,
  or the replacement's one CONNECT cannot restore bidirectional messaging.
- A joined whole-cluster restart loses credentials, requires a new SEND to read
  retained history, or loses/corrupts/reorders/duplicates an acknowledged row.
- A SENDACK does not match RECV identity/sequence/payload, or exact permitted
  realtime redelivery is confused with duplicate durable history.
- A failed setup or assertion omits the bounded post-cleanup report or leaves a
  process group, client or restart generation unjoined.

## Fixture and execution

Use one real three-node cluster with 256 Hash Slots, 12 physical Slots, three
Slot/Channel replicas and production Token authentication. Resolve the receiver
UID through the public routing primitive and complete Manager inventories. Its
initial authority differs from its ingress owner, node 3. Exclude the suite
readiness UIDs and sender from its Hash Slot, so their CONNECTs cannot prewarm
the receiver slot after a boot change. Provision two APP
credentials and CONNECT each business Session exactly once per process lifetime.

Transfer the receiver's physical Slot using one public Manager POST after
stable authority evidence. Any legal non-source Raft leader is valid; the
preferred target is not proof of authority. Keep the receiver Session open and
first send to it after transfer, without reconnect, explicit PING, touch API or
receiver-originated SEND. Normal product background work remains enabled; this
fixture does not claim that no autonomous touch occurred.

Then join and restart only the receiver owner, preserving the sender Session;
prove the first replacement CONNECT and two-way traffic. Finally close clients,
join all three processes, restart the same data directories, read all retained
history before another SEND, and prove first CONNECT and two-way traffic again.
History uses ascending pages of two rows beginning at sequence 1, terminates by
More and verifies all acknowledged rows on every ingress. Decode public
`message_id` directly into int64 and require its exact canonical decimal value
to equal `message_idstr`; missing, coercible, overflowing or disagreeing forms
fail. No float64 conversion is allowed. Artifact schema v2 records both forms. Only the explicit
503 `unavailable` history envelope is retryable. Successful wrong or truncated
history fails immediately; business CONNECT/SEND and Manager mutations have no
retry. Public readiness/authority polling is bounded separately.

No production hook, private import, controller store inspection, proxy or
profile is used. Process ownership, configuration and protocol support remain
in `test/e2e/suite`. The test owns only its small business fixture and report.

```sh
WK_E2E_PRESENCE_GENERATION_REPORT=/absolute/presence-generation.json GOWORK=off go test -tags=e2e ./test/e2e/message/presence_generation -run '^TestPresenceGenerationSurvivesAuthorityAndBootChanges$' -count=1 -timeout=4m -p=1 -v
```

`WK_E2E_BINARY` selects a frozen product binary; the report hashes that actual
binary (128 MiB read bound). JSON is capped at 64 KiB, mode 0600, and written
only after suite cleanup plus explicit joins of all retained process owners.
It records fixed fixture identities and payloads, never credentials. Default
output is `/tmp/wukongim-presence-generation.json`. Keep the original failed
receipt when repeating after a separately justified code or fixture change.
