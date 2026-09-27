# MQTT Will append identity

This refines the approved separate server idempotency domain. The full MQTT
feature remains disabled until its runtime and acceptance gates are complete.

## Durable contract

- Will configuration remains publication metadata version 1. On publication,
  the server binds the durable Will row's `IdempotencyKey` into metadata version
  2 as a final uint16-sized string after the unchanged content properties.
  Version 2 is valid only for SourceWill and requires `mqtt-will-v1:` followed
  by 64 lowercase hexadecimal characters. It grants no execution authority.
- Existing metadata without that identity remains version 1, byte-for-byte.
  The complete value remains bounded by 32 KiB. Setup must reserve room for the
  identity before accepting a Will. Old unkeyed Will templates/fixtures remain
  readable; they are not evidence of a server-domain append.
  Product send admission rejects unkeyed Will templates before routing or IDs.
- Message table 1 adds unique index 8: `(server_will_key, from_uid)` within a
  Channel. The key is derived from publication column 21. Its value is the
  existing 24-byte sequence/message-ID/payload-hash tuple, verified against the
  original row and identity. Native index 4 keeps its existing bytes and domain.
- A keyed Will keeps its original `ClientMsgNo` but never occupies index 4.
  Nonunique client-number index 3 also includes keyed Wills, retaining its
  `(client_msg_no, sequence)` layout. Two different Wills may share a client
  number, including one already used by an ordinary send.
- Index 8 uses durable point reads, bypassing the native negative filter. It
  adds no per-Channel cache or unbounded membership scan. Same-batch duplicates
  and retained-prefix recovery conflicts use the full domain identity.
- All append, follower apply, replacement, delete, retention and portable import
  paths maintain the derived indexes. There is no index-8 backfill: only the new
  version-2 records can carry its identity. Matched runtimes/tools are required;
  older readers reject metadata version 2. Rollback requires a pre-feature data
  generation. Proposal format 3 already binds the full encoded metadata.

## Failure inventory before implementation

The isolated durable codec/store seam is necessary while product MQTT E2E is
still RED. These checks precede implementation; product execution remains a
separate acceptance requirement.

1. Encoding loses the Will key, changes native v1 bytes, accepts malformed or
   client-controlled ordinary-source identities, or permits partial/trailing v2.
2. Metadata size excludes the new tail; content comparison/fingerprint ignores
   the Will key; a decoded key aliases caller-owned bytes.
3. A client number collides across ordinary/Will domains, or two Will intents
   with the same number incorrectly collapse. The same server key with a changed
   client number evades uniqueness, either in one batch or a later append.
4. Missing sender/client metadata is accepted for a keyed Will. A malformed or
   dual-domain lookup picks an unintended row. A corrupt index points at another
   valid Will and is returned without checking the row's server key.
5. Follower apply, canonical-entry eviction or reopen loses uniqueness. A warm
   native filter incorrectly proves a Will key absent.
6. Truncating/retaining one domain deletes another domain's index. Client-number
   history lookup omits Wills. Backup/import drops or fails to rebuild index 8.
7. Recovery replacement permits reuse of a retained Will key, fails to reject
   same-batch duplicate Will keys, or prevents replacing a discarded suffix key.
8. Exact proposal identity does not bind the key. Product lookup/coalescing
   still selects native identity, or execution publishes before binding a key.
9. Setup accepts a template that cannot fit its eventual server identity.
   Runtime retries fail to retain original body/time/identity after an edit,
   takeover or ambiguous commit. These require follow-on mapping/runtime tests.

## Execution recovery constraints

The executor is still required. At source `d9f660450`, original committed lookup
and index 8 cannot independently prove a publication absent after an uncertain
append. `pkg/db/message/truncate.go` deletes the derived Will index together with
the primary row; `TestWillIdempotencySeparateDomainsSurviveReopenBackupAndDeletion`
explicitly verifies that lookup misses after physical prefix trim. The existing
index contract must not be reported as retention-independent execution recovery.

Additional failure inventory before implementing the executor:

10. A Will append commits, its reply or the final Will CAS is lost, and ordinary
    history is physically trimmed before retry. Recovery must retain a committed
    receipt or protect its original proof until the detached task can finish;
    a missing index cannot authorize a new business message. This includes
    backup/restore and Channel incarnation changes, not only process restart.
11. An execution lease expires while an admitted append has an unknown outcome.
    A successor cannot treat wall-clock expiry as proof that the old append has
    stopped. In particular, later permission denial must not discharge ambiguous
    publication responsibility as a definitive rejection. Every actual publish
    still requires fresh authorization and bounded, fenced execution.
12. A committed Will is found after permission changes. Recover its original
    MessageID, sequence, timestamp and server identity before deciding whether a
    new publication is needed; do not restart the expiry clock or overwrite the
    old receipt with an authorization failure.

[System-16 receipts](mqtt-will-receipts.md) now implement the local retained-proof
prerequisite, including binary backup v4 and conflict preflight. They do not yet
supply routed execution authority, whole-channel deletion/restore fencing or a
Will executor. Those requirements remain outstanding; the product listener is
not ready.

## Frozen implementation context

Source `84b99eb1d`; applicable SHA-256 digests:

| File | SHA-256 |
| --- | --- |
| AGENTS.md | d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade |
| pkg/db/FLOW.md | d90cb020a5d487214b0dd3fefdc49533d50c7f37c92e217aa139141c9b85761e |
| pkg/db/message/FLOW.md | b6a603c01bd5c60d8bff6b749eb0f8a786f224cc0bb8c3b2845d0db66221898a |
| pkg/protocol/publication/FLOW.md | d8600932d043b5e1e722911a501558fef7fb12af69b29a69b0b8ed64f6b31d72 |
| pkg/channel/FLOW.md | 5b24bd342971d2febc4f10e6e400ae97267e119e2ee8032a1530c769bd2f335d |
| pkg/cluster/FLOW.md | af8db6b207f994883286bad63d507aa971b71f6bbea2ae9d476effce6dd85b54 |
| internal/infra/cluster/FLOW.md | 2337c3c398ba744ac13c8f87fea06a10778865c5af90586e2f9aec3d4fb28293 |
| internal/app/FLOW.md | 8dab6a38e7e158056e187b4ae2e56b60d3bceb8728e955e870820c3976ed209c |
| internal/access/mqtt/FLOW.md | 90989c3793ecb1d308fd37bb73beca1b2a61be2c4ac492918b945c89eec4ebc2 |
| internal/runtime/channelappend/FLOW.md | 64808e9b984c89929570d04aff5d9fe4615f6ab0f84dc79d3b583dc70b3f0d73 |
