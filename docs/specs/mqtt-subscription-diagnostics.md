# MQTT subscription closure diagnostics

Status: fixed entry observations verified; underlying failure diagnosis in progress. This change supplies
missing observations; it does not claim to fix the historical initial SUBSCRIBE
failure or post-resume EOF.

## Failure inventory (written before implementation)

- Every failed SUB/UNSUB packet requesting entry closure reports one fixed reason;
  a later filter failure still reports once and emits no partial ACK.
- Distinguish Owner capacity/fencing, cancellation/deadline, clock, conflict,
  invalid evidence, exhausted pending, unconfirmed intent, unknown dependency,
  malformed reply/write, unsupported entry, malformed packet and callback panic.
- Errors joined with unconfirmed intent retain their underlying diagnostic class;
  a diagnostic conflict is not proof of a definite non-write. Metrics never change
  retry policy or authorize a negative ACK.
- Definite per-filter denial/quota and successful requests produce no closure
  observation. Scope errors before and after the usecase are observed without
  changing scope lifetime, deadline, checks or cleanup ordering.
- Optional observer failure cannot replace a packet result or interrupt closure.
- The public counter pre-materializes only subscribe/unsubscribe and seventeen
  fixed reasons (34 series/node). Unknown labels are ignored; nil is safe. No
  ClientID, UID, topic, error text, payload, address or per-request label is kept.
- Real-process interrupted UNSUBSCRIBE must change the ingress node's deadline
  closure counter from zero to one, retain the existing recovery assertions, and
  record the observation in its repeatable JSON artifact.

## Signal

`wukongim_mqtt_subscription_closures_total{operation,reason}` counts entry close
requests from SUB/UNSUB processing, not unique connections or physical isolation.
An admitted operation canceled by fencing can report `canceled`; this metric
does not infer the cause of context cancellation. It does not cover closes originating in delivery, renewal or the gateway. Reasons
are `disabled`, `malformed`, `owner_limit`, `fenced`, `deadline`, `canceled`,
`clock`, `conflict`, `evidence`, `pending`, `unconfirmed`, `denied`, `quota`,
`callback`, `reply_evidence`, `reply_write`, and `unknown`.

## Hypotheses

1. Concurrent Session revision rejects subscription intent/activation: expect a
   conflict observation, then a targeted CAS probe to distinguish definite reject.
2. Projection/replay preparation escapes pending handling: expect pending,
   evidence or unknown; a bounded dependency probe must locate it before repair.
3. Owner scope closes or saturates: expect fenced/owner_limit/deadline/canceled;
   correlate with aggregate Owner work. A successful rerun is not a diagnosis.

## Current evidence

- Related access, metrics and app packages pass `-race` (2.105s / 3.679s /
  4.769s); the FLOW named check has no invalid files.
- The previous binary fails the process assertion because the metric is absent.
- The new binary reproduced initial three-node group SUBSCRIBE closure on the
  first bounded repetition, after 918.628ms and before any configured fault or
  publication. The ingress node reported exactly one `subscribe/unconfirmed`
  observation. The new metric identifies the entry path but the underlying
  dependency and cause still require a targeted probe.
- A separate error-only group/replay stage probe passed all ten repetitions
  (231.274s), including the exact injected closure counter and recovery identity.
  It captured no natural failure. Probe sources were removed immediately after
  build; passing repetitions do not prove a repair. The next diagnostic loop
  should isolate cold first-subscription admission to improve capture frequency.
- Exact binary, rule and log hashes are recorded in
  [the evidence report](../reports/mqtt-subscription-diagnostics.json).
