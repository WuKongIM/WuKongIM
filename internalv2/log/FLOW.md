# internalv2/log Flow

## Responsibility

`internalv2/log` provides the zap/lumberjack-backed `wklog.Logger`
implementation used by the internalv2 composition root.

It owns only application log construction, field conversion, file rotation, and
sync behavior. Business packages should continue to depend on `pkg/wklog`
interfaces rather than importing this package.

## Log Files

```text
Config
  -> NewLogger
  -> app.log for info and above
  -> warn.log for warn only
  -> error.log for error and above
  -> debug.log when Level enables debug
  -> optional console sink
```

`internalv2/app` is responsible for creating the root logger from `Config.Log`,
passing named children to composed runtimes, and syncing the logger during
shutdown.
