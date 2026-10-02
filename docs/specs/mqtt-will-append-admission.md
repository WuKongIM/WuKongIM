# Will admission before append submission

Source revision: `68eb337c50267fb2e0114130ef2b028760d17555`.
The operator approved recovery after Admitted and before actual SEND. Reuse the
approved process seams: independent Paho MQTT 5, public HTTP provisioning,
loopback temporary gofail controls and exact harness-owned process crashes.
Tests read no journal or Slot files. Applicable instruction/navigation digests
are frozen in `docs/reports/mqtt-will-append-admission/source-context.json`.

## Failure inventory before implementation

1. Crash after durable Admitted and before SEND leaves an unpublished Will pending
   forever despite a provable absence of any submitted append request.
2. Sealing wins but an old callback later submits locally or forwards remotely.
3. A remote append accepted before source-node death survives cancellation;
   elapsed leases or a missing receipt incorrectly authorize redispatch.
4. A version-1 Admitted journal from the earlier binary is reinterpreted as safe
   even though it could already have submitted to a remote authority.
5. A failed/late journal sync, wrong tuple, missing/corrupt record or old boot
   grants submission or negative proof. A removed tuple is reused.
6. Routing retries, item cloning or node RPC encoding drop a required admission
   guard, reorder results or accidentally serialize a process-local callback.
7. Recovery reruns transformations, skips current policy or dispatches before a
   definite successor Slot CAS; committed recovery loses the original identity.
8. A guard adds a worker, waiting queue, retained body map or unbounded fsyncs;
   Stop/restore releases generation ownership before its transitions join.

## Decision and proof limits

Separate durable admission of the Will turn from irreversible permission to
submit an append. A trusted process-local item callback must durably mark the
exact attempt immediately before either local SubmitLocal or remote forwarding.
It competes with sealing under the existing generation-owned journal mutex.
Once submission permission is issued, retain positive-only recovery even if the
caller crashes before the actual call. Retries within that same call reuse the
exact issued permission; they cannot acquire a new execution identity.

New version-2 records distinguish pre-submission Admitted from AppendIssued.
Version-1 Admitted stays unknown; its bytes must never be upgraded into a safe
pre-submission record. Version-1 Reserved/Sealed retain their original meaning.
Foreign-boot sealing still requires the exact owning node's retirement fact.
A definite durable seal, current permission, a reserved successor and its definite
Slot CAS are all required before the new frozen publication. Positive committed
content remains the first recovery choice. No Channel absence or lease proof.

## Process acceptance

Extend the existing Started recovery scenario in both 256-Slot topologies. Cut
and crash once after Admitted and once at the actual authority router before
append admission. After restart, the persistent recipient resumes without a new
SUBSCRIBE and receives exactly one frozen original Will, then ACKs and observes
15 seconds of healthy quiet. Keep the existing committed receipt/PacketID replay
and accepted append delayed beyond two grants as mandatory regression cases.

The first RED tracer is the single-node pre-send case with a 35-second delivery
bound, covering the unchanged ten-second executor grant and scanner/recovery.
Run against the exact source plus only inert cut comments, then against the repair.
Also crash after append permission is issued and require 21 seconds of healthy
quiet after restart with zero new publication attempts. For compatibility, start
the frozen version-1 binary, cut after its Admitted, then restart only that exact
executor on the candidate; the same healthy quiet must retain the unknown Will.
Bounded JSON artifacts run after process/client cleanup. Full 1,024-cap stress,
delayed CAS/cleanup races, partitions and all terminal unknown-effect recovery
remain separate acceptance work.
