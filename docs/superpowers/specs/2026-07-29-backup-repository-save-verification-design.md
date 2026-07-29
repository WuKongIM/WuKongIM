# Backup Repository Save and Verification Design

## Summary

Backup repository configuration persistence and repository connectivity
verification become two explicit operations.

Saving validates the request shape, encrypts replacement credentials, and
publishes the plan through Controller Raft without contacting the repository.
Testing operates only on the exact saved plan revision, performs the complete
repository and cluster-visibility probe, and then marks that repository
configuration verified with a revision-fenced Controller update.

An unverified repository cannot be enabled for scheduled backups or used by an
immediate backup. The Manager Web UI shows verification state next to the
repository controls and renders save/test failures directly below the action
buttons with specific, secret-safe provider details.

## Problem and Evidence

The current `PUT /manager/backups/plan` path does more than its name and Web UI
label imply. `ManagementService.Configure` opens the requested repository,
performs the cluster repository probe, and initializes repository identity
before it publishes the plan. Any connectivity or permission failure prevents
Controller publication.

The current error path then hides the reason twice:

1. `normalizeStoreAccessError` joins every repository failure with the single
   `ErrStoreUnreachable` sentinel.
2. the Manager handler returns the generic `backup_store_unreachable` response,
   and the Web page replaces even that response message with a generic localized
   sentence.

The form remains populated until navigation, but a browser refresh reloads the
last published Controller plan. Because the failed save never published a
plan, all repository fields return to their previous values or empty defaults.
The user therefore sees both a misleading save interaction and an unactionable
error.

Existing tests prove that Controller state round-trips plan data and that the
Web form can populate OSS fields from a successful dashboard response. They do
not cover a save/reload workflow, verification state, provider error
classification, or real OSS/COS endpoints.

## Goals

- Make "Save settings" persist repository configuration without contacting
  file storage, Alibaba Cloud OSS, Tencent Cloud COS, or generic S3.
- Keep encrypted object-store credentials reusable without returning plaintext
  or ciphertext to any Manager client.
- Track whether the exact saved repository configuration has passed the
  complete repository test.
- Prevent scheduled and immediate backups from using an unverified repository.
- Invalidate verification when any effective repository setting or credential
  changes, while preserving it for schedule-only or retention-only edits.
- Return stable, specific, secret-safe repository errors to the Manager Web UI.
- Display save/test results in the repository action area without requiring a
  scroll to the top of the page.
- Exercise the real WuKongIM repository provider against both OSS and COS.
- Preserve active plans created before verification metadata exists.

## Non-goals

- Creating or deleting OSS/COS buckets.
- Persisting plaintext credentials in Controller state, browser storage, logs,
  test output, or repository files.
- Supporting temporary security tokens or cloud instance-role credentials in
  this change.
- Redesigning archive, restore, retention, or backup execution beyond the
  verification admission gate.
- Adding a second draft-plan aggregate alongside the active plan.

## Repository Verification Model

`backupcontract.Plan` gains an optional repository verification record:

```go
type RepositoryVerification struct {
    Status               RepositoryVerificationStatus `json:"status"`
    VerifiedAtUnixMillis int64                        `json:"verified_at_unix_ms,omitempty"`
}

const (
    RepositoryVerificationUnverified RepositoryVerificationStatus = "unverified"
    RepositoryVerificationVerified   RepositoryVerificationStatus = "verified"
)
```

The record is optional for backward compatibility:

- `nil` means a legacy published plan. Legacy plans are admitted as verified
  because the old configure path could publish them only after a successful
  repository probe.
- every plan saved through the new API receives an explicit record;
- `"unverified"` has no verification time;
- `"verified"` has a positive UTC Unix-millisecond verification time.

The Controller backup plan DTO stores the same optional record. State
conversion and clone functions copy it without aliasing.

The verification record is preserved only when the effective repository is
unchanged. Effective repository equality includes:

- store kind;
- normalized endpoint;
- normalized region;
- normalized bucket;
- normalized prefix;
- path-style setting;
- credential revision.

Supplying a replacement access/secret key pair increments credential revision
and invalidates verification even if the non-secret fields do not change.
Leaving both credential inputs empty for the same object-store kind reuses the
stored ciphertext and credential revision. Switching object-store kind requires
a new credential pair.

Changes to enabled state, Cron, time zone, retention count, rate limit, workers,
or maximum duration do not invalidate a verified repository.

## Save Workflow

`PUT /manager/backups/plan` remains the only plan replacement endpoint.

The request keeps its existing plan fields and expected revision. The save
workflow is:

1. validate HTTP bounds and repository shape;
2. load current Controller state;
3. normalize all repository fields;
4. encrypt a replacement credential pair or reuse the same-kind stored pair;
5. compare the effective repository with the current plan;
6. preserve verification only when the effective repository is unchanged,
   otherwise set it to unverified;
7. reject `enabled=true` when the resulting repository is explicitly
   unverified;
8. publish the complete plan with the existing Controller compare-and-swap;
9. return the saved plan with ciphertext removed and
   `credentials_configured` represented separately.

The save workflow does not open the repository, perform a network call, run a
cluster probe, create repository metadata, or list archives.

The Web form turns automatic backup off when the user changes the repository
kind or any repository field. It explains that repository testing is required
before automatic or immediate backup can be enabled. The backend admission
check remains authoritative for non-Web callers and races.

## Test Workflow

`POST /manager/backups/repository/test` changes from accepting a complete
unsaved plan to accepting the exact saved plan revision:

```json
{
  "expected_plan_revision": 4
}
```

The workflow is:

1. load the current Controller plan;
2. reject a missing plan or a plan revision different from
   `expected_plan_revision`;
3. open the repository from the saved, encrypted plan;
4. generate a unique probe prefix;
5. write a coordinator marker;
6. ask every active data node to open the same saved repository, read and
   checksum the marker, and write its node receipt;
7. read and checksum every node receipt at the coordinator;
8. list the probe prefix so list permission and shared visibility are proven;
9. delete the probe prefix and fail the test if cleanup permission is missing;
10. initialize or validate the cluster-bound repository identity;
11. re-read Controller state and require the same plan revision;
12. publish `status=verified` and the current verification time through
    compare-and-swap.

The cleanup path uses a bounded context independent of a canceled HTTP request.
It attempts cleanup after every partially successful test. A cleanup error is a
test failure because backup retention also requires deletion permission.

If the plan changes during the probe, the final update returns a state-conflict
response and never marks the new configuration verified.

## Backup Admission

Verification is checked at more than one boundary:

- `Configure` rejects enabling an explicitly unverified plan.
- `StartBackup` rejects immediate backup for an explicitly unverified plan.
- scheduled evaluation does not admit a job for an explicitly unverified plan.
- job execution verifies its referenced plan is still admissible before opening
  the repository.

The stable domain error is `ErrRepositoryUnverified`. Manager maps it to HTTP
409 with `backup_repository_unverified`.

Legacy plans with no verification record remain admitted. The next effective
repository change converts them to an explicit unverified plan.

## Repository Error Model

Infrastructure keeps the original error chain and classifies repository access
failures into a typed, secret-safe error:

```go
type RepositoryAccessError struct {
    Reason       RepositoryAccessReason
    Stage        RepositoryAccessStage
    Provider     backupcontract.StoreKind
    ProviderCode string
    RequestID    string
    NodeID       uint64
    Cause        error
}
```

Stable reasons are:

- `invalid_access_key`;
- `signature_mismatch`;
- `access_denied`;
- `bucket_not_found`;
- `region_mismatch`;
- `endpoint_unreachable`;
- `tls_failure`;
- `timeout`;
- `read_failed`;
- `write_failed`;
- `list_failed`;
- `delete_failed`;
- `repository_in_use`;
- `node_unreachable`;
- `unknown`.

Stable stages are:

- `open`;
- `write_marker`;
- `read_marker`;
- `write_receipt`;
- `read_receipt`;
- `list`;
- `delete`;
- `bind_identity`;
- `mark_verified`.

MinIO error responses are classified from their provider code and status.
Network errors are classified with `errors.Is`, `net.Error`, DNS errors, URL
errors, and TLS error types. The cloud response body is not forwarded
verbatim.

The Manager response is:

```json
{
  "error": "backup_repository_auth_failed",
  "message": "Alibaba Cloud OSS rejected the AccessKey ID.",
  "detail": {
    "provider": "oss",
    "stage": "write_marker",
    "reason": "invalid_access_key",
    "provider_code": "InvalidAccessKeyId",
    "request_id": "cloud-request-id",
    "node_id": 1
  }
}
```

The top-level error code is selected from the stable reason family, including
authentication, permission, bucket, region, endpoint, TLS, timeout, operation,
cluster-node, identity, and unknown failures. The response may include a cloud
request ID for support correlation. It never includes access keys, secrets,
ciphertext, authorization headers, signed URLs, or raw request bodies.

Manager audit records keep only stable error code, stage, provider, and node ID.
They do not record provider messages or credentials.

## Manager Web UI

The repository form displays one status badge:

- **Not tested** for an explicit unverified plan;
- **Verified** with the verification time for a verified plan;
- **Verified before upgrade** for a legacy plan with no record.

