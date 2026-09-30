# MQTT group scale acceptance

A black-box process scenario on a single-node cluster with 256 hash Slots and
twelve initial physical Raft groups, matching the product default. It checks
that MQTT group consumers stay correct and bounded when the group has
100,000 members, and that subscription churn creates retirable tombstones
instead of unbounded metadata. It is one part of full process/load acceptance.
It makes no capacity claim beyond the recorded sizes.

## Shape

- Group `scale-group`: 100,000 members, provisioned through public HTTP in
  batches of 5,000 (`/channel`, then `/channel/subscriber_add`).
- K members (default 500, `WK_E2E_MQTT_SCALE_CONNECTIONS`) connect with Paho
  as persistent Sessions and subscribe to the group topic at QoS 1. The rest
  are offline members, so fanout still resolves the full member set.
- One WKProto member sends M messages (default 20). Every MQTT subscriber must
  receive each one exactly once, in `wk.message_seq` order, carrying the SEND
  acknowledgement's message id.
- Churn: C subscribers (default 200) run R rounds (default 3) of UNSUBSCRIBE
  then SUBSCRIBE on the same topic, while staying connected. A final message
  must still reach every subscriber once.
- The fixed `wukongim_mqtt_consumer_events_total{event="retired"}` counter must
  eventually reach at least C×R (live-session tombstone retirement).

## Failure inventory before implementation

1. Provisioning or SUBSCRIBE of a 100k-member group times out or is refused;
   cold admission scans the whole member set per subscriber.
2. A subscriber misses, duplicates or reorders a message; message identity
   differs from the SEND acknowledgement.
3. Parallel CONNECT/SUBSCRIBE of K clients hits owner or connection limits,
   or deadlocks behind one slow consumer.
4. After churn, a resubscribed client receives backlog sent while it was
   unsubscribed, or misses the post-churn message.
5. Churn tombstones are never retired (counter stays below C×R), which
   means metadata grows with churn.
6. The run passes without doing the work: fewer connections, members or
   messages than configured are silently accepted. The artifact records the
   observed counts, and the test asserts them.

## Artifact

`mqtt-scale.json` in `WK_E2E_MQTT_REPORT_DIR`: configured and observed sizes,
per-phase durations, end-to-end delivery latency p50/p99/max (SEND submission to last
subscriber receipt), duplicate/missing/out-of-order counts and the retired
counter delta. No credentials or payloads.
The artifact also records a ten-second post-fanout idle barrier window. The
quiet cost must stay below ten successful Slot barriers per subscriber per
second; this is an aggregate node measurement including maintenance, not a
claim that each read comes from a particular caller.

## Preparation regression

The opt-in `TestGroupMembershipProvisioningBudget` isolates the public HTTP
setup deadline at 10,000 members and a 30-second preparation budget. It retains
256 logical Hash Slots and twelve initial physical groups, checks the exact
public projected-row delta and three sampled committed conversation views, and
records bounded per-batch timing/proposal counts in `mqtt-membership.json`.
It emits a failed artifact if preparation times out; a timed-out batch may have
partial durable effects. Profile-enabled attempts are diagnostic measurements
only. See [ordinary membership proposal scheduling](ordinary-membership-proposal-scheduling.md)
for the failure inventory, baseline and unprofiled validation.

## Long-lived producer and diagnostics

The WKProto publisher keeps its original authenticated connection alive with a
real PING/PONG every fifteen seconds during the long fanout/churn/retirement
windows. Gateway defaults to three minutes of inbound inactivity; outbound
traffic cannot refresh this deadline. The joined heartbeat loop reports failures
and never reconnects or changes publication identity. Successful artifacts
record the heartbeat count.

The workload logs completed phase durations and writes `mqtt-scale-failure.json`
with its failed phase, configuration and confirmed member count on a workload
failure. Startup diagnostics remain owned by the process harness.
After successful initial subscription, a failed workload also records a bounded
receipt histogram and independent Paho closed/incomplete counts in
`mqtt-scale-delivery-failure.json`, then emits the harness process dump before
cleanup. A closed incomplete persistent client ends the wait as a failure: it
cannot receive the required future publication. Open clients keep the original
three-minute receipt bound, and all successful receipt/identity/churn assertions
remain unchanged.
`WK_E2E_MQTT_SCALE_PROFILE=1` enables only loopback profiling for diagnostic
reproductions, and the artifact records that flag. Acceptance runs leave it off.

