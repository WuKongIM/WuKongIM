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

## Current Reactor Scope

The runtime currently implements local authority validation, reactor routing,
lifecycle, pre-append preparation, lazy state creation, and item-aligned
futures. Submission checks caller cancellation before mailbox enqueue; after
that check, bounded mailbox enqueue is non-blocking and returns
`ErrBackpressured` when full. Once a submit event is accepted into a reactor
mailbox, caller cancellation no longer turns the accepted event into a rejected
submit. The group lock is held only through closed-state validation, reactor
selection, and mailbox enqueue, then released while waiting for reactor
admission ack so `Stop` can close admission promptly.

Accepted submit events dispatch preparation to bounded effect workers. The
reactor loop applies prepare completion events, and only those completion
events mutate `channelState`. Rejected and idempotent items complete their
item-aligned future slots immediately with their reason/error/result. Valid
prepared items receive one message id and one server timestamp, then enter the
owning channel state's pending queue in input order. Until durable append is
implemented, those valid prepared future slots complete with item-level
`ErrNotAppended`, making the absence of append explicit without reporting fake
durable success. Durable append, post-commit fanout, recipient delivery,
router/RPC forwarding, and flow-control enforcement are later tasks.
