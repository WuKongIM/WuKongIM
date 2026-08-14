---
scope: package
summary: Creates, verifies, and removes repository-owned Alibaba OIDC identity without Cloud Lease acquisition authority.
---

# Cloud Lease OIDC Setup Flow

## Responsibility

`cmd/wkcloudleaseoidc` is the one-time identity bootstrap boundary for automated
Cloud Lease workflows. It plans, applies, verifies, or removes the exact
repository-owned Alibaba OIDC/RAM resources.
It does not quote, acquire, deploy, or release a Cloud Lease.

## Boundaries

- The setup binary has identity-administration authority but no Lease Acquire
  or workload/deployment operation.
- Apply consumes a complete existing AccessKey pair; workflow verification uses
  temporary OIDC credentials only.
- Non-secret identifiers become GitHub Variables outside this command.

## Main Flows

1. Plan/apply the fixed provider, trust, roles, policy, and attachments from a
   strict non-secret setup document.
2. Verify the exact assumed role and its sole canonical policy using temporary
   workflow credentials without creating infrastructure.
3. Remove identity only after complete repository-tagged Lease inventory proves
   no related asset remains.

## Invariants and Failure Semantics

- Access keys, security tokens, and arbitrary provider responses are never
  printed or persisted.
- Read-after-write verification must prove every expected identity relationship.
- Incomplete inventory blocks removal; absence is never inferred from a partial
  page.

## Read First

- [Setup entrypoint](main.go)
- [Setup tests](main_test.go)

## Update Triggers

Update this file when resource inventory, credential mode, trust policy,
verification, output secrecy, or removal safety changes.
