# Cluster-Only Semantics Design

## Summary

WuKongIM should no longer carry a real "single-machine" execution concept.
From this point forward, a deployment with one process is still a distributed
node running as a `单节点集群`, not a separate local-only mode.

The current bootstrap path already enforces cluster configuration in
`internal/app`, but `internal/usecase/message` still contains a local fallback:
when no `ChannelCluster` is injected, sends allocate message IDs and channel
sequences locally and return node-local delivery results. That fallback keeps a
true single-machine semantic alive inside the message use case.

This design removes that fallback, makes cluster-backed durable send the only
write path, and standardizes repository terminology on `单节点集群`.

## Goals

- Remove the remaining true single-machine runtime semantic from message send.
- Treat one-node deployment as `单节点集群`, not a separate mode.
- Keep `pkg/storage/channellog` as the single source of truth for `MessageID`,
  `MessageSeq`, channel epoch, and leader epoch.
- Align tests, comments, and design docs with the `单节点集群` term.

## Non-Goals

- No new bootstrap architecture. `internal/app` remains the single composition
  root.
- No configuration schema rewrite. Existing `cluster.nodes` and
  `cluster.groups` stay as they are.
- No broad rename of every `local` identifier in the codebase. Node-local
  runtime concepts such as local fanout, local registry, or local storage stay
  named as local when they describe node-internal behavior rather than a
  deployment mode.
- No wider refactor of unrelated tests or packages beyond the touched semantic
  boundary.

## Current State

### What already matches the target

- `internal/app/config.go` already requires `cluster.listenAddr`,
  `cluster.nodes`, and `cluster.groups`.
- `internal/app/build.go` always constructs `raftcluster.Cluster`,
  `isrnode.Runtime`, and `channellog.Cluster`.
- `cmd/wukongim/config.go` already maps config into a cluster-first app config.

### What still carries the old concept

- `internal/usecase/message/app.go` accepts `ClusterPort` as an optional
  dependency and silently stores `nil` when the injected value does not
  implement `ChannelCluster`.
- `internal/usecase/message/send.go` branches on `a.cluster != nil`. Without a
  cluster it:
  - allocates `MessageID` through `internal/runtime/sequence`
  - allocates `MessageSeq` locally
  - returns `ReasonUserNotOnNode` when the target user is offline on the local
    node
  - returns `ReasonSystemError` when node-local delivery fails

That branch is the remaining real single-machine mode and must be removed.

## Design

### 1. Runtime semantics become cluster-only

`internal/usecase/message` must require a cluster-backed message write port.
Person-channel send will always execute the durable `channellog.Send` path and
will never use node-local sequence allocation as an authority.

Operationally this means:

- one process is modeled as a `单节点集群`
- startup still requires a normal cluster config whose node set and peer set may
  both contain only the local node
- `MessageID` and `MessageSeq` always come from the durable channel log path
- channel epoch and leader epoch validation always follows the durable write
  path

### 2. `message.App` uses an explicit cluster dependency contract

The message use case must stop treating cluster support as an implicit optional
dependency.

The concrete design decision is:

- replace the current `ClusterPort any` option with a typed `ChannelCluster`
  dependency in `message.Options`
- remove the silent type assertion path that currently converts a wrong value
  into `nil`
- add an explicit message-layer error for missing cluster support and return it
  deterministically when send is invoked without a configured cluster
- keep `internal/app/build.go` injecting `app.channelLog` as before

This keeps the change tightly scoped to the message boundary. Missing cluster
support becomes an explicit misconfiguration error, not a different runtime
mode.

### 3. Remove local write fallback from message send

Delete the node-local send pathway from `internal/usecase/message/send.go`:

- remove `sendLocalPerson`
- remove node-local sequence and message ID allocation from message send
- keep only the durable person-channel path followed by best-effort local fanout

The retained behavior is:

- unauthenticated send still returns `ErrUnauthenticatedSender`
- unsupported channel type still returns `ReasonNotSupportChannelType`
- durable write success returns `ReasonSuccess`
- post-commit local fanout remains best-effort and does not change the durable
  ack

