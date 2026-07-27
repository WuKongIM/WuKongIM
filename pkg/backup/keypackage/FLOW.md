# Backup deployment key package flow

`keypackage` is the provider-neutral deployment trust-root module shared by
the application composition root and `wkcli`. It does not depend on WuKongIM
runtime or infrastructure packages.

1. `wkcli backup keys bootstrap` creates one package bound to a repository ID.
   The package contains an independent HMAC-SHA256 integrity key, one active
   AES-256 wrapping key, and one active Ed25519 signing seed.
2. The runtime discovers the fixed protected credential, rejects unstable or
   non-private files, authenticates every package field, validates
   material-derived key IDs, and proves an envelope/signature round trip.
3. `RepositoryPinnedAuthority.Check` establishes the package ID root in both
   immutable repositories at revision one. It also verifies the signed chain
   of odd activated revisions and rejects package substitution or rollback.
   Only the configured lowest-ID Controller voter may publish a missing pin;
   all other nodes return a retryable pending result and perform read-only
   verification. The implicit single-node cluster normalizes to its local
   voter; seed-join mirrors have no admitted voter identity and therefore never
   publish. The publisher identity remains stable across Raft terms.
   Runtime crypto stays closed until the external Doctor explicitly qualifies
   the authority after all repository, staging, and UTC checks succeed.
4. Rotation staging creates the next even revision with one pending wrapping
   and signing pair. It does not advance repository freshness.
5. Rotation activation creates the next odd revision, retains old wrapping
   material and signing public keys, removes the old signing seed, and
   publishes the new signed activation pin in both repositories.
6. Each recovery kit encrypts one exact package revision with an independent
   AES-256-GCM recovery key. Repository pins reject a valid but superseded kit.

Repository pins contain only public identity, revision, linkage, and signature
data. They are permanent control objects outside generation garbage
collection.
