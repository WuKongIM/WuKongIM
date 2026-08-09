# Chat Lifecycle Run Flow

`chatlifecyclerun` owns the reviewed cross-stage policy that binds the four
operator inputs to a versioned repository Plan. It materializes generic Cloud
Lease input and public bootstrap identities, but it does not call a provider,
deploy hosts, run workers, or retain private credentials.

```text
versioned Run Plan template + four operator inputs + trusted workflow context
  -> validate exact source, operator, request, bundle, clock, and attempt
  -> bind immutable 12-hour rehearsal AutoRelease ceiling and aggregate budget ledger
  -> require released rehearsal_pass transition before formal materialization
  -> bind fresh 96-hour formal Lease to the same source, bundle, and ledger
  -> emit generic Cloud Lease Plan + public bootstrap access
```

Every stage has exactly one procurement attempt. Deployment and pre-clock
readiness repair reuse that exact Lease, bundle, and sealed identity while the
top-level orchestrator tries distinct request-bound protected-main control
revisions. There is no fresh-Lease deployment retry. The request shares one
aggregate cost ledger: CNY 1,350 is the operational admission stop and CNY
1,500 is the hard limit. The template fixes four Ubuntu x86 hosts, 4 vCPU/8 GiB,
40 GiB system disks, 500/200 GiB data disks, one 20 Mbps EIP, lease-long public
ports 22 and 80. The rehearsal template fixes a 12-hour AutoRelease ceiling, a
two-hour pre-clock readiness window, and a two-hour run. The formal template
fixes a fresh 96-hour Lease, the same two-hour readiness window, and a 72-hour
run. It is accepted only with a typed rehearsal transition containing exact
zero-inventory
proof, the same request/source/bundle identities, and the carried aggregate
commitment. The transition also carries the normalized public half of the
request-scoped Codex diagnostic identity, so a fresh formal Lease cannot switch
to a different local diagnostic owner. Runtime YAML owns the unchanged
workload and threshold details.

This package never accepts infrastructure quantities as command-line inputs.
The workflow supplies only trusted context derived from the protected
repository and the prior typed receipts. It derives and retains the exact
release selector from the admitted Plan/Quote before paid Acquire, so ambiguous
acquisition is cleanup-capable even when no Receipt Artifact survives.
