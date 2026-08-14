# FLOW.md Agent Navigation Design

## Problem

WuKongIM introduced package-local `FLOW.md` files so an Agent could acquire a
useful mental model before reading or changing a complex module. The idea is
sound: ownership, ordering, failure semantics, and cluster invariants are often
expensive to reconstruct from individual functions. The current contract has
nevertheless grown beyond that original purpose.

At repository commit `168bca71aca9f3412974441de03b37ab1509dae7`, the tree has:

- 83 `FLOW.md` files;
- 13,527 physical lines of FLOW content;
- a 61-line median and a 163-line mean;
- 19 files longer than 150 lines;
- one 1,267-line file; and
- same-directory FLOW coverage for 79 of 198 Go package directories under
  `cmd`, `internal`, and `pkg`.

Several short files are valuable because they capture a security boundary,
state machine, compatibility rule, or ownership transfer that is not obvious
from an API. The main problem is therefore not the raw number of files. It is
the absence of a precise admission rule, scope model, size budget, authority
model, and freshness contract.

Large files such as `internal/bench/chatlifecycle/FLOW.md`,
`pkg/cluster/FLOW.md`, `internal/infra/cluster/FLOW.md`, and
`internal/app/FLOW.md` now act as second implementation manuals. Hierarchical
files repeat the same route at several layers. Volatile metric, tuning, route,
and implementation detail makes semantic drift likely. Requiring an Agent to
read stale or duplicated prose can be worse than providing no local guide.

At the captured baseline, the loading implementation also treated every
`FLOW.md` as if it applied recursively to every descendant. In particular,
`internal/runtime/reviewagentverify.DiscoverInstructions` scopes both
`AGENTS.md` and `FLOW.md` solely from their directory path. This differs from
the selected model below, in which FLOW scope is explicit and package-local by
default.

## Goals

- Preserve fast, local Agent orientation for genuinely complex modules.
- Make `FLOW.md` advisory navigation rather than normative policy.
- Keep every FLOW small enough to read before code exploration.
- Make package versus subtree applicability deterministic.
- Keep high-value, non-derivable knowledge while removing repeated API and
  implementation inventories.
- Detect structural drift automatically and semantic drift during review.
- Generate one repository-wide catalog without creating another hand-written
  source of truth.
- Migrate the existing catalog incrementally without losing unique knowledge.

## Non-goals

- Replacing code, tests, type contracts, ADRs, runbooks, or operational
  documentation.
- Creating a FLOW for every package.
- Making Markdown prove semantic correctness automatically.
- Adding timestamps, owner fields, source commit hashes, or permanent size
  exceptions to each file.
- Fetching external links during validation.
- Rewriting all existing FLOW files in one unreviewable change.

## Selected Knowledge and Authority Model

| Artifact | Role | Authority |
| --- | --- | --- |
| `AGENTS.md` | Mandatory repository and scoped engineering rules | Normative |
| Code, types, schemas, and tests | Executable behavior and compatibility facts | Authoritative for implementation |
| `FLOW.md` | Small Agent navigation card for a complex module | Advisory |
| ADR and `PROJECT_KNOWLEDGE.md` | Stable cross-module decisions and business knowledge | Explanatory decision record |
| Runbooks and operational docs | Procedures, diagnostics, and environment-specific operation | Procedural |
| Generated FLOW index | Discoverability projection from FLOW metadata | Non-authoritative generated view |

When these sources disagree, an Agent must apply this precedence:

```text
AGENTS.md mandatory rule
  -> executable code / schema / test fact
  -> accepted ADR or stable project knowledge
  -> FLOW.md navigation statement
  -> generated index
```

The precedence does not make FLOW content safe to ignore as model input.
`FLOW.md` remains exact-revision, protected Agent context: a malicious or stale
navigation file can still mislead a model. Issue Agent and Review Agent must
continue to freeze and validate its source identity, while clearly labeling
the content advisory rather than policy.

## FLOW Admission

