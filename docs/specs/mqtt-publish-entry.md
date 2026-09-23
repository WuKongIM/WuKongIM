# MQTT authenticated PUBLISH entry

The entry adapter maps one accepted MQTT connection's PUBLISH into the existing
message usecase. It adds no listener, storage table, append path, or QoS tracker.
Product admission remains unavailable until complete Session and delivery wiring.

## Failure inventory before implementation

1. Trust a packet, gateway value or mutable connection UID instead of the identity
   reserved during authenticated acquisition. Read UID from the admitted owner
   operation and reject a mismatching connection descriptor. ClientID is never
   DeviceID, a system-device bypass, MessageID, ClientSeq, or an idempotency key.
2. A replaced, pending, expired, stopped or capacity-limited owner sends or ACKs.
   Begin exact-owner execution before effects; check its lease and cancellation
   after permission, after Send and before writing. Keep the scope through reply
   enqueue, including failure/panic. Cancellation does not release joined work.
3. Long permission/append calls overrun the entry deadline and return success.
   Use a bounded call context, check it on return and join entry calls. No
   detached timeout goroutine, queue, retry loop or per-connection worker.
4. MQTT publishes to encoded person conversations or configured CMD channels,
   bypasses current permissions using a warm SEND cache, or runs hooks on denial.
   Use the existing uncached ordinary-publish query before Send. Send still owns
   permissions, hooks, person-directory creation, routing and durable idempotency.
5. QoS 0 accidentally becomes NoPersist, or PacketID replaces client_msg_no.
   Both QoS levels persist. Only QoS 1 gets PUBACK. PacketID is correlation only;
   the mandatory stable client_msg_no and immutable publication metadata survive.
6. Successful PUBACK is sent before a committed result, or a timeout, route error,
   unknown failure, invalid success receipt or error-plus-success becomes a
   terminal negative ACK that tells the client to forget an uncertain publish.
   ACK success requires nonzero message ID/sequence; uncertain outcomes close
   with no PUBACK so stable application retries use existing idempotency.
7. Terminal permission/hook rejections produce unsupported MQTT reasons or leak
   payload, identities, credentials or arbitrary dependency diagnostics.
   Known authorization rejection uses 0x87, unavailable/invalid ordinary topic
   uses 0x90, other explicit business rejection uses 0x83. QoS 0 rejection closes.
   Wire errors use bounded fixed diagnostics; no raw dependency text is emitted.
8. Reply failure leaves admission open, fatal close waits recursively for its own
   operation, or takeover completes while a reply callback is still active.
   Fatal paths fence local admission and request gateway closure; the separate
   lifecycle callback joins cleanup only after this operation returns. Physical
   closure is not inferred from that request. Quiesce still joins all effects.
9. Mutation of packet buffers corrupts asynchronous storage; repeated packet IDs
   suppress distinct messages or different packet IDs duplicate one application
   retry. Map owned bytes and use shared send idempotency, with no PID cache.
10. Unit-only mocks miss wiring errors. Add an integration through the production
    message usecase and a real single-node cluster; prove committed metadata,
    QoS 0 persistence, cross-PID retry, and denial after group membership removal.
    This is separate from the outstanding full process-level MQTT acceptance.

## Protocol and bounds

[OASIS MQTT 5.0](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html)
sections 3.4.2 and 4.3.2 define PUBACK reasons and correlation; the IM contract
chooses durable commit as acceptance. Never send a nonstandard PUBACK reason.
The call timeout defaults to five seconds and is bounded to one minute. Existing
packet/property limits and gateway ordered, bounded dispatch still apply.
No ACK is promised to reach the peer merely because enqueue succeeded.

## Frozen context

Source `62129e7bb`; SHA-256 digests:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/access/mqtt/FLOW.md`: `9dcd77d3a6be9170f40b4de7959a541942c6844848e226174a8f7e1b733300cd`
- `internal/runtime/mqttsession/FLOW.md`: `e0b1686cb986a6a7c36ab51b2a60ebdcf34d02cbc9e35e0dae508c710e823e9d`
- `internal/contracts/mqttsession/FLOW.md`: `5bd4d98da6fa4d59615b62f31f98056add1127274a29816c8d5a181015329841`
- `internal/contracts/channelappend/FLOW.md`: `b8b3aaa839d8fe0cc03ff6652a29635115c5f708c19476573b695965ac863b15`
- `internal/usecase/message/FLOW.md`: `2807932a88c025051791cbd1daf3cc0392fc949fb139081630e45fa938553042`
- `internal/usecase/mqttsession/FLOW.md`: `a0b7c20fa60f7c56154ecf436d8ec8865a2d18677f2d9eea142699d174489182`
- `internal/app/FLOW.md`: `7fa6de76a3b2ac3501d212d467e022789d09400c3ba059791a5ea83c111a8889`
- `pkg/gateway/FLOW.md`: `3e2270d966269b2835a32c54e001467aba014cc5554e4db4f253e11878348dc7`

## Discovered append completion boundary

`channelappend.Future.Wait` and remote forwarding can return on cancellation while
admitted Channel work continues. A returned Send error cannot satisfy a local
owner's quiescence contract. Before releasing a scope for an uncertain Send
(including panic, invalid receipt or unknown reason), permanently mark that owner
as having unresolved work. It remains fenced and occupies bounded registry
capacity; physical close plus zero local operations returns isolation-unproved,
never a success receipt. This barrier has no timeout-based clearing path.

A valid committed receipt or definite business rejection resolves the append
outcome, including when the entry context has since expired; the entry still
suppresses late ACKs. Post-commit Channel-owned projection/delivery is independent
of the old connection. Future reconciliation must prove all uncertain accepted
attempts finished or were fenced before releasing this barrier. Product admission
must not advertise recoverable takeover until that proof is implemented.

`internal/runtime/channelappend/FLOW.md` at source `62129e7bb`: `0eed1460db51ec882d463c7b0ab8d4e38ad46ca01f33a52274ef8d7d554cf404`.

Additional pre-fix failure cases: an uncertain Send releases its scope and lets
Quiesce succeed; physical closure races uncertain marking; concurrent quiescence
waiters receive different proof; repeated marking double-counts capacity; Stop or
Sweep discards the unresolved owner; a completed scope marks an unrelated owner.
