# Cloud Lease CLI Flow

`wkcloudlease` is the command boundary for the provider-neutral temporary
infrastructure lifecycle. It delegates policy and reconciliation to
`internal/usecase/cloudlease`; provider registration belongs in this
composition layer rather than in the use case.

```text
operator / automation
  -> wkcloudlease command
     -> construct selected Provider adapter
     -> construct cloudlease.Controller
     -> execute one synchronous operation
     -> emit non-secret structured output
```

The `dry-run` command constructs only the in-memory fake Provider and
executes Quote, Acquire, Inspect, GrantAccess, RevokeAccess, Release, and Sweep
in sequence. It starts no process or background goroutine, performs no network
call, and cannot create cloud resources.

The `quote --plan <file>` command strictly decodes a bounded Plan JSON document,
constructs the selected read-only provider, and emits a versioned Quote result.
The Alibaba path requires temporary OIDC role credentials and exposes only the
read operations listed by `alibaba.RequiredQuoteActions`; it cannot acquire or
change cloud resources.

`acquire` strictly consumes versioned Plan, Quote, and public bootstrap-access
documents. `inspect` and `release` consume an exact versioned Selector, while
`sweep` takes the fixed provider, region, and repository inventory boundary.
Inspect constructs the inventory-only adapter under the Observer role and has
no mutation-authorization value. Acquire, Release, and Sweep construct only the
paid lifecycle adapter, which independently requires the exact
mutation-authorization environment value. Every command emits a
versioned non-secret JSON Receipt, Release result, or Sweep result; Release
and Acquire also emit partial/residual evidence before returning an error.
WuKongIM deployment
and workload orchestration do not belong in this command.