### 4. Behavior changes that must be explicit

Removing the local fallback changes observable behavior. These changes are
intentional and must be locked by tests and documented in code comments where
needed.

#### Recipient offline on the current node

Old fallback behavior:

- if recipient had no local connection, send returned
  `ReasonUserNotOnNode`

New cluster-only behavior:

- recipient local presence does not decide send success
- if durable write succeeds, send returns `ReasonSuccess`
- local fanout may still be a no-op when there is no local recipient session

#### Local delivery failure after durable commit

Old fallback behavior:

- local delivery failure returned `ReasonSystemError`

New cluster-only behavior:

- durable commit remains authoritative
- post-commit local delivery failure is best-effort only and does not change the
  durable send result

This preserves the existing durable-path semantics and removes the old
single-machine semantics rather than trying to merge the two.

### 5. Remove now-unused message-side sequence dependency

Once local send is removed, `internal/usecase/message` no longer needs
`internal/runtime/sequence` as a dependency for send.

The implementation should therefore:

- remove the sequence allocator from `message.Options` if it becomes unused
- remove the corresponding app wiring from `internal/app/build.go`
- update tests that currently observe local sequence allocation behavior

This is a cleanup required by the semantic change, not an unrelated refactor.

### 6. Repository terminology standard

When the repository refers to deployment topology, use `单节点集群` as the
canonical term.

Apply this to:

- test names
- test comments
- design/spec/plan text
- package comments or inline comments that describe deployment mode

Do not rewrite phrases that describe node-internal capacity or node-local
behavior. For example, phrases like "在单节点内管理大量 ISR 组" or "local fanout"
are still correct when they describe internals rather than a separate runtime
mode.

## Affected Areas

### Production code

- `internal/usecase/message/app.go`
- `internal/usecase/message/deps.go`
- `internal/usecase/message/send.go`
- `internal/app/build.go`

### Tests

- `internal/usecase/message/send_test.go`
- `internal/access/gateway/handler_test.go`
- `internal/access/gateway/integration_test.go`
- `internal/access/api/integration_test.go`
- any affected app/config tests if constructor or wiring changes require updates

### Documentation and terminology

At minimum, update documents and comments that directly imply a real
single-machine mode, including the known design-doc references that discuss
future `single-node mode` or deterministic `single-node` setups as if they were
separate runtime semantics.

## Testing Strategy

### Message use case tests

Add or update tests to prove:

- invoking message send without a valid cluster dependency fails with the new
  explicit misconfiguration error
- person-channel send always uses the durable cluster path
- recipient offline on the local node no longer returns `ReasonUserNotOnNode`
- local delivery failure after durable commit no longer changes the successful
  durable result
- stale-meta refresh flow still works unchanged

### App wiring tests

Keep or add focused coverage showing:

- `internal/app/build.go` still provides the required cluster-backed dependency
  chain to message use case construction
- send-oriented access tests that previously relied on `message.New` without a
  cluster are updated to inject a cluster-backed or fake cluster-backed message
  app explicitly

### Config tests

Keep current cluster-first config expectations intact. No new non-cluster config
  pathway should be introduced.

### Documentation checks

Search for deployment-mode wording and update the touched references from
`single-node` or `单机` semantics to `单节点集群` where appropriate.

## Risks and Mitigations

### Risk: hidden tests or callers rely on local-only send behavior

Mitigation:

- fail fast at construction or invocation rather than silently keeping the old
  behavior
- update affected tests to assert the new cluster-only contract

### Risk: terminology update becomes an unrelated repo-wide rename

Mitigation:

- only rewrite wording that describes deployment mode
- leave node-local/internal wording intact

### Risk: behavior change for offline recipients is missed

Mitigation:

- add explicit tests for offline recipient success under durable send
- call out the behavior change in the implementation plan

## Result

After this change, WuKongIM will have one runtime model:

- distributed node
- one or more nodes per cluster
- one-node deployment expressed as `单节点集群`

There will no longer be a true single-machine message send semantic hiding
behind an optional dependency in `internal/usecase/message`.
