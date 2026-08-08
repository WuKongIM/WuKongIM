# Alibaba Cloud Lease Flow

`internal/infra/cloudlease/alibaba` maps the provider-neutral Cloud Lease port
to Alibaba Cloud. Quote and paid lifecycle capabilities have separate API
interfaces and constructors so read-only jobs cannot reach mutation methods.

```text
cloudlease.Controller.Quote
  -> use case validates the approved workload Plan
  -> adapter maps supported cn-hangzhou PostPaid/x86/ESSD/EIP capabilities
  -> temporary OIDC role credentials
  -> STS GetCallerIdentity -> non-secret account hash
  -> ECS DescribeZones -> PostPaid ESSD-capable zones
  -> paginated DescribeInstanceTypes -> exact 4 vCPU / 8 GiB candidates
  -> DescribeAccountAttributes -> remaining PostPaid vCPU quota
  -> paginated Quota Center ListProductQuotas -> EIP headroom + retention-fee waiver proof
  -> DescribeAvailableResource -> WithStock NoSpot instance + requested ESSD ranges
  -> paginated DescribeImages -> latest official cloud-init Ubuntu 24.04 x86_64
  -> DescribePrice -> one-hour host+disk and pay-by-traffic EIP unit prices
  -> choose the lowest complete full-Lease estimate
```

```text
cloudlease.Controller.AcquireWithBootstrap
  -> exact paid-mutation authorization + temporary OIDC credentials
  -> idempotent tagged VPC, vSwitch, and one basic security group
  -> 3 private service hosts + 1 private load host, regular PostPaid/NoSpot
  -> Ubuntu cloud-init creates key-only wkdeploy access from 2 Ed25519 keys
  -> 40 GiB system + role-sized ESSD PL0 data disk per host
  -> one tagged 20 Mbps PostPaid PayByTraffic EIP on the load host
  -> one private-vSwitch rule + load-address-constrained typed public rules
  -> exhaustive provider inventory reconstruction -> active Receipt

Inspect / List / Release / Sweep
  -> list tagged roots with complete pagination
  -> traverse instance disks/ENIs, EIP association, security-group rules,
     NAT gateways, route tables, and custom route entries
  -> missing child tags are cleanup-only inherited identity, never healthy
  -> dependency-ordered deletion with a bounded 30-minute retry window
  -> success only after every declared inventory scope is observed empty
```

```text
one-time Cloud Lease identity setup
  -> existing complete repository AccessKey Secret pair (setup job only)
  -> exact GitHub repo + Environment + workflow@main + audience trust
  -> CloudLeaseProvisioner: Quote + Acquire + access-rule creation only
  -> CloudLeaseObserver: price, inventory, and delayed billing reads only
  -> CloudLeaseReleaser: tagged Release and Sweep only
  -> live STS role plus sole canonical policy, exact complete trust, and
     one-hour session proof for all three roles
```

The Quote API seam contains read methods only. EIP quota discovery exhaustively
paginates the provider's EIP product list, then locally requires one exact
documented quota-name and common-quota record whose paginated count is stable.
Provider-specific opaque action codes are retained only as bounded diagnostic
evidence. Missing pages,
repeated page tokens, malformed prices, incomplete or ambiguous quota, unknown
image provenance, or an unpriced eligible offer fail closed because any of them
could invalidate the claim that the chosen offer is cheapest. Every candidate uses regular
PostPaid/NoSpot capacity, one instance type for every host in the Plan, one ESSD
PL0 data disk per host, and one directly associated pay-by-traffic EIP. Workload
role names, host counts, disk sizes, and bandwidth are use-case decisions rather
than Alibaba adapter policy. Lease hours and public egress GiB are rounded upward
before cost admission. Quote reserves a 50-times-published-rate EIP retention
risk allowance for the full Lease plus four cleanup hours even when the
direct-ECS waiver is expected. The allowance carries a reviewed policy version
and expiry, after which Quote fails closed. Live account EIP quota must still be
at most 2,000 and have allocation headroom to prove waiver eligibility.
For one exact `DescribeAvailableResource` candidate, a successful provider body
that omits `AvailableZones` is authoritative no-stock evidence for that candidate;
a missing response body or API error still makes discovery unavailable.

`NewOpenAPIFromOIDCEnvironment` requires temporary AccessKey, secret, and
security-token variables. The adapter never accepts a long-lived credential
fallback in automated Quote jobs. `RequiredQuoteActions` is the exact RAM read
set consumed by this flow and is shared with the OIDC bootstrap work. The tagged
integration test also checks that STS reports the expected Quote role and that a
live role has exactly one attached custom policy whose active document matches
`QuoteRolePolicyDocument`. A sentinel `DeleteInstance` request with
`DryRun=true` must additionally return an exact RAM authorization error rather
than an arbitrary 403. These policy and permission probes are outside
`ReadAPI`; production Quote has no mutation method.

`NewLifecycleOpenAPIFromOIDCEnvironment` additionally requires the exact
`WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION=create-and-delete-paid-cloud-lease`
value. Ordinary Quote construction leaves the lifecycle guard false even
though clients share one SDK implementation. Provision, observe, and release
publish separate exact non-wildcard RAM action lists. EIP creation uses the
RAM-authorizable `AllocateEipAddress` operation with atomic tags; the similarly
named `AllocateEipAddressPro` operation is deliberately not used because its
official authorization table exposes no RAM action.

`NewInventoryOpenAPIFromOIDCEnvironment` is used only by the read-only Inspect
command under CloudLeaseObserver and does not accept the paid-mutation
authorization value. The separate identity bootstrap adapter accepts the
long-lived AccessKey pair only in the setup command, owns no infrastructure
create method, reconciles one provider plus three one-hour roles and policies,
and refuses removal while any repository-tagged Lease asset exists.

Acquire uses one Lease-owned VPC (`10.42.0.0/16`) and vSwitch
(`10.42.0.0/24`). No ECS instance receives a provider public IPv4 address and
no NAT gateway is created. A single security group is safe because public
quintuple rules include the load node's exact private `/32` destination. Every
host receives provider-native `AutoReleaseTime`, but that deadline is only a
backstop: Release and the 15-minute scheduled Sweep remain responsible for
deleting instances, disks and attachments, ENIs, EIP relationships, rules,
custom routes, unexpected NAT gateways, vSwitch, and VPC.
