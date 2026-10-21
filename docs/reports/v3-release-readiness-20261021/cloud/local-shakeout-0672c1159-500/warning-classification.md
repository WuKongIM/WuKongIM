# 0672 local shakeout: bounded log classification

Source: `0672c115950f65e6d92381aa5b3ea9f0a20fc790`. This is an offline review of the one completed run. No workload retry, threshold change, product patch, or new gate.

## What the existing local criteria establish

The local runbook (`docs/superpowers/runbooks/2026-08-04-chat-lifecycle-soak.md:115-144`) expects the native helper's coordinated operator stop plus `report/final.json`; it then requires recorded children/ports to be gone and logs to contain no panic, fatal Slot error, or raw credential. The immutable native result is terminal `operator_stop/operator_requested`, raw helper and outer wrapper exit 130. Final benchmark first-attempt failures, terminal sends, loss/duplicate/corruption/sequence regressions, harness failures and underdelivery are zero. SEND=SENDACK=876529; RECV=RECVACK=1099924; correlation pending/outstanding and workload queues are zero. The wrapper verified 12 recorded process identities gone, owned SID 94726 empty, all 64 configured ports closed, and its scoped exact-token scan passed. No panic/fatal log match was found.

Those observations satisfy the recorded local workload/cleanup criteria, with the warnings below retained. They do not establish clean shutdown of every product component, complete delivery of every unsampled recipient, or formal qualification. The coordinator report ended before product process teardown; its `cleanup_error_count=0` cannot clear later product shutdown warnings. No additional warning-as-failure criterion is invented after execution.

## Presence warnings: cause not established

Canonical `run/logs/node1/app.log` has 94 `internal.app.delivery.plan_incomplete` / `phase=presence` / `route not ready` warnings: 93 before the final report's 08:06:04.333745Z end and one at 08:06:07.752Z after it. First: line 56, 07:41:05.775Z. Node3 has one additional occurrence, line 57, 08:06:08.885Z, after the report. `warn.log` and `service-*.log` duplicate the canonical per-node app log; they are not independent events.

The observer emits this warning only when a delivery plan's terminal result is not OK (`internal/app/online_delivery.go:45-68`). `internal/runtime/presence/types.go:11-12` defines the error as incomplete authority reconstruction or a pending route conflict. Lookup paths can produce it when the current authority lacks complete UID recovery proof (`directory.go:269-299, 350-355`); recovery also has bounded-request checks (`recovery.go:22-32, 60-64`). The recorded warning carries no authoritative recovery/fence branch receipt, so this review does not select one cause.

The 07:41:01.252732875Z and 07:41:06.252775208Z worker cuts both report zero expired sessions. The first cut with expirations is 07:41:46.252998542Z (4). Thus session expiry is not supported as a necessary explanation for the first warning. Canonical product logs contain no `leader_change` entry from 07:37Z through the final report boundary; the final cluster report has 351 healthy / 0 unhealthy samples and zero hot-replica-lag/leader-imbalance breaches. Absence of a log is not proof that all authority/routing state stayed constant, but the available evidence does not establish a normal Channel transfer either. These warnings remain unexplained product delivery evidence, not confirmed harmless churn and not a demonstrated benchmark loss.

## Shutdown warnings: three components, shared product budget

Both node1 and node3 report `internal.app.lifecycle_stop_failed` for `delivery_worker`, `plugin_hook`, and `cluster` with `context deadline exceeded`, all after finalization:

| Node | Component | UTC | Canonical app.log line |
|---|---|---|---|
| 1 | delivery_worker | 08:06:10.164 | 170 |
| 1 | plugin_hook | 08:06:10.186 | 172 |
| 1 | cluster | 08:06:10.699 | 289 |
| 3 | delivery_worker | 08:06:10.165 | 65 |
| 3 | plugin_hook | 08:06:10.198 | 67 |
| 3 | cluster | 08:06:10.710 | 74 |

`cmd/wukongim/main.go:16, 64-80` creates one product Stop context with the configured/default 5-second timeout. The rendered TOML does not override it; `pkg/cluster/config.go:408-409` also defaults the cluster stop budget to five seconds. `internal/app/lifecycle.go:514-568` passes that same context through delivery, plugin and cluster stops, logs each error, and joins the errors. Therefore later warnings can inherit an already-expired shared budget; the six entries do not demonstrate six independently timed five-second waits. The actual TERM timestamp and each component's start are not captured, so exact per-component stop duration is unknown.

The helper's coordinator graceful drain default is 90 seconds (`scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh:23`); its separate final cleanup sends TERM to all remaining owned PIDs and allows 15 seconds before escalation (`:22, 471-509`). The legacy stop-after outcome is propagated as 130 once a final report exists (`:1843-1847`). These are different budgets. This run finished naturally before the outer SID cleanup, whose initial process set was empty. The evidence proves OS process cleanup, not successful product component drain. The post-final transport connection-refused, runtime-closed and canceled warnings are temporally consistent with peer/process teardown, but their full internal causality is not proved.

Raw counts, timestamps, per-minute warning summaries and source hashes are in `summary.json` and `warning-source-freeze.json`. Existing diagnostics did not collect post-final product queue convergence or a full shutdown profile; no stronger shutdown claim is made.