The form tracks whether its effective repository fields differ from the loaded
plan. A non-empty access-key or secret-key input also counts as an unsaved
repository change. "Test storage" is disabled while repository changes are
unsaved and the adjacent explanation says to save first. The test request
contains only the loaded plan revision; it never resends plaintext credentials.

Changing any repository field:

- clears the current verification presentation to "Not tested";
- switches automatic backup off in the draft;
- keeps the current typed input in the form;
- does not write credentials to local or session storage.

After save, the page reloads the Controller dashboard and repopulates endpoint,
region, bucket, prefix, and path style. Access and secret fields remain empty,
with the existing "leave blank to keep stored credentials" placeholder when
credentials are configured.

Plan save/test feedback is rendered immediately below the plan action row:

- the container uses `role="alert"` for failures and an `aria-live` region for
  completion;
- failures show the operation stage, localized reason, provider error code,
  request ID, and node ID when present;
- successes appear in the same location;
- repository save/test feedback is not duplicated in the page-top mutation
  banner;
- the page does not force a scroll or move keyboard focus away from the action
  area.

Other page-level load failures and unrelated archive/restore operations keep
their existing presentation unless their own component already has a closer
error surface.

## Real Cloud Integration

Integration tests use the production `RepositoryProvider`, credential cipher,
endpoint derivation, virtual-host addressing, and archive-store operations.
They run only with the `integration` build tag and provider-specific environment
variables.

Alibaba Cloud OSS variables:

- `WK_TEST_OSS_REGION`;
- `WK_TEST_OSS_BUCKET`;
- `WK_TEST_OSS_ACCESS_KEY_ID`;
- `WK_TEST_OSS_ACCESS_KEY_SECRET`;
- optional `WK_TEST_OSS_ENDPOINT`.

Tencent Cloud COS variables:

- `WK_TEST_COS_REGION`;
- `WK_TEST_COS_BUCKET`, including APPID;
- `WK_TEST_COS_SECRET_ID`;
- `WK_TEST_COS_SECRET_KEY`;
- optional `WK_TEST_COS_ENDPOINT`.

Each provider test:

1. derives the normal public endpoint when no override is supplied;
2. creates a unique `wukongim-integration/<provider>/<timestamp-random>`
   prefix;
3. writes an exact-size probe object with create-only semantics;
4. reads and verifies bytes and metadata;
5. lists the prefix and finds exactly the probe object;
6. deletes the object;
7. verifies the deleted object is absent;
8. runs bounded best-effort cleanup in `t.Cleanup`.

The test does not create or delete a bucket. Credentials need only scoped
read/write/list/delete permission for the temporary integration prefix. Test
code does not print environment values.

A single-node cluster integration path then exercises:

1. save an OSS/COS plan;
2. reload it and verify non-sensitive fields persist;
3. confirm automatic and immediate backup admission is blocked;
4. call the Manager repository-test endpoint;
5. reload and confirm the saved plan is verified;
6. enable the plan;
7. clean the integration prefix.

Single-node deployment remains a single-node cluster. Deterministic multi-node
tests cover marker/receipt fanout, exact failing node reporting, plan-revision
races, and cleanup failures without requiring multiple billable cloud nodes.

## Test Strategy

Implementation follows red-green-refactor cycles.

Backend tests cover:

- Controller conversion and cloning of nil, unverified, and verified metadata;
- legacy plan admission;
- save without repository calls;
- credential reuse and replacement;
- repository equality and verification invalidation;
- unverified configure/start/schedule gates;
- exact-revision test success and stale-revision conflict;
- cleanup failure preventing verification;
- MinIO provider-code and network-error classification;
- stable Manager JSON and secret redaction;
- dashboard projection after save and verification.

Frontend tests cover:

- save followed by dashboard reload repopulating OSS/COS fields;
- secret inputs remaining empty with reuse guidance;
- repository edits disabling automatic backup and invalidating displayed state;
- test disabled for unsaved repository edits;
- exact saved revision in the test request;
- inline specific error and success messages near the buttons;
- no generic replacement of server detail;
- no duplicate repository error at the top of the page.

Verification commands include focused Go tests, the backup page Vitest suite,
the Web TypeScript build, and provider-tagged integration tests when their
credentials are configured.

## Documentation Impact

The implementation updates:

- `internal/contracts/backup/FLOW.md` for verification metadata;
- `internal/usecase/backup/FLOW.md` for separate save/test and admission;
- `internal/infra/backup/FLOW.md` for provider error classification and probe
  stages;
- `internal/access/manager/FLOW.md` for the test request/response contract;
- `internal/app/FLOW.md` for the composed verification path;
- `docs/development/PROJECT_KNOWLEDGE.md` with the stable rule that saved
  repository configuration is distinct from verified backup admission.
