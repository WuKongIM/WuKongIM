# Automated Chat Lifecycle Cloud Simulation Design

**Status:** Approved

**Approved:** 2026-08-07

**Date:** 2026-08-07

**Operator:** tangtaoit

**Primary intent:** “开始聊天生命周期全流程压测”

## Problem Statement

WuKongIM needs an operator-driven but otherwise fully automated cloud test that
reproduces a real chat system over several days. New users must continuously
arrive, log in, synchronize their complete conversation list, exchange real
messages, create new person channels through first SEND, and later return to old
conversations. The workload must accumulate durable channel metadata while the
bounded runtime working set naturally moves through hot, cold, and reheated
states.

The repository already contains the native chat-lifecycle Soak and capacity
workload. It also contains a general Cloud Simulation control plane. The missing
capability is a safe, reusable, one-command path that can:

- quote and purchase temporary servers;
- deploy an exact WuKongIM revision without Docker;
- run a full-scale rehearsal, a fresh 72-hour formal Soak, and an aged-data
  capacity test;
- expose the Manager and Demo over HTTP for live human observation;
- let Codex inspect every host while a failure is still present;
- report cost, correctness, performance, and infrastructure limitations;
- collect bounded evidence; and
- destroy every temporary resource and prove that provider inventory is empty.

The operator must not have to manually buy servers, copy binaries, configure
services, watch GitHub Actions, or remember cleanup. Conversely, ordinary
questions such as status, analysis, or next-step requests must never imply
authorization to create billable resources.

The initial cloud implementation targets Alibaba Cloud in cn-hangzhou. It uses
four pay-as-you-go Ubuntu hosts: three WuKongIM nodes and one combined load,
coordination, monitoring, and public-access node. All four hosts use 4 vCPU and
8 GiB memory. The design intentionally accepts that this hardware may be too
small; if it is, the run reports an infrastructure-capacity limitation instead
of silently resizing the machines or misclassifying the result as a product
defect.

## Solution

Add a repository-owned orchestration layer around the existing
chat-lifecycle workload and the existing cloud-simulation foundations.
Procurement is factored into a reusable Cloud Lease module that knows nothing
about WuKongIM, Slot layout, or workload semantics. A separate Deployment
Action consumes a generic Lease Receipt plus an immutable deployment bundle
and a WuKongIM Deployment Plan. The top-level orchestration chains those
capabilities into the reviewed rehearsal, formal, capacity, diagnosis,
evidence, and cleanup lifecycle.

The normal user interface is a project-local skill named
wukongim-chat-lifecycle. It exposes start, status, stop, and diagnose intents.
Only an explicit start command authorizes the billable workflow. The skill
starts and monitors fixed workflows from trusted main; cloud and SSH mechanics
remain in repository code and Actions rather than in the skill.

One full command has this lifecycle:

1. Freeze the exact trusted source commit and build one content-addressed
   deployment bundle on a GitHub runner.
2. Validate the scenario, bundle, current inventory, quota, price, and aggregate
   Cost Envelope before any resource is created.
3. Acquire a rehearsal Cloud Lease, deploy it, pass the full readiness gate,
   run the exact full-scale workload for two hours, upload its report, and
   release the rehearsal lease.
4. If rehearsal reaches the typed rehearsal_pass outcome, acquire a completely
   fresh formal Cloud Lease and deploy the same source and workload.
5. Run the formal workload for 72 hours on one uninterrupted dataset, emitting
   a nonterminal qualification checkpoint at hour 24.
6. After a valid hour-72 checkpoint, run the capacity staircase for at most
   eight hours on the same aged dataset, then execute a 30-minute recovery at
   2,000 SEND/s.
7. Collect bounded final evidence, run analysis when required, upload the final
   Artifact, release the lease, and keep reconciling until all related cloud
   inventory is absent.

Remote systemd services own the multi-day workload. A GitHub-hosted runner does
not remain alive for 72 hours. GitHub Issues and a run-scoped local Codex
monitor provide asynchronous progress and alerts.

## User Stories

### Start the complete test with one explicit command

As the operator, I want to say “开始聊天生命周期全流程压测” so that Codex
performs prerequisite checks, purchases temporary infrastructure, deploys the
system, starts the workload, monitors it, analyzes important failures, and
cleans everything up without requiring me to drive each step.

Acceptance criteria:

- The exact start phrase, or an explicit project-skill start invocation, is
  treated as authorization for one Cost Envelope of at most ¥1,500.
- Codex creates a unique request identifier and opens one tracking Issue.
- Missing prerequisites are reported precisely and are the only reason Codex
  asks the operator for input.
- No status, diagnose, stop, explanation, or next-step request creates a
  billable resource.
- A second chat-lifecycle start is rejected while one is active.

### Reuse procurement independently of WuKongIM

As a repository maintainer, I want temporary-server procurement and destruction
behind a generic Cloud Lease boundary so that other repository workloads can
reuse it without depending on chat-lifecycle concepts.

Acceptance criteria:

- The Cloud Lease contract contains no WuKongIM, Slot, worker, channel, or
  scenario fields.
- The first provider adapter is Alibaba Cloud, while the core contract remains
  provider-neutral.
- Quote performs no mutation.
- Acquire, Inspect, GrantAccess, RevokeAccess, Release, and Sweep have typed,
  idempotent behavior.
- A Lease Receipt contains generic host, disk, network, tag, and expiry
  inventory but contains no private key, password, AccessKey, or bearer token.
- Release succeeds only after provider inventory proves all related resources
  are absent.

### Deploy through a dedicated Deployment Action

As the operator, I want deployment to remain a GitHub Action so that every host
receives the same auditable build and Codex does not have to perform the normal
installation manually.

Acceptance criteria:

- The build finishes before procurement and produces an immutable bundle with a
  SHA-256 digest.
- Cloud hosts do not clone source, compile Go, install Docker, or pull a mutable
  image.
- The Deployment Action receives only a Lease Receipt, deployment bundle
  identity, WuKongIM Deployment Plan, and temporary deployment credential.
- The Deployment Action cannot create or delete cloud resources.
- It emits a typed Deployment Receipt and structured failure evidence.
- A deployment or pre-clock readiness failure retains the exact acquired Lease
  and publishes typed repair evidence instead of releasing and repurchasing
  hosts.
