# Backup Repository Save and Verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist Alibaba Cloud OSS and Tencent Cloud COS backup settings independently from connectivity testing, require the exact saved repository to be verified before backup admission, and show specific secret-safe test failures beside the action buttons.

**Architecture:** Controller state remains the single durable backup-plan aggregate. Saving publishes normalized configuration and encrypted credentials with an explicit verification state but performs no repository I/O. Testing accepts only a saved plan revision, executes the production repository and cluster-visibility probe, classifies provider failures into a shared secret-safe contract, and compare-and-swaps that exact plan to verified. Backup admission checks the verification state at configure, manual start, schedule evaluation, and job execution boundaries.

**Tech Stack:** Go, Gin, Controller Raft state, MinIO Go SDK, React, TypeScript, React Intl, Vitest, Testing Library, Bun.

---

## Task 1: Make repository verification durable and accept OSS/COS in Controller state

**Files:**

- Create: `internal/contracts/backup/scheduled_test.go`
- Modify: `internal/contracts/backup/scheduled.go`
- Modify: `pkg/controller/state/scheduled_backup.go`
- Modify: `pkg/controller/state/state_test.go`
- Modify: `pkg/controller/fsm/scheduled_backup_test.go`
- Modify: `internal/infra/backup/scheduled_state_store.go`
- Modify: `internal/infra/backup/scheduled_state_store_test.go`

- [ ] **Step 1: Write failing contract clone tests**

Add table tests proving that `SystemState.Clone`:

- keeps a legacy plan's `RepositoryVerification` nil;
- deep-copies an explicit unverified record;
- deep-copies a verified record and its timestamp;
- does not alias the source verification pointer.

Use the public model:

```go
type RepositoryVerificationStatus string

const (
	RepositoryVerificationUnverified RepositoryVerificationStatus = "unverified"
	RepositoryVerificationVerified   RepositoryVerificationStatus = "verified"
)

type RepositoryVerification struct {
	Status               RepositoryVerificationStatus `json:"status"`
	VerifiedAtUnixMillis int64                        `json:"verified_at_unix_ms,omitempty"`
}
```

- [ ] **Step 2: Write failing Controller validation and round-trip tests**

Extend `pkg/controller/state/state_test.go` and `pkg/controller/fsm/scheduled_backup_test.go` to prove:

- `oss` and `cos` plans pass state validation with Region, Bucket, Prefix, and encrypted credentials;
- an unverified plan has zero verification time;
- a verified plan requires a positive verification time;
- an unknown verification status is rejected;
- the FSM replacement path retains the verification record.

Extend `internal/infra/backup/scheduled_state_store_test.go` so the existing detached round-trip includes:

```go
RepositoryVerification: &backupcontract.RepositoryVerification{
	Status:               backupcontract.RepositoryVerificationVerified,
	VerifiedAtUnixMillis: 1_800_000_000_500,
},
```

- [ ] **Step 3: Run the focused tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./internal/contracts/backup ./pkg/controller/state ./pkg/controller/fsm ./internal/infra/backup -run 'Verification|ScheduledBackup|ScheduledControllerStateStore' -count=1
```

Expected: compile or assertion failures because verification fields and Controller OSS/COS kinds are missing.

- [ ] **Step 4: Implement the durable model and clone behavior**

Add `RepositoryVerification *RepositoryVerification` to `backupcontract.Plan`. Update `SystemState.Clone` to copy the pointed-to value.

Add Controller equivalents:

```go
type BackupRepositoryVerificationStatus string

const (
	BackupStoreKindFile BackupStoreKind = "file"
	BackupStoreKindOSS  BackupStoreKind = "oss"
	BackupStoreKindCOS  BackupStoreKind = "cos"
	BackupStoreKindS3   BackupStoreKind = "s3"

	BackupRepositoryVerificationUnverified BackupRepositoryVerificationStatus =
		"unverified"
	BackupRepositoryVerificationVerified BackupRepositoryVerificationStatus =
		"verified"
)

