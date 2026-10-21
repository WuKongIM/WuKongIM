New RC requests currently cannot lower their budget to retain reserves from earlier released attempts, and migration recovery can report an ordinary append failure when every missing recovery-barrier vote explicitly requests log repair. This change carries a lower request ceiling through planning, lease/deployment receipts, stage transitions and the remote wkcli guard, and preserves public readiness only for that narrowly proven barrier state.

`request_budget_limit_micros` accepts canonical integers with `150000000 < H <= 1500000000`; the stop line remains `H - 150000000`. Omitting it preserves the existing defaults. Budget mismatches and mixing a lowered request with released-preclock continuation evidence fail closed, while cleanup and zero-inventory uploads remain available. Keeping ¥148.9552 of previous quote reserves permits a new ceiling/stop line of ¥1351.0448/¥1201.0448; quote reserves are not actual bills.

Recovery readiness requires a durable local barrier and every missing distinct voter to provide a valid, bounded NeedFrom receipt. Unknown results, conflicts, ambiguous backpressure, cancellation and local persistence failures do not gain readiness identity. Quorum/frontier rules, wire format and business retries stay unchanged. The migration fixture keeps its first cold-history read, then checks actual protocol readiness before the original retained-credential Connect; new diagnostics run only on failure.

## Release notes / 发布说明

- [x] User/operator-facing changes are recorded under `[Unreleased]`.

## Verification / 验证

- Budget failure inventory and tests preceded implementation; final independent Standards/Spec reviews passed. Prior local default suite: 219 packages passed, 15 without tests; original scripts 9-minute timeout remains recorded. The first Linux head passed default/scripts, three-node performance and SDK checks, but failed migration Connect and Rocky dependency bootstrap; those results are preserved, not counted as complete qualification.
- Recovery: 19 cases were written first (two intended positives initially failed), then all passed; full replication package passed. The gate excludes mixed/unknown/conflicting/local-failure, cancellation and malformed repair outcomes.
- One new frozen local server binary passed the focused 1→3 scenario (13 complete-history checks) and all five topologies (55 checks), with no failures or remaining owned processes. These runs saw zero temporary retries, so they do not establish the exact cause of the historical connection refusal or restart failure.
- Named format and FLOW checks passed on the final source. Earlier FLOW length/index failures remain recorded. Independent reviews also verified the final seven-file integration; existing core/fixture bytes match their reviewed freezes.
- Final head `b3fdb777b9270128fe56086bc47562b321c2c947` includes the previously reviewed budget change and the exact tested recovery patch. Its first Linux CI generation must pass before merge.

This PR starts no paid request and publishes no release. A subsequent cloud run must select a validated product containing the new wkcli guard and obtain its separate paid-start authorization; old product `fc182` cannot accept the lower ceiling.
