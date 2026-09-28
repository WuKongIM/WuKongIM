# MQTT crashed-boot retirement

Graceful shutdown persists a retirement receipt for its boot
([owner retirement](mqtt-owner-retirement.md)). SIGKILL, OOM or power loss
writes nothing, so a restarted node can never prove its previous boot quiesced
and every persistent Session it owned is refused (CONNACK 0x88) until expiry.

## Mechanism

1. Before the MQTT listener or any worker starts, `Retirements.Recover` takes an
   exclusive advisory lock on `<data>/mqtt/retired-owners/LOCK` and holds it
   for the process lifetime. A held lock proves no earlier process of this data
   directory still runs, so it cannot open sockets or run local owner work.
2. Under the lock, every `<hash>.started` marker whose boot has no receipt is a
   crashed boot. Recover mints a receipt for it with the connection bound
   `MaxUint64` (every ID it could have issued), fsyncs it, then removes the
   marker. Markers with a receipt are removed.
3. Recover then writes and fsyncs a `.started` marker for the current boot.
   Graceful `Record` removes the marker only after its receipt is durable.
4. The proof is node-local. Remote nodes still receive it only through the
   existing Quiesce RPC; partition or unreachable nodes are not covered.
   Late durable effects of the dead boot remain fenced by takeover's
   OwnerGeneration CAS, not by this receipt.

## Failure inventory before implementation

1. A still-running earlier process (double start) is declared crashed: the lock
   must be exclusive and held, and a second Recover must fail.
2. The current boot, a foreign node or a malformed/corrupt marker is retired.
3. A crash between receipt and marker removal, or between marker write and
   listener start, loses or duplicates a fact: every step is idempotent and
   receipts are never overwritten with different content.
4. A boot that never issued a connection has no receipt conflict with a later
   graceful receipt of the same boot.
5. Recover runs after the listener accepts connections, or Recover failure is
   ignored and MQTT still starts.
6. Unbounded directory scan: markers are one per boot and removed once proven.