type BackupRepositoryVerification struct {
	Status               BackupRepositoryVerificationStatus `json:"status"`
	VerifiedAtUnixMillis int64                              `json:"verified_at_unix_ms,omitempty"`
}
```

Add the optional pointer to `state.BackupPlan`, clone it in `ScheduledBackupState.Clone`, and validate the two explicit states. A nil pointer remains valid for legacy plans.

- [ ] **Step 5: Implement state-store conversion**

Map nil and non-nil verification records in both directions in `scheduled_state_store.go`. Copy the value instead of sharing a pointer.

- [ ] **Step 6: Run focused tests**

Run:

```bash
GOWORK=off go test ./internal/contracts/backup ./pkg/controller/state ./pkg/controller/fsm ./internal/infra/backup -run 'Verification|ScheduledBackup|ScheduledControllerStateStore' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/contracts/backup/scheduled.go internal/contracts/backup/scheduled_test.go pkg/controller/state/scheduled_backup.go pkg/controller/state/state_test.go pkg/controller/fsm/scheduled_backup_test.go internal/infra/backup/scheduled_state_store.go internal/infra/backup/scheduled_state_store_test.go
git commit -m "feat(backup): persist repository verification state"
```

## Task 2: Enforce verification at every backup admission boundary

**Files:**

- Modify: `internal/usecase/backup/errors.go`
- Modify: `internal/usecase/backup/scheduled_service.go`
- Modify: `internal/usecase/backup/scheduled_service_test.go`
- Modify: `internal/usecase/backup/job_runner.go`
- Modify: `internal/usecase/backup/job_runner_test.go`

- [ ] **Step 1: Write failing scheduled-service tests**

Add tests for:

- a legacy nil verification record remaining admissible;
- `Configure` rejecting `Enabled: true` with explicit unverified status;
- `StartBackup` rejecting an explicit unverified plan;
- `EvaluateSchedule` not admitting a job for an explicit unverified plan;
- `MarkRepositoryVerified` setting status and time only when `expectedPlanRevision` matches;
- a stale plan revision returning `ErrStateConflict`;
- a second verification call being idempotent for the same plan revision.

Use the stable error:

```go
var ErrRepositoryUnverified = errors.New(
	"backup usecase: repository is not verified",
)
```

- [ ] **Step 2: Write a failing runner safety test**

Seed an active job whose plan is explicitly unverified. Assert `RunOnce` does not call the repository provider and finishes or aborts the job with `repository_unverified`.

- [ ] **Step 3: Run the focused tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./internal/usecase/backup -run 'RepositoryVerified|RepositoryUnverified|MarkRepositoryVerified|JobRunner' -count=1
```

Expected: failures because the gate and verification mutation do not exist.

- [ ] **Step 4: Implement verification helpers and configure input**

Add:

```go
func repositoryIsVerified(plan backupcontract.Plan) bool {
	return plan.RepositoryVerification == nil ||
		plan.RepositoryVerification.Status ==
			backupcontract.RepositoryVerificationVerified
}
```

Extend `ConfigureRequest` with:

```go
RepositoryVerification *backupcontract.RepositoryVerification
```

Deep-copy it into the new plan. Reject only explicit unverified plans when enabling; nil keeps upgrade compatibility.

- [ ] **Step 5: Implement the revision-fenced verification mutation**

Add:

```go
func (s *ScheduledService) MarkRepositoryVerified(
	ctx context.Context,
	expectedPlanRevision uint64,
) (backupcontract.Plan, error)
```

Load state, require a plan and an exact plan revision, clone the state, increment the system revision, set verified status and `s.now().UTC().UnixMilli()`, and compare-and-swap. If the exact plan is already verified, return it without changing its original verification time. Do not increment `Plan.Revision`: it identifies the exact saved repository configuration that was tested.

- [ ] **Step 6: Add the remaining admission gates**

- `StartBackup` returns `ErrRepositoryUnverified` before job creation.
- `EvaluateSchedule` returns no admitted job for an explicit unverified plan.
- `JobRunner.RunOnce` checks verification before opening storage and aborts an impossible stale active job with `repository_unverified`.

- [ ] **Step 7: Run focused tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/backup -run 'RepositoryVerified|RepositoryUnverified|MarkRepositoryVerified|JobRunner' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/usecase/backup/errors.go internal/usecase/backup/scheduled_service.go internal/usecase/backup/scheduled_service_test.go internal/usecase/backup/job_runner.go internal/usecase/backup/job_runner_test.go
git commit -m "feat(backup): gate jobs on repository verification"
```

## Task 3: Separate plan save from exact-revision repository testing

**Files:**

- Modify: `internal/usecase/backup/management.go`
- Modify: `internal/usecase/backup/management_test.go`

- [ ] **Step 1: Replace old configure/probe expectations with failing save tests**

Add a recording provider and probe that fail the test if called during `Configure`. Prove:

- OSS and COS saves succeed without `Repository.Open`, `ProbeRepository`, or `EnsureRepository`;
- the saved non-secret fields survive a fresh `ScheduledService.State` load;
- a new repository is explicitly unverified and disabled;
- schedule-only changes preserve verified state;
- endpoint, region, bucket, prefix, path-style, kind, or credential revision changes invalidate verification;
- blank credentials reuse same-kind ciphertext and revision;
- replacement credentials increment revision without exposing ciphertext;
- switching object-store kind without a new complete credential pair is rejected;
- enabling the resulting unverified configuration returns `ErrRepositoryUnverified`.

- [ ] **Step 2: Write failing exact-revision test workflow tests**

Replace the old full-plan `TestRepository` request with:

```go
type TestRepositoryRequest struct {
	ExpectedPlanRevision uint64
}
```

Prove:

- missing plan returns `ErrDisabled`;
- a stale expected revision returns `ErrStateConflict` before opening storage;
- success opens the saved encrypted plan, probes it, binds repository identity, and marks it verified;
- a plan change between probe completion and the final mutation returns `ErrStateConflict`;
- a probe or identity failure leaves verification unverified.

- [ ] **Step 3: Run tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./internal/usecase/backup -run 'Management.*(Save|Configure|Repository|Revision|Verification|Credential)' -count=1
```

