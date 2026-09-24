# MQTT source preparation before the first message

Source revision `0f37f38acd17868a4fd64da75dd2daa380b1ecb4`; applicable AGENTS/FLOW
SHA-256 digests frozen in `/tmp/mqtt-empty-group-source.json` before edits.

## Failure inventory before implementation

The real connection-delivery integration exposed a valid group whose business
metadata and membership existed but whose Channel runtime had never been created.
Source preparation repeatedly returned `db: not found`. Subscription must not
require a first ordinary message merely to initialize infrastructure.

- An authorized empty group must create its runtime through the existing hosted
  Channel service and Slot-authoritative bounded/coalesced metadata creation.
  It must then complete replicated protection and real replay confirmation.
- Explicit fresh runtime absence is the only creation trigger. Unavailability,
  cancellation, invalid identity and malformed existing metadata must not create.
- Creation errors may have committed: return the error, without retry, source
  protection, cursor admission or success. A later ordinary call can reread.
- The creation result may use cached routing. It is initialization only; reread
  fresh authority before forming the fenced source-protection request. Never
  trust cached epochs, fabricate metadata, or keep retrying an absent reread.
- Cancellation after creation/reread prevents subsequent effects and message-ID
  allocation. Missing or malformed reread evidence cannot protect a source.
- No IM group, UID membership, user payload or second per-channel worker is added.
  Existing usecase permission gates precede this infrastructure path; denied
  subscription must leave runtime metadata absent.
- Real single-node cluster/Paho coverage uses 256 hash Slots, prepares before any
  business append, then observes automatic delivery, same-PacketID takeover,
  PUBACK credit wake and cleanup. Existing nonempty-source behavior still passes.

The SourceProtector adapter may initialize Channel runtime infrastructure only
on authoritative absence; it cannot create business metadata or change policy.
This reuses Node.ResolveChannelAppendAuthority and its existing lifecycle and
capacity owner. Shared protection still uses a separately refreshed Slot view.
Product SUB/UNSUB, offline/inbox maintenance and process acceptance remain required.

## Evidence

- Real empty-group/Paho RED reproduced `db: not found` during subscription
  preparation: `/tmp/mqtt-empty-group-red.log` (16.152s).
- Adapter RED required initialization and a second authoritative read; old code
  stopped after its first read: `/tmp/mqtt-empty-group-adapter-red.log`.
- Adapter race coverage passed (1.789s),
  `/tmp/mqtt-empty-group-adapter-race.log`:
  `GOWORK=off go test -race -p 2 ./internal/infra/cluster
  -run '^TestMQTTSourceAdapter' -count=1`.
- Empty and nonempty real-gateway race integrations passed together (10.776s),
  `/tmp/mqtt-empty-group-paho-race.log`:
  `GOWORK=off go test -race -tags=integration -p 2 ./internal/app
  -run '^TestMQTT(EmptyGroupSubscription|ConnectionDelivery)PahoSingleNodeCluster$'
  -count=1 -timeout=2m -v`. Rerun to regenerate both
  `mqtt_connection_delivery_evidence` records, with `initially_empty=true/false`.
  The empty case proves a denied group's runtime remains absent and an allowed
  group establishes protection/replay without a business append, followed by
  real automatic sending and takeover/ACK/cleanup.