- The top-level orchestrator waits for a distinct protected-`main` control
  revision, then invokes the Deployment Action again with the exact same Lease
  Receipt, immutable bundle, and sealed per-Lease SSH identity.
- Same-Lease repair is bounded by the aggregate CNY 1,350 operational stop,
  operator stop, the immutable Lease expiry, and the time still required for
  deployment, readiness, the measured stage, and its one-hour release reserve.
- A repair that requires a different product source or bundle is not silently
  substituted into the acquired Lease. It is terminal for that Lease because
  its provenance no longer matches; cleanup must prove zero inventory before a
  separately authorized run can acquire another Lease.

### Exercise real login and complete conversation synchronization

As a WuKongIM maintainer, I want every simulated login to follow the real
client startup flow so that the test stresses accumulated conversation state
instead of reusing benchmark-only cursors.

Acceptance criteria:

- Every login establishes a real WKProto connection and waits for CONNACK.
- Every login then starts `/conversation/list` with `completed_coverage=0` and
  an empty cursor, follows bounded 200-candidate pages until `done=true`, and
  retries bounded unresolved keys through `/conversation/retry`.
- No completed coverage, directory cursor, or per-channel message cursor is
  retained between sessions.
- Realtime traffic begins for that session only after its synchronization
  response passes validation.
- Each virtual user remains below 500 conversations; reaching the limit is a
  scenario failure, not silent truncation.

### Prove channel accumulation and natural hot/cold/reheat behavior

As a WuKongIM maintainer, I want new person channels to be created through real
SEND traffic and later cool and reheat naturally so that the run exposes
metadata-growth and runtime-lifecycle defects.

Acceptance criteria:

- About 250,000 new users and one million unique person channels are introduced
  per day at the standard rate.
- Person channel metadata is never pre-created by the benchmark setup API.
- Natural idle eviction, not forced benchmark eviction, establishes cold state.
- Sampled channels prove loaded state, complete unload on all three nodes,
  reactivation, and message-sequence continuity.
- The formal report distinguishes historical durable-channel growth from the
  bounded loaded runtime working set.

### Observe the live run through Manager and Demo

As the operator, I want HTTP access to WuKongIM Manager and the chat Demo so
that I can watch the cluster and manually try a small amount of real traffic
during the run.

Acceptance criteria:

- The load node exposes both applications over plain HTTP through its native
  reverse proxy.
- Manager uses a healthy service-node upstream and a temporary read-only
  manager identity.
- Demo HTTP and WebSocket traffic is distributed across the three service
  nodes.
- One random per-Lease username and password is used for Manager login and Demo
  Basic Authentication.
- Codex gives the operator the exact URLs and credential after deployment.
- Demo traffic does not count toward workload correctness or throughput
  denominators, while its real host impact remains visible in resource metrics.

### Follow progress without keeping a terminal or runner open

As the operator, I want progress and significant alerts in one GitHub Issue so
that a multi-day test remains understandable after the start command returns.

Acceptance criteria:

- The tracking Issue records stage transitions, immutable source identity,
  Lease identities, warnings, checkpoint outcomes, current cost estimate, and
  cleanup state.
- Only failures, capacity/resource warnings, disk warnings, budget events, and
  the final outcome mention tangtaoit.
- Times are shown in both UTC and Asia/Shanghai where humans act on them.
- A run-scoped Codex monitor checks the Issue and workflow/run state every 30
  minutes.
- The monitor remains active until provider inventory is zero.

### Detect undersized hardware without changing it

As the operator, I want the test to start with 4 vCPU and 8 GiB on every host
and tell me when that choice is insufficient so that I decide whether a later
run should use larger machines.

Acceptance criteria:

- The system never automatically changes instance type, CPU count, memory, disk
  size, or workload rate to hide a capacity warning.
- Sustained server or load-node saturation is recorded with per-process and
  host evidence.
- A functionally correct run with hardware limitations is reported as
  passed_with_capacity_warning rather than a clean performance pass.
- A latency breach with clear hardware headroom remains a product failure.
- When attribution cannot be established, the verdict is
  insufficient_evidence.

### Find capacity on the aged formal dataset

As a WuKongIM maintainer, I want a bounded throughput staircase after the
72-hour Soak so that the capacity result reflects accumulated channel and
conversation data.

Acceptance criteria:

- The capacity stage does not restart WuKongIM, clear data, reset identities, or
  rebuild the cluster.
- It begins at 2,000 SEND/s.
- Coarse steps increase by 25 percent, each with ten minutes of stabilization
  and twenty minutes of measurement.
- After the first failing step, refinement targets ten-percent precision between
  the last passing and first failing rates.
- The entire capacity search is capped at eight hours.
- If no boundary is found, the result is explicitly a lower bound.
- After the boundary, the workload returns to 2,000 SEND/s for 30 minutes and
  must recover without restart.

### Diagnose failures before destruction

As the operator, I want Codex to inspect a failed live run before cleanup so
that the final report contains evidence-based attribution rather than guesses.

Acceptance criteria:

- On a diagnosable runtime failure, new traffic stops and services/data are not
  restarted, cleaned, or mutated.
- Codex uses the existing wukongim-cloud-analysis skill and read-only Analysis
  MCP first.
- A separate per-Lease diagnostic SSH key permits bounded read-only fallback
  inspection when the MCP or final upload path is insufficient.
- The diagnostic window is at most two hours and remains bounded by disk,
  budget, and immutable lease expiry.
- Diagnosis does not automatically edit code, open a pull request, or rerun the
  paid workload.

### Stop immediately and still clean up

As the operator, I want an explicit stop command to halt the active test and
release resources without another confirmation so that I can cap cost or end an
unhelpful run.

Acceptance criteria:

- The exact project-skill stop intent stops new traffic immediately.
- Evidence collection is allowed for at most ten minutes.
- Cleanup begins without a second confirmation.
- The workload outcome is operator_stop.
- The command does not report success until provider inventory is zero; if
  cleanup is still pending it says so plainly.

### Bound spend across every stage and repair

As the operator, I want one aggregate budget for the full command so that a
deployment repair window or later formal Lease cannot silently reset cost
authorization.

Acceptance criteria:

- The hard Cost Envelope is ¥1,500 across rehearsal, formal, capacity, all
  disks, EIP traffic, and billable same-Lease deployment repair time.
- At an estimated aggregate cost of ¥1,350 the orchestrator safely stops new
  workload and reserves the remaining ¥150 for bounded evidence collection,
  billing lag, and release.
- Quote and admission use conservative current estimates and refuse before
  procurement when the envelope cannot contain the plan.
- Destroying a pay-as-you-go instance stops future use charges but does not
  promise a refund for already consumed compute, disk, or traffic.
- Final provider billing is retained for later reconciliation when available.

### Prove cleanup rather than assuming it

As the operator, I want cleanup to prove the absence of every temporary
resource so that a green workflow cannot conceal continuing charges.

Acceptance criteria:

- Release checks ECS instances, system and data disks, EIP and bandwidth
  resources, security groups and rules, vSwitch, VPC, ENIs, and other attached
  resources related by tags or provider relationships.
- Immediate release retries continuously for 30 minutes.
- If inventory is not empty, the Issue remains open and a scheduled sweeper
  retries every 15 minutes.
- Native instance scheduled release is an independent safety layer.
- The tracking Issue closes only after a zero-inventory receipt exists.

## Implementation Decisions

### Relationship to existing specifications

This specification composes and narrows two existing designs:

- Chat Lifecycle Soak Design defines workload correctness, traffic, lifecycle,
  checkpoints, and aged-data capacity behavior.
- Cloud Simulation GitHub Actions Design defines the repository-owned cloud
  control plane, trusted source boundary, remote execution, live evidence, and
  cleanup principles.

Where this document is silent, those accepted contracts continue to apply.
Where it differs, this document is the controlling decision only for the
automated chat-lifecycle cloud flow.

The following differences are intentional and must not be silently “corrected”
back to the earlier defaults:

| Earlier decision | This flow |
| --- | --- |
| Seven independent formal hosts: three service, three worker, one coordinator | Four hosts: three service nodes and one combined load/coordinator/monitor host |
| Spot compute | PostPaid pay-as-you-go compute only |
| 1 TB service data disks | 500 GiB service data disk on each service node |
| Temporary runner-specific SSH ingress | SSH is key-only and open to 0.0.0.0/0 for the bounded Lease |
| No standing public HTTP application | Manager and Demo are exposed over HTTP on port 80 for the bounded Lease |
| Exact durable-create equality | Expected test creates must be present; extra creates caused by Demo activity are recorded rather than failed |
| Resource saturation invalidates the whole run immediately | Declared saturation thresholds create capacity warnings unless correctness, availability, or evidence becomes invalid |
| Cloud identity may use AccessKey for ordinary runs | AccessKeys may bootstrap OIDC once; this new flow uses short-lived OIDC roles for ordinary operations |

The implementation must add or supersede ADRs for these scoped conflicts before
the corresponding behavior is merged. Existing general Cloud Simulation
workflows remain compatible unless an ADR explicitly migrates them.

### Domain boundaries

The design introduces these terms:

- Cloud Lease: a temporary, immutable-expiry grant of generic cloud
  infrastructure with a cleanup obligation.
- Lease Plan: a versioned generic request describing compute, storage, network,
  region, expiry, budget, and tags.
- Lease Receipt: a non-secret provider inventory returned by Cloud Lease
  operations.
- Deployment Plan: the versioned WuKongIM-specific topology, configuration,
  service, proxy, observability, and readiness intent.
- Deployment Receipt: the non-secret proof that an exact bundle and plan were
  activated on an exact Lease.
- Chat Lifecycle Run: the complete one-command chain containing rehearsal and
  formal Leases, formal Soak, capacity search, evidence, and cleanup.

Cloud Lease is a reusable infrastructure capability. It must not import or
encode chat-lifecycle behavior. Deployment is a consumer of Lease Receipt.
Workload orchestration is a consumer of Deployment Receipt. The top-level
orchestrator owns cross-stage state, aggregate budget, retry policy, Issue
updates, and final outcome.

The existing Simulation Run remains the term for one workload execution against
one temporary cluster. A Chat Lifecycle Run therefore contains a rehearsal
Simulation Run and, if admitted, a separate formal Simulation Run.

### Fixed operator surface

The top-level orchestration accepts only:

- source_sha;
- operator, fixed to tangtaoit for the first version;
- codex_diagnostic_pubkey;
- request_id.

Infrastructure, workload, duration, budget, thresholds, region, disk size,
network exposure, and retry settings are versioned repository Plans. They are
not runtime overrides. The project skill may generate request_id and the
diagnostic key pair, but it must not synthesize an unreviewed Plan.

The source defaults to the exact current origin/main commit. A supplied commit
must be immutable, reachable from trusted main, and paired with workflow and
control code from protected main. Moving branch names and mutable “latest”
artifacts are not deployment identities.

No CI Review Agent or model review is part of this flow. Automated compilation,
unit tests, contract tests, static workflow checks, and bundle validation remain
required engineering gates; they are validation, not a second design reviewer.
Cloud environments have no human approval step.

### Cloud Lease contract

The generic interface consists of Quote, Acquire, Inspect, GrantAccess,
RevokeAccess, Release, and Sweep.

- Quote validates the Lease Plan, discovers current eligible inventory, quota,
  and pricing, and returns a bounded estimate without creating resources.
- Acquire is idempotent on repository and request identity. It either returns
  the same exact Lease or a conflict; it never creates a duplicate set after an
  ambiguous retry.
- Inspect reconstructs truth from provider inventory and tags rather than a
  workflow-local state file.
- GrantAccess and RevokeAccess manage typed ingress grants for consumers that
  need them. The chat-lifecycle Plan separately declares lease-long port 22 and
  port 80 exposure.
- Release is idempotent, removes the complete dependency graph, and returns
  residual inventory until it can prove zero.
- Sweep discovers expired or cleanup-pending leases by mandatory tags and
  reconciles them without requiring the workflow that created them.

Every resource is tagged with at least the Lease identity, request identity,
repository, operator, provider, region, resource role, source SHA where
applicable, Plan digest, bundle digest where applicable, creation time, and
immutable expiry. Provider tag limitations must not weaken exact discovery.

Cloud Lease supports only temporary resources. It has no subscription,
keep-alive, renewal, or conversion-to-long-lived-server operation. Expiry is
immutable.

