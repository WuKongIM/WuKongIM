---
scope: package
summary: Owns provider-neutral cloud Simulation Run identity, admission, lifecycle, access windows, finalization, and cleanup rules.
---

# Cloud Simulation Control Flow

## Responsibility

This package owns provider-neutral Simulation Run lifecycle policy. Cloud SDKs
and workflow-local assumptions cannot determine live inventory or release.
It does not own provider SDK calls, workflow execution, or observability sources.

## Boundaries

- Provider authority binds credential, provider, region, and account before
  cleanup inventory is interpreted or mutated.
- `RunLocator` is a bounded identity candidate. A released decision requires a
  matching locator and provider-confirmed empty resource array.
- Provisioning, bootstrap scripts, workflows, and monitoring adapters execute
  the plans; this package defines their domain transitions and evidence.

## Main Flows

1. Validate exact Run Identity, preset, immutable lease, budget, provider
   authority, capacity quote, storage calibration, tags, and current inventory
   before create or transition.
2. Advance only forward through provisioning, ready, running, and
   `analysis_grace`; persist the workload deadline and reconcile elapsed running
   state from provider truth.
3. Open or close bounded analysis/public windows, finalize from exact workload
   evidence, then destroy and recheck the provider-backed released gate; sweep
   reconciles every expired or cleanup-pending run.

## Invariants and Failure Semantics

- Durations are allowlisted; standard 48h/168h runs require completed storage
  calibration, margin, free-space reserve, and 168h cost confirmation.
- Runtime topology is 256 physical Hash Slots in 10 logical Slot Raft Groups.
  Bootstrap compares every node with the versioned scale contract; an elected
  healthy voter is authoritative even when PreferredLeader differs.
- Finalization schedules carry only exact identity and deadlines. In-progress
  evidence permits a bounded retry; unavailable terminal diagnosis still leads
  to exact cleanup and zero-inventory proof.
- Analysis ingress is one IPv4 `/32`, at most 50 minutes, and requires 30
  minutes of remaining lease. Public view is only TCP/19443 for ready, running,
  or grace and never outlives the lease.
- Monitor discovery is bounded, read-only, authority-validated, and requires
  exact preflight before public access; missing or ambiguous evidence fails closed.
- Second-provider admission requires reviewed real Alibaba workflow references
  and zero-residual proof; fake or unit-test evidence is insufficient.

## Read First

- [Control plane](control.go)
- [Run contracts](types.go)
- [Run locator](locator.go)
- [Multi-cloud gate](multicloud_gate.go)

## Update Triggers

Update this file when identity, lifecycle states, duration or storage admission,
topology, access windows, finalization, release proof, monitoring, or cloud gates change.
