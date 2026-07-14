# Cloud Simulation

The Cloud Simulation context describes time-bounded WuKongIM workloads that run against temporary cloud infrastructure and produce evidence for later diagnosis.

## Language

**Simulation Run**:
A time-bounded execution of a black-box workload against one temporary three-node cluster, identified independently from the GitHub workflow that starts it.
_Avoid_: Deployment job, long-running Action

**Provisioning Workflow**:
The manually started operation that requests a Simulation Run and records its identity; it does not stay active for the run's lifetime.
_Avoid_: Simulation job

**Analysis Run**:
A manually requested local Codex diagnosis of a live Simulation Run, independent from provisioning and workload execution.
_Avoid_: Deployment analysis, automatic analysis

**Released Simulation Run**:
A Simulation Run whose temporary compute resources have been destroyed and which can no longer accept an Analysis Run.
_Avoid_: Unreachable run, failed MCP

**Analysis MCP**:
The run-scoped capability boundary through which an Analysis Run obtains structured WuKongIM observability data and requests bounded diagnostics.
_Avoid_: SSH access, diagnostic scripts

**Analysis Skill**:
The repository-maintained diagnostic method that guides Codex in using the Analysis MCP, evaluating evidence, and deciding whether a code change is justified.
_Avoid_: MCP server, data collector

**Analysis Token**:
A short-lived credential bound to one Simulation Run, issued only after the Analysis Session Workflow proves its GitHub identity, and encrypted to the requesting local process.
_Avoid_: MCP secret, shared token

**Analysis Access Window**:
The bounded interval during which one authenticated local Codex client is authorized to reach a live Simulation Run's Analysis MCP.
_Avoid_: Public MCP endpoint, permanent ingress

**Deployment Access Window**:
The bounded interval during which the Provisioning Workflow may transfer and activate one immutable build through the simulator node.
_Avoid_: Permanent SSH, diagnostic access

**Deployment Bundle**:
The immutable, content-addressed build and configuration activated on every host in one Simulation Run.
_Avoid_: Source checkout, mutable latest build

**Cost Envelope**:
The caller-declared maximum estimated infrastructure cost that a Simulation Run may reserve before creating resources.
_Avoid_: Spot assumption, advisory budget

**Scenario Profile**:
A versioned `wkbench/v1` workload definition selected as a whole for one Simulation Run.
_Avoid_: Workflow parameter set, ad hoc load

**Diagnostic Budget**:
The per-Analysis-Run limit on active sampling time, concurrency, and returned observability data.
_Avoid_: Unbounded pprof, unrestricted query

**Diagnosis Result**:
The structured conclusion of one Analysis Run, including attribution, supporting observations, confidence, and remediation eligibility.
_Avoid_: Evidence Bundle, raw log archive

**Local Codex Authentication**:
The operator's ChatGPT subscription login retained only on the local device and never copied to GitHub or cloud hosts.
_Avoid_: GitHub API key, uploaded auth.json

**Trusted Source Revision**:
A source commit reachable from the repository's protected default branch and eligible for a Simulation Run.
_Avoid_: Arbitrary ref, fork commit

**Analysis Grace**:
The selected post-workload interval during which live diagnostics remain available before the immutable Run Lease expires.
_Avoid_: Retention backend, lease extension

**Live Observability Plane**:
The run-local metrics, logs, diagnostics, and bounded profiles available only while a Simulation Run exists.
_Avoid_: Evidence archive, historical backend

**Diagnostic Verdict**:
The single structured outcome of a local Analysis Run, independent of whether an optional remediation attempt completed.
_Avoid_: Agent exit status, narrative summary

**Infrastructure Preset**:
A provider-neutral minimum capacity class used to select allowlisted spot compute, network, and independent storage for a Scenario Profile.
_Avoid_: Provider SKU, arbitrary instance type

**Bootstrap Gate**:
The bounded convergence checks that must pass before active workload time begins.
_Avoid_: Process started, fixed sleep

**Run Identity**:
The exact, globally unique identifier used to tag, discover, analyze, and destroy one Simulation Run.
_Avoid_: Latest run, workflow run number

**Run-Specific MCP Configuration**:
The ephemeral local Codex command-line configuration that binds one diagnosis process to one live Analysis MCP and its allowlisted tools.
_Avoid_: Committed endpoint, shared MCP token

**Observation**:
A bounded, time-stamped result returned by an Analysis MCP tool with explicit source, completeness, and warnings.
_Avoid_: Raw dump, unscoped evidence

**Remediation Eligibility**:
The explicit determination that a Diagnosis Result is sufficiently attributable and testable to permit an isolated Draft-PR attempt.
_Avoid_: Suggested fix, anomaly detected

**Analysis Session**:
One bounded local diagnosis attempt with its own broker request, network window, token, timeout, and Diagnostic Budget.
_Avoid_: Persistent access, renewable session

**Encrypted Session Handoff**:
The request-correlated metadata, pinned public CA, and RSA-OAEP-encrypted Analysis Token transferred from the Analysis Session Workflow to one local process.
_Avoid_: Plaintext token artifact, Evidence Bundle

**Pinned Run Certificate**:
The simulator certificate whose public fingerprint is bound to one Run Identity through protected cloud inventory.
_Avoid_: Insecure TLS, shared certificate

**Run Locator**:
The minimal, non-diagnostic GitHub record that proves a Run Identity existed and identifies where its live cloud resources must be queried.
_Avoid_: Evidence Bundle, historical diagnostics

**Workload Execution**:
The single existing wkbench coordinator run whose bounded phases generate and evaluate one Scenario Profile on the simulator.
_Avoid_: Cloud daemon, dev-sim loop

**Internal Capability Token**:
A random, run-scoped credential authorizing one narrow private interaction among simulator services and cluster nodes.
_Avoid_: Administrator login, shared private token

**Cloud Account Bootstrap**:
The one-time, cloud-local creation of GitHub federation and least-privilege workflow roles before ordinary Actions can authenticate.
_Avoid_: Simulation provisioning, AccessKey setup

**Cloud Provider Adapter**:
The provider-specific lifecycle boundary for creating, discovering, admitting analysis access to, and releasing a Simulation Run.
_Avoid_: Cloud script, provider branch

**Cloud Account Binding**:
The one-time trust relationship that lets approved GitHub workflows obtain short-lived, role-scoped cloud credentials for Simulation Runs.
_Avoid_: Cloud account secret, access key

**Run Lease**:
The immutable cloud-side expiry and cleanup obligation attached to a Simulation Run when it is created.
_Avoid_: Workflow timeout, simulator timer

**Cloud Control Plane**:
The repository-owned lifecycle authority that reconciles Simulation Runs through Cloud Provider Adapters and provider inventory.
_Avoid_: Workflow script, Terraform state

**Infrastructure Interruption**:
A terminal Simulation Run outcome caused by unplanned loss of temporary cloud infrastructure rather than a WuKongIM diagnosis.
_Avoid_: Product failure, node replacement

**Run Network**:
The isolated private network and ingress boundary owned by one Simulation Run.
_Avoid_: Shared test VPC, public cluster network
