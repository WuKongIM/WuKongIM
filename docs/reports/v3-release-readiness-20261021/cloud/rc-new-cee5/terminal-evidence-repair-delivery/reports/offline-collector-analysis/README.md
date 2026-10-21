# Offline terminal collector audit — cee5d51f

Source: `cee5d51f325fb84033060f546337d326110adae5`. Request: `chat-20261021T163850Z-4ccd7506`. This audit is read-only, after confirmed resource release. No product process, cloud call, workflow dispatch, credential read, or source edit occurred.

The worker-evidence collection omission is proven and locally reproducible. The cause of the cloud workload's one expired correlation is still unknown. This report does not claim to reproduce or fix `message_loss`.

## Proven collection omission

- `internal/bench/chatlifecycle/live_diagnostics.go:150` calls `writeTerminalWorkerEvidence` on a terminal cut. Lines 303–325 publish `worker-evidence.json` alongside `diagnostic-status.json`, at most 1 MiB, containing worker ID, generation, snapshot sequence, and existing bounded redacted first/last examples. `production_controller.go:544` calls terminal observation before writing final reports.
- `scripts/chat-lifecycle/rehearsal-finalize.sh:40` and its fetch block at lines 113–118 fetch only `final.json` and `final.md`. `formal-finalize.sh:106` fetches only formal qualification/final JSON+Markdown and capacity final JSON+Markdown.
- `collect-terminal-evidence.sh:66–103` makes 16 fixed calls for logs, system state, profiles, Prometheus targets and one range query. Neither worker evidence nor diagnostic status is requested. Workflow upload includes the local final directory, so upload cannot repair a file never copied there.
- Original artifact `12365508721` has 29 members. Its full ZIP SHA-256 is `a74b828be1a383d34bb8779bba4ed7473e51461ef6826c2d6d1a22afef415ccf`; all member CRCs were independently checked. Both diagnostic files and the metrics range are absent. `artifact-observation.json` preserves member names, sizes and hashes, not raw logs/configuration/profile bytes.
- The producer's two existing tests pass. The collector's existing test also passes and explicitly expects 16 captures; it does not verify worker retention. The pure fixture probe runs the actual collector, offers a worker-evidence file through fake SSH, then asserts retention. That assertion exits 1 because the file is never requested. See `probe-red.log` and `validation.json`.

This establishes that a successful remote write would not have survived this collector. The historical host was not inspected for the file before release; remote write/existence/content remain unverified. Final report presence and the producer call order support an inference, not recovered file evidence. Do not invent the expired sample's identity or fingerprint.

## Metrics size and time window

- `collect-terminal-evidence.sh:20,32–53` caps each capture at 4 MiB, remotely reads at most cap+1, and deletes the temporary file on overflow. A captured count of 4,194,305 is the sentinel/lower bound, not the complete response size.
- The archive manifest records only range `oversized`, 4,194,305 bytes, `capture_failures=1`, `complete=false`. This is consistent with the exact source. The fixture probe demonstrates the same result from an otherwise valid oversized Prometheus JSON response: exit 1, no retained range and no temporary range.
- The existing safeguard correctly avoids pretending truncated JSON is valid evidence. Increasing the limit alone does not establish bounded coverage at production cardinality. Actual full response size, dominant metric family, and labels cannot be recovered from this artifact.
- Lines 96–100 query a single broad selector for 900 seconds ending at collector wall time, step 15 seconds. No per-query requested window is written into the manifest. Collection delayed over 15 minutes after failure therefore excludes the failure instant by construction. The original collection ended 17:20:11Z and the workload failed 17:06:50Z, so this source fact does not prove that the first query excluded the failure. Later recollection must not be presented as if it used the same window.
- Both finalizers change an otherwise passing verdict to `harness_invalid/terminal_evidence_incomplete` when evidence fails. They preserve prior product failure; this collector failure cannot turn the original `message_loss` into pass or make it disappear.

## Smallest repair boundary

Keep runtime delivery behavior, sampling thresholds, producer schemas and paid workflows unchanged. Repair the terminal collector in `scripts/chat-lifecycle/collect-terminal-evidence.sh`, with a small bounded JSON/manifest helper under the same directory only if shell validation would duplicate complex schema logic. Add process-level fixture coverage in a new `scripts/chat_lifecycle_terminal_evidence_integration_test.go` with the `integration` build tag; update existing static expectations only after the red tests. Add a concise `CHANGELOG.md` entry and `docs/development/PROJECT_KNOWLEDGE.md` fact.

