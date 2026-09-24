# Inbox person-source preparation

## Failure inventory (before implementation)

- A durable inbox qualification must continue admitting new person sources while
  its Session is offline. A node-local connection/Owner is not required for this
  metadata work, and preparation grants no socket execution or publication right.
- Only a canonical person channel containing the qualification UID is accepted.
  Fresh UID qualification and coherent Session/subscription evidence are required;
  absence, unrelated rows, malformed or stale evidence never mean successful admission.
- Closed qualification, a durably ended/replaced Session, or a closed/replaced
  subscription suppresses new admission. Offline expiry time alone does not prove
  termination. Existing source debt remains discoverable for separate cleanup.
- The unknown source binding must commit before capturing its consumption boundary.
  Protection is reconfirmed after registration. A changed generation, regressed
  source, missing cursor behind an active binding or conflicting boundaries fails.
- Cursor initialization uses the current Session revision and full stored owner
  tuple through command 69. Concurrent takeover/renewal is fenced by that CAS;
  retries may follow a new owner within the same lifetime, never a new lifetime.
- Lost replies at unknown registration, boundary fixation, cursor initialization
  or activation retain recoverable work. A cursor reread must be at least as new
  as its exact initialization receipt. A fixed boundary must never advance on
  retry; multiple person sources have independent cursors under one inbox intent.
- Unsubscribe/termination during preparation must not revive admission or release
  old debt. Every effect checks current intent, and final success rechecks it.
- Context cancellation, dependency panic, invalid receipts and clock regression
  return no preparation receipt. Work is bounded by one call deadline and has no
  retry loop, worker, full directory scan or body allocation.

## Contract and remaining composition

`InboxSources.Prepare` prepares one qualification/source pair. Its result is
explicitly `Needed=false` only after authoritative closed intent, otherwise it
contains the protected source binding and initialized Session cursor. It neither
activates the inbox subscription nor proves shared replay recovery for SUBACK.

The caller must implement the [directory admission handshake](mqtt-inbox-directory.md):
commit UID membership before discovering qualifications and finish required source
preparations before the first persistent person append. Admission checkpoints must
fence the Channel incarnation so deletion/recreation cannot reuse an old success.
Initial discovery, durable
bounded future-source enumeration/checkpoints, source drain, offline accounting,
product wiring and process-level crash acceptance remain required. Closing an
intent does not remove retained source bindings or discharge earlier obligations.

## Validation scope

Failure tests use deterministic metadata storage and controlled protection ports.
The app integration starts a real single-node cluster with 256 hash slots, seeds
an explicit inbox qualification, disconnects its Session, prepares an absent
person source, sends through the native message usecase, confirms shared replay
and verifies one offline charged message. Qualification discovery and pre-append
ordering are controlled by that fixture; this is not full MQTT E2E acceptance.
