# Issue #977: permission-read baseline, before cohort implementation

This phase measures the existing product. It does not implement cross-caller
cohorts or qualify a new capacity limit. Keep the existing 500 SEND/s gate and
its acceptance thresholds unchanged. The earlier 4500 SEND/s failure remains
failed; a repeated barrier count alone cannot establish its CPU cause.

## Failure modes recorded before the test

1. Independent sessions may repeat identical UID/Channel facts. Count actual
   permission plans, envelopes, Slot groups and barrier completions, not only
   elapsed time. A same-connection burst can be gateway-batched and is therefore
   not evidence of cross-caller sharing.
2. Two Slots at one leader must still have two independent barriers. Confirm
   actual leadership and voters through all-node Manager convergence, rather
   than preferred leaders or local replica presence.
3. A non-replica ingress must reach current authority. Include one-replica
   metadata Slots only to establish this routing condition, not as an HA claim.
4. Late callers after a completed ban/unban must observe the new policy. Future
   cohorts must seal before issuing their barrier; never reuse a barrier for
   late arrivals or reuse completed mandatory facts as a cache entry.
5. Cancellation, stale routing, per-Slot errors and overload can poison a shared
   cohort or leak its budget. Future implementation needs independent deadlines,
   item alignment/UID precedence, a single failed-group route retry, joined work
   and bounded requests/facts/bytes/workers/waiting memory. This baseline does
   not claim to validate a cohort's cancellation or fault behavior.
6. A cumulative scrape is not a measured-window delta. Retain before/after
   per-node cuts, take setup and policy writes outside windows, join all SENDACKs,
   and check the topology fingerprint again after each case.
7. Missing resource samples must stay unknown. CPU/RSS gauges are periodic
   observations; Go allocation counters may be scrape-cached. Record these
   limits, profile separately, and do not attribute their whole delta to barriers.
8. Successful ACKs without exact complete history can hide lost or duplicate
   messages. Record all IDs and complete history, including rejected-message
   absence and successful controls before/after a completed Channel ban.

## Fixed experiment

Use three real processes, 256 Hash Slots and 12 physical metadata Slots with one
voter each; explicitly keep three message-data replicas. Use the
existing system UID to isolate its two mandatory facts from auxiliary membership
reads and recipient fanout. This fixture is deliberately narrower than ordinary
group traffic. Each of 32 distinct devices has a separate WKProto connection.

Select Channels by a bounded deterministic key search against public actual
leadership. Cases: one remote Slot; two distinct Slots at one remote leader;
two remote leaders; two distinct Slots at the ingress leader. Each case uses
64 sequential SENDs and the identical count in two waves of 32 callers. Each
connection has at most one outstanding SEND. There is no SEND retry.

Collect actual plan counts and successful/failed barrier histogram counts,
request/response bytes, per-node CPU/RSS/heap/allocation cuts and client-observed
SENDACK p99/max. Keep every response and exact history. Read history through
Product HTTP sync and its More/sequence continuation, using one offline group
subscriber so SEND has no online recipient fanout. Manager's singular message
scan uses node-local storage/metadata and is unsuitable for non-replica routing.
Do not assert that future
cohorts must retain today's linear counts: the test is a characterization and
can be updated only with a reviewed behavior change and frozen old evidence.

Optional bounded CPU/heap profiles run in a separate 256-SEND diagnostic phase
after unprofiled windows. Their timing is excluded from baseline latency.
Queue-owned bytes have no current public gauge and remain unknown, with the
documented 16 MiB/1024 waiting-envelope bound recorded separately.

Run the opt-in `TestPermissionCallerBaseline` in `test/e2e/message/send_ban`.
The JSON receipt includes exact product binary SHA-256/build metadata, harness
file SHA-256, fixed inputs, public placement, counter cuts, responses, resource
availability, history and pass/fail. Preserve failed receipts. This experiment
does not replace the unchanged Linux 500 SEND/s release gates.
