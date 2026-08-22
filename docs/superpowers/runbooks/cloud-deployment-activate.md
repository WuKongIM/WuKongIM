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
`main` and from the same repository. It then builds trusted validators from its
own protected checkout, revalidates the
Lease Receipt and bundle before executing any payload binary, derives
`wukongim.cloud_deployment.plan/v2`, and refuses a bundle Artifact name that
does not contain the derived digest. Long builds remain usable when `main`
advances: the bundle's trusted control SHA must equal its authenticated bundle
producer run, while the Lease provenance must bind that bundle digest and the
same immutable source SHA. An arbitrary or cross-run artifact mix therefore
still fails before host mutation.

The default `deployment_purpose=immutable` accepts only generation 1 and
requires the candidate source and bundle to equal Lease provenance. The
`repair` purpose is accepted only for a Lease tagged `stage=repair`; every
activation binds a positive candidate generation while retaining the original
Lease source and bundle separately. Generation two and later quiesce all known
units and clear only the fixed product/workload data roots before installing
the next candidate. Repair activation is deployment evidence for that
candidate, never official rehearsal or formal evidence.

The run Artifact `cloud-deployment-<workflow-run-id>` contains the non-secret
Deployment Plan, bounded readiness snapshot when collection completed, and
`deployment-outcome.json`. A successful outcome contains
`wukongim.cloud_deployment.receipt/v2`. Failure contains a stable code, the last
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

The top-level run creates a fresh Ed25519 deployment identity for each acquired
Lease. It stores only `encrypted-deployment-identity.json` in the handoff,
sealed to the repository variable `WK_CHAT_LIFECYCLE_WRAPPING_PUBLIC_KEY` and
bound to the request, Lease, source SHA, plan digest, and expiry. The Deployment
Action and finalizers may open it only inside the `cloud-deployment` Environment
with `WK_CHAT_LIFECYCLE_WRAPPING_PRIVATE_KEY`; plaintext deployment keys are
removed by always-run cleanup steps and are never repository secrets or
Artifacts. Before contacting any host, the Deployment Action validates the
exact normalized Ed25519 public-key set from the encrypted deployment identity
and the request-scoped Codex diagnostic identity against the digest recorded in
the Lease Receipt. A missing, substituted, or partial bootstrap key set fails
before SSH access.

Runtime Manager, worker, Analysis, and Demo credentials are generated inside
the Action, masked, installed as root-owned `0600` files, and deleted from the
runner and upload staging directories after activation. The read-only Manager
and Demo share one temporary password; Codex recovers it through the separate
request-scoped diagnostic SSH identity and hands it to the operator without
publishing plaintext in an Artifact. Worker and Analysis capabilities remain
separately scoped. Bootstrap public keys remain Lease-bounded; final credential
deletion and operator credential handoff are controlled by the top-level run
workflow.