A new or retained `FLOW.md` must satisfy at least one of these criteria:

1. The module owns a state machine, concurrency model, persistence model, or
   distributed consistency rule.
2. The module is a critical cross-layer, protocol, credential, publication, or
   composition boundary.
3. Correct work depends on a business invariant that is difficult to infer
   from exported types and tests.
4. An incorrect change could cause data loss, compatibility failure, cluster
   failure, uncontrolled fanout, security exposure, or billable cloud action.
5. Agents have repeatedly misunderstood the module's ownership or main route,
   and the misunderstanding cannot be removed more directly through code or
   API design.

DTO-only packages, ordinary thin adapters, straightforward utilities, test
fakes, and packages whose useful description is an exported API inventory do
not qualify by default. A short file is not automatically valuable, and a
large or important package does not automatically need a FLOW.

Admission is a semantic review decision. The structural validator must not try
to infer complexity from package size or imports.

## Required File Contract

Every FLOW uses a strict, minimal front matter:

```yaml
---
scope: package
summary: Owns channel append authority and per-channel writer execution.
---
```

The metadata contract is deliberately narrow:

- `scope` is exactly `package` or `subtree`.
- `summary` is one trimmed, non-empty English line of at most 160 printable
  ASCII bytes that states ownership, not a slogan.
- No other key is accepted.
- General YAML features, multiline scalars, aliases, and nested values are not
  accepted; tooling parses this as a closed two-field front-matter subset.

The body uses these headings in this order:

```markdown
# <module> Flow

## Responsibility

## Boundaries

## Main Flows

## Invariants and Failure Semantics

## Read First

## Update Triggers
```

All sections must contain meaningful content. The template has these semantic
limits:

- `Responsibility` says what the module owns and explicitly does not own.
- `Boundaries` identifies the important callers, dependencies, and ownership
  transfers without copying a complete import graph.
- `Main Flows` contains one to three control/data flows only.
- `Invariants and Failure Semantics` records non-derivable ordering, fencing,
  backpressure, persistence, compatibility, and fail-open/fail-closed rules.
- `Read First` links one to five repository files or focused documents that
  are the best code-reading entrypoints.
- `Update Triggers` states which behavioral changes make the FLOW inaccurate.

The whole file, including front matter and blank lines, targets at most 100
physical lines. More than 100 lines produces a validation warning. More than
150 lines is a validation failure with no waiver or allowlist.

FLOW content must be English. Existing non-English files migrate when they are
otherwise normalized or changed; translation alone does not justify a large
standalone patch.

## Content That Does Not Belong in FLOW

FLOW files must not become a second copy of facts that are cheaper and more
reliable elsewhere. Exclude:

- complete directory trees, route registries, exported API lists, struct
  fields, and function-by-function walkthroughs;
- volatile metrics catalogs, every histogram stage, tuning histories, and
  benchmark result interpretation;
- test commands already owned by `AGENTS.md`, named-check policy, or a runbook;
- environment setup, incident response, cloud operator procedures, and other
  operational steps;
- implementation plans, rollout status, historical phases, or completed
  migrations;
- duplicated global dependency rules already present in `AGENTS.md`; and
- policy language that attempts to override code, tests, or `AGENTS.md`.

Detailed knowledge can remain in a package-specific design note when locality
matters, but FLOW must summarize and link to it instead of embedding it.

## Scope Semantics

`scope: package` applies only to files whose containing directory is the FLOW
directory. For a non-Go module such as `docs-site`, `package` means that exact
directory-local module.

`scope: subtree` applies to the containing directory and every descendant. A
closer descendant FLOW supplements the parent. It does not implicitly replace
the parent and can never override an applicable `AGENTS.md`.

For one selected target path, applicability is resolved as follows:

1. Find FLOW candidates in the target directory and its ancestor directories.
2. Read the exact-directory FLOW when it declares `package` or `subtree`.
3. Read an ancestor FLOW only when it declares `subtree`.
4. Present applicable FLOW files from broadest scope to narrowest scope.
5. Reject malformed or ambiguous metadata once enforcement is enabled.

