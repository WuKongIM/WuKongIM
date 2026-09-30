# Sequential SEND delivery follow-up

The original comparative verdict remains **FAILED** (6/12 sequential rows); no
additional diagnostic replaces or retries any of the six original runs. Runtime
remains `ec21c1a586941ffd74cfb497f0da4522941bc1a4`, driver remains
`c0e95b5a45babbd1907c590a9dcdab83e74634a9`, whose compiled harness matches the
previously validated `713871701b440bab6e8ab5536e5a0a1621c37984` source.

All nine Draft PR scopes fit the unchanged main-only Review Agent material limits.
The reconstructed primary archive verifies all 194 selected file hashes and its
195 members. Running the extracted verifiers reproduces FAILED comparative
acceptance (exit 1 as required) and all nine passing Linux windows (exit 0).
The primary archive slices and all existing historical evidence bytes remain unchanged.
Final driver first-attempt automatic correctness, messaging unit/race, Manager
Chromium and all three fixed 500-SEND/s seam jobs pass; skipped jobs remain skipped.
These driver checks do not certify the runtime comparison or a release.

## Deterministic request position and extra diagnosis

In all three original new runs the same-leader/two-Slot maximum occurs at
`two-slots-one-remote-leader-c1-038` (the 39th measured SEND, message sequence 40):
32.188, 32.028 and 32.101 ms. The same position is also the maximum in all three
previous main-integration new runs (28.214, 37.111 and 29.050 ms). The corresponding
old runs instead peak at c1-003. All original raw observations remain retained.
This repeated position provides a concrete investigation target; it is not a
causal attribution.

A separate GODEBUG=gctrace=1 attempt failed before product start because the owned
TMPDIR made the plugin socket path exceed 100 bytes. Both startup failures remain
retained. The corrected short-TMPDIR old/new diagnostic passed functional controls
and captured <=64 KiB of stderr for each owned node. Its extra observer/GC-output
cost makes it unqualified. Timing correlation uses rounded runtime GC seconds,
Mach process-start identities and reconstructed per-ACK intervals; the 3-ms
allowance is not a certified clock bound. It provides no affirmative attribution
of the repeated target tail to GC. The new diagnostic also has a 323.309-ms
same-Slot maximum and 46.520-ms two-Slot maximum; these observations remain kept,
without being relabeled or used to replace the original comparison.

A fresh full-prefix candidate diagnostic preserves the four-placement sequence
and adds only one 120-second, 100% debug match for c1-038, leaving baseline sample
rates and buffer capacity unchanged. After that placement completes, it queries
only that request on three owned loopback nodes, once each, with the existing
2-second/64-KiB/32-event bounds. Every query is untruncated and credential/payload/
sender-field safe; functional controls and all four exact histories pass.
This run is explicitly diagnostic and has no profiling or GC logging.

The target SEND takes 44.137 ms. Its ingress permission span is 0.181208 ms and
ordered append-admission wait is 0.003 ms. Channel append encloses 41.015250 ms;
`replica.leader.local_durable` encloses 40.963334 ms. The matching public events
share one trace identity and preserve their original timestamps. They overlap and
must not be summed. Node 3 returns an explicit empty timeline.

The latter span's name is not a disk-only attribution: reactor append_flush.go
marks store submission after scheduling TaskQuorumCommit, and append.go calls
observeAppendStoreCompleted only after the quorum worker receipt returns.
Thus it includes worker scheduling/execution/quorum-result handling. The
0.019625-ms `quorum_wait` is the subsequent reactor publication interval,
not proof that replica communication was negligible. `pkg/channel` and `pkg/db`
source trees are unchanged between old424 and runtime ec21c1a. This localizes the
remaining target cost beyond permission/ordered admission, but does not establish
the cause or justify another product change without finer bounded evidence.

## Provenance

The primary archive is still 1,514,844 bytes, SHA-256
`2eb068e94ce358e333bd43691d546895449a8cb907c1e12bb94f9b0b1e219c5f`.
This separate follow-up bundle retains the failed startup attempts, all three
extra diagnostic receipts, owned stderr logs, raw target replies, execution
plans/scripts, final source/check receipts, PR snapshot, complete-file scope
accounting and archive round-trip receipt. No binaries or protected policy edits
are included. All PRs remain Draft; no merge, release or formal Review Agent
command was performed.

The [supplemental archive](2026-10-01-permission-delivery-followup.tar.gz) and [receipt](2026-10-01-permission-delivery-followup-archive.json) contain 42 members. Its internal manifest verifies every selected file. This supplement is added to #989; the original two sequential slices still concatenate to the unchanged primary archive described in #980. The embedded report was frozen before this supplement was added.
