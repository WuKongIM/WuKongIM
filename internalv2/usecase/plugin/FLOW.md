# internalv2/usecase/plugin Flow

## Responsibility

`internalv2/usecase/plugin` owns entry-agnostic v2 plugin lifecycle state,
candidate selection, and PDK-compatible hook payload mapping. It reuses the
existing PDK `pluginproto` wire format but does not own process launching,
Unix-socket transport, HTTP manager routes, or channel append side effects.

## Send Hook Flow

```text
message.Send / SendBatch after permission success
  -> App.BeforeSend
  -> select running, enabled plugins advertising MethodSend
  -> order by priority desc, plugin number asc
  -> for each plugin:
       map current SendCommand to pluginproto.SendPacket
       clone payload before crossing the plugin boundary
       call /plugin/send synchronously through Invoker.RequestPlugin
       fail-closed by default on invocation errors
       fail-open only when Options.FailOpen is true
       reject immediately on non-success plugin reason
       apply response payload only; preserve sender/channel/session/routing fields
       observe low-cardinality invoke result when Observer is configured
  -> return the mutated command or rejection reason to message usecase
```

`Send` hooks are synchronous and therefore on the SENDACK path. Operators
should keep Send plugin chains short and watch `method="send"` hook invoke
metrics when plugins are enabled.

## Host RPC Message Send Flow

```text
plugin /message/send host RPC
  -> access/plugin decodes pluginproto.SendReq and applies the host RPC timeout
  -> App.SendMessage
  -> map SendReq to message.SendCommand
       use DefaultSystemUID when fromUid is empty
       clone payload before entering the message usecase
       preserve NoPersist, SyncOnce, and RedDot header flags
       set NormalizePersonChannel for person-channel sends
       set Origin=SendOriginPlugin and leave SkipPluginHooks=false
  -> MessageSender.Send
  -> message usecase permissions, Send hook recursion guard, and channelappend
  -> return pluginproto.SendResp with the accepted messageId
```

Plugin-origin sends do not bypass the v2 message usecase. They use the same
permission, transient NoPersist, and channel authority routing semantics as
other trusted host-origin sends, with `SendOriginPlugin` only used to fence hook
recursion.

## Host RPC Channel Messages Flow

```text
plugin /channel/messages host RPC
  -> access/plugin decodes pluginproto.ChannelMessageBatchReq and applies timeout
  -> App.ChannelMessages
  -> for each request item:
       map to message.ChannelMessageQuery
       apply legacy limit default 100 and cap 10000
       use PullModeUp
       call MessageReader.SyncMessages
       map metadb.ErrNotFound to an empty item response
       clone each returned payload into pluginproto.Message
  -> return pluginproto.ChannelMessageBatchResp with item-aligned responses
```

`/channel/messages` is an authoritative committed-message read. It does not use
login UID person-channel normalization because the legacy plugin RPC carries the
explicit channel id and channel type to read.

## Host RPC Cluster Flow

```text
plugin /cluster/config host RPC
  -> access/plugin applies body limit and host RPC timeout
  -> App.ClusterConfig
  -> ClusterReader.ClusterSnapshot
  -> map nodes and physical Slots to pluginproto.ClusterConfig
       sort nodes by node id
       sort Slots by physical Slot id
       clone Slot replica lists
       do not infer API server addresses

plugin /cluster/channels/belongNode host RPC
  -> access/plugin decodes pluginproto.ClusterChannelBelongNodeReq and applies timeout
  -> App.ClusterChannelsBelongNode
  -> validate non-empty channel ids
  -> ChannelOwnerReader.ChannelOwnerNode for each request item
  -> reject zero or unknown owners instead of guessing local ownership
  -> group responses by owner node id, preserving request order within each group
```

Cluster host RPCs are read-only compatibility surfaces. They use authoritative
control/authority adapters supplied by the app composition root and do not
depend directly on clusterv2 from the plugin usecase.

## Host RPC Conversation Channels Flow

```text
plugin /conversation/channels host RPC
  -> access/plugin decodes pluginproto.ConversationChannelReq and applies timeout
  -> App.ConversationChannels
  -> trim and validate uid
  -> ConversationReader.ConversationChannels(limit=1000)
  -> map reader-defined channel order to pluginproto.ConversationChannelResp
```

`/conversation/channels` is an authoritative UID conversation-channel read. It
uses the legacy fixed limit of 1000 and intentionally does not join last visible
messages or reuse the user-facing conversation list defaults.

## Host RPC HTTP Forward Flow

```text
plugin /plugin/httpForward host RPC
  -> access/plugin decodes pluginproto.ForwardHttpReq and applies timeout
  -> App.HTTPForward
  -> trim pluginNo, falling back to the caller plugin number when empty
  -> clone the HTTP request and drop hop-by-hop headers plus Connection tokens
  -> enforce bounded request header/query and body sizes
  -> toNodeId <= 0:
       call local plugin /plugin/route through Invoker.RequestPlugin
  -> toNodeId > 0:
       call HTTPForwarder.ForwardPluginHTTP(target node, normalized request)
       validate and clone the returned response
  -> toNodeId == -1:
       return ErrHTTPForwardFanoutDeferred by explicit compatibility decision
```

`/plugin/httpForward` keeps the legacy host RPC surface while preserving v2
cluster routing. Remote forwarding is a narrow port owned by app/infra wiring;
the plugin usecase does not call clusterv2 directly and the remote receiver
must execute only the local `/plugin/route` hook. Fanout `toNodeId=-1` remains
an intentional deferred compatibility path; it must not scan the cluster
snapshot or issue partial remote RPCs.

## PersistAfter Flow

```text
channelappend durable commit success
  -> runtime/pluginhook bounded worker
  -> App.PersistAfterCommitted
  -> select running, enabled plugins advertising MethodPersistAfter
  -> map committed event to pluginproto.MessageBatch
  -> invoke each candidate as sync or async according to PersistAfterSync
```

PersistAfter is a post-commit side effect. Its failures are returned to the
worker for logging/metrics and do not change SENDACK, durable append, NoPersist
realtime dispatch, delivery, or conversation projection results.
