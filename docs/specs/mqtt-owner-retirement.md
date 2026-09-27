# MQTT graceful owner retirement

## Failure inventory before implementation

1. Fencing, lease expiry, an empty running registry or cancellation creates a
   false retirement proof while future reservations or effects remain possible.
2. Physical-close failure, admitted operations or uncertain remote effects are
   discarded to claim successful retirement. A proof requires terminal admission
   and zero retained owners; all product workers must join before persistence.
3. A receipt for another node/boot, a future connection ID, malformed or missing
   data authorizes takeover. Current-boot requests must always consult the live
   registry, with no persisted fallback on error.
4. Crash or interrupted receipt write leaves a partially accepted fact. Use one
   bounded, versioned, checksummed immutable file per boot, atomically published
   after file sync and followed by directory sync. Equal retries succeed;
   conflicting content never overwrites an existing fact.
5. Receipt reads allocate from an unbounded/corrupt length, scan every past boot,
   or retain one cache entry per owner. Use a hashed exact lookup and a 1 KiB cap.
6. Restore or abrupt process loss is mistaken for successful graceful shutdown.
   Missing proof remains unavailable; neither new BootID nor an offline row
   supplies proof. This mechanism does not establish uncertain-effect recovery.

## Contract

The owner runtime mints an opaque retirement value only after admission has
permanently stopped and all issued owners have proved physical closure and
operation completion. The value covers one NodeID, unique BootID and inclusive
maximum issued ConnectionID. IDs never wrap or reopen within that boot. Product
composition persists it only after its delivery, Will, deadline, replay and
connection workers have also joined. The Gateway loop remains alive until then.

The filesystem adapter stores these facts below the owning cluster data directory
at `mqtt/retired-owners/`. The file contains no UID, ClientID, payload or token.
The on-disk version starts at 1; unsupported versions and corrupt, conflicting,
missing or oversized files fail closed. No existing table/RPC encoding changes.
Pre-feature boots have no receipts and remain unavailable until another valid
fencing mechanism exists. Files are not expired by time: old session rows may
still reference them. Storage grows by one bounded file per graceful process
generation that issued any owner, never per connection; reads are point lookups.

An isolation adapter consults the live registry for its own boot and the receipt
store only for an older boot of that same node. It cannot activate/renew an old
owner or infer anything about another node. Existing exact-owner RPC transports
this result. Abrupt crash, partition, lost data, backup reactivation and uncertain
effects still need their own valid proofs and remain separate acceptance gates.
