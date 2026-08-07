# Cloud Lease Control Flow

`internal/usecase/cloudlease` owns the provider-neutral lifecycle of temporary
cloud infrastructure. It contains no WuKongIM deployment, Slot, worker,
channel, or workload policy.

```text
trusted CLI / orchestrator
  -> Controller
     -> validate and normalize strict Lease Plan v1
     -> compute immutable Plan digest and mandatory base tags
     -> Provider.Quote (read-only price, quota, and capacity)
     -> Provider.Inspect before Acquire (idempotency and conflict detection)
     -> Provider.Acquire
     -> Provider.Inspect / GrantAccess / RevokeAccess
     -> Provider.Release (success only with an exact zero-inventory proof)
     -> Provider.List + Sweep (expired access and Lease reconciliation)
```

`Quote` never mutates provider state. Admission includes the cost already
committed by a caller: the new estimate must fit inside the remaining aggregate
Budget. A Plan may declare a conservative complete-Lease public-egress byte
ceiling; it is invalid unless at least one host requests a public IPv4 address.

`Acquire` first inspects the exact Lease identity. `AcquireWithBootstrap` also
accepts up to eight distinct Ed25519 public keys, normalizes them, and adds only
their SHA-256 set digest to every resource tag; public key text and all private
material are absent from Receipts. Its idempotency tuple is
repository, request, Lease, and Plan digest: a Request may intentionally group
sequential stage-specific Leases, while one Lease identity can never be reused
for a different Plan. A matching active Receipt is an idempotent success and a
different Plan is a conflict. After an ambiguous provider error, the Controller
inspects inventory again; matching active inventory is recovered as success,
while partial inventory is returned as `acquire incomplete` and must be released.

Every Receipt is non-secret and binds Lease, request, repository, provider,
region, Plan digest, immutable expiry, Quote, resource inventory, and access
grants. Applicable source SHA and bundle-digest provenance are typed and
repeated as reserved tags. Every resource repeats the complete Lease tags and
its logical resource role.
Provider-free consumers may call `ValidateReceipt` to recheck a persisted
Receipt against its own selector identity without receiving any Quote,
Acquire, access-rule, Release, or Sweep capability.

`GrantAccess` and `RevokeAccess` operate on typed, expiring network rules.
An exact repeated grant and revocation of an absent grant are idempotent.

`Release` succeeds only when the provider returns an exact `ZeroInventoryProof`
covering its complete inventory scopes. Residual inventory returns a
`release_pending` Receipt plus `ErrResidualResources`; callers and `Sweep`
retry the same exact Lease rather than assuming a provider delete request
completed. A repeated Release remains valid after all tagged inventory has
disappeared and does not require a retained tombstone or workflow-local state.

`Sweep` deterministically revokes expired access on active Leases and releases
expired or cleanup-pending Leases. Provider inventory and mandatory tags are
the authority; workflow-local state is not.