An Agent must perform this resolution before deeply analyzing, designing, or
modifying a package. Broad file discovery, symbol search, repository inventory,
and `rg` output that merely mentions a path do not trigger mandatory FLOW
loading. Once a package becomes an actual work target, loading is mandatory.

This distinction prevents one repository-wide search from requiring all 83
documents while preserving just-in-time context where it matters.

## Agent Context Integration

Issue Agent and Review Agent currently inventory `AGENTS.md` and `FLOW.md`
together as instruction blobs. The migration must preserve exact-source
freezing but separate their meaning and scope.

### Discovery

- `AGENTS.md` keeps its existing directory-recursive instruction semantics.
- FLOW discovery considers only exact-directory and ancestor candidates for a
  changed or selected path.
- The loader reads candidate FLOW front matter before deciding whether an
  ancestor is applicable.
- `scope: package` is selected only for exact-directory changes.
- `scope: subtree` is selected for exact-directory and descendant changes.
- Multiple changed paths are deduplicated into one stable, sorted FLOW set.
- Candidate metadata and content come from the same exact base/control tree;
  the loader must not combine a current-worktree header with a frozen blob.

The candidate set is bounded by changed-path ancestor depth rather than by all
FLOW files in the repository. This permits explicit scope without downloading
all navigation content into every review context.

### Prompt labeling and precedence

Model context must present `AGENTS.md` as mandatory instructions and
`FLOW.md` as advisory module navigation. The prompt must state the precedence
defined in this design and require the model to report a FLOW conflict rather
than silently preferring it over code or tests.

The internal contract may continue using a common frozen-blob transport if
changing the DTO would add no safety, but the blob name and prompt projection
must not call FLOW content normative instructions.

### Protection

Changes to FLOW files remain Agent-control-plane-sensitive because their text
is injected into model context. Existing exact-source digesting, bounded
content, path validation, and protected Review Agent treatment remain in
force. Advisory authority is not permission to accept arbitrary or unfrozen
prompt content.

## Validation and Generated Index

A deterministic repository tool owns both validation and index rendering. It
must support:

- validation without writing;
- rendering the canonical index to stdout or one explicit repository path;
- checking that the committed index equals canonical output; and
- an inventory/report mode used during migration.

Validation checks:

- closed front-matter schema and scope enum;
- required heading presence and order;
- printable-ASCII, single-line `summary` bounds (body-language compliance
  remains a semantic review responsibility);
- 100-line warning and 150-line failure;
- relative repository links and `Read First` targets exist;
- no duplicate FLOW path or generated index entry; and
- the committed generated index is current.

External URLs are syntax-checked only; validation performs no network access.
The validator does not claim to prove that prose matches behavior.

The generated file is `docs/development/FLOW_INDEX.md`. It contains only:

| Field | Source |
| --- | --- |
| Path | Repository discovery |
| Scope | FLOW front matter |
| Summary | FLOW front matter |
| Lines | Validator count |
| Budget status | Derived from the 100/150 thresholds |

The generated file carries a do-not-edit marker. Agents may use it for
repository-wide orientation, but known-package work reads the applicable FLOW
directly and does not require the index first.

During the report-only migration phase, a legacy file without front matter is
rendered with temporary `legacy-subtree` scope and the summary `Metadata
migration pending.`. This preserves the historical loader behavior without
inventing a hand-written summary. Both temporary values disappear when the
strict baseline is enforced.

After migration, the structural contract is exposed through one named Review
Agent check, `flow-doc-contracts`. Review policy remains the sole executable
catalog for its actual arguments and limits; documentation refers only to the
check name.

## Semantic Freshness

Every FLOW's `Update Triggers` must specialize this shared baseline. A review
must consider a FLOW update when a change modifies:

- module responsibility or ownership;
- a public or cross-layer dependency boundary;
- one of the documented main control/data flows;
- ordering, fencing, concurrency, lifecycle, backpressure, or shutdown
  semantics;
- durable state, compatibility, idempotency, retry, or failure classification;
- a security, credential, publication, or billable-action boundary; or
- a `Read First` entrypoint that moved or ceased to be authoritative.

Private helper renames, test-only refactors, derived API additions, and metric
implementation changes do not require a FLOW edit unless they alter a listed
invariant or make an existing statement false.

There is deliberately no rule that every code change must touch FLOW. Such a
rule creates meaningless churn and does not prove semantic freshness. Review
Agent and human reviewers compare behavioral changes with the applicable
`Update Triggers`; structural tooling enforces only what can be proven
deterministically.

## Migration Strategy

Migration is incremental and keeps the old read-before-work rule until the
scope-aware loader and a compliant baseline are ready.

### Phase 1: foundation in report mode

1. Add the deterministic validator/index renderer and focused tests.
2. Add the generated index.
3. Update Agent context discovery to understand FLOW front matter and advisory
   precedence while accepting legacy files during the transition.
4. Update the root `AGENTS.md` rule to distinguish discovery from deep
   analysis and to describe the transition.
5. Run structural validation in report mode; do not fail on legacy files yet.

### Phase 2: eliminate hard-limit violations

Rewrite the 18 retained files currently over 150 lines. Move detailed route,
metric, runbook, and implementation material into the existing appropriate
documents or focused new design notes. Retire `internal/FLOW.md` separately
after its unique cross-module knowledge is reconciled with `AGENTS.md`,
`PROJECT_KNOWLEDGE.md`, and the generated index.

### Phase 3: normalize retained files

Add metadata and the required template to the remaining admitted FLOW files.
Files between 101 and 150 lines should be reduced toward the 100-line target
while they are being normalized.

### Phase 4: retire non-admitted files

Move unique invariants from the six retirement candidates to the listed owner,
then delete the redundant FLOW. Git history is not a substitute for moving a
still-valid invariant before deletion.

### Phase 5: enforce

1. Remove legacy parser compatibility.
2. Enable 150-line and schema failures.
3. Require canonical generated-index equality.
4. Register and require the `flow-doc-contracts` named check for changes to
   `FLOW.md`, its validator, the generated index, Agent context discovery, or
   the governing root rule.

## Current Migration Inventory

This inventory is a design-time classification, not authorization to delete
files without reading them again at their exact implementation revision.
Physical line counts are from the snapshot named in the Problem section.

### Retain and rewrite below 150 lines