Expected: failures because save still performs repository I/O and testing still accepts an unsaved plan.

- [ ] **Step 4: Implement save-only configuration**

Keep validation, normalization, credential encryption/reuse, and Controller publication. Remove repository open/probe/identity calls from `ManagementService.Configure`.

Compute verification with an effective-store comparison over:

```go
func equalEffectiveRepository(
	current backupcontract.StoreConfig,
	next backupcontract.StoreConfig,
) bool {
	return current.Kind == next.Kind &&
		current.Endpoint == next.Endpoint &&
		current.Region == next.Region &&
		current.Bucket == next.Bucket &&
		current.Prefix == next.Prefix &&
		current.PathStyle == next.PathStyle &&
		current.CredentialRevision == next.CredentialRevision
}
```

Preserve a copied current verification only when this returns true. Otherwise publish explicit unverified status when the request is disabled, and let the backend return `ErrRepositoryUnverified` if any caller attempts to save that changed repository as enabled. The Web caller turns automatic backup off before submitting repository edits.

- [ ] **Step 5: Implement saved-plan testing**

Change `ManagementService.TestRepository` to:

1. load the saved plan;
2. compare `ExpectedPlanRevision`;
3. open using its stored config;
4. run the cluster probe;
5. call `backupartifact.EnsureRepository`;
6. call `ScheduledService.MarkRepositoryVerified`.

Return the verified plan so Manager can respond with current verification data:

```go
func (s *ManagementService) TestRepository(
	ctx context.Context,
	request TestRepositoryRequest,
) (backupcontract.Plan, error)
```

- [ ] **Step 6: Run focused tests**

Run:

```bash
GOWORK=off go test ./internal/usecase/backup -run 'Management.*(Save|Configure|Repository|Revision|Verification|Credential)' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/usecase/backup/management.go internal/usecase/backup/management_test.go
git commit -m "feat(backup): separate repository save and test"
```

## Task 4: Classify provider and network failures without leaking secrets

**Files:**

- Create: `internal/contracts/backup/repository_error.go`
- Create: `internal/contracts/backup/repository_error_test.go`
- Create: `internal/infra/backup/repository_error.go`
- Create: `internal/infra/backup/repository_error_test.go`
- Modify: `internal/usecase/backup/errors.go`
- Modify: `internal/usecase/backup/management.go`

- [ ] **Step 1: Write failing safe-error contract tests**

Define stable stages and reasons from the approved design. Test that `RepositoryAccessError.Error()` includes only provider, stage, reason, provider code, request ID, and node ID. Assert it omits a cause containing an access key, secret, authorization header, signed query, and request body.

The shared type is:

```go
type RepositoryAccessError struct {
	Reason       RepositoryAccessReason
	Stage        RepositoryAccessStage
	Provider     StoreKind
	ProviderCode string
	RequestID    string
	NodeID       uint64
	Cause        error `json:"-"`
}

func (e *RepositoryAccessError) Unwrap() error {
	return e.Cause
}
```

- [ ] **Step 2: Write failing MinIO and network classification tests**

Table-test:

- `InvalidAccessKeyId` to `invalid_access_key`;
- `SignatureDoesNotMatch` to `signature_mismatch`;
- `AccessDenied` to `access_denied`;
- `NoSuchBucket` to `bucket_not_found`;
- region redirect/authorization-header-malformed responses to `region_mismatch`;
- DNS/refused connection to `endpoint_unreachable`;
- certificate failures to `tls_failure`;
- context deadline and `net.Error.Timeout` to `timeout`;
- unmatched errors to the stage-specific read/write/list/delete reason or `unknown`.

Assert provider code and request ID are preserved but raw response text is not.

