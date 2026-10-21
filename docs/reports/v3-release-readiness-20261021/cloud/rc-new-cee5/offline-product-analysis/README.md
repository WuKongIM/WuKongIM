# Offline product delivery findings

Exact source: cee5d51f325fb84033060f546337d326110adae5. Read-only source analysis after the exact cloud Lease reached zero inventory. This is not an MCP Diagnosis Result or a reproduced root cause.

## Proven observations

The retained service logs contain 47 `internal.app.delivery.plan_incomplete` events, all phase `presence`, result `timeout`, from 17:06:39.976Z through 17:06:46.848Z (22/12/13 across three services). No message or correlation identity is present. Runtime snapshots report three active/running product services and no systemd restart count; they do not establish the complete failure interval or exclude all host incidents.

The product wires the recipient worker pool but does not override `PlanTimeout`; `internal/runtime/delivery/runtime.go` therefore uses its five-second execution deadline. The clock starts when a worker begins processing, not when the per-Channel queue admits a plan. `runPlan` emits one terminal result and the worker completes the queue entry even on a presence error. `processPlan` skips failed exact presence groups while preserving successful siblings; it does not requeue failed presence groups. This describes the existing online delivery policy and does not prove the accepted durable message disappeared from storage.

The typed presence path keeps target-group alignment. The cluster adapter groups by current leader with at most four concurrent leader calls, retries typed stale/route-not-ready results within the caller context, and does not extend that context. Cold authority reconstruction uses 32 ordered admission lanes, a node-wide owner gate of four, and bounded owner reads. These mechanisms could contribute delay only under conditions not established by the retained evidence; their mere existence is not a contention diagnosis.

`PlanTerminalEvent` retains outcome, mode, recipient count, elapsed processing and one failure sample; it carries no message identity. The app warning records the representative UID, owner and failure, but cannot be joined to the expired benchmark message. Existing low-cardinality metric observations are not substitutes for the missing sample or lost range capture.

## Falsifiable hypotheses and limits

1. Product delivery remained incomplete for the expired sampled message: the 47 presence deadline failures support investigating this, but an exact message identity and receiver expectation are missing. A matching sample/message plus delivery failure would strengthen this; an already decoded and correctly correlated receiver event before expiry would refute it for that sample.
2. Load-side scheduling or correlation delayed processing past the ten-second configured correlation deadline: source analysis can identify a possible ordering mechanism, but this run has no retained per-sample receive/decode/completion timestamps proving it. The 61.212-second run duration is not a 60-second expiry setting.
3. A service or load interruption caused the observation: retained service snapshots and logs provide no positive restart/panic/OOM evidence, but absent bounded logs are not global proof of absence.

No timeout, rate, retry threshold or message-loss gate was relaxed. No product source was edited. No local performance result or post-failure profile is presented as a reproduction of this cloud failure. The smallest next feedback seam is to retain bounded worker evidence and terminal metrics, then bind failed product delivery observations to immutable message identity without adding metric-cardinality labels. A product behavior change requires a deterministic failing regression at the actual implicated boundary.
