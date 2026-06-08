# internalv2/usecase/recipient Flow

This package owns local recipient-authority post-commit processing. It is entry-agnostic: callers provide a fenced recipient authority target, one committed message event, and the recipient UIDs that belong to that target.

```text
Process(target,event,recipients)
  -> validate target is local recipient UID authority
  -> filter non-empty recipient UIDs; stop when none remain
  -> build recipient-scoped conversation active patches
  -> require a conversation updater before configured delivery
  -> AdmitPatches before delivery
  -> clone event and replace MessageScopedUIDs with this recipient group
  -> SubmitDelivery
```

Responsibilities:

- Reject work when the target is not local to this node.
- Treat an empty effective recipient group as a no-op.
- Update recent conversation state before any delivery submission.
- Scope delivery to the non-empty recipient UIDs from the request.

Non-responsibilities:

- It does not page subscribers.
- It does not resolve authority routes.
- It does not send RPC.
- It does not mutate gateway sessions.
