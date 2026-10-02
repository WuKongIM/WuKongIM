# Typed MQTT replay consumer content

## Contract and failure inventory before implementation

Consumer delivery needs complete immutable message fields and an authoritative
control classification. Extend the internal consumer page to typed publications
with the original content reference/cumulative counters. The serving storage
snapshot verifies every canonical row against its retained committed native entry
and paired proposal. Only explicit native proposal formats 4/5/6 classify as
internal controls; payload spelling and SyncOnce alone never establish that fact.
Consumer policy (No Local, expiry, QoS, permissions) remains above storage; the
[consumer accounting usecase](mqtt-consumer-accounting.md) now applies it to
qualified backlog without authorizing sends.

The Channel adapter maps strict decoded storage messages to existing Message
values; it must not use the permissive ordinary-history opaque-byte fallback.
RPC 102 uses a distinct v2 envelope and the existing version-11 message codec;
there is no downgrade to the earlier opaque-page reply. It returns typed content
once, without additionally transmitting its canonical storage envelope. Requests
remain bounded to 256 rows/16 MiB of original canonical content; replies retain
an explicit total cap and validate fields, counters, channel association and exact
request echo. No durable schema, generic replay transfer or repair format changes.

Failures to cover before implementation:

- Missing/corrupt native entry, paired proposal or committed tail; wrong entry
  identity despite a valid shared row; pending or unsupported native formats.
- Ordinary payloads that imitate control bytes, including SyncOnce messages,
  mistaken for authenticated internal controls; actual controls delivered as IM.
- Loss of IDs/sequence, sender, client message number, settings, flags, original
  expiry/timestamp, publication properties or content hash through local/RPC reads.
- Malformed canonical rows receive a permissive opaque fallback, or a late failure
  leaks an earlier partial page; returned content aliases storage or another read.
- Truncated/foreign/oversized/old/trailing RPC bodies, unknown flags, invalid
  publication metadata or incorrect cumulative counters survive structural checks.
- Original-body trim or restart destroys classification; source protection,
  consumer progress or quotas change merely because content was read.

Tests use the previously approved storage, Channel adapter, routed Node and app
integration seams. Full product delivery and process E2E remain outstanding.

## Frozen context

Source `ee982d2427ad2c320dbc473555ef77ab8d6fe542`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `pkg/db/FLOW.md`: `3560287fef837ef40dcf04754ac1037a28ec85d26dae7748f465ab9524c624fe`
- `pkg/db/message/FLOW.md`: `cf39b4608d820a060a9d3d8fe6792eede378dadc04337d44b5952db87274890d`
- `pkg/channel/FLOW.md`: `d9b8167e537ad70b449070ceae6f1a961772efa426c23a5ae41f03e112ce69b7`
- `pkg/cluster/FLOW.md`: `938e5ceb142e6fa05fe26ad9cd17feba03ef7d0ed1cc0d62f8c688a66c74494c`
- `internal/app/FLOW.md`: `692104c8015a289ebb4d9ddd8888ce86c9120f6f9efd2338a92f31f295d8b19d`
