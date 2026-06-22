# internalv2 Plugin Receive Hook Design

## Goal

Migrate the legacy plugin `Receive` hook into `internalv2` so offline recipient
notifications are generated from the authoritative v2 delivery path without
blocking SENDACK, durable append, online delivery, or NoPersist realtime sends.

## Existing Behavior to Preserve

Legacy `Receive` is an offline-recipient hook, not a committed-message hook.
For each eligible offline UID, the node selects the highest-priority running
local plugin bound to that UID and invokes a PDK-compatible
`pluginproto.RecvPacket`.

Eligibility remains narrow:

- durable committed message only;
- recipient UID is offline after authoritative presence resolution;
- message is not request-scoped;
- message is not `SyncOnce`;
- message is not `NoPersist`;
- channel is not a temporary channel;
- sender UID is not empty;
- sender UID differs from the recipient UID;
- sender UID is not a configured system UID.

Hook failures are non-fatal. A failed invocation clears the receive dedupe entry
so a later retry can invoke the hook again.

## Architecture

`internalv2/runtime/channelappend` stays plugin-agnostic. It only reports
offline recipient candidates after recipient-authority presence resolution. The
app composition root adapts those candidates to `pluginevents.ReceiveOffline`
and hands them to the plugin hook worker. The plugin usecase owns all plugin
binding selection, dedupe, eligibility, and PDK payload mapping.

```text
channelappend RecipientProcessor
  -> resolve presence for recipient-authority batch
  -> online routes go to OwnerPusher as today
  -> UIDs with no routes become offline candidate notifications
  -> runtime/pluginhook bounded worker
  -> plugin.App.ReceiveOffline
  -> binding reader selects highest-priority local running Receive plugin
  -> InvokeReceive sync or async depending on ReplySync
```

This placement is intentional. `localOwnerPusher` only sees already-online
routes and cannot infer recipients with no route. App-level inference from
failed pushes would miss truly offline UIDs and would confuse stale route drops
with offline users.

## Runtime Contract

Add a narrow channelappend port:

```go
type OfflineRecipientObserver interface {
    ObserveOfflineRecipient(context.Context, OfflineRecipientEvent)
}
```

`OfflineRecipientEvent` carries one immutable `CommittedEnvelope` plus one UID.
The observer is called after `PresenceResolver.EndpointsByUIDs` succeeds and
before owner pushes are attempted. Missing presence routes generate candidates;
echo suppression continues to affect only online route delivery.

The runtime should skip observer calls for envelopes that cannot be Receive
candidates, especially `MessageSeq == 0` and `SyncOnce`. The plugin usecase
still repeats eligibility checks because runtime filters are only an admission
optimization.

## Plugin Usecase

Add:

- `MethodReceive`;
- `PathReceive = "/plugin/receive"`;
- `MsgTypeReceive = 3`;
- `ReceiveOffline(ctx, pluginevents.ReceiveOffline) error`;
- `ReceiveBindingReader` port for UID -> plugin binding lookup;
- TTL dedupe keyed by `messageID:uid`;
- `Observer.ObserveReceiveInvoke(result, duration)`.

Binding selection:

1. read bindings for the offline UID;
2. keep enabled bindings whose plugin number exists in the node-local runtime;
3. keep plugins that are enabled, running, and advertise `MethodReceive`;
4. sort by plugin priority descending and plugin number ascending;
5. invoke only the first candidate.

The usecase maps person-channel Receive packets from the recipient's view:
for a person channel, `RecvPacket.ChannelId` is the counterpart UID for the
offline recipient. Command-channel suffixes are stripped before invoking the
plugin.

## Worker and App Wiring

Extend `internalv2/runtime/pluginhook.Worker` to handle both PersistAfter and
Receive events. It should keep the current bounded queue, short admission wait,
timeout, panic recovery, and low-cardinality metrics behavior.

The app composition root wires Receive only when plugins are enabled and a
binding reader is available from the cluster/storage adapter. Channelappend gets
only an `OfflineRecipientObserver`; it does not import plugin usecase packages.

## Performance Notes

Receive is on the recipient-authority delivery worker path for potentially
large channels. The hot path must avoid subscriber-wide plugin scans and must
avoid blocking delivery on slow plugins. Candidate generation is one pass over
the already-normalized recipient UID slice plus the already-resolved online
routes. Plugin binding lookup and invocation happen after bounded worker
admission.

Benchmarks must cover:

- Receive eligibility and packet mapping;
- Receive bound-plugin selection at 1, 16, 128, and 1024 plugins/bindings;
- pluginhook Receive enqueue under parallel pressure and queue-full admission;
- recipient processor offline observer classification for mixed online/offline
  recipient batches.

## Non-Goals

- Implement plugin binding mutation APIs.
- Implement Receive replay after restart.
- Trigger Receive for online recipients, NoPersist realtime sends, or
  request-scoped temporary messages.
- Add cross-node fanout for Receive beyond the recipient-authority delivery
  path already selected by channelappend.