| Current file | Lines | Proposed scope | Migration focus |
| --- | ---: | --- | --- |
| `internal/access/api/FLOW.md` | 266 | `package` | Keep adapter responsibilities and compatibility boundaries; move route catalog and phase detail. |
| `internal/access/manager/FLOW.md` | 655 | `package` | Keep authentication/write-replay boundaries and primary manager flows; move route-by-route behavior. |
| `internal/access/node/FLOW.md` | 595 | `package` | Keep RPC ownership, fencing, and error semantics; move complete RPC inventory. |
| `internal/app/FLOW.md` | 709 | `package` | Keep composition ownership, lifecycle order, and critical wiring seams; move component inventory. |
| `internal/bench/FLOW.md` | 768 | `subtree` | Keep benchmark subsystem map and global bounded-work invariants; move workload/metric catalogs. |
| `internal/bench/chatlifecycle/FLOW.md` | 1,267 | `package` | Keep paid-run state machine, authority, cleanup, and terminal semantics; move operator/runbook and evidence detail. |
| `internal/infra/cluster/FLOW.md` | 835 | `package` | Keep adapter ownership, routed authority, and failure mapping; move method-by-method routes. |
| `internal/runtime/channelappend/FLOW.md` | 489 | `package` | Keep authority writer lifecycle, admission, idempotency, and post-commit invariants; move metric-stage detail. |
| `internal/usecase/management/FLOW.md` | 820 | `package` | Keep orchestration boundaries and irreversible-operation semantics; move endpoint/operation catalog. |
| `internal/usecase/message/FLOW.md` | 182 | `package` | Keep permission order, batching, append delegation, and projection failure rules; move observation detail. |
| `internal/usecase/plugin/FLOW.md` | 241 | `package` | Keep hook recursion, lifecycle, and host boundary invariants; move RPC/API inventory. |
| `pkg/channel/FLOW.md` | 415 | `subtree` | Keep channel runtime model, replication/commit invariants, and package map; move detailed observation and retention mechanics. |
| `pkg/channel/reactor/FLOW.md` | 510 | `package` | Keep event domains, lifecycle state machine, fencing, and worker completion ownership; move exhaustive event detail. |
| `pkg/cluster/FLOW.md` | 890 | `subtree` | Keep cluster responsibility, route authority, lifecycle, and major proposal/read flows; move API and diagnostics inventories. |
| `pkg/controller/FLOW.md` | 213 | `subtree` | Keep Controller ownership, Raft/apply order, task and backup state invariants; move detailed feature history. |
| `pkg/db/message/FLOW.md` | 155 | `package` | Keep durable layout, idempotency, snapshot, and lifecycle semantics; make a small focused reduction. |
| `pkg/gateway/FLOW.md` | 401 | `subtree` | Keep session/protocol ownership, backpressure, and connection lifecycle; move exhaustive interface/config detail. |
| `pkg/slot/FLOW.md` | 396 | `subtree` | Keep Slot FSM ownership, routing, metadata invariants, and snapshot semantics; move complete operation catalog. |

### Retain and normalize

