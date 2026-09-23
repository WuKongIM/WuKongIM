---
scope: package
summary: Defines bounded immutable publication metadata and its versioned durable codec independently of entry packets and storage.
---

# Publication Metadata Flow

## Responsibility

This package preserves original publication QoS, publisher identity, topic,
ordered content properties and expiry basis in one bounded value. Native IM
publications omit the optional value. It owns no sessions, authorization,
delivery policy, persistence or network packets.

## Boundaries

- Entry adapters translate wire properties into these semantic property kinds.
- Message storage validates and preserves the complete encoded bytes; exact
  proposal identities must bind them alongside the original message body.
- Enclosing rows/proposals provide integrity; the value has no extra checksum.

## Main Flows

1. Encode validates source, time, identities, property variants, duplicates and
   total size before allocating v1 content or v2 content with a server Will key.
2. Decode bounds input/counts, rejects unknown or partial formats, and returns
   independently owned strings and binary properties in original order.
3. Expiry uses ordinary MQTT ingress time or the original Will source append
   timestamp, never replay time. Absent and zero expiry remain distinct.
4. Content comparison and lookup fingerprints validate optional values and
   exclude only the ingress clock. Hash matches require exact comparison;
   neither can replace a durable packet-exchange identity.

## Invariants and Failure Semantics

- Complete metadata is at most 32 KiB with at most 128 properties. The limit
  includes identities and overhead; exceeding it never truncates attributes.
- Only user properties repeat; unknown kinds, duplicate singleton properties,
  noncanonical variant fields and trailing bytes fail closed.
- Publisher namespace plus ClientID is stable across Session generations.
- Will configuration has no ingress clock and does not forward Will Delay.
- Setup reserves the 79-byte v2 identity tail within the complete size bound.
- V2 is Will-only and carries the immutable intent key; native/v1 bytes stay fixed.
- An expiry deadline does not authorize abandoning an already begun exchange.
- This format grants no execution authority and proves no committed result.

## Read First

- [Contract and codec](metadata.go)
- [Durable format tests](metadata_test.go)
- [Frozen format and rollout](../../../docs/specs/mqtt-publication-metadata.md)

## Update Triggers

Update this file when the format, bounds, property ownership or expiry basis
changes, or when a caller-dependent policy moves across this boundary.
