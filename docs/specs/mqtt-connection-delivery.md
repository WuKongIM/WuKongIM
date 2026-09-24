# MQTT connection delivery handoff

Source: `5e3de162c6bdcfce9c1c579b64d1190732405ec5`. Applicable instruction
and FLOW digests are frozen in `/tmp/mqtt-connection-delivery-source.json`.

## Contract and failure inventory before implementation

The gateway has already enqueued CONNACK when it calls OnSessionOpen. That
callback releases its handshake operation before registering one bound sink
with the app-owned DeliveryCoordinator and Deliveries runtime. Registration
uses the connection context and acquisition timeout; it adds no worker, body
queue or retry loop. Runtime owns the registered task until its usecase finishes
terminal cleanup or joined shutdown. Registration requires durable ACK support.

Failure cases to cover through the public entry and real gateway composition:

- Rejected CONNECT, failed reply, rollback, cancellation or expired ownership
  must not register a task; repeated open cannot replace an existing stream.
- Registration must see the exact accepted Connection and usable sink after
  handshake release. It may enqueue immediately after CONNACK without waiting
  for a first inbound packet or holding the connection mutex.
- Registration error, panic, cancellation or synchronous close must fail open
  and enqueue the existing exact-owner disconnect cleanup. A task accepted
  before a late error remains runtime-owned and receives a close wake.
- Close records the first normal/abnormal intent before fencing and waking;
  neither close nor a wake discards pending End continuation or proves isolation.
- A matching PUBACK wakes only after durable completion and local credit
  release. Unknown/duplicate PUBACK adds no credit or scheduling work; failed
  acknowledgement closes and preserves durable recovery.
- Wake is a nonblocking best-effort hint. Error, absence, stopped runtime or
  callback panic cannot undo a committed ACK or interrupt required close work;
  runtime idle polling supplies eventual discovery.
- Real single-node cluster coverage must use 256 hash Slots, independent Paho
  over TCP, automatic source discovery/accounting/sending, persisted PUBACK,
  takeover recovery and joined cleanup. Controlled subscription setup must be
  explicitly reported until the protocol subscription path is composed.

The access port accepts Connection plus the existing entry-neutral DeliverySink.
Only app pairs Coordinator.Open with runtime.Register; usecases import no gateway
types. Product admission remains pending the full approved MQTT design, including
subscription entry, inbox future sources, offline maintenance and restore safety.

## Validation and scope

- RED public entry contract: missing Deliveries port,
  `/tmp/mqtt-connection-delivery-red.log`.
- RED app composition: missing mqttConnectionDeliveries,
  `/tmp/mqtt-connection-delivery-app-red.log`.
- Whole entry race suite passed (2.423s):
  `GOWORK=off go test -race -p 2 ./internal/access/mqtt -count=1`,
  `/tmp/mqtt-connection-delivery-access-race.log`.
- Real single-node cluster / 256-hash-Slot Paho race integration passed (6.699s):
  `GOWORK=off go test -race -tags=integration -p 2 ./internal/app
  -run '^TestMQTTConnectionDeliveryPahoSingleNodeCluster$' -count=1 -timeout=2m -v`,
  `/tmp/mqtt-connection-delivery-paho-race.log`. Repeat the command to regenerate
  its `mqtt_connection_delivery_evidence` record. Source protection, replay,
  discovery, accounting, Sender, gateway and PUBACK commits are real. Subscription
  setup calls the real usecase directly; this is not process-level acceptance.
  A one-minute idle poll with sub-three-second progress verifies ACK/close wakes.
  Receive Maximum 1 keeps the second message pending until the first PUBACK;
  takeover preserves PacketID/content and sets DUP, then all charges reach zero.
- Existing real-gateway Paho race regression passed (8.420s):
  `GOWORK=off go test -race -tags=integration -p 2 ./internal/app
  -run '^TestMQTTGatewayPahoSingleNodeCluster$' -count=1 -timeout=2m -v`,
  `/tmp/mqtt-connection-delivery-existing-paho-race.log`. Authentication,
  failed-CONNACK rollback, normal/abnormal Will decisions, controlled outbound
  recovery, QoS 0 under full QoS 1 credit and full property mapping remain covered.
- FLOW render/check passed: 86 compliant, zero invalid, nine preexisting length
  warnings. Frozen source revision and all six governing digests were reverified;
  `git diff --check` passed. No table, RPC or configuration format changes.
- Registration tests cover callback error/panic/cancellation, synchronous sink
  close, owner fencing and full operation capacity after scheduling. Failed open
  retains supervisor cleanup; wake errors/panics cannot undo successful ACKs.

The initial integration fixture seeded group metadata/membership without a
Channel runtime and repeatedly received `db: not found` during preparation.
Creating the Channel through ordinary IM send makes the complete chain pass;
that pre-subscription message is excluded by the prepared boundary. Subscription
entry work must also cover a valid group before its first ordinary append, not
rely on this fixture's initialization. Full product listener, SUB/UNSUB, inbox
future admission, offline maintenance and restore/process acceptance remain open.
