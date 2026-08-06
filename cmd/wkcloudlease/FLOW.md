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

The initial `dry-run` command constructs only the in-memory fake Provider and
executes Quote, Acquire, Inspect, GrantAccess, RevokeAccess, Release, and Sweep
in sequence. It starts no process or background goroutine, performs no network
call, and cannot create cloud resources.

Future billable commands must keep an explicit authorization boundary outside
the Provider contract, validate the exact immutable Plan and Quote, and emit a
Receipt suitable for later inventory reconstruction. WuKongIM deployment and
workload orchestration do not belong in this command.
