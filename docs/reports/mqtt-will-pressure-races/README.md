# Will capacity and applied CAS reply qualification

Source base: `a8559a6bad3f48337315f814dc664b3c221e76b6`, local branch
`codex/mqtt-design`. The [failure inventory](../../specs/mqtt-will-pressure-races.md)
preceded the changes. [Source context](source-context.json) freezes applicable
AGENTS/FLOW digests, tested fixtures and binaries. No business behavior changes:
product edits are inert temporary gofail controls. No Changelog entry is needed.

## Evidence

| Process acceptance | Result | Business publications |
| --- | --- | --- |
| Default capacity 1,024, single-node cluster | PASS, 749.098s, joined-client rerun | 1,025 |
| Default capacity 1,024, three-node cluster | PASS, 467.413s, joined-client rerun | 1,025 |
| Applied Started/successor/terminal replies and canceled page | PASS, six cases, 638.060s | Three per case |
| Existing cap-two pressure regression | PASS, both topologies, 144.938s, joined-client rerun | Three per case |
| Ordinary Will Delay/abnormal close/normal cancellation | PASS, both topologies, 55.116s | One per case |
| Cancellation-ignoring cleanup negative control | Expected FAIL, four runs | Wrong cleanup bypasses second-page assertion |

The default-capacity tests hold one current attempt and retain 1,023 terminal
attempts through failed cleanup. One joined receiver validates every distinct
receipt; at most 32 submitted lifetimes await receipt. The next Will proves real
capacity refusal on the captured executor, bounded reclamation and one later
publication. Exact restart then recovers the held current attempt; persistent
reconnect uses the original subscription. Both cases have zero unexpected
delivery, CONNECT retry or resubscription. This is capacity correctness, not a
throughput or sustained-scale benchmark. Both final full-cap cases use the same
instrumented candidate and joined-client fixture digest. Earlier 702.393s/413.282s capacity runs remain separate evidence.

Race controls delay replies after real validated applied CAS results. Selected
long gofail actions keep sleeping after their control is cleared; clearing stops
unrelated calls sharing gofail's term mutex from queuing behind the same action.
The canceled-page case exceeds 750ms, rejects subsequent pages, requires another
page with capacity still occupied, and only then restores fresh cleanup. Current
attempts remain protected. QoS replay of a begun original exchange is recorded
separately from distinct business identity and actual publication attempts.

Initial fixtures failed on unobserved QoS replay, shared gofail term contention
and a repeatable single-node after-pressure receipt timeout. The latter reached
publication attempt two after cleanup; that did not prove append completion or
identify a product defect. Joined-client tracers and repeated single-node runs
passed, but another complete 35s-window matrix still failed. A diagnostic three-case single-node run passed;
public history also showed the after-pressure commit. This does not explain the
earlier timeout causally. The final fixture records reception time and uses a
60s completion window for fresh claim/native append/background projection; the
750ms canceled-page assertion, actual publication counts, current preservation
and duplicate checks still govern acceptance. The final six-case run passes in
638.060s; the single-node late-release receipt took 41.631s, directly exceeding the previous arbitrary 35s observation budget.
That explains why 35s is insufficient as a completion gate, without establishing
the precise latency cause. The delivered fixture additionally passes its
105.977s late-release rerun. A final refactor moves only failure-path pprof capture
to the shared suite; the real-process helper probe saves 946,209 bytes within its
1 MiB bound. Source hashes distinguish the matrix and delivered fixtures.
All earlier failed receipts are retained separately. No latency SLO is qualified.

The [negative control](negative-control-source.json) changes only a private
instrumented copy of `ReleaseAttempt` to ignore cancellation. It repeatedly fails
at `late-page-retains-capacity` because the required subsequent page is absent;
no such change enters product source. Raw logs/profiles remain outside the repo;
bounded JSON is emitted after owned resources have been stopped and joined.

## Repeat

Use Go 1.25.11 with `GOWORK=off`. Build the ordinary product with
`go build -buildvcs=false -o /tmp/wukongim-will-pressure-ordinary ./cmd/wukongim`.
Build the temporary instrumented product:

```sh
GOFLAGS=-buildvcs=false GOWORK=off scripts/build-gofail-binary.sh \
  --out /tmp/wukongim-will-pressure-gofail \
  --work-dir /tmp/wukongim-will-pressure-source --keep-work \
  --package internal/usecase/mqttsession --package internal/app \
  --package internal/infra/mqttwill --package internal/runtime/channelappend \
  --package pkg/slot/proxy
```

```sh
WK_E2E_GOFAIL_MQTT=1 WK_E2E_MQTT_WILL_CAPACITY=1 \
WK_E2E_BINARY=/tmp/wukongim-will-pressure-gofail \
WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-will-capacity \
GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/will_reclamation \
  -run TestWillJournalProductionCapacityPreservesCurrentRecovery -count=1 -timeout=70m -v

WK_E2E_GOFAIL_MQTT=1 WK_E2E_BINARY=/tmp/wukongim-will-pressure-gofail \
WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-will-races \
GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/will_reclamation_races \
  -count=1 -timeout=25m -v
```

Optional `WK_E2E_MQTT_LOG_DIR` retains private process logs and enables the debug
API only on this fixture's loopback listener. It is not required for acceptance.
The negative copy/rebuild procedure and hashes are recorded separately.

Related existing race tests pass (`mqttsession`, 40.347s; Slot proxy, 6.292s).
The ordinary build has no gofail dependency. The named `flow-doc-contracts`
check reports 88 compliant, zero invalid and 11 existing warnings.
[Validation results](validation-results.json) retain commands, source/binary/log
hashes and outcomes; [receipts](receipts) keep failed fixtures separate from
qualified runs.

## Review and limits

The [final review](review.md) preserves separate [Standards](standards-review.md)
and [Spec](spec-review.md) reports with initial findings and their dispositions.
Standards has zero unresolved documented violations; Spec has zero actionable
implementation findings within the approved slice. Two standards heuristics
remain with documented reasons.
Failed authority-read starvation from inventory item 6 has no new process proof.
Uncommitted delayed Raft apply, network partitions, corrupt/lost journals,
missing-row reclamation, complete issued-effect terminal recovery and shared
storage admission remain open. Complete MQTT and release qualification remain
in progress. The unmerged worktree is retained; no push, merge or release occurs.
