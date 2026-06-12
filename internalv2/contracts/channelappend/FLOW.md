# internalv2/contracts/channelappend Flow

## Responsibility

`internalv2/contracts/channelappend` owns the entry-independent contracts for
channel-authority writes. Gateway, HTTP, node RPC, message usecases, cluster
adapters, and the channel append authority runtime share these DTOs without pulling
in concrete entry, app, or clusterv2 packages.

## Send Contract Flow

```text
entry adapter
  -> channelappend.Submitter.Send / SendBatch
  -> channel authority router or local append runtime
  -> append contract
  -> committed envelope
  -> recipient batch and owner push contracts
```

The package only defines data, sentinel errors, and clone helpers. It does not
resolve routes, append messages, dispatch recipients, or push gateway frames.

## Ownership Rules

- `SendCommand`, `Message`, `AppendBatchRequest`, `CommittedEnvelope`,
  `RecipientBatch`, and push/result DTOs provide clone helpers when they own
  slices.
- SEND hot-path implementations may pass payload and message-scoped UID slices
  by reference. Callers and callees must treat those slices as immutable until a
  concrete ownership boundary, such as durable storage, takes its own copy.
- Post-commit delivery treats `CommittedEnvelope` payloads as immutable after
  the writer creates the async backlog copy; recipient queues and owner pushes
  pass that envelope by reference until a concrete push adapter serializes or
  copies it.
- `AuthorityTarget` identifies the fenced channel authority route used for
  write admission.
- `AppendBatchRequest` carries the expected authority epoch and leader epoch so
  append adapters can reject stale writes without re-resolving caller intent.
- Reason constants and append/routing error sentinels remain stable so entry
  adapters can map SENDACK outcomes without depending on runtime packages.
