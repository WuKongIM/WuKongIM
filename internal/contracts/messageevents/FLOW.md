# internal/contracts/messageevents Flow

## Responsibility

`internal/contracts/messageevents` contains lightweight event DTOs shared
between usecases and runtimes. Phase 1 defines only committed-message events for
later delivery, conversation, and replay migration. Committed-message events
carry the server append timestamp used by conversation ordering.

The package must stay dependency-light and must not import access, app, gateway,
or cluster packages.
