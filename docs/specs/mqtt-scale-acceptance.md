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
per-phase durations, end-to-end delivery latency p50/p99/max (SEND ack to last
subscriber receipt), duplicate/missing/out-of-order counts and the retired
counter delta. No credentials or payloads.
The artifact also records a ten-second post-fanout idle barrier window. The
quiet cost must stay below ten successful Slot barriers per subscriber per
second; this is an aggregate node measurement including maintenance, not a
claim that each read comes from a particular caller.
