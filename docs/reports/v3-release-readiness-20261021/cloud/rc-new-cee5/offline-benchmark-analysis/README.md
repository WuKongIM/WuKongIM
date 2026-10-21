# Offline benchmark/correlation diagnosis — exact cee5

The original `product_failure / message_loss` result remains unchanged. Static analysis does not prove a benchmark false positive or identify the missing sample. The important correction is that this run used a **10-second correlation deadline**, not 60 seconds.

The final report binds `thresholds.latency.single_anomaly=10000000000` ns. `worker_server.go:1171` passes that setting directly to `VerifierConfig.CorrelationDeadline`; `defaults.go:134` also defines 10 seconds. The measured window lasted 61.212052848 seconds. Load-log cut 167 still reports loss=0; cut 168, labelled 17:06:48.934614569Z, reports loss=1 at elapsed 60.025390115 seconds. The terminal cut follows 1.186662723 seconds later. These are observation labels, not the missing message's ACK or expiry timestamps. They provide no evidence of a 60-second timer.

The server's 47 retained `delivery.plan_incomplete` events all report presence timeout. Their first event is 17:06:39.976Z. This timing is compatible with a delivery problem during the late part of the measured window, but event time is not SENDACK time, and none carries the lost sample's identity. A causal binding cannot be made from the roughly 10-second separation to the terminal report.

## Exact source path and counter-evidence

All paths below are relative to the exact, clean `cee5d51f325fb84033060f546337d326110adae5` checkout. File digests and context/rule digests are in `source-binding.json`.

1. `engine.go:3415–3452` registers the logical SEND and wire attempt before `TrySend`. Registration creates the sampled correlation without a deadline (`verifier.go:504–546`). This opposes an ordinary RECV-before-registration explanation.
2. Sampling uses a deterministic ordinal phase (`verifier.go:1329–1340`). Routing requires a distinct online witness for sampled group traffic (`engine.go:4331–4339`) and a local target for sampled person traffic (`engine.go:3996–4008`). Admission pins the exact recipient until correlation closure (`session.go:875–907`). Ordinary TTL eviction excludes pinned recipients (`session.go:986`). All retained periodic cuts show 10,000 online and zero teardown reasons; terminal `generation_stop=10000` is after failure.
3. The drain calls `HandleSendackAt` with source `ObservedAt` (`session.go:1272–1278`). A first successful ACK with positive message ID and sequence schedules `acceptedAt + deadline`; duplicate ACKs cannot extend it (`verifier.go:691–709`). A valid RECV arriving before ACK is retained and completes on ACK; ACK-first completes on RECV (`verifier.go:899–916`). `ReleaseSendAt` does not remove accepted pending correlations (`verifier.go:1145–1154`). Existing tests cover these orderings, exact deadline, delayed acceptance, and recipient TTL leases; they were read, not executed here.
4. Expiry runs under the correlation lock and removes due entries, incrementing loss only when positive ACK was seen (`verifier.go:970–993`). A later matching RECV cannot undo that expired loss because its correlation entry is already gone. `Losses` is exactly `sampledExpired` (`verifier.go:1250`). Any nonzero loss triggers the unchanged correctness failure (`verdict.go:371–387`).

## Remaining benchmark-origin hypothesis

ACK timing is carried from the client result, while RECV transport returns an empty `FrameTiming` (`wkproto/client.go:611–624, 715–724`). Correlation observes a RECV only when the session drain calls the verifier. `Engine.advance` performs expiration before its completion-queue drain (`engine.go:2758–2761`); that owner queue is ACK completion bookkeeping, not the recipient RECV queue. Therefore merely reordering `drainCompletions` cannot establish timely RECV processing.

A valid RECV already waiting locally but not processed until after the deadline is a possible mechanism for attributing load-generator delay to delivery loss. Fair/bounded queues do not prove a maximum scheduling delay. No per-sample receive ingress time or queue-residence history survives here, so this possibility is **unconfirmed**, not the historical root cause. Conversely, zero receive/ACK/correlation-conflict errors, zero retries, and 1,338 completed correlations oppose simple decode, ACK identity, retry, or normal TTL explanations; they do not eliminate one delayed drain.

## Identity is recorded, but unavailable in this archive

`verifier.go:986–993` records each expired sample's logical `SampleIndex`, a 16-byte `ClientMsgNo` fingerprint, and configured deadline. `EvidenceRecorder` retains bounded first/last examples (`evidence.go:90–115, 160–190`). Worker snapshots contain these examples (`worker_server.go:1671,1731`). The main final report reduces them to class counts (`coordinator.go:2514–2539,2881`; `report.go:321–322`).

There is already a separate terminal `worker-evidence.json` writer (`live_diagnostics.go:303–325`), called by terminal observation and `ProductionEvidenceController.Finalize` (`production_controller.go:544`). The exact final ZIP has no such member, and the retained main report has no samples. **This proves missing archived evidence, not a runtime write failure.** Even that bounded file's current schema does not record per-sample ACK/expiry/RECV timing or raw server message identity. Collector analysis is deferred to the root agent.

## Delivery and verification boundary

`feedback-plan.md` specifies deterministic feedback before any behavioral repair. The only executed probe is read-only ZIP evidence validation: `archive-contract-probe.py` returns an expected RED for absent bounded sample evidence. That is an archive-sufficiency failure, not a product reproducer. No product process, Go test, binary, cloud API, SSH, MCP, source modification, or threshold change occurred. The source worktree remained clean. Original ZIP, logs, reports, unknown attribution, and previous verdict are preserved.
