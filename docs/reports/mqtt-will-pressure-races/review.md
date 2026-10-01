# Final two-axis review

## Standards

Reviewed the delivered frozen scope against `a8559a6bad3f48337315f814dc664b3c221e76b6` using `git diff HEAD` and scoped untracked files. Applicable AGENTS and valid FLOW files were read; delivered race/helper/FLOW hashes match. **Remaining hard standards findings: 0.** This reviewer ran no tests or tooling-enforced checks.

### Initial and supplemental findings: resolved

- **P2 — Unjoined clients.** Initial `test/e2e/mqtt/will_reclamation_races/races_test.go:114,151,168–169` only closed TCP, contrary to its `AGENTS.md:12–13` requirement to join clients before JSON. Delivered `test/e2e/suite/mqtt_client.go:124–140` implements `AbortAndWait`; race `races_test.go:123–126,131,168,185–186` and capacity `reclamation_test.go:115–119` use bounded joins. Artifact callbacks precede later registered client/process cleanup callbacks, so artifacts are written last.
- **P3 — Topology terminology.** Initial full-cap `reclamation_test.go:40` and race `races_test.go:36` emitted `1-node-cluster`, contrary to root `AGENTS.md:19–20`. Delivered full-cap `:40–44` and race `:39–43` explicitly use `single-node-cluster` and `three-node-cluster`.
- **P3 — Reusable pprof placement.** The completion-window fixture embedded generic HTTP/read/file operations at `races_test.go:222–235`, contrary to `test/e2e/AGENTS.md:13–14,39–40`. Delivered `test/e2e/suite/goroutine_profile.go:14–34` owns this operation with documented 1 MiB truncation and 0600 output; race `:220–223` supplies a three-second context/path. Will-specific history matching remains in the scenario. Applicable FLOW and generated index are updated.

### Baseline heuristic dispositions — judgment only

- **Duplicated Code, retained with reason:** delivered race restart/receipt hunks `races_test.go:165–180,226–244` overlap capacity `reclamation_test.go:141–153,244–257`. Their recovery timing and assertions differ; explicit controlled cuts avoid a generic fault framework. Shared client shutdown already lives in `suite`.
- **Repeated Switches, retained with reason:** capacity `reclamation_test.go:61,82,106,122,175` couples mode choices to `journalCapacity == 1024`. The two modes implement exact cap-two/default-1,024 behavior; a plan abstraction is optional.

Recorded evidence: joined capacity/cap-two and ordinary cases pass both topologies; the six-case matrix passes in 638.060s before the failure-path-only extraction. Delivered-source late-release and real-product helper capture pass separately. Four unsafe cancellation controls fail the intended assertion. A 41.631s receipt exceeds the old 35s observation budget; its precise latency cause remains open. No product repair or latency SLO is claimed.

## Spec

Reviewed initial through delivered frozen scopes against
`docs/specs/mqtt-will-pressure-races.md`, base
`a8559a6bad3f48337315f814dc664b3c221e76b6`. No tests or source changes.

### Initial findings and dispositions

1. **[P1] Incomplete delayed-CAS acceptance:** initial spec lines 52–53 required
   “three distinct original business publications and 15s healthy quiet”
   (now lines 57–58). The initial matrix passed five cases; single-node
   `late-release` repeatedly timed out after passing expired-page retention.
   Joined isolated/repeated cases passed, but another complete 35s-window
   matrix still passed only five cases. Resolved within the final bounded
   slice: all six 60s-window cases passed in 638.060s, each preserving the
   current original, three publications and healthy quiet with zero extras.
   A 41.631s receipt proves the earlier 35s gate insufficient; the precise
   latency cause and any product-repair claim remain unqualified.

2. **[P2] Uncovered early-read starvation:** line 17 names “Reclamation pages
   starve behind early failed reads or become unbounded.” Final spec lines
   64–65 explicitly exclude failed authority-read starvation from this slice.
   Disposition: accepted scope clarification, retaining open qualification;
   no implemented fairness claim.

3. **[P2] Full-capacity client joining:** line 21 inventories “Closing an owned
   client leaves Paho workers alive when the receipt is written.” Resolved:
   both fixtures now use bounded `AbortAndWait`; Paho closes `Done` after
   workers join. Exact-source full-capacity reruns passed 749.098s/467.413s,
   and joined cap-two regression passed both topologies, 144.938s.

### Final qualification and remaining limits

No remaining actionable implementation findings in the approved slice.
Spec lines 54–56 define “eventual completion acceptance, not a latency SLO.”
The 60s observation preserves the 750ms page assertion, 10s grant,
unknown-current ordering, identities, original subscription and healthy quiet.
The delivered fixture only extracts failure-path stack capture into the
shared suite; unchanged acceptance controls plus a 105.977s delivered-source
late-release rerun support the matrix result. Source hashes distinguish both.

Public failure-only history lookup is limited to eight messages, with counts
only in JSON. Optional private stack capture is capped at 1MiB; its real-process
probe saved 946,209 bytes. Neither diagnostic replaces a failed receipt.
Counters use `return(false)` and preserve branch behavior. No unasked production
behavior, scope creep or incorrect implemented requirement found.

Four unsafe cancellation-ignoring controls failed the intended second-page
assertion. Earlier failed runs remain separate. Failed-read starvation,
uncommitted Raft apply, partitions, corrupt/lost journals and complete
issued-effect terminal recovery remain open; this is not complete MQTT
or latency qualification.

Standards: zero remaining documented violations, two retained judgment-based heuristics; Spec: zero actionable implementation findings, failed-read-starvation process qualification remains open.
