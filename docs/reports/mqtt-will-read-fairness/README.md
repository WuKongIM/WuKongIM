# Will reclamation under failed authority reads

Source baseline: `7583a33fb27706349ec77ea9113a70e95cf76204`.
Final v5 matrix: **4/4 PASS** across two clean topology invocations (single-node cluster
772.504s; three-node cluster 656.326s). The matched v5 private cursor-removal negative
fails the intended business receipt in 576.552s. Existing cursor behavior needs
no product business repair; this change adds bounded temporary-copy fault
controls and process acceptance.

| Cluster | Actual read error | Case seconds | Progress observation ms | Cursor advances | Exact selected releases |
| --- | --- | ---: | ---: | ---: | ---: |
| Single-node cluster | DeadlineExceeded | 357.85 | 209430 | 18 | 1 |
| Single-node cluster | Canceled | 413.35 | 197062 | 17 | 1 |
| Three-node cluster | DeadlineExceeded | 441.77 | 281944 | 24 | 1 |
| Three-node cluster | Canceled | 213.54 | 38569 | 2 | 1 |

Each [case artifact](qualification.json) records 31 terminal receipts, one pending
publication, one current original recovery, persistent identity/PacketID/DUP replay,
zero CONNECT retries/resubscriptions, and the distinct old 32 + live 1 effect counts.
This is four-case coverage across two commands, not one combined invocation.

## What this acceptance checks

Real product processes retain one current Started attempt and 31 independently
published terminal attempts in one captured journal. Both topologies keep 256
hash Slots and explicitly one logical Slot group; the temporary capacity is 32.
Actual capacity refusal must occur before reclamation evidence is credited.

The real cursor is held during calibration. Two completed page-membership probes
select a provisioned terminal ClientID outside that fixed page. Calibration
does not delete records, expose filename order, or read journal/Slot storage.
All nonselected candidates then call the actual foreground Store with its
existing child context expired or canceled. Observers count the Store's returned
context error before the independent late-response guard. No rows or CAS results
are synthesized. The normal 16-candidate, 250ms child, 750ms page and execution
grant bounds remain unchanged.

After disabling failed cleanup, 21 seconds of healthy recipient quiet and an
unchanged 31-publication prefix prove the held-page starvation fixture. Only
cursor advancement resumes. The required pending Will business receipt precedes
the cursor/selected-retirement assertions. Normal later execution reclaims one
exact terminal, yields, then claims/authorizes/publishes through ordinary paths.
The completion observation is 420 seconds, covering ordinary turn cadence; it
does not extend a runtime grant or establish a latency SLO.

ACKs join during three seconds of healthy quiet before recipient closure and
exact captured-executor SIGKILL/join. Surviving nodes/replacement startup then use
ordinary admission. Current recovery produces one original; persistent reconnect
keeps its identity, PacketID and DUP without SUBSCRIBE. A final ACK and 15 seconds
of deadline-only quiet complete the case. The old-process prefix is 32; live
process counters add one, never replacing the killed prefix. Body-free JSON is
written after client/process cleanup.

The replacement boot enables only the publication observer. Its cleanup-time
`final_observers_reachable: false` means old read/cursor observers have no enabled
replacement counter; this is not process-health evidence. Old cursor/page/release
values and the pre-crash prefix are captured before the joined kill; public
receipts, replay and the live publication counter verify replacement behavior.

## Sensitivity and retained failures

The [private patch](evidence/cursor-negative.patch) removes exactly one actual
`scanAfter = names[start]` assignment from a generated temporary adapter.
The entire 7,034-file generated source copy comparison finds that one changed
file; read limits, faults, candidate selection and publication code are identical.
[Binary/source hashes](provenance.json), [tree comparison](evidence/negative-provenance.json)
and the exact generated source snapshots retain this provenance. Files with
verbatim generated/log whitespace use [lossless gzip archives](raw-archives.json);
decompression is verified against each original SHA-256.

The delivered v5 fixture's single-node deadline negative reaches actual refusal,
page-external calibration and healthy held-cursor quiet, then fails only the
required `after-pressure` business receive after its 420-second observation.
At cleanup it has 49 full refusals, 126 actual returned deadline errors, 31
publication attempts, zero cursor advances and zero selected releases. Errors
and refusals continue increasing after cursor control release. This is a
qualified expected failure, not a setup or counter-only failure. The negative
does not separately qualify the canceled or three-node variants.

