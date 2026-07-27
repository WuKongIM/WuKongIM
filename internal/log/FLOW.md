# internal/log Flow

## Responsibility

`internal/log` provides the zap/lumberjack-backed `wklog.Logger`
implementation used by the internal composition root, plus a node-local
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
       -> ANSI level colors only when stdout is an interactive terminal and
          neither NO_COLOR nor TERM=dumb disables color
       -> app-selected lifecycle presentation events may be excluded from
          stdout while remaining unchanged in their rolling files
```

`internal/app` is responsible for creating the root logger from `Config.Log`,
passing named children to composed runtimes, and syncing the logger during
shutdown. The app uses console-only event exclusion for startup lifecycle
records because it renders those same records as a bounded human-facing startup
summary; exclusion never changes file routing or structured fields.

## Application Log Reader

```text
manager selected-node read
  -> internal/app adapter
  -> AppLogReader.Sources/Entries
  -> fixed files under WK_LOG_DIR:
     app.log, warn.log, error.log, debug.log
```

`AppLogReader` reads only those fixed ordinary application log files. It does
not accept arbitrary paths, does not expose local filesystem paths, and does not
read Controller, Slot, Channel, Raft, or other distributed logs. Initial tail
pages and forward reads are bounded by the configured scan budget, and returned
raw lines are capped independently from best-effort JSON or console parsing.
Context reads use the same opaque cursor and a bounded backward tail scan to
return exact `before` plus `after` raw lines from the selected file. They do not
search arbitrary directories or synthesize neighboring entries on another
node.
