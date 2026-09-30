# MQTT runtime reactivation after restore

## Approved boundary

The operator approved product-process acceptance through authenticated Manager
backup/restore HTTP and independent Paho MQTT 5 clients. Product HTTP provisions
credentials and publishes/checks native messages. No MQTT tables are inspected.

## Failure inventory before implementation

1. Restore finishes but MQTT still references a terminal Owner registry, requiring
   process restart or incorrectly reviving old admission.
2. A connection admitted before maintenance reaches a new runtime through a late
   open/packet/close callback, changing the restored Session or consuming credit.
3. In-flight work, unknown effects or failed physical closure are abandoned to
   manufacture an old-boot retirement proof. Only joined terminal owners and
   producers authorize reconstruction; incomplete stop retains dependencies.
4. A new entry is published before every new worker starts or before cluster
   restore readiness. A partial construction/start failure reopens admission,
   leaks a retirement lock or prevents a bounded retry.
5. Exact-owner RPC retains the old registry after reconstruction, or a new boot
   uses missing/foreign/corrupt receipts to authorize takeover of restored rows.
6. Old delivery interests, scan cursors, queues, connections or Will leases are
   reused across the restore boundary. Reconstruction uses a new random BootID
   and newly constructed bounded runtime components.
7. Restored persistent Sessions lose subscriptions or begun QoS 1 exchanges.
   Replay must preserve original Packet Identifier, message identity/body/order
   and DUP, then accept future messages without another SUBSCRIBE.
8. Post-backup Sessions/messages survive restoration, or a second restore reuses
   the first successor's owners. Repeating the same archive creates another fresh
   runtime and restores the same point-in-time public state.
9. Startup during maintenance or concurrent shutdown races reconstruction and
   revives a stopped application. Lifecycle transitions must remain serialized.
10. One node fences Slot metadata while another drains its MQTT connections.
    A durable disconnect must not be required to prove local runtime retirement
    during restore; exact local transport/operation proof is still mandatory.

## Acceptance plan

Use real 256-hash-Slot single-node and three-node clusters with MQTT token
authentication, shared repositories and authenticated Manager backup permissions.
Use an existing native group history to isolate restore from cold first-message
subscription admission. Keep a QoS 1 group publication unacknowledged at backup time with Receive Maximum 1. After backup, add another
message and persistent Session. Restore twice without restarting the process.
Each cycle proves maintenance refusal, closure of the previous connection,
automatic admission after restore, Session Present without resubscription,
identical replay with DUP and manual ACK, removal of post-backup state, and one
fresh delivery without extras. Emit a bounded JSON report containing assertions
and counts, with no credentials or payloads.

Before probing a multi-node repository, every node must publicly expose the
configured plan revision; before restore, peers must also expose at least the
previous completed backup/restore revision with no active job. Controller mirror
propagation is not implied by Slot readiness. Plan/restore mutations are never retried after ambiguous outcomes. After
Controller completion, await every node’s public `/readyz` admission contract
before one MQTT reconnect; local maintenance mirrors propagate asynchronously.
An immediate second-cycle CONNECT returned EOF in a diagnostic run and a later
diagnostic CONNECT succeeded; that original failure remains recorded. It does
not establish the cause of every post-restore CONNACK refusal.

Restore convergence is bounded to four minutes per phase in the single-node
case and eight minutes in the three-node case (768 replica-partition operations
per pass), with a twenty-minute scenario context. The earlier four-minute
three-node budget expired while staging/verification was progressing. Foreground
CONNECT, SUBSCRIBE and receive budgets are unchanged; this is functional restore
acceptance rather than a restore-throughput gate.

Each boundary races 16 bounded CONNECT attempts. Accepted earlier-generation
connections must close; a separate connection attempt during maintenance must
fail. The three-node case initially owns the Session on node 3, then resumes
through nodes 1 and 2, exercising remote exact-owner retirement RPC.

## Runtime contract

`mqttProduct` remains the one registered Gateway handler and Owner RPC port.
Its lifecycle mutex serializes startup, restore suspend/resume and terminal Stop;
callbacks and commit hints use one atomic pointer to the published generation.
Each generation owns a unique random BootID, fresh terminal Owners, Connections,
Deliveries, all seven bounded worker runtimes and its retirement adapter.

Suspend fences new CONNECT work and Owner admission, cancels/joins CONNECT
acquisition and its Gateway handoff, then joins workers/Owners and durably records
the old boot before releasing its directory lock. Acquisition before Reserve can
perform exact-owner isolation, so it participates in the join too. Failure never
authorizes reconstruction or storage replacement. Resume first retries joins,
constructs a fresh generation and starts every worker before atomic publication.
Partial construction/start stays owned for bounded cleanup on a later retry.

Restore also switches the generation's connection-control adapter to exact local
Owner quiescence before joining. No additional Session/Will disconnect mutation
is issued during that join: another replica may already fence metadata admission.
An already executing call still joins, and failed physical close or unknown effects
still prevent retirement. This is runtime teardown, not a local business path;
all ordinary lifecycle mutations continue through Slot authority. Durable active
rows in the archive are reconciled by the existing takeover/deadline use cases
after exact old-boot isolation. Restore rollback uses the same rule. Ordinary
terminal shutdown retains the durable disconnect path.

Accepted connections retain their original handler and a once-only handoff
completion; open, rollback and close do not resolve the new generation. The
acquisition context stays alive through reply admission and open, then is released.
Owner execution fencing remains authoritative for later packets/delivery. Replay
wakes stay with their generation; ordinary commit hints resolve the current one.

The existing fenced node-resume RPC invokes reconstruction before Controller
maintenance clears. Restore rollback follows the same fresh-runtime lifecycle.
Gateway admission stays closed on local reconstruction failure. Terminal product
Stop forbids future resume, and an admission mutex orders late observer reopening
against shutdown. No table, wire encoding, RPC ID or configuration field changes.

## Bounds and proof limits

There is at most one published and one partially constructed generation. Workers
retain existing fixed cohorts, page budgets, queues and capacity; old cohorts join
before new ones start. The CONNECT gate tracks bounded Gateway auth/handoff work
with a shared count/completion channel, cancellation context and one constant-size
receipt per connection; it introduces no per-Session polling goroutine. Packet
dispatch uses the pinned handler; wake/RPC dispatch adds one atomic load. Durable
retirement files grow by one bounded fact per node generation, never per client.

Exact old-boot receipts remain node-local facts; no unavailable-node or partition
isolation is inferred. Uncertain-effect redispatch is outside this change.
