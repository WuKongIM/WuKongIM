# MQTT three-node Owner outage

Source base `633b044b671d82e6464fd1e3d236993366c743ab`, in the existing
`codex/mqtt-design` worktree. [Frozen context](frozen-context.json) records source
instruction digests and approved public seams; [candidate provenance](candidate-provenance.json)
records the binary and fixture hashes. The [failure inventory](../../specs/mqtt-owner-outage-acceptance.md)
preceded the new process test. No production behavior change is needed for the
recorded baseline; this change adds acceptance coverage, not a product repair.

## Evidence

Both original-product cases pass: [SIGSTOP/SIGCONT](baseline-suspend.json) and
[SIGKILL/restart](baseline-crash.json). Each creates a persistent Session on node
3 with an unacknowledged native QoS 1 exchange. Both surviving ingresses refuse
takeover before and after the captured 30-second grant. After at least 35 seconds
from the signal, both public Slot inventories agree on surviving leaders/quorum
and independent MQTT CONNECT controls succeed. Target refusal is therefore
distinguished from general cluster/admission unavailability.

After communication or same-node restart and public cluster/Slot convergence,
one reconnect through node 1 and one takeover through node 2 preserve Session
Present, Packet Identifier, DUP and original message identity/body/order, without
SUBSCRIBE. Prior clients close, final ACK completes the original exchange, one
fresh native message arrives once and the final aggregate active Owner count is
one. The report is written after process/client cleanup; secrets, identities,
payloads and raw logs remain outside Git.

A deliberately unsafe external overlay skips exact-owner isolation only after
the stored lease expires. The [negative control](expiry-negative-red.json)
fails exactly at the later target CONNECT: node 1 wrongly admits it, despite the
living stopped process. Both early refusals, surviving quorum and independent
controls still pass. [Negative provenance](expiry-negative-provenance.json)
retains exact original/overlay/binary/fixture hashes. This proves the test can
reject an expiry-based takeover regression; it is not a discovered defect in the
ordinary product and the overlay is absent from repository production source.

The final fixture additionally requires early refusals to finish within the
captured grant and zero sampled active Owners on surviving nodes after both
refusal phases. Its repeated [suspend](final-suspend.json) and
[crash](final-crash.json) cases pass with race instrumentation on the test
harness/Paho process. The product binary is an ordinary build, so this does not
qualify server race behavior. [Validation](validation.json) records completed
checks. The same ordinary product binary also passes the unchanged
[original full group workload](full-scale.json): 100,000 members, 500 persistent
connections, twenty initial publications plus one post-churn publication, and
600 confirmed retirements. Missing, duplicate, reordered, unexpected and wrong
identity counts are zero. Initial fanout takes 163.493 seconds under the existing
three-minute receipt deadline; idle reads are 3.45 Slot barriers per subscriber
per second. Profiling and workload overrides are disabled. This single-node
cluster workload does not qualify broader topology, load or storage capacity.

The pre-merge Standards pass moves the existing real-time metadata-contention
test into the integration tier without changing its assertions, updates two
stale FLOW capability descriptions and regenerates the FLOW index. The moved
test passes five race-instrumented repetitions, its default/integration
[tier selection](test-tier.json) is verified, and `flow-doc-contracts` passes
with the same eleven pre-existing length warnings. Product source is unchanged.

## Bounds

The cluster has 256 hash Slots, three Slot replicas and two Channel replicas.
The latter permits independent new admission with two healthy nodes. Existing
content still requires its configured replica proofs; no publication is issued
during the outage. Each target CONNECT keeps the existing five-second wire
budget. The 35-second real-time window is necessary to distinguish execution
expiry from isolation. Each case has a three-minute context and emits a bounded
failure phase without interpreting a failed response as safe retry authority.

SIGSTOP models an unavailable living process; SIGKILL covers abrupt node loss
followed by same-node boot recovery. Neither proves asymmetric network partitions,
remote effects continuing after an unknown reply, lost node data, or takeover
while the old node remains unreachable indefinitely. Fixed metrics support
diagnosis but never authorize isolation. No wire/schema/configuration fields,
worker count, queue bounds or product deadlines change. Coverage-only changes
need no new user-visible Changelog entry.

