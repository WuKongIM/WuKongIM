---
scope: package
summary: Seals and verifies immutable offline deployment bundles without cloud, credential, build, or service authority.
---

# Cloud Bundle CLI Flow

## Responsibility

`cmd/wkcloudbundle` keeps legacy run-specific simulation bundle commands
separate from procurement-independent offline deployment bundle sealing and
verification.
It does not deploy hosts, procure infrastructure, or materialize runtime secrets.

## Boundaries

- Trusted workflows build software and download checksum-pinned dependencies;
  this command only validates and packages supplied files.
- Cloud discovery, credentials, service startup, and background work are out of
  scope.
- Deployment intent belongs to `internal/usecase/clouddeploy`; no-follow
  filesystem safety belongs to its infrastructure adapter.

## Main Flows

1. Legacy render/verify delegates to the compatibility simulation bundle.
2. `seal-offline` validates the fixed host intent and filesystem, then writes a
   manifest containing exact inventory, modes, sizes, source/control revisions,
   and content digest.
3. `verify-offline` recomputes the complete manifest contract without mutation.

## Invariants and Failure Semantics

- Offline commands never clone source, build binaries, read credentials, or
  contact cloud APIs.
- Every accepted bundle is content-addressed and bound to immutable source and
  control revisions.
- Symlinks, changed inventory, mismatched modes/sizes, and digest drift fail
  closed.

## Read First

- [CLI entrypoint](main.go)
- [Offline commands](offline_commands.go)
- [Command tests](main_test.go)

## Update Triggers

Update this file when command separation, bundle identity, manifest coverage,
filesystem safety, or trusted-workflow ownership changes.
