# V3 recovery candidate integration

The release candidate fixes a public readiness classification failure after
original-v2 migration and cluster restart. Incomplete or unavailable recovery
proofs remain retryable across local and RPC reads; permanent storage/proof
errors and write fencing retain their existing behavior. This change does not
relax quorum admission or make uncommitted messages readable.

The product repair was first committed as
`98c25091bb9587a303af982bb14bf4cedd9aa110`. This integration branch selects that
repair and its regression fixtures from the evidence branch instead of merging
hundreds of archived observations into one review. The initial integration head
`5f3ecdf93e762a1b1551b44977a44f2e63c97802` matched qualified candidate
`ef798352c40a94e57f768f76b2f4713712f827fe` in `cmd`, `internal`, `pkg`, `go.mod`
and `go.sum`. Its first Linux default-suite run exposed the two failures below.
The subsequent MQTT fix changes product code, so that earlier tree equivalence
does not certify the updated candidate. Exact new-head results belong in the PR
and retained readiness evidence, without rewriting the first failed attempt.

## Failures found by candidate integration

[Candidate run 39546957124](https://github.com/WuKongIM/WuKongIM/actions/runs/39546957124)
attempt 1 failed two default-suite assertions; migration/messaging E2E was skipped.
The runner preserved exit 1 and verified Linux session cleanup with zero remaining
members. The independent SDK, Manager, native-package and three-node runs passed
on the initial head; those do not override the default-suite failure.

- MQTT cancellation: nested subscription, projection and source scopes only
  checked their immediate parent while cancellation of the original caller
  propagated asynchronously. A deterministic delayed-cancellation case drove
  the real source CAS conflict path and reproduced a pending-drain result and
  subsequent reads after caller cancellation. Carry only private scope ancestry
  and synchronously check each ancestor and Owner gate. Preserve the deadline
  minimum, uncertainty classification and source sealing constraints. Regression
  assertions require cancellation, no subsequent contention reads and an unchanged
  unsealed binding; the default-off preview boundary remains unchanged.
- Replication fixture: its single-item-only rejection bypassed coalesced batches.
  Every missing repair in six failures across 150 instrumented runs already had
  the exact durable proposal on the follower. Reject each selected batch item,
  preserve aligned accepted results, and verify exact repair keys. A deterministic
  real-batcher regression failed before the fixture fix; the original 66-item
  scenario passed 150/150 afterward, followed by the full package and focused race
  checks. No replication product code or expectation was weakened.

## Existing fixed-candidate evidence

- [Migration repair and five-topology regression](https://github.com/WuKongIM/WuKongIM/blob/32d402f7625c36f5667a290ec95c045f5f35937c/docs/reports/2026-10-20-v3-migration-recovery-repair.md):
  1→1, 1→3, 3→1, 3→3, and 3→5 passed, with 55 complete-history checks;
  related cold/hot reads and Channel failover passed on macOS.
- [Linux performance qualification](https://github.com/WuKongIM/WuKongIM/actions/runs/39453556254):
  candidate C passed all 20 unchanged results, 43,800 completed requests,
  zero errors/drops, and maximum P99 75.80928 ms. This is the candidate floor,
  not the final tag's publisher gate.
- [CPU attribution limits](https://github.com/WuKongIM/WuKongIM/blob/32d402f7625c36f5667a290ec95c045f5f35937c/docs/reports/2026-10-21-v3-cpu-contention.md):
  the joint causal screen failed. It does not establish the unique historical
  cause or beta-to-candidate non-regression, and is not an additional release gate.

## Integration checks and remaining release work

The credential-free candidate workflow runs the named `go-unit` check from its
frozen policy, then real-process migration, cross-node delivery, and self-chat
against one frozen Linux server binary. Each attempt retains source identities,
commands, exit status, raw output, and scenario receipts. Failed attempts are not
automatically retried. Existing SDK, Manager, native-package, and three-node
PR checks remain independent.

Self-chat adds coverage of an existing contract: authenticated devices with the
same UID exchange messages, the sending session is suppressed, and complete
persistent history survives a whole-cluster restart. Exact duplicate RECV frames
are recorded and deduplicated according to the public client contract; this
scenario does not introduce exactly-once network delivery or a new release gate.
The local final run passed both single-node and three-node clusters: 12 SEND
acknowledgments, 12 complete-history observations, and two whole-cluster restarts.
Earlier fixture failures (zero-bound latest-page semantics and repeated client
Close) remain in the evidence branch rather than being counted as product fixes.

This branch is not a release. Candidate CI and review must complete. The agreed
RC plan still has its 2h rehearsal, fresh 72h Soak, aged capacity and recovery
stages pending, with explicit procurement/budget authorization required before
purchase. Production stable native-package publication is disabled;
[packages PR #67](https://github.com/WuKongIM/packages/pull/67) only prepares
read-only formal-version source validation. Final release notes, exact-tag
publisher gates, immutable assets, images, documentation and signed stable
APT/RPM public-client verification remain required by
[RELEASING](../development/RELEASING.md). MQTT remains a default-off development
preview.