## Standards

The frozen branch review compares `64f73d99b3b0cb8960d40825f76053f5a0000dbd`
with `633b044b671d82e6464fd1e3d236993366c743ab`, plus the captured working copy.
[Review scope](review-scope.json) and the [full commit list](review-commits.txt)
pin 174 commits and 1,191 changed files. Two independent reviewers map that
scope and inspect the listed high-risk boundaries; this is not a full line audit.

- **[P2, repaired] Real-time test in the default tier.** The original
  `request_meta_apply_test.go` waits for a 30 ms timer and a 20 ms deadline.
  Root `AGENTS.md` requires elapsed-time simulation in the integration tier.
  It is now `request_meta_apply_integration_test.go` with the `integration`
  tag and unchanged assertions; focused race and tier checks pass.
- **[P3, repaired] Stale FLOW capability status.** `internal/app/FLOW.md` and
  `internal/usecase/mqttsession/FLOW.md` still said source-tombstone retirement
  was required, although `wireMQTTConsumers` constructs `SourceRetirement` and
  the shared cohort invokes it. Root `AGENTS.md` requires accurate affected
  FLOW navigation. The descriptions and generated index now match the code.

No justified Fowler smell or product-layer violation was identified in the
inspected dependency, acquisition, restore, replay, configuration, metric and
test boundaries. This does not imply all branch code was exhaustively reviewed.

## Spec

- **[P1, open] Started Will has no safe completion path without a receipt.**
  `internal/usecase/mqttsession/will_execution.go` persists Started at line 280
  before calling `PublishWill`. Crash in between leaves a Started obligation;
  later turns only look up positive receipts and remain Pending when none exists.
  The approved design, `docs/specs/mqtt-im-access.md:174`, requires
  “使用可恢复的待发布状态和稳定业务幂等键，避免崩溃窗口导致任务丢失或重复生成业务消息”.
  This known implementation gap needs proved isolation of previous effects and
  safe recovery admission; expiry or an absent receipt cannot authorize replay.
- **[P1, open] Shared storage has no node/cluster capacity admission.**
  `internal/app/mqtt_config.go:30` bounds per-Session backlog, while
  `pkg/db/message/mqtt_replay.go:161` rejects integer overflow, not total stored
  capacity. Many individually compliant Sessions/sources can exceed the node's
  aggregate storage budget. Design line 171 requires “节点和集群共享存储上限”,
  and line 128 requires “在产生新的不可承受责任前实施有界背压”.
  Total-capacity admission and cross-Session saturation/recovery acceptance remain
  missing. No disk-exhaustion experiment was run.

The Owner outage fixture matches its approved process-fault scope. Network
partitions, unknown remote effects and the complete failure matrix remain
unqualified. Both P1 findings are unimplemented parts of the complete design,
not failures of this new process scenario; the branch is not ready to claim
complete MQTT delivery. Next work should first cover Started Will recovery's
no-dispatch, committed/no-reply and still-running-effect boundaries.

Standards: 2 findings, original worst P2, both repaired and validated.
Spec: 2 findings, worst P1, both open.

## Reproduce

```sh
GOWORK=off go build -buildvcs=false -o /tmp/wukongim-owner-outage ./cmd/wukongim
WK_E2E_BINARY=/tmp/wukongim-owner-outage WK_E2E_MQTT_REPORT_DIR=/tmp/owner-outage GOWORK=off go test -race -p 1 -tags=e2e ./test/e2e/mqtt/owner_outage -count=1 -timeout=8m -v
```

The negative control is confined to a temporary Go overlay. Wrap the existing
`Isolation.Quiesce(ctx, observed)` error check in
`if old.LeaseUntilMS > now.UnixMilli()` in a copied `connect.go`; map only that
absolute repository source path to the copy through `go build -overlay`.
Run only this isolated fixture with that binary. It must fail at
`refusal-after-expiry` with successful controls and a wrongly admitted target,
never be treated as a candidate for deployment.
