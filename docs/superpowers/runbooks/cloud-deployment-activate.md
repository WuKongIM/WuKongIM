# Cloud Deployment Activation

`cloud-deployment-activate.yml` is the provider-free Deployment Action for the
automated chat-lifecycle run. It is not a Cloud Lease lifecycle tool. It has
only `contents: read` and `actions: read`, consumes a non-secret active Lease
Receipt plus a content-addressed offline bundle, and receives its SSH private
key only from the `cloud-deployment` Environment.

## Inputs and output

Dispatch on protected `main` with the exact Workflow run and Artifact name for
both `cloud-lease-provision.yml` and `cloud-deployment-bundle.yml`. The Action
first queries the GitHub Actions API and requires both run IDs to be successful
`workflow_dispatch` executions of those exact workflow files on protected
`main`, from the same repository and exact control commit as the Deployment
Action. It then builds trusted validators from that checkout, revalidates the
Lease Receipt and bundle before executing any payload binary, derives
`wukongim.cloud_deployment.plan/v1`, and refuses a bundle Artifact name that
does not contain the derived digest. The bundle's trusted control SHA must also
equal the exact `main` revision executing the Deployment Action; main-branch
drift therefore fails before any host mutation.

The run Artifact `cloud-deployment-<workflow-run-id>` contains the non-secret
Deployment Plan, bounded readiness snapshot when collection completed, and
`deployment-outcome.json`. A successful outcome contains
`wukongim.cloud_deployment.receipt/v1`. Failure contains a stable code, the last
completed gate, and bounded generated evidence; it never includes raw SSH
output or credentials.

## Native activation sequence

1. Stage the bundle and Plan plus short-lived `0600` credential archives on the
   public load node.
2. Relay the same archive from that load node to the three private service
   nodes using agent forwarding; the private key is never copied to a host.
3. Independently verify the sealed bundle on all four Ubuntu 24.04 x86_64
   hosts.
4. Require exactly one non-system disk on each host, format it only when empty,
   and mount it. Install
   root-owned binaries/configuration and `0600` runtime files. No package
   manager or container runtime is used.
5. Start one WuKongIM process per service node and, on the load node, three
   workers, Prometheus, Analysis, Caddy, and independent host and process
   observers. Each service node also runs the native exact-filesystem observer
   used by formal preflight. The coordinator unit is installed and observed but
   remains dormant: only the post-Receipt workload orchestrator may start its
   exact rehearsal, formal, or capacity stage. Its bounded dependency wait has
   a 960-second systemd start timeout. `Requisite=` checks workers and
   Prometheus without restarting a terminal process. Every workload and product
   unit has `Restart=no`.
6. Poll for at most 20 minutes until the exact four bundle digests, OS/base
   tools, system/data filesystems, five-percent reserve, one-second clock
   bound, systemd units, three members, 256 physical Slots, 12 logical groups,
   replica counts read from the effective startup configuration of all three
   nodes, workers, seven Prometheus targets, strict formal workload config,
   Manager, Demo, proxy, and Analysis checks all pass.

The production host path records `bundle_transferred`, `bundle_verified`,
`hosts_prepared`, and `services_active` separately. Before every operation it
writes the stable failure code, exact role, conservative last completed gate,
and bounded known-host state that will be uploaded if that operation stops the
Action.

The Action cannot Quote, Acquire, GrantAccess, RevokeAccess, Release, Sweep, or
call an Alibaba API. On failure the caller decides whether to release the Lease
and use the one permitted fresh retry.

## Credential handling

Set `CLOUD_DEPLOYMENT_SSH_PRIVATE_KEY` as an Environment secret for the exact
Lease before dispatch. Runtime Manager, worker, Analysis, and Demo credentials
are generated inside the Action, masked, installed as root-owned `0600` files,
and deleted from the runner and upload staging directories after activation.
The read-only Manager and Demo share one temporary password; Codex can recover
it through its separately provisioned diagnostic SSH identity from the
root-readable node environment and hand it to the operator without publishing
it in an Artifact. Worker and Analysis capabilities remain separately scoped.
The SSH private key is deleted from the runner in the always-run cleanup step.
Bootstrap public keys remain Lease-bounded; final credential deletion and
operator credential handoff are controlled by the top-level run workflow.