### Alibaba placement and selection

The first adapter is Alibaba Cloud. The fixed region is cn-hangzhou. The adapter
chooses one availability zone dynamically from current eligible inventory and
places all four hosts in the same run-owned VPC, vSwitch, and zone.

The selected instance must be:

- PostPaid pay-as-you-go, not Spot or subscription;
- x86_64;
- exactly 4 vCPU and 8 GiB memory;
- non-burstable;
- compatible with the required ESSD disks and official Ubuntu image; and
- the lowest current eligible total-price choice that passes quota and
  availability checks.

All four hosts use the same instance type. If no exact candidate fits the
remaining aggregate Cost Envelope, acquisition stops before mutation.
Deployment repair reuses the selected hosts and therefore does not perform a
second placement selection.

The image is the latest provider official, cloud-init-compatible Ubuntu 24.04
LTS x86_64 point image available at quote time. Its exact image identifier is
recorded in the Lease Receipt. Image selection must be allowlisted and
auditable, not a free-form marketplace search.

### Host topology and storage

The Lease contains exactly four ECS instances:

| Role | Count | Compute | System disk | Data disk |
| --- | ---: | --- | --- | --- |
| WuKongIM service/data node | 3 | 4 vCPU, 8 GiB | 40 GiB ESSD | 500 GiB ESSD PL0 each |
| Load/coordinator/monitor/public node | 1 | 4 vCPU, 8 GiB | 40 GiB ESSD | 200 GiB ESSD PL0 |

Each service node stores WuKongIM data on its independent 500 GiB disk. The
formal preflight requires at least 500,000,000,000 usable bytes on every
service data filesystem. The load-node data disk stores Prometheus, bounded
evidence, reports, and working artifacts.

Every monitored system or data filesystem triggers coordinated safe stop at
less than five percent free space. Prometheus retention is 96 hours with a
15-second scrape interval. At 140 GB of Prometheus storage, collection triggers
safe stop and finalization; 150 GB is a hard local cap. Storage is never
automatically expanded. A storage stop is attributed to
infrastructure_capacity and is reported to the operator.

### Slot and replication topology

The cluster always retains 256 physical hash slots. The number 12 refers to the
formal workload's logical Slot Raft/coverage groups, not a reduction to 12
physical hash slots. Lifecycle sampling, creation accounting, placement
coverage, and worker assignment span those 12 logical groups over the full
256-slot hash space.

Slot replication is three and channel replication is three. Every deployment,
including rehearsal, is a three-node cluster. No special standalone or
single-node business path is allowed.

### Network and public access

The three WuKongIM service nodes have private addresses only and no NAT route.
They do not download packages or artifacts. Cluster, metrics, pprof, manager
backend, benchmark, and node-control traffic stays inside the Run Network.

Only the load node receives one EIP. It uses 20 Mbps peak public bandwidth with
pay-by-traffic billing. Workload traffic between load processes and WuKongIM
uses private addresses and therefore does not depend on the EIP bandwidth.

The load-node security boundary deliberately permits:

- TCP port 80 from 0.0.0.0/0 for Manager and Demo over HTTP; and
- TCP port 22 from 0.0.0.0/0 for key-only SSH.

HTTPS and a custom domain are not required. SSH password authentication and
root password login are disabled. Only explicit per-Lease public keys are
authorized. All public rules and keys end with the Lease and are included in
zero-inventory/credential cleanup evidence.

This open ingress is an accepted test-environment risk and a scoped exception
to the existing Deployment Access Window ADR. It does not authorize public
exposure of service-node ports, cloud credentials, pprof, raw Prometheus,
benchmark APIs, or writable Manager administration.

### Operating system, time, and native services

All hosts run Ubuntu 24.04 LTS x86_64 and native systemd units. Docker, Docker
Compose, container registries, and container images are excluded from cloud
deployment.

Every dependency needed by a service node is bundled by the build or proved to
exist in the selected image. Service nodes do not run apt against the public
internet. A missing required base tool is a deployment failure.

Hosts use UTC and systemd-timesyncd with a VPC-reachable time source. Readiness
requires measured drift of at most one second across all four hosts.

The load node runs:

- three separate wkbench worker systemd services;
- one chat-lifecycle coordinator;
- Prometheus;
- node and process observation;
- bounded report collection;
- the Analysis MCP gateway; and
- one native HTTP/WebSocket reverse proxy.

The three workers share the 4 vCPU and 8 GiB host without CPU pinning or fixed
quotas. Each worker, the coordinator, Prometheus, proxy, and collector still
exports independent process-resource observations.

Each service node runs one WuKongIM process plus host/process observation.
WuKongIM and the formal workload do not automatically restart after exit.
A process exit remains visible and terminal rather than being hidden by
systemd restart.

### Build and Deployment Action

Before Quote or Acquire, an unprivileged GitHub build job:

- resolves and verifies the trusted source SHA;
- builds all required Go binaries;
- builds Manager and Demo static assets;
- packages configuration templates, systemd units, proxy configuration,
  observability binaries, and required offline dependencies;
- validates the bundle without background services; and
- publishes a content-addressed bundle and digest.

The same binaries and frontend assets are installed across the applicable
hosts. No host builds source.

The Deployment Action:

- validates Lease Receipt and Plan identities;
- transfers the bundle to the load node and through it to private service
  nodes;
- verifies the digest on every host;
- writes root-readable configuration and systemd environment files;
- mounts and verifies data disks;
- activates native services;
- validates time, disk, process, cluster, Slot, proxy, Manager, Demo,
  Prometheus, and worker readiness; and
- emits a Deployment Receipt with no secret material.

It cannot call Acquire, Release, or other billable cloud mutations. On failure
it emits a stable failure code, last successful gate, bounded logs, and known
host state. The top-level orchestrator owns release and the bounded same-Lease
repair loop. A failed Deployment Action never acquires or releases resources.

### Authentication and credential handling

Ordinary cloud operations for this flow use GitHub OIDC with three separate
least-privilege roles:

- CloudLeaseProvisioner for Quote, Acquire, and approved access-rule creation;
- CloudLeaseObserver for read-only inventory, state, price, and billing
  observation; and
- CloudLeaseReleaser for Release and Sweep of correctly tagged resources.

