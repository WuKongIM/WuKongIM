# MQTT first group subscription admission

Source base `49c74b1c0`; the existing `codex/mqtt-design` worktree is reused.
Governing source-revision digests and approved public seams are in
[frozen context](frozen-context.json). Binary/source and fixture provenance are
recorded in [candidate](candidate-provenance.json) and
[external probes](diagnostic-provenance.json).

## Reproduction and evidence

- [Original 40-case three-node RED](baseline-three-node.json): the first empty
  group SUBSCRIBE fails at 5,000 ms. This earlier fixture uses zero Session expiry,
  a ten-second receive budget and a five-minute scenario budget. Its delivery and
  later setup failures are separate; they do not establish the subscription cause.
- [Persistent minimized RED](persistent-minimized-red.json) uses the historical
  persistent Session and 30-second receive settings. The first packet still
  fails at five seconds. [Signals](persistent-minimized-signals.txt) locate
  repeated source preparation and replay confirmation in the same request.
- Parallel-only candidate: [one minimized pass](parallel-only-minimized-green.json)
  at 4,443 ms does not prove repair. In the [full matrices](matrix-candidate-sequence.json),
  the 40 single-node cases pass, but three-node fails its first
  packet; the other 39 cases and their future deliveries pass. The entire run
  takes 961.398 s. [A further probe RED](parallel-only-probe-red.json) and
  [fixed-label timings](parallel-only-signals.txt) show two pending confirmation
  phases repeating roughly 0.5 s of source preparation before final proof.
- Successful-anchor-only candidate: five independent [cold-start runs](cold-candidate-sequence.json)
  pass the single first empty subscription and future identity without a retry.
  SUBSCRIBE takes 3,928–4,757 ms. A [full matrix](matrix-candidate-sequence.json) still fails its first cold packet
  (955.745 s total). [Retained trace](anchor-only-trace-2.txt) reproduces a
  no-anchor initial plan followed by a successful Step with `Anchored=false`
  after a background anchor commit. Its redundant yield repeats preparation.
  A subsequent candidate rereads coverage after any successful bounded Step,
  but still fails the tenth of ten independent cold runs.
- [Later candidate sequence](cold-candidate-sequence.json): the successful-Step
  candidate passes nine cold runs then fails at 5,000 ms. Request-owned positive
  preparation passes six then fails its seventh. A retained preparation probe
  verifies exact Owner/UID/full-child reuse in [fixed fields](preparation-continuation-signals.txt);
  duplicate plan reads inside Step remain. The captured-plan candidate passes
  all ten independent cold runs and all 80 matrix cases, including future message
  identity. The subsequent supervised candidate passes ten independent cold runs
  at 3,191–3,951 ms and all [ten interruption/mixed-result cases](supervised-fault-cases.json).
  Its full ordinary matrix passes all 80 cases (967.374 s). Ten independent
  existing-history cold starts also pass, including future identity, at
  2,577–3,001 ms. Final admission acceptance comprises 20 independent cold
  starts, 80 matrix cases and ten fault/recovery cases, with no packet retry or
  deadline enlargement.

## Repair and safety bounds

The product's concurrent-safe Node opts into at most four joined confirmation
calls; other providers default to serial. Body-free results are bounded to the
validated placement's 256 replicas. No background queue or persistent worker is
created. Every admitted call joins before return; a hard error cannot acquire
pending classification from a different replica. Cancellation wins after join.
The cohort uses the existing supervisor's fixed MQTT burst identity; the mixed
process cases assert its public starts counter. Its absence is first reproduced
on the previous temporary build. [Validation](validation.json) retains the gate results.
Worker count follows physical placement, never group member count. Each request
adds one body-free preparation hint and at most four transient calls; the
existing adapter read/receiver admission still supplies backpressure. There is
no new resident state per group, recipient fanout, or retained message page.

One request retains only positively prepared source/start under its complete
Preparing child, exact Owner and UID. Later turns still reread intent and
reauthorize; a different packet prepares again. Confirm captures fresh placement
and progress, uses that validated plan for one bounded effect-fenced copy/anchor,
then reads fresh coverage before all-replica confirmation. It shares Step's
copy/anchor validator without invoking maintenance recovery or source release.
No second copy is submitted. The original source start and
placement must remain unchanged, progress cannot regress, and every replica
must match the captured anchor before the final fresh placement fence. Unknown
copy/anchor/read outcomes stop. Incomplete coverage stays pending. Packet deadlines,
foreground retries, source release and existing cluster authority paths stay intact.

The stage evidence identifies serial awaited RPCs and repeated projection work;
a CPU profile is not required to locate those waits. This is the documented
reason for the pprof SHOULD deviation. It does not attribute CPU cost or qualify
100k workloads, partitions, failover, or restored-workload throughput.

The additional read-phase, preparation and captured-plan isolated contracts were
written before each phase's implementation. The old nested-recovery test is
updated test-first to require one admitted copy/anchor before a rejected
confirmation, and no repeated copy on later proof. Existing negative
copy/anchor/read/replica/fence contracts remain required. A temporary gofail
process scenario combines a hard replica result
with another replica's pending result, refuses partial SUBACK and checks recovery
of the original Session without resubscription. Raw configs and application logs
remain outside Git; only bounded outcomes and fixed probe fields are retained.

## Reproduce

```sh
GOWORK=off go build -buildvcs=false -o /tmp/wukongim-mqtt-subscribe ./cmd/wukongim
WK_E2E_BINARY=/tmp/wukongim-mqtt-subscribe WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-subscribe-admission GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/subscribe_admission -count=1 -timeout=25m -v
scripts/build-gofail-binary.sh --package internal/usecase/mqttsession --out /tmp/wukongim-mqtt-subscribe-gofail
WK_E2E_BINARY=/tmp/wukongim-mqtt-subscribe-gofail WK_E2E_GOFAIL_MQTT=1 WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-subscribe-faults GOWORK=off go test -p 1 -tags=e2e ./test/e2e/mqtt/subscribe_recovery -count=1 -timeout=10m -v
```
