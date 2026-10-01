# Will reclamation fairness under failed authority reads

Source revision: `7583a33fb27706349ec77ea9113a70e95cf76204`.
The operator approved process acceptance for failed authority-read fairness,
including a private nonadvancing-cursor negative, relevant checks and local commit.
Reuse Paho MQTT 5, public Product/Manager HTTP, exact owned SIGKILL/restart and
loopback gofail on temporary product copies. No test reads journal/Slot files,
imports product internals, fabricates authority rows or changes command bytes.

## Failure inventory before edits

1. Early failed reads repeatedly spend the page budget and starve a later exact
   terminal candidate despite subsequent ordinary pressure turns.
2. Cursor selection advances only after successful reads or release; unconfirmed
   pages never rotate, or a nonadvancing private negative still passes.
3. Timeout/cancellation/absence is treated as retirement authority and deletes
   an unknown or current attempt, preventing later ordinary recovery.
4. Cleanup bypasses the 16-candidate page, 250ms read or 750ms page bound, adds
   inline reservation/publication retries, or extends the normal execution grant.
5. Pressure fills another node's journal, or the selected eligible record lies
   in the pinned page and its successful read is misreported as fairness.
6. Reclamation selection/probing deletes eligible records during calibration,
   changes the page contents or exposes identities through retained artifacts.
7. Crash resets effect counters, replays an ACK that was never joined, loses the
   persistent subscription, or hides a duplicate original business publication.
8. A fixture setup failure or missing control is presented as product RED, or a
   closed recipient is accepted as quiet. Clients/processes remain unjoined.

## First vertical slice

Start with one single-node-cluster deadline tracer, then extend to three nodes
and real canceled reads (four cases). Keep 256 hash Slots. Explicitly use one
logical Slot group so every admitted candidate shares the captured executor;
this is not multi-group scheduling, shared-storage or production-cap stress.

Temporarily cap the journal at 32 after cluster readiness. Hold one real Started
turn before dispatch and retain 31 independently published terminal attempts by
failing exact cleanup. All publishers use public CONNECT/Will/abnormal closure.
The existing active-key admission keeps the held turn from overlapping itself.
Validate and ACK every terminal receipt, with no CONNECT retry.

Submit another real Will and require actual refusal on that captured node.
Temporarily hold only cursor advancement during calibration. Actual bounded-page
probe counters select one of the already-published terminal ClientIDs absent
from that fixed page. No filename/order/attempt identity is exposed or computed
by the test. Real fresh reads and failed releases retain all calibration records.
Require two complete page observations per probe to exclude an in-flight sample.

Except for the chosen eligible key, pause reclamation's actual foreground read
until its existing child deadline, or cancel that exact child context before its
real Store.ReadMQTT call. Observe the returned real context error; do not return
synthetic rows or fake CAS results. Disable failed cleanup while keeping the
cursor held, then require a 21-second healthy quiet window and unchanged effects.
This is a reached starvation fixture: the only eligible terminal is outside the
fixed page and every candidate that would permit deletion remains unconfirmed.

Release the cursor hold and keep the read faults. Later ordinary pressure turns
must rotate to a fresh exact terminal read, complete its exact deletion, then
publish the pending Will once through normal claim/permission/admission. Require
actual page and read-error observations, bounded page size, cursor progress and
successful selected retirement after the business receipt. These observers do
not themselves substitute for publication evidence. Healthy quiet joins manual
ACKs before aborting the recipient and capturing the old-process effect prefix.
Kill/join that executor before restoring faults on surviving nodes. Replacement
startup has ordinary capacity and no calibration/read fault. The held current
Will must recover exactly once; leave it unacknowledged, then persistent reconnect
must replay the same message identity, PacketID and DUP without SUBSCRIBE.
ACK it and require strict 15-second healthy quiet. Counter prefixes stay distinct
from replacement-process counters. Emit body-free JSON after all joined cleanup.

## Sensitivity and delivery

In a private generated copy only, remove the actual `scanAfter = names[start]`
advance while retaining all reads, deadline limits and ordinary publication code.
The same delivered test must reach full refusal and calibration, then fail the
pending business receipt under continuing faults after releasing the control.
Never credit a setup or counter-only failure as this negative proof.

Freeze instruction/Flow/source/binary/fixture hashes, retain failed fixtures,
repeat commands and bounded receipts. Run related existing race/regression gates
and the named FLOW check when applicable. Review Standards and Spec before local
commit. Add a Changelog entry only if a user-visible business repair is required.

This qualifies bounded fairness for real canceled/deadline reads, not healthy
read latency, general partitions, lost/corrupt journals, full unreclaimable-cap
liveness, issued-effect terminal recovery, shared-storage admission, sustained
throughput, Linux execution or complete MQTT delivery.
