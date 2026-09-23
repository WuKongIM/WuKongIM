# MQTT 5 IM wire contract

Implementation contract for [MQTT IM access](mqtt-im-access.md). This document
freezes the application conventions; it does not announce an enabled listener.

## Application identity and topics

- CONNECT User Name is the exact UID. Password is the opaque existing device
  token. One `wk.device_flag` User Property is required, with canonical decimal
  `0` (APP), `1` (WEB), or `2` (PC). No client-selected DeviceLevel or SYSTEM
  category is accepted. ClientID is required and never used as a credential.
- ClientID, UID, decoded topic ID and `wk.client_msg_no` are limited to 1,024 UTF-8 bytes; token input is limited to 16 KiB. Whitespace-only identities are rejected without trimming valid identity values.
- Every `{id}` topic segment is the canonical unpadded base64url encoding of
  the UTF-8 business ID. Empty IDs, invalid UTF-8, noncanonical encoding and
  decoded NUL are rejected. For example, UID `bob` uses
  `wk/v1/users/Ym9i/messages`, and group `g1` uses
  `wk/v1/groups/ZzE/messages`.
- Incoming PUBLISH and Will use one `wk.client_msg_no` User Property as the
  stable application idempotency key. Packet Identifier never replaces it.
  Will execution uses a server-owned generation-bound idempotency identity;
  the supplied number is metadata, not permission to collide with another Will.
- The `wk.` User Property namespace is reserved. CONNECT accepts only
  `wk.device_flag`; PUBLISH/Will accept only `wk.client_msg_no` from clients.
  Duplicate reserved properties and other client-supplied `wk.` names fail.
  Unreserved User Properties retain their original order and duplicate keys.
- Outbound publications add `wk.message_id`, `wk.message_seq`, `wk.from_uid`,
  `wk.channel_id`, `wk.channel_type`, and `wk.client_msg_no`. Numeric values are
  decimal strings. MessageID is stable across reconnect and QoS 1 redelivery.
- Payload bytes are the existing IM payload. MQTT QoS 0 does not set NoPersist.
  Source publications retain their original QoS, publisher identity, ordered
  properties and expiry basis through commit, replay and backup.

## Protocol capability matrix

| Capability | Contract |
| --- | --- |
| Version | MQTT 5.0; other versions receive Unsupported Protocol Version where a response is permitted |
| Delivery QoS | 0 and 1; Maximum QoS=1 in CONNACK; requested subscription QoS 2 can be granted 1 |
| Retain | Retain Available=0; incoming retained PUBLISH or Will rejected |
| Topics | Exact only; Wildcard Subscription Available=0, Shared Subscription Available=0 |
| Subscription Identifier | Supported; zero and duplicate identifiers in SUBSCRIBE rejected |
| Topic Alias | Initial Topic Alias Maximum=0; no incoming aliases accepted; server sends full topic names |
| Authentication | User Name / Password; no enhanced AUTH negotiation |
| Session | Clean Start, Session Expiry and Session Present reflect authoritative durable state |
| Flow control | Respect peer Receive Maximum and Maximum Packet Size; bounded local packet, property and subscription limits |
| Will | QoS 0/1, no retain, durable execution and Will Delay with current authorization |

Application authorization and unsupported capability decisions belong in access
and use cases, after the independent codec validates MQTT wire syntax. The codec
must not import WK frames, UID permissions, storage, or gateway sessions.

## Failure inventory and approved verification seams

The approved plan's wire codec seam uses public Decode/Encode functions, literal
OASIS-derived fixtures, independent Paho client packets and bounded fuzzing.
Isolated codec tests are necessary to exercise malformed and fragmented input
without opening thousands of real processes. Feature acceptance remains real
`cmd/wukongim` processes and standard clients, with single-node and three-node
clusters. No storage side channel substitutes for observed delivery behavior.

Before implementing each slice, write its failing public-boundary test:

| Slice | Failures to distinguish |
| --- | --- |
| Framing / CONNECT | Partial header/body vs malformed input; nonminimal/overlong VBI; oversized announced packet before allocation; bad flags/version/name; truncated fields; invalid UTF-8/NUL; trailing bytes; invalid Will flags |
| Properties | Unknown/disallowed/duplicate properties; invalid booleans; zero Receive Maximum/Maximum Packet Size/Subscription Identifier/Topic Alias; malformed lengths; ordered duplicate User Properties; bounded amplification |
| Publish / subscriptions | QoS=3, QoS 0 DUP, missing/zero packet ID, invalid topic syntax, empty subscribe/unsubscribe payload, reserved subscription bits, requested QoS=3/retain handling=3, shared No Local |
| Server output | Independent client decodes CONNACK/PUBLISH/ACK/reason codes; peer packet limit enforced; invalid output rejected; input/output byte ownership |
| Application mapping | Noncanonical topic encoding, wrong UID inbox, impersonated reserved properties, missing idempotency, forbidden device category |
| Session | Concurrent ClientID takeover, stale owner ACK/close/Will, expired lease, partial projection, Clean Start/expiry/quota, no false Session Present |
| Replay | Crash after commit/before callback and after copy/before frontier; new personal source; retention/leader change; immutable retransmit; holes and missing authority proofs |
| Delivery / Will | PUBACK crash windows, No Local/QoS/expiry, Receive Maximum, unsubscribe vs revocation/rejoin, durable Will delay/cancel/execution, bounded recovery and capacity |

Protocol source: [OASIS MQTT 5.0](https://docs.oasis-open.org/mqtt/mqtt/v5.0/os/mqtt-v5.0-os.html),
sections 1.5, 2, 3 and 4. Application conventions above are WuKongIM-specific.

Gateway implementation failure slice: independent packets must use the existing
bounded auth worker and session-ordered dispatch mailbox, never run storage work
on the transport loop. The public core boundary verifies CONNECT-first/single
handshake, rejected or pending authentication, output failure activation rollback,
post-auth ordering, drain admission, queued+executing byte budgets, and serialized
packet writes. WK/JSON-RPC paths retain their existing regression suite. These
isolated scheduling tests accompany, not replace, product process acceptance.
Additional gateway failures cover fragment allocation amplification, peer packet
limits, callback panic redaction, listener-error routing, bounded packet-kind
observations, complete-packet Keep Alive renewal and zero disabling the timer.