Earlier evidence remains: v1 fails the instrumentation prerequisite; the first
gofail build fails comment-block generation; v2 serial fill and v3 batched fill
fail the insufficient 15-second two-probe observation. v3 records one completed
probe, two required. v4 batches 31 bounded producers without cross-client order
assumptions and observes two probes within 45 seconds. The v4 three-node run then reveals
non-idempotent gofail DELETE during survivor restoration after business progress
and joined crash. V5 clears only the three controls still active on survivors;
its final four-case matrix and matching negative pass their intended assertions. These are fixture/tooling
failures, not product RED or a business repair. No user-visible behavior or
configuration changes, and no Changelog entry, are required for this acceptance.

The v5 dependency-preparation attempts separately fail before E2E entry on a
proxy HTTP/2 Pebble download error. All three failed setup logs are retained.
Standalone HTTP/1 dependency download/precompile recovers the cache; executed
Go test/product parameters remain unchanged and go.mod/go.sum have no edits.
Dependency preparation time is separate from the case/package durations above.

## Related validation

- Ordinary Will E2E: both topologies PASS, package 50.780s.
- Existing cap-two reclamation E2E: both topologies PASS, package 130.339s.
- MQTT usecase race package PASS, 120.843s; infra/contracts compile with no test files.
- Initial app race run fails the unmodified Controller audit test at a partial
  first-transition snapshot. Isolated audit repeat PASS (1.830s), complete app
  race repeat PASS (4.759s). Cause remains unqualified; the failed run is retained.
- Named `flow-doc-contracts` PASS: 88 compliant, zero invalid; eleven existing
  advisory line-target warnings. The first check detects a stale generated index;
  regeneration and the required check pass.
- Source Standards and Spec reviews have zero open findings; final evidence recheck
  is recorded in [review](review.json).

## Standards

Zero open findings. Runtime/test source, four PASS artifacts, matched negative,
retained failures and source/binary hashes are consistent. Topology wording and
stale status metadata are corrected.

## Spec

Zero open findings. All ten raw archives match both compressed and original
hashes. The four positive cases and intended negative implement the agreed
bounded scenario; the resolved survivor-control fixture defect remains disclosed.

## Repeating the scenario

Use the Go 1.25.11/gofail v0.2.0 environment frozen in [provenance](provenance.json).
[Commands](commands.json) record the executed topology/negative/regression runs.
Build from a fresh temporary source directory:

```sh
GOFLAGS=-buildvcs=false scripts/build-gofail-binary.sh \
  --out /tmp/wukongim-will-fairness-gofail-v3 \
  --work-dir /tmp/wukongim-will-fairness-source-fresh --keep-work \
  --package internal/usecase/mqttsession --package internal/app \
  --package internal/infra/mqttwill --package pkg/slot/multiraft \
  --package pkg/slot/fsm --package pkg/slot/proxy

WK_E2E_GOFAIL_MQTT=1 WK_E2E_BINARY=/tmp/wukongim-will-fairness-gofail-v3 \
WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-will-fairness GOWORK=off \
go test -p 1 -tags=e2e ./test/e2e/mqtt/will_reclamation_fairness \
  -count=1 -timeout=65m -v
```

For the private negative, copy the generated tree, remove that one assignment
as in the zero-context patch, rebuild there with `GOFLAGS=-buildvcs=false`, and
run the unchanged single-node deadline case with the negative binary. Verify
the entire tree differs at only that generated adapter file before crediting it.

## Limits

This qualifies failed-read fairness and retained current recovery for cap 32,
one logical group, single-node/three-node clusters on Darwin/arm64. It does not
qualify default-cap stress, multi-group scheduling, healthy-read latency, general
partitions, missing/corrupt journals, issued-effect terminal recovery, liveness
at a full unreclaimable journal, shared-storage admission, sustained throughput,
Linux execution or complete MQTT acceptance. Earlier reports preserve the
narrower conclusions available at their exact revisions.

Review summary: Standards 0 open findings; Spec 0 open findings.
