# internalv2/runtime/channelwrite Flow

## Responsibility

`internalv2/runtime/channelwrite` owns local channel-authority write admission.
It is the in-memory reactor group entered only after routing has resolved that
the local node is the current channel authority.

## Authority-Only State

`SubmitLocal` accepts an `AuthorityTarget` that carries the channel authority
identity. The group rejects targets whose `LeaderNodeID` does not match
`Options.LocalNodeID` before choosing a reactor. Remote channels must be
forwarded by a later router/RPC layer and never create proxy `channelState`
inside this package.

`channelState` is created lazily by the owning reactor after local authority
validation. State is keyed by the channel routing key derived from
`AuthorityTarget.ChannelKey` or from `ChannelID` when the target omits an
explicit key. Reactor selection uses the same channel key, not the sender UID.

## Lifecycle

`Group.Start` starts all reactors and opens local admission. `Group.Stop` closes
admission, closes reactor mailboxes, and waits for already accepted mailbox
events to drain until the caller context expires. A stopped group is not
restarted because reactor mailboxes are closed as part of shutdown.

## Current Skeleton Scope

This task implements only local authority validation, reactor routing,
lifecycle, lazy state creation, and item-aligned futures. Submission checks
caller cancellation before mailbox enqueue; once a submit event is accepted into
a reactor mailbox, caller cancellation no longer turns the accepted event into a
rejected submit. The group lock is held only through closed-state validation,
reactor selection, and mailbox enqueue, then released while waiting for reactor
admission ack so `Stop` can close admission promptly. The skeleton completes the
returned future with item-aligned `ErrNotAppended` results after admission,
making the absence of durable append explicit without reporting fake append
success. Prepare/validation effects, durable append, post-commit fanout,
recipient delivery, router/RPC forwarding, and flow-control enforcement are
later tasks.
