# Will production capacity and delayed CAS acceptance

Source revision: `a8559a6ba`. The operator requested continuing with the full
1,024-record capacity and delayed CAS/reclamation acceptance. Reuse the approved
Paho MQTT 5, public HTTP provisioning, temporary loopback gofail and exact owned
process seams. No test reads journal/Slot files or imports product internals.

## Failure inventory before edits

1. The default 1,024-record cap is bypassed or exhausted records become unbounded.
2. Full pressure removes a current unissued reservation instead of terminal work.
3. Lost terminal cleanup prevents a later Will even when fresh terminal proof exists.
4. A delayed Started or successor CAS leaves a reservation ahead of the Slot row;
   older/current/unknown reads or elapsed grants delete that still-live evidence.
5. A late applied CAS or unknown reply permits publication without definite claim
   proof, or revives the removed original execution and duplicates business effects.
6. Reclamation pages starve behind early failed reads or become unbounded.
7. A test fills a different executor, counts only receipts instead of retained
   cleanup failures, resubscribes, retries CONNECT, or accepts a closed receiver.
8. Temporary pause/reply instrumentation changes ordinary runtime semantics.
9. Closing an owned client leaves Paho workers alive when the receipt is written.
10. Timeout diagnostics mask the receive failure or retain unbounded/public profile data.

## First tracer

Extend the existing pressure scenario using the unchanged default capacity:
keep one exact Started execution paused, then commit 1,023 distinct terminal
Wills through successive lifetimes of the same ClientID while exact cleanup fails.
A single joined receiver validates and ACKs every independent receipt, retaining
only bounded identity evidence. At most 32 lifetimes await a receipt; zero-delay
Wills detach on abnormal closure or the next Clean Start. Require 1,023 distinct
receipts and at least that many captured-executor terminal cleanup failures. Require no capacity refusal before the journal reaches
1,024 retained attempts. Disable failed cleanup, submit one more Will, and require
full refusal on that executor followed by one original publication after bounded
reclamation. Crash/restart that exact executor and recover the held original
without another SUBSCRIBE; healthy quiet excludes duplicates. No reduced cap is
enabled for this tracer. Emit bounded JSON after client/process cleanup.

## Delayed CAS slice

Use real applied Slot CAS results with delayed replies in temporary builds:
first Started, a sealed-attempt successor, and Published terminal completion.
The caller's five-second turn/ten-second grant expires while the delayed reply
is still joined. Reclamation must retain the current Started/successor reservation;
fresh terminal/newer-generation evidence can retire a different exact attempt.
Cap two, one ClientID and an actual captured-executor refusal isolate the race.
Successor restart may change Slot authority in the three-node cluster; identify
the executor from the actual successor cut rather than assuming old placement.

After fresh retirement evidence, delay one reclamation page beyond its 750ms
bound, then reject subsequent pages. Require a second page, retained capacity,
healthy recipient quiet and no new publication before restoring fresh cleanup.
This distinguishes an expired page from a new timely authority decision.
Record actual reception time. A 60s completion window includes a fresh claim,
native append and background projection; it does not extend the 750ms page or
10s execution grant. This is eventual completion acceptance, not a latency SLO.
All three cases require three distinct original business publications and 15s
healthy quiet, without CONNECT retry or SUBSCRIBE on persistent reconnect.

These are applied-CAS reply/canceled-page races. They do not model a delayed
Raft apply that has not committed, network partitions, corrupt/lost journals or
complete issued-effect terminal recovery. A separate negative control will
check that the canceled-page assertion can fail when cleanup ignores cancellation.
Failed authority-read starvation from inventory item 6 remains outside this
process slice; the existing bounded-page contracts are not a new E2E proof.
