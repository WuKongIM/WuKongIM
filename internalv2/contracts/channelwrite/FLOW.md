# internalv2/contracts/channelwrite Flow

## Responsibility

`internalv2/contracts/channelwrite` owns the entry-independent contracts for
channel-authority writes. Gateway, HTTP, node RPC, message usecases, cluster
adapters, and the future channel write reactor share these DTOs without pulling
in concrete entry, app, or clusterv2 packages.

## Send Contract Flow

```text
entry adapter
  -> channelwrite.Submitter.Send / SendBatch
  -> channel authority router or local reactor
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
- `AuthorityTarget` identifies the fenced channel authority route used for
  write admission.
- Reason constants and append/routing error sentinels remain stable so entry
  adapters can map SENDACK outcomes without depending on runtime packages.
