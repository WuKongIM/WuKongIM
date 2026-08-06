# Alibaba Cloud Lease Flow

`internal/infra/cloudlease/alibaba` maps the provider-neutral Cloud Lease port
to Alibaba Cloud. The current implementation is intentionally Quote-only; the
mutation and inventory methods remain fail-closed until #800.

```text
cloudlease.Controller.Quote
  -> use case validates the approved workload Plan
  -> adapter maps supported cn-hangzhou PostPaid/x86/ESSD/EIP capabilities
  -> temporary OIDC role credentials
  -> STS GetCallerIdentity -> non-secret account hash
  -> ECS DescribeZones -> PostPaid ESSD-capable zones
  -> paginated DescribeInstanceTypes -> exact 4 vCPU / 8 GiB candidates
  -> DescribeAccountAttributes -> remaining PostPaid vCPU quota
  -> Quota Center GetProductQuota -> EIP headroom + retention-fee waiver proof
  -> DescribeAvailableResource -> WithStock NoSpot instance + requested ESSD ranges
  -> paginated DescribeImages -> latest official cloud-init Ubuntu 24.04 x86_64
  -> DescribePrice -> one-hour host+disk and pay-by-traffic EIP unit prices
  -> choose the lowest complete full-Lease estimate
```

The Quote API seam contains read methods only. Missing pages, repeated page
tokens, malformed prices, incomplete quota, unknown image provenance, or an
unpriced eligible offer fail closed because any of them could invalidate the
claim that the chosen offer is cheapest. Every candidate uses regular
PostPaid/NoSpot capacity, one instance type for every host in the Plan, one ESSD
PL0 data disk per host, and one directly associated pay-by-traffic EIP. Workload
role names, host counts, disk sizes, and bandwidth are use-case decisions rather
than Alibaba adapter policy. Lease hours and public egress GiB are rounded upward
before cost admission. Quote reserves a 50-times-published-rate EIP retention
risk allowance for the full Lease plus four cleanup hours even when the
direct-ECS waiver is expected. The allowance carries a reviewed policy version
and expiry, after which Quote fails closed. Live account EIP quota must still be
at most 2,000 and have allocation headroom to prove waiver eligibility.

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
