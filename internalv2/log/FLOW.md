# internalv2/log Flow

## Responsibility

`internalv2/log` provides the zap/lumberjack-backed `wklog.Logger`
implementation used by the internalv2 composition root, plus a node-local
reader for the ordinary application log files produced by that logger.

It owns only ordinary application log construction, field conversion, file
rotation, bounded fixed-file reading, and sync behavior. Business packages
should continue to depend on `pkg/wklog` interfaces rather than importing this
package.

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

## Application Log Reader

```text
manager selected-node read
  -> internalv2/app adapter
  -> AppLogReader.Sources/Entries
  -> fixed files under WK_LOG_DIR:
     app.log, warn.log, error.log, debug.log
```

`AppLogReader` reads only those fixed ordinary application log files. It does
not accept arbitrary paths, does not expose local filesystem paths, and does not
read Controller, Slot, Channel, Raft, or other distributed logs. Initial tail
pages and forward reads are bounded by the configured scan budget, and returned
raw lines are capped independently from best-effort JSON or console parsing.
