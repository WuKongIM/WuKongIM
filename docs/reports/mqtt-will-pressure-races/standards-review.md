# Standards review — delivered source

Reviewed the delivered frozen scope against `a8559a6bad3f48337315f814dc664b3c221e76b6` using `git diff HEAD` and scoped untracked files. Applicable AGENTS and valid FLOW files were read; delivered race/helper/FLOW hashes match. **Remaining hard standards findings: 0.** This reviewer ran no tests or tooling-enforced checks.

## Initial and supplemental findings: resolved

- **P2 — Unjoined clients.** Initial `test/e2e/mqtt/will_reclamation_races/races_test.go:114,151,168–169` only closed TCP, contrary to its `AGENTS.md:12–13` requirement to join clients before JSON. Delivered `test/e2e/suite/mqtt_client.go:124–140` implements `AbortAndWait`; race `races_test.go:123–126,131,168,185–186` and capacity `reclamation_test.go:115–119` use bounded joins. Artifact callbacks precede later registered client/process cleanup callbacks, so artifacts are written last.
- **P3 — Topology terminology.** Initial full-cap `reclamation_test.go:40` and race `races_test.go:36` emitted `1-node-cluster`, contrary to root `AGENTS.md:19–20`. Delivered full-cap `:40–44` and race `:39–43` explicitly use `single-node-cluster` and `three-node-cluster`.
- **P3 — Reusable pprof placement.** The completion-window fixture embedded generic HTTP/read/file operations at `races_test.go:222–235`, contrary to `test/e2e/AGENTS.md:13–14,39–40`. Delivered `test/e2e/suite/goroutine_profile.go:14–34` owns this operation with documented 1 MiB truncation and 0600 output; race `:220–223` supplies a three-second context/path. Will-specific history matching remains in the scenario. Applicable FLOW and generated index are updated.

## Baseline heuristic dispositions — judgment only

- **Duplicated Code, retained with reason:** delivered race restart/receipt hunks `races_test.go:165–180,226–244` overlap capacity `reclamation_test.go:141–153,244–257`. Their recovery timing and assertions differ; explicit controlled cuts avoid a generic fault framework. Shared client shutdown already lives in `suite`.
- **Repeated Switches, retained with reason:** capacity `reclamation_test.go:61,82,106,122,175` couples mode choices to `journalCapacity == 1024`. The two modes implement exact cap-two/default-1,024 behavior; a plan abstraction is optional.

Recorded evidence: joined capacity/cap-two and ordinary cases pass both topologies; the six-case matrix passes in 638.060s before the failure-path-only extraction. Delivered-source late-release and real-product helper capture pass separately. Four unsafe cancellation controls fail the intended assertion. A 41.631s receipt exceeds the old 35s observation budget; its precise latency cause remains open. No product repair or latency SLO is claimed.
