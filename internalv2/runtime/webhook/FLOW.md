# internalv2/runtime/webhook Flow

`internalv2/runtime/webhook` owns node-local best-effort webhook delivery for
events produced by `internalv2/app` adapters. It wraps `pkg/workqueue` behind a
typed API so queue pressure, retry, JSON mapping, and endpoint delivery stay out
of access adapters and usecases.

It does not own message durability, subscriber scans, presence authority, plugin
hooks, channel append ordering, or crash replay.

## Event Flow

`msg.notify`
  -> app adapter receives a durable channelappend committed envelope
  -> webhook runtime admits a `Message` into a bounded notify batch pool
  -> worker sends one JSON array to `{HTTPAddr}?event=msg.notify`

`msg.offline`
  -> channelappend recipient processor classifies offline recipients
  -> batch observer passes bounded UID chunks to the app adapter
  -> webhook runtime admits `OfflineMessage` chunks into a sharded mailbox
  -> worker sends one JSON object to `{HTTPAddr}?event=msg.offline`

`user.onlinestatus`
  -> presence usecase observes successful route activation/deactivation
  -> app adapter formats the legacy status string
  -> webhook runtime admits it into a bounded online-status batch pool
  -> worker sends one JSON array to `{HTTPAddr}?event=user.onlinestatus`

## Performance Rules

Webhook admission is bounded and best-effort. Queue-full, closed, canceled, and
retry-exhausted events are observed and dropped. Webhook delivery never changes
SENDACK success, durable append success, conversation-active admission, or owner
delivery.

Large offline fanout must use recipient batches. Do not enqueue one webhook item
per UID for large groups.