The corresponding GitHub Environments are cloud-lease-provision,
cloud-lease-observe, cloud-lease-release, and cloud-deployment. They have exact
workflow/branch subjects, fixed concurrency, and no human approvers. Deployment
has no Alibaba permission; it uses SSH only.

On the first start, the project skill checks whether the OIDC binding and roles
exist and pass a live identity test. If not, it uses the existing complete
Alibaba AccessKey Secret pair named ALIBABA_CLOUD_ACCESS_KEY_ID and
ALIBABA_CLOUD_ACCESS_KEY_SECRET once to bootstrap OIDC, configure non-secret
GitHub Variables, verify the identities, and continue. A partial or missing
pair fails before procurement and tells the operator exactly what is needed.
Existing AccessKey Secrets are not automatically deleted because other
workflows may still depend on them. After bootstrap, ordinary runs for this
flow do not use them.

Cloud hosts receive no Alibaba or GitHub credential.

A long-lived asymmetric wrapping key is created during setup. Its public half
is a GitHub Variable and its private half is a cloud-deployment Environment
secret. Per-Lease GitHub-side SSH material and UI credentials may be retained
only as request-correlated encrypted Artifacts decryptable inside approved
deployment, monitor, and finalization jobs. Plaintext never enters logs,
summaries, Issues, tags, or Artifacts.

Each Lease has two distinct SSH identities:

- A GitHub deployment/monitor identity supports deployment, evidence rescue,
  finalization, and stop. Its private key is encrypted for the approved Action
  path and deleted after release is proven.
- A Codex diagnostic Ed25519 identity is generated locally. Only its public key
  is sent to the workflow. Its private key is stored with mode 0600 outside the
  repository in the local wukongim-leases directory, is never uploaded to
  GitHub, and is deleted after release and zero inventory.

The temporary Manager/Demo username and password is generated per Lease. The
plaintext exists only in the approved deployment process and the operator's
local Codex lease state. GitHub retains only an encrypted form. It is deleted
locally after release.

### Manager and Demo routing

The load-node proxy exposes exact Manager and Demo URLs through the EIP on
port 80. It selects a healthy WuKongIM service-node upstream for Manager and
fails closed when no healthy node exists. The Manager identity is read-only and
must not expose configuration mutation, service restart, or destructive
operations.

Demo static assets are served locally by the load node. Its HTTP API and
WebSocket connections are balanced across all three service nodes. Safe GET
requests may fail over to a different healthy upstream. Writes and WebSocket
messages are never automatically replayed or retried by the proxy.

Manual Demo traffic is allowed but excluded from workload accounting:

- only payloads bearing the exact run/workload marker enter SEND, receive,
  retry, loss, duplication, latency, and throughput denominators;
- durable metadata reconciliation requires actual successful creates per
  logical Slot group to be greater than or equal to expected marked test
  creates;
- excess creates are recorded as external_demo_activity rather than a failure;
- missing expected creates, metadata errors, duplicate marked persistence,
  corruption, loss, or sequence regression still fail; and
- Demo CPU, memory, network, storage, queue, and cache impact remains part of
  observed machine behavior and cannot be subtracted.

This means unrestricted Demo use weakens exact attribution for excess metadata
creation but does not weaken correctness of marked test traffic.

### Fixed chat-lifecycle workload

Rehearsal and formal stages use the same standard full-scale profile. Only the
stage duration and fresh Lease identity differ.

The fixed profile contains:

- 10,000 concurrent online sessions;
- about 250,000 new users per day;
- 2,000 primary SEND/s;
- three independent workers on the load node;
- 12 logical Slot coverage groups across 256 physical hash slots;
- Slot replica count three and channel replica count three;
- 90 percent person traffic and 10 percent fixed-group traffic;
- 1,600 groups with 5–20 members;
- 300 groups with 100–500 members;
- 99 groups with 1,000–10,000 members; and
- one 100,000-member group with its separately reported canary.

The existing deterministic identity, relationship, lifecycle, payload, retry,
correctness, bounded-evidence, hot/cold/reheat, and no-historical-cardinality
contracts remain unchanged.

Every login performs a full conversation synchronization from zero. This
“no retained state” rule applies to simulated client conversation cursors; it
does not remove the GitHub and provider receipts required to recover and clean
up the cloud lifecycle.

### Readiness and workload clock

No rehearsal, formal, or capacity duration starts when a process merely becomes
active. The workload clock begins only after all of these are simultaneously
true:

- the exact deployment digest is verified on all four hosts;
- all expected systemd services are active;
- all three WuKongIM nodes are ready and the cluster membership converges;
- all 256 physical slots have valid leaders and expected replicas;
- every one of the 12 logical coverage groups is observable;
- all three workers are ready;
- all Prometheus and host/process targets are up;
- time drift is at most one second;
- service and data filesystems pass their size/free-space gates;
- Manager and Demo proxy checks pass;
- 10,000 sessions completed CONNECT and full conversation synchronization; and
- the first full 2,000 SEND/s grant was delivered to the workers.

Readiness has a bounded deadline. A timeout enters the same-Lease deployment
repair window after the dormant stage coordinator is stopped. Active duration
and cost accounting are separate: cost begins when billable resources are
acquired, while test duration begins only at the completed readiness gate.

### Stage lifecycle

The rehearsal Lease has an immutable 12-hour expiry. This is an AutoRelease
ceiling that leaves a real bounded control-repair window; successful or terminal
orchestration still releases it immediately, so PostPaid billing follows actual
hold time rather than intentionally retaining it for 12 hours. It starts from empty data,
runs the full profile for exactly two hours after readiness, emits bounded
evidence, and is always released before the formal Lease is acquired.

Two hours cannot satisfy the six-hour heap trend, 24-hour goroutine, hour-24,
or hour-72 formal gates. A successful rehearsal therefore produces the distinct
rehearsal_pass outcome, never pass. Resource-capacity warnings are attached to
the rehearsal but do not alone block formal acquisition. Correctness,
availability, evidence, scenario, disk, budget, or deployment failures do.

The formal Lease has an immutable 96-hour expiry and starts from empty data on
fresh resources. It runs continuously for 72 hours. Hour 24 is a nonterminal
qualification checkpoint. A fatal hour-24 result stops the run; a valid
checkpoint continues on the same data and process history through hour 72.

