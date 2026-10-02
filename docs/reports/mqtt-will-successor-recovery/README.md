# Will successor CAS and independent in-flight pressure acceptance

Base: `423187b33b2784c2d79ffaff5621ee4c8629dabe`; local branch
`codex/mqtt-design`. The [failure inventory](../../specs/mqtt-will-successor-recovery.md)
preceded the tests and product observations. [Source context](source-context.json)
freezes instruction, fixture, binary and generated-source hashes.

## Status

Final fixture v11 passes **14/14** in **1272.474s** package time. Its exact
unsafe-reclamation negative fails the intended real publication-attempt assertion
in **49.197s**, after reached refusal/page and seven premature captured attempts.
Ordinary Will passes both topologies in **53.302s**; all four focused race packages
and the named FLOW gate pass. Scoped source Standards and Spec reviews report no
open findings. See [review](review.md) for the final delivery evidence review.

The single-node queued pressure tracer passes with thirteen effects, an exact
held QoS exchange replay set, original message identity/PacketID/DUP, and strict
final quiet. The three-node persisted/uncommitted late tracer passes; its crash
tracer passes after allowing legitimate pressure originals during the pre-close
ACK observation. Ordinary Will passes both topologies, four focused race packages
pass, and the named FLOW check reports 88 compliant, zero invalid and eleven
existing line-count warnings.

## Final matrix

| Nodes | Cut | Recovery | Pressure clients | Business originals | Seconds |
| --- | --- | --- | ---: | ---: | ---: |
| 1 | committed-unapplied | crash | 0 | 1 | 63.317 |
| 1 | committed-unapplied | late | 0 | 1 | 91.669 |
| 1 | queued | crash | 0 | 1 | 63.452 |
| 1 | queued | crash | 12 | 13 | 88.065 |
| 1 | queued | late | 0 | 1 | 93.640 |
| 1 | queued | late | 12 | 13 | 139.128 |
| 3 | committed-unapplied | crash | 0 | 1 | 94.004 |
| 3 | committed-unapplied | late | 0 | 1 | 109.781 |
| 3 | persisted-uncommitted | crash | 0 | 1 | 85.115 |
| 3 | persisted-uncommitted | crash | 12 | 13 | 88.968 |
| 3 | persisted-uncommitted | late | 0 | 1 | 78.207 |
| 3 | persisted-uncommitted | late | 12 | 13 | 92.910 |
| 3 | queued | crash | 0 | 1 | 81.608 |
| 3 | queued | late | 0 | 1 | 101.640 |

## What is observed

A real first-generation Started CAS applies before the instrumented pre-dispatch
turn returns. Its normal ten-second grant expires. The second-generation caller
reserves exact evidence before an actual queued, persisted/uncommitted, or
committed/pre-FSM boundary. Applied reply and durable resolution remain separate
observations. Only the selected ClientID contributes original successor markers;
independent pressure clients cannot contaminate them. Public Manager commit/apply
gaps corroborate committed-before-FSM cuts. Persistence markers require an index
above quorum commit; matching real Append batches stay lost through a captured
leader's joined kill.

Ten cases cover queued and committed/unapplied late/crash in both topologies,
plus three-node persisted/uncommitted late/crash. Four pressure cases pre-admit
twelve different ClientIDs, then abort them while the original proposal is still
unresolved. The captured node's cap-two journal holds its old sealed attempt and
new reservation. Actual refusal and an actual reclamation page must occur before
resolution. Actual reads must retain each attempt on current/unknown authority;
read errors retain it too. The captured full journal cannot fund an independent
publication, while other nodes may use spare capacity.

The pressure pause is 90 seconds for a queued proposal; persisted/uncommitted
Append loss stays until explicit recovery or joined kill. A separate 110-second
late-completion observation lets a selected sleep finish. Ordinary cluster
startup/restart readiness is observed for 60 seconds. Runtime call/grant bounds,
21-second unknown observation and final 15-second healthy quiet stay unchanged.
No latency SLO follows from these completion bounds.

After late pressure, ordinary 1,024-record capacity is restored without deleting
uncertain evidence. Crash cases keep captured cap-two admission through the final
old-process effect count, SIGKILL and joined exit; then surviving nodes restore
admission and the replacement boot starts with ordinary capacity. No progress
guarantee at a full unreclaimable journal is claimed.

Paho manual ACKs flush in receive order. The deliberately unacknowledged original
holds later pressure ACKs too. Track that exact pending set, prove each reconnect
replay retains message identity, PacketID and DUP, then ACK it and the original
before strict final quiet. A bounded healthy pre-close observation lets preceding
ACKs flush. Before the first reconnect, it accepts only admitted pressure traffic
and still rejects the unresolved original. All receipts finish with zero CONNECT
retry and no repeated SUBSCRIBE. Effect counts retain killed-process prefixes.

## Retained failures and sensitivity

Initial binaries lack new controls; those failures are instrumentation gaps.
The first pressure completion fixture expires a 30-second readiness wait during
an already-selected 90-second sleep. Later fixtures receive known pressure QoS
replays in final quiet. Body-free diagnostics prove same identity/PacketID/DUP;
waiting three seconds alone remains ineffective because ACK ordering holds those
exchanges behind the original. The exact pending-set loop resolves that fixture.
Another fixture wrongly demands quiet while independent nodes can still publish
pressure originals. Its bounded ACK stage now accepts this expected traffic.
The v9 exploratory matrix passes 13/14 in 1199.369s; its startup-only failure
has every fault counter zero and is retained;
the separate setup/convergence observation changes to 60 seconds.

The private generated-source negative erases a second-generation reservation
before fresh authority. Its v5/v9 runs fail the real captured-node publication
assertion after reached refusal/page, with five/seven premature publication attempts. The
candidate and negative generated trees differ only in the recorded usecase file.
The delivered v11 negative fails the same real assertion after reached full/page,
with seven premature captured attempts (49.197s package, 48.51s case).
No unsafe mutation enters the
product source. Temporary gofail controls and empty ordinary selectors do not
change user-visible behavior; this slice requires no Changelog entry.

## Repeat

Use Go 1.25.11 and gofail v0.2.0; the private toolchain/cache prefix used here is
recorded in `validation-results.json`. Build a temporary instrumented copy:

```sh
GOWORK=off GOFLAGS=-buildvcs=false scripts/build-gofail-binary.sh \
  --out /tmp/wukongim-will-successor-matched-gofail \
  --work-dir /tmp/wukongim-will-successor-matched-source --keep-work \
  --package pkg/slot/multiraft --package pkg/slot/fsm --package pkg/slot/proxy \
  --package internal/usecase/mqttsession --package internal/app \
  --package internal/infra/mqttwill --package internal/runtime/channelappend
WK_E2E_GOFAIL_MQTT=1 \
WK_E2E_BINARY=/tmp/wukongim-will-successor-matched-gofail \
WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-will-successor-final-v11-matrix \
WK_E2E_MQTT_LOG_DIR=/tmp/mqtt-will-successor-final-v11-matrix-logs \
GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/will_successor_recovery \
  -count=1 -timeout=45m -v
```

Reports are body-free, bounded JSON written after joined clients/processes. Raw
product logs and generated sources/binaries remain private outside the repo.
Failed-read fairness, full issued-effect terminal recovery, lost/corrupt journals,
general/asymmetric partitions, aggregate shared-storage admission, sustained
throughput, latency SLO, Linux qualification and complete MQTT remain open.
