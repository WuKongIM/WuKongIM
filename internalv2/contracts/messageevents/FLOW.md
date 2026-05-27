# internalv2/contracts/messageevents Flow

## Responsibility

`internalv2/contracts/messageevents` contains lightweight event DTOs shared
between usecases and runtimes. Phase 1 defines only committed-message events for
later delivery, conversation, and replay migration.

The package must stay dependency-light and must not import access, app, gateway,
or cluster packages.
