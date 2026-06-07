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
