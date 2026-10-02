# Will proposal, quorum commit and FSM apply qualification

Base: `dcaaaa8b315b1805ffef42d8bfbfcb1719edc4ca`, local branch
`codex/mqtt-design`. The [failure inventory](../../specs/mqtt-will-raft-recovery.md)
preceded changes. Product changes are temporary controls and empty ordinary
selectors, with no business behavior change; no Changelog entry is needed.
[Source context](source-context.json) freezes instructions, candidate and fixtures.

## Acceptance status

All ten delivered-fixture late/crash cases have passing evidence: the final
matrix passes nine in 758.215s and fails one three-node committed/unapplied late
case before fault setup at the unchanged 30-second readiness deadline. Every
control count remained zero; this is not business acceptance or a product
repair claim. Its unchanged-source/binary/budget repeat passes in 85.454s.
This is combined case coverage, not a clean ten-case matrix run. The corrected
private negative control fails the intended original-receipt assertion.
[Validation commands, logs and hashes](validation-results.json) preserve both
outcomes and [the retained startup failure](receipts/final-matrix/mqtt-will-raft-3-committed-unapplied-late.json).
The initial full matrix passed 7/10 in 707.694s. The corrected three-node queued
tracer passes in 84.415s: the old executor has no definite Started reply, a new
executor has one real applied successor reply and one publication, and persistent
reconnect replays the same deliberately unfinished original with DUP. The delivered
three-node queued cases additionally preserve the original PacketID. Earlier failed fixtures are
retained as failed evidence, not product RED or a business repair claim.

| Topology | Cut | Late completion | Crash/restart |
| --- | --- | --- | --- |
| Single-node cluster | Queued before RawNode | PASS, 75.92s | PASS, 61.07s |
| Single-node cluster | Committed before FSM | PASS, 73.75s | PASS, 79.84s |
| Three-node cluster | Queued before RawNode | PASS, 87.18s | PASS, 82.03s |
| Three-node cluster | Committed before FSM | Startup FAIL, then unchanged PASS, 84.67s | PASS, 84.41s |
| Three-node cluster | Persisted, not quorum-committed | PASS, 77.19s | PASS, 99.81s |

Times in the table are case durations; 758.215s and 85.454s are package times.
[Final matrix receipts](receipts/final-matrix), [unchanged case repeat](receipts/startup-case-repeat),
[negative receipt](receipts/negative-final/mqtt-will-raft-1-queued-late.json) and
[ordinary regression receipts](receipts/ordinary-regression) are bounded JSON.

All cases use 12 logical Slot groups and 256 hash Slots. Each first publishes and
retains one exact terminal attempt through failed cleanup, then creates the
current Will using the same ClientID and a two-record instrumented cap. Counter
epochs restart only after the first receipt and reached cleanup failure. The
late committed/unapplied, persisted/uncommitted and single-node queued cases
require real full refusal on that journal after delayed FSM resolution, fresh
terminal reclamation and the original receipt. A three-node queued takeover
instead requires definite successor authority and all-node convergence after
the old worker resumes; the old proposal may be discarded without an FSM result.
Crash placement may change and does not promise same-node pressure.

The three states are separate: a future queued before RawNode proposal, an entry
persisted above the current quorum commit index (three-node only), and a committed
entry paused before any Started FSM mutation. Applied-reply delays do not qualify
these cuts. The persisted marker runs only after successful log persistence;
matched MsgApp batch loss keeps ordinary heartbeats/voting and does not fabricate
commands or results. Committed evidence concerns the captured authority node;
other replicas may already have applied. Public leader commit/apply gaps corroborate
that cut. Twenty-one seconds exceeds the original five-second turn and ten-second
grant; an unknown old claim grants no dispatch. Fresh definite successors may
publish one original. Killed-process counter prefixes stay in total effect counts.

The one-group pressure fixture closed its recipient inside the observation
window and failed. Using the root example's 12 groups keeps the chosen Slot cut
from stopping the entire metadata runtime; no lease, packet or grant timeouts
changed. This supports fixture isolation, not a precise EOF root-cause claim.
The initial gofail string selector also failed before business assertions:
v0.2.0's parser cannot preserve quoted JSON bytes; bounded hex selectors now
select opaque command bytes. An initial >= negative mutation was overridden
by the exact-current stage check and was ineffective. The corrected private
[negative control](negative-control-source.json) incorrectly retires a current
nonterminal row; the previous fixture then reaches real full refusal but loses
its original receipt in 152.228s package time (151.45s case time). The delivered
fixture repeats this business failure in 153.682s (151.50s case time), after
reached capacity refusal and persistent reconnect; processes remain ready.
No unsafe negative change enters product source.

## Repeat

Use a fresh temporary build directory, Go 1.25.11, `GOWORK=off`, and the saved
toolchain/source hashes. Build the
ordinary product with `go build -buildvcs=false -o /tmp/wukongim-will-raft-ordinary
./cmd/wukongim`. The ordinary build has no gofail dependency and does not link
the temporary hex-match helper, verified with Go build metadata and symbols.

```sh
GOFLAGS=-buildvcs=false GOWORK=off scripts/build-gofail-binary.sh \
  --out /tmp/wukongim-will-raft-gofail \
  --work-dir /tmp/wukongim-will-raft-repeat-source --keep-work \
  --package pkg/slot/multiraft --package pkg/slot/fsm --package pkg/slot/proxy \
  --package internal/usecase/mqttsession --package internal/app \
  --package internal/infra/mqttwill --package internal/runtime/channelappend

WK_E2E_GOFAIL_MQTT=1 WK_E2E_BINARY=/tmp/wukongim-will-raft-gofail \
WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-will-raft \
GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/will_raft_recovery \
  -count=1 -timeout=25m -v
```

Optional `WK_E2E_MQTT_LOG_DIR` retains private product logs. Reports omit bodies,
UIDs, tokens and client identities; client/process cleanup joins before JSON
emission. Raw logs and temporary sources/binaries remain outside the repository.

## Validation and limits

Existing focused race gates pass: Multi-Raft 3.125s, FSM 4.722s, proxy 3.856s,
MQTT session 24.639s. Ordinary Will regression passes both topologies in 50.474s.
The named FLOW check passes: 88 compliant, zero invalid, eleven pre-existing
line-count warnings. Final [Standards and Spec review](review.md) reconciles this delivered evidence.

Only first-generation Started commands are directly cut. Direct successor-CAS
faults, independent pressure reclamation while an older row's proposal remains
in flight, failed authority-read fairness, full issued-effect terminal recovery,
lost/corrupt journals, general partitions and shared-storage admission remain
open. This is not latency, sustained throughput, complete MQTT or release
qualification. Keep the unmerged worktree; no push, merge or release is included.
