# Message update API and SDK contract

Server implementation for [issue #957](https://github.com/WuKongIM/WuKongIM/issues/957).
The two new routes are independent of ordinary `/channel/messagesync`. CMD,
SyncOnce, nonpersistent and stream messages cannot be edited. The backend owns
editing authorization and time-window policy; these are trusted backend APIs,
not a replacement for application authentication.

## Replace one payload

`POST /message/update`

```json
{
  "login_uid": "alice",
  "channel_id": "bob",
  "channel_type": 1,
  "message_id": "12345",
  "expected_version": "0",
  "expected_content_epoch": "0",
  "request_id": "5893e137-1994-4377-b4ed-1f0c9e8fe4bd",
  "payload": "bmV3"
}
```

For a person channel, `login_uid` and the peer channel ID follow ordinary
normalization rules. Group callers supply their group ID/type; `login_uid` does
not prove editing permission. Payload is the complete replacement, Base64
encoded in JSON, nonempty and at most 1 MiB decoded. The request body is bounded
to 2 MiB. `request_id` is required, at most 128 bytes. `expected_version` is a
required decimal string; missing original `version` means zero.
`expected_content_epoch` is also required: copy `X-WK-Content-Epoch` from the
read that supplied the displayed content. A mismatch returns HTTP 409
`content_epoch_conflict`; reload the target and make a new editing decision.
The serving Slot node rechecks the epoch under restore admission, including
forwarded requests delayed across restore.

```json
{
  "status": 200,
  "data": {
    "message_id": "12345",
    "message_seq": "100",
    "version": "1",
    "updated_at_ms": 1789300800000
  }
}
```

Success means Slot quorum commit and application, including latest content,
channel edit index/head, idempotency result and pending notification. It does
not wait for devices. Original ID, sequence, author, sending timestamp,
headers/settings, expiry, unread and conversation order are preserved.

Retry unknown outcomes with the same request ID, expected version and payload.
An identical retry returns the first result even if a later edit already exists.
A different payload/version under the same request ID conflicts. After a version
conflict, fetch current content, make a new business decision and use a new ID.
An expired/deleted target cannot be resurrected by a retry.

Errors use `{"status":409,"code":"version_conflict"}` with HTTP status:

| HTTP | Code / action |
| --- | --- |
| 400 | `invalid_request`: fix malformed/bounded input |
| 403 | `channel_not_accessible`: ordinary membership/disband policy rejects the pull |
| 404 | `message_not_found`: target or applicable metadata is gone |
| 409 | `version_conflict`, `content_epoch_conflict`, `idempotency_conflict`, `reset_required`, `stale_meta` |
| 422 | `message_not_updatable`: CMD/stream/nonpersistent target |
| 429 | `resource_exhausted`: edit counter exhausted |
| 503 | dependency/authority unavailable; an uncertain write must reuse its request ID |

## Synchronize edits for the channel being viewed

`POST /channel/messageupdates`

```json
{
  "login_uid": "bob",
  "channel_id": "alice",
  "channel_type": 1,
  "update_cursor": "opaque-server-cursor",
  "limit": 100
}
```

`login_uid` is required and ordinary history membership/visibility rules apply.
Limit defaults to 100 and is bounded to 200. This endpoint queries one channel;
it does not discover or synchronize all of a user's channels.

```json
{
  "updates": [
    {
      "message_id": "12345",
      "message_idstr": "12345",
      "message_seq": "100",
      "channel_id": "alice",
      "channel_type": 1,
      "version": "1",
      "updated_at_ms": 1789300800000,
      "payload": "bmV3"
    }
  ],
  "next_update_cursor": "next-opaque-server-cursor",
  "more": false,
  "reset_required": false
}
```

The example omits unchanged ordinary message fields. The new endpoint uses
strings for message ID, sequence and version. Existing APIs retain their old
numeric fields and `message_idstr`, adding a decimal-string `version` and
`updated_at_ms` only when edited. SDKs must not round 64-bit IDs through a
JavaScript number.

An empty cursor returns an empty `updates`, `reset_required:true` and a baseline
cursor. First obtain that baseline, then load the visible history page through
`/channel/messagesync`, and commit page plus baseline in one local transaction.
Do not load history first and subsequently jump to a later edit head. A new
person conversation without directory membership returns an empty baseline;
when membership becomes available, the cursor resets to its visible generation.

Cursors are opaque and bounded to 4,096 input bytes. Format 2 binds the user,
canonical channel ID and channel type through a fixed-size SHA-256 digest of
their canonical JSON tuple, so supported long identities still produce usable
cursors. A valid, matching format-1 cursor returns a reset with a fresh format-2
baseline; foreign or malformed cursors are rejected. An oversized old cursor
cannot be decoded: bootstrap with an empty cursor instead.

Normal pages use a fixed edit upper bound. Keep calling this channel while
`more:true`, even if filtering produced an empty `updates`. Persist only the
returned cursor together with the merged data. A message edited again during
paging may move past the upper bound and appear in the next round; this is a
latest-state feed, not an edit history or historical snapshot.

A channel incarnation, membership visibility generation or successful restore
change returns `reset_required`. Mark old history caches uncalibrated and repeat
baseline-before-page initialization. A later visit to an old page must reload
that page using the existing history interface. Absence from an incomplete page
is not a deletion signal.

## SDK merge and online hint flow

1. After an authenticated connection becomes active, send an EVENT with type
   `message_updates.enable` and JSON data `{"enabled":true}`. Wait for
   `message_updates.ready`. Re-negotiate after reconnect; old connections never
   receive unsolicited edit EVENTs. `{"enabled":false}` disables hints.
2. A `message_updated` EVENT contains channel ID/type, message ID, original
   sequence and version, with no payload. All 64-bit fields are decimal strings.
   Person channel IDs are projected into the recipient's peer view.
3. For the visible channel, coalesce hints into one in-flight edit sync, then
   drain pages. Entering a channel, reconnecting and foreground resume also sync
   it. Leaving the channel stops proactive sync. Lost hints may be repaired by a
   capacity-tested, jittered low-frequency sync of the visible channel only.
4. In a local transaction, merge only newer versions for the same message,
   update a conversation preview only if it still points to that message, and
   save the returned cursor. Editing an older message must not replace a newer
   last-message preview. Do not increment unread or insert another chat message.
5. Both `/conversation/list` and `/conversation/sync` return latest previews.
   Legacy sync re-includes an edited tail even when `last_msg_seqs` is caught up;
   repeated responses are expected and must be merged by identity/version.
   Its existing array, timestamp, conversation version, sorting, `only_unread`
   and `msg_count=0` semantics remain intact.
6. Read `X-WK-Content-Epoch` on history, exact lookup, edit and both conversation
   responses. It is a decimal successful-restore generation, not a wall clock.
   A higher generation invalidates cached version comparisons and edit cursors;
   discard delayed responses from a lower generation. Page reloads in the new
   generation can legitimately return a lower message version. Browser CORS
   exposes this header. Ordinary restarts and leader transfers retain it.
   If a restore transition overlaps response assembly, the server rejects the
   response with `503 unavailable` and no epoch header or partial data. The
   production response fence samples a node-local transition stamp before the
   epoch read and before the first successful response commit; it adds no second
   Controller read or RPC. Embedders supporting restore must supply
   `ContentReadFence`; epoch resampling alone cannot detect a completed failed
   restore cycle.

No official SDK repository is changed by this server patch. JavaScript,
Android, iOS, Flutter and HarmonyOS integrations need their own transport,
transactional-cache and response-order tests before end-to-end feature rollout.

## Storage, rollout and performance boundaries

- Original Channel logs stay immutable. Channel-owned Slot tables 18–21 hold
  latest replacements, heads, idempotency results and body-free notification
  checkpoints; schema registration includes snapshot/backup/Hash Slot movement. Offline
  inspect, export/import and store comparison include every edit projection;
  nonempty edit datasets require the new CLI.
- The ordered latest-state index seeks by channel and update sequence. A page
  uses one pinned storage snapshot for head/index/payload. Exact overlay reads
  group at most 200 targets by physical Slot with at most four managed workers.
- Each production Slot group uses fresh local-leader `ReadIndex` quorum
  confirmation and waits for durable local apply. It does not write an empty
  log entry or cache readiness/negative results. At most 256 unconfirmed reads
  are retained per Slot; canceled/transiently failed requests remain counted
  until Raft confirmation or reset. New leaders first need a durable commit in
  their current term. Custom embedding ports without ReadIndex use the
  conservative fresh-noop fallback. Network quorum latency still applies.
- Across Slot groups, retained reply bytes are checked before accumulation and
  excess work is canceled. Up to four in-flight bounded RPC pages additionally
  occupy temporary buffers; 8 MiB is not a process-wide memory claim.
- Payload growth is checked after overlay. Byte-limited history scans retain
  continuation rather than silently claiming the range ended. Read waves and
  total page assembly are bounded; oversized requests fail and callers must
  request a smaller page.
- No payload or conversation row is rewritten per group member. Pending UID
  checkpoints are independent small rows; progress uses an exact previous-UID
  CAS, respecting the subscriber store's encoded key order and 65,535-byte UID
  limit. The dispatcher reads only pending identity and retention, not message
  bodies on each subscriber page. Hint frames split by encoded bytes as well
  as route count; a single identity that cannot fit is dropped as an advisory
  hint. One supervised
  worker processes bounded pages, prefers active slots, yields under a deadline,
  and logs aggregate retry failures at most once per minute.
- Retention cleanup scans only bounded index keys and rechecks the durable
  retention floor before deleting latest payload, indexes, pending state and
  all idempotency rows for the target. Channel deletion removes all edit spans.
  Cleanup is eventual; idempotency storage grows with successful edit requests
  until the original is reclaimed. It is not an audit history.
- Before activating a channel's edit head, all current Slot replicas must answer
  the new protocol probe. The head persists that exact replica-set proof; later
  writes reuse it and can commit with an already-activated replica offline.
  A changed replica set needs a fresh probe. Operate with matching server
  binaries, including nodes that rejoin under an existing ID. Mixed-version
  membership changes and rolling downgrade after edits are unsupported; recover
  an older release only from a compatible pre-feature backup. The ID-set proof
  is not a build-attestation or rolling-upgrade protocol.
- Restore maintenance stops/joins the repair worker before storage replacement
  and restarts it with empty process cursors after activation. The restore epoch
  is Controller-owned and is not rolled back with message metadata.

## Validation evidence

The [review and performance report](../reports/2026-09-13-message-updates-review-performance.md)
records the baseline comparison, fixed findings, commands and limitations.
On Apple M4 / Go 1.25.11, the same 256-Hash-Slot / 8-physical-Slot / 32-conversation
fixture showed the original noop implementation adding 64/512/1,024 Slot commits
per 64 history/list/sync reads. ReadIndex removed these read-induced commits.
Single-concurrency unedited history/list/sync p95 was approximately
0.12/1.24/3.75 ms after optimization. These are in-process HTTP handler samples,
not network RTT or production capacity.

Focused checks cover CAS/idempotency, index movement/paging, restore admission,
final page-byte accounting and continuation, durable snapshots/offline transfer,
long subscriber identities, worker fairness, and 100,000-recipient body-free
paging with fake routing. Real clusters test HTTP flows, leadership transfer,
restart, ReadIndex quorum loss, and unchanged original log data.

Before production rollout, run the existing capacity workflow against the exact
candidate: 256 Hash Slots, cross-node conversation pages, sparse/dense edits,
large payloads, reconnect bursts and real 100,000-member online groups. Compare
throughput, p95/p99, CPU/allocations, disk/Raft latency and queue/rejection rates
with the baseline. Local samples and fake-recipient tests do not validate those
workloads or set a safe SDK polling interval.

## Retryable failures on existing reads

`POST /messages`, single-channel `POST /channel/messagesync`,
`POST /conversation/list` and `POST /conversation/sync` now return known temporary
network, authority and admission failures as:

```http
HTTP/1.1 503 Service Unavailable
Content-Type: application/json

{"status":503,"code":"unavailable","msg":"temporarily unavailable"}
```

The `msg` and `status` fields remain present for legacy clients; successful
response shapes are unchanged. This intentionally changes some former 400/500
failures to 503. Parameter validation, membership/visibility denial, missing
messages and unknown failures do not automatically become retryable. Edit
overlay reads revalidate Slot mapping and authority; changes return a dedicated
retryable read-route cause. Database/CAS conflicts retain their own semantics. CMD and
batch-sync response contracts are outside this change.

For 503 `unavailable` or a connection failure/timeout, retain the exact read
request, cursor and cache. Retry with bounded exponential backoff and jitter;
cancel retries when leaving the channel. Do not advance a cursor, clear messages
or infer end-of-history from failure. A new request can have a new timeout while
preserving its query. Never retry every HTTP 400 or parse `msg` to infer authority.
An uncertain `/message/update` still requires the identical request ID, expected
version, content epoch and payload; this read policy does not relax CAS conflicts.
