# MQTT persistent Session product validation

## Failure inventory before implementation

1. A real product reconnect loses Session Present or subscriptions while isolated
   module tests pass; tests must not resubscribe or install storage fixtures.
2. QoS 1 delivery acknowledges automatically in the fixture, hiding a broken
   persistent exchange. Use explicit Paho manual PUBACK and Receive Maximum 1.
3. A resumed exchange changes Packet Identifier, payload or message identity,
   lacks DUP, or follows newly admitted backlog instead of preceding it.
4. A new contact sends its first person message while the receiver is offline,
   and the durable inbox misses that source despite a successful native SENDACK.
5. An old live owner remains usable after same-ClientID cross-node takeover,
   the new owner loses its unacknowledged exchange, or an unrelated UID can take
   over a bound ClientID with otherwise valid credentials.
6. PUBACK does not release exactly one durable window credit. A later publication
   must prove that earlier acknowledgement reached server admission.
7. A live-node reconnect pass is presented as proof of node-crash or partition
   recovery, history-cleanup safety, complete offline quotas or scale capacity.
   Those broader approved gates remain required.
8. Gateway shutdown destroys the transport loop before MQTT owners join their
   physical-close receipts. A live client must close on TERM, the process must
   exit successfully, and no lifecycle stop failure may be logged. Keep transport
   and business dependencies alive until MQTT cleanup actually joins.
9. A new process rejects a persistent session from a prior boot even after a
   successful graceful shutdown. A different BootID or elapsed lease alone is
   never isolation proof; restart acceptance needs explicit old-owner evidence.

Use real `cmd/wukongim` processes, 256 hash Slots, independent Paho clients,
WKProto SEND/SENDACK and trusted HTTP credential provisioning. Preserve topology,
message identities and explicit assertions as JSON without credentials or bodies.

## Verified implementation

The 256-hash-Slot one-node and three-node reconnect/takeover scenarios pass with
manual PUBACK and Receive Maximum 1. Subscriptions are never reinstalled; old
exchanges precede backlog and retain packet/message identity and DUP. A first
message from a new offline contact arrives after reconnect, and another UID's
valid credentials cannot take over the bound ClientID.

The separate single-node process restart preserves the unacknowledged exchange
through [proved graceful owner retirement](mqtt-owner-retirement.md). A focused
TERM scenario also proves successful process exit and no lifecycle-stop failure
with an active MQTT owner. Before the lifecycle fix that scenario was killed by
the harness; before retirement persistence the restarted client was rejected.

Reproduce with the command in the scenario's `AGENTS.md`; the frozen source,
governing digests, test results and public JSON assertions are preserved in
[the evidence artifact](../reports/mqtt-session-process.json). Crash/partition,
unknown effects, restore, complete offline cleanup and scale are still required.
