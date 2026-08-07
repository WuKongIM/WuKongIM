# Cloud Lease OIDC identity setup

This runbook configures only workflow identity. It does not Quote, Acquire, or
create any billable Alibaba resource.

## Prerequisites

- `gh` is authenticated as a repository administrator.
- The repository has a complete existing Secret pair named
  `ALIBABA_CLOUD_ACCESS_KEY_ID` and `ALIBABA_CLOUD_ACCESS_KEY_SECRET` when the
  Cloud Lease roles do not yet exist. A partial or missing pair fails setup.
- The AccessKey identity can manage the repository-owned Alibaba OIDC provider,
  RAM roles and policies, and can read repository-tagged Lease inventory.
- The setup Workflow is present on `main`.

The existing AccessKey Secrets are never deleted or rotated by this flow.

## Inspect GitHub changes

```bash
./scripts/cloud-lease/configure-github-identity.sh plan WuKongIM/WuKongIM
```

The plan covers the repository OIDC subject and exactly these Environments:

- `cloud-lease-provision`
- `cloud-lease-observe`
- `cloud-lease-release`
- `cloud-deployment`

Applying the GitHub configuration removes human-review requirements because
these are unattended Agent Tools. It preserves wait timers, deployment branch
policy, Environment Secrets and Variables, and unrelated repository settings.

## Configure and live-verify all roles

```bash
./scripts/cloud-lease/setup-identity.sh WuKongIM/WuKongIM
```

This command:

1. applies the exact repository OIDC subject template;
2. creates or reconciles the four Environments without reviewers;
3. dispatches `cloud-lease-oidc-setup.yml` on `main`;
4. uses the existing AccessKey pair only if the binding Variables are absent;
5. creates or repairs CloudLeaseProvisioner, CloudLeaseObserver, and
   CloudLeaseReleaser with one-hour sessions and exact workflow policies;
6. exchanges GitHub OIDC independently inside all three Environments;
7. verifies the exact assumed role, sole canonical policy, one-hour session,
   and both setup and ordinary-workflow trust subjects; and
8. writes only non-secret provider, role, audience, region, and account-hash
   values to repository Variables.

If a normal live check detects role, policy, session, or trust drift, the local
setup command automatically retries once with `--force` through the existing
AccessKey pair. `--force` may also be supplied directly. Neither path exposes
the AccessKeys to ordinary Cloud Lease jobs.

## Ordinary tools

- `cloud-lease-provision.yml` uses CloudLeaseProvisioner. Quote-only is the
  default; paid Acquire consumes the exact still-valid admitted Quote rather
  than obtaining a second placement, and additionally requires
  `paid_authorization=create-paid-cloud-lease`.
- `cloud-lease-observe.yml` uses CloudLeaseObserver for inventory, price, and
  delayed billing reads and cannot acquire, change access rules, or release
  resources.
- `cloud-lease-release.yml` uses CloudLeaseReleaser and requires
  `release_authorization=release-tagged-cloud-lease` for manual exact Release.
  Its protected 15-minute schedule sweeps repository-tagged expired or
  cleanup-pending Leases without operator input.
- Deployment uses `cloud-deployment`, SSH credentials only, and no Alibaba
  token.

Provision, Observe, and Deployment are manual Agent Tools. Release is also the
unattended expiry/cleanup-pending safety automation. Ordinary push and pull
request CI cannot dispatch paid infrastructure.

## Protected removal

`wkcloudleaseoidc remove --config <strict-config.json>` first lists complete
repository-tagged Alibaba inventory. Any instance, disk, ENI, EIP relationship,
rule, route, NAT gateway, vSwitch, or VPC prevents identity deletion. Remove the
binding only after Release or Sweep returns the exact zero-inventory proof.