| Current file | Lines | Proposed scope | Note |
| --- | ---: | --- | --- |
| `cmd/wkcloudanalysisbridge/FLOW.md` | 18 | `package` | Retain the pinned-TLS and loopback-only security boundary. |
| `cmd/wkcloudbundle/FLOW.md` | 22 | `package` | Retain the trusted offline-bundle boundary. |
| `cmd/wkcloudlease/FLOW.md` | 40 | `package` | Retain billable command separation and mutation authorization boundaries. |
| `cmd/wkcloudleaseoidc/FLOW.md` | 20 | `package` | Retain identity bootstrap and secret-handling boundaries. |
| `docs-site/FLOW.md` | 129 | `subtree` | Retain publishing ownership; reduce toward the 100-line target. |
| `internal/access/cloudanalysismcp/FLOW.md` | 41 | `package` | Retain the closed-world, read-only/active-diagnostics tool boundary. |
| `internal/access/cloudview/FLOW.md` | 48 | `package` | Retain irreversible write replay and benchmark-purity semantics. |
| `internal/access/gateway/FLOW.md` | 100 | `package` | Retain protocol mapping, presence, send, and entry-error boundaries. |
| `internal/access/issueagentcli/FLOW.md` | 20 | `package` | Retain the strict JSON process and credential-output boundary. |
| `internal/access/opsmcp/FLOW.md` | 32 | `package` | Retain authentication, forwarding revalidation, and closed tool registry. |
| `internal/access/reviewagentcheckmcp/FLOW.md` | 25 | `package` | Retain named-check isolation and no-arbitrary-command guarantees. |
| `internal/access/reviewagentcli/FLOW.md` | 23 | `package` | Retain strict control/model-result normalization boundaries. |
| `internal/contracts/backup/FLOW.md` | 77 | `package` | Retain bounded cross-node backup and restore DTO invariants. |
| `internal/contracts/channelappend/FLOW.md` | 48 | `package` | Retain immutable ownership and authority-fence contracts. |
| `internal/contracts/issueagent/FLOW.md` | 33 | `package` | Retain bounded JSON and publication-authority separation. |
| `internal/contracts/onlinedelivery/FLOW.md` | 16 | `package` | Retain the hot-path immutable ownership transfer. |
| `internal/contracts/reviewagent/FLOW.md` | 26 | `package` | Retain signed generation and bounded model-result boundaries. |
| `internal/infra/backup/FLOW.md` | 85 | `package` | Retain repository, export, restore, and failure-recovery boundaries. |
| `internal/infra/cloudanalysis/FLOW.md` | 59 | `package` | Retain bounded read-only HTTP adapter behavior. |
| `internal/infra/clouddeploy/FLOW.md` | 16 | `package` | Retain no-follow filesystem and atomic-bundle safety semantics. |
| `internal/infra/cloudlease/alibaba/FLOW.md` | 124 | `package` | Retain paid provider invariants; reduce toward the 100-line target. |
| `internal/infra/delivery/FLOW.md` | 38 | `package` | Retain owner-fence validation and retryable/dropped classification. |
| `internal/infra/issueagentgithub/FLOW.md` | 52 | `package` | Retain GitHub authority, source freezing, and publication fences. |
| `internal/infra/reviewagentgithub/FLOW.md` | 48 | `package` | Retain exact-head reads and bounded Review/Check projection boundaries. |
| `internal/log/FLOW.md` | 54 | `package` | Retain fixed-file reading, path confinement, and lifecycle ownership. |
| `internal/observability/taskaudit/FLOW.md` | 31 | `package` | Retain persistence, replay, retention, and ordering semantics. |
| `internal/runtime/backup/FLOW.md` | 53 | `package` | Retain leader scheduling and portable archive publication semantics. |
| `internal/runtime/cloudviewstate/FLOW.md` | 18 | `package` | Retain monotonic, fail-closed benchmark-purity state. |
| `internal/runtime/delivery/FLOW.md` | 83 | `package` | Retain canonical delivery plan, ACK, retry, and owner-push semantics. |
| `internal/runtime/issueagentverify/FLOW.md` | 29 | `package` | Retain clean verification and protected-file candidate rules. |
| `internal/runtime/online/FLOW.md` | 39 | `package` | Retain local-session ownership and touch-batching fences. |
| `internal/runtime/opsmcp/FLOW.md` | 44 | `package` | Retain credential budgets, audit redaction, and bounded profiling. |
| `internal/runtime/presence/FLOW.md` | 90 | `package` | Retain authority epochs, TTL, fencing, and conflict rules. |
| `internal/runtime/reviewagentverify/FLOW.md` | 35 | `package` | Retain evidence, sandbox, and named-check sealing boundaries. |
| `internal/runtime/webhook/FLOW.md` | 38 | `package` | Retain best-effort/backpressure semantics and SENDACK independence. |
| `internal/usecase/backup/FLOW.md` | 88 | `package` | Retain plan admission, scheduling, restore, and retention invariants. |
| `internal/usecase/channel/FLOW.md` | 81 | `package` | Retain membership projection and mutation-version semantics. |
| `internal/usecase/chatlifecyclerun/FLOW.md` | 39 | `package` | Retain paid-stage policy, ledger, and exact selector derivation. |
| `internal/usecase/cloudanalysis/FLOW.md` | 63 | `package` | Retain observation verdict and released-run boundaries. |
| `internal/usecase/clouddeploy/FLOW.md` | 131 | `package` | Retain deployment state machine; reduce toward the 100-line target. |
| `internal/usecase/cloudlease/FLOW.md` | 70 | `package` | Retain provider-neutral paid lifecycle and cleanup semantics. |
| `internal/usecase/cloudsim/FLOW.md` | 109 | `package` | Retain simulation lifecycle and partial-failure cleanup; reduce toward target. |
| `internal/usecase/conversation/FLOW.md` | 94 | `package` | Retain transient projection, cursor, and personal-state invariants. |
| `internal/usecase/delivery/FLOW.md` | 46 | `package` | Retain while the explicit compatibility rejection surface exists; retire with that surface. |
| `internal/usecase/issueagent/FLOW.md` | 24 | `package` | Retain deterministic lifecycle and human merge authority. |
| `internal/usecase/opsobserve/FLOW.md` | 42 | `package` | Retain closed-world observation and missing-evidence semantics. |
| `internal/usecase/presence/FLOW.md` | 107 | `package` | Retain activation/deactivation and authority orchestration; reduce toward target. |
| `internal/usecase/reviewagent/FLOW.md` | 41 | `package` | Retain deterministic review lifecycle and model/adaptor separation. |
| `internal/usecase/user/FLOW.md` | 39 | `package` | Retain legacy-compatible token/session-close and restore behavior. |
| `pkg/backup/FLOW.md` | 61 | `package` | Retain portable archive format and complete-publication invariants. |
| `pkg/bench/model/FLOW.md` | 65 | `package` | Retain shared wire/schema and deterministic planning constraints. |
| `pkg/channel/worker/FLOW.md` | 144 | `package` | Retain worker pool ordering and shutdown; reduce toward the 100-line target. |
| `pkg/client/FLOW.md` | 108 | `package` | Retain session, SENDACK, RECV, and bounded queue semantics; reduce toward target. |
| `pkg/db/FLOW.md` | 44 | `subtree` | Retain root storage lifecycle and Pebble isolation boundary. |
| `pkg/db/meta/FLOW.md` | 83 | `package` | Retain table ownership, cross-Slot fencing, and restore semantics. |
| `pkg/goroutine/FLOW.md` | 94 | `package` | Retain process-wide goroutine ownership and shutdown evidence semantics. |
| `pkg/hashslot/FLOW.md` | 14 | `package` | Retain neutral routing/rebalance ownership and dependency boundary. |
| `pkg/workqueue/FLOW.md` | 110 | `package` | Retain admission, drain, and shutdown guarantees; reduce toward target. |
| `test/e2e/suite/FLOW.md` | 93 | `package` | Retain process lifecycle, port ownership, redaction, and convergence semantics. |