- [ ] **Step 3: Run tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./internal/contracts/backup ./internal/infra/backup -run 'RepositoryAccessError|ClassifyRepository' -count=1
```

Expected: compile failures because the typed contract and classifier do not exist.

- [ ] **Step 4: Implement the shared error vocabulary**

Add every approved reason and stage as typed string constants. Implement a bounded safe `Error()` method and retain the cause only through `Unwrap`.

- [ ] **Step 5: Implement infrastructure classification**

Use `minio.ToErrorResponse`, `errors.Is`, `errors.As`, `net.Error`, `net.DNSError`, `url.Error`, `x509` certificate errors, and TLS record errors. Accept provider and stage as explicit inputs:

```go
func classifyRepositoryError(
	provider backupcontract.StoreKind,
	stage backupcontract.RepositoryAccessStage,
	err error,
) error
```

Return an existing `RepositoryAccessError` unchanged except when filling a missing provider or stage.

- [ ] **Step 6: Preserve the typed cause in the use case**

Update `normalizeStoreAccessError` so `errors.Is(err, ErrStoreUnreachable)` remains true while `errors.As` can still recover `*backupcontract.RepositoryAccessError`. Wrap repository-open and identity failures with their exact stages.

- [ ] **Step 7: Run focused tests**

Run:

```bash
GOWORK=off go test ./internal/contracts/backup ./internal/infra/backup ./internal/usecase/backup -run 'RepositoryAccessError|ClassifyRepository|Management.*Repository' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/contracts/backup/repository_error.go internal/contracts/backup/repository_error_test.go internal/infra/backup/repository_error.go internal/infra/backup/repository_error_test.go internal/usecase/backup/errors.go internal/usecase/backup/management.go
git commit -m "feat(backup): classify repository access failures"
```

## Task 5: Complete and safely transport the cluster repository probe

**Files:**

- Modify: `internal/contracts/backup/scheduled_rpc.go`
- Modify: `internal/infra/backup/repository_probe.go`
- Create: `internal/infra/backup/repository_probe_test.go`
- Modify: `internal/access/node/scheduled_backup_rpc.go`
- Modify: `internal/access/node/scheduled_backup_rpc_test.go`

- [ ] **Step 1: Write failing probe behavior tests**

Use an in-memory archive store plus deterministic one-node and multi-node fakes. Prove the coordinator performs:

- marker write;
- marker read at every active data node;
- node receipt write;
- receipt read at the coordinator;
- prefix list containing the marker and every receipt;
- prefix delete;
- post-delete absence check.

Also prove:

- an exact remote node ID is attached to a failure;
- a missing receipt fails at `read_receipt`;
- list denial fails at `list`;
- cleanup denial fails at `delete` and prevents verification;
- cleanup uses an independent bounded context even after the request context is canceled;
- no active data nodes fails with a bounded `node_unreachable` detail.

- [ ] **Step 2: Write failing RPC error round-trip tests**

Add a wire-safe DTO:

```go
type RepositoryAccessFailure struct {
	Reason       RepositoryAccessReason `json:"reason"`
	Stage        RepositoryAccessStage  `json:"stage"`
	Provider     StoreKind              `json:"provider"`
	ProviderCode string                 `json:"provider_code,omitempty"`
	RequestID    string                 `json:"request_id,omitempty"`
	NodeID       uint64                 `json:"node_id,omitempty"`
}
```

Extend only `scheduledBackupProbeResponse` with `Failure *RepositoryAccessFailure`. Prove the server extracts safe fields from a typed error and the client reconstructs a typed error. Ensure malformed or oversized error fields are rejected or bounded by existing RPC JSON limits.

- [ ] **Step 3: Run tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./internal/infra/backup ./internal/access/node -run 'RepositoryProbe|ScheduledBackupRepositoryProbe' -count=1
```

Expected: failures because list/delete verification and typed RPC failures are missing.

- [ ] **Step 4: Implement staged probe wrapping**

Wrap every repository operation through the classifier with the exact stage. When a remote transport call itself fails, return reason `node_unreachable`, include the target `NodeID`, and retain the transport cause internally.

- [ ] **Step 5: Implement bounded mandatory cleanup**

Use a helper with a fresh timeout:

```go
func cleanupRepositoryProbe(
	store backupartifact.ArchiveStore,
	prefix string,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return store.DeletePrefix(ctx, prefix)
}
```

Attempt it after every partial success. If both the main probe and cleanup fail, preserve the primary failure internally but report the cleanup failure as the operator-facing stage because delete permission is required for retention. After successful deletion, confirm the probe prefix is absent.

- [ ] **Step 6: Implement safe RPC failure transport**

Keep the probe response magic/version unchanged because JSON fields are additive. Populate `Failure` only for typed repository errors. Never serialize `Cause`. Reconstruct a `RepositoryAccessError` on the client and fill the requested node ID if the remote response omitted it.

