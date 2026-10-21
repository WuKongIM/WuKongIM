# Deterministic RECV queue/expiry mechanism probe

**Confirmed mechanism, unknown historical attribution:** on unchanged cee5 runtime code, a valid sampled RECV queued in the fake client's existing bounded ingress queue at +9 seconds, but held from the actual SessionPool drain until +11 seconds, produces `expired=1/loss=1` when Engine advances to the configured +10-second deadline. Processing and acknowledging the RECV afterwards does not reverse that loss. True ingress at +11 seconds produces identical recorded aggregate and bounded evidence fields. The additional probe-only enqueue and processing receipts distinguish them.

This is not a real socket, protocol decoder, or server reproduction. It deliberately controls the existing `engineFakeClient.frames` transport seam and supplies a fake ACK observation clock. It cannot prove that historical cee5 had such queueing, and it does not change that run's `product_failure/message_loss` verdict.

## Actual path and isolation

Base/head: `cee5d51f325fb84033060f546337d326110adae5`; tree `ae5a4175337f10f521a13a6c704c1b3040cf2bd2`.

The detached `.worktrees/v3-rc-recv-probe` contains one untracked probe test. All tracked source and the original live-analysis worktree remain unchanged. Applicable AGENTS/FLOW/CONTEXT/ADR digests are frozen in `source-binding-before.json`.

The probe uses one sender, one distinct recipient, and one sampled logical SEND. SEND follows Engine admission → existing fake-client `TrySend` and ACK queue → actual `SessionPool.drain` → Verifier. RECV follows the same existing fake-client frames queue → actual drain → Verifier → RECVACK. The probe never directly calls `HandleRecv` or edits expiration counters. Each read requires a channel permit; entry into the next read proves the previous drain iteration completed. Clock values and explicit permits establish all ordering without sleeps or timing races.

The shared old test fixture defaults to one minute; the probe injects the actual run's 10-second configuration **before startup**. No production configuration or failure threshold changes. Its long-lived two-session fixture removes ordinary TTL churn from this experiment. Heartbeat waiting is canceled during joined shutdown; there is no network or 10,000-client load.

## Eight schedules and final results

| RECV schedule (relative to ACK at 0 unless specified) | Queue at expiry | Delivered / expired | Received / RECVACK |
| --- | ---: | ---: | ---: |
| Enqueue and process at +9s | 0 | 1 / 0 | 1 / 1 |
| Enqueue +9s, process +11s | 1 | 0 / 1 | 1 / 1 |
| Actual ingress and process +11s | 0 | 0 / 1 | 1 / 1 |
| No RECV | 0 | 0 / 1 | 0 / 0 |
| At +10s, complete RECV processing before expiry | 0 | 1 / 0 | 1 / 1 |
| At +10s, complete expiry before permitting RECV processing | 1 | 0 / 1 | 1 / 1 |
| Process RECV +9s, observe ACK +11s | 0 | 1 / 0 | 1 / 1 |
| Positive ACK remains queued/unobserved at +10s | 0 | 0 / 0 | 0 / 0 |

Every expired case retained the matching logical index/fingerprint and 10-second configured deadline. All eight cases joined shutdown with zero online sessions and zero active gated reads. Normal and race runs produced identical structured case receipts. See `mechanism-results-final.json`.

## Preserved execution history

- `attempt-1`: default local Go was 1.23.4, below go.mod's minimum; no test executed.
- `attempt-2`: first normal execution, all eight scenarios passed. Its initial projection is preserved in `mechanism-results.json`.
- `attempt-3-race`: exposed a **new probe-only** admission-result variable race. `enqueueBlocking` guarantees queue admission, not completion of its closure. The probe read that result without a completion barrier. This is not a product race finding.
- Only the probe was corrected to return the admission result through a one-shot channel. `attempt-4-corrected` and `attempt-5-corrected-race` both pass all eight scenarios. These are the canonical results. Original results and both source generations remain intact.

The failure matrix was recorded before probe implementation. This is a characterization of existing code, not a claim that new product functionality was developed test-first.

## Repeat offline

Use the existing probe worktree or copy the frozen test into a separate exact cee5 checkout. No tracked file needs modification. The locally cached toolchain was Go 1.25.11 darwin/arm64:

```sh
GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off \
  /Users/tt/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.11.darwin-arm64/bin/go \
  test -race -run '^TestOfflineRecvQueueExpiryProbe$' -count=1 -timeout=45s \
  -json ./internal/bench/chatlifecycle
```

The runner additionally bounded each subprocess group to 120 seconds; neither corrected run timed out. Exact commands, environment overrides, source hashes, output, elapsed time, and exit codes are retained per attempt.

## Smallest next observation

First collect the existing bounded `worker-evidence.json`; the historical archive currently lacks its sample index/fingerprint. Then, before another attribution experiment, specify bounded per-sample diagnostic facts that distinguish **real RECV ingress** from **verifier processing**: worker/generation, existing redacted sample fingerprint, positive ACK observation, configured/actual expiry boundary, RECV ingress timestamp when available, and verifier-processing timestamp. A missing source timestamp must remain unavailable, not be fabricated at drain time. Keep first/last retention bounded and omit raw identities, payloads, and arbitrary error text.

This probe's two differing ingress schedules should be the deterministic acceptance control for that observation. Do not fix this ambiguity by increasing the deadline, reordering ACK completion bookkeeping, accepting any late RECV, or weakening the zero-loss gate. Product-behavior repair and historical cause remain undecided.
