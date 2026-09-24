# MQTT outbound window admission

WindowAdmission.Prepare derives the next action for one already-accounted source
under an exact live Owners scope. It pins Session/cursor/accounting head, verifies
current active subscription and source binding, reads bounded original content
under a committed replay anchor, and rechecks placement/receive permission.
The input has no message, counts or mutable caller-supplied admission reference.
Only accounting version 1 is accepted; legacy state needs explicit safe upgrade.

Each turn handles at most 256 positions and one accounting receipt. A skipped
prefix advances in one command-70 commit, debiting exactly its original charges.
Expired/No Local/internal records cannot become packets. A currently QoS-1 record
that was never charged is not resurrected by changed subscription options. The
first eligible QoS-1 record is atomically admitted with its original content
reference, PacketID and DeliveryOrder; a subsequent authoritative point read
confirms that exchange before returning it. Window-full is a flow-control result.

QoS 0 remains distinct: Prepare returns an uncommitted candidate and a private
captured completion mutation. CompleteQoS0 may be called only after successful
packet enqueue; it checks the exact owner and commits that original revision's
advance/debit. Failure keeps the record recoverable and cannot report completion.
It can release a prior QoS-1 charge after an explicit subscription downgrade,
but never fabricates an inflight exchange or silently discards the message.
No offline guarantee is added for original QoS-0 publications. Independent
scheduling/expiry cleanup may discard such uncharged history within that contract.

Returned preparation is not a send grant. The caller still orders old exchanges
before new delivery on resume, rechecks receive permission at the network admission
point, serializes preparation/enqueue/completion per connection, binds the packet
in the entry and holds exact-owner execution across the write. Ordinary unsubscribe preserves begun exchanges; this module never ACKs or
abandons them. Actual sender/recovery scheduling remains required before enabling
the product listener. No schema, command or RPC format is added here.

[OASIS MQTT 5.0](https://docs.oasis-open.org/mqtt/mqtt/v5.0/mqtt-v5.0.html),
sections 3.8.4, 4.3.2 and 4.9, define effective publication QoS, existing QoS-1
exchanges and the connection send quota; this module's admission is separate from
the gateway's enqueue/connection quota.

## Failure inventory before implementation

- Body/bytes/hash/position are invented instead of read from anchored originals;
  missing, foreign, partial or corrupt Session/child/head/binding/page becomes empty.
- Skips debit re-evaluated counts instead of original charges, cross unbounded
  receipts, erase ACK gaps or revive uncharged QoS-1 records after option changes.
- QoS downgrade erases queued responsibility before sending; QoS-0 completion
  can be forged, used for another owner or repeated after another mutation.
- Expiry/No Local changes are ignored before new admission, or begun exchanges
  are incorrectly expired or acknowledged by preparation.
- Parent/options/owner/permission/source-placement races admit foreign work;
  full windows are reported as successful sends or expose a phantom exchange.
- Lost/malformed commit replies or failed readback expose unproved exchanges;
  callback panic, clock regression, cancellation or owner expiry leak scopes.

Tests precede code at the approved public usecase/metadata and real Node seams.

## Frozen context

Source `882dd81a9860b1e21a621dd6b6a50501d147c935`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `6af994db31ead9a63acb867d91886757ece04969e0735ede0982da041c09e769`
- `internal/app/FLOW.md`: `2a675b7a1aaba305e5d5200b4939d1f8c511441d9055e3c9b55a98824d996e06`
- `pkg/channel/FLOW.md`: `e1afae5bebc02c8af1228479ee5cc32686507fe969b6500c27380527de249b01`
- `pkg/db/FLOW.md`: `3560287fef837ef40dcf04754ac1037a28ec85d26dae7748f465ab9524c624fe`
- `pkg/db/meta/FLOW.md`: `39d9e3024bb42cd14587b12557c0d58b9250444c21ceac6598581517096e0079`
- `pkg/protocol/publication/FLOW.md`: `c233c21b41ca6cfc2f4c34ffec50bba4eb099952591856b4f3233f231a2ec655`
