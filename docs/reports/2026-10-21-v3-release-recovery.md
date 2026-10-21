# V3 recovery candidate integration

The release candidate fixes a public readiness classification failure after
original-v2 migration and cluster restart. Incomplete or unavailable recovery
proofs remain retryable across local and RPC reads; permanent storage/proof
errors and write fencing retain their existing behavior. This change does not
relax quorum admission or make uncommitted messages readable.

The product repair was first committed as
`98c25091bb9587a303af982bb14bf4cedd9aa110`. This integration branch selects that
repair and its regression fixtures from the evidence branch instead of merging
hundreds of archived observations into one review. Product directories
`cmd`, `internal`, and `pkg`, plus `go.mod` and `go.sum`, match the qualified
candidate `ef798352c40a94e57f768f76b2f4713712f827fe`.

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
