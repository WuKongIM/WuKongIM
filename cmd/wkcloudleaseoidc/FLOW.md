# Cloud Lease OIDC Setup Flow

`wkcloudleaseoidc` is the one-time identity boundary for the automated Cloud
Lease workflows. It is separate from `wkcloudlease`: the setup binary can
create or remove repository-owned Alibaba OIDC/RAM identity, but it has no
Cloud Lease Acquire operation.

```text
strict non-secret setup config + complete existing AccessKey Secret pair
  -> plan/apply seven repository-owned identity resources
  -> read-after-write exact provider, trust, role, policy, and attachment proof
  -> three OIDC jobs call verify with temporary credentials
  -> non-secret role/provider identifiers become GitHub Variables
```

`remove` first enumerates the complete repository-tagged Cloud Lease inventory
and refuses while any related asset remains. `verify` uses temporary OIDC
credentials only and proves the exact assumed role and its sole canonical
custom policy without creating infrastructure. The command never prints or
persists AccessKeys, security tokens, or arbitrary provider responses.
