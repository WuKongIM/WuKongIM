# Channelwrite Observability Design

## Goal

Add low-cardinality metrics for the two new asynchronous boundaries created by
the channelwrite split: conversation active cache/flush and recipient delivery
worker admission/processing.

## Scope

The change observes runtime pressure and latency only. It does not add new
configuration, change SEND/SENDACK semantics, retry failed best-effort effects,
or add UID/channel/slot labels.

## Architecture

Runtime packages emit observer events with stable result labels and numeric
shape data. `internalv2/app` maps those events to `pkg/metrics`, keeping
Prometheus concerns out of runtime packages.

`internalv2/runtime/conversationactive.Manager` reports active cache pressure:
current cached rows, dirty rows, oldest dirty row age, and flush attempts.
Flush observations include selected and flushed rows plus duration.

`internalv2/runtime/channelwrite.RecipientDeliveryWorker` reports delivery
worker pressure: queue depth/capacity, admission attempts and wait time, and
process duration/batch recipient counts. Terminal processing failures continue
to use `PostCommitFailureObservation`; this avoids duplicate error counters.

## Metric Labels

All labels stay low-cardinality:

- conversation active flush: `result`
- conversation active rows: gauge-only, no UID/channel labels
- recipient delivery worker: `result`

Allowed recipient delivery admission results are `accepted`, `closed`,
`canceled`, and `timeout`. Allowed process results are `ok`, `error`, and
`panic`.

## Testing

Tests cover the runtime observer events first, then the app metrics mapping,
then the Prometheus registry output. Verification runs targeted package tests
for `conversationactive`, `channelwrite`, `app`, and `pkg/metrics`.
