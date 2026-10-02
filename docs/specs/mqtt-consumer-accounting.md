# MQTT consumer accounting orchestration

One bounded maintenance turn accounts committed shared replay for an existing
active subscription/source binding and current Session lifetime. It runs for
online and offline Sessions without socket ownership, writes no packets, and
creates no inflight exchanges. Its only mutation is command 82 AccountQualified,
fenced by the captured complete owner, parent revision, subscription revision,
source generation and authorization version. A takeover/replacement races that
CAS; it never retries using a successor's identity in the same turn.

Read a pinned Session/cursor and an exactly matching Session/subscription view,
then the active source binding. Read fresh Channel placement and a committed
anchor; one typed original-content page is bounded by 256 source positions and
16 MiB. Runtime is O(page positions + bytes), with no retained body cache or
per-consumer worker/queue; a future bounded fair scheduler must cap concurrent
page memory independently of the number of group members. Validate full page
association and continuity. Recheck placement and
receive permission before the single accounting proposal. The caller supplies
only the exact cursor identity, never counts, content, current owner or source HW.

Qualified backlog is effective QoS 1 only. Native persistent publications default
to QoS 1; MQTT and Will retain original publication QoS. No Local compares stable
publisher namespace plus ClientID, never UID or connection generation. Explicit
native internal controls, expired messages and effective QoS 0 add no debt, but
their source coverage advances. Native Expire uses original append time; MQTT
Message Expiry uses ingress time and Will uses original append time. Both expiry
constraints apply when present. SyncOnce alone does not classify a control.
Nonpersistent live messages are absent from this durable source and keep their
separate online QoS 0 path. Pending counts include admitted inflight; a full
window cannot halt independent accounting. One fixed evaluation time identifies
all charged items in the stored range receipt.

Maintenance checks the persisted lifetime and its deadline; wall time alone does
not end a Session or prove owner isolation. A quota-ending command returns the
exact observed owner and persisted end result for lifecycle cleanup; it does not
claim that owner has quiesced. A missing anchor yields not-ready, not empty work.
An idle result means only the captured accepted prefix is accounted. Ordinary
source progress/retirement, owner shutdown, discovery/scheduling, window admission
and actual sends remain separate responsibilities. Product activation still
requires all of them. No schema, command or RPC change is needed in this slice.

## Failure inventory before implementation

- Native/MQTT/Will QoS or No Local is reinterpreted from UID/current socket;
  SyncOnce/payload spelling replaces native internal-control proof.
- Expiry is restarted on replay, absent/zero expiry is conflated, native expiry
  overflows, invalid metadata is treated as an ordinary publication.
- Online window fullness hides backlog; offline Sessions are skipped or expired
  lifetimes accrue debt; legacy unadmitted debt is guessed on upgrade.
- Cursor pending/inflight debt exceeds the parent Session totals.
- Missing/partial/foreign Session, cursor, subscription, binding, page or anchor
  becomes empty coverage. Source start/sequence gaps, wrong hashes/counters or
  transient overlays advance the cursor.
- Changed parent/owner/options/authorization/placement or closed binding is
  accepted; denied/infrastructure authorization errors cause silent skipping.
- Empty qualifying pages allocate receipts; short pages skip unread records;
  repeated calls double-charge; byte overflow/oversized work escapes bounds.
- Wall time regresses between page evaluation and proposal while remaining above
  the previous Session timestamp.
- Quota termination is mistaken for owner isolation, a failed or malformed commit
  reports success, callback panic leaks details, canceled work starts a mutation,
  or reply loss causes implicit unbounded retry.

Tests first at the approved public usecase + real metadata and app/Node seams;
process acceptance remains a separate required gate.

## Frozen context

Source `bf602b1da0daf3ddf6120ab13107a107b6a8699c`; SHA-256:

- `AGENTS.md`: `d1a79d1ca586c933ee11d984ff3c401e816fc09de13c635febb7fe4d57f50ade`
- `internal/usecase/mqttsession/FLOW.md`: `7a8d4be7ac4a8fa626fe5a6fd67ee33c60069fa7ddaf446bbd1f3788621acd4e`
- `internal/app/FLOW.md`: `8ce0a08f1e4b58075b7b7d878d93066c1e215a19de2ed269e048b1399b4ed187`
- `pkg/channel/FLOW.md`: `e1afae5bebc02c8af1228479ee5cc32686507fe969b6500c27380527de249b01`
- `pkg/protocol/publication/FLOW.md`: `c233c21b41ca6cfc2f4c34ffec50bba4eb099952591856b4f3233f231a2ec655`
