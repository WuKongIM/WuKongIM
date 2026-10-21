# Grant diagnosis before implementation

Source: ed705c0fbffdea485bbb65608d6e340bb049d97e. No implementation change is authorized yet. Parent has shown four falsifiable hypotheses before this test: expiry removes usable senders; activity/canary state mismatch; schedule lateness; product/transport backpressure.

Failure modes the focused probe must distinguish:

1. The real worker constructor / committed YAML / worker ID 2 reaches ApplyGrant underdelivery with a frozen, on-time clock; this rejects timer jitter as a necessary condition in the probe.
2. Natural session TTL vs a single changed long-session control changes the verdict; this isolates whether expiry is necessary in the probe, without asserting historical causality.
3. Existing mandatory bootstrap activity outlives the fixed five-minute eligibility window; preserve queue and first/last due-deadline summaries before the failed ApplyGrant. Error value 1 is not sufficient by itself to identify this branch.
4. Person routing exhausts all candidates; preserve active-channel count and aggregate eligibility, never raw identities.
5. Due canary remains unavailable for five minutes; preserve canary-unavailable age, count, and group ownership, not channel/member IDs.
6. A fake transport that emits only SENDACK lacks real RECV coverage. Any verification/capacity failure caused by this limitation must be called fixture failure, not reproduction; do not relax production correctness gates to pass.
7. Constructor/bootstrap/queue capacity errors must remain distinct from the observed ApplyGrant RuntimeFailureUnderDelivery. Use the real worker factory capacities.
8. Clock advancement must not leap over subsecond dispatch deadlines. Use the real 10ms cadence, join fake ACK completions before moving the clock, and preserve the exact configured global rate/allocator.

The probe is a diagnostic fixture, not an E2E qualification. It uses no sockets, product processes, cloud APIs or wall-clock sleep. Existing frozen live evidence remains untouched. Tests are written before any implementation repair.