The existing stage argument and sibling fetched reports are sufficient to identify rehearsal versus formal/capacity report locations; define exact required/not-started states before implementation. Collect worker evidence (1 MiB) and diagnostic status (existing 64 KiB producer cap) from the matching remote stage directory, validate closed schema/run identity/three unique workers/generation and sequence, then retain exact accepted bytes and a hash/status receipt. Formal failure before capacity starts must not falsely require a nonexistent capacity document. Missing or invalid required evidence stays explicit and never upgrades the original verdict.

For metrics, preserve the original selected signal set while replacing one oversized response with a finite set of disjoint queries, for example six fixed families: `up`, `wukongim_runtime_.*`, `wukongim_channel_worker_.*`, `wukongim_process_.*`, the two metadata/activation counters, and `wukongim_go_.*`. Keep each response at 4 MiB and enforce an explicit total byte budget and shared collection deadline (six full files imply a stated maximum of 24 MiB, not unlimited growth). Retain successful partitions even if a sibling is oversized/unavailable; `complete` is true only if every required partition is valid. A partition still over the bound remains incomplete; do not recursively split without a fixed cap or downsample/retry until green. If preserving the existing 4 MiB total range budget is preferred, share that budget across partitions and document that completeness is not guaranteed. Either choice must be an explicit implementation decision with tested limits.

Anchor the requested diagnostic window to the validated terminal report's end time, retain the configured 900-second/15-second semantics, and record source/anchor/start/end/step and query name for every partition. A missing valid terminal anchor needs a typed fallback marked as such, never silently claiming failure-window coverage. Any extra calls must fit cleanup's existing time bound; collection may not delay release indefinitely.

No edit to `.github/workflows`, Issue/Review Agent policy, Skill files, `AGENTS.md`, or `FLOW.md` is required. `.github/issue-agent/policy.json` lists `.github/workflows` and agent control paths as protected for Issue Agent candidates. Reading the current workflows does not authorize changing them; the above repair avoids them.

## Tests to write before implementation

1. Actual collector plus fake SSH retains exact bounded worker/status bytes and their hashes for rehearsal; the current source must fail this assertion.
2. Formal-only terminal failure, formal-to-capacity continuation, and capacity terminal cut select only the correct run/stage paths; a stage never started is distinctly not applicable.
3. Worker/status absent, empty, malformed JSON, wrong schema/run, duplicate/missing worker, zero/invalid generation or sequence, and forbidden raw identity/error fields remain incomplete without leaking contents.
4. Exact byte boundary passes; boundary+1, truncated transfer, nonzero SSH/curl, and timeout fail with fixed status and clean temporary files. Earlier accepted evidence is not overwritten by a failed later fetch.
5. Metrics combined response exceeds 4 MiB while each fixed partition fits: all original families and 15-second samples are retained with a complete partition manifest. This must be red against the current single-query collector.
6. One oversized/error/invalid/missing partition retains valid siblings but sets incomplete. Wrong result type, Prometheus error envelope and mismatched metric family do not pass merely because `.data.result` is an array.
7. Total bytes/query count/deadline remain bounded; exhausted budget prevents new queries and records remaining partitions as uncollected. No recursive/unlimited retry or silent coarse step is allowed.
8. Pin fake wall time well after failure; query start/end derive from the exact terminal end. Invalid/missing/future anchor and unavailable historical Prometheus samples report truthful coverage.
9. Missing/invalid evidence cannot replace an original product verdict. Both stage finalizers still prevent incomplete evidence from authorizing a passing rehearsal/formal chain.
10. Successful metadata-only assertions never expose raw logs, UIDs, channel IDs, message bodies, credentials, private keys, or arbitrary error text in manifests/test artifacts.

The current default-tier collector test is a no-network/no-background fixture; keep it as historical evidence. New tests involving deadlines/process behavior belong in the integration tier. Suggested future focused invocation: `GOWORK=off go test -tags=integration ./scripts -run 'TestChatLifecycleTerminalEvidence' -count=1 -timeout=3m`. This proposed command is not a claim that future tests exist or pass yet.

## Files and reproducibility

`source-freeze.json` contains applicable rule/FLOW/source digests and clean-tree checks before/after. `validation.json` has exact commands and exits. `probe-collector.py` runs only the real collector against deterministic fake transports and never executes remote command strings. To reproduce the retention failure: `python3 probe-collector.py EXACT_SOURCE_WORKTREE --expect-worker-retained` (observed exit 1). `probe-results.json` contains sanitized manifest/status evidence. No production `message_loss` replay was possible with the retained aggregate-only artifact.