- [ ] **Step 7: Run focused tests**

Run:

```bash
GOWORK=off go test ./internal/infra/backup ./internal/access/node -run 'RepositoryProbe|ScheduledBackupRepositoryProbe' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/contracts/backup/scheduled_rpc.go internal/infra/backup/repository_probe.go internal/infra/backup/repository_probe_test.go internal/access/node/scheduled_backup_rpc.go internal/access/node/scheduled_backup_rpc_test.go
git commit -m "feat(backup): report staged cluster probe failures"
```

## Task 6: Expose exact saved-plan testing and structured errors through Manager

**Files:**

- Modify: `internal/access/manager/errors.go`
- Modify: `internal/access/manager/backups.go`
- Modify: `internal/access/manager/backups_test.go`
- Modify: `internal/access/manager/backup_audit.go`

- [ ] **Step 1: Write failing request-contract tests**

Change the repository-test request to:

```go
type backupRepositoryTestRequest struct {
	ExpectedPlanRevision uint64 `json:"expected_plan_revision"`
}
```

Prove the handler:

- accepts only a positive saved plan revision;
- no longer accepts or forwards store fields or credentials;
- forwards the exact revision to `ManagementService.TestRepository`;
- returns the verified plan with credential ciphertext removed;
- maps stale revision to `backup_plan_conflict`.

- [ ] **Step 2: Write failing structured-error response tests**

Feed Manager typed failures for OSS and COS and assert JSON contains:

```json
{
  "error": "backup_repository_auth_failed",
  "message": "Alibaba Cloud OSS rejected the AccessKey ID.",
  "detail": {
    "provider": "oss",
    "stage": "write_marker",
    "reason": "invalid_access_key",
    "provider_code": "InvalidAccessKeyId",
    "request_id": "request-1",
    "node_id": 1
  }
}
```

Cover stable top-level families for authentication, permission, bucket, region, endpoint, TLS, timeout, operation, cluster node, identity, and unknown failures. Assert secret marker strings never occur in the response body.

- [ ] **Step 3: Write a failing unverified admission mapping test**

Assert `ErrRepositoryUnverified` maps to HTTP 409, code `backup_repository_unverified`, and an actionable message.

- [ ] **Step 4: Run tests and confirm they fail**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'ManagerBackup.*(Repository|Unverified|Error|Revision)' -count=1
```

Expected: failures because the old endpoint binds a full plan and the response has no detail.

- [ ] **Step 5: Implement Manager request and response changes**

Extend `errorResponse` with:

```go
Detail any `json:"detail,omitempty"`
```

Keep `jsonError` unchanged for unrelated handlers and add a backup-specific helper that emits the typed detail. Provider-specific messages must be derived from stable reason/provider pairs, never from `Cause`.

Return:

```go
c.JSON(http.StatusOK, gin.H{
	"ok":   true,
	"plan": redactedPlan,
})
```

For `PUT /manager/backups/plan`, return the existing plan and optional initial
job plus `credentials_configured`, calculated before ciphertext redaction. This
lets the client clear plaintext inputs while immediately showing credential
reuse state even before its dashboard refresh completes.

- [ ] **Step 6: Make audit fields useful and bounded**

For typed repository failures record only:

- stable top-level error code;
- provider;
- stage;
- node ID.

Do not record provider message, provider request body, credentials, ciphertext, endpoint query strings, or signed URLs.

- [ ] **Step 7: Run focused tests**

Run:

```bash
GOWORK=off go test ./internal/access/manager -run 'ManagerBackup.*(Repository|Unverified|Error|Revision)' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/access/manager/errors.go internal/access/manager/backups.go internal/access/manager/backups_test.go internal/access/manager/backup_audit.go
git commit -m "feat(manager): return actionable backup storage errors"
```

## Task 7: Update the Web API model and preserve structured error detail

**Files:**

- Modify: `web/src/lib/manager-api.types.ts`
- Modify: `web/src/lib/manager-api.ts`
- Modify: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write failing API client tests**

Prove:

- `testBackupRepository(4)` sends only `{"expected_plan_revision":4}`;
- `parseManagerError` preserves `detail`;
- existing `report` behavior for unrelated endpoints remains compatible.

- [ ] **Step 2: Run tests and confirm they fail**

Run:

```bash
bun run test -- src/lib/manager-api.test.ts
```

Expected: failures because test storage still sends a complete plan and `ManagerApiError` has no detail.

- [ ] **Step 3: Add exact TypeScript types**

Add:

```ts
export type ManagerBackupRepositoryVerification = {
  status: "unverified" | "verified"
  verified_at_unix_ms?: number
}

