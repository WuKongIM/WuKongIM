# MQTT existing-exchange recovery preparation

ExchangeRecovery.Next reads the next existing exchange in original DeliveryOrder
for an exact live owner. The trusted caller supplies the last successfully
processed cursor, serializes recovery, and completes recovery before admitting
new delivery. The method has no writes, retries, socket or scheduled work and
cannot itself claim network-send permission or completed connection recovery.

One bounded one-entry inflight page pins the Session. Its exact source cursor
and retained active/removing binding must corroborate the exchange, UID, original
authorization incarnation and incomplete responsibility. Current subscription
options/tombstones/replacement generations cannot alter a begun exchange. Receive
permission is checked for the original group or authenticated user's inbox with
the cursor's authorization version. Ordinary unsubscribe may preserve recovery;
revocation/incarnation change denies it without ACK or silent abandonment.

Read exactly one original publication under fresh placement/committed anchor,
compare immutable ID/sequence/version/hash/bytes and recheck permission, then
point-read the exact exchange under current ownership. Concurrent disappearance,
new admission, changed identity or incomplete evidence yields without moving the
caller's cursor. ACK of a different exchange may update links; immutable exchange
identity remains mandatory. Expiry, No Local and current QoS options never erase
a begun exchange. QoS/SubscriptionIdentifier remain frozen in that exchange.

## Failure inventory before implementation

- Empty/partial/stale/foreign pages or broken continuation report recovery done;
  PacketID order replaces immutable DeliveryOrder, or invalid cursor skips work.
- Wrong owner/UID/lease, mismatched Session/cursor, impossible debt/position,
  released or foreign source binding becomes sendable evidence.
- Current subscription replacement overwrites original exchange options, ordinary
  unsubscribe discards it, or changed permission incarnation silently passes.
- Unanchored/gapped/edited/corrupt/foreign content replaces the original reference;
  lost placement or permission races retain a delivery result.
- ACK/removal/identity changes during content reads still expose stale delivery;
  owner cancellation, clock regression or callback panic leaks scopes.
- A read-only preparation writes ACK/window/progress or treats a lower current
  Receive Maximum as permission to erase old exchanges.

Public usecase/real metadata tests and the real three-node original-trim app seam
precede code. Final network permission, sender serialization/ordering and complete
reconnect/product acceptance remain required under the original goal.

## Frozen context

Source `0c18d5fdb89dfe8a9adf072c24f69c37c75ebf44`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `5de69cd8e7b1535a356185eb912e7e81caddafd00d6b6005af9be4a2686c4c99`
- `internal/app/FLOW.md`: `ba824074593750552ad80e62d415eccb9174af7b783fc17cba07715d41a6e5aa`
- `pkg/channel/FLOW.md`: `e1afae5bebc02c8af1228479ee5cc32686507fe969b6500c27380527de249b01`
- `pkg/db/FLOW.md`: `3560287fef837ef40dcf04754ac1037a28ec85d26dae7748f465ab9524c624fe`
- `pkg/db/meta/FLOW.md`: `39d9e3024bb42cd14587b12557c0d58b9250444c21ceac6598581517096e0079`
- `pkg/protocol/publication/FLOW.md`: `c233c21b41ca6cfc2f4c34ffec50bba4eb099952591856b4f3233f231a2ec655`