After a valid hour-72 checkpoint, the capacity stage starts on the same
processes and aged dataset. It is bounded to eight hours, followed by the
30-minute recovery interval. Normal completion releases immediately; the
remaining lease time is not consumed merely because it was authorized.

No stage resumes after a WuKongIM, worker, or coordinator exit. Evidence from
separate process lifetimes is never spliced into one formal verdict.

### Capacity staircase

The capacity stage changes only the primary offered SEND rate. Online users,
login/full-sync behavior, new-user and channel growth, lifecycle distribution,
group ratios, payloads, retries, correctness verification, and observation
remain active.

Each coarse step is 1.25 times the previous offered rate, beginning at 2,000
SEND/s. Each step uses ten minutes for stabilization and twenty minutes for its
measured window. Once a boundary is observed, refinement selects rates between
the highest pass and first failure until the interval is no wider than ten
percent or the eight-hour stage deadline is reached.

A capacity step may fail because delivered rate, latency, queues, CPU, memory,
network, storage, or service health cannot sustain that rate. Such a boundary
does not rewrite an already frozen 72-hour functional verdict. Any message
loss, duplicate persistence, payload corruption, sequence regression, terminal
SEND failure, or unrecovered cluster failure remains a product failure even
during overload.

The final 30-minute recovery uses 2,000 SEND/s. Failure to return to the formal
health, latency, error, queue, and lifecycle bounds without restart is a product
failure.

### Correctness and performance verdicts

The existing zero-tolerance correctness requirements remain authoritative:

- zero confirmed loss;
- zero duplicate durable persistence;
- zero payload corruption;
- zero sequence regression;
- zero terminal SEND failures after retry;
- zero max-channel activation rejection; and
- all expected marked durable person-channel creates present.

Existing first-attempt error-rate, hot SEND latency, cold activation latency,
full-sync latency, cluster, placement, heap, goroutine, queue-floor, natural
cooling, and evidence gates remain in force except for the attribution rule
below.

The fixed error and latency thresholds are:

| Signal | Whole-run/window requirement |
| --- | --- |
| First-attempt SEND failure rate | Below 0.01 percent for the whole run |
| One-minute first-attempt SEND failure rate | At most 0.1 percent |
| Loaded hot-channel SEND to SENDACK | p99 at most 200 ms and p99.9 at most 1 s |
| New or unloaded cold-channel activation | p99 at most 2 s and p99.9 at most 5 s |
| Login full conversation synchronization | p99 at most 1 s and p99.9 at most 3 s |
| Long-tail anomaly capture | Every operation beyond 10 s |

A latency threshold is sustained when it is breached for five consecutive
minutes. Whole-run aggregation must not conceal a bad window. The attribution
rules below decide whether such a breach is a product failure, an
infrastructure-capacity warning, or insufficient evidence.

The first two workload hours are the resource warmup baseline. After warmup,
forced-GC live heap must not grow more than five percent in any rolling six-hour
window, the hour-24 goroutine baseline must not grow more than five percent,
loaded Channel runtime count must follow the bounded hot set rather than
historical channel count, and bounded queues must repeatedly return to their
stable floor. Rehearsal records these signals but cannot satisfy the longer
formal windows.

The resource-capacity warning thresholds are:

- CPU above 90 percent for 15 continuous minutes;
- memory above 85 percent for 15 continuous minutes;
- a bounded queue above 80 percent for 15 continuous minutes; or
- the load node being unable to deliver the scheduled rate.

Crossing one captures evidence and continues while correctness, cluster
availability, evidence validity, disk safety, and budget remain intact. The
final functional outcome becomes passed_with_capacity_warning unless a stronger
terminal outcome occurs.

Latency attribution follows this order:

- a sustained latency breach accompanied by sustained relevant server or load
  saturation is infrastructure_capacity and continues as a warning when safe;
- a sustained latency breach with clear generator and server headroom is
  product_failure; and
- a sustained latency breach without enough evidence to distinguish the two is
  insufficient_evidence.

The existing general cloud monitor's direct failure thresholds of CPU above 85
percent, RSS above 80 percent, queues above 80 percent, or disk above 70 percent
must not be reused blindly. This scenario needs its own two-tier warning/fatal
policy.

Fatal safe-stop conditions include correctness failure, OOM, WuKongIM or
workload process exit, sustained cluster unavailability, disk below five
percent free, Prometheus reaching 140 GB, aggregate budget stop, and immutable
expiry risk. Hardware insufficiency without a fatal condition is reported, not
automatically repaired.

### Failure, repair, retry, and diagnosis policy

Procurement is not retried after a valid active Lease Receipt exists.
Deployment or pre-clock readiness failure keeps that exact Lease active and
enters a bounded repair loop. Each failed Deployment Action publishes its typed
failure code, last successful gate, exact child run, exact Deployment Action
control SHA, and repair deadline on the request Issue without publishing host
addresses or credentials. The orchestrator then waits for a different
protected-`main` revision whose commit message has the exact
`Chat-Lifecycle-Repair: <request_id>` trailer and re-dispatches the Deployment
Action with the same Lease artifact run/name, bundle artifact run/name,
diagnostic public key, and encrypted deployment identity. One distinct control
SHA is attempted at most once, so a persistent defect cannot create an
unbounded dispatch loop.

The repair loop stops and releases the exact Lease when the operator requests
stop, the aggregate conservative spend reaches CNY 1,350, the Lease no longer
has enough time for one bounded Deployment Action plus readiness, the full
measured stage, and the one-hour release reserve, or the orchestrator loses
safe control. Release remains selector-bound and must end in authenticated
zero-inventory proof. A workflow/job interruption also prefers immediate exact
Release; provider AutoRelease and the 15-minute sweeper remain independent
backstops.

The original product source SHA and content-addressed bundle stay immutable
during same-Lease repair. Protected-main changes may fix Deployment Action
orchestration and repository control scripts because the Deployment Action
authenticates the original upstream artifacts independently of its current
control SHA. If diagnosis shows that the product binary, frontend, or sealed
bundle payload itself must change, the current Lease is released and the run is
terminal; a new paid run requires a new explicit start authorization.