export type ManagerBackupRepositoryErrorDetail = {
  provider: ManagerBackupStoreKind
  stage: string
  reason: string
  provider_code?: string
  request_id?: string
  node_id?: number
}
```

Add optional `repository_verification` to `ManagerBackupPlan`, and a `ManagerBackupRepositoryTestResult` containing `ok` plus the verified plan.

Add `credentials_configured: boolean` to
`ManagerBackupConfigureResult`.

- [ ] **Step 4: Preserve `detail` in `ManagerApiError`**

Add `detail?: unknown` without removing `report`. Parse both fields so no unrelated caller regresses.

Change:

```ts
export function testBackupRepository(expectedPlanRevision: number) {
  return jsonManagerFetch<ManagerBackupRepositoryTestResult>(
    "/manager/backups/repository/test",
    {
      method: "POST",
      body: JSON.stringify({
        expected_plan_revision: expectedPlanRevision,
      }),
    },
  )
}
```

- [ ] **Step 5: Run focused tests**

Run:

```bash
bun run test -- src/lib/manager-api.test.ts
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/manager-api.types.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat(web): model backup repository verification errors"
```

## Task 8: Show verification state and save/test feedback beside the buttons

**Files:**

- Modify: `web/src/pages/backups/page.tsx`
- Modify: `web/src/pages/backups/page.test.tsx`
- Modify: `web/src/i18n/messages/en.ts`
- Modify: `web/src/i18n/messages/zh-CN.ts`

- [ ] **Step 1: Write failing persistence and dirty-state tests**

Add Testing Library tests proving:

- saving OSS settings, then resolving the dashboard reload with the saved plan, repopulates Region, Bucket, Prefix, and Endpoint;
- the same workflow works for the full COS Bucket name;
- credential inputs are empty after reload and show the reuse placeholder;
- editing any effective repository field switches automatic backup off and displays Not tested;
- a non-empty access or secret input counts as an unsaved repository change;
- Test storage is disabled while repository changes are unsaved and explains Save first;
- testing sends only the loaded plan revision;
- immediate backup and automatic backup controls are disabled for explicit unverified state;
- legacy nil verification displays Verified before upgrade.

- [ ] **Step 2: Write failing inline feedback tests**

For a typed `invalid_access_key` failure, assert:

- the message is rendered after the plan action row inside the repository section;
- the failure container has `role="alert"`;
- provider code, request ID, and node ID are visible when present;
- the server detail is not replaced by the old generic sentence;
- the same error text is absent from the page-top mutation area;
- test success appears in an `aria-live` region beside the buttons.

- [ ] **Step 3: Run page tests and confirm they fail**

Run:

```bash
bun run test -- src/pages/backups/page.test.tsx
```

Expected: failures because verification state, dirty-state gating, and inline feedback are absent.

- [ ] **Step 4: Separate plan feedback from global mutation feedback**

Keep page-level feedback for archive, restore, and unrelated operations. Add plan-specific state:

```ts
type PlanFeedback =
  | { kind: "success"; message: string }
  | { kind: "error"; message: string; detail?: ManagerBackupRepositoryErrorDetail }
  | null
