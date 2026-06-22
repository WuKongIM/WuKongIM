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
