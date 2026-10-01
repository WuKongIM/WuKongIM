# MQTT three-node Owner outage acceptance

The operator approved process-level acceptance of refused takeover while an
old Owner cannot be isolated, followed by persistent recovery after communication
or node restart. Reuse public MQTT 5, Product HTTP/WKProto, Manager Slot inventory,
fixed metrics and harness-owned process groups. Do not inspect MQTT tables.

## Failure inventory before implementation

1. An unavailable node, elapsed lease or failed RPC is mistaken for old Owner
   isolation, admitting a second connection while the old process may still live.
2. A general quorum/admission outage masquerades as successful isolation refusal.
   First prove surviving Slot quorum and successful unrelated MQTT CONNECTs on
   both other nodes; rejected target CONNECTs alone are insufficient attribution.
3. A failed CONNECT leaves a candidate that becomes active after communication
   returns. All rejected client sockets close, and fixed aggregate active-owner
   observations must contain no target admission on the surviving nodes.
4. Restarting the old node creates a foreign or incomplete boot proof; remote
   reconnect must wait for actual readiness and exact-owner isolation, never
   infer isolation solely from SIGKILL or a new process ID.
5. Recovery resets the Session, silently reinstalls subscriptions, changes the
   original Packet Identifier/content identity/order or omits DUP on replay.
6. A late old socket/callback modifies the successor or consumes its unfinished
   exchange. Successful handoff closes the prior client before the next exchange;
   a second cross-node handoff retains that same unacknowledged exchange.
7. Recovery completes the old exchange but future delivery stops, duplicates or
   reorders. A fresh native publication arrives once, with its original identity.
8. A stopped process remains suspended during failed-test cleanup, or restart
   reuses its data/ports before the previous process group has fully joined.
9. Reports claim success before cleanup, include secrets/payloads, omit failure
   phase or qualify a network partition/full scale that was never exercised.

## Scenario

Use a real three-node cluster, 256 hash Slots, three Slot replicas and two Channel
replicas. The latter permits independent admission with two healthy nodes;
existing source replication is still required and no publication is issued
during the outage. Create one persistent inbox Session on node 3, with manual
QoS 1 PUBACK and Receive Maximum 1, and leave one native message unacknowledged.

Run independent SIGSTOP/SIGCONT and SIGKILL/restart cases. Refuse one target
CONNECT through each surviving ingress shortly after loss, then again after
at least 35 seconds from the signal. The product's captured grant is 30 seconds;
this necessary real elapsed-time window distinguishes expiry from isolation.
Before the later refusals, require agreed live Slot leadership/quorum through
both surviving public inventories and successful independent CONNECT/close.
No failed target CONNECT is automatically retried or changed into a success.

Resume the same process or join the dead process group and restart the same node
spec. Await public cluster and Slot convergence, then reconnect once through
node 1 and take over once through node 2 without SUBSCRIBE. Both receipts must
preserve Session Present, Packet Identifier, DUP and original message identity.
The preceding client must be closed. ACK on the final owner, publish one fresh
native message and observe no extra delivery. Fixed metrics are diagnostics,
not isolation proof. Write bounded success/failure JSON after all cleanup.

SIGSTOP models an unavailable living process; it is not a complete network
partition, asymmetric-link or uncertain remote-effect qualification. The test
adds no product recovery mechanism and grants no time-based takeover authority.
