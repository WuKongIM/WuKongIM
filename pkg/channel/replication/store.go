package replication

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// Mutation is one synchronous exact follower write. Manifest is mandatory;
// there is no caller-selectable durability or legacy append mode.
type Mutation struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Manifest   ch.ProposalManifest
	Records    []ch.Record
	Committed  uint64
}

// MutationResult is the closed per-item result of ReplicaStore.Sync.
type MutationResult struct {
	Outcome    ch.AppendOutcome
	LastOffset uint64
	// NeedFrom is the exact next offset when the follower has a gap.
	NeedFrom uint64
	Err      error
}

// ReplicaStore synchronously applies exact immutable mutations. Returning from
// Sync with a durable outcome proves physical durability for that item.
type ReplicaStore interface {
	Sync(context.Context, []Mutation) []MutationResult
}