```

Save and Test update `PlanFeedback`; they do not update the top banner. Render it immediately after the action row. Do not scroll or move focus.

- [ ] **Step 5: Track effective repository dirtiness**

Compare the normalized draft against the loaded plan over kind, endpoint, region, bucket, prefix, and path style. Treat either non-empty credential input as dirty. Centralize repository-field updates so they:

- preserve typed input;
- set `enabled` false;
- clear stale success feedback;
- make displayed verification Not tested.

The Test button is disabled when there is no saved plan, the repository is dirty, a mutation is active, or write permission is unavailable.

- [ ] **Step 6: Render verification and specific failure details**

Use the saved plan record:

- nil: Verified before upgrade;
- explicit unverified: Not tested;
- verified: Verified plus formatted timestamp.

Use the stable reason and stage for localized primary text. Append optional provider code, request ID, and node ID as labeled metadata. Do not special-case `backup_store_unreachable` into the generic localized sentence.

- [ ] **Step 7: Add English and Simplified Chinese messages**

Add messages for:

- Not tested, Verified, Verified before upgrade;
- Save before testing;
- testing required before enabling or starting backup;
- every stable reason and stage label;
- provider code, request ID, and node ID labels.

- [ ] **Step 8: Run page tests**

Run:

```bash
bun run test -- src/pages/backups/page.test.tsx
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add web/src/pages/backups/page.tsx web/src/pages/backups/page.test.tsx web/src/i18n/messages/en.ts web/src/i18n/messages/zh-CN.ts
git commit -m "fix(web): keep backup storage feedback beside actions"
```

## Task 9: Exercise the production provider against real OSS and COS

**Files:**

- Create: `internal/infra/backup/repository_provider_integration_test.go`
- Modify: `internal/infra/backup/archive_s3_store_integration_test.go`
- Create: `internal/access/manager/backups_repository_integration_test.go`

- [ ] **Step 1: Refactor the existing S3 integration round trip into a shared helper**

Keep the `integration` build tag. The helper accepts a production `ArchiveRepositoryProvider`, a `StoreConfig`, and a provider name. It must:

- use a unique `wukongim-integration/provider/timestamp-random` prefix;
- put an exact-size create-only object;
- open and verify bytes and metadata;
- list and find the exact key;
- delete it;
- confirm it is absent;
- perform bounded best-effort `DeletePrefix` cleanup in `t.Cleanup`;
- never print environment values.

- [ ] **Step 2: Add real Alibaba Cloud OSS coverage**

Read:

- `WK_TEST_OSS_REGION`;
- `WK_TEST_OSS_BUCKET`;
- `WK_TEST_OSS_ACCESS_KEY_ID`;
- `WK_TEST_OSS_ACCESS_KEY_SECRET`;
- optional `WK_TEST_OSS_ENDPOINT`.

Create a real `CredentialCipher`, seal credentials into `ciphertext`, construct
a production `RepositoryProvider`, and open:

```go
backupcontract.StoreConfig{
	Kind:                 backupcontract.StoreKindOSS,
	Endpoint:             strings.TrimSpace(os.Getenv("WK_TEST_OSS_ENDPOINT")),
	Region:               region,
	Bucket:               bucket,
	Prefix:               uniquePrefix,
	PathStyle:            false,
	CredentialCiphertext: ciphertext,
	CredentialRevision:   1,
}
```

Leave Endpoint empty to exercise production OSS endpoint derivation when no override is supplied.

- [ ] **Step 3: Add real Tencent Cloud COS coverage**

Read:

- `WK_TEST_COS_REGION`;
- `WK_TEST_COS_BUCKET`;
- `WK_TEST_COS_SECRET_ID`;
- `WK_TEST_COS_SECRET_KEY`;
- optional `WK_TEST_COS_ENDPOINT`.

Use the full COS Bucket name including APPID. Leave Endpoint empty to exercise production COS endpoint derivation when no override is supplied.

- [ ] **Step 4: Run the integration test without credentials**

Run:

```bash
GOWORK=off go test -tags=integration ./internal/infra/backup -run 'TestRepositoryProviderRoundTripAgainst(OSS|COS)' -count=1 -timeout=3m
```

Expected before credentials are configured: both provider cases SKIP with provider-specific missing-environment messages.

- [ ] **Step 5: Add the real single-node cluster Manager workflow**

Build an integration-tagged test around the real authenticated Gin Manager
handler, `ManagementService`, `ScheduledService`, credential cipher, production
`RepositoryProvider`, and `ClusterRepositoryProbe`. The probe cluster fixture
must publish one active data node through Controller-shaped state; it must not
introduce a standalone path that bypasses cluster semantics.

Run the same table once for OSS and once for COS:

1. `PUT /manager/backups/plan` with `enabled=false` and real credentials;
2. `GET /manager/backups` and assert all non-secret fields persisted,
   credentials are redacted, and verification is unverified;
3. `POST /manager/backups/jobs` and assert
   `backup_repository_unverified`;
4. `POST /manager/backups/repository/test` with only the saved revision;
5. `GET /manager/backups` and assert the same plan revision is verified;
6. `PUT /manager/backups/plan` with unchanged repository fields, blank
   credential inputs, and `enabled=true`;
7. assert credential reuse and verified state were preserved.

Use a separate unique integration prefix from the low-level provider
round-trip. Cleanup that prefix with a bounded independent context.

- [ ] **Step 6: Run the Manager integration test without credentials**

Run:

```bash
GOWORK=off go test -tags=integration ./internal/access/manager -run 'TestManagerBackupRepositoryWorkflowAgainst(OSS|COS)' -count=1 -timeout=3m
```

Expected before credentials are configured: both provider cases SKIP with
provider-specific missing-environment messages.

- [ ] **Step 7: Request real credentials from the user**

Ask the user to export or securely provide the OSS and COS environment values listed above. Do not place credentials in source files, shell history shown in chat, commits, logs, or test failure messages.

- [ ] **Step 8: Run both real cloud test layers**

Run:

```bash
GOWORK=off go test -tags=integration ./internal/infra/backup ./internal/access/manager -run 'Test(RepositoryProviderRoundTrip|ManagerBackupRepositoryWorkflow)Against(OSS|COS)' -count=1 -timeout=5m
```

Expected: the low-level provider and full Manager workflow both PASS against
OSS and COS. If either fails, retain and report the structured provider code,
request ID, stage, and reason; do not fall back to a generic unavailable
message.

- [ ] **Step 9: Commit**

```bash
git add internal/infra/backup/repository_provider_integration_test.go internal/infra/backup/archive_s3_store_integration_test.go internal/access/manager/backups_repository_integration_test.go
git commit -m "test(backup): verify real OSS and COS repositories"
```

## Task 10: Update flow and project knowledge documentation

**Files:**

- Modify: `internal/contracts/backup/FLOW.md`
- Modify: `internal/usecase/backup/FLOW.md`
- Modify: `internal/infra/backup/FLOW.md`
- Modify: `internal/access/node/FLOW.md`
- Modify: `internal/access/manager/FLOW.md`
- Modify: `internal/app/FLOW.md`
- Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Update each affected flow**

Document:

- optional verification metadata and legacy nil behavior;
- save-only publication versus exact-revision test;
- repository equality and credential revision invalidation;
- configure/manual/schedule/runner admission gates;
- marker, node receipt, list, delete, identity, and verification stages;
- safe probe error DTO over node RPC;
- Manager structured error response and redaction;
- app composition of production provider, probe, and scheduled service.

- [ ] **Step 2: Record stable project knowledge**

Add a concise rule:

> Saving backup repository configuration is a durable Controller operation and never proves connectivity. Only an exact saved plan revision that completes the repository and all-active-data-node probe is verified and eligible for backup admission. A nil verification record is legacy verified state until the effective repository changes.

- [ ] **Step 3: Check documentation consistency**

Run:

```bash
rg -n "repository|verification|unverified|OSS|COS" internal/contracts/backup/FLOW.md internal/usecase/backup/FLOW.md internal/infra/backup/FLOW.md internal/access/node/FLOW.md internal/access/manager/FLOW.md internal/app/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
```

Expected: all new contracts are described with consistent terminology.

- [ ] **Step 4: Commit**

```bash
git add internal/contracts/backup/FLOW.md internal/usecase/backup/FLOW.md internal/infra/backup/FLOW.md internal/access/node/FLOW.md internal/access/manager/FLOW.md internal/app/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs: explain backup repository verification"
```

## Task 11: Run the complete focused verification matrix

**Files:**

- Verify only; modify production files only if a failing test exposes a defect.

- [ ] **Step 1: Format changed Go files**

Run `gofmt` over the changed Go files listed by:

```bash
git diff --name-only 54b83e43c..HEAD -- '*.go'
```

- [ ] **Step 2: Run focused Go unit tests**

Run:

```bash
GOWORK=off go test ./internal/contracts/backup ./internal/usecase/backup ./internal/infra/backup ./internal/access/node ./internal/access/manager ./internal/app ./pkg/controller/state ./pkg/controller/fsm -count=1
```

Expected: PASS.

- [ ] **Step 3: Run the broader directly affected Go suites**

Run:

```bash
GOWORK=off go test ./internal/... ./pkg/controller/... -count=1
```

Expected: PASS.

- [ ] **Step 4: Run Web tests and production build**

Run:

```bash
cd web
bun run test -- src/lib/manager-api.test.ts src/pages/backups/page.test.tsx
bun run build
```

Expected: tests PASS and Vite/TypeScript build succeeds.

- [ ] **Step 5: Re-run real OSS and COS tests**

Run:

```bash
GOWORK=off go test -tags=integration ./internal/infra/backup ./internal/access/manager -run 'Test(RepositoryProviderRoundTrip|ManagerBackupRepositoryWorkflow)Against(OSS|COS)' -count=1 -timeout=5m
```

Expected: provider round-trip and Manager save/test/enable workflows PASS
against both real providers with the user-supplied environment.

- [ ] **Step 6: Inspect the final diff for secrets and generated artifacts**

Run:

```bash
git status --short
git diff --check
git diff --stat
git diff --name-only
rg -n 'AccessKeySecret|SecretKey=|Authorization:|X-Amz-Signature' internal web docs --glob '!**/*_test.go'
```

Expected: no credentials, authorization data, signed URLs, accidental Web build output, or whitespace errors.

- [ ] **Step 7: Commit any test-driven corrections**

If verification required corrections, commit only those scoped changes:

```bash
git add internal pkg/controller web docs
git commit -m "fix(backup): complete repository verification flow"
```

- [ ] **Step 8: Record final evidence**

Capture:

- focused and broader Go test results;
- Web test and build results;
- real OSS result;
- real COS result;
- final commit range and clean worktree status.