### Migrate unique knowledge, then retire

| Current file | Lines | Destination before deletion | Reason |
| --- | ---: | --- | --- |
| `internal/FLOW.md` | 207 | Root `AGENTS.md`, `docs/development/PROJECT_KNOWLEDGE.md`, retained package FLOW files, and generated index | It is a duplicated cross-package overview and currently causes implicit broad context loading. |
| `internal/contracts/channelmembers/FLOW.md` | 11 | `internal/usecase/channel/FLOW.md` plus exported helper comments/tests | The stable legacy namespace is valuable, but the DTO/helper package does not need its own navigation card. |
| `internal/contracts/messageevents/FLOW.md` | 11 | `internal/usecase/message/FLOW.md` or `internal/runtime/channelappend/FLOW.md` plus type comments | The content is a lightweight DTO boundary already owned by message/append flows. |
| `internal/infra/clouddeploy/fake/FLOW.md` | 8 | `internal/usecase/clouddeploy` test documentation and fake package comments | A deterministic test fake does not independently meet admission criteria. |
| `internal/infra/cloudlease/fake/FLOW.md` | 25 | `internal/usecase/cloudlease` tests and fake package comments | Failure injection belongs with the provider contract tests, not an Agent navigation card. |
| `internal/infra/cloudsim/fake/FLOW.md` | 14 | `internal/usecase/cloudsim` tests and fake package comments | Deterministic fake inventory is test behavior rather than a separate architectural boundary. |

## Expected Result

The migration retains the established filename and the valuable local-context
habit while changing its economics:

- an Agent reads a bounded navigation card rather than a second manual;
- parent FLOW content is loaded only when the author explicitly selected
  subtree scope;
- policy and executable behavior remain outside advisory prose;
- high-risk short documents survive because admission is semantic, not based
  on line count;
- detailed knowledge has an explicit destination instead of being silently
  discarded; and
- the repository gains a deterministic catalog and structural gate without
  pretending that a linter can prove documentation truth.

No existing FLOW is deleted or rewritten by this design document. Each
migration remains a separately reviewable implementation change.