There is no automatic retry for rehearsal workload, formal workload,
qualification, 72-hour Soak, capacity, recovery, correctness, runtime process,
disk, budget, or operator-stop outcomes.

For a runtime failure that can still be inspected:

1. stop new workload traffic;
2. do not restart or mutate services;
3. freeze bounded logs and metrics around the event;
4. invoke the repository cloud-analysis skill;
5. allow at most two hours for evidence and read-only diagnosis; and
6. finalize and release.

Disk, budget, expiry, and provider safety deadlines may shorten that window.
If report upload fails, the orchestrator retries every ten minutes for at most
two hours while the lease remains safe. Codex may use its diagnostic SSH key to
rescue bounded evidence. Failure to upload does not authorize keeping resources
beyond the budget or expiry.

### Cost accounting

The ¥1,500 Cost Envelope is an aggregate authorization for the complete Chat
Lifecycle Run, not a per-Lease amount. The ledger includes:

- rehearsal compute, disks, EIP, and traffic;
- formal compute, disks, EIP, and traffic;
- capacity and recovery time;
- all billable hold time spent repairing deployment on the current Lease;
- evidence/diagnostic retention; and
- cleanup and provider billing delay.

Preflight obtains a conservative quote using current eligible instance price,
disk price, declared peak runtime, EIP bandwidth/traffic assumptions, and
known provider billing granularity. Quote failure or uncertainty that cannot be
bounded fails closed before acquisition.

The operational stop is ¥1,350 estimated aggregate spend. The remaining ¥150
is a reserve, not permission to continue the workload. The estimate accrues
from quoted rates multiplied by actual held time, allocated disks, and observed
traffic, with a conservative allowance for delayed provider billing data.
Budget never resets after release or retry.

### Evidence, reporting, and tracking

Every command creates one GitHub tracking Issue. It is the human control record,
not the provider state authority. Each Lease and Simulation Run remains
identified exactly by receipts, tags, and immutable source/Plan digests.

The final Artifact is retained for 90 days and contains:

- request, source, scenario, Plan, bundle, Lease, topology, image, and selected
  instance identity;
- conservative and provider-reported cost evidence;
- two-hour rehearsal report;
- hour-24 qualification, hour-72 Soak, capacity, and recovery reports when
  reached;
- bounded Prometheus metric slices;
- bounded service, worker, coordinator, proxy, and system log windows;
- bounded profiles and Diagnosis Result references;
- Manager/Demo external-activity accounting;
- warnings, failure classification, and unresolved evidence; and
- cleanup receipts proving zero inventory.

The full Prometheus TSDB is not uploaded. Intermediate patrol, request, and
encrypted handoff Artifacts are retained for eight days. Secrets, raw
credentials, unbounded logs, raw message contents, and complete payloads are
excluded.

The final Artifact may be assembled from rehearsal and formal evidence after
the rehearsal Lease has already been destroyed. Upload of each Lease's minimum
survival report is therefore a gate before that Lease is released, subject to
the two-hour rescue policy.

### Cleanup and expiry

The rehearsal Lease expires after 12 hours and the formal Lease after 96
hours. Expiry cannot be extended. Every compute instance receives provider
native scheduled release where supported. An independent scheduled sweeper
runs every 15 minutes.

Normal completion, any terminal failure, budget stop, and operator stop invoke
Release immediately after the bounded evidence window. Release actively retries
for 30 minutes. Residual inventory then remains cleanup_pending, mentions the
operator, and is swept until empty.

Neither workflow success nor a provider “delete accepted” response proves
cleanup. Only an Inspect/Release result showing zero related instances, disks,
EIP resources, rules, network objects, ENIs, and attachments is released. The
tracking Issue and local monitor stop only after that proof.

### Project-local skill behavior

The wukongim-chat-lifecycle skill is a concise intent and orchestration layer.
It supports:

- start: perform/setup prerequisites and dispatch the full paid chain;
- status: inspect the exact active request and summarize stage, health,
  warnings, cost, and cleanup without mutation;
- stop: perform the immediate coordinated operator stop and cleanup;
- diagnose: delegate one exact live Simulation Run to
  wukongim-cloud-analysis.

The skill does not embed Alibaba API calls, SSH deployment logic, scenario
configuration, or cleanup algorithms. It invokes fixed repository tools and
workflows, validates exact identities, presents Manager/Demo access locally,
and maintains the run-scoped 30-minute Codex monitor. If the desktop automation
facility is unavailable, the workflow and Issue still progress; the Issue
becomes the fallback notification surface.

## Testing Decisions

### Highest-value seams

Testing follows the highest stable boundaries rather than trying to unit-test
workflow YAML alone.

1. Cloud Lease port and provider contract through a fake provider. This proves
   Quote purity, Acquire idempotency, ambiguous retry handling, mandatory tags,
   immutable expiry, access grants, aggregate budget admission, release
   ordering, residual inventory, and Sweep.
2. Alibaba adapter read-only integration. This proves authentication, paginated
   inventory, candidate filtering, quote parsing, quota, zone/image discovery,
   and permissions without creating paid resources.
3. Deployment Action contract. This proves Lease Receipt and bundle-digest
   validation, offline bootstrap generation, no provider mutation, systemd
   layout, disk/time gates, proxy configuration, structured receipts, and
   dry-run failure reporting.
4. Top-level workflow contract. Static and no-background dry runs prove trusted
   main selection, fixed inputs, environment separation, concurrency, cost
   ledger propagation, one-retry behavior, Issue updates, failure precedence,
   evidence retention, and unconditional cleanup dispatch.
5. Existing wkbench black-box seams. Focused unit, integration, and E2E tests
   continue to prove the chat-lifecycle Soak, full login synchronization,
   natural hot/cold/reheat behavior, correctness, checkpoints, capacity, and
   bounded evidence.
6. Project-skill forward tests. Fixtures prove start authorization, non-billable
   status/diagnose behavior, immediate stop, exact identity propagation, missing
   prerequisite messages, and deletion of local credentials after release.

### Test tiers

Default unit tests use fake providers, fake clocks, deterministic randomness,
and fixture receipts. They must not access Alibaba Cloud or create resources.

Integration tests may perform read-only Alibaba discovery, quote, quota, image,
and OIDC permission checks. They may build binaries, render systemd/proxy
configuration, and start local processes only under the repository integration
tag and timeout rules.

