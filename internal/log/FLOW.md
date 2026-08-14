---
scope: package
summary: Provides internal application log construction, rotation, console filtering, synchronization, and bounded reads.
---

# Application Logging Flow

## Responsibility

This package implements the zap and lumberjack-backed `wklog.Logger`, console
presentation filtering, and bounded reads of ordinary node-local application
logs.
It does not define business events or read distributed runtime logs.

## Boundaries

- Business packages depend on `pkg/wklog`; `internal/app` creates, names, and
  synchronizes the concrete logger.
- The reader exposes only `app.log`, `warn.log`, `error.log`, and `debug.log`
  beneath the configured log directory.
- It does not read Controller, Slot, Channel, Raft, arbitrary paths, or logs on
  another node.

## Main Flows

1. Logger construction routes structured records by level into rotating files
   and optionally to the console.
2. Console-only lifecycle exclusions let the app render selected startup events
   while preserving their file records and structured fields.
3. `AppLogReader` performs bounded initial, forward, and contextual reads using
   opaque cursors and capped raw lines.

## Invariants and Failure Semantics

- ANSI colors are enabled only for an interactive terminal and are disabled by
  `NO_COLOR` or `TERM=dumb`.
- Console filtering never changes file routing or record fields.
- Reader input cannot select a filesystem path; scans and returned lines have
  independent bounds.
- Context reads return exact before/after lines from the same selected file.

## Read First

- [Logger construction](zap.go)
- [Log configuration](config.go)
- [Console filtering](console_filter.go)
- [Application log reader](app_reader.go)

## Update Triggers

Update this file when log routing, rotation, console presentation, sync, fixed
sources, cursor behavior, or read bounds change.
