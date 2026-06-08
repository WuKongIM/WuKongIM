# internalv2/usecase/recipient Flow

This package owns recipient-authority post-commit dispatch and local processing. It is entry-agnostic: callers may submit committed events for bounded paging and authority resolution through dispatcher ports, or provide a fenced recipient authority target with the recipient UIDs that belong to that target.

```text
SubmitCommitted(event)
  -> if MessageScopedUIDs: use them directly
  -> else page RecipientSource with bounded page size
  -> resolve each recipient UID to authority.Target
  -> group by exact target and chunk by TargetBatchSize
  -> local Processor for local targets; Remote for remote targets

Process(target,event,recipients)
  -> validate target is local and current recipient UID authority
  -> filter non-empty recipient UIDs; stop when none remain
  -> build recipient-scoped conversation active patches
  -> require a conversation updater before configured delivery
  -> AdmitPatches before delivery
  -> clone event and replace MessageScopedUIDs with this recipient group
  -> SubmitDelivery
```

Responsibilities:

- Reject work when the target is not local to this node.
- Reject work when the target cannot be validated as the current authority fence.
- Treat an empty effective recipient group as a no-op.
- Page durable channel recipients progressively for unscoped committed events.
- Route recipients by exact fenced authority target before processing.
- Update recent conversation state before any delivery submission.
- Scope delivery to the non-empty recipient UIDs from the request.

Non-responsibilities:

- It does not load all subscribers into memory.
- It does not perform retry or backoff.
- It does not implement node RPC transport.
- It does not mutate gateway sessions.
