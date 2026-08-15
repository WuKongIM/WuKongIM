package replication

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// ExchangeVersion is the only supported data-bearing peer protocol version.
const ExchangeVersion uint16 = 1

// ExchangeKind identifies one bounded peer operation.
type ExchangeKind uint8

const (
	// ExchangeReplicate carries an immutable proposal to one follower.
	ExchangeReplicate ExchangeKind = iota + 1
)

// ReplicateStatus is the closed durable result of one follower replication.
type ReplicateStatus uint8

const (
	ReplicateDurable ReplicateStatus = iota + 1
	ReplicateAlreadyDurable
	ReplicateNeedFrom
	ReplicateStaleFence
	ReplicateConflict
	ReplicateBackpressured
	ReplicateOutcomeUnknown
)

// Valid reports whether status is one closed peer result.
func (s ReplicateStatus) Valid() bool {
	return s >= ReplicateDurable && s <= ReplicateOutcomeUnknown
}

// Durable reports whether the follower proved the requested immutable range.
func (s ReplicateStatus) Durable() bool {
	return s == ReplicateDurable || s == ReplicateAlreadyDurable
}

// ReplicateRequest carries records and their immutable manifest to one voter.
type ReplicateRequest struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Leader     ch.NodeID
	Follower   ch.NodeID
	Manifest   ch.ProposalManifest
	Records    []ch.Record
	// Committed is the leader's current committed frontier, never above this proposal.
	Committed uint64
}

// ReplicateProof is the follower's exact durable identity echo. A durable
// vote is valid only when every field equals the request that produced it.
type ReplicateProof struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Leader     ch.NodeID
	Follower   ch.NodeID
	Manifest   ch.ProposalManifest
}

func replicateProofFor(request ReplicateRequest) ReplicateProof {
	return ReplicateProof{
		ChannelKey: request.ChannelKey,
		ChannelID:  request.ChannelID,
		Leader:     request.Leader,
		Follower:   request.Follower,
		Manifest:   request.Manifest,
	}
}

// Valid reports whether the request carries one complete immutable proposal.
func (r ReplicateRequest) Valid() bool {
	if r.ChannelKey == "" || r.ChannelID.ID == "" || r.Leader == 0 || r.Follower == 0 || r.Leader == r.Follower ||
		!r.Manifest.ValidFor(r.Manifest.BaseOffset, len(r.Records)) || r.Committed > r.Manifest.LastOffset {
		return false
	}
	entries, ok := ch.DeriveProposalEntries(r.Manifest, len(r.Records), func(index int) ch.Record { return r.Records[index] })
	return ok && len(entries) == len(r.Records) && entries[len(entries)-1].Digest == r.Manifest.Digest
}

// ReplicateResult is the follower's durable response for one request.
type ReplicateResult struct {
	Status     ReplicateStatus
	LastOffset uint64
	// Proof is mandatory for durable statuses and zero for every other status.
	Proof ReplicateProof
	// NeedFrom is the follower's exact next offset when Status is ReplicateNeedFrom.
	NeedFrom uint64
}

// ExchangeItem is one correlated request in a peer batch.
type ExchangeItem struct {
	RequestID uint64
	Kind      ExchangeKind
	Replicate *ReplicateRequest
}

// ExchangeBatch carries ready work for one target without another collection timer.
type ExchangeBatch struct {
	Version uint16
	Items   []ExchangeItem
}

// ExchangeItemResult correlates one peer result to its request identity.
type ExchangeItemResult struct {
	RequestID uint64
	Replicate ReplicateResult
}

// ExchangeBatchResult returns exactly one correlated result per input item.
type ExchangeBatchResult struct {
	Version uint16
	Items   []ExchangeItemResult
}

// PeerLink sends one bounded batch to a repository-owned peer node.
type PeerLink interface {
	Exchange(context.Context, ch.NodeID, ExchangeBatch) (ExchangeBatchResult, error)
}

func estimateReplicateRequestBytes(request ReplicateRequest) int {
	const fixedBytes = 256
	total := fixedBytes + len(request.ChannelKey) + len(request.ChannelID.ID)
	for _, record := range request.Records {
		total += 96 + len(record.FromUID) + len(record.ClientMsgNo) + len(record.Payload)
	}
	return total
}
