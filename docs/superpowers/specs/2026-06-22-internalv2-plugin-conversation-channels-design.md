# internalv2 Plugin Conversation Channels Design

## Scope

Migrate the legacy plugin host RPC `/conversation/channels` to `internalv2`.
This is a read-only compatibility surface that returns recent conversation
channel IDs for one UID.

Out of scope:

- `/plugin/httpForward`
- `/stream/open`, `/stream/write`, and `/stream/close`
- Last-message joins, unread counters, and conversation list pagination changes

## Compatibility Contract

- Decode `pluginproto.ConversationChannelReq`.
- Trim `uid` and reject an empty UID.
- Use the legacy default limit of `1000`.
- Preserve the authoritative reader order in the response.
- Return `pluginproto.ConversationChannelResp` with cloned channel entries.
- Propagate reader errors.

## Architecture

```text
plugin /conversation/channels host RPC
  -> internalv2/access/plugin.Server.handleConversationChannels
  -> internalv2/usecase/plugin.App.ConversationChannels
  -> ConversationReader.ConversationChannels(uid, limit=1000)
  -> internalv2/infra/cluster.PluginConversationReader
  -> clusterv2 ListConversationActivePage(kind=normal, uid, empty cursor, limit)
```

The plugin usecase owns validation and protocol mapping. It depends only on a
narrow `ConversationReader` port that returns `message.ChannelID` values.

The cluster infra adapter reads active conversation rows directly. It does not
call `conversation.App.List`, because that path joins last visible messages and
applies smaller list defaults that are not part of the legacy plugin RPC.

## Performance Notes

- One active-row page is read with the fixed legacy limit.
- No last-message reads are performed.
- Response allocation is linear in the returned channel count.
- The adapter rejects invalid channel type values rather than silently
  overflowing to `uint8`.

## Verification

- Usecase unit tests for validation, mapping, order preservation, and error
  propagation.
- Access tests for route registration, decode, timeout behavior, and response
  encoding.
- Infra/app tests for clusterv2 active-row wiring.
- Benchmarks for usecase mapping and host RPC handler overhead.
