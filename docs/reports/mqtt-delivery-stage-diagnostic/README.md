# MQTT stage diagnostic artifacts

These are failed diagnostic scenarios and a measurement recipe, not adopted
product code or passing capacity acceptance. Source revision:
`5dfa36d8871c0c606246f610c4c80339f1360bec`; product behavior still matches
`dd8e49bb9ffb284666791d8d937f09e2ced7af13`.
See [result and next regression boundary](../../specs/mqtt-delivery-read-budget.md)
and [exact provenance](provenance.json).

`*-failure.json` and `*-receipts.json` retain the scenario's original verdict.
`*-aggregates.jsonl.gz` contains fixed-label cumulative JSON snapshots, with
histogram upper bounds recorded in provenance. `*-steady-summary.json` contains
the actual component timestamps, count/time/result deltas. `*-overlay.patch.gz`
records temporary instrumented source relative to the base; both v1 probes use
`full-v1-overlay.patch.gz`. Do not apply diagnostic patches to product source.
The first two v1 probes lose read attribution and must not support that claim.

Copy this directory outside the repository before checking out the exact base
revision in a separate `.worktrees/` checkout. The v3 generator takes that
checkout and writes copied source plus a Go overlay outside it:

```sh
python3 /tmp/mqtt-stage-recipe/make_overlay.py --worktree "$MQTT_STAGE_SOURCE" --out /tmp/mqtt-stage-v3-5dfa-overlay
```

In that exact checkout, build the instrumented binary and finish validation
before starting the measured workload:

```sh
GOWORK=off go build -overlay=/tmp/mqtt-stage-v3-5dfa-overlay/overlay.json -o /tmp/wukongim-mqtt-stage-v3-5dfa ./cmd/wukongim
GOWORK=off go test -race -overlay=/tmp/mqtt-stage-v3-5dfa-overlay/overlay.json ./internal/usecase/mqttsession -run 'TestDeliveryCoordinatorDiscoversAccountsAndSendsWithoutCallerKeys|TestDeliveryCoordinatorRecoversBeforeDiscoveringNewSources|TestDeliveryIdleWakeDuringPassCannotInstallQuietHint' -count=1 -timeout=2m
mkdir -p /tmp/mqtt-stage-v3-2000-5dfa
WK_E2E_BINARY=/tmp/wukongim-mqtt-stage-v3-5dfa WK_E2E_MQTT_SCALE_MEMBERS=2000 WK_E2E_MQTT_REPORT_DIR=/tmp/mqtt-stage-v3-2000-5dfa GOWORK=off go test -overlay=/tmp/mqtt-stage-v3-5dfa-overlay/overlay.json -p 1 -tags=e2e ./test/e2e/mqtt/scale -run '^TestGroupScaleDeliveryAndChurnRetirement$' -count=1 -timeout=8m -v > /tmp/mqtt-stage-v3-2000-5dfa.log 2>&1
python3 /tmp/mqtt-stage-recipe/analyze.py /tmp/mqtt-stage-v3-2000-5dfa
```

The scenario may fail; retain that verdict. It defers copying only the tagged,
fixed aggregate stderr records before process cleanup. No table reads, user
content, credentials or client identity labels enter these aggregate artifacts.
The recipe never changes product files. An optional live analyzer can read the
exact task-owned process's aggregate stderr; completed analysis uses the report.

Stage `window` includes its nested `window_auth`; `account` includes
`account_auth`; `final_enqueue` includes `final_auth`. Node method clocks include
their inner metadata/barrier waits. The scheduler labels reuse fixed numerical
stage indices; use the explicit scheduler legend in provenance rather than the
same strings' usecase meanings. The enqueue denominator records accepted gateway
enqueues, not independent public receipt completion.

The generator preserves only its diagnostic context marker when Owner admission
derives a dependency context. It does not copy arbitrary entry values or replace
Owner cancellation, synchronous parent checks, or the existing deadline.
