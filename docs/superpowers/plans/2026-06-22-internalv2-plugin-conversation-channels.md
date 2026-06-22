# internalv2 Plugin Conversation Channels Plan

## Goal

Support `/conversation/channels` in the internalv2 plugin host RPC surface with
legacy-compatible behavior and lightweight active-row reads.

## Tasks

### Task 1: Usecase ConversationChannels

- Add RED tests in `internalv2/usecase/plugin` for mapping, missing reader,
  missing UID, reader error propagation, and order preservation.
- Add `ConversationReader` and `Options.Conversations`.
- Implement `App.ConversationChannels`.
- Add protocol response mapping from `[]message.ChannelID`.

### Task 2: Access Host RPC Route

- Add RED tests for route registration, request decode, timeout context, and
  response encoding.
- Extend the plugin access `Usecase` interface.
- Register `/conversation/channels`.
- Implement the handler.

### Task 3: Cluster Infra And App Wiring

- Add RED infra tests for active-row mapping, normal-kind lookup, limit
  propagation, invalid channel types, and error propagation.
- Add app wiring test proving the plugin usecase can read conversation channels
  from a clusterv2-compatible cluster.
- Implement `PluginConversationReader`.
- Wire it from `internalv2/app` when the cluster exposes active conversation
  reads.

### Task 4: Benchmarks And Flow Docs

- Add usecase and access benchmarks for `/conversation/channels`.
- Update `internalv2/usecase/plugin/FLOW.md`.
- Update `internalv2/app/FLOW.md`.

### Task 5: Verification And Commit

- Run focused unit tests.
- Run plugin benchmarks with `-benchmem`.
- Run `git diff --check`.
- Commit all changes.