A diagnostic full run at the eight-worker membership revision prepared 100,000
members in 122.664s, subscribed 500 clients in 13.088s, reached all initial twenty
publication receipts in 174.186s and completed 600 subscription churn operations
in 19.374s. The quiet barrier rate was 2,025.9/s (4.05/subscriber/s). The sender
then reported disconnected after more than three minutes without inbound
activity, so post-churn delivery and retirement were not accepted.
[Failed diagnostic artifact](../reports/mqtt-scale-profile-sender-failure.json).
It is not a capacity or complete scale pass. The previously observed intermittent
cold SUBSCRIBE failure did not recur in this run; its repair remains unproven.

The unprofiled full reproduction with producer heartbeats reached all initial
receipts in 142.207s, then failed at round 2 resubscription: twelve
`subscribe/conflict` closures. No post-churn delivery or retirement acceptance
is claimed. [Failed artifact](../reports/mqtt-scale-churn-conflict-failure.json).
A separate 2,000-member/500-connection/two-message run passed every receipt and
all 600 retirements, recording five producer heartbeats and no profiling.
[Reduced delivery/full churn artifact](../reports/mqtt-scale-churn-600.json).
It qualifies only the recorded workload and does not replace full acceptance.

After the independently proved final-completion CAS fix, the unprofiled full
run at `bc41096de` prepared all members in 129.668s, completed 500 subscriptions
in 14.821s and reached all initial receipt counts in 166.175s. Quiet barriers
were 1,835.4/s (3.67/subscriber/s). Round 0 resubscription still produced three
conflict closures. Full scale acceptance is incomplete; this does not prove
the origin of every process conflict. [Failure artifact](../reports/mqtt-scale-completion-failure.json),
[command and exact binary provenance](../reports/mqtt-scale-completion-provenance.json).

A fixed-position diagnostic build of the same revision also passed the reduced
2,000-member/two-message workload with 500 clients and all 600 retirements. A
separate twenty-message diagnostic attempt stopped at the unchanged three-minute
receipt wait. [Probe bounds](../reports/mqtt-stage-diagnostic-evidence.json).
These measurements do not replace the required full workload or establish the
origin of the remaining subscription closures.

After the four independently reproduced churn repairs at `dd8e49bb9`, the
ordinary in-flight probe passes all 500 fresh receipts and 600 retirements with
zero subscription closure observations. The original unprofiled full workload
then confirms 100,000 members in 123.791s and all 500 subscriptions in 13.403s,
but exhausts its unchanged three-minute initial receipt wait. Public owner gauges
show 497 active/held owners; closure origin was not captured and churn was not
reached. [Failed full artifact](../reports/mqtt-scale-churn-final-failure.json),
[exact source/binary/command provenance](../reports/mqtt-scale-churn-final-provenance.json).
Full acceptance remains incomplete.

A temporary terminal-error overlay with 2,000 members and the same 500 clients
and twenty messages reaches every initial receipt in 148.785s, then fails on
one unsubscribe/conflict. A two-message/three-round probe passes, while increasing
only rounds to ten fails on round 8 resubscription with two subscribe/deadline
closures. These different failures do not identify or repair the full fanout
failure. [Diagnostic dimensions and artifacts](../reports/mqtt-fanout-closure-evidence.json).

The original-size terminal-boundary overlay subsequently repeats the fanout
timeout with all 500 clients open and 13–19 observed receipts each. This shows
the unchanged deadline can fail without a closed client, but leaves the earlier
three lost owners unexplained. [Boundary evidence](../reports/mqtt-full-dd8e-boundaries-provenance.json).
A bounded Accounting deferral experiment is not adopted because no measured
throughput benefit or complete passing comparison is established.
[Experiment](mqtt-accounted-delivery-experiment.md). Full scale acceptance,
intermittent unsubscribe/conflict and their remaining causes stay unresolved.
