# internalv2/usecase/channel Flow

## Responsibility

`internalv2/usecase/channel` coordinates legacy-compatible channel management
without depending on HTTP, gateway frames, clusterv2, or concrete storage. It
owns channel metadata mutations, ordinary subscriber mutations, temporary
subscriber lists, allowlists, denylists, subscriber mutation versioning, and
bounded subscriber page/chunk iteration.

## Store Port

```text
App command
  -> Store.GetChannel / UpsertChannel / DeleteChannel
  -> Store.AddChannelSubscribers / RemoveChannelSubscribers / ListChannelSubscribers
```

The store is expected to represent cluster-authoritative Slot metadata. The
usecase does not add single-node-only branches; single-node deployment is still
handled by the store implementation as a single-node cluster.

## Membership Projection Port

```text
ordinary Upsert/AddSubscribers/RemoveSubscribers/RemoveAllSubscribers
  -> Store subscriber mutation
  -> MembershipIndex.UpsertChannelMemberships / DeleteChannelMemberships
```

The projection is only for ordinary channel subscribers. Allowlist, denylist,
and temporary member-list mutations keep using their internal channel IDs and do
not create user-channel membership rows. Reset operations first remove the
current ordinary subscriber snapshot from the membership index, then project the
replacement subscribers using the same logical subscriber mutation version.
After ordinary subscriber mutations complete, the usecase reads the stored
subscriber count and refreshes the channel large-group flag when the count is
greater than the configured threshold.

## Subscriber Mutation Observer Port

```text
ordinary Upsert/AddSubscribers/RemoveSubscribers/RemoveAllSubscribers
  -> Store subscriber mutation and large-group refresh
  -> SubscriberMutationObserver.ObserveSubscriberMutation(final channel metadata)
```

The observer is notified only after the durable mutation, membership projection,
and large-group flag refresh succeed. The event carries the final
`SubscriberMutationVersion`, final `Large` flag, reset/add/remove shape, and
cloned UID lists so the composition root can keep runtime channel-state caches
aligned without letting the HTTP adapter or this usecase depend on
`runtime/channelappend`. Allowlist, denylist, and temporary member-list
mutations do not emit observer events.

## Member Lists

Allowlist, denylist, and temporary subscribers are represented as subscriber
rows on stable internal member-list channel IDs derived by
`internalv2/contracts/channelmembers`. This preserves compatibility with the
legacy metadata layout while keeping the HTTP adapter thin.

## Subscriber Mutation Versioning

For ordinary subscribers, each logical mutation reads the current channel
`SubscriberMutationVersion` and forwards one next version across all chunks in
that logical operation. Reset operations use the same version for removal of the
old snapshot and addition of the replacement snapshot.
