# Chat Lifecycle Run Flow

`chatlifecyclerun` owns the reviewed cross-stage policy that binds the four
operator inputs to a versioned repository Plan. It materializes generic Cloud
Lease input and public bootstrap identities, but it does not call a provider,
deploy hosts, run workers, or retain private credentials.

```text
versioned Run Plan template + four operator inputs + trusted workflow context
  -> validate exact source, operator, request, bundle, clock, and attempt
  -> bind immutable six-hour rehearsal Lease and aggregate budget ledger
  -> require released rehearsal_pass transition before formal materialization
  -> bind fresh 96-hour formal Lease to the same source, bundle, and ledger
  -> emit generic Cloud Lease Plan + public bootstrap access
```

Attempt one has no placement exclusion and no committed retry cost. Attempt two
is valid only after a nonzero prior commitment and excludes exactly the first
zone/compute-type pair. Both attempts share one aggregate cost ledger: CNY
1,350 is the operational admission stop and CNY 1,500 is the hard limit. The template fixes four Ubuntu x86 hosts, 4 vCPU/8 GiB,
40 GiB system disks, 500/200 GiB data disks, one 20 Mbps EIP, lease-long public
ports 22 and 80. The rehearsal template fixes a six-hour Lease and a two-hour
run. The formal template fixes a fresh 96-hour Lease and a 72-hour run; it is
accepted only with a typed rehearsal transition containing exact zero-inventory
proof, the same request/source/bundle identities, and the carried aggregate
commitment. Runtime YAML owns the unchanged workload and threshold details.

This package never accepts infrastructure quantities as command-line inputs.
The workflow supplies only trusted context derived from the protected
repository and the prior typed receipts. It derives and retains the exact
release selector from the admitted Plan/Quote before paid Acquire, so ambiguous
acquisition is cleanup-capable even when no Receipt Artifact survives.