Process-level E2E tests remain black-box and follow the repository E2E
instructions. Scale-reduced native three-node tests prove the deployment and
workload contracts without claiming cloud capacity.

Ordinary CI never buys infrastructure. The first paid end-to-end acceptance is
the explicit two-hour full-scale rehearsal authorized through the project
skill. A successful rehearsal does not automatically waive the requirement to
inspect its report before treating the automation as production-ready.

### Required failure coverage

Tests must cover at least:

- unavailable quote, quota, zone, exact instance type, or Ubuntu image;
- price over budget and budget carry-over across retry;
- partial resource creation and ambiguous Acquire response;
- deployment failure before and after service activation;
- first Lease cleanup failure followed by sweeper recovery;
- missing/offline dependency on a private service node;
- disk mount, disk size, free-space, Prometheus-cap, and time-drift failures;
- one worker, coordinator, service node, proxy, Prometheus, and collector exit;
- full-sync validation failure before traffic;
- load-node saturation, server saturation, and ambiguous latency attribution;
- Demo traffic excluded from marked workload counters with excess metadata
  recorded;
- report upload failure and two-hour rescue deadline;
- operator stop at every stage;
- native expiry plus delayed scheduled cleanup;
- encrypted credential Artifact mismatch or expiration;
- local diagnostic key permissions and deletion; and
- zero-inventory proof including detached disks, EIP, rules, ENIs, vSwitch, and
  VPC.

### Repository documentation

Implementation must update applicable FLOW documents when behavior lands,
including the cloud control plane, chat-lifecycle coordinator, benchmark
surface, and Manager/proxy integration. It must also update the project
glossary, relevant ADRs, example Plans, runbook, and project knowledge. Scripts
that build binaries, start or signal processes, use real waits, open listeners,
or exercise retries belong in integration-tagged tests.

## Out of Scope

- Cloud providers other than Alibaba Cloud in the first implementation.
- Spot instances, subscription instances, long-lived servers, Lease renewal, or
  converting a Lease into a permanent environment.
- More than one concurrent Chat Lifecycle Run.
- Docker or container-based cloud deployment.
- Automatic instance, disk, or bandwidth resize.
- Automatic workload reduction to make 4 vCPU and 8 GiB appear sufficient.
- Automatic code changes, remediation pull requests, merges, or CI Review
  Agent approval.
- A paid formal run as part of ordinary CI or as an automatic consequence of
  merging implementation.
- Automatic deletion or rotation of the repository's pre-existing Alibaba
  AccessKey Secrets.
- Public HTTPS, certificate management, a custom domain, public pprof, public
  Prometheus, or public service-node access.
- Grafana, Loki, ELK, cloud object storage, or upload of the full Prometheus
  TSDB.
- Planned node loss, Slot migration, network partition, disk-pressure fault
  injection, or Spot-reclaim testing during the clean Soak.
- Exact attribution of every extra metadata create while unrestricted manual
  Demo traffic is allowed.
- Removing Demo resource impact from observed host metrics.
- Resuming or splicing a formal result after any core process exit.
- Automatic refund claims for consumed pay-as-you-go resources.

## Further Notes

### Decision records

The implementation boundary is reinforced by these accepted, scoped ADRs:

- ADR 0039 extracts the reusable Cloud Lease boundary.
- ADR 0040 separates Deployment from Cloud Lease lifecycle.
- ADR 0041 selects four PostPaid hosts for automated chat lifecycle.
- ADR 0042 exposes only the load node for the bounded Cloud Lease.
- ADR 0043 bootstraps chat-lifecycle OIDC from the existing AccessKeys.
- ADR 0044 separates GitHub Deployment and local Codex SSH identities.
- ADR 0045 excludes manual Demo traffic from workload verdicts.
- ADR 0046 reports attributable resource saturation as a capacity warning.
- ADR 0047 retains one acquired Lease across bounded Deployment Action repair.

The affected existing ADRs contain explicit pointers to these scoped
exceptions, so the general Cloud Simulation behavior is not silently changed.

### Implementation order

Implementation may be delivered internally in this dependency order, but the
feature is not complete until the entire approved scope is present:

1. generic Cloud Lease contracts, fake provider, receipts, and zero-inventory
   cleanup;
2. Alibaba Quote/Acquire/Inspect/Release/Sweep and OIDC bootstrap;
3. immutable offline bundle and Deployment Action;
4. four-host topology, proxy, Manager/Demo, and observability;
5. chat-lifecycle orchestration, cost ledger, rehearsal/formal/capacity state
   machine, Issue reporting, and evidence;
6. project-local skill and run-scoped Codex monitor;
7. paid two-hour rehearsal, cleanup drills, then the first explicitly
   authorized formal run.

No implementation phase may create paid resources merely to validate ordinary
unit or integration code.

### Completion criteria

The implementation is complete only when:

1. the generic Cloud Lease fake-provider suite proves idempotent acquisition and
   leak-free cleanup behavior;
2. Alibaba read-only discovery and OIDC role checks pass;
3. an explicit paid rehearsal deploys four Ubuntu hosts without Docker, reaches
   the complete readiness gate, runs the full profile for two hours, uploads
   evidence, and proves zero inventory after release;
4. Manager and Demo are reachable over the returned HTTP URLs with the
   temporary credential while private service-node surfaces remain private;
5. every simulated login performs and validates a zero-coverage full
   conversation synchronization before traffic;
6. the orchestration can execute a fresh 72-hour formal Soak followed by the
   bounded aged-data capacity and recovery stages;
7. resource saturation produces the reviewed capacity-warning attribution while
   correctness and headroom-backed latency failures remain product failures;
8. operator stop, budget stop, disk stop, same-Lease deployment repair, report rescue, and
   scheduled Sweep have verified failure paths;
9. the final Artifact contains bounded evidence and zero-inventory proof without
   secret leakage; and
10. all applicable unit, integration, E2E, documentation, FLOW, and workflow
    contract checks pass.

### Approval gate

This document is the implementation boundary. The operator approved it by
continuing to implementation on 2026-08-07. Any material change to workload,
topology, public exposure, credential ownership, Cost Envelope, retry,
attribution, evidence, or cleanup semantics requires a clearly identified
specification revision before implementation proceeds.
