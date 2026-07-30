---
status: accepted
---

# Converge Online Delivery behind one runtime seam

WuKongIM will treat Online Delivery as one deep runtime module. Its seam begins
after channel append has produced a bounded Recipient Delivery Plan grouped by
exact recipient-authority target. The module owns presence resolution, offline
classification, owner-node grouping, local and remote owner push, bounded
retry, exact-session writes, and the complete pending-ACK lifecycle. Channel
append retains subscriber discovery, recipient-authority grouping, conversation
projection, and its pre-append post-commit capacity reservation.

The module presents narrow typed interfaces to its distinct callers: plan
admission for channel append, owner-local push for node RPC, recipient feedback
for the gateway, and lifecycle for app composition. A local-session write
adapter performs exact-session validation, packet construction, and the
physical write, but it never sees ACK tokens. Plans transfer shared immutable
payload and recipient storage on successful admission; production adapters copy
only when serialization or independent ownership requires it. Plans explicitly
distinguish Durable and Transient delivery.

Online Delivery remains bounded best-effort behavior. A successful plan
admission does not promise eventual session delivery, terminal processing
failures do not flow back to channel append, and this decision does not add
durable committed-event replay. Stop quiesces admission, cancels and accounts
for accepted work, clears transient ACK state, and permits restart only after
the previous lifecycle has completely exited.

We rejected keeping committed fanout and ACK handling as separate modules
because it preserves duplicate delivery paths and leaks ACK transaction details
across their seam. We also rejected a single tagged `Apply` operation because
asynchronous admission, synchronous owner push, and recipient feedback have
different invariants and result shapes; caller-specific typed interfaces provide
better leverage and locality. The migration must replace the old paths rather
than layer over them, while preserving the owner-push RPC service identifier and
wire encoding for mixed-version clusters.
