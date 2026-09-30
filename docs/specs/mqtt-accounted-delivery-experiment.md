# MQTT delivery of an already-accounted prefix

The full-size terminal-boundary probe at `dd8e49bb9` reaches its unchanged
three-minute receipt deadline with all 500 clients open and only 13–19 receipts
per client. A separate 2,000-member profile keeps the same 500 clients/twenty
messages. One goroutine sample finds ten of sixteen delivery workers in
Accounting read barriers, four in WindowAdmission read barriers and two in
proposal futures; the delivery scheduler waits for queue space. The twenty-second
public metric interval records 70,756 successful Slot barriers with 413.421s
aggregate duration. These are measurements, not proof that every wait is wasted.

## Failure inventory before implementation

- Every enqueue rescans Accounting even while its recently confirmed prefix is
  still unconsumed. Verify a real multi-message cursor/window pipeline sends
  original ordered identities with fewer Accounting cursor reads.
- A hint suppresses new debt indefinitely: retain only one exact source key,
  its confirmed through position and original turn-start time. At ten seconds,
  a backwards clock, a different key or an exhausted prefix, run Accounting.
  Never extend the time after a skipped scan or a slow call.
- A stale hint grants send authority: source discovery must freshly observe
  the same accounted through position with window through below it. Keep all
  WindowAdmission, original content, Owner and final receive checks unchanged.
  Corrupt/foreign cursor, changed options, cancellation, fencing and revocation
  must not enqueue under cached evidence.
- A full receive window masks quota: if the skipped scan's actual sender turn
  reports Busy, run Accounting before returning. Keep quota ending and offline
  ConsumerMaintenance independent of connection delivery.
- Accounting/sending errors retain a stale hint: clear it on every failed turn
  and before every Accounting attempt. Unknown/lost writes cannot mint a hint.
- Skipped Accounting incorrectly arms a quiet hint: a skipped turn cannot prove
  source idleness, even if the sender subsequently reports Idle. A bounded next
  turn must resume Accounting before complete-idle refresh can begin.

This retains O(1) body-free scheduling state per connection and no new queue,
worker, configuration field, durable record, ownership proof or retry. A hint
only defers a redundant scan for at most ten seconds; it never admits content.
Original full scale acceptance must run again after relevant correctness checks.

## Decision: not adopted

The candidate passes its thirteen isolated safety cases, but the ordinary
500-client/twenty-message workload still times out with 406 incomplete clients.
Paired sequential 64-client runs take 29.467s on the baseline and 30.471s on the
candidate for initial fanout; neither establishes throughput benefit. The
baseline reaches delivery/churn/retirement but fails its unchanged idle bound
(10.24 barriers/client/s), and the candidate fails at one unsubscribe/conflict.
No complete passing comparison exists. The hint implementation and its test
prototype were moved outside the repository; product source, FLOW, Changelog
and project knowledge retain their original behavior. No performance repair
or quota-deferral policy is adopted from this experiment.
