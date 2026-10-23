Chat-lifecycle finalization could report `meta_checkpoint/metrics_fetch` while discarding which declared metrics source failed and whether acquisition or parsing rejected it. Retain a bounded source mask and three closed failure categories in the existing finalization diagnostic, with positions ordered by `observation.service_nodes`.

Metrics acquisition uses the existing single HTTP loop and parser. Safe typed Metrics errors preserve their original error chain; the three-source aggregate retains causes privately and unwraps only the original harness-invalid sentinel. Child context/product errors therefore cannot change finalization precedence. The optional projection validates masks and categories and never serializes raw causes, addresses, tokens or response bodies. Other observation endpoints, numeric limits, retries, deadlines, accounting and report validity remain unchanged.

Validation:
- All 54 failure/control cases were fixed before code. After correcting a fixture-only Authorization expectation on unchanged production, RED v2 compiled with 122 failing / 330 passing events.
- The same tests pass normally and under race: 452 events each, with unchanged test hashes.
- Full target/chat-lifecycle/all wkcli package tests pass: 2,452 events / 1,109 top-level tests, no skipped tests or failures.
- Named formatting and FLOW checks pass. Independent Standards and Spec reviews both report zero findings at exact head `56c287493a5e9fc3df65ea0631b99612672cd678`.
- All four original automatic Linux CI workflows pass on their first attempt (16 successful checks, two event skips). The actual test merge has the exact reviewed tree. Coverage includes unit/race, scripts integration, all five migration topologies, cross-node messaging, restart self-chat, three fresh 500 QPS seams, and native package lifecycle on four distributions. Opt-in 2,000 QPS qualification and complete full-distribution loss repair are not claimed.

The preceding exact-source full-distribution local experiment still had one sampled expiry and no complete final report. Nearby retained scrapes parse successfully but do not reconstruct its failed fetch round. This diagnostic change does not repair that message loss, qualify v3 GA, publish native packages or authorize paid cloud resources. A further bounded local diagnostic will separately verify the new failure projection.
